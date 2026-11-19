package channels

import (
	"context"
	"testing"
)

// a channel judges a title of a folder «Только для взрослых» as 18+ (a
// channel limited below that never plays it), and its rules (genres,
// years…) never pick a title of a folder «Скрыть из общих подборок»; a
// channel of picked titles still may
func TestChannelsFolderMarks(t *testing.T) {
	f := newFixture(t)
	adult := f.movie("Взрослый", 90, itemOpt{rating: "0+", genres: []string{"Комедия"}})
	hidden := f.movie("Скрытый", 90, itemOpt{rating: "0+", genres: []string{"Комедия"}})
	plain := f.movie("Обычный", 90, itemOpt{rating: "0+", genres: []string{"Комедия"}})
	_, eps := f.series("Сериал", 1, 2, 20, itemOpt{rating: "0+"})
	f.exec(`UPDATE items SET adult=1 WHERE id=?`, adult)
	f.exec(`UPDATE items SET unlisted=1 WHERE id=?`, hidden)
	f.exec(`UPDATE items SET adult=1 WHERE id=(SELECT parent_id FROM items WHERE id=(SELECT parent_id FROM items WHERE id=?))`, eps[0])
	cat := Catalog{DB: f.db}
	units, err := cat.Units(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := func(us []Unit) map[int64]bool {
		out := map[int64]bool{}
		for _, u := range us {
			out[u.ID] = true
		}
		return out
	}
	kids := ids(Match(units, Rules{MaxRating: "12+"}))
	if kids[adult] || kids[hidden] || !kids[plain] || len(kids) != 1 {
		t.Fatalf("a 12+ channel: %v", kids)
	}
	all := ids(Match(units, Rules{Genres: []string{"Комедия"}}))
	if !all[adult] || all[hidden] || !all[plain] {
		t.Fatalf("an unlimited channel by genre: %v", all)
	}
	picked := ids(Match(units, Rules{Titles: []int64{hidden, adult}, MaxRating: "16+"}))
	if !picked[hidden] || picked[adult] {
		t.Fatalf("picked titles: %v", picked)
	}
	// the episodes of an adults-only series, picked one by one, judge by it
	fs, err := cat.videoFiles(context.Background(), `i.id=?`, eps[0])
	if err != nil || len(fs) != 1 || allowed("12+", fs[0]) {
		t.Fatalf("%+v %v", fs, err)
	}
}
