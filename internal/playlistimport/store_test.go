package playlistimport

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
)

// newTestStore opens a fresh database with one admin profile.
func newTestStore(t *testing.T) (*Store, int64) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	u, err := auth.NewStore(d).CreateFirstAdmin(context.Background(), "Папа", "secret")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	return &Store{DB: d, Now: func() time.Time { return now }}, u.ID
}

func mustExec(t *testing.T, d *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestCreateJobUpsertsPlaylists(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	first := Snapshot{Service: "spotify", Tracks: []SnapTrack{
		{Key: "a", Artist: "Кино", Title: "Кукушка"},
		{Key: "b", Artist: "Кино", Title: "Звезда по имени Солнце"},
	}, Playlists: []SnapPlaylist{{ID: "p1", Name: "Старое имя", Readable: true, Entries: []string{"a", "b"}}}}
	j1, err := st.CreateJob(ctx, uid, first)
	if err != nil {
		t.Fatal(err)
	}
	// the first import finished; the same library is imported again
	mustExec(t, st.DB, `UPDATE import_jobs SET status = 'done' WHERE id = ?`, j1)
	second := Snapshot{Service: "spotify", Tracks: []SnapTrack{
		{Key: "b", Artist: "Кино", Title: "Звезда по имени Солнце"},
		{Key: "c", Artist: "Кино", Title: "Группа крови"},
	}, Playlists: []SnapPlaylist{{ID: "p1", Name: "Новое имя", Readable: true, Entries: []string{"c", "b", "c"}}}}
	j2, err := st.CreateJob(ctx, uid, second)
	if err != nil {
		t.Fatal(err)
	}
	if j1 == j2 {
		t.Fatal("same job id")
	}
	var n int
	var name string
	var job, plID int64
	if err := st.DB.QueryRow(`SELECT COUNT(*), MAX(name), MAX(job_id), MAX(id) FROM import_playlists`).Scan(&n, &name, &job, &plID); err != nil {
		t.Fatal(err)
	}
	if n != 1 || name != "Новое имя" || job != j2 {
		t.Fatalf("playlists: %d %q job %d (want 1, «Новое имя», %d)", n, name, job, j2)
	}
	rows, err := st.DB.Query(`SELECT t.job_id, t.key FROM import_playlist_entries e JOIN import_tracks t ON t.id = e.track_id
		WHERE e.import_playlist_id = ? ORDER BY e.position`, plID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var jid int64
		var k string
		if err := rows.Scan(&jid, &k); err != nil {
			t.Fatal(err)
		}
		if jid != j2 {
			t.Errorf("entry of job %d", jid)
		}
		keys = append(keys, k)
	}
	if len(keys) != 3 || keys[0] != "c" || keys[1] != "b" || keys[2] != "c" {
		t.Fatalf("entries %v", keys)
	}
	j, err := st.Job(ctx, j2)
	if err != nil || j.Status != StatusPreview || j.Service != "spotify" || j.UserID != uid {
		t.Fatalf("job %+v %v", j, err)
	}
}

func TestNewSnapshotRefusedWhileJobOpen(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	snap := func() Snapshot {
		return Snapshot{Service: "spotify", Tracks: []SnapTrack{
			{Key: "a", Artist: "Кино", Title: "Кукушка"},
			{Key: "b", Artist: "Кино", Title: "Группа крови"},
		}, Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Readable: true, Entries: []string{"a", "b"}}}}
	}
	a, err := st.CreateJob(ctx, uid, snap())
	if err != nil {
		t.Fatal(err)
	}
	// a job in preview already owns the playlists
	if _, err := st.CreateJob(ctx, uid, snap()); !errors.Is(err, ErrBusy) {
		t.Fatalf("second snapshot in preview: %v", err)
	}
	if err := st.StartJob(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateJob(ctx, uid, snap()); !errors.Is(err, ErrBusy) {
		t.Fatalf("second snapshot while matching: %v", err)
	}
	r, err := st.CreateReadingJob(ctx, uid, "spotify")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FillJob(ctx, r, snap()); !errors.Is(err, ErrBusy) {
		t.Fatalf("connector fill while matching: %v", err)
	}
	// the running job kept its playlist songs
	if p, err := st.Progress(ctx, a); err != nil || p.Total != 2 {
		t.Fatalf("running job progress %+v %v", p, err)
	}
	if pls, err := st.Playlists(ctx, a); err != nil || len(pls) != 1 || pls[0].Songs != 2 {
		t.Fatalf("running job playlists %+v %v", pls, err)
	}
	// another service is fine
	other := snap()
	other.Service = "file"
	if _, err := st.CreateJob(ctx, uid, other); err != nil {
		t.Fatalf("other service: %v", err)
	}
	// once cancelled, the service's playlists are free again
	if err := st.CancelJob(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := st.FillJob(ctx, r, snap()); err != nil {
		t.Fatalf("fill after cancel: %v", err)
	}
}

func TestReadingJob(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	if _, err := st.CreateReadingJob(ctx, uid, "napster"); !errors.Is(err, ErrUnknownService) {
		t.Fatalf("napster: %v", err)
	}
	id, err := st.CreateReadingJob(ctx, uid, "deezer")
	if err != nil {
		t.Fatal(err)
	}
	if j, err := st.Job(ctx, id); err != nil || j.Status != StatusReading {
		t.Fatalf("reading %+v %v", j, err)
	}
	snap := Snapshot{Service: "deezer", Account: "me", Tracks: []SnapTrack{{Key: "a", Artist: "Кино", Title: "Кукушка", Liked: true}}}
	if err := st.FillJob(ctx, id, snap); err != nil {
		t.Fatal(err)
	}
	if j, err := st.Job(ctx, id); err != nil || j.Status != StatusPreview || j.Account != "me" {
		t.Fatalf("filled %+v %v", j, err)
	}
	if c, err := st.Counts(ctx, id); err != nil || c.Songs != 1 {
		t.Fatalf("counts %+v %v", c, err)
	}
	if err := st.FillJob(ctx, id, snap); !errors.Is(err, ErrStarted) {
		t.Fatalf("second fill: %v", err)
	}
	if err := st.FillJob(ctx, 9999, snap); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown job: %v", err)
	}
}

func TestStatusMovesNeverOverwriteCancel(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	id, err := st.CreateJob(ctx, uid, Snapshot{Service: "file", Tracks: []SnapTrack{{Key: "a", Artist: "Кино", Title: "Кукушка", Liked: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.StartJob(ctx, id); err != nil {
		t.Fatal(err)
	}
	// the worker checked «matching», then a cancel came in before its write
	if err := st.CancelJob(ctx, id); err != nil {
		t.Fatal(err)
	}
	if moved, err := st.Advance(ctx, id, StatusMatching, StatusDownloading, ""); err != nil || moved {
		t.Fatalf("advance after cancel: moved %v %v", moved, err)
	}
	if err := st.SetStatus(ctx, id, StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Job(ctx, id); j.Status != StatusCancelled {
		t.Fatalf("status %s (want cancelled)", j.Status)
	}
	if _, err := st.Advance(ctx, 9999, StatusMatching, StatusDownloading, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown job: %v", err)
	}
}

// A deleted job's id is not given to the next job while a playlist still
// names it as its builder: the re-run would take it for the same job.
func TestJobIDNotReusedAfterDelete(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	snap := Snapshot{Service: "spotify", Tracks: []SnapTrack{{Key: "a", Artist: "Кино", Title: "Кукушка"}},
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Readable: true, Entries: []string{"a"}}}}
	j1, err := st.CreateJob(ctx, uid, snap)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, st.DB, `UPDATE import_playlists SET built_job = ? WHERE external_id = 'p1'`, j1)
	mustExec(t, st.DB, `UPDATE import_jobs SET status = 'done' WHERE id = ?`, j1)
	if err := st.DeleteJob(ctx, j1); err != nil {
		t.Fatal(err)
	}
	j2, err := st.CreateJob(ctx, uid, snap)
	if err != nil {
		t.Fatal(err)
	}
	j3, err := st.CreateReadingJob(ctx, uid, "deezer")
	if err != nil {
		t.Fatal(err)
	}
	if j2 == j1 || j3 == j1 || j3 == j2 {
		t.Fatalf("ids %d %d %d", j1, j2, j3)
	}
}

// A done job is not reopened (resolve, retry) while a newer job of its
// service is open: that job owns the service's playlists.
func TestDoneJobNotReopenedUnderNewerJob(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	snap := Snapshot{Service: "spotify", Tracks: []SnapTrack{{Key: "a", Artist: "Кино", Title: "Кукушка", Liked: true}}}
	j1, err := st.CreateJob(ctx, uid, snap)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, st.DB, `UPDATE import_tracks SET state = 'review' WHERE job_id = ?`, j1)
	mustExec(t, st.DB, `INSERT INTO import_groups(job_id, release_group, artist, album, state, updated_at) VALUES (?, 'rg', 'Кино', 'А', 'not_found', '')`, j1)
	mustExec(t, st.DB, `UPDATE import_jobs SET status = 'done' WHERE id = ?`, j1)
	if _, err := st.CreateJob(ctx, uid, snap); err != nil { // the re-run, in preview
		t.Fatal(err)
	}
	var track, group int64
	st.DB.QueryRow(`SELECT id FROM import_tracks WHERE job_id = ?`, j1).Scan(&track)
	st.DB.QueryRow(`SELECT id FROM import_groups WHERE job_id = ?`, j1).Scan(&group)
	if err := st.Resolve(ctx, track, Decision{ReleaseGroup: "rg2", Album: "Б", Artist: "Кино"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("resolve: %v", err)
	}
	if err := st.RetryGroup(ctx, group); !errors.Is(err, ErrBusy) {
		t.Fatalf("retry: %v", err)
	}
	if j, _ := st.Job(ctx, j1); j.Status != StatusDone {
		t.Fatalf("reopened: %s", j.Status)
	}
}
