package comics

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/natsort"
)

// cacheLimit is the size of all unpacked archives in a cache dir together.
// The least recently opened entries go first; entries in use stay.
var cacheLimit int64 = 2 << 30

const (
	indexName = "index.json"
	tmpPrefix = ".tmp-"
	staleTmp  = time.Hour // an unpack older than this was interrupted
)

// cacheIndex describes an unpacked archive: page i is the file
// Pages[i].File in the entry folder. Page files are named by number, so a
// name inside the archive never becomes a path on disk.
type cacheIndex struct {
	Pages []cachePage `json:"pages"`
	Info  *ComicInfo  `json:"info,omitempty"`
	Size  int64       `json:"size"`
}

type cachePage struct {
	Name string `json:"name"`
	File string `json:"file"`
}

var (
	unpackMu sync.Mutex // one unpack at a time: bounded disk churn and decoder memory
	useMu    sync.Mutex
	inUse    = map[string]int{} // cache entry folder → open archives
)

func acquire(dir string) {
	useMu.Lock()
	inUse[dir]++
	useMu.Unlock()
}

func release(dir string) {
	useMu.Lock()
	if inUse[dir]--; inUse[dir] <= 0 {
		delete(inUse, dir)
	}
	useMu.Unlock()
}

// cacheKey is sha1(path|size|mtime): a replaced file gets a new entry.
func cacheKey(p string, st os.FileInfo) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	sum := sha1.Sum(fmt.Appendf(nil, "%s|%d|%d", p, st.Size(), st.ModTime().Unix()))
	return hex.EncodeToString(sum[:])
}

// openCached opens a RAR/7z volume from its unpacked copy in cacheDir,
// unpacking it on the first open.
func openCached(p string, st os.FileInfo, cacheDir string, f format) (*Archive, error) {
	if cacheDir == "" {
		return nil, errors.New("comics: no cache dir for " + filepath.Ext(p))
	}
	dir := filepath.Join(cacheDir, cacheKey(p, st))
	acquire(dir)
	idx, err := loadIndex(dir)
	if err != nil {
		unpackMu.Lock()
		if idx, err = loadIndex(dir); err != nil {
			evict(cacheDir, declaredPages(p, f)) // make room first
			if idx, err = unpack(p, cacheDir, dir, f); err == nil {
				evict(cacheDir, 0)
			}
		}
		unpackMu.Unlock()
	}
	if err != nil {
		release(dir)
		return nil, err
	}
	now := time.Now()
	_ = os.Chtimes(dir, now, now) // LRU: last opened
	a := &Archive{release: func() { release(dir) }}
	if idx.Info != nil {
		a.info, a.hasInfo = *idx.Info, true
	}
	for _, pg := range idx.Pages {
		a.pages = append(a.pages, pg.Name)
		a.files = append(a.files, filepath.Join(dir, pg.File))
	}
	return a, nil
}

// loadIndex reads a complete cache entry (an entry is renamed into place
// only when fully written).
func loadIndex(dir string) (cacheIndex, error) {
	var idx cacheIndex
	f, err := os.Open(filepath.Join(dir, indexName))
	if err != nil {
		return idx, err
	}
	defer f.Close()
	b, err := readCapped(f, 16<<20)
	if err != nil {
		return idx, err
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		return idx, err
	}
	for _, pg := range idx.Pages {
		if pg.File == "" || pg.File != filepath.Base(pg.File) || strings.HasPrefix(pg.File, ".") {
			return idx, fmt.Errorf("comics: bad cache entry %s", dir)
		}
	}
	return idx, nil
}

// unpack writes the pages and ComicInfo of an archive into a temporary
// folder, then renames it to dir. Over a limit nothing is left behind.
func unpack(p, cacheDir, dir string, f format) (idx cacheIndex, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("comics: %s: panic: %v", p, r)
		}
	}()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return idx, err
	}
	tmp, err := os.MkdirTemp(cacheDir, tmpPrefix+filepath.Base(dir)+"-")
	if err != nil {
		return idx, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()
	limit := min(maxExtractBytes, cacheLimit)
	infoDepth := -1
	err = walkArchive(p, f, func(e entry) error {
		switch {
		case isPage(e.name):
			if len(idx.Pages) >= maxPages {
				return errTooMany
			}
			if e.size > maxPageBytes || (e.size > 0 && idx.Size+e.size > limit) {
				return errTooBig
			}
			file := fmt.Sprintf("%05d%s", len(idx.Pages), strings.ToLower(path.Ext(e.name)))
			n, err := writeEntry(e, filepath.Join(tmp, file), min(maxPageBytes, limit-idx.Size))
			if err != nil {
				return err
			}
			idx.Size += n
			idx.Pages = append(idx.Pages, cachePage{Name: e.name, File: file})
		case isInfo(e.name) && (infoDepth < 0 || strings.Count(e.name, "/") < infoDepth):
			if b, err := readEntry(e, maxInfoBytes); err == nil {
				if ci, err := parseComicInfo(b); err == nil {
					idx.Info, infoDepth = &ci, strings.Count(e.name, "/")
				}
			}
		}
		return nil
	})
	if err != nil {
		return idx, err
	}
	sort.SliceStable(idx.Pages, func(i, j int) bool { return natsort.Less(idx.Pages[i].Name, idx.Pages[j].Name) })
	b, err := json.Marshal(idx)
	if err != nil {
		return idx, err
	}
	if err := os.WriteFile(filepath.Join(tmp, indexName), b, 0o644); err != nil {
		return idx, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if other, lerr := loadIndex(dir); lerr == nil { // another process won
			os.RemoveAll(tmp)
			return other, nil
		}
		return idx, err
	}
	return idx, nil
}

// writeEntry copies an entry to a new file, at most limit bytes.
func writeEntry(e entry, dst string, limit int64) (int64, error) {
	rc, err := e.open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(rc, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = errTooBig
	}
	return n, err
}

// evict removes the least recently opened entries until the cache plus
// reserve (the pages about to be unpacked) fits cacheLimit, and leftovers
// of interrupted unpacks. Entries in use stay: an entry is taken out of
// use-checking and renamed away atomically (removeUnused), so an open that
// races with eviction either keeps it or unpacks it again.
func evict(cacheDir string, reserve int64) {
	es, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	type ent struct {
		dir   string
		size  int64
		mtime time.Time
	}
	var all []ent
	var total int64
	for _, e := range es {
		dir := filepath.Join(cacheDir, e.Name())
		info, err := e.Info()
		if err != nil || !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), tmpPrefix) {
			if time.Since(info.ModTime()) > staleTmp {
				os.RemoveAll(dir)
			}
			continue
		}
		idx, err := loadIndex(dir)
		if err != nil {
			removeUnused(dir) // not a complete entry
			continue
		}
		all = append(all, ent{dir, idx.Size, info.ModTime()})
		total += idx.Size
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mtime.Before(all[j].mtime) })
	for _, e := range all {
		if total+reserve <= cacheLimit {
			break
		}
		if removeUnused(e.dir) {
			total -= e.size
		}
	}
}

// removeUnused deletes a cache entry unless an archive holds it. The
// check and the rename away happen under useMu, the same lock acquire
// takes, so no open can start using the entry in between.
func removeUnused(dir string) bool {
	useMu.Lock()
	if inUse[dir] > 0 {
		useMu.Unlock()
		return false
	}
	gone := filepath.Join(filepath.Dir(dir), tmpPrefix+"evict-"+filepath.Base(dir)+fmt.Sprintf("-%d", time.Now().UnixNano()))
	err := os.Rename(dir, gone)
	useMu.Unlock()
	if err != nil {
		return false
	}
	return os.RemoveAll(gone) == nil
}

// declaredPages is the declared size of an archive's pages (0 when the
// listing fails or sizes are unknown): the room evict makes first.
func declaredPages(p string, f format) int64 {
	entries, closeFn, err := listArchive(p, f)
	if err != nil {
		return 0
	}
	defer closeFn()
	var n int64
	for _, e := range entries {
		if isPage(e.name) && e.size > 0 {
			n = min(n+e.size, 1<<62)
		}
	}
	return min(n, min(maxExtractBytes, cacheLimit))
}
