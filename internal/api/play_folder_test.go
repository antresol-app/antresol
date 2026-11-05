package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

// A comic volume that is a folder of images has no file to stream or
// download: the reader opens it page by page.
func TestStreamFolderRow404(t *testing.T) {
	e := newLibEnv(t)
	dir := filepath.Join(e.media, "Берсерк", "Том 1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "001.jpg"), []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	vol, err := e.repo.UpsertComicVolume(context.Background(), library.ComicInput{
		File: library.FileInput{Path: dir, Size: 4, Mtime: 1}, SeriesDir: filepath.Dir(dir), Series: "Берсерк", Volume: 1, Pages: 1, Direction: "rtl"})
	if err != nil {
		t.Fatal(err)
	}
	fs, err := e.repo.FilesOf(context.Background(), vol)
	if err != nil || len(fs) != 1 {
		t.Fatalf("%+v %v", fs, err)
	}
	for _, u := range []string{"/api/stream/", "/api/download/"} {
		w := e.do("GET", u+itoa(fs[0].ID), "", e.admin)
		if w.Code != 404 || !strings.Contains(w.Body.String(), "Это папка, откройте том в читалке") {
			t.Fatalf("%s: %d %s", u, w.Code, w.Body)
		}
	}
}

// A kid gets 404 on a volume of an 18+ series, by item and by file, even
// when the volume row carries a lower rating of its own.
func TestKidComicVolume404(t *testing.T) {
	e := newLibEnv(t)
	p := filepath.Join(e.media, "v01.cbz")
	if err := os.WriteFile(p, []byte("PK\x05\x06"+strings.Repeat("\x00", 18)), 0o644); err != nil {
		t.Fatal(err)
	}
	vol, err := e.repo.UpsertComicVolume(context.Background(), library.ComicInput{
		File: library.FileInput{Path: p, Size: 22, Mtime: 1}, SeriesDir: e.media, Series: "Взрослое", Volume: 1, Rating: "18+"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE items SET rating='6+' WHERE id=?`, vol); err != nil {
		t.Fatal(err)
	}
	fs, err := e.repo.FilesOf(context.Background(), vol)
	if err != nil || len(fs) != 1 {
		t.Fatalf("%+v %v", fs, err)
	}
	kid := e.kid("12+")
	for _, u := range []string{"/api/items/" + itoa(vol), "/api/download/" + itoa(fs[0].ID), "/api/stream/" + itoa(fs[0].ID)} {
		if w := e.do("GET", u, "", kid); w.Code != 404 {
			t.Fatalf("kid %s: %d", u, w.Code)
		}
		if w := e.do("GET", u, "", e.admin); w.Code != 200 {
			t.Fatalf("admin %s: %d %s", u, w.Code, w.Body)
		}
	}
}
