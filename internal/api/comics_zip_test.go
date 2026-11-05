package api

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

func TestComicPagesZip(t *testing.T) {
	dir := t.TempDir()
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.DeviceCopies = &media.DeviceCopies{Dir: dir, FreeBytes: func(string) (int64, error) { return 1 << 50, nil }}
		t.Cleanup(d.DeviceCopies.Close)
	})
	_, vols := e.comic("Берсерк", "", 1)
	fl, _ := e.repo.FileByID(context.Background(), vols[0].file)
	pages := map[string][]byte{}
	var want [][]byte
	for i := 1; i <= 5; i++ {
		var b bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, i, i))
		if i == 5 {
			png.Encode(&b, img)
		} else {
			jpeg.Encode(&b, img, nil)
		}
		// natural order: 2 before 10
		pages[fmt.Sprintf("%d.%s", []int{0, 1, 2, 10, 11, 20}[i], map[bool]string{true: "png", false: "jpg"}[i == 5])] = b.Bytes()
		want = append(want, b.Bytes())
	}
	writeCBZ(t, fl.Path, pages)

	url := "/api/comics/" + itoa(vols[0].file) + "/pages.zip"
	w := e.do("GET", url, "", e.admin)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("%d %v %s", w.Code, w.Header(), w.Body)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for i, zf := range zr.File {
		names = append(names, zf.Name)
		if zf.Method != zip.Store {
			t.Errorf("%s compressed", zf.Name)
		}
		rc, _ := zf.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		if !bytes.Equal(b, want[i]) {
			t.Errorf("%s: wrong page", zf.Name)
		}
	}
	if !slices.Equal(names, []string{"page-0001.jpg", "page-0002.jpg", "page-0003.jpg", "page-0004.jpg", "page-0005.png"}) {
		t.Fatal(names)
	}
	w = e.do("GET", url, "", e.admin, "Range", "bytes=0-9")
	if w.Code != 206 || w.Body.Len() != 10 {
		t.Fatalf("%d %d", w.Code, w.Body.Len())
	}
	// built once
	if es, _ := os.ReadDir(filepath.Join(dir, "comics")); len(es) != 1 {
		t.Fatalf("%d zips", len(es))
	}

	_, hidden := e.comic("Взрослое", "18+", 1)
	kid := e.kid("12")
	if w := e.do("GET", "/api/comics/"+itoa(hidden[0].file)+"/pages.zip", "", kid); w.Code != 404 {
		t.Fatalf("kid: %d %s", w.Code, w.Body)
	}
}

// A fetch records the zip's access without moving its Last-Modified: a
// download resumed with If-Range after someone else fetched it gets 206.
func TestComicPagesZipStableValidator(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.DeviceCopies = &media.DeviceCopies{Dir: dir, FreeBytes: func(string) (int64, error) { return 1 << 50, nil },
			Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }}
		t.Cleanup(d.DeviceCopies.Close)
	})
	_, vols := e.comic("Берсерк", "", 1)
	url := "/api/comics/" + itoa(vols[0].file) + "/pages.zip"
	w := e.do("GET", url, "", e.admin)
	lm := w.Header().Get("Last-Modified")
	if w.Code != 200 || lm == "" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	mu.Lock()
	now = now.Add(2 * time.Hour)
	mu.Unlock()
	w = e.do("GET", url, "", e.admin) // records the access
	if w.Code != 200 || w.Header().Get("Last-Modified") != lm {
		t.Fatalf("validator moved: %q → %q", lm, w.Header().Get("Last-Modified"))
	}
	zips, _ := filepath.Glob(filepath.Join(dir, "comics", "*.zip.atime"))
	if len(zips) != 1 {
		t.Fatalf("access not recorded: %v", zips)
	}
	if st, err := os.Stat(zips[0]); err != nil || !st.ModTime().Equal(now) {
		t.Fatalf("access time %v %v", st.ModTime(), err)
	}
	w = e.do("GET", url, "", e.admin, "Range", "bytes=0-9", "If-Range", lm)
	if w.Code != 206 || w.Body.Len() != 10 {
		t.Fatalf("resume: %d %d", w.Code, w.Body.Len())
	}
	w = e.do("GET", url, "", e.admin, "If-Unmodified-Since", lm)
	if w.Code != 200 {
		t.Fatalf("If-Unmodified-Since: %d", w.Code)
	}
}
