package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/files"
)

type diskBreakdownResp struct {
	Category string `json:"category"`
	Pending  bool   `json:"pending"`
	Bytes    int64  `json:"bytes"`
	Entries  []struct {
		Name  string `json:"name"`
		Bytes int64  `json:"bytes"`
		Dir   bool   `json:"dir"`
	} `json:"entries"`
}

// diskEnv: the file browser over a media root with one movie (3000 bytes,
// in its own folder), no acquisition: both editions have it.
type diskEnv struct {
	*libEnv
	root string
}

func newDiskEnv(t *testing.T) *diskEnv {
	root := t.TempDir()
	movies := filepath.Join(root, "media", "movies")
	p := filepath.Join(movies, "Дюна (2021)", "Dune.2021.mkv")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, 3000), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		sb := files.NewSandbox(root, []files.Root{
			{ID: "movies", Title: "Фильмы", Dir: movies},
			{ID: "series", Title: "Сериалы", Dir: filepath.Join(root, "media", "series")},
			{ID: "music", Title: "Музыка", Dir: filepath.Join(root, "media", "music")},
			{ID: "downloads", Title: "Загрузки", Dir: filepath.Join(root, "torrents")},
		})
		d.Files = files.NewService(files.ServiceConfig{Sandbox: sb, DB: db, Library: d.Library, DataDir: t.TempDir()})
	})
	return &diskEnv{libEnv: e, root: root}
}

func TestStatsDiskBreakdown(t *testing.T) {
	e := newDiskEnv(t)
	p := filepath.Join(e.root, "torrents", "Seed", "s.mkv")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, 700), 0o644); err != nil {
		t.Fatal(err)
	}

	w := e.do("GET", "/api/stats/disk/movies", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), e.root) {
		t.Errorf("absolute path leaked: %s", w.Body)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	if keys["category"] == nil || keys["pending"] == nil || keys["bytes"] == nil || keys["entries"] == nil || keys["more"] != nil || keys["rest"] != nil {
		t.Errorf("keys %s", w.Body)
	}
	var b diskBreakdownResp
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if b.Category != "movies" || b.Pending || b.Bytes != 3000 || len(b.Entries) != 1 ||
		b.Entries[0].Name != "Дюна (2021)" || b.Entries[0].Bytes != 3000 || !b.Entries[0].Dir {
		t.Fatalf("movies %s", w.Body)
	}

	w = e.do("GET", "/api/stats/disk/downloads", "", e.admin)
	b = diskBreakdownResp{}
	json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != 200 || b.Bytes != 700 || len(b.Entries) != 1 || b.Entries[0].Name != "Seed" {
		t.Fatalf("downloads %d %s", w.Code, w.Body)
	}

	w = e.do("GET", "/api/stats/disk/other", "", e.admin)
	b = diskBreakdownResp{}
	json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != 200 || b.Category != "other" {
		t.Fatalf("other %d %s", w.Code, w.Body)
	}

	// unknown, and a category without a root here
	for _, c := range []string{"nope", "stream-cache", "library"} {
		if w := e.do("GET", "/api/stats/disk/"+c, "", e.admin); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s", c, w.Code, w.Body)
		}
	}
	user := e.login(`{"name":"Мама"}`)
	if w := e.do("GET", "/api/stats/disk/movies", "", user); w.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d", w.Code)
	}
}

// Without the file browser there is nothing to break down.
func TestStatsDiskBreakdownNoFiles(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db })
	if w := e.do("GET", "/api/stats/disk/movies", "", e.admin); w.Code != http.StatusNotFound {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
