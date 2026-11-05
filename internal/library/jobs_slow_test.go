package library

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// blockingEnricher holds its pass until release is closed or ctx ends.
type blockingEnricher struct {
	started chan struct{}
	release chan struct{}
	passes  atomic.Int32
	stopped atomic.Bool
}

func (b *blockingEnricher) EnrichPending(ctx context.Context) error {
	if b.passes.Add(1) == 1 {
		close(b.started)
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		b.stopped.Store(true)
		return ctx.Err()
	}
	return nil
}

func TestSlowEnrichersDoNotBlockScans(t *testing.T) {
	s, mv, _ := newScanner(t)
	fast := &countEnricher{}
	slow := &blockingEnricher{started: make(chan struct{}), release: make(chan struct{})}
	j := &Jobs{Scanner: s, Enrichers: []Enricher{fast}, SlowEnrichers: []Enricher{slow}, Interval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { j.Run(ctx); close(done) }()

	select {
	case <-slow.started: // woken by the first scan
	case <-time.After(5 * time.Second):
		t.Fatal("slow enricher not started after the scan")
	}
	waitIdle := func(runs int) {
		deadline := time.Now().Add(5 * time.Second)
		for (fast.runs() < runs || j.Status().Running) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if fast.runs() < runs || j.Status().Running {
			t.Fatalf("scan %d did not finish while the slow pass runs: %+v", runs, j.Status())
		}
	}
	waitIdle(1)

	// a new file arrives while the slow pass is still running: the
	// triggered scan finishes and the movie is in the library
	touch(t, filepath.Join(mv, "Брат (1997)", "Брат (1997).mkv"), "x")
	j.Trigger()
	waitIdle(2)
	items, _, err := s.Repo.List(context.Background(), ListQuery{Kind: KindMovie})
	if err != nil || len(items) != 1 || j.Status().Movies != 1 {
		t.Fatalf("movie not published: %v %v %+v", items, err, j.Status())
	}
	if slow.passes.Load() != 1 || slow.stopped.Load() {
		t.Fatalf("slow passes %d", slow.passes.Load())
	}

	// shutdown stops the worker mid-pass; Run returns only after it
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if !slow.stopped.Load() {
		t.Fatal("the slow pass was not stopped by shutdown")
	}
}

func TestSlowEnrichersCoalesce(t *testing.T) {
	s, _, _ := newScanner(t)
	slow := &blockingEnricher{started: make(chan struct{}), release: make(chan struct{})}
	j := &Jobs{Scanner: s, SlowEnrichers: []Enricher{slow}, Interval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { j.Run(ctx); close(done) }()
	<-slow.started
	for i := 0; i < 5; i++ { // five scans during one pass
		j.RunOnce(ctx)
	}
	close(slow.release)
	deadline := time.Now().Add(5 * time.Second)
	for slow.passes.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if n := slow.passes.Load(); n != 2 {
		t.Fatalf("passes %d, want 2 (the wake-ups coalesce)", n)
	}
	cancel()
	<-done
}
