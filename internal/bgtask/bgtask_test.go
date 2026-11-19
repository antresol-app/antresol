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
