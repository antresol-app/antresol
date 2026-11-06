package api

import (
	"bytes"
	"database/sql"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// The web app asks for a card's poster at the width it shows (?w=): a
// copy no wider than that, made once; an unknown width or none is the
// original, so no URL can make the server keep copies of every size.
func TestImageShrunkToRequestedWidth(t *testing.T) {
	var dir string
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { dir = filepath.Join(d.DataDir, "images") })
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1000, 1500)), nil); err != nil {
		t.Fatal(err)
	}
	name := "0123456789abcdef0123456789abcdef01234567.jpg"
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	width := func(q string) int {
		t.Helper()
		w := e.do("GET", "/api/images/"+name+q, "", e.admin)
		if w.Code != 200 {
			t.Fatalf("%s: %d", q, w.Code)
		}
		cfg, err := jpeg.DecodeConfig(w.Body)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Width
	}
	if got := width("?w=320"); got != 320 {
		t.Fatalf("w=320: %d", got)
	}
	if got := width("?w=320"); got != 320 { // the kept copy
		t.Fatalf("again: %d", got)
	}
	for _, q := range []string{"", "?w=321", "?w=abc", "?w=1920"} { // 1920: never enlarged
		if got := width(q); got != 1000 {
			t.Fatalf("%q: %d", q, got)
		}
	}
	copies, _ := filepath.Glob(filepath.Join(dir, "resized", "*.jpg"))
	if len(copies) != 1 {
		t.Fatalf("copies %v", copies)
	}
	// a TMDB cache file: 30 days, then revalidated
	for _, q := range []string{"", "?w=320"} {
		if cc := e.do("GET", "/api/images/"+name+q, "", e.admin).Header().Get("Cache-Control"); cc != "private, max-age=2592000" {
			t.Fatalf("%q: Cache-Control %q", q, cc)
		}
	}
}
