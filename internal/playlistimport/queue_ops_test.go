package playlistimport

import (
	"context"
	"slices"
	"testing"
	"time"
)

// opsJob is matchedJob's job (a, b on the library's album rg-have, x on
// rg-absent) with its groups by release group and its songs by key.
type opsJob struct {
	id     int64
	groups map[string]int64
	tracks map[string]int64
}

func newOpsJob(t *testing.T, e *rebuildEnv) opsJob {
	t.Helper()
	ctx := context.Background()
	o := opsJob{id: matchedJob(t, e), groups: map[string]int64{}, tracks: map[string]int64{}}
	gs, err := e.st.Groups(ctx, o.id)
	if err != nil || len(gs) != 2 {
		t.Fatalf("groups %+v %v", gs, err)
	}
	for _, g := range gs {
		o.groups[g.ReleaseGroup] = g.ID
	}
	rows, err := e.st.DB.Query(`SELECT id, key FROM import_tracks WHERE job_id = ?`, o.id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var k string
		if err := rows.Scan(&id, &k); err != nil {
			t.Fatal(err)
		}
		o.tracks[k] = id
	}
	return o
}

// libAlbum is the library's album «Сборник».
func libAlbum(t *testing.T, e *rebuildEnv) int64 {
	t.Helper()
	it, ok, err := e.lib.FindAlbum(context.Background(), "Кино", "Сборник")
	if err != nil || !ok {
		t.Fatalf("album: %v %v", ok, err)
	}
	return it.ID
}

func mustGroup(t *testing.T, st *Store, id int64) Group {
	t.Helper()
	g, err := st.Group(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func mustTrack(t *testing.T, st *Store, id int64) Track {
	t.Helper()
	tr, err := st.Track(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// startOn moves the group to searching and starts its download key.
func startOn(t *testing.T, st *Store, id int64, key string, downloading, missing []int64) {
	t.Helper()
	ctx := context.Background()
	at := st.clock()
	g := mustGroup(t, st, id)
	if ok, err := st.MoveGroup(ctx, id, g.State, GroupSearching, at); err != nil || !ok {
		t.Fatalf("move %s → searching: %v %v", g.State, ok, err)
	}
	if ok, err := st.StartGroup(ctx, id, key, 100, downloading, missing, at); err != nil || !ok {
		t.Fatalf("start: %v %v", ok, err)
	}
}

func TestQueueOps_MoveIsCAS(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	at := e.st.clock()
	g := o.groups["rg-absent"]
	if ok, err := e.st.MoveGroup(ctx, g, GroupWaiting, GroupSearching, at); err != nil || !ok {
		t.Fatalf("first move: %v %v", ok, err)
	}
	// the second claimer of the same album loses
	if ok, err := e.st.MoveGroup(ctx, g, GroupWaiting, GroupSearching, at); err != nil || ok {
		t.Fatalf("second move: %v %v", ok, err)
	}
	if n, err := e.st.ActiveGroups(ctx); err != nil || n != 1 {
		t.Fatalf("active %d %v", n, err)
	}
	// a cancelled job's searches no longer hold slots
	mustExec(t, e.st.DB, `UPDATE import_jobs SET status = 'cancelled' WHERE id = ?`, o.id)
	if n, err := e.st.ActiveGroups(ctx); err != nil || n != 0 {
		t.Fatalf("active after cancel %d %v", n, err)
	}
	if ok, err := e.st.StillDownloading(ctx, o.id); err != nil || ok {
		t.Fatalf("still downloading %v %v", ok, err)
	}
}

func TestQueueOps_StartIsCAS(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	at := e.st.clock()
	g, x := o.groups["rg-absent"], o.tracks["x"]
	// not searching: refused
	if ok, err := e.st.StartGroup(ctx, g, "k1", 100, []int64{x}, nil, at); err != nil || ok {
		t.Fatalf("start from waiting: %v %v", ok, err)
	}
	if ok, err := e.st.MoveGroup(ctx, g, GroupWaiting, GroupSearching, at); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// a core restart puts the search back in the queue: the start fails
	if err := e.st.ResetSearching(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.st.StartGroup(ctx, g, "k1", 100, []int64{x}, nil, at); err != nil || ok {
		t.Fatalf("start after reset: %v %v", ok, err)
	}
	if got := mustGroup(t, e.st, g); got.State != GroupWaiting || got.InfoHash != "" {
		t.Fatalf("after reset %+v", got)
	}
	if ok, err := e.st.MoveGroup(ctx, g, GroupWaiting, GroupSearching, at); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := e.st.StartGroup(ctx, g, "k1", 100, []int64{x}, nil, at); err != nil || !ok {
		t.Fatalf("start: %v %v", ok, err)
	}
	// two claimers: the second start loses
	if ok, err := e.st.StartGroup(ctx, g, "k2", 100, []int64{x}, nil, at); err != nil || ok {
		t.Fatalf("second start: %v %v", ok, err)
	}
	got := mustGroup(t, e.st, g)
	if got.State != GroupDownloading || got.InfoHash != "k1" || got.Bytes != 100 || got.Clock.Base != 0 {
		t.Fatalf("started %+v", got)
	}
	if tr := mustTrack(t, e.st, x); tr.State != TrackDownloading {
		t.Fatalf("x %s", tr.State)
	}
}

// The songs of an operation are those of its album: an id of another
// album is left alone.
func TestQueueOps_TracksOfTheGroupOnly(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	g, a, x := o.groups["rg-absent"], o.tracks["a"], o.tracks["x"]
	startOn(t, e.st, g, "k1", []int64{x, a}, nil)
	if tr := mustTrack(t, e.st, a); tr.State != TrackMatched {
		t.Fatalf("a (another album's) moved to %s", tr.State)
	}
	if err := e.st.FailGroup(ctx, g, []int64{x, a}, "Ошибка", false, e.st.clock()); err != nil {
		t.Fatal(err)
	}
	if tr := mustTrack(t, e.st, a); tr.State != TrackMatched {
		t.Fatalf("a failed with another album: %s", tr.State)
	}
}

func TestQueueOps_Candidates(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	now := e.st.clock()
	have, absent := o.groups["rg-have"], o.groups["rg-absent"]
	ids := func(retryBefore time.Time) []int64 {
		t.Helper()
		gs, err := e.st.QueueCandidates(ctx, o.id, retryBefore)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, g := range gs {
			out = append(out, g.ID)
		}
		return out
	}
	if got := ids(now); !slices.Equal(got, []int64{have, absent}) {
		t.Fatalf("waiting: %v", got)
	}
	for _, st := range []string{GroupWaitingSpace, GroupWaitingDownload} {
		mustExec(t, e.st.DB, `UPDATE import_groups SET state = ? WHERE id = ?`, st, absent)
		if got := ids(now); !slices.Equal(got, []int64{have, absent}) {
			t.Fatalf("%s: %v", st, got)
		}
	}
	mustExec(t, e.st.DB, `UPDATE import_groups SET state = 'searching' WHERE id = ?`, absent)
	if got := ids(now); !slices.Equal(got, []int64{have}) {
		t.Fatalf("searching: %v", got)
	}
	// failed once: a candidate once its retry is due
	if err := e.st.FailGroup(ctx, absent, []int64{o.tracks["x"]}, "Ошибка", true, now); err != nil {
		t.Fatal(err)
	}
	if got := ids(now.Add(-time.Minute)); !slices.Equal(got, []int64{have}) {
		t.Fatalf("failed, not due: %v", got)
	}
	if got := ids(now); !slices.Equal(got, []int64{have, absent}) {
		t.Fatalf("failed, due: %v", got)
	}
	// failed twice: only «Повторить»
	if err := e.st.FailGroup(ctx, absent, nil, "Ошибка", true, now); err != nil {
		t.Fatal(err)
	}
	if got := ids(now); !slices.Equal(got, []int64{have}) {
		t.Fatalf("failed twice: %v", got)
	}
	if gs, err := e.st.JobGroups(ctx, o.id, GroupFailed); err != nil || len(gs) != 1 || gs[0].ID != absent {
		t.Fatalf("job groups failed %+v %v", gs, err)
	}
	if gs, err := e.st.JobGroups(ctx, o.id, ""); err != nil || len(gs) != 2 {
		t.Fatalf("job groups all %+v %v", gs, err)
	}
}

func TestQueueOps_WaitAndGiveUp(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	at := e.st.clock()
	g := o.groups["rg-absent"]
	if err := e.st.WaitFor(ctx, g, "w1", "Кино — Нет такого", 10, at); err != nil {
		t.Fatal(err)
	}
	got := mustGroup(t, e.st, g)
	if got.State != GroupWaitingDownload || got.InfoHash != "w1" || got.WaitingFor != "Кино — Нет такого" || got.WaitDone != 10 ||
		!got.WaitAt.Equal(at) {
		t.Fatalf("waiting %+v", got)
	}
	// the same download, no more bytes: the clock keeps its start
	later := at.Add(time.Hour)
	if err := e.st.WaitFor(ctx, g, "w1", "Кино — Нет такого", 10, later); err != nil {
		t.Fatal(err)
	}
	if got := mustGroup(t, e.st, g); !got.WaitAt.Equal(at) {
		t.Fatalf("clock restarted without bytes: %v", got.WaitAt)
	}
	// more bytes: it restarts
	if err := e.st.WaitFor(ctx, g, "w1", "Кино — Нет такого", 20, later); err != nil {
		t.Fatal(err)
	}
	if got := mustGroup(t, e.st, g); !got.WaitAt.Equal(later) || got.WaitDone != 20 {
		t.Fatalf("clock with bytes: %+v", got)
	}
	if err := e.st.GiveUpWait(ctx, g, "w1", later); err != nil {
		t.Fatal(err)
	}
	got = mustGroup(t, e.st, g)
	if got.State != GroupWaiting || got.SkipInfoHash != "w1" || got.WaitingFor != "" || got.WaitDone != 0 || !got.WaitAt.IsZero() {
		t.Fatalf("given up %+v", got)
	}
	// not waiting for a download: nothing
	if err := e.st.GiveUpWait(ctx, g, "w2", later); err != nil {
		t.Fatal(err)
	}
	if got := mustGroup(t, e.st, g); got.SkipInfoHash != "w1" {
		t.Fatalf("given up a wait it had not: %+v", got)
	}
}

func TestQueueOps_ClockOfItsDownloadOnly(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	g := o.groups["rg-absent"]
	startOn(t, e.st, g, "k1", []int64{o.tracks["x"]}, nil)
	c := RunClock{Run: 90 * time.Second, Quiet: 30 * time.Second, Base: 5, Done: 50}
	if err := e.st.SaveClock(ctx, g, "other", c); err != nil {
		t.Fatal(err)
	}
	if got := mustGroup(t, e.st, g); got.Clock.Run != 0 {
		t.Fatalf("another key's clock saved: %+v", got.Clock)
	}
	if err := e.st.SaveClock(ctx, g, "k1", c); err != nil {
		t.Fatal(err)
	}
	if got := mustGroup(t, e.st, g); got.Clock != c || got.WaitDone != 50 {
		t.Fatalf("clock %+v", got.Clock)
	}
}

func TestQueueOps_RequeueGrowsDead(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	g, x := o.groups["rg-absent"], o.tracks["x"]
	at := e.st.clock()
	for i, key := range []string{"k1", "k2", "k3"} {
		startOn(t, e.st, g, key, []int64{x}, nil)
		if err := e.st.MarkGivenUp(ctx, g, key, false); err != nil {
			t.Fatal(err)
		}
		if err := e.st.MarkGivenUp(ctx, g, key, false); err != nil { // listed once
			t.Fatal(err)
		}
		if got := mustGroup(t, e.st, g); len(got.Dead) != i+1 || got.Dead[i] != key {
			t.Fatalf("dead after %s: %v", key, got.Dead)
		}
		if err := e.st.Requeue(ctx, g, key, "Мёртвые", 3, at); err != nil {
			t.Fatal(err)
		}
		got := mustGroup(t, e.st, g)
		if i < 2 {
			if got.State != GroupWaiting || got.InfoHash != "" || got.Bytes != 0 || got.Clock.Base != -1 {
				t.Fatalf("requeued after %s: %+v", key, got)
			}
			if tr := mustTrack(t, e.st, x); tr.State != TrackMatched {
				t.Fatalf("x after requeue %s", tr.State)
			}
			continue
		}
		// the third dead download: failed, only «Повторить» is left
		if got.State != GroupFailed || got.Error != "Мёртвые" || got.Attempts != AutoRetries+1 {
			t.Fatalf("failed after three: %+v", got)
		}
		if tr := mustTrack(t, e.st, x); tr.State != TrackFailed {
			t.Fatalf("x after the third %s", tr.State)
		}
	}
	// a slow one goes to slow_json; requeue of a key it no longer
	// downloads changes nothing
	if err := e.st.RetryGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	startOn(t, e.st, g, "s1", []int64{x}, nil)
	if err := e.st.MarkGivenUp(ctx, g, "s1", true); err != nil {
		t.Fatal(err)
	}
	if got := mustGroup(t, e.st, g); !slices.Equal(got.Slow, []string{"s1"}) || len(got.Dead) != 0 {
		t.Fatalf("slow %v dead %v", got.Slow, got.Dead)
	}
	if err := e.st.Requeue(ctx, g, "other", "", 3, at); err != nil {
		t.Fatal(err)
	}
	if got := mustGroup(t, e.st, g); got.State != GroupDownloading {
		t.Fatalf("requeued by another key: %s", got.State)
	}
}

func TestQueueOps_FinishSetsTracksAndAlbum(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	g, a, b := o.groups["rg-have"], o.tracks["a"], o.tracks["b"]
	startOn(t, e.st, g, "k1", []int64{a, b}, nil)
	album := libAlbum(t, e)
	if err := e.st.FinishGroup(ctx, g, map[int64]int64{a: e.items["a"]}, []int64{b}, album, e.st.clock()); err != nil {
		t.Fatal(err)
	}
	got := mustGroup(t, e.st, g)
	if got.State != GroupDone || got.AlbumItemID != album {
		t.Fatalf("finished %+v", got)
	}
	if tr := mustTrack(t, e.st, a); tr.State != TrackDone || tr.ItemID != e.items["a"] {
		t.Fatalf("a %+v", tr)
	}
	if tr := mustTrack(t, e.st, b); tr.State != TrackFailed || tr.Reason != MsgError {
		t.Fatalf("b %+v", tr)
	}
}

func TestQueueOps_NoFormatAndFail(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	g, x := o.groups["rg-absent"], o.tracks["x"]
	at := e.st.clock()
	if err := e.st.NoFormat(ctx, g, []int64{x}, "Нет формата", at); err != nil {
		t.Fatal(err)
	}
	if got := mustGroup(t, e.st, g); got.State != GroupNoFormat || got.Error != "Нет формата" {
		t.Fatalf("no format %+v", got)
	}
	if tr := mustTrack(t, e.st, x); tr.State != TrackNotFound {
		t.Fatalf("x %s", tr.State)
	}
	if err := e.st.RetryGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	// a download that went wrong: no automatic retry
	if err := e.st.FailGroup(ctx, g, []int64{x}, "Сломалось", false, at); err != nil {
		t.Fatal(err)
	}
	if got := mustGroup(t, e.st, g); got.State != GroupFailed || got.Attempts != AutoRetries+1 || got.Error != "Сломалось" {
		t.Fatalf("failed %+v", got)
	}
}

func TestQueueOps_HaveInsertsOwners(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	g, a, b := o.groups["rg-have"], o.tracks["a"], o.tracks["b"]
	album := libAlbum(t, e)
	if err := e.st.MarkHave(ctx, g, map[int64]int64{a: e.items["a"], b: e.items["b"]}, album, true, e.st.clock()); err != nil {
		t.Fatal(err)
	}
	if got := mustGroup(t, e.st, g); got.State != GroupHave || got.AlbumItemID != album {
		t.Fatalf("have %+v", got)
	}
	var n int
	if err := e.st.DB.QueryRow(`SELECT COUNT(*) FROM item_owners WHERE item_id = ? AND user_id = ?`, album, e.uid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("owner rows %d %v", n, err)
	}
}

func TestQueueOps_Jobs(t *testing.T) {
	ctx := context.Background()
	e := newRebuildEnv(t)
	o := newOpsJob(t, e)
	if ids, err := e.st.JobIDs(ctx, StatusDownloading); err != nil || !slices.Equal(ids, []int64{o.id}) {
		t.Fatalf("downloading %v %v", ids, err)
	}
	if ok, err := e.st.StillDownloading(ctx, o.id); err != nil || !ok {
		t.Fatalf("still %v %v", ok, err)
	}
	r, err := e.st.CreateReadingJob(ctx, e.uid, "spotify")
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := e.st.ReadingJobs(ctx, e.uid); err != nil || !slices.Equal(ids, []int64{r}) {
		t.Fatalf("reading %v %v", ids, err)
	}
	if ids, err := e.st.ReadingJobs(ctx, e.uid+100); err != nil || len(ids) != 0 {
		t.Fatalf("another profile's reading %v %v", ids, err)
	}
}
