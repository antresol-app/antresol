package jellyfin

import (
	"context"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// fakeSimilar: TMDB's titles around movie 157336 (Interstellar) and 10
// (the cartoon): Deadpool, the cartoon and one the library lacks.
type fakeSimilar struct{ asked []int }

func (f *fakeSimilar) Language() string { return "ru-RU" }

func (f *fakeSimilar) Titles(_ context.Context, kind string, id int, _ string) (tmdb.RelatedTitles, bool) {
	f.asked = append(f.asked, id)
	if kind != "movie" {
		return tmdb.RelatedTitles{}, false
	}
	return tmdb.RelatedTitles{Similar: []tmdb.TitleRef{
		{ID: 293660, Kind: "movie", Title: "Deadpool"},
		{ID: 4242, Kind: "movie", Title: "Not here"},
		{ID: 10, Kind: "movie", Title: "Cartoon"},
		{ID: 157336, Kind: "movie", Title: "Interstellar"},
	}}, true
}

// /Items/{id}/Similar: the library's titles TMDB names, in its order,
// within the profile's rating; the title itself and titles outside the
// library are left out.
func TestSimilarFromTMDB(t *testing.T) {
	e := newLibEnv(t)
	f := &fakeSimilar{}
	e.s.Similar = f
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=10 WHERE id=?`, e.cartoon); err != nil {
		t.Fatal(err)
	}
	var p QueryResult[BaseItemDto]
	decode(t, e.get(e.anyaTok, "/Items/"+ItemID(e.interstellar)+"/Similar?limit=12"), &p)
	if got := names(p.Items); len(got) != 2 || got[0] != "Дэдпул" || got[1] != "Мультик" || p.TotalRecordCount != 2 {
		t.Fatalf("similar %v (%d)", got, p.TotalRecordCount)
	}
	decode(t, e.get(e.anyaTok, "/Items/"+ItemID(e.interstellar)+"/Similar?limit=1"), &p)
	if got := names(p.Items); len(got) != 1 || got[0] != "Дэдпул" {
		t.Fatalf("limit 1: %v", got)
	}
	// a kid (6+) sees the cartoon only: Deadpool (18+) is left out, and
	// Interstellar (12+) is not its page to open
	decode(t, e.get(e.mishaTok, "/Items/"+ItemID(e.cartoon)+"/Similar"), &p)
	if got := names(p.Items); len(got) != 0 {
		t.Fatalf("kid: %v", got)
	}
	if w := e.get(e.mishaTok, "/Items/"+ItemID(e.interstellar)+"/Similar"); w.Code != 404 {
		t.Fatalf("kid on a hidden title: %d", w.Code)
	}
	// a title without a TMDB id: empty, TMDB not asked
	n := len(f.asked)
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=NULL WHERE id=?`, e.cartoon); err != nil {
		t.Fatal(err)
	}
	decode(t, e.get(e.anyaTok, "/Items/"+ItemID(e.cartoon)+"/Similar"), &p)
	if len(p.Items) != 0 || len(f.asked) != n {
		t.Fatalf("no tmdb id: %v, asked %v", names(p.Items), f.asked)
	}
}
