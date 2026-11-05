package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Certification reads only the release dates (movie) or content ratings
// (show), not the details with their appends; search excludes adult titles.
func TestCertificationAndAdultSearch(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		switch r.URL.Path {
		case "/movie/7/release_dates":
			w.Write([]byte(`{"id":7,"results":[{"iso_3166_1":"US","release_dates":[{"certification":"PG-13"}]}]}`))
		case "/tv/9/content_ratings":
			w.Write([]byte(`{"id":9,"results":[{"iso_3166_1":"RU","rating":"16"}]}`))
		case "/search/movie":
			w.Write([]byte(`{"results":[{"id":1,"title":"A","adult":true},{"id":2,"title":"B"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	if r, err := c.Certification(context.Background(), "movie", 7); err != nil || r != "12+" {
		t.Errorf("movie: %q %v", r, err)
	}
	if r, err := c.Certification(context.Background(), "tv", 9); err != nil || r != "16+" {
		t.Errorf("tv: %q %v", r, err)
	}
	if _, err := c.Certification(context.Background(), "movie", 404); err == nil {
		t.Error("missing title: no error")
	}
	rs, err := c.SearchMovie(context.Background(), "x", 0)
	if err != nil || len(rs) != 2 || !rs[0].Adult || rs[1].Adult {
		t.Errorf("search: %+v %v", rs, err)
	}
	for _, p := range paths {
		if strings.Contains(p, "append_to_response") {
			t.Errorf("details fetched: %s", p)
		}
		if strings.HasPrefix(p, "/search/") && !strings.Contains(p, "include_adult=false") {
			t.Errorf("search without include_adult=false: %s", p)
		}
	}
}
