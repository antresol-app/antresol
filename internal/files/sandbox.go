// Package files is the Finder-style file browser over the media storage:
// sandboxed path resolution, listings, thumbnails, admin file operations
// with a trash, and disk usage. Clients address files by root id and a path
// relative to that root; absolute server paths never leave this package's
// callers' JSON.
package files

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/antresol-app/antresol/internal/config"
)

var (
	// ErrOutside: the path escapes its root (.., absolute path, NUL, a
	// symlink pointing outside) or points into the trash.
	ErrOutside     = errors.New("files: path outside root")
	ErrUnknownRoot = errors.New("files: unknown root")
	ErrNotFound    = errors.New("files: not found")
	ErrNotDir      = errors.New("files: not a directory")
	ErrExists      = errors.New("files: already exists")
	ErrBadName     = errors.New("files: invalid name")
	ErrIsRoot      = errors.New("files: operation not allowed on a root")
	ErrCrossDevice = errors.New("files: source and destination are on different disks")
	// ErrReadOnly: the destination is managed by the app (the stream cache)
	// and never receives moved files.
	ErrReadOnly = errors.New("files: destination is read-only")
)

// TrashDirName is the trash folder directly under MediaRoot.
const TrashDirName = ".vault-trash"

// Root is a named entry point of the browser.
type Root struct {
	ID        string
	Title     string
	Icon      string
	Dir       string // absolute
	AdminOnly bool
	NoDrop    bool // never a Move destination (app-managed, e.g. the stream cache)
}

// Sandbox maps (root id, relative path) to absolute paths that are
// guaranteed to stay inside the root.
type Sandbox struct {
	mediaRoot string
	roots     []Root
	// MountOf finds a path's mount point (tests set their own): see TrashFor.
	MountOf MountResolver
}

// NewSandbox builds a sandbox over explicit roots (mediaRoot holds the trash).
func NewSandbox(mediaRoot string, roots []Root) *Sandbox {
	s := &Sandbox{mediaRoot: filepath.Clean(mediaRoot), MountOf: systemMountOf}
	for _, r := range roots {
		r.Dir = filepath.Clean(r.Dir)
		s.roots = append(s.roots, r)
	}
	return s
}

// DefaultRoots are the roots of a standard configuration, in sidebar order.
func DefaultRoots(cfg config.Config) []Root {
	roots := []Root{
		{ID: "library", Title: "Медиатека", Icon: "library", Dir: cfg.Abs("media")},
		{ID: "movies", Title: "Фильмы", Icon: "film", Dir: cfg.Abs(cfg.MoviesDir)},
		{ID: "series", Title: "Сериалы", Icon: "tv", Dir: cfg.Abs(cfg.SeriesDir)},
		{ID: "music", Title: "Музыка", Icon: "music", Dir: cfg.Abs(cfg.MusicDir)},
		{ID: "books", Title: "Книги", Icon: "book", Dir: cfg.Abs(cfg.BooksDir)},
	}
	roots = append(roots, acquisitionRoots(cfg)...)
	return append(roots, Root{ID: "disk", Title: "Весь диск", Icon: "disk", Dir: cfg.MediaRoot, AdminOnly: true})
}

// LibraryOnlyRoots drops the roots a library-only server has no use for:
// «Загрузки» and «Кеш просмотра» (no downloads run there), and the whole
// disk, which would show what else lives beside the library.
func LibraryOnlyRoots(roots []Root) []Root {
	out := make([]Root, 0, len(roots))
	for _, r := range roots {
		if r.ID != "downloads" && r.ID != "stream-cache" && r.ID != "disk" {
			out = append(out, r)
		}
	}
	return out
}

// Roots returns all configured roots (callers filter AdminOnly / missing dirs).
func (s *Sandbox) Roots() []Root { return append([]Root(nil), s.roots...) }

// Root returns the root with the given id.
func (s *Sandbox) Root(id string) (Root, error) {
	for _, r := range s.roots {
		if r.ID == id {
			return r, nil
		}
	}
	return Root{}, ErrUnknownRoot
}

// MediaRoot is the storage root (the trash lives directly under it).
func (s *Sandbox) MediaRoot() string { return s.mediaRoot }

// TrashDir is MediaRoot/.vault-trash: the trash of what lives on the same
// mount as MediaRoot (and of everything when mounts are not known).
func (s *Sandbox) TrashDir() string { return filepath.Join(s.mediaRoot, TrashDirName) }

// mountOf resolves the mount of p (its existing part, symlinks followed).
func (s *Sandbox) mountOf(p string) (string, bool) {
	if s.MountOf == nil {
		return "", false
	}
	real, err := realPath(p)
	if err != nil {
		real = filepath.Clean(p)
	}
	return s.MountOf(real)
}

// TrashFor is the trash a path goes to: a rename cannot leave its mount
// (two bind mounts of one disk are two mounts), so a path on another mount
// than MediaRoot goes to <its mount point>/.vault-trash; one on the same
// mount, or with mounts unknown, to TrashDir.
func (s *Sandbox) TrashFor(p string) string {
	m, ok := s.mountOf(p)
	if !ok {
		return s.TrashDir()
	}
	if mm, ok := s.mountOf(s.mediaRoot); !ok || mm == m {
		return s.TrashDir()
	}
	return filepath.Join(m, TrashDirName)
}

// TrashDirs are every trash the sandbox may use: TrashDir and the trash of
// each root's mount, once each.
func (s *Sandbox) TrashDirs() []string {
	out := []string{s.TrashDir()}
	seen := map[string]bool{out[0]: true}
	for _, r := range s.roots {
		if t := s.TrashFor(r.Dir); !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// knownTrash reports whether dir is one of TrashDirs.
func (s *Sandbox) knownTrash(dir string) bool {
	for _, t := range s.TrashDirs() {
		if t == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// cleanRel validates a client path and returns it cleaned ("" for the root).
func cleanRel(rel string) (string, error) {
	if strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || filepath.IsAbs(rel) {
		return "", ErrOutside
	}
	var parts []string
	for _, p := range strings.Split(rel, "/") {
		switch p {
		case "", ".":
			continue
		case "..":
			return "", ErrOutside
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "/"), nil
}

// within reports whether p is base or below it.
func within(base, p string) bool {
	return p == base || strings.HasPrefix(p, base+string(filepath.Separator))
}

// realPath resolves symlinks of the longest existing prefix of abs and
// appends the rest. A dangling symlink is reported as ErrOutside: creating
// through it would write wherever it points.
func realPath(abs string) (string, error) {
	rest := ""
	p := abs
	for {
		r, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(r, rest), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if _, lerr := os.Lstat(p); lerr == nil {
			return "", ErrOutside // exists as a link whose target does not
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// Resolve maps a client path to an absolute path inside the root. The
// path need not exist (its existing part is checked). It returns the
// lexical path (rootDir/rel), which is what the library stores.
func (s *Sandbox) Resolve(root, rel string) (string, error) {
	r, err := s.Root(root)
	if err != nil {
		return "", err
	}
	clean, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(r.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", err
	}
	abs := filepath.Join(r.Dir, filepath.FromSlash(clean))
	real, err := realPath(abs)
	if err != nil {
		return "", err
	}
	if !within(realRoot, real) {
		return "", ErrOutside
	}
	if s.inTrash(real) {
		return "", ErrOutside
	}
	return abs, nil
}

// inTrash reports whether the resolved path real is the trash or inside it.
// Besides the lexical check it compares case-insensitively on darwin and
// by file identity (a case variant on a case-insensitive disk is the same
// folder under another name).
// Every trash counts: MediaRoot's and those of other mounts.
func (s *Sandbox) inTrash(real string) bool {
	for _, t := range s.TrashDirs() {
		if inTrashDir(filepath.Dir(t), real) {
			return true
		}
	}
	return false
}

// inTrashDir is inTrash for the trash directly under base.
func inTrashDir(base, real string) bool {
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return false
	}
	trash := filepath.Join(realBase, TrashDirName)
	if within(trash, real) || runtime.GOOS == "darwin" && within(strings.ToLower(trash), strings.ToLower(real)) {
		return true
	}
	tst, err := os.Stat(trash)
	if err != nil {
		return false
	}
	for p := real; within(realBase, p) && p != realBase; p = filepath.Dir(p) {
		if st, err := os.Stat(p); err == nil && os.SameFile(st, tst) {
			return true
		}
	}
	return false
}

// NoDropAt reports whether dir (an absolute path) lies in a root that must
// not receive moved files, whichever root it was addressed through.
func (s *Sandbox) NoDropAt(dir string) bool {
	real, err := realPath(dir)
	if err != nil {
		return true
	}
	for _, r := range s.roots {
		if !r.NoDrop {
			continue
		}
		rd, err := filepath.EvalSymlinks(r.Dir)
		if err != nil {
			rd = r.Dir
		}
		if within(rd, real) || runtime.GOOS == "darwin" && within(strings.ToLower(rd), strings.ToLower(real)) {
			return true
		}
	}
	return false
}

// Rel converts an absolute path below the root back to a client path.
func (s *Sandbox) Rel(root, abs string) (string, bool) {
	r, err := s.Root(root)
	if err != nil {
		return "", false
	}
	abs = filepath.Clean(abs)
	if !within(r.Dir, abs) {
		return "", false
	}
	rel, err := filepath.Rel(r.Dir, abs)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

// ValidName checks a single path component for mkdir/rename: non-empty, at
// most 255 bytes, no '/', NUL or control characters, no leading or trailing
// spaces, not "." / "..", not hidden (a leading dot hides it from listings),
// no invisible format characters (bidi overrides that disguise "gpj.exe" as
// "exe.jpg", zero-width spaces, BOM).
func ValidName(name string) error {
	if name == "" || len(name) > 255 || strings.HasPrefix(name, ".") || strings.TrimSpace(name) != name {
		return ErrBadName
	}
	for _, c := range name {
		if c == '/' || c == '\\' || unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return ErrBadName
		}
	}
	return nil
}
