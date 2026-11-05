package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/mediaimport"
)

type mediaImportEnv struct {
	*libEnv
	svc *mediaimport.Service
}

// testMediaImport is the media-server import over the databases of an
// empty import folder; it stops with the test.
func testMediaImport(t *testing.T, d *Deps) *mediaimport.Service {
	data := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	jf, kv, abs := mediaimport.DefaultPaths(t.TempDir())
	svc := mediaimport.NewService(&mediaimport.Service{Profiles: d.Users, Library: d.Library, Progress: d.Progress,
		Jellyfin: jf, Kavita: kv, ABS: abs,
		ReportDir: filepath.Join(data, "migrate"), TmpDir: filepath.Join(data, "tmp"), BaseCtx: ctx, Bus: &events.Bus{}})
	t.Cleanup(func() { cancel(); svc.Wait() })
	return svc
}

// newMediaImportEnv serves the media-server import alone (both editions).
func newMediaImportEnv(t *testing.T) *mediaImportEnv {
	ie := &mediaImportEnv{}
	ie.libEnv = newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		ie.svc = testMediaImport(t, d)
		d.MediaImport = ie.svc
	})
	return ie
}

func errText(w *httptest.ResponseRecorder) string {
	var e struct {
		Error string `json:"error"`
	}
	json.Unmarshal(w.Body.Bytes(), &e)
	return e.Error
}

func (e *mediaImportEnv) waitIdle() {
	until := time.Now().Add(10 * time.Second)
	for e.svc.Running() != "" {
		if time.Now().After(until) {
			e.t.Fatal("the run did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMediaImportStatus(t *testing.T) {
	e := newMediaImportEnv(t)
	w := e.do("GET", "/api/migrate", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var st struct {
		Sources []struct{ ID, Name string } `json:"sources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Sources) != 3 || st.Sources[0].ID != "jellyfin" || st.Sources[1].ID != "kavita" || st.Sources[2].ID != "audiobookshelf" {
		t.Fatalf("sources %s", w.Body)
	}
	for _, k := range []string{`"running":null`, `"libraryReady":{"ok":true`, `"leftovers":[]`, `"users":[]`} {
		if !strings.Contains(w.Body.String(), k) {
			t.Errorf("missing %s in %s", k, w.Body)
		}
	}
	// the switch that follows a move is another build's: it is not in this status
	for _, k := range []string{`"pendingFollows"`, `"state"`} {
		if strings.Contains(w.Body.String(), k) {
			t.Errorf("unexpected %s in %s", k, w.Body)
		}
	}
	if w := e.do("GET", "/api/migrate/state", "", e.admin); w.Code != 200 || strings.Contains(w.Body.String(), "pendingFollows") {
		t.Errorf("state %d %s", w.Code, w.Body)
	}
}

func TestMediaImportRunRequiresDryRunHTTP(t *testing.T) {
	e := newMediaImportEnv(t)
	w := e.do("POST", "/api/migrate/run", `{"sources":["jellyfin"],"dryRun":false}`, e.admin)
	if w.Code != http.StatusConflict || errText(w) != "Сначала проверьте, что будет перенесено" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = e.do("POST", "/api/migrate/run", `{"sources":["jellyfin"],"dryRun":true,"userMap":{}}`, e.admin)
	if w.Code != http.StatusAccepted {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var r struct {
		ReportID string `json:"reportId"`
	}
	json.Unmarshal(w.Body.Bytes(), &r)
	e.waitIdle()
	w = e.do("POST", "/api/migrate/run", `{"sources":["jellyfin"],"dryRun":false,"planId":"`+r.ReportID+`"}`, e.admin)
	if w.Code != http.StatusAccepted {
		t.Fatalf("real: %d %s", w.Code, w.Body)
	}
	e.waitIdle()
	if w := e.do("GET", "/api/migrate/reports", "", e.admin); w.Code != 200 || strings.Count(w.Body.String(), `"id"`) != 2 {
		t.Fatalf("reports %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/migrate/reports/"+r.ReportID, "", e.admin); w.Code != 200 {
		t.Fatalf("report %d %s", w.Code, w.Body)
	}
	// a source this build does not have
	w = e.do("POST", "/api/migrate/run", `{"sources":["nosuch"],"dryRun":true}`, e.admin)
	if w.Code != 400 || errText(w) != "Неизвестный источник" {
		t.Fatalf("unknown: %d %s", w.Code, w.Body)
	}
}

func TestMediaImportAdminOnly(t *testing.T) {
	e := newMediaImportEnv(t)
	adult := e.login(`{"name":"Мама"}`)
	kid := e.kid("")
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/migrate", ""},
		{"GET", "/api/migrate/state", ""},
		{"POST", "/api/migrate/run", `{"sources":["jellyfin"],"dryRun":true}`},
		{"GET", "/api/migrate/reports", ""},
		{"GET", "/api/migrate/reports/20261025T193000Z-abcdef", ""},
	} {
		for who, ck := range map[string]*http.Cookie{"adult": adult, "kid": kid} {
			if w := e.do(c.method, c.path, c.body, ck); w.Code != http.StatusForbidden {
				t.Errorf("%s %s %s: %d", who, c.method, c.path, w.Code)
			}
		}
		if w := e.do(c.method, c.path, c.body, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s: %d", c.method, c.path, w.Code)
		}
	}
	if e.svc.Running() != "" {
		t.Fatal("a refused request started a run")
	}
}
