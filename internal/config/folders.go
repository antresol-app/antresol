package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Folder is one library folder: its path (relative to MediaRoot, or
// absolute) and how what is in it is shown.
type Folder struct {
	Path string `json:"path"`
	// Adult is «Только для взрослых»: everything in the folder counts as
	// rated 18+, so every profile with an age limit (kids) never sees it
	// (library.RatingOf: the age-rating filter, not a second one).
	Adult bool `json:"adult,omitempty"`
	// Unlisted is «Скрыть из общих подборок»: the folder's titles stay off
	// the home shelves and the generated channels for everyone; the library,
	// search and a direct link still have them.
	Unlisted bool `json:"unlisted,omitempty"`
}

// FolderList is one kind's folders. The first is the kind's default
// destination: downloads, uploads and imports of that kind go there.
type FolderList []Folder

// UnmarshalJSON also reads a single path, how the settings kept one folder
// per kind before a kind could have several.
func (l *FolderList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*l = nil
		if one != "" {
			*l = FolderList{{Path: one}}
		}
		return nil
	}
	var many []Folder
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

// Dirs is a list of plain folders (no flags).
func Dirs(paths ...string) FolderList {
	var out FolderList
	for _, p := range paths {
		out = append(out, Folder{Path: p})
	}
	return out
}

// First is the default destination's path ("" for an empty list).
func (l FolderList) First() string {
	if len(l) == 0 {
		return ""
	}
	return l[0].Path
}

// Paths are the folders' paths, in order.
func (l FolderList) Paths() []string {
	out := make([]string, 0, len(l))
	for _, f := range l {
		out = append(out, f.Path)
	}
	return out
}

// Folders are the library's folders, per kind: the environment's VAULT_*_DIR
// (one each), or what the administrator picked in the first-run wizard or
// «Настройки сервера», which the server reads at its start (WithFolders).
type Folders struct {
	Movies     FolderList `json:"movies"`
	Series     FolderList `json:"series"`
	Music      FolderList `json:"music"`
	Ebooks     FolderList `json:"ebooks"`
	Audiobooks FolderList `json:"audiobooks"`
	Comics     FolderList `json:"comics"`
}

// FolderKinds names the kinds of Folders, in display order (the names of
// mediacheck.Kinds).
var FolderKinds = []string{"movies", "series", "music", "ebooks", "audiobooks", "comics"}

// Of is one kind's folders.
func (f Folders) Of(kind string) FolderList {
	switch kind {
	case "movies":
		return f.Movies
	case "series":
		return f.Series
	case "music":
		return f.Music
	case "ebooks":
		return f.Ebooks
	case "audiobooks":
		return f.Audiobooks
	case "comics":
		return f.Comics
	}
	return nil
}

// Set replaces one kind's folders.
func (f *Folders) Set(kind string, l FolderList) {
	switch kind {
	case "movies":
		f.Movies = l
	case "series":
		f.Series = l
	case "music":
		f.Music = l
	case "ebooks":
		f.Ebooks = l
	case "audiobooks":
		f.Audiobooks = l
	case "comics":
		f.Comics = l
	}
}

// Equal: the same folders with the same flags, in the same order.
func (f Folders) Equal(g Folders) bool {
	for _, k := range FolderKinds {
		a, b := f.Of(k), g.Of(k)
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
	}
	return true
}

// SamePaths: the same folders in the same order, whatever their flags (a
// change of flags alone needs no restart).
func (f Folders) SamePaths(g Folders) bool {
	for _, k := range FolderKinds {
		a, b := f.Of(k), g.Of(k)
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i].Path != b[i].Path {
				return false
			}
		}
	}
	return true
}

// Folders are the library folders this configuration has.
func (c Config) Folders() Folders {
	f := c.Library
	// a kind the settings did not list is the VAULT_*_DIR one
	for _, k := range FolderKinds {
		if len(f.Of(k)) == 0 {
			if p := c.dirOf(k); p != "" {
				f.Set(k, Dirs(p))
			}
		}
	}
	return f
}

func (c Config) dirOf(kind string) string {
	switch kind {
	case "movies":
		return c.MoviesDir
	case "series":
		return c.SeriesDir
	case "music":
		return c.MusicDir
	case "ebooks":
		return c.EbooksDir
	case "audiobooks":
		return c.AudiobooksDir
	case "comics":
		return c.ComicsDir
	}
	return ""
}

// LibraryDirs are one kind's folders as absolute paths, the default
// destination first.
func (c Config) LibraryDirs(kind string) []string {
	var out []string
	for _, p := range c.Folders().Of(kind).Paths() {
		out = append(out, c.Abs(p))
	}
	return out
}

// WithFolders is the configuration with the folders f sets (a kind without
// folders keeps its current ones). Each kind's first folder becomes its
// VAULT_*_DIR (MoviesDir…), what everything with one destination uses.
// When the three book kinds' first folders share a parent that holds
// nothing else of the library, that parent becomes the books folder (the
// file browser's «Книги», open to every adult profile), and the default
// import path map follows the book folders.
func (c Config) WithFolders(f Folders) Config {
	lib := c.Folders()
	for _, k := range FolderKinds {
		if l := f.Of(k); len(l) > 0 {
			lib.Set(k, append(FolderList(nil), l...))
		}
	}
	c.Library = lib
	c.MoviesDir, c.SeriesDir, c.MusicDir = lib.Movies.First(), lib.Series.First(), lib.Music.First()
	c.EbooksDir, c.AudiobooksDir, c.ComicsDir = lib.Ebooks.First(), lib.Audiobooks.First(), lib.Comics.First()
	if len(f.Ebooks) > 0 || len(f.Audiobooks) > 0 || len(f.Comics) > 0 {
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
// a trailing slash), and none of the other library folders (any of each
// kind's) is it or lies in it. Otherwise the books folder stays as it was,
// and a layout like library/{movies,series,ebooks,...} does not hand the
// films to everyone through «Книги».
func (c Config) sharedBooksParent() (string, bool) {
	root := filepath.Clean(c.MediaRoot)
	e, a, m := filepath.Clean(c.Abs(c.EbooksDir)), filepath.Clean(c.Abs(c.AudiobooksDir)), filepath.Clean(c.Abs(c.ComicsDir))
	parent := filepath.Dir(e)
	if filepath.Dir(a) != parent || filepath.Dir(m) != parent || parent == root || !within(root, parent) {
		return "", false
	}
	for _, k := range []string{"movies", "series", "music"} {
		for _, other := range c.LibraryDirs(k) {
			if within(parent, filepath.Clean(other)) {
				return "", false
			}
		}
	}
	// the book kinds' other folders: an adults-only one under the parent
	// would reach everyone through «Книги» too
	for _, k := range []string{"ebooks", "audiobooks", "comics"} {
		for i, d := range c.LibraryDirs(k) {
			if i > 0 && within(parent, filepath.Clean(d)) {
				return "", false
			}
		}
	}
	return parent, true
}

// within: p is dir or inside it (both clean).
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
