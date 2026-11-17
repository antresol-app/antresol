package trailers

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// listedFor: how long a listed key stays playable without being listed
// again (TMDB's list is cached a day, the apps' an hour).
const listedFor = 30 * 24 * time.Hour

// Listed are the trailer keys the server listed, by the items whose
// trailers listed them (kid and rating gating), kept in a file so a
// restart keeps them.
type Listed struct {
	path string // "": memory only
	now  func() time.Time

	mu sync.Mutex
	m  map[string]*listing
}

// listing is a key the server listed, for which items, and when last.
type listing struct {
	Items []int64 `json:"items"`
	At    int64   `json:"at"`
}

// NewListed reads the keys kept in path ("" keeps them in memory only).
func NewListed(path string) *Listed {
	l := &Listed{path: path, m: map[string]*listing{}}
	if path == "" {
		return l
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return l
	}
	var m map[string]*listing
	if json.Unmarshal(b, &m) != nil {
		return l
	}
	cut := l.clock().Add(-listedFor).Unix()
	for k, x := range m {
		if ValidKey(k) && x != nil && x.At >= cut {
			l.m[k] = x
		}
	}
	return l
}

func (l *Listed) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// Add records that the trailers of itemID listed keys: only these keys
// are played, for a profile that may see one of their items.
func (l *Listed) Add(itemID int64, keys []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	cut := now.Add(-listedFor).Unix()
	changed := false
	for k, x := range l.m {
		if x.At < cut {
			delete(l.m, k)
			changed = true
		}
	}
	for _, k := range keys {
		if !ValidKey(k) {
			continue
		}
		x := l.m[k]
		if x == nil {
			x = &listing{}
			l.m[k] = x
		}
		if !containsID(x.Items, itemID) {
			x.Items = append(x.Items, itemID)
			changed = true
		}
		// written again at most daily for a key seen often
		if now.Unix()-x.At > int64((24 * time.Hour).Seconds()) {
			changed = true
		}
		x.At = now.Unix()
	}
	if changed {
		l.save()
	}
}

// ItemsOf are the items whose trailers listed key; nil: never listed (or
// too long ago).
func (l *Listed) ItemsOf(key string) []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	x := l.m[key]
	if x == nil || x.At < l.clock().Add(-listedFor).Unix() {
		return nil
	}
	return append([]int64(nil), x.Items...)
}

// save writes the keys (l.mu held).
func (l *Listed) save() {
	if l.path == "" {
		return
	}
	b, err := json.Marshal(l.m)
	if err != nil {
		return
	}
	if err := writeFileAtomic(l.path, b); err != nil {
		log.Printf("trailers: save listed keys: %v", err)
	}
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func writeFileAtomic(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
