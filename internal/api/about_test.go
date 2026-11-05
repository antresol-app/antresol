package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/version"
)

func getAbout(t *testing.T) (map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	// no session, no database: the offer is public
	NewRouter(Deps{}).ServeHTTP(w, httptest.NewRequest("GET", "/api/about", nil))
	var m map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &m) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return m, w
}

// GET /api/about answers anyone (AGPL §13: every user of the server can
// find its source) with the version, the commit, the licence and the link.
func TestAboutPublic(t *testing.T) {
	oldV, oldC, oldS := version.Version, version.Commit, version.SourceURL
	t.Cleanup(func() { version.Version, version.Commit, version.SourceURL = oldV, oldC, oldS })
	version.Version, version.Commit, version.SourceURL = "1.2.3", "0123abcd", "https://example.org/antresol/tree/v1.2.3"
	m, w := getAbout(t)
	want := map[string]any{"version": "1.2.3", "commit": "0123abcd", "license": "AGPL-3.0-or-later",
		"sourceUrl": "https://example.org/antresol/tree/v1.2.3"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v (%s)", k, m[k], v, w.Body)
		}
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("type %q", w.Header().Get("Content-Type"))
	}
	t.Setenv("VAULT_SOURCE_URL", "https://example.org/fork")
	if m, _ := getAbout(t); m["sourceUrl"] != "https://example.org/fork" {
		t.Errorf("override: %v", m["sourceUrl"])
	}
}

// A build without a source link (the private deploy, a local build) leaves
// the key out, and the web shows no link.
func TestAboutEmptySourceURLOmitted(t *testing.T) {
	old := version.SourceURL
	t.Cleanup(func() { version.SourceURL = old })
	version.SourceURL = ""
	m, w := getAbout(t)
	if _, ok := m["sourceUrl"]; ok {
		t.Fatalf("sourceUrl present: %s", w.Body)
	}
	if m["license"] != "AGPL-3.0-or-later" || m["version"] == nil || m["commit"] == nil {
		t.Fatalf("%s", w.Body)
	}
	version.SourceURL = "https://example.org/antresol"
	t.Setenv("VAULT_SOURCE_URL", "")
	if m, _ := getAbout(t); m["sourceUrl"] != nil {
		t.Fatalf("cleared by the env: %v", m["sourceUrl"])
	}
}

// The licence and the third-party notices travel with the binary and are
// served as text, without a session.
func TestAboutLicenceTexts(t *testing.T) {
	for path, want := range map[string]string{
		"/api/about/license":     "GNU AFFERO GENERAL PUBLIC LICENSE",
		"/api/about/third-party": "Third-party licences",
	} {
		w := httptest.NewRecorder()
		NewRouter(Deps{}).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
			t.Errorf("%s: %d %q %.80s", path, w.Code, w.Header().Get("Content-Type"), w.Body)
		}
	}
}
