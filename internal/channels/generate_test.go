package channels

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGeneratorSync(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		f.movie("C", 90, itemOpt{year: 1991 + i, rating: "6+", genres: []string{"Комедия", "семейный"}})
	}
	f.movie("D", 90, itemOpt{year: 2005, genres: []string{"драма"}})
	s, _ := f.series("Long", 1, 8, 20, itemOpt{year: 2010, rating: "0+", genres: []string{"Kids"}})
	f.series("Short", 1, 3, 20, itemOpt{})
	ann := f.user("ann")
	for _, id := range []int64{1, 2, 3, 4, 5, 6} {
		f.exec(`INSERT INTO item_owners (item_id, user_id) VALUES (?,?)`, id, ann)
	}
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}, MinRuntime: time.Nanosecond}
	res, err := g.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := f.st.All(ctx)
	by := map[string]Channel{}
	for _, c := range all {
		by[c.AutoKey] = c
	}
	for _, key := range []string{"movies", "genre:комедия", "genre:семейный", "decade:1990", "kids", "series:" + itoa(s), "mine:" + itoa(ann)} {
		if _, ok := by[key]; !ok {
			t.Errorf("no channel %s (have %v)", key, keys(by))
		}
	}
	for _, key := range []string{"series", "genre:драма", "decade:2000", "docs"} {
		if _, ok := by[key]; ok {
			t.Errorf("unexpected channel %s", key)
		}
	}
	if res.Created != len(all) {
		t.Fatalf("created %d of %d", res.Created, len(all))
	}
	if c := by["genre:комедия"]; c.Name != "Комедии" || c.Category != CatGenres || c.Number != 2 {
		t.Fatalf("comedy: %+v", c)
	}
	// one list 1…N in the plan's order
	nums := map[int]bool{}
	for _, c := range all {
		nums[c.Number] = true
	}
	for n := 1; n <= len(all); n++ {
		if !nums[n] {
			t.Fatalf("numbers are not 1…%d: %v", len(all), nums)
		}
	}
	if c := by["decade:1990"]; c.Name != "Кино 90-х" {
		t.Fatalf("decade: %+v", c)
	}
	if c := by["mine:"+itoa(ann)]; c.UserID != ann || c.Rules.LibraryUser != ann {
		t.Fatalf("mine: %+v", c)
	}
	if c := by["kids"]; c.Rules.MaxRating != "6+" {
		t.Fatalf("kids: %+v", c)
	}
	// a second run changes nothing
	if res, _ := g.Sync(ctx); res != (SyncResult{}) {
		t.Fatalf("second run: %+v", res)
	}
	// an edited channel is left alone; one that lost its content is dropped
	// (not remembered as deleted: it comes back with its content)
	c := by["genre:комедия"]
	c.Name, c.Edited = "Смешное", true
	if _, err := f.st.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE files SET missing_since='x' WHERE item_id IN (SELECT id FROM items WHERE kind='episode')`)
	res, err = g.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.Get(ctx, by["series:"+itoa(s)].ID); !errors.Is(err, ErrNotFound) || res.Dropped == 0 {
		t.Fatalf("series channel after its files went: %v %+v", err, res)
	}
	if rm, _ := f.st.RemovedKeys(ctx); rm["series:"+itoa(s)] {
		t.Fatal("a dropped channel is remembered as deleted")
	}
	if got, _ := f.st.Get(ctx, c.ID); got.Name != "Смешное" {
		t.Fatalf("edited channel changed: %+v", got)
	}
}

func itoa(v int64) string {
	b := []byte{}
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func keys(m map[string]Channel) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Animated series get «Мультсериалы» and stay out of «Все сериалы».
func TestGeneratorToonsApartFromSeries(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		f.series("Toon", 1, 2, 20, itemOpt{year: 2000 + i, genres: []string{"Мультфильм", "комедия"}})
		f.series("Live", 1, 2, 20, itemOpt{year: 2000 + i, genres: []string{"драма"}})
	}
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}, MinRuntime: time.Nanosecond}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := f.st.All(ctx)
	by := map[string]Channel{}
	for _, c := range all {
		by[c.AutoKey] = c
	}
	toons, ok := by["toons"]
	if !ok || toons.Name != "Мультсериалы" {
		t.Fatalf("no toons channel: %v", keys(by))
	}
	series, ok := by["series"]
	if !ok || len(series.Rules.NotGenres) != 1 {
		t.Fatalf("series channel without the animation exclusion: %+v", series.Rules)
	}
	units, _ := g.Catalog.Units(ctx)
	for _, u := range Match(units, series.Rules) {
		for _, gk := range u.Genres {
			if gk == animationGenre {
				t.Fatalf("animated %q in «Все сериалы»", u.Title)
			}
		}
	}
	if n := len(Match(units, toons.Rules)); n != 6 {
		t.Fatalf("toons has %d titles, want 6", n)
	}
}

// A generated channel needs two days of programmes, not just six titles.
func TestGeneratorNeedsTwoDays(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		f.movie("Short", 90, itemOpt{year: 1990 + i, genres: []string{"комедия"}})
	}
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}}
	plan, err := g.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan {
		if p.AutoKey == "movies" || p.AutoKey == "genre:комедия" {
			t.Fatalf("9 hours of films made channel %s", p.AutoKey)
		}
	}
	for i := 0; i < 27; i++ { // 33 × 90 min = 49.5 h
		f.movie("More", 90, itemOpt{year: 1990 + i%10, genres: []string{"комедия"}})
	}
	plan, err = g.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, p := range plan {
		have[p.AutoKey] = true
	}
	if !have["movies"] || !have["genre:комедия"] || !have["decade:1990"] {
		t.Fatalf("49.5 hours of comedies of the 90s: %v", have)
	}
}

// A series channel generated before channels skipped openings gets the
// rule on the next sync, once; an admin who turned it off (an edit) keeps
// it off.
func TestGeneratorSyncGivesSkipSegmentsOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s, _ := f.series("Long", 1, 8, 20, itemOpt{})
	t2, _ := f.series("Other", 1, 8, 20, itemOpt{})
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}, MinRuntime: time.Nanosecond}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := f.st.All(ctx)
	by := map[string]Channel{}
	for _, c := range all {
		by[c.AutoKey] = c
	}
	for _, key := range []string{"series:" + itoa(s), "series:" + itoa(t2)} {
		if !by[key].Rules.SkipSegments {
			t.Fatalf("%s without skipSegments: %+v", key, by[key])
		}
	}
	// as an older build left it: no rule
	old := by["series:"+itoa(s)]
	f.exec(`UPDATE channels SET rules_json=json_remove(rules_json, '$.skipSegments') WHERE id=?`, old.ID)
	// the admin turned it off on the other one
	off := by["series:"+itoa(t2)]
	off.Rules.SkipSegments, off.Edited = false, true
	if _, err := f.st.Update(ctx, off); err != nil {
		t.Fatal(err)
	}
	res, err := g.Sync(ctx)
	if err != nil || res.Updated != 1 {
		t.Fatalf("sync %+v %v", res, err)
	}
	if got, _ := f.st.Get(ctx, old.ID); !got.Rules.SkipSegments {
		t.Fatalf("old channel %+v", got.Rules)
	}
	if got, _ := f.st.Get(ctx, off.ID); got.Rules.SkipSegments {
		t.Fatalf("the admin's off was undone: %+v", got.Rules)
	}
	if res, _ := g.Sync(ctx); res != (SyncResult{}) {
		t.Fatalf("third run: %+v", res)
	}
}

// Every kind needs the same two days: six films, a three-film franchise or a
// profile's six titles make no channel.
func TestGeneratorTwoDaysForEveryKind(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	ann := f.user("ann")
	for i := 0; i < 6; i++ {
		id := f.movie("C", 120, itemOpt{year: 1991 + i, genres: []string{"Комедия"}})
		f.exec(`INSERT INTO item_owners (item_id, user_id) VALUES (?,?)`, id, ann)
	}
	_, tm := starWars(f)
	(&Franchises{Store: f.st, TMDB: tm}).Pass(ctx, false)
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if all, _ := f.st.All(ctx); len(all) != 0 {
		var ks []string
		for _, c := range all {
			ks = append(ks, c.AutoKey)
		}
		t.Fatalf("channels under two days: %v", ks)
	}
}

// A series of a franchise plays on the franchise's channel, not on its own.
func TestGeneratorSeriesOfFranchiseHasNoOwnChannel(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	l, tm := starWars(f)
	(&Franchises{Store: f.st, TMDB: tm}).Pass(ctx, false)
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}, MinRuntime: time.Nanosecond}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := f.st.All(ctx)
	by := map[string]Channel{}
	for _, c := range all {
		by[c.AutoKey] = c
	}
	sw, ok := by["franchise:c10"]
	if !ok {
		t.Fatalf("no franchise channel: %v", keys(by))
	}
	if !contains(pickIDs(sw.Rules.Picks), l.clone) {
		t.Skipf("the fixture's franchise has no series: %+v", sw.Rules.Picks)
	}
	if _, ok := by["series:"+itoa(l.clone)]; ok {
		t.Fatal("a franchise's series has its own channel")
	}
}

func pickIDs(ps []Pick) []int64 {
	var out []int64
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

// Reset gives deleted generated channels back and keeps edited ones.
func TestGeneratorReset(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for i := 0; i < 6; i++ {
		f.movie("C", 90, itemOpt{year: 1991 + i, genres: []string{"Комедия"}})
	}
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}, MinRuntime: time.Nanosecond}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	by := map[string]Channel{}
	all, _ := f.st.All(ctx)
	for _, c := range all {
		by[c.AutoKey] = c
	}
	if err := f.st.Delete(ctx, by["movies"].ID); err != nil {
		t.Fatal(err)
	}
	c := by["genre:комедия"]
	c.Name, c.Edited = "Смешное", true
	if _, err := f.st.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(must(f.st.All(ctx))); n != len(all)-1 {
		t.Fatalf("a deleted channel came back on a plain sync: %d of %d", n, len(all))
	}
	res, err := g.Reset(ctx)
	if err != nil || res.Created != 1 {
		t.Fatalf("reset %+v %v", res, err)
	}
	after := map[string]Channel{}
	for _, c := range must(f.st.All(ctx)) {
		after[c.AutoKey] = c
	}
	if _, ok := after["movies"]; !ok {
		t.Fatal("reset did not give the deleted channel back")
	}
	if after["genre:комедия"].Name != "Смешное" {
		t.Fatalf("reset changed an edited channel: %+v", after["genre:комедия"])
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
