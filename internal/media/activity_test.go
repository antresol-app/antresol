package media

import (
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActivityWithin(t *testing.T) {
	now := time.Unix(1000, 0)
	a := &Activity{Now: func() time.Time { return now }}
	if a.Within(time.Minute) {
		t.Fatal("never touched: active")
	}
	a.Touch()
	now = now.Add(59 * time.Second)
	if !a.Within(time.Minute) {
		t.Fatal("touched 59 s ago: idle")
	}
	now = now.Add(2 * time.Second)
	if a.Within(time.Minute) {
		t.Fatal("touched 61 s ago: active")
	}
	var nilA *Activity
	if nilA.Within(time.Minute) {
		t.Fatal("nil: active")
	}
	nilA.Touch() // no panic
}

// Begin/End: a request in flight is reading however long it runs; its end
// starts the quiet over.
func TestActivityInFlight(t *testing.T) {
	now := time.Unix(1000, 0)
	a := &Activity{Now: func() time.Time { return now }}
	a.Begin()
	a.Begin()
	now = now.Add(time.Hour)
	if !a.Reading(time.Minute) {
		t.Fatal("two in flight: not reading")
	}
	a.End()
	if !a.Reading(time.Minute) {
		t.Fatal("one in flight: not reading")
	}
	a.End()
	now = now.Add(59 * time.Second)
	if !a.Reading(time.Minute) {
		t.Fatal("59 s after the last end: quiet")
	}
	now = now.Add(2 * time.Second)
	if a.Reading(time.Minute) {
		t.Fatal("61 s after the last end: reading")
	}
	var nilA *Activity
	nilA.Begin()
	nilA.End()
	if nilA.Reading(time.Minute) {
		t.Fatal("nil: reading")
	}
}

// Reading: a session counts while its ffmpeg runs (not held paused far
// ahead) or it served a segment lately; a live but idle session (a paused
// player still sending heartbeats) does not.
func TestHLSManagerReading(t *testing.T) {
	now := time.Unix(5000, 0)
	m := &HLSManager{WorkDir: t.TempDir(), IdleTimeout: time.Hour, Now: func() time.Time { return now }}
	defer m.Close()
	if m.Reading(time.Minute) {
		t.Fatal("no sessions: reading")
	}
	m.init()
	done := make(chan struct{})
	s := &session{id: "x", last: now, done: done}
	m.mu.Lock()
	m.sessions["x"] = s
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.sessions, "x"); m.mu.Unlock() }()
	if !m.Reading(time.Minute) {
		t.Fatal("ffmpeg running: not reading")
	}
	m.mu.Lock()
	s.paused = true
	m.mu.Unlock()
	if m.Reading(time.Minute) {
		t.Fatal("ffmpeg held far ahead, nothing served: reading")
	}
	m.mu.Lock()
	s.lastSeg = now.Add(-30 * time.Second)
	m.mu.Unlock()
	if !m.Reading(time.Minute) {
		t.Fatal("a segment served 30 s ago: not reading")
	}
	now = now.Add(time.Minute)
	close(done)
	m.mu.Lock()
	s.paused = false
	m.mu.Unlock()
	if m.Reading(time.Minute) {
		t.Fatal("ffmpeg done, last segment 90 s ago: reading")
	}
}

// The limit counts running time only: a run held stopped by busy past
// the limit is not killed; one running past it is.
func TestRunYieldingLimitCountsRunningTimeOnly(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	var busy atomic.Bool
	busy.Store(true)
	time.AfterFunc(800*time.Millisecond, func() { busy.Store(false) })
	cmd := exec.Command("sh", "-c", "sleep 0.3")
	if err := RunYieldingLimit(cmd, busy.Load, 20*time.Millisecond, 600*time.Millisecond); err != nil {
		t.Fatalf("held 0.8 s, ran 0.3 s of a 0.6 s limit: %v", err)
	}
	cmd = exec.Command("sh", "-c", "sleep 5")
	began := time.Now()
	err := RunYieldingLimit(cmd, func() bool { return false }, 20*time.Millisecond, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "time limit") || time.Since(began) > 3*time.Second {
		t.Fatalf("over the limit: %v after %v", err, time.Since(began))
	}
}
