package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// Cast photos come from the server's image cache: one the enricher kept is
// served with TMDB down; one not kept yet is fetched on its first view and
// then served offline too; names are cache-checked, never paths.
func TestCastPhotoServedFromCache(t *testing.T) {
	var hits atomic.Int32
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/t/p/w185/kept.jpg":
			w.Write([]byte("KEPT"))
		case "/t/p/w185/lazy.jpg":
			w.Write([]byte("LAZY"))
		default:
			http.NotFound(w, r)
		}
	}))
	var photos *tmdb.Photos
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		photos = &tmdb.Photos{Client: &tmdb.Client{ImageBase: img.URL + "/t/p"},
			Images: &metadata.Images{Dir: filepath.Join(d.DataDir, "images")}}
		d.CastPhotos = photos
	})
	// as the enricher does at scan time
	if _, err := photos.Fetch(context.Background(), "/kept.jpg"); err != nil {
		t.Fatal(err)
	}
	if w := e.do("GET", "/api/images/cast/lazy.jpg", "", e.admin); w.Code != 200 || w.Body.String() != "LAZY" {
		t.Fatalf("lazy: %d %s", w.Code, w.Body)
	}
	img.Close() // the internet is off
	n := hits.Load()
	for name, want := range map[string]string{"kept.jpg": "KEPT", "lazy.jpg": "LAZY"} {
		if w := e.do("GET", "/api/images/cast/"+name, "", e.admin); w.Code != 200 || w.Body.String() != want {
			t.Fatalf("offline %s: %d %s", name, w.Code, w.Body)
		}
	}
	if hits.Load() != n {
		t.Fatal("cached photos must not be fetched again")
	}
	for _, name := range []string{"never.jpg", "..%2Fv.db", "x.svg"} {
		if w := e.do("GET", "/api/images/cast/"+name, "", e.admin); w.Code != 404 {
			t.Fatalf("%s: %d", name, w.Code)
		}
	}
	if w := e.do("GET", "/api/images/cast/kept.jpg", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", w.Code)
	}
}
