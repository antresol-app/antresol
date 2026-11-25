package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// What looks for new work after a scan (AfterChange) runs when the scan
// found some, not on every tick; AfterScan runs on every one.
func TestJobsAfterChangeOnlyWhenTheScanChanged(t *testing.T) {
	s, mv, _ := newScanner(t)
	touch(t, filepath.Join(mv, "Брат (1997)", "Брат (1997).mkv"), "x")
	var scans, changes, probes int
	j := &Jobs{Scanner: s,
		AfterScan:   func(context.Context) { scans++ },
		AfterChange: func(context.Context) { changes++ },
		AfterProbe:  func(context.Context) { probes++ },
		Probe:       func(context.Context, string) (string, int64, error) { return `{"Container":"mkv"}`, 1000, nil },
	}
	ctx := context.Background()
	j.RunOnce(ctx) // a new film, probed
	if scans != 1 || changes != 1 || probes != 1 {
		t.Fatalf("first run: scans=%d changes=%d probes=%d", scans, changes, probes)
	}
	j.RunOnce(ctx) // nothing new
	if scans != 2 || changes != 1 || probes != 1 {
		t.Fatalf("quiet run: scans=%d changes=%d probes=%d", scans, changes, probes)
	}
	touch(t, filepath.Join(mv, "Брат 2 (2000)", "Брат 2 (2000).mkv"), "x")
	j.RunOnce(ctx)
	if scans != 3 || changes != 2 || probes != 2 {
		t.Fatalf("new film: scans=%d changes=%d probes=%d", scans, changes, probes)
	}
	if err := os.Remove(filepath.Join(mv, "Брат 2 (2000)", "Брат 2 (2000).mkv")); err != nil {
		t.Fatal(err)
	}
	j.RunOnce(ctx) // a removal is a change too
	if changes != 3 {
		t.Fatalf("removed film: changes=%d", changes)
	}
}

// The first run after a start and a manual scan read everything again, so
// the workers are told even when nothing is new.
func TestJobsFullScansAlwaysTellTheWorkers(t *testing.T) {
	s, mv, _ := newScanner(t)
	touch(t, filepath.Join(mv, "Брат (1997)", "Брат (1997).mkv"), "x")
	var changes atomic.Int32
	j := &Jobs{Scanner: s, Interval: time.Hour, AfterChange: func(context.Context) { changes.Add(1) }}
	j.RunOnce(context.Background())
	j.RunOnce(context.Background())
	if changes.Load() != 1 {
		t.Fatalf("changes=%d", changes.Load())
	}
	j.TriggerFull()
	j.RunOnce(context.Background())
	if changes.Load() != 2 {
		t.Fatalf("a manual scan: changes=%d", changes.Load())
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { j.Run(ctx); close(done) }() // Run's first scan reads everything
	deadline := time.Now().Add(5 * time.Second)
	for changes.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if changes.Load() != 3 {
		t.Fatalf("the first scan of a start: changes=%d", changes.Load())
	}
}

// The probe pass waits while the media disk is busy, and gives up after
// probeWait so a long playback does not hold the scans back.
func TestJobsProbeWaitsForIdle(t *testing.T) {
	oldPoll, oldWait := probePoll, probeWait
	probePoll, probeWait = time.Millisecond, time.Hour
	defer func() { probePoll, probeWait = oldPoll, oldWait }()
	s, mv, _ := newScanner(t)
	touch(t, filepath.Join(mv, "Брат (1997)", "Брат (1997).mkv"), "x")
	var checks, probed int
	j := &Jobs{Scanner: s,
		Idle: func() bool { checks++; return checks > 3 },
		Probe: func(context.Context, string) (string, int64, error) {
			probed++
			return `{"Container":"mkv"}`, 1000, nil
		},
	}
	j.RunOnce(context.Background())
	if checks != 4 || probed != 1 {
		t.Fatalf("checks=%d probed=%d", checks, probed)
	}

	touch(t, filepath.Join(mv, "Брат 2 (2000)", "Брат 2 (2000).mkv"), "x")
	probeWait = 20 * time.Millisecond
	j.Idle = func() bool { return false } // a playback that outlasts probeWait
	j.RunOnce(context.Background())
	if probed != 1 {
		t.Fatalf("probed while busy: %d", probed)
	}
	j.Idle = nil
	j.RunOnce(context.Background())
	if probed != 2 {
		t.Fatalf("probed after: %d", probed)
	}
}

// hookLog is a Jobs whose hooks record what ran, in order.
func hookLog(s *Scanner, events *[]string) *Jobs {
	return &Jobs{Scanner: s,
		AfterChange: func(context.Context) { *events = append(*events, "change") },
		AfterProbe:  func(context.Context) { *events = append(*events, "afterprobe") },
		Probe: func(context.Context, string) (string, int64, error) {
			*events = append(*events, "probe")
			return `{"Container":"mkv"}`, 7200000, nil
		},
	}
}

func wantEvents(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s: events %v, want %v", what, got, want)
	}
}

// A scan run by someone else (the addon import runs ScanAll itself, then
// triggers Jobs) is not lost to the follow-up run, which finds everything in
// place: the workers are still told.
func TestJobsFollowUpOfAnImportScanKicksTheWorkers(t *testing.T) {
	ctx := context.Background()
	s, mv, _ := newScanner(t)
	var events []string
	j := hookLog(s, &events)
	j.RunOnce(ctx)
	events = nil
	touch(t, filepath.Join(mv, "Brat (1997)", "Brat (1997).mkv"), "x")
	if st, err := s.ScanAll(ctx); err != nil || !st.Dirty() { // what runImport does
		t.Fatalf("%+v %v", st, err)
	}
	j.RunOnce(ctx) // the run after jobs.Trigger()
	wantEvents(t, "follow-up of the import", events, "change", "probe", "afterprobe")
	events = nil
	j.RunOnce(ctx)
	wantEvents(t, "next quiet run", events)
}

// The request to read everything again is not lost either, when another scan
// entry point takes it first.
func TestJobsRescanTakenByAnotherScanStillKicks(t *testing.T) {
	ctx := context.Background()
	s, mv, _ := newScanner(t)
	touch(t, filepath.Join(mv, "Brat (1997)", "Brat (1997).mkv"), "x")
	var events []string
	j := hookLog(s, &events)
	j.RunOnce(ctx)
	events = nil
	j.TriggerFull()
	if _, err := s.ScanAll(ctx); err != nil { // someone else's scan takes the request
		t.Fatal(err)
	}
	j.RunOnce(ctx)
	wantEvents(t, "manual scan taken by another scan", events, "change", "afterprobe")
}

// A book scanned on its own (an upload) is noted like any other change.
func TestJobsFollowUpOfABookScanKicksTheWorkers(t *testing.T) {
	ctx := context.Background()
	s, root := bookScanner(t)
	var events []string
	j := hookLog(s, &events)
	j.RunOnce(ctx)
	events = nil
	touch(t, filepath.Join(root, "Булгаков - Морфий.epub"), "x")
	if err := s.ScanBooks(ctx); err != nil {
		t.Fatal(err)
	}
	j.RunOnce(ctx)
	wantEvents(t, "follow-up of the book scan", events, "change", "afterprobe")
}

// What needs a file's probe is told after the probe pass, not before it.
func TestJobsProbeDependentHookRunsAfterTheProbe(t *testing.T) {
	ctx := context.Background()
	s, mv, _ := newScanner(t)
	var events []string
	j := hookLog(s, &events)
	j.RunOnce(ctx)
	events = nil
	touch(t, filepath.Join(mv, "Brat (1997)", "Brat (1997).mkv"), "x")
	j.RunOnce(ctx)
	wantEvents(t, "a new film", events, "change", "probe", "afterprobe")
	events = nil
	j.RunOnce(ctx)
	wantEvents(t, "a quiet scan", events)
}
