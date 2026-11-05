package screentime

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
)

// ErrOver: a metered response stopped because the profile's time is over.
var ErrOver = errors.New("screentime: time is over")

// MeterEvery is how often a metered response counts what it sent and
// checks the profile's time (tests set 0: every write).
var MeterEvery = 5 * time.Second

// Meter wraps a media response of a kid profile: the bytes it sends are
// playback delivered (over the stream's bitrate, Signal.Media), counted
// every MeterEvery, and once the time is over its writes fail, which ends
// the response (a long audiobook or a whole mix stops at the limit, not at
// the end of the file).
type Meter struct {
	http.ResponseWriter
	s           *Service
	ctx         context.Context
	u           auth.User
	sig         Signal
	bytesPerSec float64

	mu      sync.Mutex
	pending int64
	last    time.Time
	stopped bool
}

// Meter is w counted as sig's stream at bytesPerSec; nil when nothing is
// to count (not a kid, a nil Service, no bitrate): use w as is. Call Done
// when the response ends.
func (s *Service) Meter(ctx context.Context, w http.ResponseWriter, u auth.User, sig Signal, bytesPerSec float64) *Meter {
	if s == nil || !u.IsKid || sig.Category == "" || bytesPerSec <= 0 {
		return nil
	}
	return &Meter{ResponseWriter: w, s: s, ctx: context.WithoutCancel(ctx), u: u, sig: sig, bytesPerSec: bytesPerSec}
}

func (m *Meter) Write(p []byte) (int, error) {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return 0, ErrOver
	}
	m.mu.Unlock()
	n, err := m.ResponseWriter.Write(p)
	m.mu.Lock()
	m.pending += int64(n)
	flush := m.last.IsZero() || time.Since(m.last) >= MeterEvery
	m.mu.Unlock()
	if flush {
		m.flush(true)
	}
	return n, err
}

// flush counts what was sent since the last flush; check: then ask
// whether the time is over (the next write fails).
func (m *Meter) flush(check bool) {
	m.mu.Lock()
	n := m.pending
	m.pending = 0
	m.last = time.Now()
	m.mu.Unlock()
	if n > 0 {
		sig := m.sig
		sig.Media = time.Duration(float64(n) / m.bytesPerSec * float64(time.Second))
		m.s.Signal(m.ctx, m.u, sig)
	}
	if check && m.s.Allow(m.ctx, m.u, m.sig.Category) != nil {
		m.mu.Lock()
		m.stopped = true
		m.mu.Unlock()
	}
}

// Done counts the rest of what was sent.
func (m *Meter) Done() { m.flush(false) }

// Unwrap lets http.ResponseController reach the connection (deadlines, flush).
func (m *Meter) Unwrap() http.ResponseWriter { return m.ResponseWriter }

// Flush passes a flush on (a transcode streams).
func (m *Meter) Flush() {
	if f, ok := m.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
