package imgcache

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The resized-copy cache keeps what was used lately and stays under a
// size: a copy unused for pruneAge goes (a replaced poster leaves its old
// copies behind, their names carry the source mtime), and when the rest
// is still over pruneBytes the least recently used go first.
const (
	pruneAge   = 90 * 24 * time.Hour
	pruneBytes = 1 << 30
	// pruneEvery: Resized prunes a cache directory on its first call and
	// then at most this often.
	pruneEvery = 6 * time.Hour
	// touchAfter: a hit refreshes the copy's mtime (the LRU clock; atime
	// may be off) when it is older than this, so a busy copy costs one
	// Chtimes a day.
	touchAfter = 24 * time.Hour
	// stale: a temp file of an interrupted encode.
	stale = time.Hour
)

var (
	pruneMu  sync.Mutex
	prunedAt = map[string]time.Time{}
)

// pruneDue prunes cacheDir's copies when it has not been done lately.
func pruneDue(cacheDir string) {
	pruneMu.Lock()
	due := time.Since(prunedAt[cacheDir]) >= pruneEvery
	if due {
		prunedAt[cacheDir] = time.Now()
	}
	pruneMu.Unlock()
	if due {
		Prune(cacheDir, pruneAge, pruneBytes)
	}
}

// touch marks the copy at p as used now when its mtime is old.
func touch(p string, st os.FileInfo) {
	if now := time.Now(); now.Sub(st.ModTime()) > touchAfter {
		os.Chtimes(p, now, now)
	}
}

// Prune removes the copies in cacheDir/resized not used for maxAge, then
// the least recently used ones until the rest fits in maxBytes, and the
// temp files of interrupted encodes. It returns how many files it removed.
func Prune(cacheDir string, maxAge time.Duration, maxBytes int64) int {
	dir := filepath.Join(cacheDir, "resized")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	type file struct {
		path string
		size int64
		used time.Time
	}
	var keep []file
	var total int64
	removed := 0
	drop := func(p string) {
		if os.Remove(p) == nil {
			removed++
		}
	}
	for _, e := range ents {
		st, err := e.Info()
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		p, age := filepath.Join(dir, e.Name()), time.Since(st.ModTime())
		switch {
		case strings.HasPrefix(e.Name(), ".resize-"):
			if age > stale {
				drop(p)
			}
		case age > maxAge:
			drop(p)
		default:
			keep = append(keep, file{p, st.Size(), st.ModTime()})
			total += st.Size()
		}
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].used.Before(keep[j].used) })
	for _, f := range keep {
		if total <= maxBytes {
			break
		}
		drop(f.path)
		total -= f.size
	}
	return removed
}
