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
	out = withRoot(out, "music", s.Roots(KindFolderMusic), s.scanMusic)
	out = withRoot(out, "books", s.Roots(KindFolderEbooks), s.scanBooks)
	out = withRoot(out, "audiobooks", s.Roots(KindFolderAudiobooks), s.scanAudiobooks)
	out = withRoot(out, "comics", s.Roots(KindFolderComics), s.scanComics)
	return out
}

// withRoot appends a scanner unless the kind has no folder.
func withRoot(out []scanFunc, name string, roots []string, run func(ctx context.Context, st *ScanStats) error) []scanFunc {
	if len(roots) == 0 {
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
	err := s.scanBooksIn(ctx, s.Ebooks, &st)
	s.noteChanges(st, false)
	if err != nil {
		return err
	}
	return s.syncFlags(ctx)
}

// noteChanges records, for Jobs, that a scan changed the library: it wrote or
// removed something, or (full) read everything again.
func (s *Scanner) noteChanges(st ScanStats, full bool) {
	if st.Dirty() || full {
		s.dirty.Store(true)
	}
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
	s.createMissingRoots(ctx)
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
	if err := s.tidy(ctx, s.full || st.Changed > 0); err != nil {
		errs = append(errs, err)
	}
	s.noteChanges(st, s.full)
	if err := s.syncFlags(ctx); err != nil {
		errs = append(errs, err)
	}
	return st, errors.Join(errs...)
}

// tidy brings the missing files in line once the walks are done, when force
// says so: in the scan that read everything again (after a start, a manual
// one) and in a scan that wrote something, which may have left an item empty
// (a file that moved to another episode, a track to another album). The
// removals a walk finds are tidied at once (Repo.removeMissing), and a scan
// that found nothing changed tidies nothing. Not while the media disk is
// offline, nor after a scan that was cut short.
func (s *Scanner) tidy(ctx context.Context, force bool) error {
	if !force || ctx.Err() != nil || s.StorageOffline != nil && s.StorageOffline() != "" {
		return nil
	}
	return s.Repo.tidy(ctx, true)
}
