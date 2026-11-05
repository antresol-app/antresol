package library

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Hold waits for a running scan only so long (or until the request ends).
func TestScannerHoldWaitsBounded(t *testing.T) {
	s := &Scanner{}
	old := HoldWait
	HoldWait = 60 * time.Millisecond
	t.Cleanup(func() { HoldWait = old })

	release, err := s.Hold(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := s.Hold(context.Background()); !errors.Is(err, ErrScanBusy) || time.Since(start) > time.Second {
		t.Fatalf("busy: %v after %v", err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Hold(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	release()
	r2, err := s.Hold(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r2()
}
