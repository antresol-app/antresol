package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// fakeTranslator has English text of movie 157336 and series 1; down:
// TMDB fails (Get answers nothing). Peek answers what Get handed out.
type fakeTranslator struct {
	gets, peeks []string
	down        bool
	got         map[string]bool
}

func (f *fakeTranslator) Language() string { return "ru-RU" }

func (f *fakeTranslator) text(kind string, id int) tmdb.Translation {
	if kind == "movie" && id == 157336 {
		return tmdb.Translation{Title: "Interstellar", Overview: "Space",
			Cast: []tmdb.Person{{Name: "Matthew McConaughey", Character: "Cooper", Profile: "/m.jpg"}}}
	}
	return tmdb.Translation{Title: "Shogun", Overview: "Japan",
		Seasons: map[int][]tmdb.EpisodeText{1: {{Number: 1, Name: "Anjin", Overview: "Ship"}}}}
}

func (f *fakeTranslator) Peek(_ context.Context, kind string, id int, tag string) (tmdb.Translation, bool) {
	f.peeks = append(f.peeks, kind+"/"+tag)
	if !f.got[kind] {
		return tmdb.Translation{}, false
	}
	return f.text(kind, id), true
}

func (f *fakeTranslator) Get(_ context.Context, kind string, id int, tag string, seasons []int) (tmdb.Translation, bool) {
	f.gets = append(f.gets, kind+"/"+tag)
	if f.down {
		return tmdb.Translation{}, false
	}
	if f.got == nil {
		f.got = map[string]bool{}
	}
	f.got[kind] = true
	return f.text(kind, id), true
}

func (f *fakeTranslator) Has(context.Context, string, int, string, []int) bool { return false }

func translatedMovie(t *testing.T) (*libEnv, *fakeTranslator, int64) {
	f := &fakeTranslator{}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Translations = f })
	id, _ := e.movie("Интерстеллар", "12+", "x")
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=157336, overview='Космос',
		meta_json='{"cast":[{"name":"Мэттью Макконахи","character":"Купер","profile":"/m.jpg"}]}' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	return e, f, id
}

func detailOf(t *testing.T, e *libEnv, id int64, lang string) ItemDetail {
	t.Helper()
	w := e.do("GET", "/api/items/"+itoa(id), "", e.admin, "Accept-Language", lang)
	var d ItemDetail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return d
}

// In another language than the stored one, the page shows TMDB's text in
// it; in the stored language TMDB is not asked.
func TestItemDetailInTheViewersLanguage(t *testing.T) {
	e, f, id := translatedMovie(t)
	d := detailOf(t, e, id, "ru")
	if d.Title != "Интерстеллар" || d.Overview != "Космос" || d.Cast[0].Name != "Мэттью Макконахи" || len(f.gets)+len(f.peeks) != 0 {
		t.Fatalf("ru: %+v, asked %v %v", d, f.gets, f.peeks)
	}
	d = detailOf(t, e, id, "en")
	if d.Title != "Interstellar" || d.Overview != "Space" || len(d.Cast) != 1 || d.Cast[0].Name != "Matthew McConaughey" ||
		d.Cast[0].Character != "Cooper" || d.Cast[0].Photo != "/api/images/cast/m.jpg" {
		t.Fatalf("en: %+v", d)
	}
	if strings.Join(f.gets, ",") != "movie/en-US" {
		t.Fatalf("gets %v", f.gets)
	}
	// cards use the cached text only
	w := e.do("GET", "/api/items?kind=movie", "", e.admin, "Accept-Language", "en")
	if !strings.Contains(w.Body.String(), `"title":"Interstellar"`) || len(f.gets) != 1 {
		t.Fatalf("card: %s, gets %v", w.Body, f.gets)
	}
}

// TMDB down: the stored text.
func TestItemDetailTranslationFailureKeepsStored(t *testing.T) {
	e, f, id := translatedMovie(t)
	f.down = true
	d := detailOf(t, e, id, "en")
	if d.Title != "Интерстеллар" || d.Overview != "Космос" || d.Cast[0].Name != "Мэттью Макконахи" || len(f.gets) != 1 {
		t.Fatalf("%+v, gets %v", d, f.gets)
	}
}

// Text the enricher marked as English is not translated for English.
func TestItemDetailStoredLanguageFromMeta(t *testing.T) {
	e, f, id := translatedMovie(t)
	if _, err := e.db.Exec(`UPDATE items SET meta_json='{"tmdb_lang":"en-US"}' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if d := detailOf(t, e, id, "en"); d.Overview != "Космос" || len(f.gets) != 0 {
		t.Fatalf("%+v, gets %v", d, f.gets)
	}
	if d := detailOf(t, e, id, "ru"); d.Title != "Interstellar" || len(f.gets) != 1 || f.gets[0] != "movie/ru-RU" {
		t.Fatalf("%+v, gets %v", d, f.gets)
	}
}

// A series page shows its episodes' names and overviews in the language.
func TestSeriesDetailEpisodesTranslated(t *testing.T) {
	f := &fakeTranslator{}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Translations = f })
	ctx := context.Background()
	epID, err := e.repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Сёгун", SeriesTmdbID: 1, Season: 1, Episode: 1,
		Title: "Анджин", Overview: "Корабль", File: library.FileInput{Path: e.media + "/s.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	ep, _ := e.repo.Item(ctx, epID)
	season, _ := e.repo.Item(ctx, ep.ParentID)
	d := detailOf(t, e, season.ParentID, "en")
	if d.Title != "Shogun" || d.Overview != "Japan" || len(d.Seasons) != 1 || len(d.Seasons[0].Episodes) != 1 {
		t.Fatalf("%+v", d)
	}
	got := d.Seasons[0].Episodes[0]
	if got.Title != "Anjin" || got.Overview != "Ship" || !strings.HasPrefix(got.Subtitle, "Shogun · ") {
		t.Fatalf("%+v", got)
	}
	// the episode's own page
	d = detailOf(t, e, epID, "en")
	if d.Title != "Anjin" || d.Overview != "Ship" {
		t.Fatalf("%+v", d)
	}
}

// artTranslator is fakeTranslator with TMDB's English poster and logo.
type artTranslator struct{ fakeTranslator }

func (f *artTranslator) Get(ctx context.Context, kind string, id int, tag string, seasons []int) (tmdb.Translation, bool) {
	t, ok := f.fakeTranslator.Get(ctx, kind, id, tag, seasons)
	t.Poster, t.Logo, t.Art = "/en.jpg", "/en.svg", true
	return t, ok
}

func (f *artTranslator) Peek(ctx context.Context, kind string, id int, tag string) (tmdb.Translation, bool) {
	t, ok := f.fakeTranslator.Peek(ctx, kind, id, tag)
	if ok {
		t.Poster, t.Logo, t.Art = "/en.jpg", "/en.svg", true
	}
	return t, ok
}

const (
	ruPoster = "1111111111111111111111111111111111111111.jpg"
	ruLogo   = "2222222222222222222222222222222222222222.png"
)

// The poster and logo follow the viewer's language as the title does
// (the stored ones are the metadata language's): TMDB's art in it, served
// by /api/images/art once downloaded; the stored art in the stored
// language, with no text cached yet, and with the art not on the disk
// yet. Episodes borrow the series' art in the language.
func TestCardArtInTheViewersLanguage(t *testing.T) {
	f := &artTranslator{}
	var art *tmdb.Photos
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.Translations = f
		art = &tmdb.Photos{Client: &tmdb.Client{}, Images: &metadata.Images{Dir: filepath.Join(d.DataDir, "images")}, Size: tmdb.ArtSize}
		d.Artwork = art
	})
	ctx := context.Background()
	epID, err := e.repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Миллиарды", SeriesTmdbID: 1, Season: 1, Episode: 1,
		Title: "Пилот", File: library.FileInput{Path: e.media + "/b.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	ep, _ := e.repo.Item(ctx, epID)
	season, _ := e.repo.Item(ctx, ep.ParentID)
	seriesID := season.ParentID
	if _, err := e.db.Exec(`UPDATE items SET poster=?, meta_json=? WHERE id=?`, ruPoster, `{"logo":"`+ruLogo+`"}`, seriesID); err != nil {
		t.Fatal(err)
	}
	if d := detailOf(t, e, seriesID, "ru"); d.Logo != "/api/images/"+ruLogo || d.Poster != "/api/images/"+ruPoster {
		t.Fatalf("ru: %+v", d.Card)
	}
	// nothing cached: a card never waits, the stored art
	w := e.do("GET", "/api/items?kind=series", "", e.admin, "Accept-Language", "en")
	if !strings.Contains(w.Body.String(), `"logo":"/api/images/`+ruLogo) {
		t.Fatalf("uncached: %s", w.Body)
	}
	// the text is cached, the art not downloaded yet (the image host may
	// be down): the stored art, no picture that may not load
	if d := detailOf(t, e, seriesID, "en"); d.Title != "Shogun" || d.Logo != "/api/images/"+ruLogo || d.Poster != "/api/images/"+ruPoster {
		t.Fatalf("en, art not cached: %+v", d.Card)
	}
	// as Translations.Art does in the background
	for _, p := range []string{"/en.svg", "/en.jpg"} {
		name, _ := art.Cached(p)
		os.MkdirAll(art.Images.Dir, 0o755)
		if err := os.WriteFile(filepath.Join(art.Images.Dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if d := detailOf(t, e, seriesID, "en"); d.Logo != "/api/images/art/en.svg" || d.Poster != "/api/images/art/en.jpg" {
		t.Fatalf("en: %+v", d.Card)
	}
	w = e.do("GET", "/api/items?kind=series", "", e.admin, "Accept-Language", "en")
	if !strings.Contains(w.Body.String(), `"logo":"/api/images/art/en.svg"`) {
		t.Fatalf("card: %s", w.Body)
	}
	if d := detailOf(t, e, epID, "en"); d.Logo != "/api/images/art/en.svg" {
		t.Fatalf("episode: %+v", d.Card)
	}

	// local artwork stays
	if _, err := e.db.Exec(`UPDATE items SET poster='file:/m/poster.jpg', meta_json='{"logo":"file:/m/logo.png"}' WHERE id=?`, seriesID); err != nil {
		t.Fatal(err)
	}
	d := detailOf(t, e, seriesID, "en")
	if want := "/api/images/local/" + itoa(seriesID); d.Logo != want+"/logo" || d.Poster != want+"/poster" {
		t.Fatalf("local: %+v", d.Card)
	}
}

// /api/images/art serves TMDB posters and logos by file name only:
// fetched once, then from the cache; never a path or another name.
func TestArtImageServedFromCache(t *testing.T) {
	var hits atomic.Int32
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/t/p/w500/logo.svg" {
			w.Write([]byte("<svg/>"))
			return
		}
		http.NotFound(w, r)
	}))
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.Artwork = &tmdb.Photos{Client: &tmdb.Client{ImageBase: img.URL + "/t/p"},
			Images: &metadata.Images{Dir: filepath.Join(d.DataDir, "images")}, Size: tmdb.ArtSize}
	})
	w := e.do("GET", "/api/images/art/logo.svg", "", e.admin)
	if w.Code != 200 || w.Body.String() != "<svg/>" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("%d %s %v", w.Code, w.Body, w.Header())
	}
	img.Close()
	if w := e.do("GET", "/api/images/art/logo.svg", "", e.admin); w.Code != 200 || hits.Load() != 1 {
		t.Fatalf("offline: %d, %d hits", w.Code, hits.Load())
	}
	n := hits.Load()
	for _, name := range []string{"..%2Fv.db", "x.exe", "a.b.png", ".png"} {
		if w := e.do("GET", "/api/images/art/"+name, "", e.admin); w.Code != 404 {
			t.Fatalf("%s: %d", name, w.Code)
		}
	}
	if hits.Load() != n {
		t.Fatal("a bad name reached TMDB")
	}
	if w := e.do("GET", "/api/images/art/logo.svg", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", w.Code)
	}
}
