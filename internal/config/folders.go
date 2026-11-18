package config

import (
	"path/filepath"
	"strings"
)

// Folders are the library's folders (relative to MediaRoot, or absolute):
// the environment's VAULT_*_DIR, or what the administrator picked in the
// first-run wizard or «Настройки сервера», which the server reads at its
// start (WithFolders).
type Folders struct {
	Movies     string `json:"movies"`
	Series     string `json:"series"`
	Music      string `json:"music"`
	Ebooks     string `json:"ebooks"`
	Audiobooks string `json:"audiobooks"`
	Comics     string `json:"comics"`
}

// Folders are the library folders this configuration has.
func (c Config) Folders() Folders {
	return Folders{Movies: c.MoviesDir, Series: c.SeriesDir, Music: c.MusicDir,
		Ebooks: c.EbooksDir, Audiobooks: c.AudiobooksDir, Comics: c.ComicsDir}
}

// WithFolders is the configuration with the folders f sets (an empty field
// keeps the current one). When the three book folders share a parent that
// holds nothing else of the library, that parent becomes the books folder
// (the file browser's «Книги», open to every adult profile), and the
// default import path map follows the book folders.
func (c Config) WithFolders(f Folders) Config {
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&c.MoviesDir, f.Movies)
	set(&c.SeriesDir, f.Series)
	set(&c.MusicDir, f.Music)
	set(&c.EbooksDir, f.Ebooks)
	set(&c.AudiobooksDir, f.Audiobooks)
	set(&c.ComicsDir, f.Comics)
	if f.Ebooks != "" || f.Audiobooks != "" || f.Comics != "" {
		if parent, ok := c.sharedBooksParent(); ok {
			c.BooksDir = parent
		}
	}
	if !c.importPathsSet {
		c.ImportPaths = c.defaultImportPaths()
	}
	return c
}

// sharedBooksParent is the folder the three book folders are directly in,
// when making it the books folder shows nothing but books: it is inside the
// media folder (never the media folder itself, whose written form may carry
// a trailing slash), and none of the other library folders is it or lies in
// it. Otherwise the books folder stays as it was, and a layout like
// library/{movies,series,ebooks,...} does not hand the films to everyone
// through «Книги».
func (c Config) sharedBooksParent() (string, bool) {
	root := filepath.Clean(c.MediaRoot)
	e, a, m := filepath.Clean(c.Abs(c.EbooksDir)), filepath.Clean(c.Abs(c.AudiobooksDir)), filepath.Clean(c.Abs(c.ComicsDir))
	parent := filepath.Dir(e)
	if filepath.Dir(a) != parent || filepath.Dir(m) != parent || parent == root || !within(root, parent) {
		return "", false
	}
	for _, other := range []string{c.MoviesDir, c.SeriesDir, c.MusicDir} {
		if within(parent, filepath.Clean(c.Abs(other))) {
			return "", false
		}
	}
	return parent, true
}

// within: p is dir or inside it (both clean).
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
