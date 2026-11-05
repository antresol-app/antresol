package files

import (
	"cmp"
	"context"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// breakdownMax bounds the entries of one breakdown; the rest are summed.
const breakdownMax = 100

// UsageEntry is one entry of a category's breakdown: a first-level child of
// the category root (for «other», a path below the media root). Name is
// relative: absolute server paths never leave the server.
type UsageEntry struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Dir   bool   `json:"dir"`
}

// UsageMore sums the entries cut off a breakdown.
type UsageMore struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// UsageBreakdown is what a disk category is made of, largest first.
type UsageBreakdown struct {
	Category string       `json:"category"`
	Pending  bool         `json:"pending"` // «other» is still being walked: ask again
	Bytes    int64        `json:"bytes"`   // the category's total from the report
	Entries  []UsageEntry `json:"entries"`
	More     *UsageMore   `json:"more,omitempty"`
	// Rest (other only): what no entry explains (system files, file-system
	// overhead, what lives outside the media root).
	Rest int64 `json:"rest,omitempty"`
	// Partial (other only): the walk ran out of time; the entries are what
	// it counted by then.
	Partial bool `json:"partial,omitempty"`
}

// otherSizes is the «other» walk done against snap.
type otherSizes struct {
	snap    *usageSnap
	at      time.Time
	entries []UsageEntry
	sum     int64
	// partial: the walk ran out of time; what it counted is kept
	partial bool
}

// otherWalkTimeout bounds one «other» walk; what it counted by then is kept.
const otherWalkTimeout = 10 * time.Minute

// entrySums adds file sizes up by entry.
type entrySums map[string]*UsageEntry

// under returns the add callback of walkFiles for files below root: name
// maps the path relative to root to its entry (nil: its first component).
func (e entrySums) under(root string, name func(rel string) (string, bool)) func(p string, n int64) {
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if name == nil {
		name = func(rel string) (string, bool) {
			if i := strings.IndexByte(rel, filepath.Separator); i >= 0 {
				return rel[:i], true
			}
			return rel, false
		}
	}
	return func(p string, n int64) {
		rel, ok := strings.CutPrefix(p, prefix)
		if !ok || n == 0 {
			return
		}
		k, dir := name(rel)
		if x := e[k]; x != nil {
			x.Bytes += n
		} else {
			e[k] = &UsageEntry{Name: k, Bytes: n, Dir: dir}
		}
	}
}

// sorted lists the entries by size, largest first (ties by name).
func (e entrySums) sorted() []UsageEntry {
	out := make([]UsageEntry, 0, len(e))
	for _, x := range e {
		out = append(out, *x)
	}
	slices.SortFunc(out, func(a, b UsageEntry) int {
		if c := cmp.Compare(b.Bytes, a.Bytes); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// Breakdown lists what a category of the report is made of (id is a usage
// category or "other"; one without a root is ErrUnknownRoot). A category's
// entries come with the cached report; «other» needs a walk of the rest of
// the disk, which runs in the background: Pending says to ask again. A
// report without the disk (NoDisk) has no breakdown.
func (u *Usage) Breakdown(ctx context.Context, id string) (UsageBreakdown, error) {
	if u.NoDisk {
		return UsageBreakdown{}, ErrUnknownRoot
	}
	if id != "other" {
		if !slices.Contains(usageCategories, id) {
			return UsageBreakdown{}, ErrUnknownRoot
		}
		if _, err := u.Sandbox.Root(id); err != nil {
			return UsageBreakdown{}, ErrUnknownRoot
		}
	}
	s, err := u.snapshot(ctx)
	if err != nil {
		return UsageBreakdown{}, err
	}
	b := UsageBreakdown{Category: id, Entries: []UsageEntry{}}
	for _, c := range s.report.Categories {
		if c.ID == id {
			b.Bytes = c.Bytes
		}
	}
	all := s.entries[id]
	if id == "other" {
		// pending with entries: the last result while a fresh walk runs
		o, pending := u.otherOf(s)
		b.Pending = pending
		if o == nil {
			return b, nil
		}
		all = o.entries
		b.Partial = o.partial
		b.Rest = max(b.Bytes-o.sum, 0)
	}
	if len(all) > breakdownMax {
		m := &UsageMore{Count: len(all) - breakdownMax}
		for _, x := range all[breakdownMax:] {
			m.Bytes += x.Bytes
		}
		b.More, all = m, all[:breakdownMax]
	}
	b.Entries = append(b.Entries, all...)
	return b, nil
}

// otherOf returns the «other» breakdown of s when it is done; otherwise it
// starts its walk (one at a time) and returns false.
func (u *Usage) otherOf(s *usageSnap) (o *otherSizes, pending bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	o = u.other
	// done for this report — a walk that ran out of time too: it is not
	// started again before the report's TTL
	if o != nil && o.snap == s && time.Since(o.at) < usageTTL {
		return o, false
	}
	if !u.otherRunning {
		u.otherRunning = true
		go u.runOther(s)
	}
	// the last result (or nothing yet) while the walk runs
	return o, true
}

// runOther walks «other» for s, one at a time, in a slot of the folder-size
// walks (it competes with them, playback and downloads for the disk), and
// keeps what it counted even when it runs out of time.
func (u *Usage) runOther(s *usageSnap) {
	sem := u.sem()
	sem <- struct{}{}
	defer func() { <-sem }()
	timeout := u.otherTimeout
	if timeout == 0 {
		timeout = otherWalkTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	o := u.walkOther(ctx, s)
	u.mu.Lock()
	u.other = o
	u.otherRunning = false
	u.mu.Unlock()
}

// walkOther walks the media root around the category roots (and other
// disks mounted below it). A hardlinked file the report counted is 0 here.
// A file's bytes go to the shortest path below the media root that is not
// on the way to a category root: media/kids-old beside media/movies,
// backup, .vault-trash, media/readme.txt.
func (u *Usage) walkOther(ctx context.Context, s *usageSnap) *otherSizes {
	if u.otherHook != nil {
		u.otherHook(ctx)
	}
	mr := u.Sandbox.MediaRoot()
	cats := map[string]bool{}
	ancestors := map[string]bool{} // relative to mr
	for _, id := range usageCategories {
		root, err := u.Sandbox.Root(id)
		if err != nil {
			continue
		}
		cats[root.Dir] = true
		rel, err := filepath.Rel(mr, root.Dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		for d := filepath.Dir(rel); d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
			ancestors[d] = true
		}
	}
	dev, devKnown := deviceOf(mr)
	skip := func(p string, d fs.DirEntry) bool {
		if cats[p] {
			return true
		}
		if !devKnown {
			return false
		}
		fi, err := d.Info()
		if err != nil {
			return false
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		return ok && uint64(st.Dev) != dev
	}
	name := func(rel string) (string, bool) {
		for i := 0; ; {
			j := strings.IndexByte(rel[i:], filepath.Separator)
			if j < 0 {
				return rel, false
			}
			if !ancestors[rel[:i+j]] {
				return rel[:i+j], true
			}
			i += j + 1
		}
	}
	acc := entrySums{}
	if !cats[mr] {
		walkFiles(ctx, mr, maps.Clone(s.seen), skip, acc.under(mr, name))
	}
	o := &otherSizes{snap: s, at: time.Now(), entries: acc.sorted(), partial: ctx.Err() != nil}
	for _, x := range o.entries {
		o.sum += x.Bytes
	}
	return o
}

// deviceOf is the device a path lives on.
func deviceOf(p string) (uint64, bool) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}
