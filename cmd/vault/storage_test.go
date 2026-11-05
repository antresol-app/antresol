package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/antresol-app/antresol/internal/config"
	"github.com/antresol-app/antresol/internal/i18n"
)

// The storage check covers the media root, movies and series only.
func TestStorageDirsChecked(t *testing.T) {
	cfg := config.Config{MediaRoot: "/srv/media", MoviesDir: "media/movies", SeriesDir: "media/series",
		MusicDir: "media/music", EbooksDir: "media/books/ebooks", AudiobooksDir: "media/books/audiobooks", ComicsDir: "media/books/manga"}
	want := []string{"/srv/media", "/srv/media/media/movies", "/srv/media/media/series"}
	if got := storageDirs(cfg); !slices.Equal(got, want) {
		t.Fatalf("storageDirs = %q, want %q", got, want)
	}
}

// A missing (trashed, never created) music or book folder does not take the
// storage offline.
func TestMissingMusicFolderKeepsStorageOnline(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"media/movies", "media/series"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{MediaRoot: root, MoviesDir: "media/movies", SeriesDir: "media/series",
		MusicDir: "media/music", EbooksDir: "media/books/ebooks", AudiobooksDir: "media/books/audiobooks", ComicsDir: "media/books/manga"}
	if why := storageOffline(cfg)(); why != "" {
		t.Fatalf("music/books missing took storage offline: %v", why)
	}
	if err := os.RemoveAll(filepath.Join(root, "media/series")); err != nil {
		t.Fatal(err)
	}
	if storageOffline(cfg)() == "" {
		t.Fatal("missing series folder: storage still online")
	}
}

// A missing folder names itself in a message that is a catalog key.
func TestStorageOfflineMessage(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "media/series"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MediaRoot: root, MoviesDir: "media/movies", SeriesDir: "media/series"}
	dir := filepath.Join(root, "media/movies")
	want := "Медиадиск недоступен: нет папки " + dir
	if got := storageOffline(cfg)(); got != want {
		t.Fatalf("message %q, want %q", got, want)
	}
	if got := i18n.LocalizeL(i18n.EN, want); got != "The media disk is unavailable: no folder "+dir {
		t.Fatalf("en %q", got)
	}
}

// Background writers into DataDir start only with 20 GB free and below
// 95 % used.
func TestRoomFor(t *testing.T) {
	const gb = 1 << 30
	for _, c := range []struct {
		total, avail int64
		want         bool
	}{{100 * gb, 21 * gb, true}, {100 * gb, 19 * gb, false}, {1000 * gb, 60 * gb, true}, {500 * gb, 22 * gb, false}, {0, 0, false}} {
		if got := roomFor(c.total, c.avail); got != c.want {
			t.Errorf("roomFor(%d GB, %d GB free) = %v", c.total/gb, c.avail/gb, got)
		}
	}
}

// A DataDir whose filesystem cannot be read counts as full: background
// writers (cleaned voice-overs) wait.
func TestSpaceForUnreadable(t *testing.T) {
	if spaceFor(filepath.Join(t.TempDir(), "gone")) {
		t.Fatal("a missing folder has space")
	}
}
