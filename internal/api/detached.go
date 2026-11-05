package api

import (
	"context"
	"errors"
	"sync"
	"time"
)

// errStillWorking: the job did not finish while the request waited; the
// client asks again later and finds it (or its result) then.
var errStillWorking = errors.New("api: still working")

// detached runs at most one job per key, outside any request: a client
// that gives up (a player reload, a proxy read timeout) does not cancel
// work that the next request needs. Results are not kept here; jobs store
// them (a file on disk, a cache) and later requests look there first.
type detached[T any] struct {
	mu   sync.Mutex
	jobs map[string]*job[T]
}

type job[T any] struct {
	done chan struct{}
	v    T
	err  error
}

// do starts f for key unless it is already running, then waits for it up
// to wait or until ctx ends: errStillWorking means it is still running.
// timeout bounds f itself.
func (d *detached[T]) do(ctx context.Context, key string, wait, timeout time.Duration, f func(context.Context) (T, error)) (T, error) {
	d.mu.Lock()
	if d.jobs == nil {
		d.jobs = map[string]*job[T]{}
	}
	j := d.jobs[key]
	if j == nil {
		j = &job[T]{done: make(chan struct{})}
		d.jobs[key] = j
		go func() {
			jctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			j.v, j.err = f(jctx)
			d.mu.Lock()
			delete(d.jobs, key)
			d.mu.Unlock()
			close(j.done)
		}()
	}
	d.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-j.done:
		return j.v, j.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case <-t.C:
		var zero T
		return zero, errStillWorking
	}
}

// running reports whether a job for key is in flight.
func (d *detached[T]) running(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.jobs[key] != nil
}
