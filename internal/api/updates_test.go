package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/version"
	"github.com/go-chi/chi/v5"
)

func getUpdates(t *testing.T, admin bool, latest func(context.Context) (string, string, error)) (coreUpdate, int) {
	t.Helper()
	r := chi.NewRouter()
	mountUpdates(r, latest)
	req := httptest.NewRequest("GET", "/api/updates", nil)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: 1, Name: "a", IsAdmin: admin}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out coreUpdate
	if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("%s", w.Body)
	}
	return out, w.Code
}

// GET /api/updates: the admin's; the running version against the latest
// release, «newer» only for a real version below it; a failed check says so.
func TestUpdates(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old })
	version.Version = "0.8.0"
	rel := func(context.Context) (string, string, error) { return "v0.9.0", "https://github.com/o/r/releases/tag/v0.9.0", nil }
	if _, code := getUpdates(t, false, rel); code != 403 {
		t.Fatalf("not an admin: %d", code)
	}
	out, _ := getUpdates(t, true, rel)
	if !out.Checked || !out.Newer || out.Latest != "v0.9.0" || out.Version != "0.8.0" || out.ReleaseURL == "" {
		t.Fatalf("%+v", out)
	}
	version.Version = "dev"
	if out, _ := getUpdates(t, true, rel); out.Newer || !out.Checked {
		t.Fatalf("dev: %+v", out)
	}
	fail := func(context.Context) (string, string, error) { return "", "", errors.New("offline") }
	if out, _ := getUpdates(t, true, fail); out.Checked || out.Latest != "" {
		t.Fatalf("failed check: %+v", out)
	}
	if out, _ := getUpdates(t, true, nil); out.Checked {
		t.Fatalf("no check: %+v", out)
	}
}
