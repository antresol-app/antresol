package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Subsonic, OPDS and Jellyfin answers in a text type are gzipped for a
// client that takes it; streams, downloads, pictures, ranges and a client
// that does not take it get the answer as it is, on the connection's own
// writer (net/http's sendfile stays possible).
func TestMuxGzipsTextAnswers(t *testing.T) {
	body := strings.Repeat(`{"name":"a track"},`, 300)
	var seen http.ResponseWriter
	stub := func(ct string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = w
			w.Header().Set("Content-Type", ct)
			io.WriteString(w, body)
		})
	}
	mux := buildMux(http.NotFoundHandler(), stub("text/xml; charset=utf-8"),
		stub("application/atom+xml;profile=opds-catalog;kind=navigation"), stub("application/json; charset=utf-8"))
	get := func(path, enc, rng string) (*httptest.ResponseRecorder, bool) {
		r := httptest.NewRequest("GET", path, nil)
		if enc != "" {
			r.Header.Set("Accept-Encoding", enc)
		}
		if rng != "" {
			r.Header.Set("Range", rng)
		}
		w := httptest.NewRecorder()
		seen = nil
		mux.ServeHTTP(w, r)
		return w, seen == http.ResponseWriter(w)
	}
	for _, path := range []string{"/rest/getAlbumList2.view", "/opds/new", "/Items", "/users/me"} {
		w, raw := get(path, "gzip", "")
		if vary := strings.Join(w.Header().Values("Vary"), ","); w.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(vary, "Accept-Encoding") || raw {
			t.Errorf("%s: encoding %q vary %q", path, w.Header().Get("Content-Encoding"), vary)
			continue
		}
		zr, err := gzip.NewReader(w.Body)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if got, _ := io.ReadAll(zr); string(got) != body {
			t.Errorf("%s: %d bytes, want %d", path, len(got), len(body))
		}
	}
	for _, path := range []string{"/rest/stream.view", "/rest/getCoverArt", "/rest/download", "/opds/files/7/book.epub",
		"/opds/covers/3", "/Videos/5/stream", "/LiveStreams/5/x", "/Items/5/Images/Primary", "/Items/5/Download",
		"/Playback/BitrateTest"} {
		if w, raw := get(path, "gzip", ""); w.Header().Get("Content-Encoding") != "" || !raw || w.Body.String() != body {
			t.Errorf("%s: encoding %q, own writer %v", path, w.Header().Get("Content-Encoding"), raw)
		}
	}
	if w, raw := get("/rest/ping", "", ""); w.Header().Get("Content-Encoding") != "" || w.Body.String() != body || raw {
		t.Errorf("no Accept-Encoding: %q", w.Header().Get("Content-Encoding"))
	}
	if w, _ := get("/rest/ping", "gzip", "bytes=0-9"); w.Header().Get("Content-Encoding") != "" || w.Body.String() != body {
		t.Errorf("a range request was compressed")
	}
}
