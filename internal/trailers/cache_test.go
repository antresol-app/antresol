package trailers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeFetcher copies testdata/trailer.mp4 to dst; block (when set) holds
// each fetch until it is closed; fail makes every fetch fail.
type fakeFetcher struct {
	runs  atomic.Int32
	block chan struct{}
	fail  bool
	mu    sync.Mutex
	keys  []string
}

func (f *fakeFetcher) Fetch(ctx context.Context, key, dst string) error {
	f.runs.Add(1)
	f.mu.Lock()
	f.keys = append(f.keys, key)
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.fail {
		return errors.New("fake failure")
	}
	b, err := os.ReadFile("testdata/trailer.mp4")
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

const (
	keyA = "aaaaaaaaaaa"
	keyB = "bbbbbbbbbbb"
	keyC = "ccccccccccc"
)

func newCache(t *testing.T, f Fetcher, mut func(*Config)) *Cache {
	cfg := Config{Dir: t.TempDir(), Fetcher: f, SlotWait: 50 * time.Millisecond}
	if mut != nil {
		mut(&cfg)
	}
	c := New(cfg)
	if c == nil {
		t.Fatal("no cache")
	}
	return c
}

func TestNewWithoutFetcher(t *testing.T) {
	if New(Config{Dir: t.TempDir()}) != nil {
		t.Fatal("a cache without a fetcher")
	}
}

func TestValidKey(t *testing.T) {
	for k, ok := range map[string]bool{keyA: true, "dQw4w9WgXc_": true, "a-b_c-d_e-f": true,
		"": false, "short": false, "twelvechars1": false, "../../etc/p": false, "aaaaaaaaaa/": false, "aaaa aaaaaa": false} {
		if ValidKey(k) != ok {
			t.Errorf("ValidKey(%q) = %v", k, !ok)
		}
	}
}

func TestEnsureFetchesOnceThenHits(t *testing.T) {
	f := &fakeFetcher{}
	c := newCache(t, f, nil)
	p, dur, err := c.Ensure(context.Background(), keyA)
	if err != nil {
		t.Fatal(err)
	}
	if dur < 1900 || dur > 2100 {
		t.Fatalf("duration %d", dur)
	}
	if p != filepath.Join(c.cfg.Dir, keyA+".mp4") || !c.Cached(keyA) {
		t.Fatalf("path %s", p)
	}
	if _, _, err := c.Ensure(context.Background(), keyA); err != nil || f.runs.Load() != 1 {
		t.Fatalf("hit: %v, runs %d", err, f.runs.Load())
	}
	// no temp leftovers
	if ents, _ := os.ReadDir(filepath.Join(c.cfg.Dir, ".tmp")); len(ents) != 0 {
		t.Fatalf("temp left: %v", ents)
	}
}

func TestEnsureSingleflight(t *testing.T) {
	f := &fakeFetcher{block: make(chan struct{})}
	c := newCache(t, f, func(c *Config) { c.SlotWait = time.Second })
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := c.Ensure(context.Background(), keyA)
			errs <- err
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(f.block)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := f.runs.Load(); n != 1 {
		t.Fatalf("runs %d", n)
	}
}

func TestEnsureBusyAndWaitTimeout(t *testing.T) {
	f := &fakeFetcher{block: make(chan struct{})}
	c := newCache(t, f, nil)
	// two slots taken; the waits give up (still fetching)
	for _, k := range []string{keyA, keyB} {
		short, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, _, err := c.Ensure(short, k)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s: %v", k, err)
		}
	}
	for deadline := time.Now().Add(2 * time.Second); len(c.sem) < 2 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if len(c.sem) != 2 {
		t.Fatalf("slots taken: %d", len(c.sem))
	}
	if _, _, err := c.Ensure(context.Background(), keyC); !errors.Is(err, ErrBusy) {
		t.Fatalf("third: %v", err)
	}
	close(f.block)
	// the first two finish for the next play
	deadline := time.Now().Add(2 * time.Second)
	for !(c.Cached(keyA) && c.Cached(keyB)) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !c.Cached(keyA) || !c.Cached(keyB) {
		t.Fatal("background fetches did not finish")
	}
}

func TestEnsureFailure(t *testing.T) {
	f := &fakeFetcher{fail: true}
	c := newCache(t, f, nil)
	if _, _, err := c.Ensure(context.Background(), keyA); !errors.Is(err, ErrFetch) {
		t.Fatalf("%v", err)
	}
	if c.Cached(keyA) {
		t.Fatal("cached after a failure")
	}
	// a play tries again
	if _, _, err := c.Ensure(context.Background(), keyA); !errors.Is(err, ErrFetch) || f.runs.Load() != 2 {
		t.Fatalf("%v runs %d", err, f.runs.Load())
	}
}

// notMP4 writes something that is not an MP4.
type notMP4 struct{}

func (notMP4) Fetch(_ context.Context, _, dst string) error {
	return os.WriteFile(dst, []byte("<html>"), 0o644)
}

func TestEnsureRefusesNonMP4(t *testing.T) {
	c := newCache(t, notMP4{}, nil)
	if _, _, err := c.Ensure(context.Background(), keyA); !errors.Is(err, ErrFetch) || c.Cached(keyA) {
		t.Fatalf("%v", err)
	}
}

func TestPrefetchOnceAndFailureMemo(t *testing.T) {
	f := &fakeFetcher{}
	c := newCache(t, f, nil)
	c.Prefetch(keyA)
	c.Wait()
	c.Prefetch(keyA)
	c.Wait()
	if !c.Cached(keyA) || f.runs.Load() != 1 {
		t.Fatalf("runs %d", f.runs.Load())
	}
	// a failed key is not prefetched again the same day
	ff := &fakeFetcher{fail: true}
	c2 := newCache(t, ff, nil)
	c2.Prefetch(keyB)
	c2.Wait()
	c2.Prefetch(keyB)
	c2.Wait()
	if ff.runs.Load() != 1 {
		t.Fatalf("failed key prefetched %d times", ff.runs.Load())
	}
}

func TestPrefetchOneAtATimeAndOnlyFreeSlot(t *testing.T) {
	f := &fakeFetcher{block: make(chan struct{})}
	c := newCache(t, f, nil)
	c.Prefetch(keyA)
	time.Sleep(50 * time.Millisecond)
	c.Prefetch(keyB) // one is running: dropped
	close(f.block)
	c.Wait()
	if f.runs.Load() != 1 || c.Cached(keyB) {
		t.Fatalf("runs %d", f.runs.Load())
	}
}

func TestEvictOverCap(t *testing.T) {
	f := &fakeFetcher{}
	size, _ := os.Stat("testdata/trailer.mp4")
	// room for two trailers
	c := newCache(t, f, func(c *Config) { c.MaxBytes = 2*size.Size() + 10 })
	ctx := context.Background()
	for i, k := range []string{keyA, keyB} {
		if _, _, err := c.Ensure(ctx, k); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(time.Duration(i-10) * time.Hour)
		os.Chtimes(c.path(k), old, old)
	}
	c.Played(keyA) // A played now: B is the least recently played
	if _, _, err := c.Ensure(ctx, keyC); err != nil {
		t.Fatal(err)
	}
	if !c.Cached(keyA) || c.Cached(keyB) || !c.Cached(keyC) {
		t.Fatalf("A %v B %v C %v", c.Cached(keyA), c.Cached(keyB), c.Cached(keyC))
	}
}

func TestCleanupMaxAge(t *testing.T) {
	c := newCache(t, &fakeFetcher{}, nil)
	ctx := context.Background()
	for _, k := range []string{keyA, keyB} {
		if _, _, err := c.Ensure(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-31 * 24 * time.Hour)
	os.Chtimes(c.path(keyA), old, old)
	stale := filepath.Join(c.cfg.Dir, ".tmp", keyC+"-dead")
	os.MkdirAll(stale, 0o755)
	os.Chtimes(stale, old, old)
	c.Cleanup()
	if c.Cached(keyA) || !c.Cached(keyB) {
		t.Fatalf("A %v B %v", c.Cached(keyA), c.Cached(keyB))
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale temp dir kept")
	}
}

func TestListedPersistsAndExpires(t *testing.T) {
	dir := t.TempDir()
	c := New(Config{Dir: dir, Fetcher: &fakeFetcher{}})
	c.Listed(7, []string{keyA, "bad key", keyB})
	c.Listed(9, []string{keyA})
	if got := c.ItemsOf(keyA); len(got) != 2 || got[0] != 7 || got[1] != 9 {
		t.Fatalf("%v", got)
	}
	if c.ItemsOf(keyC) != nil || c.ItemsOf("bad key") != nil {
		t.Fatal("unlisted key has items")
	}
	// a restart keeps them
	c2 := New(Config{Dir: dir, Fetcher: &fakeFetcher{}})
	if got := c2.ItemsOf(keyB); len(got) != 1 || got[0] != 7 {
		t.Fatalf("after restart %v", got)
	}
	// too long ago: gone
	c2.mu.Lock()
	c2.listed[keyB].At = time.Now().Add(-31 * 24 * time.Hour).Unix()
	c2.mu.Unlock()
	if c2.ItemsOf(keyB) != nil {
		t.Fatal("expired listing")
	}
}

func TestDuration(t *testing.T) {
	if d := Duration("testdata/trailer.mp4"); d < 1900 || d > 2100 {
		t.Fatalf("%d", d)
	}
	if Duration("cache.go") != 0 || Duration("missing") != 0 {
		t.Fatal("non-MP4 has a duration")
	}
}
