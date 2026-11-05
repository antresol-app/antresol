package musicbrainz

import (
	"context"
	"errors"
	"github.com/antresol-app/antresol/internal/version"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/metadata"
)

var testUA = version.UserAgent

const surferRosa = `{"created":"2026-10-25T00:00:00.000Z","count":2,"offset":0,"release-groups":[
 {"id":"6b4fe6e4-27b9-3b2b-9f2b-6d0f0e1b1a8e","type-id":"f529b476","score":100,"primary-type":"Album",
  "title":"Surfer Rosa","first-release-date":"1988-03-21",
  "artist-credit":[{"name":"Pixies","artist":{"id":"b6b2bb8d-54a9-491f-9607-7b546023b433","name":"Pixies","sort-name":"Pixies"}}],
  "tags":[{"count":3,"name":"seen live"}]},
 {"id":"0f0f","score":"62","title":"Surfer Rosa / Come On Pilgrim","first-release-date":"1988","disambiguation":"compilation","aliases":[{"name":"Surfer Rosa & Come On Pilgrim","sort-name":"x"},{"name":" "}],
  "artist-credit":[{"name":"Pixies","joinphrase":" & ","artist":{"id":"b6b2","name":"Pixies"}},{"name":"Friends","artist":{"id":"ff","name":"Friends"}}]}
]}`

func TestDefaultIntervalOneSecond(t *testing.T) {
	if DefaultMinInterval != time.Second {
		t.Fatal(DefaultMinInterval)
	}
	c := &Client{}
	if c.limiter().Interval != time.Second {
		t.Fatalf("nil Limiter gives %v", c.limiter().Interval)
	}
}

func TestSearchReleaseGroupsParses(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/2/release-group" || r.URL.Query().Get("fmt") != "json" || r.URL.Query().Get("limit") != "5" {
			t.Errorf("request %s", r.URL)
		}
		query = r.URL.Query().Get("query")
		w.Write([]byte(surferRosa))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/ws/2", Limiter: &metadata.Limiter{}}
	rs, err := c.SearchReleaseGroups(context.Background(), "Pixies", `Surfer "Rosa"`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if query != `releasegroup:"Surfer \"Rosa\"" AND artist:"Pixies"` {
		t.Fatalf("query %q", query)
	}
	if len(rs) != 2 {
		t.Fatalf("%+v", rs)
	}
	g := rs[0]
	if g.ID != "6b4fe6e4-27b9-3b2b-9f2b-6d0f0e1b1a8e" || g.Title != "Surfer Rosa" || g.Artist != "Pixies" ||
		g.ArtistID != "b6b2bb8d-54a9-491f-9607-7b546023b433" || g.Year != 1988 || g.Score != 100 || len(g.Genres) != 0 ||
		g.Disambiguation != "" || rs[1].Disambiguation != "compilation" || len(g.Aliases) != 0 ||
		len(rs[1].Aliases) != 1 || rs[1].Aliases[0] != "Surfer Rosa & Come On Pilgrim" {
		t.Fatalf("%+v", g)
	}
	if rs[1].Score != 62 || rs[1].Artist != "Pixies & Friends" {
		t.Fatalf("%+v", rs[1])
	}
}

func TestReleaseGroupLookupGenres(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release-group/abc" || r.URL.Query().Get("inc") != "artist-credits genres" {
			t.Errorf("request %s", r.URL)
		}
		w.Write([]byte(`{"id":"abc","title":"Doolittle","first-release-date":"1989-04-17",
		  "artist-credit":[{"name":"Pixies","artist":{"id":"p","name":"Pixies"}}],
		  "genres":[{"name":"indie rock","count":2},{"name":"alternative rock","count":9}]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Limiter: &metadata.Limiter{}}
	g, err := c.ReleaseGroup(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if g.Year != 1989 || strings.Join(g.Genres, ",") != "alternative rock,indie rock" {
		t.Fatalf("%+v", g)
	}
}

func TestUserAgentSent(t *testing.T) {
	var ua atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		w.Write([]byte(`{"release-groups":[]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, UserAgent: testUA, Limiter: &metadata.Limiter{}}
	if _, err := c.SearchFree(context.Background(), "pixies"); err != nil {
		t.Fatal(err)
	}
	if ua.Load() != testUA {
		t.Fatalf("User-Agent %q", ua.Load())
	}
}

func TestRetry503(t *testing.T) {
	old := retryDelay
	retryDelay = time.Millisecond
	defer func() { retryDelay = old }()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"release-groups":[{"id":"x","title":"X","score":95}]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Limiter: &metadata.Limiter{}}
	rs, err := c.search(context.Background(), "x", groupLimit)
	if err != nil || len(rs) != 1 || hits.Load() != 3 {
		t.Fatalf("%v %v hits=%d", rs, err, hits.Load())
	}

	// always 503: 1 try + 3 retries, then an HTTPError
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv2.Close()
	hits.Store(0)
	c2 := &Client{BaseURL: srv2.URL, Limiter: &metadata.Limiter{}}
	_, err = c2.SearchFree(context.Background(), "x")
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 503 || hits.Load() != 4 {
		t.Fatalf("%v hits=%d", err, hits.Load())
	}
}

func TestLookupNotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Limiter: &metadata.Limiter{}}
	if _, err := c.ReleaseGroup(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestRequestsSpaced(t *testing.T) {
	var last atomic.Int64
	var minGap atomic.Int64
	minGap.Store(int64(time.Hour))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UnixNano()
		if prev := last.Swap(now); prev != 0 && now-prev < minGap.Load() {
			minGap.Store(now - prev)
		}
		w.Write([]byte(`{"release-groups":[]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Limiter: &metadata.Limiter{Interval: 50 * time.Millisecond}}
	for i := 0; i < 3; i++ {
		if _, err := c.SearchFree(context.Background(), "x"); err != nil {
			t.Fatal(err)
		}
	}
	if g := time.Duration(minGap.Load()); g < 45*time.Millisecond {
		t.Fatalf("requests %v apart", g)
	}
}

func TestCoverURL(t *testing.T) {
	c := &Client{CoverBase: "https://coverartarchive.org/"}
	if u := c.CoverURL("abc"); u != "https://coverartarchive.org/release-group/abc/front-500" {
		t.Fatal(u)
	}
	if (&Client{}).CoverURL("abc") != "" {
		t.Fatal("no CoverBase must give no URL")
	}
}
