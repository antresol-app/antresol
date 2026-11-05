package media

import (
	"sync"
	"time"
)

// Activity remembers when someone last played something (a stream range
// read, an HLS segment, a progress save): background readers of the media
// disk (the skip-segment analysis, the scrub previews) hold off while it
// is recent, or while a media response is still being written (Begin/End:
// a direct play's one long range read). A nil Activity is never active.
type Activity struct {
	Now func() time.Time

	mu       sync.Mutex
	last     time.Time
	inFlight int
}

func (a *Activity) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Touch records playback now.
func (a *Activity) Touch() {
	if a == nil {
		return
	}
	t := a.now()
	a.mu.Lock()
	a.last = t
	a.mu.Unlock()
}

// Within reports playback during the last d.
func (a *Activity) Within(d time.Duration) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	last := a.last
	a.mu.Unlock()
	return !last.IsZero() && a.now().Sub(last) < d
}

// Begin records a media response starting (End when it is written).
func (a *Activity) Begin() {
	if a == nil {
		return
	}
	t := a.now()
	a.mu.Lock()
	a.inFlight++
	a.last = t
	a.mu.Unlock()
}

// End records a media response done: the quiet starts from now.
func (a *Activity) End() {
	if a == nil {
		return
	}
	t := a.now()
	a.mu.Lock()
	if a.inFlight > 0 {
		a.inFlight--
	}
	a.last = t
	a.mu.Unlock()
}

// Reading reports a media response in flight, or playback during the last d.
func (a *Activity) Reading(d time.Duration) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	n := a.inFlight
	a.mu.Unlock()
	return n > 0 || a.Within(d)
}
