package playlistimport

import (
	"context"
	"slices"
	"testing"
)

// matchByKey matches a song to the album of its key's prefix: "a…" and
// "b…" are on the library's «Сборник», the rest on an album it lacks.
type matchByKey struct{}

func (matchByKey) Match(_ context.Context, t Track) (Decision, error) {
	if t.Key == "a" || t.Key == "b" {
		return Decision{State: TrackMatched, RecordingID: "rec-" + t.Key, ReleaseGroup: "rg-have", Title: t.Title, Artist: "Кино",
			Album: "Сборник", Year: 1988}, nil
	}
	return Decision{State: TrackMatched, RecordingID: "rec-" + t.Key, ReleaseGroup: "rg-absent", Title: t.Title, Artist: "Кино",
		Album: "Нет такого", Year: 1990}, nil
}

// matchedJob stores a job of the songs a, b (on the library's album) and
// x (on one it lacks) in downloading, grouped by album.
func matchedJob(t *testing.T, e *rebuildEnv) int64 {
	t.Helper()
	ctx := context.Background()
	snap := Snapshot{Service: "file", Tracks: []SnapTrack{
		{Key: "a", Artist: "Кино", Title: "a", Liked: true}, {Key: "b", Artist: "Кино", Title: "b", Liked: true},
		{Key: "x", Artist: "Кино", Title: "x", Liked: true}}}
	id, err := e.st.CreateJob(ctx, e.uid, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a", "b", "x"} {
		var tr Track
		if err := e.st.DB.QueryRow(`SELECT id, key, title FROM import_tracks WHERE job_id = ? AND key = ?`, id, k).Scan(&tr.ID, &tr.Key, &tr.Title); err != nil {
			t.Fatal(err)
		}
		d, _ := matchByKey{}.Match(ctx, tr)
		if err := e.st.SetDecision(ctx, tr.ID, d); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, e.st.DB, `UPDATE import_jobs SET status = 'downloading' WHERE id = ?`, id)
	if err := e.st.AssignGroups(ctx, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLibraryStepLinksHaveAndMarksMissing(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	id := matchedJob(t, e)
	if err := LibraryStep(e.st, e.lib).Step(ctx, id); err != nil {
		t.Fatal(err)
	}
	gs, err := e.st.Groups(ctx, id)
	if err != nil || len(gs) != 2 {
		t.Fatalf("groups %+v %v", gs, err)
	}
	states := map[string]string{}
	for _, g := range gs {
		states[g.ReleaseGroup] = g.State
		if g.ReleaseGroup == "rg-absent" && g.Error != MsgNotInLibrary {
			t.Errorf("missing album's error %q", g.Error)
		}
	}
	if states["rg-have"] != GroupHave || states["rg-absent"] != GroupNotFound {
		t.Fatalf("group states %v", states)
	}
	ts, err := e.st.tracks(ctx, `SELECT `+trackCols+` FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id WHERE t.job_id = ? ORDER BY t.id`, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range ts {
		switch tr.Key {
		case "a", "b":
			if tr.State != TrackHave || tr.ItemID != e.items[tr.Key] {
				t.Errorf("%s: %s item %d (want %d)", tr.Key, tr.State, tr.ItemID, e.items[tr.Key])
			}
		case "x":
			if tr.State != TrackNotFound || tr.Reason != MsgNotInLibrary {
				t.Errorf("x: %s %q", tr.State, tr.Reason)
			}
		}
	}
	// settled: a second step changes nothing
	if err := LibraryStep(e.st, e.lib).Step(ctx, id); err != nil {
		t.Fatal(err)
	}
	if busy, err := e.st.Unfinished(ctx, id); err != nil || busy {
		t.Fatalf("unfinished %v %v", busy, err)
	}
}

func TestServiceWithoutDownloaderFinishes(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	snap := Snapshot{Service: "file", Tracks: []SnapTrack{
		{Key: "a", Artist: "Кино", Title: "a"}, {Key: "b", Artist: "Кино", Title: "b"}, {Key: "x", Artist: "Кино", Title: "x"}},
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "x", "b"}}}}
	id, err := e.st.CreateJob(ctx, e.uid, snap)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: e.st, Matcher: matchByKey{}, Library: e.lib,
		Rebuild: func(ctx context.Context, jobID int64) error { return Rebuild(ctx, e.st, e.m, jobID) }}
	if err := svc.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		svc.step(ctx)
	}
	j, err := e.st.Job(ctx, id)
	if err != nil || j.Status != StatusDone {
		t.Fatalf("job %+v %v", j, err)
	}
	if _, _, items := e.playlist("p1"); !slices.Equal(items, e.ids("a", "b")) {
		t.Fatalf("playlist %v (want a, b)", items)
	}
}

// TestLibraryStepSettlesStuckDownloads: a database another build used
// (albums unfinished, songs in its queue) is opened by an
// edition without that queue: its albums are settled by the library too, or
// the job would never end.
func TestLibraryStepSettlesStuckDownloads(t *testing.T) {
	ctx := context.Background()
	for _, state := range []string{GroupSearching, GroupDownloading, GroupWaitingSpace, GroupWaitingDownload} {
		t.Run(state, func(t *testing.T) {
			e := newRebuildEnv(t)
			id := matchedJob(t, e)
			mustExec(t, e.st.DB, `UPDATE import_groups SET state = ? WHERE job_id = ?`, state, id)
			mustExec(t, e.st.DB, `UPDATE import_tracks SET state = 'queued' WHERE job_id = ? AND key = 'a'`, id)
			mustExec(t, e.st.DB, `UPDATE import_tracks SET state = 'downloading' WHERE job_id = ? AND key = 'x'`, id)
			if busy, _ := e.st.Unfinished(ctx, id); !busy {
				t.Fatal("setup: the job is not unfinished")
			}
			if err := LibraryStep(e.st, e.lib).Step(ctx, id); err != nil {
				t.Fatal(err)
			}
			if busy, err := e.st.Unfinished(ctx, id); err != nil || busy {
				t.Fatalf("unfinished %v %v", busy, err)
			}
			gs, _ := e.st.Groups(ctx, id)
			got := map[string]string{}
			for _, g := range gs {
				got[g.ReleaseGroup] = g.State
			}
			if got["rg-have"] != GroupHave || got["rg-absent"] != GroupNotFound {
				t.Fatalf("group states %v", got)
			}
			var st string
			if err := e.st.DB.QueryRow(`SELECT state FROM import_tracks WHERE job_id = ? AND key = 'a'`, id).Scan(&st); err != nil || st != TrackHave {
				t.Fatalf("a: %q %v", st, err)
			}
		})
	}
	t.Run("failed, to be retried", func(t *testing.T) {
		e := newRebuildEnv(t)
		id := matchedJob(t, e)
		mustExec(t, e.st.DB, `UPDATE import_groups SET state = 'failed', attempts = 1 WHERE job_id = ?`, id)
		mustExec(t, e.st.DB, `UPDATE import_tracks SET state = 'failed' WHERE job_id = ?`, id)
		if err := LibraryStep(e.st, e.lib).Step(ctx, id); err != nil {
			t.Fatal(err)
		}
		if busy, err := e.st.Unfinished(ctx, id); err != nil || busy {
			t.Fatalf("unfinished %v %v", busy, err)
		}
	})
}

// A partly present album: the songs the library has are linked (have), the
// one it lacks is not found, and the album ends as not found with the
// reason, since it cannot be completed without a download.
func TestLibraryStepPartlyPresentAlbum(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	snap := Snapshot{Service: "file", Tracks: []SnapTrack{
		{Key: "a", Artist: "Кино", Title: "a", Liked: true}, {Key: "b", Artist: "Кино", Title: "b", Liked: true},
		{Key: "z", Artist: "Кино", Title: "z", Liked: true}}}
	id, err := e.st.CreateJob(ctx, e.uid, snap)
	if err != nil {
		t.Fatal(err)
	}
	// all three on the one album the library has; "z" is not on its shelf
	for _, k := range []string{"a", "b", "z"} {
		var tr Track
		if err := e.st.DB.QueryRow(`SELECT id, key, title FROM import_tracks WHERE job_id = ? AND key = ?`, id, k).Scan(&tr.ID, &tr.Key, &tr.Title); err != nil {
			t.Fatal(err)
		}
		d := Decision{State: TrackMatched, RecordingID: "rec-" + k, ReleaseGroup: "rg-have", Title: tr.Title, Artist: "Кино", Album: "Сборник", Year: 1988}
		if err := e.st.SetDecision(ctx, tr.ID, d); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, e.st.DB, `UPDATE import_jobs SET status = 'downloading' WHERE id = ?`, id)
	if err := e.st.AssignGroups(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := LibraryStep(e.st, e.lib).Step(ctx, id); err != nil {
		t.Fatal(err)
	}
	gs, err := e.st.Groups(ctx, id)
	if err != nil || len(gs) != 1 {
		t.Fatalf("groups %+v %v", gs, err)
	}
	if gs[0].State != GroupNotFound || gs[0].Error != MsgNotInLibrary {
		t.Errorf("group %s %q, want %s %q", gs[0].State, gs[0].Error, GroupNotFound, MsgNotInLibrary)
	}
	if gs[0].AlbumItemID == 0 {
		t.Error("the album found is not recorded on the group")
	}
	ts, err := e.st.tracks(ctx, `SELECT `+trackCols+` FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id WHERE t.job_id = ? ORDER BY t.id`, id)
	if err != nil || len(ts) != 3 {
		t.Fatalf("tracks %+v %v", ts, err)
	}
	for _, tr := range ts {
		switch tr.Key {
		case "a", "b":
			if tr.State != TrackHave || tr.ItemID != e.items[tr.Key] {
				t.Errorf("%s: %s item %d (want have, %d)", tr.Key, tr.State, tr.ItemID, e.items[tr.Key])
			}
		case "z":
			if tr.State != TrackNotFound || tr.Reason != MsgNotInLibrary {
				t.Errorf("z: %s %q", tr.State, tr.Reason)
			}
		}
	}
	if busy, err := e.st.Unfinished(ctx, id); err != nil || busy {
		t.Fatalf("unfinished %v %v", busy, err)
	}
}

// LibraryStep without a library (nil *library.Repo): nothing is present,
// every album and song is not found, and nothing panics.
func TestLibraryStepNilLibrary(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	id := matchedJob(t, e)
	if err := LibraryStep(e.st, nil).Step(ctx, id); err != nil {
		t.Fatal(err)
	}
	gs, err := e.st.Groups(ctx, id)
	if err != nil || len(gs) != 2 {
		t.Fatalf("groups %+v %v", gs, err)
	}
	for _, g := range gs {
		if g.State != GroupNotFound || g.Error != MsgNotInLibrary {
			t.Errorf("%s: %s %q", g.ReleaseGroup, g.State, g.Error)
		}
	}
	ts, err := e.st.tracks(ctx, `SELECT `+trackCols+` FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id WHERE t.job_id = ? ORDER BY t.id`, id)
	if err != nil || len(ts) != 3 {
		t.Fatalf("tracks %+v %v", ts, err)
	}
	for _, tr := range ts {
		if tr.State != TrackNotFound || tr.Reason != MsgNotInLibrary {
			t.Errorf("%s: %s %q", tr.Key, tr.State, tr.Reason)
		}
	}
	if busy, err := e.st.Unfinished(ctx, id); err != nil || busy {
		t.Fatalf("unfinished %v %v", busy, err)
	}
}
