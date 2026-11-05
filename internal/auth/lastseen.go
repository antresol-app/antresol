package auth

import (
	"context"
	"log"
	"sync"
	"time"
)

// touchTimeout bounds one background last_seen write (busy_timeout retries
// included).
const touchTimeout = 30 * time.Second

// touches writes last_seen in the background. The write needs SQLite's
// one write lock, which a commit stalled on the busy media disk can hold
// for seconds: written inside the request, the first click after a few
// idle minutes waited up to busy_timeout (5 s) for it, or failed with 500.
// One write per key is in flight at a time; a failed one is tried again by
// the next request past the throttle.
type touches struct {
	mu      sync.Mutex
	pending map[string]bool
	wg      sync.WaitGroup
}

func (t *touches) do(key string, write func(ctx context.Context) error) {
	t.mu.Lock()
	if t.pending[key] {
		t.mu.Unlock()
		return
	}
	if t.pending == nil {
		t.pending = map[string]bool{}
	}
	t.pending[key] = true
	t.wg.Add(1)
	t.mu.Unlock()
	go func() {
		defer t.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), touchTimeout)
		defer cancel()
		if err := write(ctx); err != nil {
			log.Printf("auth: last seen: %v", err)
		}
		t.mu.Lock()
		delete(t.pending, key)
		t.mu.Unlock()
	}()
}

// wait waits for the writes in flight (tests).
func (t *touches) wait() { t.wg.Wait() }
