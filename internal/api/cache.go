package api

import "sync"

// boundedCache is a map under a mutex that holds a limited number of
// entries (the limit is given at each write): adding a new key to a full
// one drops one other entry, not the whole cache. The zero value is ready.
// Values that need a check on the way out (a TTL, a stored size) are
// checked by the user of get.
type boundedCache[K comparable, V any] struct {
	mu sync.Mutex
	m  map[K]V
}

func (c *boundedCache[K, V]) get(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok
}

func (c *boundedCache[K, V]) put(k K, v V, limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.room(k, limit, nil)
	c.m[k] = v
}

// room readies the map for a write of k (the caller holds mu): a new key
// in a cache of limit entries drops one entry for which pinned is false
// (nil: any).
func (c *boundedCache[K, V]) room(k K, limit int, pinned func(V) bool) {
	if c.m == nil {
		c.m = map[K]V{}
		return
	}
	if _, ok := c.m[k]; ok || len(c.m) < limit {
		return
	}
	for old, v := range c.m {
		if pinned == nil || !pinned(v) {
			delete(c.m, old)
			return
		}
	}
}
