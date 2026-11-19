package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/bgtask"
	"github.com/antresol-app/antresol/internal/library"
)

func getAdmin(t *testing.T, deps Deps, path string, admin bool) (int, []byte) {
	t.Helper()
	r := chi.NewRouter()
	mountTasks(r, deps)
	req := httptest.NewRequest("GET", path, nil)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: 1, Name: "a", IsAdmin: admin}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

// GET /api/admin/tasks: the admin's; the scan and every worker's progress
// in one answer, workers [] when there are none.
func TestTasks(t *testing.T) {
	var tr bgtask.Tracker
	tr.Begin(3, 10)
	tr.Add(2)
	deps := Deps{Jobs: &library.Jobs{}, Background: []BackgroundTask{{ID: "trickplay", Progress: tr.Snapshot}}}
	if code, _ := getAdmin(t, deps, "/api/admin/tasks", false); code != http.StatusForbidden {
		t.Fatalf("not an admin: %d", code)
	}
	code, body := getAdmin(t, deps, "/api/admin/tasks", true)
	var out struct {
		Scan    *library.JobStatus `json:"scan"`
		Voclean *json.RawMessage   `json:"voclean"`
		Workers []struct {
			ID      string `json:"id"`
			Running bool   `json:"running"`
			Done    int    `json:"done"`
			Total   int    `json:"total"`
		} `json:"workers"`
	}
	if code != 200 || json.Unmarshal(body, &out) != nil {
		t.Fatalf("%d %s", code, body)
	}
	if out.Scan == nil || out.Voclean != nil || len(out.Workers) != 1 || out.Workers[0].ID != "trickplay" || !out.Workers[0].Running || out.Workers[0].Done != 5 || out.Workers[0].Total != 10 {
		t.Fatalf("%s", body)
	}
	_, body = getAdmin(t, Deps{}, "/api/admin/tasks", true)
	if string(body) != "{\"workers\":[]}\n" {
		t.Fatalf("none: %s", body)
	}
}

// GET /api/admin/health/network: any answer is reached (a 404 too), a
// refused connection is not.
func TestHealthNetwork(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer up.Close()
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	downURL := down.URL
	down.Close()
	deps := Deps{NetTargets: []NetTarget{{ID: "tmdb", Name: "TMDB", URL: up.URL}, {ID: "github", Name: "GitHub", URL: downURL}},
		NetClient: &http.Client{Timeout: 2 * time.Second}}
	code, body := getAdmin(t, deps, "/api/admin/health/network", true)
	var out []netResult
	if code != 200 || json.Unmarshal(body, &out) != nil || len(out) != 2 {
		t.Fatalf("%d %s", code, body)
	}
	if !out[0].OK || out[0].ID != "tmdb" || out[1].OK || out[1].Error == "" {
		t.Fatalf("%s", body)
	}
}
