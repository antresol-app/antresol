package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// titleTMDB is a fake TMDB: the movie 7 «Дюна» (rating: its RU
// certification, adult: TMDB's mark), the series 42 «Сёгун» (specials, an
// empty season and two regular ones), the search of «дюна», and 404 else.
// certs counts the rating-only lookups (/release_dates).
type titleTMDB struct {
	client *tmdb.Client
	certs  atomic.Int32
}

func newTitleTMDB(t *testing.T, rating string, adult bool) *titleTMDB {
	f := &titleTMDB{}
	adultJSON := "false"
	if adult {
		adultJSON = "true"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/movie/7":
			w.Write([]byte(`{"id":7,"adult":` + adultJSON + `,"title":"Дюна","original_title":"Dune","release_date":"2024-02-27",
				"overview":"Пустыня.","runtime":166,"poster_path":"/p.jpg","backdrop_path":"/b.jpg",
				"genres":[{"id":878,"name":"фантастика"}],"external_ids":{"imdb_id":"tt15239678"},
				"credits":{"cast":[{"name":"Тимоти Шаламе","character":"Пол","profile_path":"/t.jpg"}]},
				"release_dates":{"results":[{"iso_3166_1":"RU","release_dates":[{"certification":"` + rating + `"}]}]}}`))
		case "/3/tv/42":
			w.Write([]byte(`{"id":42,"name":"Сёгун","original_name":"Сёгун","first_air_date":"2024-02-27",
				"seasons":[{"season_number":0,"episode_count":3},{"season_number":1,"episode_count":10},
				{"season_number":2,"episode_count":0},{"season_number":3,"episode_count":8}],
				"content_ratings":{"results":[{"iso_3166_1":"RU","rating":"` + rating + `"}]}}`))
		case "/3/search/movie":
			w.Write([]byte(`{"results":[{"id":7,"title":"Дюна","release_date":"2024-02-27"}]}`))
		case "/3/search/tv":
			w.Write([]byte(`{"results":[]}`))
		case "/3/movie/7/release_dates":
			f.certs.Add(1)
			w.Write([]byte(`{"results":[{"iso_3166_1":"RU","release_dates":[{"certification":"` + rating + `"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	f.client = &tmdb.Client{Key: "k", BaseURL: srv.URL + "/3"}
	return f
}

// newTitleEnv: requests open (open false: closed), TMDB from tm (nil: no
// key).
func newTitleEnv(t *testing.T, tm *titleTMDB, open bool) *libEnv {
	return newCoreRequestsEnv(t, fakeRequests{}, func(d *Deps) {
		if tm != nil {
			d.Titles = tm.client
			d.Requests = TMDBRequests(tm.client, d.Library)
		}
		if !open {
			d.RequestsOpen = func(context.Context) bool { return false }
		}
	})
}

type titleAnswer struct {
	TMDBID        int      `json:"tmdbId"`
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"originalTitle"`
	Year          int      `json:"year"`
	Overview      string   `json:"overview"`
	Poster        string   `json:"poster"`
	Backdrop      string   `json:"backdrop"`
	Genres        []string `json:"genres"`
	Runtime       int      `json:"runtime"`
	Rating        string   `json:"rating"`
	IMDbID        string   `json:"imdbId"`
	InLibrary     bool     `json:"inLibrary"`
	LibraryItemID *int64   `json:"libraryItemId"`
	Cast          []struct {
		Name, Character, Photo string
	} `json:"cast"`
	Seasons []struct {
		Number   int `json:"number"`
		Episodes int `json:"episodes"`
	} `json:"seasons"`
	KidRequest *struct {
		Key     string `json:"key"`
		Allowed bool   `json:"allowed"`
		Request *struct {
			ID int64 `json:"id"`
		} `json:"request"`
	} `json:"kidRequest"`
}

func (e *libEnv) title(c *http.Cookie, path string) titleAnswer {
	e.t.Helper()
	w := e.do("GET", path, "", c)
	if w.Code != 200 {
		e.t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	return decodeJSON[titleAnswer](e.t, w.Body.Bytes())
}

// A movie: TMDB's details, credits, external ids and rating; the library's
// copy of it makes inLibrary and libraryItemId.
func TestTitle_Movie(t *testing.T) {
	e := newTitleEnv(t, newTitleTMDB(t, "12", false), true)
	got := e.title(e.admin, "/api/title/movie/7")
	if got.TMDBID != 7 || got.Kind != "movie" || got.Title != "Дюна" || got.OriginalTitle != "Dune" || got.Year != 2024 ||
		got.Overview != "Пустыня." || got.Runtime != 166 || got.Rating != "12+" || got.IMDbID != "tt15239678" ||
		!strings.HasSuffix(got.Poster, "/w342/p.jpg") || !strings.HasSuffix(got.Backdrop, "/w1280/b.jpg") ||
		!slices.Equal(got.Genres, []string{"фантастика"}) || len(got.Cast) != 1 || got.Cast[0].Name != "Тимоти Шаламе" ||
		got.Cast[0].Character != "Пол" || !strings.HasSuffix(got.Cast[0].Photo, "/w185/t.jpg") ||
		got.Seasons == nil || len(got.Seasons) != 0 || got.InLibrary || got.LibraryItemID != nil || got.KidRequest != nil {
		t.Fatalf("%+v", got)
	}
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "Дюна", TmdbID: 7,
		File: library.FileInput{Path: "/m/Dune (2024)/Dune.mkv", Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	got = e.title(e.admin, "/api/title/movie/7")
	if !got.InLibrary || got.LibraryItemID == nil || *got.LibraryItemID != id {
		t.Fatalf("in library: %+v", got)
	}
}

// The answer has the keys of the legacy title page (acquire.TitleInfo:
// Candidate's keys and the title's) without follow; libraryItemId is null
// off the library; kidRequest only for kids.
func TestTitle_Keys(t *testing.T) {
	e := newTitleEnv(t, newTitleTMDB(t, "6", false), true)
	keys := func(c *http.Cookie) []string {
		w := e.do("GET", "/api/title/movie/7", "", c)
		var m map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		if string(m["libraryItemId"]) != "null" {
			t.Errorf("libraryItemId %s", m["libraryItemId"])
		}
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		slices.Sort(ks)
		return ks
	}
	legacy := []string{"backdrop", "cast", "extId", "genres", "imdbId", "inLibrary", "kind", "libraryItemId", "originalTitle",
		"overview", "poster", "rating", "runtime", "seasons", "subtitle", "title", "tmdbId", "year"}
	if got := keys(e.admin); !slices.Equal(got, legacy) {
		t.Errorf("adult keys %v, want %v", got, legacy)
	}
	kid := append(slices.Clone(legacy), "kidRequest")
	slices.Sort(kid)
	if got := keys(e.kid("")); !slices.Equal(got, kid) {
		t.Errorf("kid keys %v, want %v", got, kid)
	}
}

// A series: regular seasons with their episodes (specials and empty
// seasons dropped).
func TestTitle_SeriesSeasons(t *testing.T) {
	e := newTitleEnv(t, newTitleTMDB(t, "16", false), true)
	got := e.title(e.admin, "/api/title/series/42")
	if got.Kind != "series" || got.Title != "Сёгун" || got.OriginalTitle != "" || got.Rating != "16+" ||
		len(got.Seasons) != 2 || got.Seasons[0].Number != 1 || got.Seasons[0].Episodes != 10 ||
		got.Seasons[1].Number != 3 || got.Seasons[1].Episodes != 8 || got.Genres == nil || got.Cast == nil {
		t.Fatalf("%+v", got)
	}
}

// Requests closed: a kid gets no title page.
func TestTitle_KidRequestsClosed(t *testing.T) {
	e := newTitleEnv(t, newTitleTMDB(t, "6", false), false)
	w := e.do("GET", "/api/title/movie/7", "", e.kid(""))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Недоступно в детском профиле") ||
		strings.Contains(w.Body.String(), "Дюна") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/title/movie/7", "", e.admin); w.Code != 200 {
		t.Fatalf("adult: %d %s", w.Code, w.Body)
	}
}

// Over the kid's rating, unrated or adult: 403 with nothing of the title.
func TestTitle_KidOverRating(t *testing.T) {
	for _, c := range []struct {
		name, rating, limit string
		adult               bool
	}{
		{"over", "16", "12+", false},
		{"unrated", "", "", false},
		{"adult", "6", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newTitleEnv(t, newTitleTMDB(t, c.rating, c.adult), true)
			w := e.do("GET", "/api/title/movie/7", "", e.kid(c.limit))
			body := w.Body.String()
			if w.Code != http.StatusForbidden || !strings.Contains(body, "Недоступно для этого профиля") {
				t.Fatalf("%d %s", w.Code, body)
			}
			for _, leak := range []string{"Дюна", "Dune", "p.jpg", "b.jpg", "Пустыня", "tt15239678", "Шаламе", `"tmdbId"`} {
				if strings.Contains(body, leak) {
					t.Errorf("leaks %q: %s", leak, body)
				}
			}
		})
	}
}

// Under the kid's rating: the page with its kidRequest (and the kid's
// request once it asked).
func TestTitle_KidUnderRating(t *testing.T) {
	e := newTitleEnv(t, newTitleTMDB(t, "6", false), true)
	kid := e.kid("12+")
	got := e.title(kid, "/api/title/movie/7")
	if got.Title != "Дюна" || got.KidRequest == nil || got.KidRequest.Key != "tmdb:7" || !got.KidRequest.Allowed ||
		got.KidRequest.Request != nil {
		t.Fatalf("%+v", got)
	}
	req := e.askMovie(kid, "7")
	got = e.title(kid, "/api/title/movie/7")
	if got.KidRequest == nil || got.KidRequest.Request == nil || got.KidRequest.Request.ID != req.ID {
		t.Fatalf("after asking: %+v", got)
	}
}

// The kid's title page puts the rating into the shared cache: the kid's
// search of that title asks TMDB for no rating again.
func TestTitle_RatingCached(t *testing.T) {
	tm := newTitleTMDB(t, "6", false)
	e := newTitleEnv(t, tm, true)
	kid := e.kid("12+")
	e.title(kid, "/api/title/movie/7")
	w := e.do("GET", "/api/requests/search?kind=video&q=дюна", "", kid)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"tmdbId":7`) {
		t.Fatalf("search: %d %s", w.Code, w.Body)
	}
	if n := tm.certs.Load(); n != 0 {
		t.Fatalf("%d certification lookups after the title page", n)
	}
	// control: without the page the search looks the rating up
	e2 := newTitleEnv(t, tm, true)
	if w := e2.do("GET", "/api/requests/search?kind=video&q=дюна", "", e2.kid("12+")); w.Code != 200 || tm.certs.Load() != 1 {
		t.Fatalf("control: %d %s, %d lookups", w.Code, w.Body, tm.certs.Load())
	}
}

// A kind or an id the page does not know, or a title TMDB has none of: 404.
func TestTitle_Unknown(t *testing.T) {
	e := newTitleEnv(t, newTitleTMDB(t, "6", false), true)
	for _, p := range []string{"/api/title/movie/8", "/api/title/book/7", "/api/title/movie/0", "/api/title/movie/x", "/api/title/series/-1"} {
		w := e.do("GET", p, "", e.admin)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "Неизвестный фильм или сериал") {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
	if w := e.do("GET", "/api/title/movie/7", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", w.Code)
	}
}

// Without a TMDB key, or TMDB down: 503.
func TestTitle_TMDBOff(t *testing.T) {
	e := newTitleEnv(t, nil, true)
	if w := e.do("GET", "/api/title/movie/7", "", e.admin); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no key: %d %s", w.Code, w.Body)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(down.Close)
	e = newCoreRequestsEnv(t, fakeRequests{}, func(d *Deps) { d.Titles = &tmdb.Client{Key: "k", BaseURL: down.URL} })
	if w := e.do("GET", "/api/title/movie/7", "", e.admin); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("down: %d %s", w.Code, w.Body)
	}
}

// Ported from the legacy TestDiscoverTitle: an adult's page, a bad kind, a
// kid's page (no kidRequest on an adult's).
func TestTitle_LegacyDiscover(t *testing.T) {
	e := newTitleEnv(t, newTitleTMDB(t, "6", false), true)
	user := e.login(`{"name":"Мама"}`)
	got := e.title(user, "/api/title/movie/7")
	if got.TMDBID != 7 || got.Kind != "movie" || got.Title != "Дюна" || got.Year != 2024 || got.InLibrary || got.Genres == nil ||
		got.KidRequest != nil {
		t.Errorf("%+v", got)
	}
	if w := e.do("GET", "/api/title/book/7", "", user); w.Code != http.StatusNotFound {
		t.Errorf("bad kind: %d", w.Code)
	}
	if w := e.do("GET", "/api/title/movie/7", "", e.kid("")); w.Code != http.StatusOK {
		t.Errorf("kid: %d %s", w.Code, w.Body)
	}
}

// A rating-limited adult gets the same gate as a kid: over its limit,
// unrated or adult, 403 with nothing of the title; within it, the page
// (no kidRequest). An adult with no limit sees everything.
func TestTitle_LimitedAdult(t *testing.T) {
	for _, c := range []struct {
		name, rating string
		adult, ok    bool
	}{
		{"over", "18", false, false},
		{"unrated", "", false, false},
		{"adult", "6", true, false},
		{"within", "12", false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newTitleEnv(t, newTitleTMDB(t, c.rating, c.adult), false)
			limited := e.login(`{"name":"Петя","maxRating":"16+"}`)
			w := e.do("GET", "/api/title/movie/7", "", limited)
			body := w.Body.String()
			if c.ok {
				if w.Code != 200 || !strings.Contains(body, "Дюна") || strings.Contains(body, "kidRequest") {
					t.Fatalf("%d %s", w.Code, body)
				}
				return
			}
			if w.Code != http.StatusForbidden || !strings.Contains(body, "Недоступно для этого профиля") {
				t.Fatalf("%d %s", w.Code, body)
			}
			for _, leak := range []string{"Дюна", "Dune", "p.jpg", "Пустыня", `"tmdbId"`} {
				if strings.Contains(body, leak) {
					t.Errorf("leaks %q: %s", leak, body)
				}
			}
			if w := e.do("GET", "/api/title/movie/7", "", e.admin); w.Code != 200 {
				t.Fatalf("unlimited adult: %d %s", w.Code, w.Body)
			}
		})
	}
}
