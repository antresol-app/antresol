package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/trickplay"
)

// fakeTrickplay is a real trickplay.Worker store over a temp dir, with
// the plays it was asked for.
type fakeTrickplay struct {
	*trickplay.Worker
	wanted []int64
}

func (f *fakeTrickplay) Want(id int64) { f.wanted = append(f.wanted, id) }

// makePreviews stores one sheet of previews for file id.
func makePreviews(t *testing.T, e *libEnv, dir string, id int64) trickplay.Index {
	t.Helper()
	var path string
	var size, mtime int64
	if err := e.db.QueryRow(`SELECT path, size, mtime FROM files WHERE id=?`, id).Scan(&path, &size, &mtime); err != nil {
		t.Fatal(err)
	}
	key := trickplay.Key(path, size, mtime)
	ix, err := trickplay.Generate(context.Background(), filepath.Join(dir, key), key, 400, 8, 8,
		func(context.Context, float64) ([]byte, error) { return make([]byte, 8*8*3), nil })
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestTrickplayRoutes(t *testing.T) {
	dir := t.TempDir()
	tp := &fakeTrickplay{Worker: &trickplay.Worker{Dir: dir}}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Trickplay = tp })
	_, f := e.movie("Брат", "16+", "0123456789")
	_, adult := e.movie("Взрослый", "18+", "abc")

	// not made yet: 404, never cached
	w := e.do("GET", "/api/files/"+itoa(f)+"/trickplay", "", e.admin)
	if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("before: %d %q %s", w.Code, w.Header().Get("Cache-Control"), w.Body)
	}
	// asking for them queues the file (a film's part, another version)
	if len(tp.wanted) != 1 || tp.wanted[0] != f {
		t.Fatalf("wanted %v", tp.wanted)
	}
	ix := makePreviews(t, e, dir, f)
	makePreviews(t, e, dir, adult)

	w = e.do("GET", "/api/files/"+itoa(f)+"/trickplay", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("index: %d %s", w.Code, w.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{"interval": 10.0, "width": 8.0, "height": 8.0, "columns": 10.0, "rows": 10.0,
		"count": 40.0, "sheets": 1.0, "key": ix.Key, "version": float64(trickplay.Version)} {
		if got[k] != v {
			t.Fatalf("index %s = %v, want %v (%s)", k, got[k], v, w.Body)
		}
	}

	w = e.do("GET", "/api/files/"+itoa(f)+"/trickplay/0.jpg?v="+ix.Key, "", e.admin)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("sheet: %d %v", w.Code, w.Header())
	}
	if b := w.Body.Bytes(); len(b) < 3 || b[0] != 0xff || b[1] != 0xd8 {
		t.Fatal("not a JPEG")
	}
	// without the key the URL may outlive the file: never cached for good
	if w := e.do("GET", "/api/files/"+itoa(f)+"/trickplay/0.jpg", "", e.admin); w.Code != 200 || strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("no v: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	for _, p := range []string{"/trickplay/1.jpg", "/trickplay/0.jpg?v=old", "/trickplay/x.jpg", "/trickplay/0.png"} {
		if w := e.do("GET", "/api/files/"+itoa(f)+p, "", e.admin); w.Code != 404 && w.Code != 400 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}

	// a kid sees only what it may watch
	kid := e.kid("16+")
	if w := e.do("GET", "/api/files/"+itoa(f)+"/trickplay", "", kid); w.Code != 200 {
		t.Fatalf("kid, allowed: %d", w.Code)
	}
	for _, p := range []string{"/trickplay", "/trickplay/0.jpg"} {
		if w := e.do("GET", "/api/files/"+itoa(adult)+p, "", kid); w.Code != 404 {
			t.Fatalf("kid, hidden %s: %d", p, w.Code)
		}
	}
	// signed out: nothing
	if w := e.do("GET", "/api/files/"+itoa(f)+"/trickplay", "", nil); w.Code != 401 {
		t.Fatalf("signed out: %d", w.Code)
	}
}

// A play puts a file without previews first in their queue.
func TestPlayWantsPreviews(t *testing.T) {
	dir := t.TempDir()
	tp := &fakeTrickplay{Worker: &trickplay.Worker{Dir: dir}}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Trickplay = tp })
	_, f := e.movie("Брат", "16+", "0123456789")
	e.probeJSON(f, directProbe)
	if w := e.do("POST", "/api/play/"+itoa(f), directCaps, e.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(tp.wanted) != 1 || tp.wanted[0] != f {
		t.Fatalf("wanted %v", tp.wanted)
	}
	makePreviews(t, e, dir, f)
	e.do("POST", "/api/play/"+itoa(f), directCaps, e.admin)
	if len(tp.wanted) != 1 {
		t.Fatalf("made, still wanted: %v", tp.wanted)
	}
}

// Without a worker the routes answer 404.
func TestTrickplayNone(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Брат", "16+", "0123456789")
	w := e.do("GET", "/api/files/"+itoa(f)+"/trickplay", "", e.admin)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "Превью") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// «Превью при перемотке»: on by default, a PUT turns it off and back on.
func TestSettingsTrickplay(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db })
	get := func() bool {
		w := e.do("GET", "/api/settings", "", e.admin)
		var v struct {
			Trickplay *bool `json:"trickplay"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Trickplay == nil {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		return *v.Trickplay
	}
	if !get() {
		t.Fatal("default off")
	}
	// the screen sends back what it got, one field changed
	var all map[string]any
	json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &all)
	all["trickplay"] = false
	if _, ok := all["streamCacheGB"]; ok {
		all["streamCacheGB"] = 20 // the sources build's: none in the test env
	}
	body, _ := json.Marshal(all)
	if w := e.do("PUT", "/api/settings", string(body), e.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if get() {
		t.Fatal("still on")
	}
	s, err := LoadAppSettings(context.Background(), e.db, AppSettings{})
	if err != nil || s.Trickplay {
		t.Fatalf("stored %v %v", s.Trickplay, err)
	}
	// a client that does not know the field keeps it
	delete(all, "trickplay")
	body, _ = json.Marshal(all)
	if w := e.do("PUT", "/api/settings", string(body), e.admin); w.Code != 200 || get() {
		t.Fatalf("%d %s", w.Code, w.Body)
	}

	// «Место для превью»: 10 GB by default, 1…1000 GB
	if s.TrickplayBudgetBytes() != 10<<30 || all["trickplayBudgetGB"] != 10.0 {
		t.Fatalf("default budget %d / %v", s.TrickplayBudgetBytes(), all["trickplayBudgetGB"])
	}
	all["trickplayBudgetGB"] = 0.5
	body, _ = json.Marshal(all)
	if w := e.do("PUT", "/api/settings", string(body), e.admin); w.Code != 400 {
		t.Fatalf("half a GB: %d", w.Code)
	}
	all["trickplayBudgetGB"] = 5000
	body, _ = json.Marshal(all)
	if w := e.do("PUT", "/api/settings", string(body), e.admin); w.Code != 400 {
		t.Fatalf("5000 GB: %d", w.Code)
	}
	all["trickplayBudgetGB"] = 20
	body, _ = json.Marshal(all)
	if w := e.do("PUT", "/api/settings", string(body), e.admin); w.Code != 200 {
		t.Fatalf("20 GB: %d %s", w.Code, w.Body)
	}
	if s, _ := LoadAppSettings(context.Background(), e.db, AppSettings{}); s.TrickplayBudgetBytes() != 20<<30 {
		t.Fatalf("stored budget %d", s.TrickplayBudgetBytes())
	}
}
