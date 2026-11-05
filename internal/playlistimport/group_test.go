package playlistimport

import (
	"context"
	"errors"
	"testing"
)

func TestAssignGroupsOnePerAlbum(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	snap := Snapshot{Service: "file", Tracks: []SnapTrack{
		{Key: "a", Artist: "Кино", Title: "Группа крови", Liked: true},
		{Key: "b", Artist: "Кино", Title: "Закрой за мной дверь", Liked: true},
		{Key: "c", Artist: "Кино", Title: "Война", Liked: true},
		{Key: "d", Artist: "Кино", Title: "Кукушка", Liked: true},
		{Key: "e", Artist: "Кино", Title: "Неизвестная", Liked: true},
	}}
	id, err := st.CreateJob(ctx, uid, snap)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, st.DB, `UPDATE import_jobs SET status = 'matching' WHERE id = ?`, id)
	tracks, err := st.NextPending(ctx, id, 10)
	if err != nil || len(tracks) != 5 {
		t.Fatalf("%d %v", len(tracks), err)
	}
	blood := Decision{State: TrackMatched, ReleaseGroup: "rg-blood", Artist: "Кино", Album: "Группа крови", Year: 1988}
	black := Decision{State: TrackMatched, ReleaseGroup: "rg-black", Artist: "Кино", Album: "Чёрный альбом", Year: 1990}
	for i, d := range []Decision{blood, blood, blood, black, {State: TrackReview, Reason: "Нет точного совпадения"}} {
		d.Title = tracks[i].Title
		if err := st.SetDecision(ctx, tracks[i].ID, d); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 { // idempotent
		if err := st.AssignGroups(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	type group struct {
		rg, artist, album, state string
		year, songs              int
	}
	groups := func() map[string]group {
		rows, err := st.DB.Query(`SELECT g.release_group, g.artist, g.album, g.year, g.state,
			(SELECT COUNT(*) FROM import_tracks t WHERE t.group_id = g.id) FROM import_groups g WHERE g.job_id = ?`, id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]group{}
		for rows.Next() {
			var g group
			if err := rows.Scan(&g.rg, &g.artist, &g.album, &g.year, &g.state, &g.songs); err != nil {
				t.Fatal(err)
			}
			out[g.rg] = g
		}
		return out
	}
	gs := groups()
	if len(gs) != 2 || gs["rg-blood"] != (group{"rg-blood", "Кино", "Группа крови", GroupWaiting, 1988, 3}) ||
		gs["rg-black"] != (group{"rg-black", "Кино", "Чёрный альбом", GroupWaiting, 1990, 1}) {
		t.Fatalf("groups %+v", gs)
	}
	var ungrouped int
	st.DB.QueryRow(`SELECT COUNT(*) FROM import_tracks WHERE job_id = ? AND group_id IS NULL`, id).Scan(&ungrouped)
	if ungrouped != 1 {
		t.Fatalf("%d songs without a group (want the review one)", ungrouped)
	}

	// a song resolved into a finished album sends it back to the queue
	mustExec(t, st.DB, `UPDATE import_groups SET state = 'done' WHERE job_id = ? AND release_group = 'rg-black'`, id)
	mustExec(t, st.DB, `UPDATE import_jobs SET status = 'done' WHERE id = ?`, id)
	if err := st.Resolve(ctx, tracks[4].ID, Decision{ReleaseGroup: "rg-black", Artist: "Кино", Album: "Чёрный альбом", Year: 1990,
		Aliases: []string{"Black Album"}}); err != nil {
		t.Fatal(err)
	}
	if g := groups()["rg-black"]; g.songs != 2 || g.state != GroupWaiting {
		t.Fatalf("resolved into %+v", g)
	}
	if j, _ := st.Job(ctx, id); j.Status != StatusDownloading {
		t.Fatalf("job %s (want downloading again)", j.Status)
	}
	if err := st.Resolve(ctx, tracks[4].ID, Decision{ReleaseGroup: "rg-black"}); !errors.Is(err, ErrNotReview) {
		t.Fatalf("resolve twice: %v", err)
	}
}
