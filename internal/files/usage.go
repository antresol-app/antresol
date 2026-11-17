package files

import (
	"cmp"
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	usageTTL   = 10 * time.Minute
	dirSizeTTL = 10 * time.Minute
	// dirSizeConcurrency bounds background folder-size walks: they compete
	// with playback, the scanner and downloads for the media disk.
	dirSizeConcurrency = 2
	dirSizeMaxEntries  = 10000
)

// usageCategories are the roots counted separately, in priority order: a
// hardlinked file (an addon's download + library copy) is counted once, in
// the first category that holds it; the addons' segments (Usage.Extra) come
// after them.
var usageCategories = []string{"movies", "series", "music", "books"}

// extraDeadline bounds Usage.Extra (the disk.usage hook's own timeout).
const extraDeadline = 2 * time.Second

// ExtraSegment is a disk segment an addon reports (the disk.usage hook):
// with Dir, the folder is walked after the library categories; without,
// Bytes is taken as is. Label is the segment's name by language (ru, en).
type ExtraSegment struct {
	ID    string
	Label map[string]string
	Dir   string
	Bytes int64

	forms []string // Dir as given, resolved (Dir once checked), and resolved as below the media root
}

type Category struct {
	ID    string `json:"id"`
	Bytes int64  `json:"bytes"`
	// Label (an addon's segment): its name in the request's language
	// (UsageReport.Localized); the library categories have none.
	Label string `json:"label,omitempty"`

	labels map[string]string
}

// Localized is r with each addon segment's Label in lang (the other
// language when the addon gave none in lang).
func (r UsageReport) Localized(lang string) UsageReport {
	r.Categories = slices.Clone(r.Categories)
	for i, c := range r.Categories {
		if c.labels == nil {
			continue
		}
		l := c.labels[lang]
		if l == "" {
			l = cmp.Or(c.labels["ru"], c.labels["en"])
		}
		r.Categories[i].Label = l
	}
	return r
}

type UsageReport struct {
	Total      int64      `json:"total"`
	Used       int64      `json:"used"`
	Free       int64      `json:"free"`
	Categories []Category `json:"categories"`
}

// Usage reports disk usage (cached 10 min) and folder sizes (computed in
// the background, cached 10 min).
type Usage struct {
	Sandbox *Sandbox
	// NoDisk (a library-only server): the report says nothing about the
	// disk (total, used and free are 0, so no meter is drawn) and lists
	// only the categories whose roots exist, without «other».
	NoDisk bool

	mu      sync.Mutex // guards snap and the «other» fields
	snap    *usageSnap
	compute sync.Mutex // one usage walk at a time
	// other is the last «other» breakdown; otherRunning: its walk runs.
	other        *otherSizes
	otherRunning bool
	// otherTimeout bounds an «other» walk (0: otherWalkTimeout); otherHook
	// runs at the start of one (tests).
	otherTimeout time.Duration
	otherHook    func(ctx context.Context)

	dmu     sync.Mutex
	dirs    map[string]dirSize
	walkSem chan struct{}
	// walk overrides treeSize (tests).
	walk func(ctx context.Context, dir string) int64

	// Extra lists the addons' segments (nil: none); it is called with a
	// deadline of extraTimeout when the report is recomputed.
	Extra func(ctx context.Context) []ExtraSegment
	// extraTimeout overrides extraDeadline; statfs, the disk figures (tests).
	extraTimeout time.Duration
	statfs       func(path string) (total, used, free int64, err error)
}

type dirSize struct {
	size    int64
	at      time.Time
	running bool
	done    bool
}

type inode struct{ dev, ino uint64 }

// treeSize sums apparent file sizes below dir, counting each hardlinked
// inode once (seen is shared across calls when not nil).
func treeSize(ctx context.Context, dir string, seen map[inode]bool) int64 {
	return walkFiles(ctx, dir, seen, nil, nil)
}

// walkFiles is treeSize that also reports each counted file to add (when
// set; a hardlinked inode seen before is not reported) and does not enter
// the directories skip says no to.
func walkFiles(ctx context.Context, dir string, seen map[inode]bool, skip func(p string, d fs.DirEntry) bool, add func(p string, n int64)) int64 {
	var total int64
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if d.IsDir() && skip != nil && p != dir && skip(p, d) {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
			k := inode{uint64(st.Dev), uint64(st.Ino)}
			if seen[k] {
				return nil
			}
			seen[k] = true
		}
		total += fi.Size()
		if add != nil {
			add(p, fi.Size())
		}
		return nil
	})
	return total
}

// usageSnap is one usage walk: the report, each category's first-level
// entries (sorted, see Breakdown), the hardlinked inodes it counted, the
// addons' segments (by id) and the folders they walked (each in every
// form a walk of the media root may meet it).
type usageSnap struct {
	report    UsageReport
	at        time.Time
	entries   map[string][]UsageEntry
	seen      map[inode]bool
	extra     map[string]bool
	extraDirs []string
}

// Report returns total/used/free of the media disk and per-category sizes.
func (u *Usage) Report(ctx context.Context) (UsageReport, error) {
	s, err := u.snapshot(ctx)
	if err != nil {
		return UsageReport{}, err
	}
	return s.report, nil
}

// snapshot is the cached usage walk, recomputed when stale.
func (u *Usage) snapshot(ctx context.Context) (*usageSnap, error) {
	u.mu.Lock()
	if s := u.snap; s != nil && time.Since(s.at) < usageTTL {
		u.mu.Unlock()
		return s, nil
	}
	u.mu.Unlock()

	u.compute.Lock()
	defer u.compute.Unlock()
	u.mu.Lock()
	if s := u.snap; s != nil && time.Since(s.at) < usageTTL { // computed while we waited
		u.mu.Unlock()
		return s, nil
	}
	u.mu.Unlock()

	r := UsageReport{Categories: []Category{}}
	if !u.NoDisk {
		var err error
		if r.Total, r.Used, r.Free, err = u.disk(); err != nil {
			return nil, err
		}
	}
	s := &usageSnap{entries: map[string][]UsageEntry{}, seen: map[inode]bool{}, extra: map[string]bool{}}
	var sum int64
	for _, id := range usageCategories {
		var n int64
		root, err := u.Sandbox.Root(id)
		if err == nil {
			// The same walk sums the first-level entries: the breakdown of
			// a category costs no IO of its own.
			acc := entrySums{}
			n = walkFiles(ctx, root.Dir, s.seen, nil, acc.under(root.Dir, nil))
			s.entries[id] = acc.sorted()
		} else if u.NoDisk {
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sum += n
		r.Categories = append(r.Categories, Category{ID: id, Bytes: n})
	}
	for _, x := range u.extraSegments(ctx) {
		n := max(x.Bytes, 0)
		if x.Dir != "" {
			// the same pass and seen set: a download hardlinked into the
			// library was counted there
			acc := entrySums{}
			n = walkFiles(ctx, x.Dir, s.seen, nil, acc.under(x.Dir, nil))
			s.entries[x.ID] = acc.sorted()
			s.extraDirs = append(s.extraDirs, x.forms...)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sum += n
		s.extra[x.ID] = true
		r.Categories = append(r.Categories, Category{ID: x.ID, Bytes: n, labels: x.Label})
	}
	if !u.NoDisk {
		r.Categories = append(r.Categories, Category{ID: "other", Bytes: max(r.Used-sum, 0)})
	}

	s.report, s.at = r, time.Now()
	u.mu.Lock()
	u.snap = s
	u.mu.Unlock()
	return s, nil
}

// disk is the media disk's total, used and free bytes.
func (u *Usage) disk() (total, used, free int64, err error) {
	if u.statfs != nil {
		return u.statfs(u.Sandbox.MediaRoot())
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(u.Sandbox.MediaRoot(), &st); err != nil {
		return 0, 0, 0, err
	}
	bs := int64(st.Bsize)
	total, free = int64(st.Blocks)*bs, int64(st.Bavail)*bs
	return total, total - int64(st.Bfree)*bs, free, nil
}

// extraSegments asks Extra (within extraDeadline) and keeps the segments
// it may count: an id not seen before, and a Dir that is absolute, clean,
// at or inside the media root or a file-browser root (their symlinks
// resolved, as the dir's), and neither at, inside nor around a library
// category or an earlier segment's folder (so never the media root
// itself); "" Dir is a segment by Bytes. A dir that does not resolve (a
// missing mount) is left out for this report.
func (u *Usage) extraSegments(ctx context.Context) []ExtraSegment {
	if u.Extra == nil {
		return nil
	}
	ectx, cancel := context.WithTimeout(ctx, cmp.Or(u.extraTimeout, extraDeadline))
	defer cancel()
	segs := u.Extra(ectx)
	var roots, taken []string
	rmr, _ := filepath.EvalSymlinks(u.Sandbox.MediaRoot())
	for _, d := range append([]string{u.Sandbox.MediaRoot()}, rootDirs(u.Sandbox.Roots())...) {
		if r, err := filepath.EvalSymlinks(d); err == nil {
			roots = append(roots, r)
		}
	}
	for _, id := range usageCategories {
		if root, err := u.Sandbox.Root(id); err == nil {
			if r, err := filepath.EvalSymlinks(root.Dir); err == nil {
				taken = append(taken, r)
			}
		}
	}
	ids := map[string]bool{}
	var out []ExtraSegment
	for _, x := range segs {
		if x.ID == "" || ids[x.ID] || slices.Contains(usageCategories, x.ID) || x.ID == "other" {
			continue
		}
		if x.Dir != "" {
			if !filepath.IsAbs(x.Dir) || filepath.Clean(x.Dir) != x.Dir {
				continue
			}
			d, err := filepath.EvalSymlinks(x.Dir)
			if err != nil || !slices.ContainsFunc(roots, func(r string) bool { return r == d || strictlyBelow(d, r) }) ||
				slices.ContainsFunc(taken, func(t string) bool { return t == d || strictlyBelow(d, t) || strictlyBelow(t, d) }) {
				continue
			}
			// walked resolved: a dir that is itself a symlink is walked into
			x.forms, x.Dir = []string{x.Dir, d}, d
			if strictlyBelow(d, rmr) {
				rel, _ := filepath.Rel(rmr, d)
				x.forms = append(x.forms, filepath.Join(u.Sandbox.MediaRoot(), rel))
			}
			taken = append(taken, d)
		}
		ids[x.ID] = true
		out = append(out, x)
	}
	return out
}

func rootDirs(rs []Root) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Dir)
	}
	return out
}

// strictlyBelow: p lies inside dir (both clean and absolute), not at it.
func strictlyBelow(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// sem is the slots of the background walks (folder sizes, «other»).
func (u *Usage) sem() chan struct{} {
	u.dmu.Lock()
	defer u.dmu.Unlock()
	if u.walkSem == nil {
		u.walkSem = make(chan struct{}, dirSizeConcurrency)
	}
	return u.walkSem
}

// DirSize returns the cached size of a folder; when it is missing or stale
// it starts a background computation and returns ok=false (or the stale
// value with ok=true while recomputing). At most dirSizeConcurrency walks
// run at once; when all slots are busy nothing is started and the caller
// just sees pending, so a client walking many folders cannot pile up IO.
func (u *Usage) DirSize(abs string) (int64, bool) {
	u.dmu.Lock()
	defer u.dmu.Unlock()
	if u.dirs == nil {
		u.dirs = map[string]dirSize{}
	}
	if u.walkSem == nil {
		u.walkSem = make(chan struct{}, dirSizeConcurrency)
	}
	if len(u.dirs) > dirSizeMaxEntries { // bound memory, but never forget running walks
		for k, d := range u.dirs {
			if !d.running {
				delete(u.dirs, k)
			}
		}
	}
	d := u.dirs[abs]
	if d.done && time.Since(d.at) < dirSizeTTL {
		return d.size, true
	}
	if d.running {
		return d.size, d.done
	}
	select {
	case u.walkSem <- struct{}{}:
	default:
		return d.size, d.done // busy: try again on a later request
	}
	d.running = true
	u.dirs[abs] = d
	go func() {
		defer func() { <-u.walkSem }()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var n int64
		if u.walk != nil {
			n = u.walk(ctx, abs)
		} else {
			n = treeSize(ctx, abs, map[inode]bool{})
		}
		u.dmu.Lock()
		if ctx.Err() == nil {
			u.dirs[abs] = dirSize{size: n, at: time.Now(), done: true}
		} else {
			delete(u.dirs, abs)
		}
		u.dmu.Unlock()
	}()
	return d.size, d.done
}
