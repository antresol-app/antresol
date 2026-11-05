package api

import (
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestHealth(t *testing.T) {
	r := NewRouter(Deps{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	var b struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || !b.OK || b.Version == "" {
		t.Fatalf("%v %s", err, w.Body)
	}
}

func TestSPAFallback(t *testing.T) {
	r := NewRouter(Deps{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/movies/123", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<div id="root">`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// Deferred minor (Task 1): a missing hashed asset is a 404.
func TestSPAMissingAssetIs404(t *testing.T) {
	r := NewRouter(Deps{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/assets/Manage-old.js", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("%d %s", w.Code, w.Header().Get("Content-Type"))
	}
}

// Book content (EPUB sections are same-origin blob: iframes) must not run
// scripts: the app document and everything the SPA serves carry a CSP that
// allows only same-origin scripts.
func TestSPAContentSecurityPolicy(t *testing.T) {
	r := NewRouter(Deps{})
	for _, p := range []string{"/", "/books/12", "/read/12", "/index.html"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		csp := w.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'self'", "connect-src 'self'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP %q lacks %q", p, csp, want)
			}
		}
		if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
			t.Errorf("%s: CSP too lax: %q", p, csp)
		}
	}
}

// The service worker script is revalidated on every load, so a new build
// reaches open tabs; the app's other files keep their caching.
func TestSWNoCache(t *testing.T) {
	h := spa(fstest.MapFS{
		"sw.js":           {Data: []byte("self.__VAULT_BUILD = 'x';")},
		"index.html":      {Data: []byte(`<div id="root"></div>`)},
		"assets/app-1.js": {Data: []byte("1")},
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/sw.js", nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" || !strings.Contains(w.Body.String(), "__VAULT_BUILD") {
		t.Fatalf("%d %q %s", w.Code, w.Header().Get("Cache-Control"), w.Body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("Content-Type %q", ct)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/assets/app-1.js", nil))
	if w.Header().Get("Cache-Control") == "no-cache" {
		t.Fatal("assets must not be no-cache")
	}
}

// A range of a text file is answered as asked (the range, plain), and the
// event stream is flushed at once, never buffered by a compressor.
func TestCompressionLeavesRangesAndStreams(t *testing.T) {
	const body = "console.log('0123456789abcdefghij');"
	h := compressText(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: 1\n\n"))
			w.(http.Flusher).Flush()
			return
		}
		w.Header().Set("Content-Type", "text/javascript")
		http.ServeContent(w, r, "app.js", time.Time{}, strings.NewReader(body))
	}))
	q := httptest.NewRequest("GET", "/assets/app.js", nil)
	q.Header.Set("Accept-Encoding", "gzip")
	q.Header.Set("Range", "bytes=0-6")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, q)
	if w.Code != http.StatusPartialContent || w.Body.String() != body[:7] || w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("range: %d %q %q", w.Code, w.Body, w.Header().Get("Content-Encoding"))
	}
	q = httptest.NewRequest("GET", "/api/events", nil)
	q.Header.Set("Accept-Encoding", "gzip")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, q)
	if !w.Flushed || w.Body.String() != "data: 1\n\n" || w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("events: flushed %v %q %q", w.Flushed, w.Body, w.Header().Get("Content-Encoding"))
	}
}

// The build's hashed assets never change under their name: the browser
// keeps them a year without asking again; the page itself is revalidated.
func TestSPAAssetsImmutable(t *testing.T) {
	h := spa(fstest.MapFS{
		"index.html":      {Data: []byte(`<div id="root"></div>`)},
		"assets/app-1.js": {Data: []byte("1")},
		"manifest.json":   {Data: []byte("{}")},
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/assets/app-1.js", nil))
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("asset Cache-Control %q", cc)
	}
	for _, p := range []string{"/", "/movies/1", "/index.html", "/manifest.json"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if cc := w.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") || strings.Contains(cc, "max-age") {
			t.Fatalf("%s: Cache-Control %q", p, cc)
		}
	}
}

// JSON and the app's text files are gzipped for a client that takes it;
// the event stream and media are not touched.
func TestCompression(t *testing.T) {
	r := NewRouter(Deps{})
	req := func(p string) *httptest.ResponseRecorder {
		q := httptest.NewRequest("GET", p, nil)
		q.Header.Set("Accept-Encoding", "gzip, deflate, br")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		return w
	}
	w := req("/api/health")
	if w.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Accept-Encoding") {
		t.Fatalf("health: %q %q", w.Header().Get("Content-Encoding"), w.Header().Get("Vary"))
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	var b struct{ OK bool }
	if err := json.NewDecoder(zr).Decode(&b); err != nil || !b.OK {
		t.Fatalf("%v %+v", err, b)
	}
	if w := req("/"); w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("index: %q", w.Header().Get("Content-Encoding"))
	}
	// no Accept-Encoding: plain
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
	if w.Header().Get("Content-Encoding") != "" || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("plain: %q %s", w.Header().Get("Content-Encoding"), w.Body)
	}
	for _, ct := range []string{"text/event-stream", "video/x-matroska", "audio/flac", "image/jpeg", "application/zip"} {
		if compressible(ct) {
			t.Errorf("%s compressed", ct)
		}
	}
	// a plain answer of a compressible type says it depends on the
	// encoding too (a shared cache must not give it to a gzip client, nor
	// the gzip one to a plain client)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
	if !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Accept-Encoding") {
		t.Fatalf("plain answer without Vary: %q", w.Header().Values("Vary"))
	}
	if n := strings.Count(strings.Join(req("/api/health").Header().Values("Vary"), ","), "Accept-Encoding"); n != 1 {
		t.Fatalf("gzip answer: Vary Accept-Encoding %d times", n)
	}
	// media routes are never wrapped (sendfile), whatever they answer
	for _, p := range []string{"/api/stream/5", "/api/tracks/9/stream", "/api/hls/s/seg-1.m4s", "/api/images/a.jpg", "/api/events",
		"/api/music/albums/3/zip", "/api/files/5/trickplay/0.jpg", "/api/subs/5/2.vtt", "/api/torrent-stream/ih/0"} {
		if !mediaRoute(p) {
			t.Errorf("%s: not a media route", p)
		}
	}
	for _, p := range []string{"/api/home", "/api/items/5", "/api/torrents", "/api/music/albums/3", "/api/files/list"} {
		if mediaRoute(p) {
			t.Errorf("%s: a media route", p)
		}
	}
}
