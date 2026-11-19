package channels

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// fakeFetchCatalog names a series of tmdb 100 with n episodes of 45 minutes
// and movies by their tmdb id (100 minutes).
type fakeFetchCatalog struct{ episodes int }

func (c fakeFetchCatalog) Programmes(_ context.Context, t FetchTitle) ([]FetchProgramme, error) {
	if t.Kind == library.KindMovie {
		return []FetchProgramme{{Kind: library.KindMovie, Title: fmt.Sprintf("M%d", t.TMDBID), RuntimeMs: 100 * 60000}}, nil
	}
	var out []FetchProgramme
	for e := 1; e <= c.episodes; e++ {
		out = append(out, FetchProgramme{Kind: library.KindEpisode, Season: 1, Episode: e, Title: "S", EpisodeTitle: fmt.Sprintf("E%d", e), RuntimeMs: 45 * 60000})
	}
	return out, nil
}

func (fakeFetchCatalog) Search(context.Context, string) ([]FetchFound, error) { return nil, nil }

func (fakeFetchCatalog) Seasons(context.Context, int) ([]int, error) { return []int{1}, nil }

type fakeFetcher struct {
	mu     sync.Mutex
	states map[string]FetchState
	wants  [][]FetchWant
	drops  []string
}

func (f *fakeFetcher) Ready(context.Context) bool { return true }

func (f *fakeFetcher) Fetch(_ context.Context, want []FetchWant, drop []string) ([]FetchState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wants = append(f.wants, want)
	f.drops = append(f.drops, drop...)
	var out []FetchState
	for _, w := range want {
		st, ok := f.states[w.Key]
		if !ok {
			st = FetchState{State: FetchSearching}
		}
		st.Key = w.Key
		out = append(out, st)
	}
	return out, nil
}

type fetchFix struct {
	*fixture
	svc     *Service
	fetcher *fakeFetcher
	removed []int64
	series  int64
	season  int64
	eps     map[int]int64 // episode number → item id (on disk)
}

// newFetchFix: a series (tmdb 100) of n episodes, the library having
// those of have, and a channel with fetching playing it.
func newFetchFix(t *testing.T, n int, have []int, rules FetchRules) (*fetchFix, Channel) {
	f := &fetchFix{fixture: newFixture(t), fetcher: &fakeFetcher{states: map[string]FetchState{}}, eps: map[int]int64{}}
	f.series = f.item("series", "S", 0, itemOpt{})
	f.exec(`UPDATE items SET tmdb_id=100 WHERE id=?`, f.series)
	f.season = f.exec(`INSERT INTO items (kind, parent_id, title, season_no) VALUES ('season',?,?,1)`, f.series, "Season 1")
	for _, e := range have {
		f.arrive(e)
	}
	f.svc = New(f.db, nil)
	f.svc.Store.Now = f.st.Now
	f.svc.SetFetch(f.fetcher, fakeFetchCatalog{episodes: n}, func(_ context.Context, id int64) error {
		f.removed = append(f.removed, id)
		f.exec(`UPDATE files SET missing_since='x' WHERE item_id=?`, id)
		return nil
	})
	if len(rules.Titles) == 0 {
		rules.Titles = []FetchTitle{{Kind: library.KindSeries, TMDBID: 100}}
	}
	c := f.create(Channel{Name: "Marathon", Rules: Rules{Fetch: &rules}})
	return f, c
}

// arrive puts episode e of the series on disk.
func (f *fetchFix) arrive(e int) int64 {
	id := f.exec(`INSERT INTO items (kind, parent_id, title, season_no, episode_no) VALUES ('episode',?,?,1,?)`, f.season, fmt.Sprintf("E%d", e), e)
	f.file(id, 45)
	f.eps[e] = id
	return id
}

func (f *fetchFix) pass() {
	f.t.Helper()
	if err := f.svc.FetchPass(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fetchFix) row(c Channel, e int) fetchRow {
	f.t.Helper()
	rows, err := fetchRows(context.Background(), f.db, `channel_id=? AND episode=?`, c.ID, e)
	if err != nil || len(rows) != 1 {
		f.t.Fatalf("row of E%d: %v %v", e, rows, err)
	}
	return rows[0]
}

// placeholderOf is the first schedule entry of episode e not on disk.
func (f *fetchFix) placeholderOf(c Channel, e int) (Entry, bool) {
	r := f.row(c, e)
	for _, x := range f.entries(c.ID) {
		if x.FetchID == r.ID {
			return x, true
		}
	}
	return Entry{}, false
}

func TestFetchNormalize(t *testing.T) {
	r := Rules{Order: OrderShuffle, Extras: true, Picks: []Pick{{Kind: PickTitle, ID: 1}},
		Fetch: &FetchRules{Titles: []FetchTitle{{Kind: "movie", TMDBID: 5, Season: 3}, {Kind: "movie", TMDBID: 5}}}}
	if err := normalizeFetch(&r); err != nil {
		t.Fatal(err)
	}
	if r.Order != OrderOrdered || r.Extras || r.Picks != nil || len(r.Fetch.Titles) != 1 || r.Fetch.Titles[0].Season != 0 {
		t.Fatalf("normalized: %+v %+v", r, r.Fetch)
	}
	bad := Rules{Fetch: &FetchRules{Titles: []FetchTitle{{Kind: "album", TMDBID: 1}}}}
	if err := normalizeFetch(&bad); err == nil {
		t.Fatal("an album was taken")
	}
	empty := Rules{Fetch: &FetchRules{}}
	if err := normalizeFetch(&empty); err != nil || empty.Fetch != nil {
		t.Fatalf("empty fetch: %+v %v", empty.Fetch, err)
	}
}

// The schedule has programmes not on disk, but never before their lead
// time: the slots before it take what is on disk; the addon is asked for
// one once its lead time comes.
func TestFetchScheduleWithMissing(t *testing.T) {
	f, c := newFetchFix(t, 3, []int{1}, FetchRules{})
	f.pass()
	es := f.entries(c.ID)
	if len(es) == 0 || es[0].ItemID != f.eps[1] || !es[0].Start.Equal(f.now) {
		t.Fatalf("first entry: %+v", es)
	}
	ph, ok := f.placeholderOf(c, 2)
	if !ok {
		t.Fatalf("E2 never scheduled: %+v", es)
	}
	// lead: 15 + 15 minutes and 1.62 GB at 2 MB/s ×1.5 ≈ 20 min, plus the late margin
	if ph.Start.Sub(f.now) < 55*time.Minute {
		t.Fatalf("E2 at %v: before its lead time", ph.Start.Sub(f.now))
	}
	if es[1].ItemID != f.eps[1] {
		t.Fatalf("the slot before E2's lead time is not E1 again: %+v", es[1])
	}
	if r := f.row(c, 2); r.State != fetchWanted {
		t.Fatalf("E2 asked for too early: %s", r.State)
	}
	// once its lead time comes, it is asked for and the addon told
	f.now = ph.Start.Add(-60 * time.Minute)
	f.pass()
	if r := f.row(c, 2); r.State != fetchRequested {
		t.Fatalf("E2 not asked for at its lead time: %s", r.State)
	}
	last := f.fetcher.wants[len(f.fetcher.wants)-1]
	if len(last) == 0 || last[0].Key != fetchKey(library.KindEpisode, 100, 1, 2) {
		t.Fatalf("the addon was told %+v", last)
	}
}

// A fetch that turns late gives its slot to an available programme: the
// schedule is cut there and built again, the programme on air untouched.
func TestFetchLateFallsBack(t *testing.T) {
	f, c := newFetchFix(t, 3, []int{1}, FetchRules{})
	f.pass()
	ph, _ := f.placeholderOf(c, 2)
	f.now = ph.Start.Add(-60 * time.Minute)
	f.pass() // asked for
	onAir, err := f.st.At(context.Background(), c.ID, f.now)
	if err != nil {
		t.Fatal(err)
	}
	// the addon finds a release of 20 GB, barely started
	f.fetcher.states[fetchKey(library.KindEpisode, 100, 1, 2)] = FetchState{State: FetchDownloading, Size: 20 << 30, Done: 1 << 20, Rate: 1 << 20}
	f.pass() // learns the size
	f.pass() // judges with it
	if again, ok := f.placeholderOf(c, 2); ok && !again.Start.After(ph.Start) {
		t.Fatalf("E2 still at its slot though it will be late: %+v", again)
	}
	now, err := f.st.At(context.Background(), c.ID, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if now.Seq != onAir.Seq || !now.Start.Equal(onAir.Start) || now.ItemID != onAir.ItemID {
		t.Fatalf("the programme on air changed: %+v → %+v", onAir, now)
	}
	// the slot is filled with what is on disk
	es, _ := f.st.Range(context.Background(), c.ID, ph.Start, ph.Start.Add(time.Minute))
	if len(es) != 1 || es[0].FetchID != 0 {
		t.Fatalf("E2's old slot: %+v", es)
	}
	if r := f.row(c, 2); r.State != fetchRequested {
		t.Fatalf("a late fetch stops: %s", r.State)
	}
}

// A programme not on disk at its slot (the server was off) is never
// played: Program cuts it and airs what is available.
func TestFetchProgramSkipsMissing(t *testing.T) {
	f, c := newFetchFix(t, 3, []int{1}, FetchRules{})
	f.pass()
	ph, _ := f.placeholderOf(c, 2)
	f.now = ph.Start.Add(time.Minute)
	p, err := f.svc.Program(context.Background(), c.ID, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Path == "" || p.Entry.FetchID != 0 {
		t.Fatalf("played a programme not on disk: %+v", p)
	}
}

// What arrives takes its place with its file; after airing and the grace
// period it is removed — but nothing the library had before, and nothing
// someone keeps.
func TestFetchArrivesAndCleansUp(t *testing.T) {
	f, c := newFetchFix(t, 2, []int{1}, FetchRules{GraceHours: 2})
	f.pass()
	ph, _ := f.placeholderOf(c, 2)
	f.now = ph.Start.Add(-60 * time.Minute)
	f.pass()
	e2 := f.arrive(2)
	f.pass()
	r := f.row(c, 2)
	if r.State != fetchReady || r.ItemID != e2 {
		t.Fatalf("E2 arrived: %+v", r)
	}
	var aired *Entry
	for _, x := range f.entries(c.ID) {
		if x.ItemID == e2 && x.FileID != 0 {
			x := x
			aired = &x
			break
		}
	}
	if aired == nil {
		t.Fatalf("E2 not scheduled with its file: %+v", f.entries(c.ID))
	}
	if _, ok := f.placeholderOf(c, 2); ok {
		t.Fatal("E2's placeholder is still there")
	}
	// it aired; the channel is switched off (nothing ahead any more)
	f.now = aired.End
	f.exec(`UPDATE channels SET enabled=0 WHERE id=?`, c.ID)
	f.pass()
	f.now = aired.End.Add(time.Hour)
	f.pass()
	if len(f.removed) != 0 {
		t.Fatal("removed within the grace period")
	}
	f.now = aired.End.Add(2*time.Hour + time.Minute)
	f.pass()
	if len(f.removed) != 1 || f.removed[0] != e2 {
		t.Fatalf("removed %v, want [%d]", f.removed, e2)
	}
	f.pass() // the drop goes out with the next talk
	if len(f.fetcher.drops) == 0 {
		t.Fatal("the addon was not told to drop it")
	}
	for _, id := range f.removed {
		if id == f.eps[1] {
			t.Fatal("removed what the library had before")
		}
	}
}

func TestFetchCleanupKeeps(t *testing.T) {
	for _, keeper := range []string{"progress", "watchlist", "mark", "ahead", "addon"} {
		t.Run(keeper, func(t *testing.T) {
			f, c := newFetchFix(t, 2, []int{1}, FetchRules{GraceHours: 1})
			f.pass()
			ph, _ := f.placeholderOf(c, 2)
			f.now = ph.Start.Add(-60 * time.Minute)
			f.pass()
			e2 := f.arrive(2)
			f.pass()
			u := f.user("u")
			switch keeper {
			case "progress":
				f.exec(`INSERT INTO progress (user_id, item_id, position_ms, updated_at) VALUES (?,?,60000,'x')`, u, e2)
			case "watchlist":
				f.exec(`INSERT INTO watchlist (user_id, item_id, added_at) VALUES (?,?,'x')`, u, f.series)
			case "mark":
				tt := f.exec(`INSERT INTO tracked_titles (kind, tmdb_id, name) VALUES ('series', 100, 'S')`)
				f.exec(`INSERT INTO title_marks (user_id, title_id, state, updated_at) VALUES (?,?,'watching','x')`, u, tt)
			case "addon":
				f.fetcher.states[fetchKey(library.KindEpisode, 100, 1, 2)] = FetchState{State: FetchDone, Keep: true}
			}
			var end time.Time
			for _, x := range f.entries(c.ID) {
				if x.ItemID == e2 {
					end = x.End
				}
			}
			f.now = end.Add(2 * time.Hour)
			if keeper != "ahead" {
				f.exec(`UPDATE channels SET enabled=0 WHERE id=?`, c.ID)
			}
			f.pass()
			f.pass()
			if keeper == "ahead" {
				// the marathon comes round to it again within the schedule
				if len(f.removed) != 0 {
					t.Fatalf("removed though scheduled ahead: %v", f.removed)
				}
				return
			}
			if len(f.removed) != 0 {
				t.Fatalf("removed though kept by %s", keeper)
			}
			// kept: no longer the channel's (it left the order, so the row goes)
			if rows, _ := fetchRows(context.Background(), f.db, `channel_id=? AND episode=2 AND state<>?`, c.ID, fetchKept); len(rows) != 0 {
				t.Fatalf("state %s, want kept", rows[0].State)
			}
		})
	}
}

// A file someone else put there (not what arrived for the channel) keeps
// the item.
func TestFetchCleanupForeignFile(t *testing.T) {
	f, c := newFetchFix(t, 2, []int{1}, FetchRules{GraceHours: 1})
	f.pass()
	ph, _ := f.placeholderOf(c, 2)
	f.now = ph.Start.Add(-60 * time.Minute)
	f.pass()
	e2 := f.arrive(2)
	f.pass()
	f.file(e2, 45) // a second copy, not the channel's
	f.exec(`UPDATE channels SET enabled=0 WHERE id=?`, c.ID)
	f.now = f.now.Add(48 * time.Hour)
	f.pass()
	if len(f.removed) != 0 {
		t.Fatalf("removed with a foreign file: %v", f.removed)
	}
}

// Nothing is scheduled or asked for beyond the disk budget.
func TestFetchBudget(t *testing.T) {
	f, c := newFetchFix(t, 3, []int{1}, FetchRules{BudgetGB: 1})
	f.pass()
	if _, ok := f.placeholderOf(c, 2); ok {
		t.Fatal("E2 (1.6 GB) scheduled within a 1 GB budget")
	}
	for e := 2; e <= 3; e++ {
		if r := f.row(c, e); r.State != fetchWanted {
			t.Fatalf("E%d asked for beyond the budget: %s", e, r.State)
		}
	}
	// the global budget bounds a generous channel too
	if err := f.st.SetFetchGlobalGB(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	f2, c2 := newFetchFix(t, 3, []int{1}, FetchRules{BudgetGB: 100})
	if err := f2.st.SetFetchGlobalGB(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	f2.pass()
	if _, ok := f2.placeholderOf(c2, 2); ok {
		t.Fatal("E2 scheduled beyond the global budget")
	}
}

// A channel with nothing on disk asks for its first programmes at once.
func TestFetchBootstrap(t *testing.T) {
	f, c := newFetchFix(t, 4, nil, FetchRules{})
	f.pass()
	got := 0
	for e := 1; e <= 4; e++ {
		if f.row(c, e).State == fetchRequested {
			got++
		}
	}
	if got != fetchBootstrap {
		t.Fatalf("%d asked for, want %d", got, fetchBootstrap)
	}
	if f.row(c, 1).State != fetchRequested {
		t.Fatal("not the first in order")
	}
}

func TestFetchSpeedLearnt(t *testing.T) {
	f, _ := newFetchFix(t, 1, nil, FetchRules{})
	ctx := context.Background()
	if err := f.st.sawFetchSpeed(ctx, 10_000_000); err != nil {
		t.Fatal(err)
	}
	if v := f.st.fetchSpeed(ctx); v <= defaultFetchSpeed || v >= 10_000_000 {
		t.Fatalf("speed %v", v)
	}
}
