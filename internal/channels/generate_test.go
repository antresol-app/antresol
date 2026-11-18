package channels

import (
	"context"
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
	// an edited channel is left alone; one that lost its content is disabled
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
	got, _ := f.st.Get(ctx, by["series:"+itoa(s)].ID)
	if got.Enabled || res.Disabled == 0 {
		t.Fatalf("series channel after its files went: %+v %+v", got, res)
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
