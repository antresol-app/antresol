//go:build !sources

package api

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/files"
)

// settingsEnvFull: the core build has no release preferences.
type settingsEnvFull struct{}

func (e *settingsEnv) withFull(*testing.T, *Deps) {}

// jsonKeys is the set of top-level keys of a JSON object.
func jsonKeys(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// TestLibraryEditionSettings: /api/settings holds the library's own
// settings only, and saves them without the sources build's fields.
func TestLibraryEditionSettings(t *testing.T) {
	e := newSettingsEnv(t)
	w := e.do("GET", "/api/settings", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	keys := jsonKeys(t, w.Body.Bytes())
	for _, k := range []string{"uploadLimited", "uploadLimitKBs", "streamCacheGB", "onlineEnabled", "quality", "voice", "voices",
		"maxSizeGB", "preferLossless", "musicFormat", "mp3Bitrate", "bookLangs", "audiobookLangs", "comicLangs"} {
		if keys[k] {
			t.Errorf("settings has %q: %s", k, w.Body)
		}
	}
	for _, k := range []string{"remoteQuality", "remoteMusic", "autoSubtitles", "subtitleLangs", "languages", "metadataLanguage", "subtitlesAvailable", "studios"} {
		if !keys[k] {
			t.Errorf("settings lacks %q: %s", k, w.Body)
		}
	}
	w = e.do("PUT", "/api/settings", `{"remoteQuality":"720","languages":["en"],"subtitleLangs":["en"]}`, e.admin)
	if w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	var got struct {
		RemoteQuality string   `json:"remoteQuality"`
		Languages     []string `json:"languages"`
	}
	json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &got)
	if got.RemoteQuality != "720" || len(got.Languages) != 1 || got.Languages[0] != "en" {
		t.Fatalf("saved %+v", got)
	}
}

// TestLibraryEditionStatsAndMe: no sharing block in /api/stats; /api/me
// says libraryOnly and nothing about another build's features.
func TestLibraryEditionStatsAndMe(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db })
	w := e.do("GET", "/api/stats", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if keys := jsonKeys(t, w.Body.Bytes()); keys["seeding"] || !keys["counts"] || !keys["disk"] {
		t.Fatalf("stats %s", w.Body)
	}
	w = e.do("GET", "/api/me", "", e.admin)
	keys := jsonKeys(t, w.Body.Bytes())
	if keys["canAcquire"] || keys["canWatchOnline"] || !keys["libraryOnly"] {
		t.Fatalf("me %s", w.Body)
	}
	var me struct {
		LibraryOnly bool `json:"libraryOnly"`
	}
	json.Unmarshal(w.Body.Bytes(), &me)
	if !me.LibraryOnly {
		t.Fatalf("libraryOnly false: %s", w.Body)
	}
}

// TestLibraryEditionRoutesAbsent: the sources build's routes do not exist,
// for an admin either.
func TestLibraryEditionRoutesAbsent(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		d.MediaImport = testMediaImport(t, d) // «Переезд» is core; its sources build's parts are not
	})
	for _, p := range []string{"/api/online/status", "/api/discover?q=x", "/api/torrents", "/api/sources", "/api/follow",
		"/api/migrate/switch", "/api/arrivals", "/api/disk", "/api/acquire/progress?kind=movie&tmdbId=1", "/api/torrent-stream/x/0", "/api/torrent-engine"} {
		if w := e.do("GET", p, "", e.admin); w.Code != 404 {
			t.Errorf("GET %s: %d %s", p, w.Code, w.Body)
		}
	}
	if w := e.do("POST", "/api/acquire", `{}`, e.admin); w.Code != 404 {
		t.Errorf("POST /api/acquire: %d", w.Code)
	}
	if w := e.do("POST", "/api/migrate/reports/x/rollback", `{}`, e.admin); w.Code != 404 {
		t.Errorf("POST rollback: %d", w.Code)
	}
}

// TestLibraryEditionNoDiskBreakdown: the disk breakdown of «Статистика»
// is the sources build's, absent with the file browser too.
func TestLibraryEditionNoDiskBreakdown(t *testing.T) {
	mr := t.TempDir()
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		sb := files.NewSandbox(mr, []files.Root{{ID: "movies", Title: "Фильмы", Dir: filepath.Join(mr, "movies")}})
		d.Files = files.NewService(files.ServiceConfig{Sandbox: sb, DB: db, Library: d.Library, DataDir: t.TempDir(), NoDiskUsage: true})
	})
	for _, c := range []string{"movies", "other"} {
		if w := e.do("GET", "/api/stats/disk/"+c, "", e.admin); w.Code != 404 {
			t.Errorf("%s: %d %s", c, w.Code, w.Body)
		}
	}
}

// TestLibraryEditionRequestsClosed: kids' requests are core, but closed in
// the core build for now (nothing sets Deps.RequestsOpen): the admin's
// list is empty and /api/me offers nothing.
func TestLibraryEditionRequestsClosed(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		d.Requests = fakeRequests{}
	})
	w := e.do("GET", "/api/requests", "", e.admin)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("requests: %d %s", w.Code, w.Body)
	}
	var me struct {
		Requests   bool `json:"requests"`
		CanRequest bool `json:"canRequest"`
	}
	if w := e.do("GET", "/api/me", "", e.admin); json.Unmarshal(w.Body.Bytes(), &me) != nil || me.Requests || me.CanRequest || !jsonKeys(t, w.Body.Bytes())["requests"] {
		t.Fatalf("me: %s", w.Body)
	}
	if w := e.do("GET", "/api/arrivals", "", e.admin); w.Code != 404 {
		t.Errorf("arrivals: %d", w.Code)
	}
}

// TestLibraryEditionImportFiles: the playlist import is core: its jobs are
// there (none yet), and a file upload makes one.
func TestLibraryEditionImportFiles(t *testing.T) {
	e := newCoreImportEnv(t)
	if w := e.do("GET", "/api/import/jobs", "", e.admin); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"jobs":[]}` {
		t.Fatalf("jobs: %d %s", w.Code, w.Body)
	}
	if w := uploadFiles(e, [2]string{"a.m3u", "#EXTM3U\n#EXTINF:1,Кино - Кукушка\n1.flac\n"}); w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
}

// TestImportServicesEmptyInCore: with no sign-in services the tiles are [].
func TestImportServicesEmptyInCore(t *testing.T) {
	e := newCoreImportEnv(t)
	w := e.do("GET", "/api/import/services", "", e.admin)
	if w.Code != 200 || strings.ReplaceAll(strings.TrimSpace(w.Body.String()), " ", "") != `{"services":[]}` {
		t.Fatalf("services: %d %s", w.Code, w.Body)
	}
}

// TestImportRetryAbsentInLibrary: «Повторить» of an import and the
// sign-ins are another build's: no such routes.
func TestImportRetryAbsentInLibrary(t *testing.T) {
	e := newCoreImportEnv(t)
	for _, p := range []string{"/api/import/groups/1/retry", "/api/import/connect/spotify", "/api/import/deezer/public"} {
		if w := e.do("POST", p, `{}`, e.admin); w.Code != 404 {
			t.Errorf("POST %s: %d %s", p, w.Code, w.Body)
		}
	}
}
