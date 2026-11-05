package files

import (
	"context"
	"io/fs"
	"path/filepath"
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
// hardlinked file (a managed download + library copy) is counted once, in the
// first category that holds it.
var usageCategories = []string{"movies", "series", "music", "books", "downloads", "stream-cache"}

type Category struct {
	ID    string `json:"id"`
	Bytes int64  `json:"bytes"`
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
// entries (sorted, see Breakdown) and the hardlinked inodes it counted.
type usageSnap struct {
	report  UsageReport
	at      time.Time
	entries map[string][]UsageEntry
	seen    map[inode]bool
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
		var st syscall.Statfs_t
		if err := syscall.Statfs(u.Sandbox.MediaRoot(), &st); err != nil {
			return nil, err
		}
		bs := int64(st.Bsize)
		r.Total, r.Free = int64(st.Blocks)*bs, int64(st.Bavail)*bs
		r.Used = r.Total - int64(st.Bfree)*bs
	}
	s := &usageSnap{entries: map[string][]UsageEntry{}, seen: map[inode]bool{}}
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
	if !u.NoDisk {
		r.Categories = append(r.Categories, Category{ID: "other", Bytes: max(r.Used-sum, 0)})
	}

	s.report, s.at = r, time.Now()
	u.mu.Lock()
	u.snap = s
	u.mu.Unlock()
	return s, nil
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
