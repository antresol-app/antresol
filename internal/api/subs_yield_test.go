package api

import (
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// A subtitle extraction stays stopped while playback needs the disk, and
// finishes once it is free.
func TestRunYieldingHoldsWhileBusy(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	var busy atomic.Bool
	busy.Store(true)
	time.AfterFunc(1200*time.Millisecond, func() { busy.Store(false) })
	// counts on its own: a stopped process does not count
	cmd := exec.Command("sh", "-c", "i=0; while [ $i -lt 6 ]; do sleep 0.1; i=$((i+1)); done")
	began := time.Now()
	if err := runYielding(cmd, busy.Load, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took < 1200*time.Millisecond {
		t.Fatalf("ran while busy: done in %v", took)
	}
	cmd = exec.Command("sh", "-c", "sleep 0.2")
	began = time.Now()
	if err := runYielding(cmd, func() bool { return false }, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > time.Second {
		t.Fatalf("idle disk, still took %v", took)
	}
}
