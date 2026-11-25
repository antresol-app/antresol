package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

type searchHit struct {
	Kind       string `json:"kind"`
	TMDBID     int    `json:"tmdbId"`
	ExtID      string `json:"extId"`
	Title      string `json:"title"`
	Poster     string `json:"poster"`
	KidRequest *struct {
		Key     string `json:"key"`
		Allowed bool   `json:"allowed"`
		Request *struct {
			ID int64 `json:"id"`
		} `json:"request"`
	} `json:"kidRequest"`
}

// fakeTMDB answers searches with movies (one adult) and certifications
// (rating: of every title).
func fakeTMDB(t *testing.T, rating string) *tmdb.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/3/search/movie":
			w.Write([]byte(`{"results":[{"id":603,"title":"Матрица","release_date":"1999-03-30","poster_path":"/m.jpg"},
				{"id":666,"title":"Взрослое","adult":true}]}`))
		case r.URL.Path == "/3/search/tv":
			w.Write([]byte(`{"results":[]}`))
		case strings.HasSuffix(r.URL.Path, "/release_dates"):
			w.Write([]byte(`{"results":[{"iso_3166_1":"RU","release_dates":[{"certification":"` + rating + `"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &tmdb.Client{Key: "k", BaseURL: srv.URL + "/3"}
}

// newSearchEnv: requests open, and an addon "dl" that searches albums and
// books, movies and series from a fake TMDB.
func newSearchEnv(t *testing.T, rating string) *reqEnv {
	tm := fakeTMDB(t, rating)
	e := newReqEnv(t, fakeRequests{}, func(d *Deps) { d.Requests = TMDBRequests(tm, d.Library) })
	e.addon("dl", "Каталог", nil, "requests.search")
	return e
}

func (e *reqEnv) search(c *http.Cookie, q string) []searchHit {
	e.t.Helper()
	w := e.do("GET", "/api/requests/search?"+q, "", c)
	if w.Code != 200 {
		e.t.Fatalf("search %s: %d %s", q, w.Code, w.Body)
	}
	return decodeJSON[[]searchHit](e.t, w.Body.Bytes())
}

// Video comes from TMDB: adult titles dropped, each hit with its
// kidRequest (the kid's request once it asked).
func TestRequestSearch_VideoFromTMDB(t *testing.T) {
	e := newSearchEnv(t, "6")
	kid := e.kid("")
	hits := e.search(kid, "kind=video&q=matrix")
	if len(hits) != 1 || hits[0].TMDBID != 603 || hits[0].Kind != "movie" || hits[0].Title != "Матрица" ||
		!strings.HasSuffix(hits[0].Poster, "/w342/m.jpg") || hits[0].KidRequest == nil || hits[0].KidRequest.Key != "tmdb:603" ||
		!hits[0].KidRequest.Allowed || hits[0].KidRequest.Request != nil {
		t.Fatalf("%+v", hits)
	}
	if n := len(e.hookCalls("requests.search")); n != 0 {
		t.Fatalf("video asked the addons %d times", n)
	}
}

// Albums and books come from the addons: explicit ones dropped, no
// outside covers; ported from the full build's kid book search
// (TestKidBookSearchNoCoversNoErotica).
func TestRequestSearch_MusicFromHook(t *testing.T) {
	e := newSearchEnv(t, "6")
	e.answer("dl", "requests.search", 200, `{"candidates":[
		{"kind":"music","extId":"mb-1","title":"Группа крови","subtitle":"Кино","year":1988,"poster":"https://coverartarchive.org/x"},
		{"kind":"music","extId":"mb-2","title":"Нельзя","subtitle":"Некто","explicit":true},
		{"kind":"book","extId":"/works/OL1W","title":"Чужое"}]}`)
	kid := e.kid("")
	hits := e.search(kid, "kind=music&q=kino")
	if len(hits) != 1 || hits[0].ExtID != "mb-1" || hits[0].Poster != "" || hits[0].KidRequest == nil || hits[0].KidRequest.Key != "ext:mb-1" {
		t.Fatalf("%+v", hits)
	}
	if c := e.hookCalls("requests.search"); len(c) != 1 || c[0].body != `{"kind":"music","q":"kino"}` {
		t.Fatalf("calls %+v", c)
	}
	if w := e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-1","title":"Группа крови","subtitle":"Кино"}`, kid); w.Code != 200 {
		t.Fatalf("ask: %d %s", w.Code, w.Body)
	}
	if hits := e.search(kid, "kind=music&q=kino"); len(hits) != 1 || hits[0].KidRequest.Request == nil {
		t.Fatalf("after asking: %+v", hits)
	}
	// garbage from an addon is left out
	e.answer("dl", "requests.search", 200, `{"candidates":"nope"}`)
	if hits := e.search(kid, "kind=book&q=x"); len(hits) != 0 {
		t.Fatalf("garbage: %+v", hits)
	}
}

// A title rated over the kid's limit is not offered.
func TestRequestSearch_RatingDropped(t *testing.T) {
	e := newSearchEnv(t, "16")
	kid := e.kid("6+")
	if hits := e.search(kid, "kind=video&q=matrix"); len(hits) != 0 {
		t.Fatalf("%+v", hits)
	}
}

// Only kids search for requests.
func TestRequestSearch_NotKid(t *testing.T) {
	e := newSearchEnv(t, "6")
	if w := e.do("GET", "/api/requests/search?kind=video&q=x", "", e.admin); w.Code != http.StatusForbidden {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	kid := e.kid("")
	if w := e.do("GET", "/api/requests/search?kind=nope&q=x", "", kid); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind: %d %s", w.Code, w.Body)
	}
	if hits := e.search(kid, "kind=video&q=+"); len(hits) != 0 {
		t.Fatalf("empty query: %+v", hits)
	}
}

// Requests closed: 403.
func TestRequestSearch_Closed(t *testing.T) {
	e := newReqEnv(t, fakeRequests{}, func(d *Deps) { d.RequestsOpen = func(context.Context) bool { return false } })
	kid := e.kid("")
	w := e.do("GET", "/api/requests/search?kind=video&q=x", "", kid)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Запросы здесь не принимаются") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
