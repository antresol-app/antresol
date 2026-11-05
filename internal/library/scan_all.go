package library

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// scanFunc is one non-video scanner run by ScanAll.
type scanFunc struct {
	Name string
	Run  func(ctx context.Context, st *ScanStats) error
}

// extra lists the non-video scanners in order: music → books → audiobooks
// → comics. A scanner whose root is empty is left out (see withRoot).
func (s *Scanner) extra() []scanFunc {
	if s.extraOverride != nil {
		return s.extraOverride
	}
	var out []scanFunc
	out = withRoot(out, "music", s.Music, s.scanMusic)
	out = withRoot(out, "books", s.Ebooks, s.scanBooks)
	out = withRoot(out, "audiobooks", s.Audiobooks, s.scanAudiobooks)
	out = withRoot(out, "comics", s.Comics, s.scanComics)
	return out
}

// withRoot appends a scanner unless its root is not configured.
func withRoot(out []scanFunc, name, root string, run func(ctx context.Context, st *ScanStats) error) []scanFunc {
	if root == "" {
		return out
	}
	return append(out, scanFunc{Name: name, Run: run})
}

// ScanBooks indexes the ebooks root alone, under the lock of ScanAll: a file
// just added is in the library when it returns.
func (s *Scanner) ScanBooks(ctx context.Context) error {
	if s.Ebooks == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var st ScanStats
	return s.scanBooks(ctx, &st)
}

// ErrScanBusy: a scan holds the library and did not finish in time.
var ErrScanBusy = errors.New("library: a scan is running")

// HoldWait is how long Hold waits for a running scan.
var HoldWait = 5 * time.Second

// Hold takes the scans' lock until release is called: no scan runs (nor
// brings back rows of files that are being moved away) meanwhile. It
// waits at most HoldWait for a running scan (ErrScanBusy), less when ctx
// ends first.
func (s *Scanner) Hold(ctx context.Context) (release func(), err error) {
	deadline := time.NewTimer(HoldWait)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for !s.mu.TryLock() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, ErrScanBusy
		case <-tick.C:
		}
	}
	return s.mu.Unlock, nil
}

// ScanAll indexes every media root under one lock: video first, then the
// extra scanners. A failing scanner does not stop the others; the errors
// are joined, each prefixed with its scanner name.
func (s *Scanner) ScanAll(ctx context.Context) (ScanStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st ScanStats
	var errs []error
	if err := s.scanVideo(ctx, &st); err != nil {
		errs = append(errs, err)
	}
	for _, f := range s.extra() {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if err := f.Run(ctx, &st); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Name, err))
		}
	}
	return st, errors.Join(errs...)
}
