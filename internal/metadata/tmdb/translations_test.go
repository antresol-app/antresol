package tmdb

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/metadata"
)

// fakeTranslations answers /movie/{id} and /tv/{id} in English, with the
// seasons appended; status, when set, fails every request.
type fakeTranslations struct {
	mu     sync.Mutex
	hits   []string
	status int
	srv    *httptest.Server
}

func newFakeTranslations(t *testing.T) *fakeTranslations {
	f := &fakeTranslations{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits = append(f.hits, r.URL.Path+"?"+r.URL.Query().Get("append_to_response"))
		status := f.status
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if r.URL.Query().Get("language") != "en-US" {
			t.Errorf("language %q", r.URL.Query().Get("language"))
		}
		switch r.URL.Path {
		case "/3/movie/157336":
			w.Write([]byte(`{"id":157336,"title":"Interstellar","overview":"Space","credits":{"cast":[{"name":"Matthew McConaughey","character":"Cooper","profile_path":"/m.jpg"}]}}`))
		case "/3/tv/1":
			parts := []string{`"id":1,"name":"Shogun","overview":"Japan"`}
			for _, p := range strings.Split(r.URL.Query().Get("append_to_response"), ",") {
				if n, ok := strings.CutPrefix(p, "season/"); ok {
					parts = append(parts, fmt.Sprintf(`"season/%s":{"episodes":[{"episode_number":1,"name":"Anjin %s","overview":"Ship"}]}`, n, n))
				}
			}
			w.Write([]byte("{" + strings.Join(parts, ",") + "}"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTranslations) fail(status int) {
	f.mu.Lock()
	f.status = status
	f.mu.Unlock()
}

func (f *fakeTranslations) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hits...)
}

func (f *fakeTranslations) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hits)
}

func translationsDB(t *testing.T) *sql.DB {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// newTranslations is a cache whose background fetches stop before the
// test's database closes (cleanups run last-registered first).
func newTranslations(t *testing.T, c *Client, d *sql.DB) *Translations {
	s := &Translations{Client: c, DB: d}
	t.Cleanup(s.Close)
	return s
}

func TestTranslationFetchedOnceThenCached(t *testing.T) {
	f := newFakeTranslations(t)
	d := translationsDB(t)
	c := &Client{Key: "k", BaseURL: f.srv.URL + "/3"}
	s := newTranslations(t, c, d)
	ctx := context.Background()
	if _, ok := s.Peek(ctx, "movie", 157336, "en-US"); ok {
		t.Fatal("nothing cached yet")
	}
	tr, ok := s.Get(ctx, "movie", 157336, "en-US", nil)
	if !ok || tr.Title != "Interstellar" || tr.Overview != "Space" || len(tr.Cast) != 1 || tr.Cast[0].Profile != "/m.jpg" {
		t.Fatalf("%v %+v", ok, tr)
	}
	n := f.count()
	if _, ok := s.Get(ctx, "movie", 157336, "en-US", nil); !ok {
		t.Fatal("cached")
	}
	if tr, ok := s.Peek(ctx, "movie", 157336, "en-US"); !ok || tr.Title != "Interstellar" {
		t.Fatal("peek", tr)
	}
	// a new cache (a restart) reads the table
	s2 := newTranslations(t, c, d)
	if tr, ok := s2.Get(ctx, "movie", 157336, "en-US", nil); !ok || tr.Overview != "Space" {
		t.Fatal("from the table", tr)
	}
	if got := f.count(); got != n {
		t.Fatalf("cached reads asked TMDB: %d → %d hits", n, got)
	}
}

// A series' seasons are appended to its details; a season not fetched
// yet makes Get fetch again, 18 seasons to the first request (with credits
// and images).
func TestTranslationSeasons(t *testing.T) {
	f := newFakeTranslations(t)
	s := newTranslations(t, &Client{Key: "k", BaseURL: f.srv.URL + "/3"}, translationsDB(t))
	ctx := context.Background()
	tr, ok := s.Get(ctx, "tv", 1, "en-US", []int{1})
	if e, _ := tr.Episode(1, 1); !ok || tr.Title != "Shogun" || e.Name != "Anjin 1" || e.Overview != "Ship" {
		t.Fatalf("%+v", tr)
	}
	if f.requests()[0] != "/3/tv/1?credits,images,season/1" {
		t.Fatalf("hits %v", f.requests())
	}
	if _, ok := s.Get(ctx, "tv", 1, "en-US", []int{1}); !ok || f.count() != 1 {
		t.Fatalf("hits %v", f.requests())
	}
	var many []int
	for i := 0; i <= 20; i++ {
		many = append(many, i)
	}
	tr, _ = s.Get(ctx, "tv", 1, "en-US", many)
	if e, _ := tr.Episode(20, 1); e.Name != "Anjin 20" || f.count() != 3 {
		t.Fatalf("hits %v, %+v", f.requests(), tr)
	}
	if !strings.HasSuffix(f.requests()[1], ",season/17") || f.requests()[2] != "/3/tv/1?season/18,season/19,season/20" {
		t.Fatalf("hits %v", f.requests())
	}
}

// Seasons fetched one page at a time add up: S1, S2, then S1 again asks
// TMDB twice, and the cached entry (and the table) keeps both.
func TestTranslationSeasonsMerge(t *testing.T) {
	f := newFakeTranslations(t)
	d := translationsDB(t)
	c := &Client{Key: "k", BaseURL: f.srv.URL + "/3"}
	s := newTranslations(t, c, d)
	ctx := context.Background()
	s.Get(ctx, "tv", 1, "en-US", []int{1})
	s.Get(ctx, "tv", 1, "en-US", []int{2})
	tr, ok := s.Get(ctx, "tv", 1, "en-US", []int{1})
	if e, _ := tr.Episode(1, 1); !ok || e.Name != "Anjin 1" || f.count() != 2 {
		t.Fatalf("hits %v, %+v", f.requests(), tr)
	}
	if e, _ := tr.Episode(2, 1); e.Name != "Anjin 2" {
		t.Fatalf("%+v", tr)
	}
	// a refresh without seasons (the prefetcher's when nothing is cached)
	// keeps them too
	if _, err := s.fetch(ctx, "tv", 1, "en-US", nil); err != nil {
		t.Fatal(err)
	}
	tr, _ = newTranslations(t, c, d).Get(ctx, "tv", 1, "en-US", []int{1, 2})
	if e, _ := tr.Episode(2, 1); e.Name != "Anjin 2" || f.count() != 3 {
		t.Fatalf("hits %v, %+v", f.requests(), tr)
	}
}

// TMDB failing: no text (the stored one stays), and no new request for a
// while; a stale text is still served.
func TestTranslationFailureFallsBack(t *testing.T) {
	f := newFakeTranslations(t)
	d := translationsDB(t)
	c := &Client{Key: "k", BaseURL: f.srv.URL + "/3", Backoff: time.Millisecond}
	s := newTranslations(t, c, d)
	ctx := context.Background()
	f.fail(http.StatusInternalServerError)
	if _, ok := s.Get(ctx, "movie", 157336, "en-US", nil); ok {
		t.Fatal("no text when TMDB fails")
	}
	n := f.count()
	if _, ok := s.Get(ctx, "movie", 157336, "en-US", nil); ok || f.count() != n {
		t.Fatalf("retried at once: %v", f.requests())
	}

	// stale, TMDB down: the old text
	if _, err := d.Exec(`INSERT INTO tmdb_translations VALUES ('movie', 7, 'en-US', '{"title":"Old"}', ?)`,
		time.Now().Add(-60*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	s.Wait = 50 * time.Millisecond
	if tr, ok := s.Get(ctx, "movie", 7, "en-US", nil); !ok || tr.Title != "Old" {
		t.Fatal("stale", tr, ok)
	}

	// unknown to TMDB: kept as an empty text, not asked again
	f.fail(0)
	s3 := newTranslations(t, c, d)
	if tr, ok := s3.Get(ctx, "movie", 404, "en-US", nil); !ok || tr.Title != "" {
		t.Fatal(tr, ok)
	}
	n = f.count()
	s3.Get(ctx, "movie", 404, "en-US", nil)
	if f.count() != n {
		t.Fatal("asked again")
	}
}

// Peek never waits for TMDB: a missing text is fetched in the background.
func TestTranslationPeekPrefetches(t *testing.T) {
	f := newFakeTranslations(t)
	s := newTranslations(t, &Client{Key: "k", BaseURL: f.srv.URL + "/3"}, translationsDB(t))
	ctx := context.Background()
	if _, ok := s.Peek(ctx, "movie", 157336, "en-US"); ok {
		t.Fatal("not cached yet")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if tr, ok := s.Peek(ctx, "movie", 157336, "en-US"); ok {
			if tr.Title != "Interstellar" {
				t.Fatal(tr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not prefetched")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if f.count() != 1 {
		t.Fatalf("hits %v", f.requests())
	}
}

// A title's poster and logo in the language: one with its text first,
// else a textless one, never one in the stored language; TMDB is asked
// for exactly those, and the chosen ones are downloaded in the background
// (Art).
func TestTranslationArt(t *testing.T) {
	var mu sync.Mutex
	var langs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/t/p/w500/") {
			w.Write([]byte("IMG"))
			return
		}
		mu.Lock()
		langs = append(langs, r.URL.Query().Get("include_image_language"))
		mu.Unlock()
		switch r.URL.Path {
		case "/3/tv/62852": // logos in Russian and English, posters in Russian and textless
			w.Write([]byte(`{"id":62852,"name":"Billions","images":{
				"logos":[{"file_path":"/ru.png","iso_639_1":"ru"},{"file_path":"/null.png","iso_639_1":null},{"file_path":"/en.svg","iso_639_1":"en"}],
				"posters":[{"file_path":"/pru.jpg","iso_639_1":"ru"},{"file_path":"/pnull.jpg","iso_639_1":null}]}}`))
		case "/3/movie/2": // Russian art only
			w.Write([]byte(`{"id":2,"title":"Two","images":{"logos":[{"file_path":"/ru.png","iso_639_1":"ru"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := &Client{Key: "k", BaseURL: srv.URL + "/3", ImageBase: srv.URL + "/t/p"}
	s := newTranslations(t, c, translationsDB(t))
	s.Art = &Photos{Client: c, Images: &metadata.Images{Dir: t.TempDir()}, Size: ArtSize}
	ctx := context.Background()
	tr, ok := s.Get(ctx, "tv", 62852, "en-US", nil)
	if !ok || tr.Logo != "/en.svg" || tr.Poster != "/pnull.jpg" || !tr.Art {
		t.Fatalf("%+v", tr)
	}
	mu.Lock()
	if langs[0] != "en,null" {
		t.Fatalf("include_image_language %q", langs[0])
	}
	mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, logo := s.Art.Cached("/en.svg")
		_, poster := s.Art.Cached("/pnull.jpg")
		if logo && poster {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("art not downloaded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tr, _ := s.Get(ctx, "movie", 2, "en-US", nil); tr.Logo != "" || tr.Poster != "" {
		t.Fatalf("Russian art for English: %+v", tr)
	}
}

// An entry cached before the art was kept still serves its text, and is
// fetched again once for the art.
func TestTranslationWithoutArtRefetched(t *testing.T) {
	f := newFakeTranslations(t)
	d := translationsDB(t)
	if _, err := d.Exec(`INSERT INTO tmdb_translations VALUES ('movie', 157336, 'en-US', '{"title":"Old"}', ?)`,
		time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	s := newTranslations(t, &Client{Key: "k", BaseURL: f.srv.URL + "/3"}, d)
	ctx := context.Background()
	if tr, ok := s.Peek(ctx, "movie", 157336, "en-US"); !ok || tr.Title != "Old" {
		t.Fatal(tr, ok)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if tr, _ := s.Peek(ctx, "movie", 157336, "en-US"); tr.Art {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not fetched again")
		}
		time.Sleep(10 * time.Millisecond)
	}
	n := f.count()
	if tr, ok := s.Get(ctx, "movie", 157336, "en-US", nil); !ok || tr.Title != "Interstellar" || f.count() != n {
		t.Fatalf("%+v, hits %v", tr, f.requests())
	}
}

// Has: a fresh entry with the seasons asked; never a fetch.
func TestTranslationsHas(t *testing.T) {
	s := &Translations{}
	ctx := context.Background()
	if s.Has(ctx, "tv", 1, "ru-RU", nil) {
		t.Fatal("empty cache has it")
	}
	s.remember(translationKey("tv", 1, "ru-RU"), cachedTranslation{t: Translation{Title: "x", Covers: []int{1}, Art: true}, at: time.Now()})
	if !s.Has(ctx, "tv", 1, "ru-RU", []int{1}) || s.Has(ctx, "tv", 1, "ru-RU", []int{1, 2}) || s.Has(ctx, "tv", 1, "en-US", nil) {
		t.Fatal("seasons or language ignored")
	}
	s.remember(translationKey("movie", 2, "ru-RU"), cachedTranslation{t: Translation{Title: "x", Art: true}, at: time.Now().Add(-2 * translationTTL)})
	if s.Has(ctx, "movie", 2, "ru-RU", nil) {
		t.Fatal("stale entry counts")
	}
}
