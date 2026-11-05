package config

import (
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{"VAULT_LISTEN", "VAULT_MEDIA_ROOT", "VAULT_DATA_DIR", "VAULT_TMDB_KEY", "VAULT_HWACCEL", "VAULT_TORRENT_PORT", "VAULT_TMDB_BASEURL", "VAULT_TMDB_IMAGEBASE"} {
		t.Setenv(k, "")
	}
	t.Setenv("VAULT_MEDIA_ROOT", "/srv/media")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8420" {
		t.Fatalf("listen %q", c.Listen)
	}
	if got := c.Abs(c.MoviesDir); got != "/srv/media/media/movies" {
		t.Fatalf("movies %q", got)
	}
	if c.Abs(c.MusicDir) != "/srv/media/media/music" || c.Abs(c.BooksDir) != "/srv/media/media/books" {
		t.Fatalf("music/books %q %q", c.MusicDir, c.BooksDir)
	}
	if c.TMDBBaseURL != "https://api.themoviedb.org/3" || c.TMDBImageBase != "https://image.tmdb.org/t/p" {
		t.Fatalf("tmdb defaults %q %q", c.TMDBBaseURL, c.TMDBImageBase)
	}
	if c.HWAccel != "auto" {
		t.Fatalf("bad defaults %+v", c)
	}
}

func TestLoadEnvOverride(t *testing.T) {
	t.Setenv("VAULT_MEDIA_ROOT", "/x")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Abs(c.SeriesDir); got != "/x/media/series" {
		t.Fatalf("series %q", got)
	}
}

func TestLoadTMDBOverride(t *testing.T) {
	t.Setenv("VAULT_TMDB_BASEURL", "http://x/3")
	t.Setenv("VAULT_TMDB_IMAGEBASE", "http://x/img")
	c, err := Load()
	if err != nil || c.TMDBBaseURL != "http://x/3" || c.TMDBImageBase != "http://x/img" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestLoadBookDirsDefaults(t *testing.T) {
	for _, k := range []string{"VAULT_MEDIA_ROOT", "VAULT_EBOOKS_DIR", "VAULT_AUDIOBOOKS_DIR", "VAULT_COMICS_DIR"} {
		t.Setenv(k, "")
	}
	t.Setenv("VAULT_MEDIA_ROOT", "/srv/media")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.EbooksDir != "media/books/ebooks" || c.Abs(c.EbooksDir) != "/srv/media/media/books/ebooks" {
		t.Fatalf("ebooks %q", c.EbooksDir)
	}
	if c.Abs(c.AudiobooksDir) != "/srv/media/media/books/audiobooks" {
		t.Fatalf("audiobooks %q", c.AudiobooksDir)
	}
	if c.Abs(c.ComicsDir) != "/srv/media/media/books/manga" {
		t.Fatalf("comics %q", c.ComicsDir)
	}
}

func TestLoadBookDirsEnv(t *testing.T) {
	t.Setenv("VAULT_COMICS_DIR", "/x/comics")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Abs(c.ComicsDir); got != "/x/comics" {
		t.Fatalf("comics %q", got)
	}
}

// F29: the book sub-dirs follow VAULT_BOOKS_DIR unless set themselves.
func TestLoadBookDirsFollowBooksDir(t *testing.T) {
	for _, k := range []string{"VAULT_EBOOKS_DIR", "VAULT_AUDIOBOOKS_DIR"} {
		t.Setenv(k, "")
	}
	t.Setenv("VAULT_MEDIA_ROOT", "/data")
	t.Setenv("VAULT_BOOKS_DIR", "/media/books")
	t.Setenv("VAULT_COMICS_DIR", "/x/comics")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Abs(c.EbooksDir); got != "/media/books/ebooks" {
		t.Fatalf("ebooks %q", got)
	}
	if got := c.Abs(c.AudiobooksDir); got != "/media/books/audiobooks" {
		t.Fatalf("audiobooks %q", got)
	}
	if got := c.Abs(c.ComicsDir); got != "/x/comics" {
		t.Fatalf("comics %q (its own variable wins)", got)
	}
}

// VAULT_PUBLIC_URL is read by both editions (links in notification letters).
func TestPublicURLBothEditions(t *testing.T) {
	t.Setenv("VAULT_PUBLIC_URL", " https://antresol.example/ ")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicURL != "https://antresol.example" {
		t.Fatalf("PublicURL %q", c.PublicURL)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestDefaultMediaRoot(t *testing.T) {
	type st struct{ dir, nonEmpty bool }
	for _, tc := range []struct {
		name string
		fs   map[string]st
		want string
	}{
		{"media with files", map[string]st{"/media": {true, true}}, "/media"},
		{"empty media mount", map[string]st{"/media": {true, false}}, "/media"},
		{"nothing", map[string]st{}, mustAbs(t, "media")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultMediaRoot(func(p string) (bool, bool) { s := tc.fs[p]; return s.dir, s.nonEmpty }); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadMediaRootEnvWins(t *testing.T) {
	t.Setenv("VAULT_MEDIA_ROOT", "/x")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.MediaRoot != "/x" {
		t.Fatalf("media root %q", c.MediaRoot)
	}
}
