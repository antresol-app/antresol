package channels

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPoolFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	com := f.movie("Comedy 90s", 90, itemOpt{year: 1994, rating: "12+", genres: []string{"Комедия"}})
	f.movie("Drama 90s", 100, itemOpt{year: 1995, rating: "16+", genres: []string{"драма"}})
	en := f.movie("Comedy en 80s", 95, itemOpt{year: 1985, rating: "PG", genres: []string{"Comedy"}})
	noDur := f.item("movie", "No duration", 0, itemOpt{genres: []string{"комедия"}})
	f.exec(`INSERT INTO files (item_id, path) VALUES (?, '/m/nodur.mkv')`, noDur)
	s, _ := f.series("Show", 2, 3, 30, itemOpt{year: 2001, rating: "12+", genres: []string{"комедия"}})
	gone := f.movie("Gone", 90, itemOpt{genres: []string{"комедия"}})
	f.exec(`UPDATE items SET missing_since='2026-01-01' WHERE id=?`, gone)

	cat := Catalog{DB: f.db}
	ids := func(r Rules) []int64 {
		p, err := cat.Pool(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, u := range p.Units {
			out = append(out, u.ID)
		}
		return out
	}
	eq := func(got, want []int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	}
	eq(ids(Rules{Genres: []string{"комедия"}}), []int64{com, en, s})
	eq(ids(Rules{Genres: []string{"Comedy"}, Kinds: []string{"movie"}}), []int64{com, en})
	eq(ids(Rules{YearFrom: 1990, YearTo: 1999}), []int64{com, com + 1})
	eq(ids(Rules{MaxRating: "12+", Kinds: []string{"movie"}}), []int64{com, en})
	eq(ids(Rules{Titles: []int64{s, en}}), []int64{en, s})

	owner := f.user("ann")
	f.exec(`INSERT INTO item_owners (item_id, user_id) VALUES (?,?)`, s, owner)
	eq(ids(Rules{LibraryUser: owner}), []int64{s})

	p, _ := cat.Pool(ctx, Rules{Titles: []int64{s}})
	if n := len(p.Progs[s]); n != 6 {
		t.Fatalf("episodes: %d", n)
	}
}

func TestOrderedMarathonPlaysEpisodesInOrderAndWraps(t *testing.T) {
	f := newFixture(t)
	s, eps := f.series("Show", 2, 2, 30, itemOpt{})
	// season 0 is left out
	se0 := f.exec(`INSERT INTO items (kind, parent_id, title, season_no) VALUES ('season',?,'Specials',0)`, s)
	sp := f.exec(`INSERT INTO items (kind, parent_id, title, season_no, episode_no) VALUES ('episode',?,'Sp',0,1)`, se0)
	f.file(sp, 30)
	c := f.create(Channel{Name: "Show", Rules: Rules{Titles: []int64{s}, Order: OrderOrdered}})
	b := f.builder()
	b.Horizon = 5 * time.Hour
	if err := b.Build(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	es := f.entries(c.ID)
	if len(es) != 10 {
		t.Fatalf("entries: %d", len(es))
	}
	for i, e := range es {
		if e.ItemID != eps[i%4] {
			t.Fatalf("entry %d plays %d, want %d", i, e.ItemID, eps[i%4])
		}
		if i == 0 && !e.Start.Equal(f.now) {
			t.Fatalf("starts %v", e.Start)
		}
		if i > 0 && !e.Start.Equal(es[i-1].End) {
			t.Fatalf("gap before %d", i)
		}
	}
}

func TestShuffleIsDeterministicWithoutQuickRepeatsAndSeriesInOrder(t *testing.T) {
	build := func() []Entry {
		f := newFixture(t)
		for i := 0; i < 9; i++ {
			f.movie("M", 60, itemOpt{genres: []string{"драма"}})
		}
		f.series("S", 1, 5, 60, itemOpt{genres: []string{"драма"}})
		c := f.create(Channel{Name: "Drama", Rules: Rules{Genres: []string{"драма"}}})
		b := f.builder()
		b.Horizon = 40 * time.Hour
		if err := b.Build(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		return f.entries(c.ID)
	}
	a, b := build(), build()
	if len(a) != 40 || len(b) != 40 {
		t.Fatalf("entries: %d %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ItemID != b[i].ItemID {
			t.Fatalf("not deterministic at %d", i)
		}
	}
	// 10 units, window 6: a unit never comes back within 6 picks
	last := map[int64]int{}
	var series []int64
	for i, e := range a {
		if j, ok := last[e.UnitID]; ok && i-j <= 6 {
			t.Fatalf("unit %d again after %d picks", e.UnitID, i-j)
		}
		last[e.UnitID] = i
		if e.UnitID != e.ItemID {
			series = append(series, e.ItemID)
		}
	}
	if len(series) < 2 {
		t.Fatalf("series played %d times", len(series))
	}
	for i := 1; i < len(series); i++ {
		if series[i] != series[i-1]+1 && series[i] >= series[i-1] { // episode ids are consecutive within the season
			t.Fatalf("episodes out of order: %v", series)
		}
	}
}

func TestBuildExtendsKeepsHistoryAndTrims(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s, eps := f.series("S", 1, 3, 60, itemOpt{})
	c := f.create(Channel{Name: "S", Rules: Rules{Titles: []int64{s}, Order: OrderOrdered}})
	b := f.builder()
	b.Horizon, b.Keep = 2*time.Hour, time.Hour
	if err := b.Build(ctx, c); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(3 * time.Hour) // e1, e2 aired; e1 ended 2 h ago
	if err := b.Build(ctx, c); err != nil {
		t.Fatal(err)
	}
	es := f.entries(c.ID)
	// e1 trimmed (ended 2 h ago > keep); e2 kept; then e3 (now…) and e1 again
	if len(es) < 3 || es[0].ItemID != eps[1] {
		t.Fatalf("after trim: %+v", es)
	}
	cur, err := f.st.At(ctx, c.ID, f.now.Add(time.Minute))
	if err != nil || cur.ItemID != eps[2] && cur.ItemID != eps[0] {
		t.Fatalf("on air: %+v %v", cur, err)
	}
}

func TestEditCutsAfterCurrentAndMissingFileCuts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.movie("A", 60, itemOpt{})
	bb := f.movie("B", 60, itemOpt{})
	c := f.create(Channel{Name: "X", Rules: Rules{Titles: []int64{a}, Order: OrderOrdered}})
	b := f.builder()
	b.Horizon = 4 * time.Hour
	if err := b.Build(ctx, c); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(30 * time.Minute)
	c.Rules.Titles = []int64{bb}
	if _, err := f.st.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := b.Build(ctx, c); err != nil {
		t.Fatal(err)
	}
	es := f.entries(c.ID)
	if es[0].ItemID != a || es[1].ItemID != bb {
		t.Fatalf("after edit: %+v", es)
	}
	// B's file disappears: cut from its first programme, rebuilt with nothing
	f.exec(`UPDATE files SET missing_since='x' WHERE item_id=?`, bb)
	cut, err := f.st.CutMissing(ctx, c.ID)
	if err != nil || !cut {
		t.Fatalf("cut %v %v", cut, err)
	}
	if es := f.entries(c.ID); len(es) != 1 {
		t.Fatalf("after cut: %+v", es)
	}
	if _, err := f.st.At(ctx, c.ID, f.now.Add(time.Hour)); !errors.Is(err, ErrNothing) {
		t.Fatalf("at: %v", err)
	}
}

func TestVisibility(t *testing.T) {
	sh := Channel{Enabled: true}
	kid := Channel{Enabled: true, Rules: Rules{MaxRating: "6+"}}
	mine := Channel{Enabled: true, UserID: 7}
	adult := Viewer{ID: 7}
	child := Viewer{ID: 8, MaxRating: "12+"}
	if !VisibleTo(sh, adult) || VisibleTo(sh, child) || !VisibleTo(kid, child) || !VisibleTo(mine, adult) || VisibleTo(mine, child) {
		t.Fatal("visibility")
	}
	if VisibleTo(Channel{Enabled: false}, adult) {
		t.Fatal("disabled visible")
	}
	if !CanEdit(mine, adult) || CanEdit(sh, adult) || !CanEdit(sh, Viewer{Admin: true}) {
		t.Fatal("edit rights")
	}
}

func TestCreateNumbersAndValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.create(Channel{Name: "A"})
	b := f.create(Channel{Name: "B"})
	if a.Number != ManualFrom || b.Number != ManualFrom+1 {
		t.Fatalf("numbers %d %d", a.Number, b.Number)
	}
	if _, err := f.st.Create(ctx, Channel{Name: "C", Number: a.Number}); !errors.Is(err, ErrNumberTaken) {
		t.Fatalf("dup number: %v", err)
	}
	if _, err := f.st.Create(ctx, Channel{Name: " "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := f.st.Create(ctx, Channel{Name: "x", Rules: Rules{Kinds: []string{"track"}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("kind: %v", err)
	}
	g := f.create(Channel{Name: "G", AutoKey: "movies"})
	if err := f.st.Delete(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.st.Get(ctx, g.ID); got.Enabled || !got.Edited {
		t.Fatalf("generated channel after delete: %+v", got)
	}
	if err := f.st.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

// A title with several genres is never on two channels at the same time
// (owner's report: «Комедии» and «Драмы» both showed one film).
func TestBuildNoTitleOnTwoChannelsAtOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		f.movie("Both "+itoa(int64(i)), 90+i*7, itemOpt{genres: []string{"комедия", "драма"}})
	}
	com := f.create(Channel{Name: "Комедии", Rules: Rules{Genres: []string{"комедия"}}, Seed: 7})
	dra := f.create(Channel{Name: "Драмы", Rules: Rules{Genres: []string{"драма"}}, Seed: 7})
	b := f.builder()
	for _, c := range []Channel{com, dra} {
		if err := b.Build(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	a, d := f.entries(com.ID), f.entries(dra.ID)
	if len(a) == 0 || len(d) == 0 {
		t.Fatal("nothing built")
	}
	for _, x := range a {
		for _, y := range d {
			if x.UnitID == y.UnitID && x.Start.Before(y.End) && y.Start.Before(x.End) {
				t.Fatalf("unit %d on both channels: %v–%v and %v–%v", x.UnitID, x.Start, x.End, y.Start, y.End)
			}
		}
	}
}
