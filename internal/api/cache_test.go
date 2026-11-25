package api

import (
	"testing"
	"time"
)

func TestBoundedCache(t *testing.T) {
	var c boundedCache[int, string]
	if _, ok := c.get(1); ok {
		t.Fatal("a zero cache has nothing")
	}
	for i := 0; i < 3; i++ {
		c.put(i, "v", 3)
	}
	c.put(1, "again", 3) // an update drops nothing
	if len(c.m) != 3 || c.m[1] != "again" {
		t.Fatalf("%v", c.m)
	}
	c.put(9, "new", 3) // a new key in a full cache drops one other, not all
	if v, ok := c.get(9); len(c.m) != 3 || !ok || v != "new" {
		t.Fatalf("%v", c.m)
	}
	for i := 10; i < 40; i++ {
		c.put(i, "x", 3)
		if len(c.m) != 3 {
			t.Fatalf("grew to %d", len(c.m))
		}
	}
}

// room never drops a pinned entry (a running progress call), whichever the
// map's order is.
func TestBoundedCacheRoomKeepsPinned(t *testing.T) {
	for range 50 {
		var c boundedCache[int, bool]
		c.m = map[int]bool{1: true, 2: false, 3: true}
		c.mu.Lock()
		c.room(4, 3, func(pinned bool) bool { return pinned })
		c.mu.Unlock()
		if _, ok := c.m[2]; ok || !c.m[1] || !c.m[3] || len(c.m) != 2 {
			t.Fatalf("%v", c.m)
		}
	}
	var c boundedCache[int, bool]
	c.m = map[int]bool{1: true}
	c.mu.Lock()
	c.room(2, 1, func(pinned bool) bool { return pinned })
	c.mu.Unlock()
	if len(c.m) != 1 {
		t.Fatalf("all pinned: %v", c.m) // grows past the limit rather than drop a running call
	}
}

func TestKeyTimesTakeAndBound(t *testing.T) {
	var f keyTimes
	now := time.Now()
	if !f.take("a", now, time.Minute, false) || f.take("a", now.Add(time.Second), time.Minute, false) {
		t.Fatal("a take within every is false")
	}
	if !f.take("a", now.Add(2*time.Minute), time.Minute, false) || !f.take("a", now, 0, true) {
		t.Fatal("a later take, or a forced one, is true")
	}
	for i := 0; i < 3*probeCacheMax; i++ {
		f.putAt(string(rune('A'+i%26))+string(rune(i)), now)
	}
	if len(f.m) > probeCacheMax {
		t.Fatalf("grew to %d", len(f.m))
	}
}
