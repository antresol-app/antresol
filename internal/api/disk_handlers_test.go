package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/disk"
)

func TestDiskRoute(t *testing.T) {
	usage := disk.Usage{Used: 900, Total: 1000, Level: disk.LevelWarn}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.DiskUsage = func() (disk.Usage, error) { return usage, nil }
	})
	w := e.do("GET", "/api/disk", "", e.admin)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"used":900,"total":1000,"level":"warn"}` {
		t.Fatalf("admin: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/disk", "", e.login(`{"name":"Мама"}`)); w.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d", w.Code)
	}
	if w := e.do("GET", "/api/disk", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", w.Code)
	}

	usage = disk.Usage{Level: disk.LevelOK, Offline: "Медиадиск недоступен: нет папки /x"}
	w = e.do("GET", "/api/disk", "", e.admin, "Accept-Language", "en")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"offline":"The media disk is unavailable: no folder /x"`) {
		t.Fatalf("en: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/disk", "", e.admin); !strings.Contains(w.Body.String(), `"offline":"Медиадиск недоступен: нет папки /x"`) {
		t.Fatalf("ru: %s", w.Body)
	}
}

func TestDiskRouteUnreadable(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.DiskUsage = func() (disk.Usage, error) { return disk.Usage{}, errors.New("statfs: boom") }
	})
	w := e.do("GET", "/api/disk", "", e.admin, "Accept-Language", "en")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "Couldn’t find out how much disk space there is") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// Without a disk monitor there is no /api/disk.
func TestDiskRouteDisabled(t *testing.T) {
	e := newLibEnvWith(t, nil)
	if w := e.do("GET", "/api/disk", "", e.admin); w.Code != http.StatusNotFound {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// The overview's disk figures come from DiskUsage when there is no file browser.
func TestStatsDiskFromDiskUsage(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		d.DiskUsage = func() (disk.Usage, error) { return disk.Usage{Used: 300, Total: 1000, Level: disk.LevelOK}, nil }
	})
	w := e.do("GET", "/api/stats", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"disk":{"total":1000,"used":300,"free":700,`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
