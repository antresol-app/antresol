package files

import (
	"os"
	"path/filepath"
	"testing"
)

// testTree creates a media root with the standard roots and returns a sandbox over it.
func testSandbox(t *testing.T) (*Sandbox, string) {
	t.Helper()
	mr := t.TempDir()
	for _, d := range []string{"media/movies", "media/series", "torrents", "tmp/vault-stream"} {
		if err := os.MkdirAll(filepath.Join(mr, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return NewSandbox(mr, []Root{
		{ID: "library", Title: "Медиатека", Dir: filepath.Join(mr, "media")},
		{ID: "movies", Title: "Фильмы", Dir: filepath.Join(mr, "media/movies")},
		{ID: "series", Title: "Сериалы", Dir: filepath.Join(mr, "media/series")},
		{ID: "downloads", Title: "Загрузки", Dir: filepath.Join(mr, "torrents")},
		{ID: "stream-cache", Title: "Кеш просмотра", Dir: filepath.Join(mr, "tmp/vault-stream"), NoDrop: true},
		{ID: "disk", Title: "Весь диск", Dir: mr, AdminOnly: true},
	}), mr
}

func write(t *testing.T, p string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, size)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// fakeSeeds reports every path under (or equal to) one of its prefixes as seeding.
type fakeSeeds map[string]string // abs path -> infohash

func (f fakeSeeds) SeedInfo(p string) (string, bool, bool) {
	for k, ih := range f {
		if k == p || len(k) > len(p) && k[:len(p)+1] == p+"/" {
			return ih, true, true
		}
	}
	return "", false, false
}
