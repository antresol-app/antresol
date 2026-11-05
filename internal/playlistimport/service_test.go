package playlistimport

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

type fakeMatcher struct {
	mu   sync.Mutex
	keys []string
}

func (m *fakeMatcher) Match(_ context.Context, t Track) (Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys = append(m.keys, t.Key)
	return Decision{State: TrackMatched, RecordingID: "rec-" + t.Key, ReleaseGroup: "rg-1", Title: t.Title, Artist: t.Artist,
		Album: "Группа крови", Year: 1988}, nil
}

func (m *fakeMatcher) seen() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.keys)
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(until) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestResumeAfterRestart(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	snap := Snapshot{Service: "spotify"}
	for i := range 5 {
		snap.Tracks = append(snap.Tracks, SnapTrack{Key: "k" + strconv.Itoa(i), Artist: "Кино", Title: "Песня " + strconv.Itoa(i), Liked: true})
	}
	id, err := st.CreateJob(ctx, uid, snap)
	if err != nil {
		t.Fatal(err)
	}
	// the server stopped mid-matching: 2 matched, 3 pending, a group searching
	mustExec(t, st.DB, `UPDATE import_jobs SET status = 'matching' WHERE id = ?`, id)
	mustExec(t, st.DB, `INSERT INTO import_groups(job_id, release_group, artist, album, state, attempts, updated_at)
		VALUES (?, 'rg-0', 'Кино', 'Начальник Камчатки', 'searching', 1, '2026-10-26T11:00:00Z')`, id)
	mustExec(t, st.DB, `UPDATE import_tracks SET state = 'matched', mb_release_group = 'rg-0' WHERE job_id = ? AND key IN ('k0', 'k1')`, id)

	m := &fakeMatcher{}
	svc := &Service{Store: st, Matcher: m}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(rctx) }()
	defer func() { cancel(); <-done }()

	waitUntil(t, "matching to finish", func() bool {
		j, err := st.Job(ctx, id)
		return err == nil && j.Status == StatusDownloading
	})
	got := m.seen()
	slices.Sort(got)
	if !slices.Equal(got, []string{"k2", "k3", "k4"}) {
		t.Fatalf("matched again: %v (want only the 3 pending)", got)
	}
	var state string
	var attempts int
	if err := st.DB.QueryRow(`SELECT state, attempts FROM import_groups WHERE job_id = ? AND release_group = 'rg-0'`, id).
		Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != GroupWaiting || attempts != 1 {
		t.Fatalf("group %s attempts %d (want waiting, 1)", state, attempts)
	}
	p, err := st.Progress(ctx, id)
	if err != nil || p.Total != 5 || p.Matched != 5 {
		t.Fatalf("progress %+v %v", p, err)
	}
	// a waiting group keeps the job downloading
	time.Sleep(50 * time.Millisecond)
	if j, _ := st.Job(ctx, id); j.Status != StatusDownloading {
		t.Fatalf("status %s", j.Status)
	}
}

// A cancel that lands while the worker rebuilds playlists (between its
// check that nothing is left and its move to done) wins.
func TestCancelDuringRebuildWins(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	id, err := st.CreateJob(ctx, uid, Snapshot{Service: "file", Tracks: []SnapTrack{{Key: "a", Artist: "Кино", Title: "Кукушка", Liked: true}}})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, st.DB, `UPDATE import_jobs SET status = 'downloading' WHERE id = ?`, id)
	mustExec(t, st.DB, `UPDATE import_tracks SET state = 'done' WHERE job_id = ?`, id)
	var svc *Service
	rebuilt := make(chan struct{}, 1)
	svc = &Service{Store: st, Tick: 10 * time.Millisecond, Rebuild: func(ctx context.Context, jobID int64) error {
		if err := svc.Cancel(ctx, jobID); err != nil {
			t.Error(err)
		}
		select {
		case rebuilt <- struct{}{}:
		default:
		}
		return nil
	}}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(rctx) }()
	<-rebuilt
	time.Sleep(50 * time.Millisecond) // a few more ticks
	cancel()
	<-done
	if j, _ := st.Job(ctx, id); j.Status != StatusCancelled {
		t.Fatalf("status %s (want cancelled)", j.Status)
	}
}

// idleDownloader downloads nothing and settles no album.
type idleDownloader struct{}

func (idleDownloader) Step(context.Context, int64) error { return nil }

// After an upgrade of the matcher the songs of a running job that older
// rules sent to review are matched once more, a few per tick: a match
// joins its album (queued for download), the others stay in review. A
// song the admin skipped is never asked (a skip is final), nor one the
// current rules decided.
func TestRematchAfterMatcherUpgrade(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	snap := Snapshot{Service: "file"}
	for _, k := range []string{"bi2", "skipped", "fresh", "absent", "alisa"} {
		snap.Tracks = append(snap.Tracks, SnapTrack{Key: k, Artist: "Bi-2", Title: k, Liked: true})
	}
	id, err := st.CreateJob(ctx, uid, snap)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, st.DB, `UPDATE import_jobs SET status = 'downloading' WHERE id = ?`, id)
	mustExec(t, st.DB, `UPDATE import_tracks SET state = 'review', reason = ? WHERE job_id = ? AND key != 'skipped'`, ReasonNotInMB, id)
	mustExec(t, st.DB, `UPDATE import_tracks SET state = 'skipped', reason = ? WHERE job_id = ? AND key = 'skipped'`, ReasonNotInMB, id)
	mustExec(t, st.DB, `UPDATE import_tracks SET match_ver = ? WHERE job_id = ? AND key = 'fresh'`, MatcherVersion, id)
	var mu sync.Mutex
	var asked []string
	// the albums stay queued: this test is about matching, not downloading
	svc := &Service{Store: st, Queue: idleDownloader{}, Matcher: matcherFunc(func(_ context.Context, tr Track) (Decision, error) {
		mu.Lock()
		asked = append(asked, tr.Key)
		mu.Unlock()
		switch tr.Key {
		case "alisa": // the admin skips it while it is being matched: the skip wins
			mustExec(t, st.DB, `UPDATE import_tracks SET state = 'skipped' WHERE id = ?`, tr.ID)
			fallthrough
		case "bi2", "skipped":
			return Decision{State: TrackMatched, RecordingID: "rec-" + tr.Key, ReleaseGroup: "rg-bi2", Title: tr.Title, Artist: "Би-2",
				Album: "Би-2", Year: 2000}, nil
		}
		return review(ReasonNoExact), nil
	})}
	for range 5 {
		svc.step(ctx)
	}
	if !slices.Equal(asked, []string{"bi2", "absent", "alisa"}) {
		t.Fatalf("asked %v", asked)
	}
	state := func(key string) (string, string, int64) {
		var s, r string
		var g int64
		if err := st.DB.QueryRow(`SELECT state, reason, COALESCE(group_id, 0) FROM import_tracks WHERE job_id = ? AND key = ?`, id, key).
			Scan(&s, &r, &g); err != nil {
			t.Fatal(err)
		}
		return s, r, g
	}
	for key, want := range map[string]string{"bi2": TrackMatched, "skipped": TrackSkipped, "alisa": TrackSkipped,
		"absent": TrackReview, "fresh": TrackReview} {
		s, r, g := state(key)
		if s != want || (s == TrackMatched) != (g != 0) {
			t.Errorf("%s: %s %q group %d (want %s)", key, s, r, g, want)
		}
	}
	if _, r, _ := state("absent"); r != ReasonNoExact {
		t.Errorf("absent reason %q", r)
	}
	gs, err := st.Groups(ctx, id)
	if err != nil || len(gs) != 1 || gs[0].State != GroupWaiting || gs[0].Songs != 1 {
		t.Fatalf("groups %+v %v", gs, err)
	}
	// nothing is asked twice
	for range 3 {
		svc.step(ctx)
	}
	if len(asked) != 3 {
		t.Fatalf("asked again %v", asked)
	}
}
