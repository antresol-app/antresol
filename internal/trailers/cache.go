// Package trailers plays a title's trailers inside Antresol: a Fetcher
// registered by the build puts a trailer that TMDB listed (GET
// /api/items/{id}/trailers) into a small MP4 cache, and the apps play that
// file in their own player. Only keys the server listed itself are
// fetched: the server is never an open video download proxy. Without a
// Fetcher there is no cache, and the apps play trailers their own way.
package trailers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// Fetcher writes the trailer of key as an MP4 (H.264 + AAC, what every
// client plays without a transcode) to dst, a path in a temp directory of
// its own. It runs at the lowest priority and stops with ctx.
type Fetcher interface {
	Fetch(ctx context.Context, key, dst string) error
}

const (
	// DefaultMaxBytes is the cache's total size: above it the trailers
	// played longest ago go first.
	DefaultMaxBytes = 3 << 30
	// DefaultMaxAge: a trailer nobody played for this long is removed.
	DefaultMaxAge = 30 * 24 * time.Hour
	// fetchSlots: fetches at once, plays and the prefetch together.
	fetchSlots = 2
	// defaultSlotWait: how long a play waits for a slot before it is busy.
	defaultSlotWait = 2 * time.Second
	// fetchLimit bounds one fetch (a slow network, a stuck merge).
	fetchLimit = 3 * time.Minute
	// retryAfterFailure: a key whose fetch failed is not prefetched again
	// for this long (a play still tries).
	retryAfterFailure = 24 * time.Hour
	// listedFor: how long a listed key stays playable without being listed
	// again (TMDB's list is cached a day, the apps' an hour).
	listedFor = 30 * 24 * time.Hour
	// tmpAge: a temp directory older than this is a fetch that died.
	tmpAge = time.Hour
)

// keyRe: the video site's id of a trailer, as TMDB lists it.
var keyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ValidKey says whether key looks like a trailer's id.
func ValidKey(key string) bool { return keyRe.MatchString(key) }

var (
	// ErrBusy: every fetch slot stayed busy (503, try again soon).
	ErrBusy = errors.New("trailers: every fetch slot busy")
	// ErrFetch: the fetcher could not fetch the trailer (502).
	ErrFetch = errors.New("trailers: fetch failed")
	// ErrNotCached: no cached file for the key.
	ErrNotCached = errors.New("trailers: not cached")
)

// Config sets up a Cache.
type Config struct {
	// Dir is the cache (DataDir/trailers).
	Dir string
	// Fetcher gets a trailer that is not cached.
	Fetcher Fetcher
	// MaxBytes and MaxAge bound the cache (0: the defaults).
	MaxBytes int64
	MaxAge   time.Duration
	// SlotWait is how long a play waits for a fetch slot (0: 2 s).
	SlotWait time.Duration
}

// Cache is the trailer cache.
type Cache struct {
	cfg Config

	sem     chan struct{}
	flight  singleflight.Group
	prefOn  atomic.Bool // a prefetch is running
	failed  sync.Map    // key → time.Time of the last failed fetch
	evictMu sync.Mutex
	bg      sync.WaitGroup

	mu     sync.Mutex
	listed map[string]*listing
}

// listing is a key the server listed, for which items (kid gating), and
// when last.
type listing struct {
	Items []int64 `json:"items"`
	At    int64   `json:"at"`
}

// New makes the cache; nil without a Fetcher or a Dir (no trailers to
// play here).
func New(cfg Config) *Cache {
	if cfg.Fetcher == nil || cfg.Dir == "" {
		return nil
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultMaxBytes
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = DefaultMaxAge
	}
	if cfg.SlotWait <= 0 {
		cfg.SlotWait = defaultSlotWait
	}
	c := &Cache{cfg: cfg, sem: make(chan struct{}, fetchSlots), listed: map[string]*listing{}}
	c.loadListed()
	return c
}

func (c *Cache) path(key string) string { return filepath.Join(c.cfg.Dir, key+".mp4") }

// --- listed keys ---

func (c *Cache) listedFile() string { return filepath.Join(c.cfg.Dir, "listed.json") }

func (c *Cache) loadListed() {
	b, err := os.ReadFile(c.listedFile())
	if err != nil {
		return
	}
	var m map[string]*listing
	if json.Unmarshal(b, &m) != nil {
		return
	}
	cut := time.Now().Add(-listedFor).Unix()
	for k, l := range m {
		if ValidKey(k) && l != nil && l.At >= cut {
			c.listed[k] = l
		}
	}
}

// saveListed writes the listed keys (c.mu held), so a restart keeps them.
func (c *Cache) saveListed() {
	b, err := json.Marshal(c.listed)
	if err != nil {
		return
	}
	if err := writeFileAtomic(c.listedFile(), b); err != nil {
		log.Printf("trailers: save listed keys: %v", err)
	}
}

// Listed records that the trailers of itemID listed keys: only these keys
// are fetched and played, for a profile that may see one of their items.
func (c *Cache) Listed(itemID int64, keys []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	cut := now.Add(-listedFor).Unix()
	changed := false
	for k, l := range c.listed {
		if l.At < cut {
			delete(c.listed, k)
			changed = true
		}
	}
	for _, k := range keys {
		if !ValidKey(k) {
			continue
		}
		l := c.listed[k]
		if l == nil {
			l = &listing{}
			c.listed[k] = l
		}
		if !containsID(l.Items, itemID) {
			l.Items = append(l.Items, itemID)
			changed = true
		}
		// written again at most daily for a key seen often
		if now.Unix()-l.At > int64((24 * time.Hour).Seconds()) {
			changed = true
		}
		l.At = now.Unix()
	}
	if changed {
		c.saveListed()
	}
}

// ItemsOf are the items whose trailers listed key; nil: never listed (or
// too long ago).
func (c *Cache) ItemsOf(key string) []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.listed[key]
	if l == nil || l.At < time.Now().Add(-listedFor).Unix() {
		return nil
	}
	return append([]int64(nil), l.Items...)
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// --- the cache ---

// Cached says whether key's trailer is in the cache.
func (c *Cache) Cached(key string) bool {
	st, err := os.Stat(c.path(key))
	return err == nil && st.Mode().IsRegular()
}

// Played marks key's trailer as played now (the cache drops the trailers
// played longest ago first).
func (c *Cache) Played(key string) {
	now := time.Now()
	_ = os.Chtimes(c.path(key), now, now)
}

// Open opens key's cached trailer; ErrNotCached when there is none.
func (c *Cache) Open(key string) (*os.File, os.FileInfo, error) {
	if !ValidKey(key) {
		return nil, nil, ErrNotCached
	}
	f, err := os.Open(c.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrNotCached
	}
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, nil, ErrNotCached
	}
	return f, st, nil
}

// Ensure has key's trailer in the cache, fetching it when it is not: its
// file and duration (ms, 0 unknown). The fetch outlives ctx (the next play
// finds it); ctx only bounds the wait. ErrBusy: no fetch slot; ErrFetch:
// the fetch failed; ctx's error: still fetching.
func (c *Cache) Ensure(ctx context.Context, key string) (string, int64, error) {
	if !ValidKey(key) {
		return "", 0, ErrFetch
	}
	p := c.path(key)
	if c.Cached(key) {
		return p, Duration(p), nil
	}
	ch := c.flight.DoChan(key, func() (any, error) { return nil, c.fetch(key, c.cfg.SlotWait) })
	select {
	case res := <-ch:
		if res.Err != nil {
			return "", 0, res.Err
		}
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}
	return p, Duration(p), nil
}

// Prefetch fetches key's trailer in the background when it is not cached:
// one prefetch at a time, only when a fetch slot is free, and not again for
// a day after a key failed.
func (c *Cache) Prefetch(key string) {
	if !ValidKey(key) || c.Cached(key) {
		return
	}
	if at, ok := c.failed.Load(key); ok && time.Since(at.(time.Time)) < retryAfterFailure {
		return
	}
	if !c.prefOn.CompareAndSwap(false, true) {
		return
	}
	c.bg.Add(1)
	go func() {
		defer c.bg.Done()
		defer c.prefOn.Store(false)
		_, _, _ = c.flight.Do(key, func() (any, error) { return nil, c.fetch(key, 0) })
	}()
}

// Wait waits for the background work started so far (tests).
func (c *Cache) Wait() { c.bg.Wait() }

// fetch has the Fetcher write key's trailer into a temp directory, then
// moves the MP4 into the cache. wait is how long to wait for a slot (0:
// only a free one).
func (c *Cache) fetch(key string, wait time.Duration) error {
	if c.Cached(key) {
		return nil
	}
	if wait <= 0 {
		select {
		case c.sem <- struct{}{}:
		default:
			return ErrBusy
		}
	} else {
		t := time.NewTimer(wait)
		select {
		case c.sem <- struct{}{}:
			t.Stop()
		case <-t.C:
			return ErrBusy
		}
	}
	defer func() { <-c.sem }()
	if err := c.download(key); err != nil {
		c.failed.Store(key, time.Now())
		log.Printf("trailers: fetch %s: %v", key, err)
		return ErrFetch
	}
	c.failed.Delete(key)
	c.evict(key)
	return nil
}

func (c *Cache) download(key string) error {
	tmpRoot := filepath.Join(c.cfg.Dir, ".tmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(tmpRoot, key+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(context.Background(), fetchLimit)
	defer cancel()
	out := filepath.Join(tmp, key+".mp4")
	if err := c.cfg.Fetcher.Fetch(ctx, key, out); err != nil {
		return err
	}
	if !isMP4(out) {
		return errors.New("no MP4 written")
	}
	return os.Rename(out, c.path(key))
}

// evict removes trailers unplayed for MaxAge, then the ones played longest
// ago while the cache is above MaxBytes (never keep, the one just fetched),
// and temp directories of fetches that died.
func (c *Cache) evict(keep string) {
	c.evictMu.Lock()
	defer c.evictMu.Unlock()
	ents, err := os.ReadDir(c.cfg.Dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		size int64
		mod  time.Time
	}
	var files []file
	var total int64
	now := time.Now()
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".mp4") || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(c.cfg.Dir, name)
		if now.Sub(info.ModTime()) > c.cfg.MaxAge {
			os.Remove(p)
			continue
		}
		files = append(files, file{p, info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	kept := c.path(keep)
	for _, f := range files {
		if total <= c.cfg.MaxBytes {
			break
		}
		if keep != "" && f.path == kept {
			continue
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
	if tmps, err := os.ReadDir(filepath.Join(c.cfg.Dir, ".tmp")); err == nil {
		for _, e := range tmps {
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > tmpAge {
				os.RemoveAll(filepath.Join(c.cfg.Dir, ".tmp", e.Name()))
			}
		}
	}
}

// Cleanup applies the cache's limits now.
func (c *Cache) Cleanup() { c.evict("") }

// Run cleans the cache at start and daily until ctx ends.
func (c *Cache) Run(ctx context.Context) {
	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	for {
		c.Cleanup()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
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

// LimitWriter keeps the first N bytes written to it (a tool's stderr).
type LimitWriter struct {
	W io.Writer
	N int
}

func (l *LimitWriter) Write(p []byte) (int, error) {
	if l.N > 0 {
		k := min(len(p), l.N)
		l.W.Write(p[:k])
		l.N -= k
	}
	return len(p), nil
}
