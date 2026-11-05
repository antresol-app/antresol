package metadata

import (
	"context"
	"sync"
	"time"
)

// Limiter spaces calls at least Interval apart (MusicBrainz allows one
// request per second per client). One Limiter is shared by all requests of
// a client, retries included; waiters are served one at a time. A nil
// Limiter or Interval <= 0 never waits.
type Limiter struct {
	Interval time.Duration

	mu   sync.Mutex
	last time.Time // when the previous call was let through
}

// Wait blocks until the next call is allowed or ctx is done. A cancelled
// wait does not count as a call.
func (l *Limiter) Wait(ctx context.Context) error {
	if l == nil || l.Interval <= 0 {
		return ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.last.IsZero() {
		if d := time.Until(l.last.Add(l.Interval)); d > 0 {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	l.last = time.Now()
	return nil
}
