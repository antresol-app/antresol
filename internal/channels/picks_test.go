package channels

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func (f *fixture) fileAt(item int64, path string, minutes int) int64 {
	return f.exec(`INSERT INTO files (item_id, path, duration_ms) VALUES (?,?,?)`, item, path, int64(minutes)*60000)
}

// picksLib: movies under /lib/movies, a series under /lib/series/Show
// whose season 2 files are named so that only a natural order is right.
type picksLib struct {
	movies []int64 // A, B, C (12+), X (18+)
	series int64
	s1, s2 []int64 // episodes of season 1 and 2 in episode order
}

func newPicksLib(f *fixture) picksLib {
	var l picksLib
	for _, m := range []struct {
		t, r string
	}{{"A", "12+"}, {"B", "12+"}, {"C", "12+"}, {"X", "18+"}} {
		id := f.item("movie", m.t, 0, itemOpt{year: 2000, rating: m.r})
		f.fileAt(id, "/lib/movies/"+m.t+" (2000)/"+m.t+".mkv", 90)
		l.movies = append(l.movies, id)
	}
	l.series = f.item("series", "Show", 0, itemOpt{rating: "12+"})
	for sn, names := range [][]string{{"Show 1.mkv", "Show 2.mkv", "Show 3.mkv"}, {"Ep 2.mkv", "Ep 9.mkv", "Ep 10.mkv"}} {
		se := f.exec(`INSERT INTO items (kind, parent_id, title, season_no) VALUES ('season',?,?,?)`, l.series, fmt.Sprintf("Season %d", sn+1), sn+1)
		for en, name := range names {
			e := f.exec(`INSERT INTO items (kind, parent_id, title, season_no, episode_no) VALUES ('episode',?,?,?,?)`, se, fmt.Sprintf("E%d", en+1), sn+1, en+1)
			f.fileAt(e, fmt.Sprintf("/lib/series/Show/Season %d/%s", sn+1, name), 30)
			if sn == 0 {
				l.s1 = append(l.s1, e)
			} else {
				l.s2 = append(l.s2, e)
			}
		}
	}
	return l
}

func (f *fixture) picksBuilder() *Builder {
	b := f.builder()
	b.Catalog.Roots = LibraryRoots("/lib/movies", "/lib/series/")
	return b
}

func itemsOf(es []Entry) []int64 {
	var out []int64
	for i, e := range es {
		if i > 0 && es[i-1].ItemID == e.ItemID && es[i-1].End.Equal(e.Start) {
			continue // a programme's parts
		}
		out = append(out, e.ItemID)
	}
	return out
}

func TestPicksNormalize(t *testing.T) {
	c := Channel{Name: "x", Rules: Rules{Titles: []int64{5, 6}, Picks: []Pick{
		{Kind: PickFolder, Path: "/series/Show/Season 2/"}, {Kind: PickItem, ID: 9, Path: "junk"}, {Kind: PickTitle, ID: 6},
	}}}
	if err := normalize(&c); err != nil {
		t.Fatal(err)
	}
	want := []Pick{{Kind: PickFolder, Path: "series/Show/Season 2"}, {Kind: PickItem, ID: 9}, {Kind: PickTitle, ID: 6}, {Kind: PickTitle, ID: 5}}
	if fmt.Sprint(c.Rules.Picks) != fmt.Sprint(want) || c.Rules.Titles != nil {
		t.Fatalf("picks %v titles %v", c.Rules.Picks, c.Rules.Titles)
	}
	for _, bad := range []Pick{{Kind: "song", ID: 1}, {Kind: PickTitle}, {Kind: PickFolder, Path: "series/../../etc"},
		{Kind: PickFolder, Path: ""}, {Kind: PickFolder, Path: "a//b"}, {Kind: PickFolder, Path: `a\..\b`}} {
		c := Channel{Name: "x", Rules: Rules{Picks: []Pick{bad}}}
		if err := normalize(&c); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	// a stored round 1 channel reads as picks
	f := newFixture(t)
	id := f.exec(`INSERT INTO channels (number, name, category, rules_json, enabled, edited, seed, created_at, updated_at) VALUES (1,'old','', '{"titles":[3,4],"order":"ordered"}',1,0,1,'','')`)
	got, err := f.st.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got.Rules.Picks) != fmt.Sprint([]Pick{{Kind: PickTitle, ID: 3}, {Kind: PickTitle, ID: 4}}) || got.Rules.Titles != nil {
		t.Fatalf("read %+v", got.Rules)
	}
}

// Ordered picks play exactly in the list's order, each in its natural
// order, round and round; missing files, files without a duration and
// titles above the channel's limit are left out.
func TestPicksOrderedMixedKinds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l := newPicksLib(f)
	// a file of the folder that is gone, and one never probed
	gone := f.item("episode", "Gone", 0, itemOpt{})
	f.exec(`UPDATE items SET parent_id=(SELECT parent_id FROM items WHERE id=?) WHERE id=?`, l.s2[0], gone)
	gf := f.fileAt(gone, "/lib/series/Show/Season 2/Ep 5.mkv", 30)
	f.exec(`UPDATE files SET missing_since='2026-11-01' WHERE id=?`, gf)
	nodur := f.item("episode", "NoDur", 0, itemOpt{})
	f.exec(`UPDATE items SET parent_id=(SELECT parent_id FROM items WHERE id=?) WHERE id=?`, l.s2[0], nodur)
	f.exec(`INSERT INTO files (item_id, path) VALUES (?, '/lib/series/Show/Season 2/Ep 6.mkv')`, nodur)

	c := f.create(Channel{Name: "Mix", Rules: Rules{Order: OrderOrdered, MaxRating: "16+", Picks: []Pick{
		{Kind: PickFolder, Path: "series/Show/Season 2"},
		{Kind: PickItem, ID: l.s1[1]},
		{Kind: PickTitle, ID: l.movies[3]}, // 18+: out
		{Kind: PickTitle, ID: l.movies[1]},
		{Kind: PickFolder, Path: "movies/nothing here"},
	}}})
	b := f.picksBuilder()
	b.Horizon = 10 * time.Hour
	if err := b.Build(ctx, c); err != nil {
		t.Fatal(err)
	}
	got := itemsOf(f.entries(c.ID))
	cycle := []int64{l.s2[0], l.s2[1], l.s2[2], l.s1[1], l.movies[1]}
	if len(got) < 2*len(cycle) {
		t.Fatalf("entries %v", got)
	}
	for i, id := range got {
		if id != cycle[i%len(cycle)] {
			t.Fatalf("programme %d is %d, want %d (got %v)", i, id, cycle[i%len(cycle)], got)
		}
	}
	// the same item in two picks: the order still goes on from the right one
	f.now = f.now.Add(15 * time.Minute) // the folder's first on air
	c.Rules.Picks = []Pick{{Kind: PickItem, ID: l.s2[0]}, {Kind: PickTitle, ID: l.movies[0]}, {Kind: PickFolder, Path: "series/Show/Season 2"}}
	c, err := f.st.Update(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Build(ctx, c); err != nil {
		t.Fatal(err)
	}
	got = itemsOf(f.entries(c.ID))
	// on air: s2[0] of the folder (not the item pick of it): the folder goes on
	want := []int64{l.s2[0], l.s2[1], l.s2[2], l.s2[0], l.movies[0], l.s2[0], l.s2[1]}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("after edit %v, want prefix %v", got, want)
		}
	}
}

// Shuffled picks: each pick is a unit, a folder plays in order within
// itself.
func TestPicksShuffleUnits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l := newPicksLib(f)
	c := f.create(Channel{Name: "Sh", Rules: Rules{Picks: []Pick{
		{Kind: PickFolder, Path: "series/Show/Season 2"}, {Kind: PickTitle, ID: l.movies[0]},
		{Kind: PickTitle, ID: l.movies[1]}, {Kind: PickItem, ID: l.movies[2]}, {Kind: PickTitle, ID: l.series},
	}}})
	b := f.picksBuilder()
	b.Horizon = 72 * time.Hour
	if err := b.Build(ctx, c); err != nil {
		t.Fatal(err)
	}
	es := f.entries(c.ID)
	units := map[int64]bool{}
	next := map[int64]int{}
	order := map[int64][]int64{folderUnit("series/Show/Season 2"): l.s2, l.series: append(append([]int64{}, l.s1...), l.s2...)}
	for _, e := range es {
		units[e.UnitID] = true
		if want, ok := order[e.UnitID]; ok {
			if e.ItemID != want[next[e.UnitID]%len(want)] {
				t.Fatalf("unit %d plays %d out of order", e.UnitID, e.ItemID)
			}
			next[e.UnitID]++
		}
	}
	if len(units) != 5 {
		t.Fatalf("units %v", units)
	}
	// the same seed and pool make the same schedule
	f2 := newFixture(t)
	newPicksLib(f2)
	c2 := f2.create(Channel{Name: "Sh", Rules: c.Rules})
	b2 := f2.picksBuilder()
	b2.Horizon = 72 * time.Hour
	if err := b2.Build(ctx, c2); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(itemsOf(f2.entries(c2.ID))) != fmt.Sprint(itemsOf(es)) {
		t.Fatal("not deterministic")
	}
}

// A folder of a series is the series: it is not on air while another
// channel shows the series.
func TestPicksNoTitleOnTwoChannels(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l := newPicksLib(f)
	marathon := f.create(Channel{Name: "Show", Rules: Rules{Order: OrderOrdered, Titles: []int64{l.series}}})
	mix := f.create(Channel{Name: "Mix", Rules: Rules{Picks: []Pick{
		{Kind: PickFolder, Path: "series/Show"}, {Kind: PickItem, ID: l.s1[0]}, {Kind: PickTitle, ID: l.movies[0]}, {Kind: PickTitle, ID: l.movies[1]},
	}}})
	b := f.picksBuilder()
	for _, c := range []Channel{marathon, mix} {
		if err := b.Build(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	show := map[int64]bool{}
	for _, e := range append(append([]int64{}, l.s1...), l.s2...) {
		show[e] = true
	}
	es := f.entries(mix.ID)
	if len(es) == 0 {
		t.Fatal("nothing built")
	}
	for _, e := range es {
		if show[e.ItemID] {
			t.Fatalf("the series on two channels: %+v", e)
		}
	}
}

func TestBrowseFolders(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l := newPicksLib(f)
	cat := Catalog{DB: f.db, Roots: LibraryRoots("/lib/movies", "/lib/series")}
	top, err := cat.Browse(ctx, "", "")
	if err != nil || fmt.Sprint(top.Folders) != "[{movies movies} {series series}]" {
		t.Fatalf("roots %+v %v", top, err)
	}
	s, err := cat.Browse(ctx, "series/Show", "")
	if err != nil || fmt.Sprint(s.Folders) != "[{series/Show/Season 1 Season 1} {series/Show/Season 2 Season 2}]" || len(s.Files) != 0 {
		t.Fatalf("show %+v %v", s, err)
	}
	s2, err := cat.Browse(ctx, "/series/Show/Season 2/", "")
	if err != nil || s2.Path != "series/Show/Season 2" || len(s2.Files) != 3 ||
		s2.Files[0].Name != "Ep 2.mkv" || s2.Files[1].Name != "Ep 9.mkv" || s2.Files[2].ID != l.s2[2] || s2.Files[2].Kind != "episode" {
		t.Fatalf("season %+v %v", s2, err)
	}
	m, _ := cat.Browse(ctx, "movies", "12+")
	if len(m.Folders) != 3 { // X is 18+
		t.Fatalf("limited %+v", m)
	}
	m, _ = cat.Browse(ctx, "movies", "")
	if len(m.Folders) != 4 {
		t.Fatalf("all %+v", m)
	}
	for _, p := range []string{"..", "../etc", "series/../../etc", "series/Show/..", `series\..\x`} {
		if _, err := cat.Browse(ctx, p, ""); !errors.Is(err, ErrBadPath) {
			t.Fatalf("%q: %v", p, err)
		}
	}
	for _, p := range []string{"etc", "series/Nope"} {
		if _, err := cat.Browse(ctx, p, ""); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%q: %v", p, err)
		}
	}
}

func TestSearchOptions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l := newPicksLib(f)
	for _, id := range append([]int64{l.series}, l.movies...) {
		f.exec(`INSERT INTO item_titles (item_id, title, norm) SELECT id, title, lower(title) FROM items WHERE id=?`, id)
	}
	cat := Catalog{DB: f.db}
	hits, err := cat.Search(ctx, "show", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 7 || hits[0].Kind != "series" || hits[0].Pick != (Pick{Kind: PickTitle, ID: l.series}) ||
		hits[1].Kind != "episode" || hits[1].ID != l.s1[0] || hits[1].Subtitle != "Show · S1 E1" || hits[1].Pick != (Pick{Kind: PickItem, ID: l.s1[0]}) {
		t.Fatalf("hits %+v", hits)
	}
	if hits, _ := cat.Search(ctx, "x", "12+"); len(hits) != 0 {
		t.Fatalf("limited %+v", hits)
	}
	if hits, _ := cat.Search(ctx, "x", ""); len(hits) != 1 || hits[0].Kind != "movie" {
		t.Fatalf("x %+v", hits)
	}
	if hits, _ := cat.Search(ctx, "show", "6+"); len(hits) != 0 {
		t.Fatalf("show 6+ %+v", hits)
	}
	if hits, _ := cat.Search(ctx, "e2", ""); len(hits) != 2 { // episodes by their own title
		t.Fatalf("e2 %+v", hits)
	}
}

func TestDescribePicks(t *testing.T) {
	f := newFixture(t)
	l := newPicksLib(f)
	cat := Catalog{DB: f.db, Roots: LibraryRoots("/lib/movies", "/lib/series")}
	got, err := cat.DescribePicks(context.Background(), Rules{MaxRating: "12+", Picks: []Pick{
		{Kind: PickFolder, Path: "series/Show/Season 2"}, {Kind: PickItem, ID: l.s1[1]}, {Kind: PickTitle, ID: l.movies[3]}, {Kind: PickTitle, ID: 9999},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].Title != "Season 2" || got[0].Missing || got[1].Title != "E2" || got[1].Subtitle != "Show · S1 E2" ||
		got[2].Title != "X" || !got[2].Missing || !got[3].Missing {
		t.Fatalf("%+v", got)
	}
}
