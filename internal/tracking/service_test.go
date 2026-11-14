package tracking

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/notify"
)

type fakeSkeleton struct {
	eps    []Episode
	status string
	err    error
	calls  []int64
	mu     sync.Mutex
}

func (f *fakeSkeleton) Episodes(_ context.Context, t Title) ([]Episode, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, t.ID)
	return f.eps, f.status, f.err
}

func (f *fakeSkeleton) called() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.calls...)
}

type fakeScanner struct {
	kind string
	srcs []Source
	err  error
}

func (f *fakeScanner) Kind() string { return f.kind }
func (f *fakeScanner) Scan(context.Context, Title, []Episode) ([]Source, error) {
	return f.srcs, f.err
}

type fakeNotifier struct{ got []notify.Event }

func (f *fakeNotifier) Enqueue(_ context.Context, e notify.Event) (bool, error) {
	f.got = append(f.got, e)
	return true, nil
}

var tnow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T, sk Skeleton, sc ...Scanner) (*Service, *fakeNotifier, *bytes.Buffer) {
	t.Helper()
	st := newStore(t)
	n := &fakeNotifier{}
	var logs bytes.Buffer
	return &Service{Store: st, Skeleton: sk, Scanners: sc, Notify: n, Interest: st.Interested,
		Now: func() time.Time { return tnow }, Log: slog.New(slog.NewTextHandler(&logs, nil))}, n, &logs
}

func watched(t *testing.T, s *Service, users ...string) (Title, []int64) {
	t.Helper()
	ctx := context.Background()
	ti, err := s.Store.EnsureTitle(ctx, "series", 42, "Show", "/p.jpg")
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, u := range users {
		id := addUser(t, s.Store, u)
		s.Store.SetMark(ctx, Mark{UserID: id, TitleID: ti.ID, State: Watching})
		ids = append(ids, id)
	}
	return ti, ids
}

func TestRefreshStoresSkeletonAndSources(t *testing.T) {
	sk := &fakeSkeleton{status: "airing", eps: []Episode{
		{Season: 1, Number: 1, Name: "One", AirDate: "2026-03-01"},
		{Season: 1, Number: 2, Name: "Two", AirDate: "2026-03-08"},
	}}
	lib := &fakeScanner{kind: "library", srcs: []Source{{Season: 1, Episode: 1, Ref: "7"}}}
	s, _, _ := newService(t, sk, lib)
	ti, _ := watched(t, s, "one")
	ctx := context.Background()
	var hooked []Source
	s.OnAdded = append(s.OnAdded, func(_ context.Context, got Title, added []Source) error {
		if got.ID != ti.ID {
			t.Errorf("hook title %+v", got)
		}
		hooked = append(hooked, added...)
		return nil
	})
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	s.Store.DB.QueryRow(`SELECT count(*) FROM title_episodes WHERE title_id=?`, ti.ID).Scan(&n)
	if n != 2 {
		t.Fatalf("episodes=%d", n)
	}
	src, _ := s.Store.Sources(ctx, ti.ID)
	if len(src) != 1 || src[0].Kind != "library" || src[0].Ref != "7" {
		t.Fatalf("sources %+v", src)
	}
	if len(hooked) != 1 || hooked[0].Kind != "library" {
		t.Fatalf("hooked %+v", hooked)
	}
	got, _, _ := s.Store.TitleByID(ctx, ti.ID)
	if got.Status != "airing" || !got.Refreshed.Equal(tnow) || !got.NextCheck.Equal(tnow.Add(6*time.Hour)) {
		t.Fatalf("title %+v", got)
	}
}

func TestNewEpisodeNotifiesInterestedProfiles(t *testing.T) {
	sk := &fakeSkeleton{status: "airing", eps: []Episode{
		{Season: 1, Number: 7, AirDate: "2026-03-01"},
		{Season: 1, Number: 8, Name: "Eight", AirDate: "2026-03-08"},
	}}
	tor := &fakeScanner{kind: "torrent", srcs: []Source{{Season: 1, Episode: 7, Ref: "a"}}}
	onl := &fakeScanner{kind: "online"}
	s, n, _ := newService(t, sk, tor, onl)
	ti, users := watched(t, s, "one", "two")
	dropped := addUser(t, s.Store, "three")
	ctx := context.Background()
	s.Store.SetMark(ctx, Mark{UserID: dropped, TitleID: ti.ID, State: Dropped})

	// the first refresh is the baseline: what is already there tells nobody
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	if len(n.got) != 0 {
		t.Fatalf("baseline notified: %+v", n.got)
	}
	tor.srcs = append(tor.srcs, Source{Season: 1, Episode: 8, Ref: "b"})
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	if len(n.got) != 2 {
		t.Fatalf("events %+v", n.got)
	}
	for i, e := range n.got {
		if e.UserID != users[i] || e.Topic != notify.Episodes || e.Key != "trk:"+strconv.FormatInt(ti.ID, 10)+":S01E08" ||
			e.Data.Title != "Show" || e.Data.Season != 1 || e.Data.Episode != 8 || e.Data.EpisodeTitle != "Eight" ||
			e.Data.Poster != "https://image.tmdb.org/t/p/w342/p.jpg" {
			t.Fatalf("event %d: %+v", i, e)
		}
	}
	// another source of the same episode (other kind): no second letter
	onl.srcs = []Source{{Season: 1, Episode: 8, Ref: "x"}}
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	if len(n.got) != 2 {
		t.Fatalf("second source notified: %+v", n.got[2:])
	}
}

func TestScannerErrorKeepsOldRows(t *testing.T) {
	sk := &fakeSkeleton{status: "airing", eps: []Episode{{Season: 1, Number: 1}, {Season: 1, Number: 2}}}
	tor := &fakeScanner{kind: "torrent", srcs: []Source{{Season: 1, Episode: 1, Ref: "a"}}}
	onl := &fakeScanner{kind: "online", srcs: []Source{{Season: 1, Episode: 1, Ref: "o"}}}
	s, _, logs := newService(t, sk, tor, onl)
	ti, _ := watched(t, s, "one")
	ctx := context.Background()
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	tor.srcs, tor.err = nil, errors.New("tracker down")
	onl.srcs = []Source{{Season: 1, Episode: 2, Ref: "o2"}}
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	src, _ := s.Store.Sources(ctx, ti.ID)
	var refs []string
	for _, x := range src {
		refs = append(refs, x.Kind+":"+x.Ref)
	}
	if strings.Join(refs, ",") != "torrent:a,online:o2" {
		t.Fatalf("sources %v", refs)
	}
	if !strings.Contains(logs.String(), "tracker down") {
		t.Fatalf("not logged: %s", logs.String())
	}
}

func TestNextCheckCadence(t *testing.T) {
	now := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		status string
		eps    []Episode
		want   time.Duration
	}{
		{"airing today", "airing", []Episode{{AirDate: "2026-03-03"}, {AirDate: "2026-03-10"}}, 2 * time.Hour},
		{"airing yesterday", "airing", []Episode{{AirDate: "2026-03-09"}, {AirDate: "2026-03-16"}}, 2 * time.Hour},
		{"airing otherwise", "airing", []Episode{{AirDate: "2026-03-05"}, {AirDate: "2026-03-12"}}, 6 * time.Hour},
		{"upcoming", "upcoming", []Episode{{AirDate: "2026-05-01"}}, 24 * time.Hour},
		{"ended", "ended", []Episode{{AirDate: "2026-03-10"}}, 72 * time.Hour},
		{"unknown", "unknown", nil, 72 * time.Hour},
		{"empty", "", nil, 72 * time.Hour},
	}
	for _, c := range cases {
		if got := NextCheck(Title{Status: c.status}, c.eps, now); !got.Equal(now.Add(c.want)) {
			t.Errorf("%s: %v, want +%v", c.name, got, c.want)
		}
	}
}

func TestRunRefreshesOnlyDue(t *testing.T) {
	sk := &fakeSkeleton{status: "ended"}
	s, _, _ := newService(t, sk)
	ctx := context.Background()
	u := addUser(t, s.Store, "one")
	due, _ := s.Store.EnsureTitle(ctx, "series", 1, "Due", "")
	later, _ := s.Store.EnsureTitle(ctx, "series", 2, "Later", "")
	untracked, _ := s.Store.EnsureTitle(ctx, "series", 3, "Untracked", "")
	_ = untracked
	s.Store.SetMark(ctx, Mark{UserID: u, TitleID: due.ID, State: Watching})
	s.Store.SetMark(ctx, Mark{UserID: u, TitleID: later.ID, State: Planned})
	s.Store.SetChecked(ctx, later.ID, "", tnow.Add(time.Hour), tnow)
	s.startAfter = time.Millisecond

	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Run(rctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(sk.called()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// a kicked title is refreshed even when not due
	s.Kick(later.ID)
	for len(sk.called()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	got := sk.called()
	if len(got) != 2 || got[0] != due.ID || got[1] != later.ID {
		t.Fatalf("refreshed %v, want [%d %d]", got, due.ID, later.ID)
	}
}

func TestLibraryOnlyEpisodeNotNotified(t *testing.T) {
	sk := &fakeSkeleton{status: "airing", eps: []Episode{{Season: 1, Number: 1}, {Season: 1, Number: 2}, {Season: 1, Number: 3}}}
	lib := &fakeScanner{kind: "library"}
	tor := &fakeScanner{kind: "torrent"}
	s, n, _ := newService(t, sk, lib, tor)
	ti, _ := watched(t, s, "one")
	ctx := context.Background()
	var hooked []Source
	s.OnAdded = append(s.OnAdded, func(_ context.Context, _ Title, added []Source) error {
		hooked = append(hooked, added...)
		return nil
	})
	if err := s.Refresh(ctx, ti.ID); err != nil { // baseline
		t.Fatal(err)
	}
	// E01: library only (notify.Collect mails it); E02: library and torrent
	lib.srcs = []Source{{Season: 1, Episode: 1, Ref: "7"}, {Season: 1, Episode: 2, Ref: "7"}}
	tor.srcs = []Source{{Season: 1, Episode: 2, Ref: "t"}}
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	if len(n.got) != 1 || n.got[0].Data.Episode != 2 {
		t.Fatalf("events %+v", n.got)
	}
	if len(hooked) != 3 {
		t.Fatalf("hooked %+v", hooked)
	}
	// E01 later appears as a torrent too: it had a source already, no letter
	tor.srcs = append(tor.srcs, Source{Season: 1, Episode: 1, Ref: "t1"})
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	if len(n.got) != 1 {
		t.Fatalf("late torrent notified: %+v", n.got)
	}
}

func episodesOf(n *fakeNotifier) []int {
	var out []int
	for _, e := range n.got {
		out = append(out, e.Data.Episode)
	}
	return out
}

func TestScannerDownAtBaselineStaysSilentLater(t *testing.T) {
	sk := &fakeSkeleton{status: "airing", eps: []Episode{
		{Season: 1, Number: 1, AirDate: "2026-03-01"}, {Season: 1, Number: 2, AirDate: "2026-03-02"},
		{Season: 1, Number: 3, AirDate: "2026-03-03"}, {Season: 1, Number: 4, AirDate: "2026-03-09"},
	}}
	lib := &fakeScanner{kind: "library", srcs: []Source{{Season: 1, Episode: 1, Ref: "7"}}}
	tor := &fakeScanner{kind: "torrent", err: errors.New("tracker down")}
	s, n, _ := newService(t, sk, lib, tor)
	ti, _ := watched(t, s, "one")
	ctx := context.Background()
	if err := s.Refresh(ctx, ti.ID); err != nil { // baseline with the torrents down
		t.Fatal(err)
	}
	// the tracker is back: its first successful scan is its baseline
	tor.err = nil
	tor.srcs = []Source{{Season: 1, Episode: 2, Ref: "a"}, {Season: 1, Episode: 3, Ref: "b"}}
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	if len(n.got) != 0 {
		t.Fatalf("back catalogue mailed: %v", episodesOf(n))
	}
	tor.srcs = append(tor.srcs, Source{Season: 1, Episode: 4, Ref: "c"})
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	if got := episodesOf(n); len(got) != 1 || got[0] != 4 {
		t.Fatalf("events %v, want [4]", got)
	}
}

func TestNewScannerKindBaselinesSilently(t *testing.T) {
	sk := &fakeSkeleton{status: "airing", eps: []Episode{
		{Season: 1, Number: 1, AirDate: "2026-03-01"}, {Season: 1, Number: 5, AirDate: "2026-03-05"},
		{Season: 1, Number: 6, AirDate: "2026-03-09"},
	}}
	tor := &fakeScanner{kind: "torrent", srcs: []Source{{Season: 1, Episode: 1, Ref: "a"}}}
	s, n, _ := newService(t, sk, tor)
	ti, _ := watched(t, s, "one")
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := s.Refresh(ctx, ti.ID); err != nil {
			t.Fatal(err)
		}
	}
	// a kind added later (a new scanner) baselines on its first scan
	onl := &fakeScanner{kind: "online", srcs: []Source{{Season: 1, Episode: 5, Ref: "o5"}}}
	s.Scanners = append(s.Scanners, onl)
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	if len(n.got) != 0 {
		t.Fatalf("new kind mailed its baseline: %v", episodesOf(n))
	}
	onl.srcs = append(onl.srcs, Source{Season: 1, Episode: 6, Ref: "o6"})
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	if got := episodesOf(n); len(got) != 1 || got[0] != 6 {
		t.Fatalf("events %v, want [6]", got)
	}
}

func TestOldEpisodeNeverMailed(t *testing.T) {
	sk := &fakeSkeleton{status: "airing", eps: []Episode{
		{Season: 1, Number: 1, AirDate: "2026-01-01"}, // 68 days before tnow
		{Season: 1, Number: 2, AirDate: "2026-02-08"}, // 30 days: still mailed
		{Season: 1, Number: 3, AirDate: "2026-03-05"},
		{Season: 1, Number: 4}, // no air date
	}}
	tor := &fakeScanner{kind: "torrent"}
	s, n, _ := newService(t, sk, tor)
	ti, _ := watched(t, s, "one")
	ctx := context.Background()
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	tor.srcs = []Source{{Season: 1, Episode: 1, Ref: "a"}, {Season: 1, Episode: 2, Ref: "b"},
		{Season: 1, Episode: 3, Ref: "c"}, {Season: 1, Episode: 4, Ref: "d"}}
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	if got := episodesOf(n); len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 4 {
		t.Fatalf("events %v, want [2 3 4]", got)
	}
}

func TestScannerUnchangedKeepsRowsAndLastSeen(t *testing.T) {
	sk := &fakeSkeleton{status: "airing", eps: []Episode{{Season: 1, Number: 1}}}
	tor := &fakeScanner{kind: "torrent", srcs: []Source{{Season: 1, Episode: 1, Ref: "a"}}}
	s, _, logs := newService(t, sk, tor)
	ti, _ := watched(t, s, "one")
	ctx := context.Background()
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	first := tnow
	s.Now = func() time.Time { return first.Add(time.Hour) }
	tor.srcs, tor.err = nil, ErrUnchanged
	if err := s.Refresh(ctx, ti.ID); err != nil {
		t.Fatal(err)
	}
	src, _ := s.Store.Sources(ctx, ti.ID)
	if len(src) != 1 || src[0].Ref != "a" || !src[0].LastSeen.Equal(first) {
		t.Fatalf("sources %+v", src)
	}
	if logs.Len() != 0 {
		t.Fatalf("logged: %s", logs.String())
	}
}

// Each title's letters are listed under that title (a group of its own that
// never meets a library series id) with a link to its page; a film is sent
// as «появилось», not as a new episode.
func TestNotifyGroupsByTitleAndSendsFilmsAsAvailable(t *testing.T) {
	sk := &fakeSkeleton{status: "airing", eps: []Episode{{Season: 1, Number: 1, AirDate: "2026-03-01"}}}
	tor := &fakeScanner{kind: "torrent"}
	s, n, _ := newService(t, sk, tor)
	s.TitlePath = func(t Title) string { return "/title/" + t.Kind + "/" + strconv.FormatInt(t.TMDBID, 10) }
	ctx := context.Background()
	show, _ := watched(t, s, "one")
	film, err := s.Store.EnsureTitle(ctx, "movie", 77, "Film", "/f.jpg")
	if err != nil {
		t.Fatal(err)
	}
	uid := addUser(t, s.Store, "two")
	s.Store.SetMark(ctx, Mark{UserID: uid, TitleID: film.ID, State: Planned})
	for _, id := range []int64{show.ID, film.ID} {
		if err := s.Refresh(ctx, id); err != nil { // baselines
			t.Fatal(err)
		}
	}
	tor.srcs = []Source{{Season: 1, Episode: 1, Ref: "a"}}
	if err := s.Refresh(ctx, show.ID); err != nil {
		t.Fatal(err)
	}
	sk.eps = []Episode{{Season: 0, Number: 0, AirDate: "2026-03-01"}}
	tor.srcs = []Source{{Season: 0, Episode: 0, Ref: "f"}}
	if err := s.Refresh(ctx, film.ID); err != nil {
		t.Fatal(err)
	}
	if len(n.got) != 2 {
		t.Fatalf("events %+v", n.got)
	}
	ep, mv := n.got[0], n.got[1]
	if ep.Topic != notify.Episodes || ep.GroupID != -show.ID || ep.ItemID != 0 || ep.Data.Path != "/title/series/42" {
		t.Fatalf("episode event %+v", ep)
	}
	if mv.Topic != notify.Available || mv.GroupID != -film.ID || mv.Data.Kind != "movie" ||
		mv.Data.Path != "/title/movie/77" || mv.Data.Title != "Film" || mv.UserID != uid {
		t.Fatalf("film event %+v", mv)
	}
}
