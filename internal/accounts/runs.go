package accounts

import "sync"

// Runs is the set of syncs in flight, one per profile and provider. The
// periodic loops (Syncer.Run, the MyShows and Goodreads pullers), the pull
// right after linking and a profile's «sync now» all register here before
// they read the remote list, so two of them never read it at once (the
// per-profile locks keep the writes apart but would let a second read queue
// up behind the first and repeat it) and the API can say an account is
// syncing right now.
//
// The zero value is ready; a nil *Runs registers nothing (a loop built
// without one runs as it always did).
type Runs struct {
	mu     sync.Mutex
	active map[runKey]struct{}
}

type runKey struct {
	uid      int64
	provider string
}

// Start registers a sync of the profile's provider link. ok is false when
// one is registered already (nothing is registered then, and end is nil);
// otherwise end releases it and must be called once the sync is over.
func (r *Runs) Start(uid int64, provider string) (end func(), ok bool) {
	if r == nil {
		return func() {}, true
	}
	k := runKey{uid, provider}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, busy := r.active[k]; busy {
		return nil, false
	}
	if r.active == nil {
		r.active = map[runKey]struct{}{}
	}
	r.active[k] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.active, k)
			r.mu.Unlock()
		})
	}, true
}

// Active: a sync of the profile's provider link is in flight.
func (r *Runs) Active(uid int64, provider string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, busy := r.active[runKey{uid, provider}]
	return busy
}
