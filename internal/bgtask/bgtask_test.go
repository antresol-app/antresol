package bgtask

import (
	"testing"
	"time"
)

func TestTracker(t *testing.T) {
	var tr Tracker
	if p := tr.Snapshot(); p.Running || p.Total != 0 || !p.LastRun.IsZero() {
		t.Fatalf("zero: %+v", p)
	}
	tr.Begin(2, 5)
	tr.SetState(StateFull)
	tr.Add(10) // never past Total
	if p := tr.Snapshot(); !p.Running || p.Done != 5 || p.Total != 5 || p.State != StateFull {
		t.Fatalf("pass: %+v", p)
	}
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	tr.End(at)
	if p := tr.Snapshot(); p.Running || !p.LastRun.Equal(at) {
		t.Fatalf("end: %+v", p)
	}
	tr.Begin(7, 3) // done never above total
	if p := tr.Snapshot(); p.Done != 3 || p.State != "" {
		t.Fatalf("begin: %+v", p)
	}
}

// What is worked on now goes with the pass; what went wrong stays until
// cleared.
func TestTrackerNowAndError(t *testing.T) {
	var tr Tracker
	tr.Begin(0, 2)
	tr.SetNow("Book", 140)
	tr.SetError("busy")
	if p := tr.Snapshot(); p.Now != "Book" || p.NowPercent != 100 || p.Error != "busy" {
		t.Fatalf("%+v", p)
	}
	tr.End(time.Now())
	if p := tr.Snapshot(); p.Now != "" || p.NowPercent != 0 || p.Error != "busy" {
		t.Fatalf("end: %+v", p)
	}
	tr.SetError("")
	tr.SetNow("", 50)
	if p := tr.Snapshot(); p.Error != "" || p.NowPercent != 0 {
		t.Fatalf("cleared: %+v", p)
	}
}
