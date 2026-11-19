// Package bgtask is how a background worker reports its progress for the
// admin's «Задачи» (GET /api/admin/tasks): whether a pass runs now, how
// much of its work is done, why it stands still, and when the last pass
// ended. A worker keeps a Tracker and updates it as it goes; the page reads
// Snapshot.
package bgtask

import (
	"sync"
	"time"
)

// Why a worker stands still (Progress.State).
const (
	// StateOff: turned off in the settings.
	StateOff = "off"
	// StateFull: its space is used up (more only for what is played).
	StateFull = "full"
	// StateOffline: the media disk is away.
	StateOffline = "offline"
)

// Progress is a worker's state as «Задачи» shows it.
type Progress struct {
	// Running: a pass is under way now (it may be waiting for playback to
	// be quiet).
	Running bool `json:"running"`
	// Done of Total: what the worker has finished of what it has to do
	// (its own unit: files, tracks); Total 0 before the first pass.
	Done  int `json:"done"`
	Total int `json:"total"`
	// State: why it stands still (StateOff, StateFull, StateOffline; ""
	// none).
	State string `json:"state,omitempty"`
	// LastRun: when the last pass ended (zero: none yet).
	LastRun time.Time `json:"lastRun,omitzero"`
}

// Tracker holds a worker's Progress; the zero value is ready. Safe for
// concurrent use.
type Tracker struct {
	mu  sync.Mutex
	p   Progress
	now func() time.Time
}

// Begin starts a pass: done of total so far.
func (t *Tracker) Begin(done, total int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.Running, t.p.Done, t.p.Total, t.p.State = true, min(done, total), total, ""
}

// Add counts n more finished units (never past Total).
func (t *Tracker) Add(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.Done = min(t.p.Done+n, t.p.Total)
}

// SetState says why the worker stands still ("" clears it).
func (t *Tracker) SetState(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.State = s
}

// End ends a pass at `at`.
func (t *Tracker) End(at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.Running, t.p.LastRun = false, at.UTC()
}

// Snapshot is the current Progress.
func (t *Tracker) Snapshot() Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.p
}
