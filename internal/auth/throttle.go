package auth

import (
	"sync"
	"time"
)

// Throttle counts failed logins per key (client address, profile) and locks a
// key out with exponential backoff: after Free failures the key waits Base,
// then twice as long after every further failure, up to Max. Failures are
// forgotten after Forget without new ones. In memory: a restart clears it.
type Throttle struct {
	Free   int
	Base   time.Duration
	Max    time.Duration
	Forget time.Duration
	Now    func() time.Time

	mu sync.Mutex
	m  map[string]*strikes
}

type strikes struct {
	n     int
	until time.Time
	last  time.Time
}

// maxThrottleKeys bounds memory under a spray from many addresses.
const maxThrottleKeys = 10000

// NewThrottle returns a throttle with the defaults: 5 free attempts, then
// 30 s doubling up to 15 min; forgotten after an hour.
func NewThrottle() *Throttle {
	return &Throttle{Free: 5, Base: 30 * time.Second, Max: 15 * time.Minute, Forget: time.Hour, Now: time.Now}
}

// Wait returns how long the longest-locked of keys must still wait (0: go on).
func (t *Throttle) Wait(keys ...string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	var wait time.Duration
	for _, k := range keys {
		if s := t.m[k]; s != nil {
			if d := s.until.Sub(now); d > wait {
				wait = d
			}
		}
	}
	return wait
}

// Fail records a failed attempt for every key.
func (t *Throttle) Fail(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	if t.m == nil {
		t.m = map[string]*strikes{}
	}
	if len(t.m) >= maxThrottleKeys {
		t.prune(now)
	}
	for _, k := range keys {
		s := t.m[k]
		if s == nil || now.Sub(s.last) > t.Forget {
			s = &strikes{}
			t.m[k] = s
		}
		s.n++
		s.last = now
		if s.n >= t.Free {
			d := t.Max
			if shift := s.n - t.Free; shift < 30 {
				if b := t.Base << shift; b < d {
					d = b
				}
			}
			s.until = now.Add(d)
		}
	}
}

// Reset forgets the keys (after a successful login).
func (t *Throttle) Reset(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		delete(t.m, k)
	}
}

func (t *Throttle) prune(now time.Time) {
	for k, s := range t.m {
		if now.Sub(s.last) > t.Forget || (s.n < t.Free && now.Sub(s.last) > t.Base) {
			delete(t.m, k)
		}
	}
	if len(t.m) >= maxThrottleKeys { // still full of fresh entries: drop unlocked ones
		for k, s := range t.m {
			if !s.until.After(now) {
				delete(t.m, k)
			}
		}
	}
	// All locked: evict those whose lock ends soonest, so the map never grows past the cap.
	for len(t.m) >= maxThrottleKeys {
		var oldK string
		var oldU time.Time
		first := true
		for k, s := range t.m {
			if first || s.until.Before(oldU) {
				oldK, oldU, first = k, s.until, false
			}
		}
		delete(t.m, oldK)
	}
}
