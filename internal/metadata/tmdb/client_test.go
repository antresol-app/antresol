package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const movieJSON = `{
 "id": 157336, "title": "Интерстеллар", "original_title": "Interstellar", "release_date": "2014-11-05",
 "overview": "Фантастика", "runtime": 169, "poster_path": "/p.jpg", "backdrop_path": "/b.jpg",
 "genres": [{"name":"фантастика"},{"name":"драма"}],
 "alternative_titles": {"titles": [{"title":"Interstellar IMAX"},{"title":"Интерстеллар"}]},
 "translations": {"translations": [{"data":{"title":"Interstellar"}},{"data":{"title":"Intérstellaire"}}]},
 "release_dates": {"results": [
   {"iso_3166_1":"US","release_dates":[{"certification":"PG-13"}]},
   {"iso_3166_1":"RU","release_dates":[{"certification":""},{"certification":"12+"}]}]},
 "credits": {"cast": [{"name":"Matthew McConaughey","character":"Cooper","profile_path":"/m.jpg"}]},
 "images": {"logos": [{"file_path":"/en.png","iso_639_1":"en"},{"file_path":"/ru.png","iso_639_1":"ru"}]},
 "external_ids": {"imdb_id": "tt0816692"}
}`

func TestMovieDetailsParsesAltTitles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/movie/157336" || r.URL.Query().Get("language") != "ru-RU" ||
			!strings.Contains(r.URL.Query().Get("append_to_response"), "alternative_titles") {
			t.Errorf("bad request %s", r.URL)
		}
		w.Write([]byte(movieJSON))
	}))
	defer srv.Close()
	c := &Client{Key: "k", BaseURL: srv.URL + "/3"}
	d, err := c.Movie(context.Background(), 157336)
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, a := range d.AltTitles {
		has[a] = true
	}
	for _, w := range []string{"Интерстеллар", "Interstellar", "Interstellar IMAX", "Intérstellaire"} {
		if !has[w] {
			t.Errorf("alt titles lack %q: %v", w, d.AltTitles)
		}
	}
	if d.Rating != "12+" || d.Year != 2014 || d.Runtime != 169 || d.Logo != "/ru.png" || len(d.Cast) != 1 || len(d.Genres) != 2 {
		t.Fatalf("%+v", d)
	}
}

func TestRatingFallsBackToUSMapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":1,"name":"S","first_air_date":"2016-01-01","content_ratings":{"results":[{"iso_3166_1":"US","rating":"TV-MA"}]}}`))
	}))
	defer srv.Close()
	d, err := (&Client{BaseURL: srv.URL}).TV(context.Background(), 1)
	if err != nil || d.Rating != "18+" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestAuthModes(t *testing.T) {
	var gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotAuth = r.URL.Query().Get("api_key"), r.Header.Get("Authorization")
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	(&Client{Key: "short", BaseURL: srv.URL}).SearchMovie(context.Background(), "x", 0)
	if gotKey != "short" || gotAuth != "" {
		t.Fatalf("v3: %q %q", gotKey, gotAuth)
	}
	long := strings.Repeat("a", 60)
	(&Client{Key: long, BaseURL: srv.URL}).SearchMovie(context.Background(), "x", 0)
	if gotKey != "" || gotAuth != "Bearer "+long {
		t.Fatalf("v4: %q %q", gotKey, gotAuth)
	}
}

func TestRetryOn429(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(429)
			return
		}
		w.Write([]byte(`{"results":[{"id":5,"title":"A","release_date":"2001-01-01"}]}`))
	}))
	defer srv.Close()
	c := &Client{Key: "k", BaseURL: srv.URL, Backoff: time.Millisecond}
	rs, err := c.SearchMovie(context.Background(), "a", 2001)
	if err != nil || len(rs) != 1 || rs[0].ID != 5 || rs[0].Year != 2001 || n.Load() != 3 {
		t.Fatalf("%v %v %d", rs, err, n.Load())
	}
	// permanent 429 gives up after 3 retries
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1); w.WriteHeader(429) }))
	defer srv2.Close()
	n.Store(0)
	if _, err := (&Client{BaseURL: srv2.URL, Backoff: time.Millisecond}).SearchTV(context.Background(), "a", 0); err == nil || n.Load() != 4 {
		t.Fatalf("%v %d", err, n.Load())
	}
}

func TestConcurrencyLimit(t *testing.T) {
	var cur, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := cur.Add(1)
		for {
			p := peak.Load()
			if c <= p || peak.CompareAndSwap(p, c) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		cur.Add(-1)
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	done := make(chan struct{})
	for i := 0; i < 12; i++ {
		go func() { c.SearchMovie(context.Background(), "x", 0); done <- struct{}{} }()
	}
	for i := 0; i < 12; i++ {
		<-done
	}
	if peak.Load() > 4 || peak.Load() < 2 {
		t.Fatalf("peak %d", peak.Load())
	}
}

func TestErrorsDoNotLeakKey(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close() // connection refused -> *url.Error
	_, err := (&Client{Key: "SECRETKEY123", BaseURL: base}).SearchMovie(context.Background(), "x", 0)
	if err == nil || strings.Contains(err.Error(), "SECRETKEY123") {
		t.Fatalf("err %v", err)
	}
}

func TestTVSeasonCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":1,"name":"S","seasons":[{"season_number":0,"episode_count":2},{"season_number":1,"episode_count":10}]}`))
	}))
	defer srv.Close()
	d, err := (&Client{BaseURL: srv.URL}).TV(context.Background(), 1)
	if err != nil || len(d.Seasons) != 2 || d.Seasons[1] != (SeasonCount{1, 10}) {
		t.Fatalf("%+v %v", d.Seasons, err)
	}
}

func TestEpisodeAirDateParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv/126308/season/1":
			w.Write([]byte(`{"season_number":1,"air_date":"2024-02-27","episodes":[{"episode_number":1,"name":"Анджин","air_date":"2024-02-27"},{"episode_number":2,"air_date":null}]}`))
		case "/movie/693134":
			w.Write([]byte(`{"id":693134,"title":"Дюна: Часть вторая","release_date":"2024-02-27"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	s, err := c.Season(context.Background(), 126308, 1)
	if err != nil || len(s.Episodes) != 2 || s.Episodes[0].AirDate != "2024-02-27" || s.Episodes[1].AirDate != "" || s.AirDate != "2024-02-27" {
		t.Fatalf("%+v %v", s, err)
	}
	d, err := c.Movie(context.Background(), 693134)
	if err != nil || d.ReleaseDate != "2024-02-27" || d.Year != 2024 {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestStatusErrorTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL}).TV(context.Background(), 1)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 502 || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("%v", err)
	}
}

// external_ids gives the IMDb id (online sources search by it).
func TestDetailsIMDbIDAndLanguage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("append_to_response"), "external_ids") {
			t.Errorf("no external_ids in %s", r.URL)
		}
		w.Write([]byte(`{"id":1,"name":"S","original_language":"en","external_ids":{"imdb_id":"tt0000001"}}`))
	}))
	defer srv.Close()
	d, err := (&Client{BaseURL: srv.URL}).TV(context.Background(), 1)
	if err != nil || d.IMDbID != "tt0000001" || d.OriginalLanguage != "en" {
		t.Fatalf("%+v %v", d, err)
	}
}

// The lookup language: the client's (the library's metadata language,
// ru-RU by default) unless the context asks for the viewer's.
func TestLookupLanguage(t *testing.T) {
	var lang, images atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang.Store(r.URL.Query().Get("language"))
		images.Store(r.URL.Query().Get("include_image_language"))
		w.Write([]byte(movieJSON))
	}))
	defer srv.Close()
	c := &Client{Key: "k", BaseURL: srv.URL + "/3"}
	check := func(ctx context.Context, wantLang, wantImages, wantLogo string) {
		t.Helper()
		d, err := c.Movie(ctx, 157336)
		if err != nil {
			t.Fatal(err)
		}
		if lang.Load() != wantLang || images.Load() != wantImages || d.Logo != wantLogo {
			t.Errorf("language %v, images %v, logo %q; want %s, %s, %s", lang.Load(), images.Load(), d.Logo, wantLang, wantImages, wantLogo)
		}
		if _, err := c.SearchMovie(ctx, "x", 0); err != nil {
			t.Fatal(err)
		}
		if lang.Load() != wantLang {
			t.Errorf("search language %v, want %s", lang.Load(), wantLang)
		}
	}
	bg := context.Background()
	check(bg, "ru-RU", "ru,en,null", "/ru.png")
	check(WithLanguage(bg, "en-US"), "en-US", "en,ru,null", "/en.png")
	c.SetLanguage("en-US") // «Язык метаданных»: English
	check(bg, "en-US", "en,ru,null", "/en.png")
	check(WithLanguage(bg, "ru-RU"), "ru-RU", "ru,en,null", "/ru.png")
	if ContextLanguage(WithLanguage(bg, "en-US")) != "en-US" || ContextLanguage(bg) != "" {
		t.Error("ContextLanguage")
	}
}
