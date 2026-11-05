// Package events is an in-process publish/subscribe bus. GET /api/events
// streams it to browsers as server-sent events.
package events

import "sync"

// Event types (the sources build's own: types_full.go).
const (
	LibraryUpdated  = "library.updated"  // data: what changed
	DiskWarning     = "disk.warning"     // data: disk usage when the level is warn or full
	MigrateProgress = "migrate.progress" // data: «Переезд» finished a source ({reportId, source, done, total, counts, finished}); admins only
	ImportProgress  = "import.progress"  // data: a library import's progress ({jobId, status, progress}); admins only
	RequestChanged  = "request.changed"  // data: a kid's request was made or decided ({id, userId}): the kid and admins reload
)

// Event is one message. Data is marshalled to JSON by the SSE handler.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// subBuffer is the queue of one subscriber; when it is full (a stalled
// client) further events are dropped for that subscriber only.
const subBuffer = 64

// Bus fans events out to subscribers. The zero value is ready to use.
type Bus struct {
	mu     sync.Mutex
	subs   map[chan Event]func(typ string) bool // nil filter = everything
	closed bool
}

// Close ends every subscription (their channels close, so SSE streams
// return and graceful shutdown does not wait for open tabs) and makes
// later subscriptions closed from the start. Publish becomes a no-op.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.subs {
		close(ch)
	}
	b.subs = nil
}

// Publish delivers e to every subscriber without blocking.
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch, accept := range b.subs {
		if accept != nil && !accept(e.Type) {
			continue
		}
		select {
		case ch <- e:
		default: // slow subscriber: drop rather than stall the publisher
		}
	}
}

// Subscribe returns a channel of events and a function that ends the
// subscription (and closes the channel). The cancel function may be called
// more than once. An optional accept filter limits the event types.
func (b *Bus) Subscribe(accept ...func(typ string) bool) (<-chan Event, func()) {
	ch := make(chan Event, subBuffer)
	var f func(string) bool
	if len(accept) > 0 {
		f = accept[0]
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	if b.subs == nil {
		b.subs = map[chan Event]func(string) bool{}
	}
	b.subs[ch] = f
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[ch]; ok { // not closed by Close yet
			delete(b.subs, ch)
			close(ch)
		}
	}
}

// Wants reports whether any subscriber accepts events of typ (periodic
// publishers skip work nobody would receive).
func (b *Bus) Wants(typ string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, accept := range b.subs {
		if accept == nil || accept(typ) {
			return true
		}
	}
	return false
}

// Subscribers returns the number of active subscriptions (periodic
// publishers skip work nobody listens to).
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
