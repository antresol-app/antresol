package mediaimport

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

// screenCache keeps the «Переезд» screen's last look at the old services
// (Discover, Leftovers, Users): each look copies their databases, so it is
// reused while every file it read is unchanged (path, size, mtime) and
// dropped when a run starts («Проверить» looks afresh).
type screenCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	key string
	val any
}

func (c *screenCache) get(name, key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[name]
	if !ok || e.key != key {
		return nil, false
	}
	return e.val, true
}

func (c *screenCache) put(name, key string, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cacheEntry{}
	}
	c.entries[name] = cacheEntry{key: key, val: v}
}

func (c *screenCache) forget() {
	c.mu.Lock()
	c.entries = nil
	c.mu.Unlock()
}

// fingerprint names the state of the files at paths: size and mtime of
// each, of a SQLite database's -wal file (its recent writes) and of a
// folder itself (entries added or removed).
func fingerprint(paths ...string) string {
	var b strings.Builder
	for _, p := range paths {
		for _, f := range []string{p, p + "-wal"} {
			b.WriteString(f)
			if st, err := os.Stat(f); err == nil {
				b.WriteString("|" + strconv.FormatInt(st.Size(), 10) + "|" + strconv.FormatInt(st.ModTime().UnixNano(), 10))
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}
