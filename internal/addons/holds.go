package addons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/antresol-app/antresol/addonkit"
)

// Limits of one addon's set of holds.
const (
	MaxHolds     = 20000
	MaxHoldFiles = 200000 // all the set's files together
)

// ErrBadHold: a set of holds is refused; the previous set stays.
var ErrBadHold = errors.New("addons: bad holds")

// Holds indexes the files addons hold (files they still need), so the
// core's file operations can warn, refuse or ask the addon first. Each
// addon gives its whole set at once (Put); the index covers every listed
// path of a hold and its folders up to the hold's root, and a listed path
// that is a folder covers everything below it. Paths match byte for byte.
// The sets are kept in the database and loaded at start (Load), ignoring
// addons no longer installed.
type Holds struct {
	Refs  *RefStore
	Hooks *Hooks // set after the Manager exists
	// Roots: the file browser's roots; a hold's root is at or inside one.
	Roots []string
	// Installed: id → display name of the installed addons; Load drops
	// the holds of others. Required by Load.
	Installed func(ctx context.Context) (map[string]string, error)
	Log       *slog.Logger // nil: slog.Default()

	writeMu sync.Mutex // serializes Load, Put and Drop (database, then swap)
	mu      sync.RWMutex
	sets    map[string][]addonkit.Hold // addon id → its set
	index   holdIndex
}

type holdEntry struct {
	addon    string
	hold     *addonkit.Hold
	selected bool // some selected listed path of the hold is at or below the path
}

type holdIndex struct {
	at     map[string]holdEntry      // listed paths and their folders up to the root
	listed map[string]holdEntry      // listed paths only: they cover what is below
	byKey  map[string]*addonkit.Hold // "<addon id>\x00<key>"
}

func (h *Holds) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

// Load replaces the index with the saved sets of the installed addons.
// A saved hold no longer valid (its root left the file browser, an entry
// that does not decode, a key twice, past the limits) is skipped and
// logged; the addon's other holds stay.
func (h *Holds) Load(ctx context.Context) error {
	if h.Installed == nil {
		return errors.New("addons: holds: no Installed")
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	raw, err := h.Refs.LoadHolds(ctx)
	if err != nil {
		return err
	}
	installed, err := h.Installed(ctx)
	if err != nil {
		return err
	}
	sets := map[string][]addonkit.Hold{}
	for id, b := range raw {
		if _, ok := installed[id]; !ok {
			continue
		}
		if set := h.loadSet(id, b); len(set) > 0 {
			sets[id] = set
		}
	}
	h.swap(sets)
	return nil
}

// loadSet decodes a saved set hold by hold, keeping the valid ones.
func (h *Holds) loadSet(id string, b []byte) []addonkit.Hold {
	var items []json.RawMessage
	if err := json.Unmarshal(b, &items); err != nil {
		h.log().Warn("addon holds: saved set unreadable, skipped", "addon", id, "err", err)
		return nil
	}
	rc := h.newResolver()
	set := make([]addonkit.Hold, 0, len(items))
	keys := map[string]bool{}
	files := 0
	for i, item := range items {
		var x addonkit.Hold
		if err := json.Unmarshal(item, &x); err != nil {
			h.log().Warn("addon holds: saved hold unreadable, skipped", "addon", id, "index", i, "err", err)
			continue
		}
		x, err := h.validateHold(rc, x, true)
		switch {
		case err != nil:
		case keys[x.Key]:
			err = fmt.Errorf("%w: key %q twice", ErrBadHold, x.Key)
		case len(set) >= MaxHolds:
			err = fmt.Errorf("%w: more than %d holds", ErrBadHold, MaxHolds)
		case files+len(x.Files) > MaxHoldFiles:
			err = fmt.Errorf("%w: more than %d files", ErrBadHold, MaxHoldFiles)
		}
		if err != nil {
			h.log().Warn("addon holds: saved hold skipped", "addon", id, "key", x.Key, "err", err)
			continue
		}
		keys[x.Key] = true
		files += len(x.Files)
		set = append(set, x)
	}
	return set
}

// Put replaces the addon's set of holds with holds: all are validated
// first, and a refused set (or a failed save) leaves the previous one.
func (h *Holds) Put(ctx context.Context, addonID string, holds []addonkit.Hold) error {
	if addonID == "" {
		return ErrNoAddon
	}
	set, err := h.validate(holds)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return err
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if err := h.Refs.SaveHolds(ctx, addonID, raw); err != nil {
		return err
	}
	sets := make(map[string][]addonkit.Hold, len(h.sets)+1)
	for id, s := range h.sets {
		sets[id] = s
	}
	if len(set) == 0 {
		delete(sets, addonID)
	} else {
		sets[addonID] = set
	}
	h.swap(sets)
	return nil
}

// Drop forgets the addon's holds, saved and indexed (Manager.Remove).
func (h *Holds) Drop(ctx context.Context, addonID string) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if err := h.Refs.DropHolds(ctx, addonID); err != nil {
		return err
	}
	if _, ok := h.sets[addonID]; !ok {
		return nil
	}
	sets := make(map[string][]addonkit.Hold, len(h.sets))
	for id, s := range h.sets {
		if id != addonID {
			sets[id] = s
		}
	}
	h.swap(sets)
	return nil
}

// swap builds the index of sets and installs both; the caller holds
// writeMu (h.sets is only written under it).
func (h *Holds) swap(sets map[string][]addonkit.Hold) {
	idx := buildHoldIndex(sets)
	h.mu.Lock()
	h.sets, h.index = sets, idx
	h.mu.Unlock()
}

// Lookup reports the hold at path: a listed path of a hold, one of its
// folders up to the root, or anything below a listed path (a listed
// folder). selected is false when only unselected listed paths of the
// hold are at, below or above path: the addon still owns them, though it
// does not use them. A selected path wins over an unselected one, then an
// active hold over an inactive one. The hold must not be modified.
func (h *Holds) Lookup(path string) (addonID string, hold *addonkit.Hold, selected bool, ok bool) {
	path = filepath.Clean(path)
	h.mu.RLock()
	e := h.index.at[path]
	for q := filepath.Dir(path); len(h.index.listed) > 0; q = filepath.Dir(q) {
		if l, ok := h.index.listed[q]; ok {
			e = betterHold(e, l)
		}
		if q == filepath.Dir(q) {
			break
		}
	}
	h.mu.RUnlock()
	if e.hold == nil {
		return "", nil, false, false
	}
	return e.addon, e.hold, e.selected, true
}

// Get returns the addon's hold of key. The hold must not be modified.
func (h *Holds) Get(addonID, key string) (*addonkit.Hold, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	x, ok := h.index.byKey[addonID+"\x00"+key]
	return x, ok
}

// Sets returns every addon's set of holds, by addon id. The sets must not
// be modified (a Put installs new ones; these stay as they are).
func (h *Holds) Sets() map[string][]addonkit.Hold {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sets
}

// Touching returns the keys, by addon id, of the holds a file operation on
// from touches: a hold with a listed path at, below or above from (from
// is a listed path of the hold, one of its folders, its root, a folder
// containing it, or lies inside a listed folder). An unlisted file below a
// hold's root touches nothing. Keys are sorted.
func (h *Holds) Touching(from string) map[string][]string {
	from = filepath.Clean(from)
	out := map[string][]string{}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for id, set := range h.sets {
		for i := range set {
			if !within(from, set[i].Root) && !within(set[i].Root, from) {
				continue
			}
			for _, f := range set[i].Files {
				if within(from, f.Path) || within(f.Path, from) {
					out[id] = append(out[id], set[i].Key)
					break
				}
			}
		}
		sort.Strings(out[id])
	}
	for id, keys := range out {
		if len(keys) == 0 {
			delete(out, id)
		}
	}
	return out
}

// validate checks a whole set: at most MaxHolds holds and MaxHoldFiles
// files, unique keys, and each hold as validateHold. It returns the set
// with every path canonical (a copy: holds is not modified).
func (h *Holds) validate(holds []addonkit.Hold) ([]addonkit.Hold, error) {
	if len(holds) > MaxHolds {
		return nil, fmt.Errorf("%w: more than %d holds", ErrBadHold, MaxHolds)
	}
	rc := h.newResolver()
	out := make([]addonkit.Hold, 0, len(holds))
	files := 0
	keys := make(map[string]bool, len(holds))
	for _, x := range holds {
		c, err := h.validateHold(rc, x, false)
		if err != nil {
			return nil, err
		}
		if keys[x.Key] {
			return nil, fmt.Errorf("%w: key %q twice", ErrBadHold, x.Key)
		}
		keys[x.Key] = true
		if files += len(x.Files); files > MaxHoldFiles {
			return nil, fmt.Errorf("%w: more than %d files", ErrBadHold, MaxHoldFiles)
		}
		out = append(out, c)
	}
	return out, nil
}

// validateHold checks one hold and returns it with canonical paths: its
// key a valid ref; its root absolute, clean and, symlinks resolved, at or
// inside a file-browser root (also resolved); each listed path absolute,
// clean, at or inside the hold's root and not a file-browser root itself
// (a listed folder covers everything below it: the whole root would be
// frozen), both as written and resolved. The hold protects the real path:
// a path listed through a symlinked folder is stored as the real one, spelled
// below the root as the file browser names it (the root as configured).
//
// A path that cannot be resolved for another reason than not being there
// (EACCES, EIO) is refused; loading (a saved set, already in the root's
// spelling) keeps it as saved instead, when it is at or inside a root as
// written, and logs it: the data stays protected while the addon is down.
func (h *Holds) validateHold(rc *resolver, x addonkit.Hold, loading bool) (addonkit.Hold, error) {
	if !ValidRef(x.Key) {
		return x, fmt.Errorf("%w: a key is 1-%d printable ASCII characters", ErrBadHold, MaxRefLen)
	}
	if !cleanAbs(x.Root) {
		return x, fmt.Errorf("%w: hold %q: root outside the file browser", ErrBadHold, x.Key)
	}
	// resolve: p canonical in the root's spelling; bad: a refusal that
	// says more than "outside" (p cannot be resolved)
	resolve := func(p string) (out string, bad error) {
		c, err := rc.canonical(p)
		if err == nil {
			out, err = rc.inRootForm(c)
			if err != nil {
				return "", nil
			}
			return out, nil
		}
		if !loading || !rc.underRoot(p) {
			return "", fmt.Errorf("%w: hold %q: %s cannot be resolved: %v", ErrBadHold, x.Key, p, err)
		}
		h.log().Warn("addon holds: saved path cannot be resolved now, kept as saved", "key", x.Key, "path", p, "err", err)
		return p, nil
	}
	root, bad := resolve(x.Root)
	if bad != nil {
		return x, bad
	}
	if root == "" {
		return x, fmt.Errorf("%w: hold %q: root outside the file browser", ErrBadHold, x.Key)
	}
	x.Root = root
	files := make([]addonkit.HoldFile, len(x.Files))
	for i, f := range x.Files {
		if !cleanAbs(f.Path) {
			return x, fmt.Errorf("%w: hold %q: a file outside its root", ErrBadHold, x.Key)
		}
		p, bad := resolve(f.Path)
		if bad != nil {
			return x, bad
		}
		if p == "" || !within(root, p) {
			return x, fmt.Errorf("%w: hold %q: a file outside its root", ErrBadHold, x.Key)
		}
		if rc.isRoot(f.Path) || rc.isRoot(p) {
			return x, fmt.Errorf("%w: hold %q: a listed path is a root of the file browser", ErrBadHold, x.Key)
		}
		f.Path = p
		files[i] = f
	}
	x.Files = files
	return x, nil
}

// resolver resolves paths against the file system for one validation:
// the roots once (as files.realRoots does: EvalSymlinks), folders cached.
type resolver struct {
	roots []string // the roots as written, clean, absolute
	real  []string // the same, resolved
	dirs  map[string]string
}

func (h *Holds) newResolver() *resolver {
	rc := &resolver{dirs: map[string]string{}}
	for _, r := range h.Roots {
		if r == "" || !filepath.IsAbs(r) {
			continue
		}
		r = filepath.Clean(r)
		real, err := canonical(r)
		if err != nil {
			// it cannot be resolved now (EACCES…): kept as written, so a
			// saved hold under it is not lost and a new one is refused
			// path by path
			real = r
		}
		rc.roots = append(rc.roots, r)
		rc.real = append(rc.real, real)
	}
	return rc
}

// underRoot: p (clean, absolute) is at or inside a root as written.
func (rc *resolver) underRoot(p string) bool {
	for _, r := range rc.roots {
		if within(r, p) {
			return true
		}
	}
	return false
}

// isRoot: p is one of the file browser's roots, as written or resolved.
func (rc *resolver) isRoot(p string) bool {
	for i := range rc.roots {
		if p == rc.roots[i] || p == rc.real[i] {
			return true
		}
	}
	return false
}

// inRootForm spells p, already canonical, the way the file browser's root
// containing it names it (the root as configured, then the path below it):
// the file layer looks holds up by that spelling, whether or not the root
// is itself a symlink. Several roots containing p: the longest resolved
// one. An error: p is outside every root.
func (rc *resolver) inRootForm(p string) (string, error) {
	best := -1
	for i, r := range rc.real {
		if within(r, p) && (best < 0 || len(r) > len(rc.real[best])) {
			best = i
		}
	}
	if best < 0 {
		return "", errors.New("addons: path outside the file browser")
	}
	rel, err := filepath.Rel(rc.real[best], p)
	if err != nil {
		return "", err
	}
	return filepath.Join(rc.roots[best], rel), nil
}

// canonical is the package's canonical(), with the folder part cached
// (a hold lists many files of few folders).
func (rc *resolver) canonical(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("addons: path %q is not absolute", p)
	}
	p = filepath.Clean(p)
	dir, base := filepath.Split(p)
	if dir == "" || base == "" {
		return canonical(p)
	}
	if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return canonical(p) // the file itself is a link: follow it
	}
	dir = filepath.Clean(dir)
	d, ok := rc.dirs[dir]
	if !ok {
		var err error
		if d, err = canonical(dir); err != nil {
			return "", err
		}
		rc.dirs[dir] = d
	}
	return filepath.Join(d, base), nil
}

// canonical returns p, absolute, cleaned, with its symlinks resolved:
// EvalSymlinks of the deepest existing parent plus the rest (a path that
// does not exist, or a dangling link, keeps its own tail). Only a part
// that is not there is walked up from: any other error (EACCES, EIO) is
// returned, since what is below an unreadable folder cannot be known.
func canonical(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("addons: path %q is not absolute", p)
	}
	p = filepath.Clean(p)
	rest := ""
	for q := p; ; q = filepath.Dir(q) {
		real, err := filepath.EvalSymlinks(q)
		if err == nil {
			return filepath.Join(real, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", err
		}
		if q == filepath.Dir(q) {
			return p, nil
		}
		rest = filepath.Join(filepath.Base(q), rest)
	}
}

// cleanAbs: p is an absolute path in its clean form.
func cleanAbs(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p
}

// within: p is base or below it (both clean).
func within(base, p string) bool {
	if p == base {
		return true
	}
	if base == string(filepath.Separator) {
		return strings.HasPrefix(p, base)
	}
	return strings.HasPrefix(p, base+string(filepath.Separator))
}

// buildHoldIndex indexes every listed path of every hold and its folders
// up to the hold's root, and keeps the listed paths apart (they cover
// what is below them).
func buildHoldIndex(sets map[string][]addonkit.Hold) holdIndex {
	idx := holdIndex{at: map[string]holdEntry{}, listed: map[string]holdEntry{}, byKey: map[string]*addonkit.Hold{}}
	ids := make([]string, 0, len(sets))
	for id := range sets {
		ids = append(ids, id)
	}
	sort.Strings(ids) // a tie goes to the first id, every time
	for _, id := range ids {
		set := sets[id]
		for i := range set {
			x := &set[i]
			idx.byKey[id+"\x00"+x.Key] = x
			for _, f := range x.Files {
				n := holdEntry{addon: id, hold: x, selected: f.Selected}
				idx.listed[f.Path] = betterHold(idx.listed[f.Path], n)
				for p := f.Path; ; p = filepath.Dir(p) {
					idx.at[p] = betterHold(idx.at[p], n)
					if p == x.Root || p == filepath.Dir(p) {
						break
					}
				}
			}
		}
	}
	return idx
}

// betterHold picks the entry a path reports: a selected file wins over an
// unselected one, then an active hold over an inactive one. A folder is
// selected when any selected file is below it.
func betterHold(old, n holdEntry) holdEntry {
	switch {
	case old.hold == nil:
		return n
	case n.selected != old.selected:
		if n.selected {
			return n
		}
		return old
	case n.hold.Active && !old.hold.Active:
		return n
	}
	return old
}
