package api

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/playlistimport"
)

// offlineMatcher: MusicBrainz does not answer.
type offlineMatcher struct{}

func (offlineMatcher) Match(context.Context, playlistimport.Track) (playlistimport.Decision, error) {
	return playlistimport.Decision{}, &metadata.HTTPError{Service: "musicbrainz", Path: "/recording", Status: http.StatusServiceUnavailable}
}

// newCoreImportEnv serves the playlist import alone, as the core build
// does (both editions): no connectors, no download queue, no catalog.
func newCoreImportEnv(t *testing.T) *libEnv {
	return newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.PlaylistImport = &playlistimport.Service{Store: &playlistimport.Store{DB: db}, Bus: &events.Bus{}, Matcher: offlineMatcher{}}
	})
}

// uploadFiles posts files as the «Файл» tile does (multipart, field «file»).
func uploadFiles(e *libEnv, files ...[2]string) *httptest.ResponseRecorder {
	e.t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for _, f := range files {
		w, err := mw.CreateFormFile("file", f[0])
		if err != nil {
			e.t.Fatal(err)
		}
		w.Write([]byte(f[1]))
	}
	mw.Close()
	return e.do("POST", "/api/import/files", b.String(), e.admin, "Content-Type", mw.FormDataContentType(), "X-Vault-Upload", "1")
}

// TestImportFilesCreatesPreview: an M3U upload makes a job in the
// preview, in both editions.
func TestImportFilesCreatesPreview(t *testing.T) {
	e := newCoreImportEnv(t)
	m3u := "#EXTM3U\n#EXTINF:401,Кино - Кукушка\n02.flac\n#EXTINF:300,Queen - Bohemian Rhapsody\n11.mp3\n"
	w := uploadFiles(e, [2]string{"Дорога.m3u8", m3u})
	if w.Code != 201 {
		t.Fatalf("upload %d %s", w.Code, w.Body)
	}
	j := decodeJSON[struct {
		ID      int64  `json:"id"`
		Status  string `json:"status"`
		Service string `json:"service"`
		Counts  struct{ Songs int }
	}](t, w.Body.Bytes())
	if j.Status != playlistimport.StatusPreview || j.Service != "file" || j.Counts.Songs != 2 {
		t.Fatalf("job %s", w.Body)
	}
	// the routes need a session
	if w := e.do("GET", "/api/import/jobs", "", nil); w.Code != 401 {
		t.Fatalf("no session: %d", w.Code)
	}
}
