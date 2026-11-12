package channels

import (
	"context"
	"testing"
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
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}}
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
