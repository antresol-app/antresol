package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// fakeRelated: TMDB's titles around The Matrix (603) and its collection
// (2344, four parts; 999 not in any library); tags records the languages
// asked for.
type fakeRelated struct{ tags []string }

func (f *fakeRelated) Titles(_ context.Context, kind string, id int, tag string) (tmdb.RelatedTitles, bool) {
	f.tags = append(f.tags, tag)
	if kind != "movie" || id != 603 {
		return tmdb.RelatedTitles{}, true
	}
	return tmdb.RelatedTitles{
		Similar: []tmdb.TitleRef{
			{ID: 680, Kind: "movie", Title: "Pulp Fiction", Year: 1994, Poster: "/p.jpg"},
			{ID: 604, Kind: "movie", Title: "The Matrix Reloaded", Year: 2003},
			{ID: 11, Kind: "movie", Title: "Star Wars", Year: 1977, Poster: "/sw.jpg"},
			{ID: 12, Kind: "movie", Title: "Finding Nemo", Year: 2003},
		},
		Collection: &tmdb.CollectionRef{ID: 2344, Name: "The Matrix Collection"},
	}, true
}

func (f *fakeRelated) Collection(_ context.Context, id int, tag string) (tmdb.Collection, bool) {
	if id != 2344 {
		return tmdb.Collection{}, false
	}
	return tmdb.Collection{ID: 2344, Name: "Матрица (Коллекция)", Overview: "Нео", Backdrop: "/cb.jpg", Parts: []tmdb.TitleRef{
		{ID: 603, Kind: "movie", Title: "The Matrix", Year: 1999},
		{ID: 604, Kind: "movie", Title: "The Matrix Reloaded", Year: 2003},
		{ID: 605, Kind: "movie", Title: "The Matrix Revolutions", Year: 2003, Poster: "/rev.jpg"},
		{ID: 624860, Kind: "movie", Title: "The Matrix Resurrections", Year: 2021},
	}}, true
}

func (f *fakeRelated) ImageURL(size, p string) string {
	if p == "" {
		return ""
	}
	return "https://image.test/" + size + p
}

// relatedLibrary: The Matrix (16+), Reloaded (16+), Resurrections (18+),
// Pulp Fiction (18+) and Finding Nemo (0+) in the library.
func relatedLibrary(t *testing.T, e *libEnv) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	for _, m := range []struct {
		name, rating string
		tmdb         int
	}{{"Матрица", "16+", 603}, {"Матрица: Перезагрузка", "16+", 604}, {"Матрица: Воскрешение", "18+", 624860},
		{"Криминальное чтиво", "18+", 680}, {"В поисках Немо", "0+", 12}} {
		id, _ := e.movie(m.name, m.rating, m.name)
		if _, err := e.db.Exec(`UPDATE items SET tmdb_id=? WHERE id=?`, m.tmdb, id); err != nil {
			t.Fatal(err)
		}
		ids[m.name] = id
	}
	return ids
}

func relatedOf(t *testing.T, e *libEnv, id int64, who string, cookie *http.Cookie) RelatedRows {
	t.Helper()
	w := e.do("GET", "/api/items/"+itoa(id)+"/related", "", cookie, "Accept-Language", "en")
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", who, w.Code, w.Body)
	}
	var out RelatedRows
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func cardTitles(cs []Card) string {
	var out []string
	for _, c := range cs {
		s := c.Title
		if c.ID == 0 {
			s += "?"
		}
		if c.Watched {
			s += "✓"
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

// The library's titles TMDB names, in its order; the collection's parts
// the library has, in release order, the watched one marked. No card of
// a title outside the library in this setup (the core build never
// has them; the sources build only with «Найти и добавить»).
func TestRelatedLibraryTitles(t *testing.T) {
	f := &fakeRelated{}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Related = f })
	ids := relatedLibrary(t, e)
	if err := e.prog.MarkWatched(context.Background(), 1, ids["Матрица: Перезагрузка"], true); err != nil {
		t.Fatal(err)
	}
	got := relatedOf(t, e, ids["Матрица"], "admin", e.admin)
	if s := cardTitles(got.Similar); s != "Криминальное чтиво, Матрица: Перезагрузка✓, В поисках Немо" {
		t.Fatalf("similar: %s", s)
	}
	if got.Similar[0].ID != ids["Криминальное чтиво"] || !got.Similar[0].OnServer || got.Similar[0].Poster == "" {
		t.Fatalf("library card %+v", got.Similar[0])
	}
	c := got.Collection
	if c == nil || c.ID != 2344 || c.Name != "Матрица" || c.Backdrop != "https://image.test/w1280/cb.jpg" {
		t.Fatalf("collection %+v", c)
	}
	if s := cardTitles(c.Parts); s != "Матрица, Матрица: Перезагрузка✓, Матрица: Воскрешение" {
		t.Fatalf("parts: %s", s)
	}
	// the year, not the genres, under a part
	if c.Parts[0].Subtitle != "" || c.Parts[0].Year != 2000 {
		t.Fatalf("part %+v", c.Parts[0])
	}
	if strings.Join(f.tags, ",") != "en-US" {
		t.Fatalf("languages %v", f.tags)
	}
	// the collection page
	w := e.do("GET", "/api/items/"+itoa(ids["Матрица"])+"/collection", "", e.admin)
	var page CollectionRow
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Parts) != 3 || page.Overview != "Нео" {
		t.Fatalf("collection page: %d %s", w.Code, w.Body)
	}
}

// A kid sees nothing above its rating, and no title outside the library;
// a collection with nothing but the title itself is no row.
func TestRelatedKid(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Related = &fakeRelated{} })
	ids := relatedLibrary(t, e)
	if _, err := e.db.Exec(`UPDATE items SET rating='6+' WHERE id=?`, ids["Матрица"]); err != nil {
		t.Fatal(err)
	}
	kid := e.kid("12+")
	got := relatedOf(t, e, ids["Матрица"], "kid", kid)
	if s := cardTitles(got.Similar); s != "В поисках Немо" {
		t.Fatalf("kid similar: %s", s)
	}
	if got.Collection != nil {
		t.Fatalf("kid collection: %+v", got.Collection)
	}
	if w := e.do("GET", "/api/items/"+itoa(ids["Матрица"])+"/collection", "", kid); w.Code != 404 {
		t.Fatalf("kid collection page: %d", w.Code)
	}
	// a title above the limit: 404, as its page
	if w := e.do("GET", "/api/items/"+itoa(ids["Криминальное чтиво"])+"/related", "", kid); w.Code != 404 {
		t.Fatalf("hidden title: %d", w.Code)
	}
}

// A film in two editions is one card (the first), watched when the
// profile finished either.
func TestRelatedWatchedAnyEdition(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Related = &fakeRelated{} })
	ids := relatedLibrary(t, e)
	second, _ := e.movie("Матрица: Перезагрузка 4K", "16+", "4k")
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=604 WHERE id=?`, second); err != nil {
		t.Fatal(err)
	}
	if err := e.prog.MarkWatched(context.Background(), 1, second, true); err != nil {
		t.Fatal(err)
	}
	got := relatedOf(t, e, ids["Матрица"], "admin", e.admin)
	if s := cardTitles(got.Similar); s != "Криминальное чтиво, Матрица: Перезагрузка✓, В поисках Немо" {
		t.Fatalf("similar: %s", s)
	}
	if got.Similar[1].ID != ids["Матрица: Перезагрузка"] {
		t.Fatalf("card of edition %d, want %d", got.Similar[1].ID, ids["Матрица: Перезагрузка"])
	}
	if s := cardTitles(got.Collection.Parts); s != "Матрица, Матрица: Перезагрузка✓, Матрица: Воскрешение" {
		t.Fatalf("parts: %s", s)
	}
}

func TestShortCollectionName(t *testing.T) {
	for in, want := range map[string]string{
		"Матрица (Коллекция)": "Матрица", "The Matrix Collection": "The Matrix", "Коллекция «Шрек»": "Шрек",
		"Звёздные войны": "Звёздные войны", "Collection": "Collection",
	} {
		if got := shortCollectionName(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// Without TMDB, or for a title TMDB does not know: empty rows.
func TestRelatedEmpty(t *testing.T) {
	e := newLibEnv(t)
	id, _ := e.movie("Брат", "16+", "x")
	got := relatedOf(t, e, id, "no tmdb", e.admin)
	if got.Similar == nil || len(got.Similar) != 0 || got.Collection != nil {
		t.Fatalf("%+v", got)
	}
	w := e.do("GET", "/api/items/"+itoa(id)+"/related", "", e.admin)
	if !strings.Contains(w.Body.String(), `"similar":[]`) || !strings.Contains(w.Body.String(), `"collection":null`) {
		t.Fatalf("shape %s", w.Body)
	}
	e2 := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Related = &fakeRelated{} })
	id2, _ := e2.movie("Брат", "16+", "x")
	if got := relatedOf(t, e2, id2, "no tmdb id", e2.admin); len(got.Similar) != 0 || got.Collection != nil {
		t.Fatalf("%+v", got)
	}
}
