package api

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
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

// GET /api/import/services also says what the server does with the albums
// the library lacks (downloads: an addon takes them) and whether the
// review can search a catalog: the page words its texts by these, not by
// what else the addons offer.
func TestImportServicesOffers(t *testing.T) {
	var svc *playlistimport.Service
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		svc = &playlistimport.Service{Store: &playlistimport.Store{DB: db}, Bus: &events.Bus{}, Matcher: offlineMatcher{}}
		d.PlaylistImport = svc
	})
	body := func() string {
		t.Helper()
		w := e.do("GET", "/api/import/services", "", e.admin)
		if w.Code != 200 {
			t.Fatalf("services: %d %s", w.Code, w.Body)
		}
		return strings.TrimSpace(w.Body.String())
	}
	if got := body(); got != `{"catalog":false,"downloads":false,"services":[]}` {
		t.Fatalf("no offers: %s", got)
	}
	var asked int
	svc.Offers = func(ctx context.Context) playlistimport.Offers {
		asked++
		if ctx == nil {
			t.Error("no context")
		}
		return playlistimport.Offers{Downloads: true, Catalog: true}
	}
	if got := body(); got != `{"catalog":true,"downloads":true,"services":[]}` || asked != 1 {
		t.Fatalf("offers: %s (asked %d)", got, asked)
	}
}
