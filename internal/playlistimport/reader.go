package playlistimport

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

// readTimeout bounds one background read.
const readTimeout = 30 * time.Minute

// coreReads are the reads the core itself runs in the background
// (ReadInBackground): one per profile and service at a time, each stopped
// by a cancel or delete of its job.
type coreReads struct {
	busy  map[string]bool     // "<user>/<service>" → reading
	stops map[int64]*readStop // job → its read
	wg    sync.WaitGroup
}

// readStop cancels one read (compared by pointer: a job id given again is
// another read).
type readStop struct{ cancel context.CancelFunc }

// ReadInBackground makes a reading job of service for userID whose snapshot
// read fills in the background (an uploaded Takeout whose videos are being
// named): the job moves to preview, or fails with the read's error text
// (read gives a user-facing, Russian one). A cancel or delete of the job
// stops the read (its context ends), and a shutdown (Run's context ending)
// fails it. One runs at a time per profile and service: another is ErrBusy,
// and so is one asked for once Run's context has ended (shutting down).
func (s *Service) ReadInBackground(ctx context.Context, userID int64, service string, read func(ctx context.Context, jobID int64) (Snapshot, error)) (int64, error) {
	key := strconv.FormatInt(userID, 10) + "/" + service
	s.readMu.Lock()
	if s.reads.busy[key] || (s.readBase != nil && s.readBase.Err() != nil) {
		s.readMu.Unlock()
		return 0, ErrBusy
	}
	if s.reads.busy == nil {
		s.reads.busy = map[string]bool{}
		s.reads.stops = map[int64]*readStop{}
	}
	s.reads.busy[key] = true
	base := s.readBase
	// counted under readMu: WaitReads' barrier orders it before its Wait
	s.reads.wg.Add(1)
	s.readMu.Unlock()
	release := func() {
		s.readMu.Lock()
		delete(s.reads.busy, key)
		s.readMu.Unlock()
	}
	id, err := s.Store.CreateReadingJob(ctx, userID, service)
	if err != nil {
		release()
		s.reads.wg.Done()
		return 0, err
	}
	if base == nil {
		base = context.Background()
	}
	rctx, cancel := context.WithTimeout(base, readTimeout)
	stop := &readStop{cancel: cancel}
	s.readMu.Lock()
	s.reads.stops[id] = stop
	s.readMu.Unlock()
	s.Publish(ctx, id, true)
	go func() {
		defer s.reads.wg.Done()
		defer func() {
			s.readMu.Lock()
			if s.reads.stops[id] == stop {
				delete(s.reads.stops, id)
			}
			s.readMu.Unlock()
			cancel()
			release()
		}()
		s.fillRead(rctx, base, id, service, read)
	}()
	return id, nil
}

// fillRead runs one read and fills its job (a cancel or delete meanwhile
// wins).
func (s *Service) fillRead(ctx, base context.Context, jobID int64, service string, read func(context.Context, int64) (Snapshot, error)) {
	log := s.Logger().With("job", jobID, "service", service)
	snap, err := read(ctx, jobID)
	if err == nil {
		snap.Service = service
		err = s.Fill(ctx, jobID, snap)
	}
	if err == nil {
		return
	}
	text := err.Error()
	switch {
	case base.Err() != nil:
		log.Warn("import: read stopped by shutdown")
		text = MsgError
	case errors.Is(ctx.Err(), context.Canceled):
		log.Info("import: read stopped: the job was cancelled or deleted")
		return
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		log.Warn("import: read ran out of time")
		text = MsgError
	default:
		log.Warn("import: read failed", "err", err)
	}
	if _, aerr := s.Advance(context.WithoutCancel(ctx), jobID, StatusReading, StatusFailed, text); aerr != nil &&
		!errors.Is(aerr, ErrNotFound) {
		log.Error("import: fail reading job", "err", aerr)
	}
}

// stopRead ends the core's own read of a job, if one runs.
func (s *Service) stopRead(jobID int64) {
	s.readMu.Lock()
	stop := s.reads.stops[jobID]
	s.readMu.Unlock()
	if stop != nil {
		stop.cancel()
	}
}

// WaitReads waits for the core's background reads in progress (tests,
// shutdown). Once Run's context has ended no read starts any more, and
// every one that started was counted under readMu: taking it once orders
// those counts before the Wait.
func (s *Service) WaitReads() {
	s.readMu.Lock()
	s.readMu.Unlock() //nolint:staticcheck // a barrier, not an empty section by mistake
	s.reads.wg.Wait()
}

// setReadBase makes ctx (Run's) the base of the reads started from now on:
// a shutdown ends them.
func (s *Service) setReadBase(ctx context.Context) {
	s.readMu.Lock()
	s.readBase = ctx
	s.readMu.Unlock()
}
