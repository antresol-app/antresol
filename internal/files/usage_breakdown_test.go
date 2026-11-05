package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func entriesOf(es []UsageEntry) string {
	var b strings.Builder
	for _, e := range es {
		fmt.Fprintf(&b, "%s=%d,%v;", e.Name, e.Bytes, e.Dir)
	}
	return b.String()
}

// The report keeps each category's first-level entries; a hardlinked file
// counts once, under the first category, so «Торренты» lists only download
// data that is not in the library.
func TestUsageBreakdownEntries(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, "media/movies/A (2021)/a.mkv"), 3000)
	write(t, filepath.Join(mr, "media/movies/B/b.mkv"), 1000)
	write(t, filepath.Join(mr, "media/movies/B/sub/b.srt"), 10)
	write(t, filepath.Join(mr, "media/movies/loose.mkv"), 500)
	write(t, filepath.Join(mr, "torrents/Linked/x.mkv"), 2000)
	if err := os.MkdirAll(filepath.Join(mr, "media/movies/C"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(mr, "torrents/Linked/x.mkv"), filepath.Join(mr, "media/movies/C/x.mkv")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(mr, "torrents/Seed/s.mkv"), 700)
	write(t, filepath.Join(mr, "torrents/single.iso"), 300)
	for i := range 103 {
		write(t, filepath.Join(mr, "media/series", fmt.Sprintf("e%03d.mkv", i)), i+1)
	}
	u := &Usage{Sandbox: s}
	ctx := context.Background()

	b, err := u.Breakdown(ctx, "movies")
	if err != nil {
		t.Fatal(err)
	}
	if got := entriesOf(b.Entries); b.Pending || b.Bytes != 6510 || b.More != nil || b.Rest != 0 ||
		got != "A (2021)=3000,true;C=2000,true;B=1010,true;loose.mkv=500,false;" {
		t.Fatalf("movies %+v %s", b, got)
	}
	b, err = u.Breakdown(ctx, "downloads")
	if err != nil {
		t.Fatal(err)
	}
	if got := entriesOf(b.Entries); b.Bytes != 1000 || got != "Seed=700,true;single.iso=300,false;" {
		t.Fatalf("downloads %+v %s", b, got)
	}
	b, err = u.Breakdown(ctx, "stream-cache")
	if err != nil || b.Entries == nil || len(b.Entries) != 0 || b.Bytes != 0 {
		t.Fatalf("stream-cache %+v %v", b, err)
	}
	// At most breakdownMax entries; the cut ones are summed.
	b, err = u.Breakdown(ctx, "series")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != breakdownMax || b.Entries[0].Name != "e102.mkv" || b.More == nil || *b.More != (UsageMore{Count: 3, Bytes: 6}) {
		t.Fatalf("series %d entries, more %+v", len(b.Entries), b.More)
	}
	for _, id := range []string{"nope", "music", "library", "disk", ""} {
		if _, err := u.Breakdown(ctx, id); !errors.Is(err, ErrUnknownRoot) {
			t.Errorf("%q: %v", id, err)
		}
	}
	if _, err := (&Usage{Sandbox: s, NoDisk: true}).Breakdown(ctx, "other"); !errors.Is(err, ErrUnknownRoot) {
		t.Errorf("other without the disk: %v", err)
	}
}

func otherDone(t *testing.T, u *Usage) UsageBreakdown {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := u.Breakdown(context.Background(), "other")
		if err != nil {
			t.Fatal(err)
		}
		if !b.Pending {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatal("other never computed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// «Другое» is what lives on the media disk outside the categories, by the
// shortest path that is not on the way to a category root.
func TestUsageBreakdownOther(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, "media/movies/m.mkv"), 1000)
	write(t, filepath.Join(mr, "tmp/vault-stream/c.ts"), 100)
	write(t, filepath.Join(mr, ".vault-trash/x/y.bin"), 200)
	write(t, filepath.Join(mr, "backup/a/b"), 300)
	if err := os.Link(filepath.Join(mr, "media/movies/m.mkv"), filepath.Join(mr, "backup/m-copy.mkv")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(mr, "media/kids-old/x.mkv"), 400)
	write(t, filepath.Join(mr, "media/readme.txt"), 50)
	write(t, filepath.Join(mr, "tmp/other.log"), 60)
	u := &Usage{Sandbox: s}

	b, err := u.Breakdown(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Pending || len(b.Entries) != 0 {
		t.Fatalf("first call must compute in background: %+v", b)
	}
	b = otherDone(t, u)
	want := "media/kids-old=400,true;backup=300,true;.vault-trash=200,true;tmp/other.log=60,false;media/readme.txt=50,false;"
	if got := entriesOf(b.Entries); got != want {
		t.Fatalf("other %s, want %s", got, want)
	}
	if b.Bytes <= 0 || b.Rest != max(b.Bytes-1010, 0) || b.More != nil {
		t.Fatalf("other %+v", b)
	}

	// The rest never goes below zero.
	u.mu.Lock()
	for i, c := range u.snap.report.Categories {
		if c.ID == "other" {
			u.snap.report.Categories[i].Bytes = 10
		}
	}
	u.mu.Unlock()
	if b = otherDone(t, u); b.Bytes != 10 || b.Rest != 0 || len(b.Entries) != 5 {
		t.Fatalf("clamped %+v", b)
	}

	// A recomputed report computes «Другое» again.
	write(t, filepath.Join(mr, "backup/new"), 5)
	u.mu.Lock()
	u.snap.at = time.Now().Add(-2 * usageTTL)
	u.mu.Unlock()
	if b, err = u.Breakdown(context.Background(), "other"); err != nil || !b.Pending {
		t.Fatalf("after a new report: %+v %v", b, err)
	}
	if b = otherDone(t, u); b.Entries[1].Name != "backup" || b.Entries[1].Bytes != 305 {
		t.Fatalf("recomputed %s", entriesOf(b.Entries))
	}
}

// A walk of «Другое» that runs out of time keeps what it counted, marked
// partial, and is not started again before the report's TTL: a slow disk
// is not walked from zero on every poll.
func TestUsageBreakdownOtherTimeout(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, "backup/a"), 300)
	started := 0
	var mu sync.Mutex
	u := &Usage{Sandbox: s, otherTimeout: 20 * time.Millisecond, otherHook: func(ctx context.Context) {
		mu.Lock()
		started++
		mu.Unlock()
		<-ctx.Done() // a disk slower than the timeout
	}}
	b := otherDone(t, u)
	if !b.Partial || b.Pending {
		t.Fatalf("a walk that timed out: %+v", b)
	}
	for range 5 {
		if b, _ = u.Breakdown(context.Background(), "other"); b.Pending || !b.Partial {
			t.Fatalf("restarted after a timeout: %+v", b)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if started != 1 {
		t.Fatalf("%d walks, want 1", started)
	}
}

// After a new report the last «Другое» is served (pending: a fresh walk
// runs) instead of an empty list, and the walk takes a slot of the
// folder-size walks so it never runs beside two of them.
func TestUsageBreakdownOtherStaleAndBounded(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, "backup/a"), 300)
	u := &Usage{Sandbox: s}
	first := otherDone(t, u)
	if entriesOf(first.Entries) != "backup=300,true;" {
		t.Fatalf("first %s", entriesOf(first.Entries))
	}
	// every folder-size slot is busy: the new walk waits for one
	sem := u.sem()
	for range cap(sem) {
		sem <- struct{}{}
	}
	u.mu.Lock()
	u.snap.at = time.Now().Add(-2 * usageTTL)
	u.mu.Unlock()
	b, err := u.Breakdown(context.Background(), "other")
	if err != nil || !b.Pending || entriesOf(b.Entries) != "backup=300,true;" {
		t.Fatalf("stale while the walk waits: %+v %v", b, err)
	}
	time.Sleep(50 * time.Millisecond)
	if b, _ = u.Breakdown(context.Background(), "other"); !b.Pending {
		t.Fatalf("walked without a slot: %+v", b)
	}
	for range cap(sem) {
		<-sem
	}
	if b = otherDone(t, u); b.Partial || entriesOf(b.Entries) != "backup=300,true;" {
		t.Fatalf("after the slot: %+v", b)
	}
}
