package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUsageCountsHardlinksOnce(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, "torrents/x.mkv"), 4096)
	if err := os.Link(filepath.Join(mr, "torrents/x.mkv"), filepath.Join(mr, "media/movies/x.mkv")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(mr, "media/series/e.mkv"), 1000)
	u := &Usage{Sandbox: s}
	r, err := u.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Total <= 0 || r.Used <= 0 || r.Free <= 0 {
		t.Fatalf("%+v", r)
	}
	got := map[string]int64{}
	for _, c := range r.Categories {
		got[c.ID] = c.Bytes
	}
	if got["movies"] != 4096 || got["downloads"] != 0 || got["series"] != 1000 {
		t.Fatalf("categories %+v", r.Categories)
	}

	dir := filepath.Join(mr, "media")
	if _, ok := u.DirSize(dir); ok {
		t.Fatal("first call must compute in background")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, ok := u.DirSize(dir); ok {
			if n != 5096 {
				t.Fatalf("dir size %d", n)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dir size never computed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A library-only server: no disk figures, no downloads or stream cache
// roots, only the library categories (no «other»).
func TestUsageNoDisk(t *testing.T) {
	full, mr := testSandbox(t)
	write(t, filepath.Join(mr, "media/movies/x.mkv"), 4096)
	write(t, filepath.Join(mr, "torrents/t.mkv"), 1000)
	s := NewSandbox(mr, LibraryOnlyRoots(full.Roots()))
	for _, id := range []string{"downloads", "stream-cache", "disk"} {
		if _, err := s.Root(id); err == nil {
			t.Fatalf("%s kept", id)
		}
	}
	if _, err := s.Root("movies"); err != nil {
		t.Fatal("library roots must stay")
	}
	r, err := (&Usage{Sandbox: s, NoDisk: true}).Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 0 || r.Used != 0 || r.Free != 0 {
		t.Fatalf("disk figures: %+v", r)
	}
	got := map[string]int64{}
	for _, c := range r.Categories {
		got[c.ID] = c.Bytes
	}
	if len(got) != 2 || got["movies"] != 4096 {
		t.Fatalf("categories %+v, want movies and series only", r.Categories)
	}
}

// Folder-size walks are bounded: at most dirSizeConcurrency run at once,
// the rest report pending without starting a walk, and evicting the cache
// never forgets running walks (which would start duplicates).
func TestDirSizeBoundedWalks(t *testing.T) {
	s, mr := testSandbox(t)
	release := make(chan struct{})
	var mu sync.Mutex
	active, peak, started := 0, 0, map[string]int{}
	u := &Usage{Sandbox: s, walk: func(ctx context.Context, dir string) int64 {
		mu.Lock()
		active++
		peak = max(peak, active)
		started[dir]++
		mu.Unlock()
		<-release
		mu.Lock()
		active--
		mu.Unlock()
		return 7
	}}
	var dirs []string
	for i := range 20 {
		dirs = append(dirs, filepath.Join(mr, "media", fmt.Sprint("d", i)))
	}
	for range 3 {
		for _, d := range dirs {
			if _, ok := u.DirSize(d); ok {
				t.Fatal("size before any walk finished")
			}
		}
	}
	// Overflow the cache with finished entries: running ones must survive.
	u.dmu.Lock()
	for i := range 10001 {
		u.dirs[fmt.Sprint("/done/", i)] = dirSize{size: 1, at: time.Now(), done: true}
	}
	u.dmu.Unlock()
	u.DirSize(filepath.Join(mr, "media/fresh"))
	for _, d := range dirs {
		u.DirSize(d)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if peak > dirSizeConcurrency {
		t.Errorf("peak concurrent walks %d > %d", peak, dirSizeConcurrency)
	}
	for d, n := range started {
		if n > 1 {
			t.Errorf("%s walked %d times concurrently", d, n)
		}
	}
	mu.Unlock()
	u.dmu.Lock()
	if len(u.dirs) > 10001 {
		t.Errorf("cache not bounded: %d", len(u.dirs))
	}
	u.dmu.Unlock()
	close(release)
	// Once the running walks finish, the queued folders get their turn.
	deadline := time.Now().Add(5 * time.Second)
	for _, d := range dirs {
		for {
			if n, ok := u.DirSize(d); ok {
				if n != 7 {
					t.Fatalf("%s: %d", d, n)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never computed", d)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}
