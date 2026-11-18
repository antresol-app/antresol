// Package mediacheck looks at the server's folders the way the server sees
// them: what is under the media folder, whether each library folder is
// there, and whether the server can really write to it (it makes and
// removes a file, as the permissions say nothing about a read-only mount
// or an ACL). The first-run wizard and «Настройки сервера» show the answers,
// with the exact fix when the server may not write.
package mediacheck

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode"
)

// Kind names a library folder.
type Kind string

// The library folders, in the order they are shown.
const (
	Movies     Kind = "movies"
	Series     Kind = "series"
	Music      Kind = "music"
	Ebooks     Kind = "ebooks"
	Audiobooks Kind = "audiobooks"
	Comics     Kind = "comics"
)

// Kinds lists every kind in display order.
var Kinds = []Kind{Movies, Series, Music, Ebooks, Audiobooks, Comics}

// Status is what the server found at a path.
type Status string

const (
	StatusOK         Status = "ok"         // there, listable and writable
	StatusMissing    Status = "missing"    // no such folder
	StatusNotDir     Status = "notdir"     // a file is in its place
	StatusUnreadable Status = "unreadable" // exists, the server may not open it
	StatusReadOnly   Status = "readonly"   // opens, but the write test failed
)

// Why a path failed (Dir.Why).
const (
	WhyDenied     = "denied"     // permission denied: the owner or the mode is wrong
	WhyReadOnlyFS = "readonlyFS" // the file system is mounted read-only
	WhyOther      = "other"      // anything else (a full disk, an I/O error)
)

// TestPrefix starts the name of the file the write test makes; the file is
// hidden, so a scan never sees it even if a crash leaves one.
const TestPrefix = ".antresol-write-test-"

// CountCap bounds how many entries Check counts in a folder.
var CountCap = 9999

// createTemp makes the write test's file (tests replace it).
var createTemp = os.CreateTemp

// Dir is what Check found at one path. Path is for the caller to fill.
type Dir struct {
	Path   string `json:"path"`
	Status Status `json:"status"`
	// Items are the visible entries directly in the folder, up to CountCap;
	// More: there are more than that.
	Items int  `json:"items"`
	More  bool `json:"more,omitempty"`
	// Error is the operating system's own words; Why classifies them.
	Error string `json:"error,omitempty"`
	Why   string `json:"why,omitempty"`
}

// Classify puts a failed file operation into one of the Why kinds.
func Classify(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, syscall.EROFS):
		return WhyReadOnlyFS
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM), errors.Is(err, fs.ErrPermission):
		return WhyDenied
	}
	return WhyOther
}

// Check inspects the folder at abs: whether it is there, how many entries it
// holds, and whether the server can write into it (a real write test). It
// never creates the folder.
func Check(abs string) Dir {
	st, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Dir{Status: StatusMissing}
		}
		return Dir{Status: StatusUnreadable, Error: err.Error(), Why: Classify(err)}
	}
	if !st.IsDir() {
		return Dir{Status: StatusNotDir}
	}
	n, more, err := countEntries(abs, CountCap)
	if err != nil {
		return Dir{Status: StatusUnreadable, Error: err.Error(), Why: Classify(err)}
	}
	d := Dir{Status: StatusOK, Items: n, More: more}
	if err := WriteTest(abs); err != nil {
		d.Status, d.Error, d.Why = StatusReadOnly, err.Error(), Classify(err)
	}
	return d
}

// CheckTranscode is Check for the folder of playback segments: the server
// makes it itself when it is missing, so a missing one is made here, and a
// parent the server may not write to is what fails. within is a folder that
// has to be there already for the folder to be made (the media folder when
// the transcode folder is inside it, "" otherwise): a media disk that is not
// mounted is an error to show, never a folder to make on the disk under it.
func CheckTranscode(abs, within string) Dir {
	if within != "" {
		if st, err := os.Stat(within); (err != nil && errors.Is(err, fs.ErrNotExist)) || (err == nil && !st.IsDir()) {
			return Dir{Status: StatusMissing}
		}
	}
	if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return Dir{Status: StatusReadOnly, Error: err.Error(), Why: Classify(err)}
		}
	}
	return Check(abs)
}

// WriteTest makes a file in dir and removes it again.
func WriteTest(dir string) error {
	f, err := createTemp(dir, TestPrefix+"*")
	if err != nil {
		return err
	}
	name := f.Name()
	_, werr := f.WriteString("antresol")
	cerr := f.Close()
	rerr := os.Remove(name)
	return errors.Join(werr, cerr, rerr)
}

// countEntries counts dir's visible entries, at most limit.
func countEntries(dir string, limit int) (n int, more bool, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	for {
		names, err := f.Readdirnames(256)
		for _, name := range names {
			if hidden(name) {
				continue
			}
			if n >= limit {
				return n, true, nil
			}
			n++
		}
		if errors.Is(err, io.EOF) {
			return n, false, nil
		}
		if err != nil {
			return n, false, err
		}
	}
}

// hidden: dot files and the NAS system folders no library lives in.
func hidden(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@") || strings.HasPrefix(name, "#")
}

// Entry is a folder under the media folder; Name is relative to it
// ("books/ebooks" for a nested one).
type Entry struct {
	Name  string `json:"name"`
	Items int    `json:"items"`
	More  bool   `json:"more,omitempty"`
}

// Subfolders lists the folders directly under root, and under each of the
// top-level folders named in nested (the book folders live in one), with how
// many entries each holds. Sorted by name.
//
// A link to a folder outside root is left out: a folder there cannot be
// picked (the server keeps its folders inside the media folder).
func Subfolders(root string, nested []string) ([]Entry, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = filepath.Clean(root)
	}
	top, err := dirsIn(root, realRoot)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, name := range top {
		out = append(out, entryOf(root, name))
		for _, n := range nested {
			if !strings.EqualFold(n, name) {
				continue
			}
			subs, err := dirsIn(filepath.Join(root, name), realRoot)
			if err != nil {
				continue
			}
			for _, s := range subs {
				out = append(out, entryOf(root, name+"/"+s))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func entryOf(root, rel string) Entry {
	n, more, _ := countEntries(filepath.Join(root, filepath.FromSlash(rel)), CountCap)
	return Entry{Name: rel, Items: n, More: more}
}

// dirsIn names the visible folders directly in dir (a link to a folder is
// one, unless it leads out of realRoot).
func dirsIn(dir, realRoot string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if hidden(e.Name()) {
			continue
		}
		isDir := e.IsDir()
		if !isDir && e.Type()&fs.ModeSymlink != 0 {
			full := filepath.Join(dir, e.Name())
			if st, err := os.Stat(full); err == nil && st.IsDir() {
				if real, err := filepath.EvalSymlinks(full); err == nil && Inside(realRoot, real) {
					isDir = true
				}
			}
		}
		if isDir {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// aliases are the folder names people give each kind (compared without case,
// spaces and punctuation).
var aliases = map[Kind][]string{
	Movies:     {"movies", "movie", "films", "film", "фильмы", "кино"},
	Series:     {"series", "tv", "tvshows", "tvseries", "shows", "сериалы"},
	Music:      {"music", "музыка"},
	Ebooks:     {"ebooks", "ebook", "epub", "электронныекниги"},
	Audiobooks: {"audiobooks", "audiobook", "аудиокниги", "аудиокнига"},
	Comics:     {"comics", "comic", "manga", "комиксы", "манга"},
}

func normName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Suggest guesses which of the folders is each kind by its name. A kind with
// no folder that looks like it is left out. Where several look like it, one
// with files in it wins over an empty one (the server makes empty default
// folders itself, so the real one is the one with the files), then a
// subfolder over the folder that holds it.
func Suggest(folders []Entry) map[Kind]string {
	out := map[Kind]string{}
	best := map[Kind]Entry{}
	for _, f := range folders {
		last := f.Name
		if i := strings.LastIndex(last, "/"); i >= 0 {
			last = last[i+1:]
		}
		norm := normName(last)
		for _, k := range Kinds {
			for _, a := range aliases[k] {
				if a != norm {
					continue
				}
				if cur, ok := best[k]; !ok || better(f, cur) {
					best[k], out[k] = f, f.Name
				}
			}
		}
	}
	return out
}

// better: a beats b as the folder for a kind.
func better(a, b Entry) bool {
	if (a.Items > 0) != (b.Items > 0) {
		return a.Items > 0
	}
	return strings.Count(a.Name, "/") > strings.Count(b.Name, "/")
}

// Identity is the user and group the server runs as.
type Identity struct {
	UID int `json:"uid"`
	GID int `json:"gid"`
}

// Self is who the server runs as.
func Self() Identity { return Identity{UID: os.Getuid(), GID: os.Getgid()} }

// NearestExisting is path, or its closest parent that exists: the folder
// that has to be made writable when path cannot be made.
func NearestExisting(path string) string {
	p := filepath.Clean(path)
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

// NearestWithin is NearestExisting that never goes above limit (the folder
// the server was told to use): "", false when path is not inside limit, or
// limit itself is not there. That is a folder whose place is missing, a
// mount to fix, and no folder above it is the server's to hand over.
func NearestWithin(path, limit string) (string, bool) {
	p, limit := filepath.Clean(path), filepath.Clean(limit)
	if !Inside(limit, p) {
		return "", false
	}
	for {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
		if p == limit {
			return "", false
		}
		p = filepath.Dir(p)
	}
}

// Inside: p is base or lies inside it (compared as written, both cleaned).
func Inside(base, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Resolve is path with every link followed. A path that is not there yet is
// judged by the deepest folder of it that is, so a folder made there lands
// where Resolve says.
func Resolve(path string) (string, error) {
	cur, rest := filepath.Clean(path), ""
	for {
		_, err := os.Lstat(cur)
		if err == nil {
			real, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			return filepath.Join(real, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// Unmounted: dir is not there, is not a folder, or holds nothing visible,
// which is what a media disk that is not mounted looks like from inside a
// container. A folder that cannot be read is not called unmounted.
func Unmounted(dir string) bool {
	st, err := os.Stat(dir)
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	if !st.IsDir() {
		return true
	}
	n, _, err := countEntries(dir, 1)
	return err == nil && n == 0
}

// OwnerOf is the user and group that own path (or its nearest parent).
func OwnerOf(path string) (uid, gid int, ok bool) {
	st, err := os.Stat(NearestExisting(path))
	if err != nil {
		return 0, 0, false
	}
	sys, isUnix := st.Sys().(*syscall.Stat_t)
	if !isUnix {
		return 0, 0, false
	}
	return int(sys.Uid), int(sys.Gid), true
}
