package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata/ratings"
)

type fakeRatings struct{ asked []string }

func (f *fakeRatings) Get(_ context.Context, id string) ratings.Ratings {
	f.asked = append(f.asked, id)
	if id == "tt0816692" {
		return ratings.Ratings{IMDb: &ratings.IMDbRating{Rating: 8.7, Votes: 2300000}, RottenTomatoes: &ratings.RTScores{Tomatometer: 73}}
	}
	return ratings.Ratings{}
}

func TestRatingsEndpoint(t *testing.T) {
	f := &fakeRatings{}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Ratings = f })
	id, _ := e.movie("Интерстеллар", "12+", "x")
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=157336, meta_json='{"imdb_id":"tt0816692"}' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if d := detailOf(t, e, id, "ru"); d.ImdbID != "tt0816692" {
		t.Fatalf("detail imdbId %q", d.ImdbID)
	}

	w := e.do("GET", "/api/ratings/tt0816692", "", e.admin)
	if w.Code != 200 || w.Body.String() != `{"imdb":{"rating":8.7,"votes":2300000},"rottenTomatoes":{"tomatometer":73}}`+"\n" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/ratings/tt1234567", "", e.admin); w.Code != 200 || w.Body.String() != "{}\n" {
		t.Fatalf("unknown: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/ratings/nm0000001", "", e.admin); w.Code != 400 {
		t.Fatalf("bad id: %d", w.Code)
	}
	if len(f.asked) != 2 {
		t.Fatalf("asked %v", f.asked)
	}
	if w := e.do("GET", "/api/ratings/tt0816692", "", nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
}

func TestRatingsWithoutSourcesAndBadMetaID(t *testing.T) {
	e := newLibEnvWith(t, func(*Deps, *sql.DB) {})
	id, _ := e.movie("Фильм", "", "x")
	if _, err := e.db.Exec(`UPDATE items SET meta_json='{"imdb_id":"not-an-id"}' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if d := detailOf(t, e, id, "ru"); d.ImdbID != "" {
		t.Fatalf("bad meta id shown: %q", d.ImdbID)
	}
	w := e.do("GET", "/api/ratings/tt0816692", "", e.admin)
	var r map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &r) != nil || len(r) != 0 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// A kid's profile gets ratings only of the library's titles it may open:
// it cannot make the server look up arbitrary ids outside.
func TestRatingsKidOnlyOwnTitles(t *testing.T) {
	f := &fakeRatings{}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Ratings = f })
	ok, _ := e.movie("Интерстеллар", "12+", "x")
	adult, _ := e.movie("Джокер", "18+", "y")
	for id, imdb := range map[int64]string{ok: "tt0816692", adult: "tt7286456"} {
		if _, err := e.db.Exec(`UPDATE items SET meta_json=? WHERE id=?`, `{"imdb_id":"`+imdb+`"}`, id); err != nil {
			t.Fatal(err)
		}
	}
	kid := e.kid("12+")
	if w := e.do("GET", "/api/ratings/tt0816692", "", kid); w.Code != 200 || len(f.asked) != 1 {
		t.Fatalf("own title: %d %s %v", w.Code, w.Body, f.asked)
	}
	for _, id := range []string{"tt7286456", "tt1234567"} {
		if w := e.do("GET", "/api/ratings/"+id, "", kid); w.Code != 200 || w.Body.String() != "{}\n" {
			t.Fatalf("%s: %d %s", id, w.Code, w.Body)
		}
	}
	if len(f.asked) != 1 {
		t.Fatalf("asked %v", f.asked)
	}
}
