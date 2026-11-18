package accounts

import (
	"context"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/tracking"
)

func TestImportWins(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	local := func(state string, at time.Time) Mark { return Mark{State: state, UpdatedAt: at} }
	for _, c := range []struct {
		name   string
		state  string
		at     time.Time
		local  Mark
		has    bool
		remote bool
	}{
		{"no mark here", "planned", t0, Mark{}, false, true},
		{"completed beats planned, older or not", "completed", t0, local("planned", t0.Add(time.Hour)), true, true},
		{"planned never undoes completed, even newer", "planned", t0.Add(time.Hour), local("completed", t0), true, false},
		{"watching beats on hold", "watching", t0, local("on_hold", t0.Add(time.Hour)), true, true},
		{"same rank: the newer wins", "planned", t0.Add(time.Hour), local("planned", t0), true, true},
		{"same rank: an older one loses", "dropped", t0, local("on_hold", t0.Add(time.Hour)), true, false},
		{"same rank, no remote time: the mark stays", "planned", time.Time{}, local("planned", t0), true, false},
	} {
		if got := ImportWins(c.state, c.at, c.local, c.has); got != c.remote {
			t.Errorf("%s: ImportWins = %v", c.name, got)
		}
	}
}

// The tester's case: a movie MyShows has watched («completed», from MyShows)
// and Shikimori has «planned». Linking Shikimori must not undo it: the
// mark stays completed (so it is out of «Вышло новое») and goes to
// Shikimori.
func TestFirstPullNeverUndoesCompleted(t *testing.T) {
	e := newSyncEnv(t)
	movie := e.myshowsMark("movie", 102, tracking.Completed, e.now.Add(-time.Hour))
	e.clearOutbox()
	must(t, e.acc.SetWriteState(context.Background(), e.uid, Shikimori, "enabled"))
	e.remote.rates = []RemoteRate{rate(12, 2, "planned", 0, e.now.Add(-time.Minute))}
	if r := e.pull(); r.Updated+r.Imported != 0 {
		t.Fatalf("%+v", r)
	}
	if m := e.mark("movie", 102); m == nil || m.State != tracking.Completed || m.Origin != "myshows" {
		t.Fatalf("completed undone: %+v", m)
	}
	if items := e.outbox(); len(items) != 1 || items[0].TitleID != movie || items[0].Op != "upsert" {
		t.Fatalf("completed not queued for Shikimori: %+v", items)
	}
	// write-back declined: the mark stays, nothing goes out
	e2 := newSyncEnv(t)
	e2.myshowsMark("movie", 102, tracking.Completed, e2.now.Add(-time.Hour))
	e2.clearOutbox()
	must(t, e2.acc.SetWriteState(context.Background(), e2.uid, Shikimori, "declined"))
	e2.remote.rates = []RemoteRate{rate(12, 2, "planned", 0, e2.now.Add(-time.Minute))}
	e2.pull()
	if m := e2.mark("movie", 102); m == nil || m.State != tracking.Completed {
		t.Fatalf("declined: completed undone: %+v", m)
	}
	if items := e2.outbox(); len(items) != 0 {
		t.Fatalf("declined queued %+v", items)
	}
}

// On a first pull, the same rank goes to the newer side.
func TestFirstPullSameRankNewerWins(t *testing.T) {
	e := newSyncEnv(t)
	e.local("series", 101, tracking.OnHold, e.now.Add(-time.Hour))
	e.local("series", 104, tracking.OnHold, e.now.Add(-48*time.Hour))
	e.clearOutbox()
	e.remote.rates = []RemoteRate{
		rate(11, 1, "dropped", 0, e.now.Add(-48*time.Hour)), // older than the mark
		rate(14, 4, "dropped", 0, e.now.Add(-time.Hour)),    // newer
	}
	e.pull()
	if m := e.mark("series", 101); m.State != tracking.OnHold || m.Origin != "antresol" {
		t.Fatalf("newer mark here lost: %+v", m)
	}
	if m := e.mark("series", 104); m.State != tracking.Dropped || m.Origin != "shikimori" {
		t.Fatalf("newer remote lost: %+v", m)
	}
}

// After the first pull, a real remote change wins by recency, even one
// that goes back (a rewatch planned on Shikimori).
func TestLaterRemoteChangeMayGoBack(t *testing.T) {
	e := newSyncEnv(t)
	t0 := e.now.Add(-48 * time.Hour)
	e.remote.rates = []RemoteRate{rate(12, 2, "completed", 0, t0)}
	e.pull()
	e.remote.rates = []RemoteRate{rate(12, 2, "planned", 0, e.now.Add(-time.Minute))}
	e.pull()
	if m := e.mark("movie", 102); m.State != tracking.Planned || m.Origin != "shikimori" {
		t.Fatalf("newer remote change lost: %+v", m)
	}
}

// No ping-pong: a MyShows mark goes to Shikimori once; the echo of that
// push is recognised, and the lists stay settled.
func TestReconcileSettles(t *testing.T) {
	e := newPushEnv(t)
	e.myshowsMark("movie", 102, tracking.Completed, e.now.Add(-time.Hour))
	e.push()
	if len(e.w.creates) != 1 || e.w.creates[0].W.Status != "completed" {
		t.Fatalf("creates %+v", e.w.creates)
	}
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("outbox %+v", items)
	}
	// the next pull reads our own write back
	e.remote.rates = []RemoteRate{rate(e.w.creates[0].RateID, 2, "completed", 0, e.w.at)}
	if r := e.pull(); r.Updated+r.Imported != 0 {
		t.Fatalf("echo applied %+v", r)
	}
	if m := e.mark("movie", 102); m.Origin != "myshows" || m.State != tracking.Completed {
		t.Fatalf("mark %+v", m)
	}
	e.push()
	if len(e.w.creates)+len(e.w.updates) != 1 || len(e.outbox()) != 0 {
		t.Fatalf("pushed again: creates %+v updates %+v", e.w.creates, e.w.updates)
	}
}

// Removals reach Shikimori only from here.
func TestOnlyLocalRemovalDeletesRemotely(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	tid := e.myshowsMark("series", 101, tracking.Planned, e.now)
	e.clearOutbox()
	must(t, e.st.DeleteMarkFrom(ctx, e.uid, tid, "myshows"))
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("MyShows removal queued %+v", items)
	}
	e.myshowsMark("series", 101, tracking.Planned, e.now)
	e.clearOutbox()
	must(t, e.st.DeleteMark(ctx, e.uid, tid))
	if items := e.outbox(); len(items) != 1 || items[0].Op != "delete" {
		t.Fatalf("local removal not queued %+v", items)
	}
}

func TestRemoteWatched(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	movie, _ := e.st.EnsureTitle(ctx, "movie", 102, "m", "")
	series, _ := e.st.EnsureTitle(ctx, "series", 300, "s", "")
	other, _ := e.st.EnsureTitle(ctx, "series", 104, "o", "")
	e.sy.Episodes = func(_ context.Context, titleID int64) ([]Episode, error) {
		eps := append(aired(2, 1, 3), aired(1, 1, 3)...) // out of order on purpose
		eps = append(eps, Episode{Season: 0, Number: 1, AirDate: "2026-01-01"}, Episode{Season: 2, Number: 4, AirDate: "2099-01-01"})
		return eps, nil
	}
	// the movie: finished on MyShows
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: "myshows", TargetID: 7, TitleID: movie.ID, RemoteStatus: "finished"}))
	// series 300: Shikimori cour S1 completed (3 episodes), cour S2 at 1
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 21, TitleID: series.ID, TMDBSeason: 1, RemoteStatus: "completed", AnimeEpisodes: 3}))
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 22, TitleID: series.ID, TMDBSeason: 2, RemoteStatus: "watching", RemoteEpisodes: 1}))
	// series 104: MyShows counts 4 watched (S1E1–3, S2E1), or finished with no count
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: "myshows", TargetID: 8, TitleID: other.ID, RemoteStatus: "watching", RemoteEpisodes: 4}))

	get := func(ti tracking.Title, skip string) map[[2]int]bool {
		w, err := e.sy.RemoteWatched(ctx, e.uid, refOf(ti), skip)
		must(t, err)
		return w
	}
	if w := get(movie, ""); !w[[2]int{0, 0}] || len(w) != 1 {
		t.Fatalf("movie %v", w)
	}
	if w := get(series, ""); len(w) != 4 || !w[[2]int{1, 3}] || !w[[2]int{2, 1}] || w[[2]int{2, 2}] {
		t.Fatalf("cours %v", w)
	}
	if w := get(series, Shikimori); len(w) != 0 {
		t.Fatalf("skipped list counted %v", w)
	}
	if w := get(other, ""); len(w) != 4 || !w[[2]int{1, 1}] || !w[[2]int{2, 1}] || w[[2]int{0, 1}] {
		t.Fatalf("MyShows count %v", w)
	}
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: "myshows", TargetID: 8, TitleID: other.ID, RemoteStatus: "finished"}))
	if w := get(other, ""); len(w) != 6 || w[[2]int{2, 4}] {
		t.Fatalf("MyShows finished: want every aired regular episode %v", w)
	}
}

// What another list says was watched goes to Shikimori with a push.
func TestPushCarriesEpisodesWatchedElsewhere(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	tid := e.local("series", 400, tracking.Watching, e.now.Add(-time.Hour))
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 31, TitleID: tid, TMDBSeason: 1, RateID: 71, RemoteStatus: "watching", AnimeEpisodes: 12, MarkAt: e.now.Add(-time.Hour)}))
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: "myshows", TargetID: 9, TitleID: tid, RemoteStatus: "watching", RemoteEpisodes: 5}))
	e.episodes[tid] = aired(1, 1, 24)
	e.clearOutbox()
	e.sy.EpisodesChanged(ctx, e.uid, tid)
	e.push()
	if len(e.w.updates) != 1 || e.w.updates[0].W.Episodes == nil || *e.w.updates[0].W.Episodes != 5 {
		t.Fatalf("updates %+v", e.w.updates)
	}
}
