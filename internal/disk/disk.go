// Package disk watches the media disk: its fill level, whether it is there
// at all, and the disk.warning event when the level worsens.
package disk

import (
	"context"
	"errors"
	"sync"
	"syscall"
	"time"

	"github.com/antresol-app/antresol/internal/events"
)

// Disk levels.
const (
	LevelOK   = "ok"
	LevelWarn = "warn" // ≥ 85 %: shown to the admin
	LevelFull = "full" // ≥ 95 %: new downloads wait for space
)

// Usage is GET /api/disk and the disk.warning payload.
type Usage struct {
	Used  int64  `json:"used"`
	Total int64  `json:"total"`
	Level string `json:"level"`
	// Offline says why the media disk is unavailable; Used and Total are
	// then unknown.
	Offline string `json:"offline,omitempty"`
}

// LevelOf classifies a usage.
func LevelOf(used, total int64) string {
	if total <= 0 {
		return LevelOK
	}
	switch pct := float64(used) * 100 / float64(total); {
	case pct >= 95:
		return LevelFull
	case pct >= 85:
		return LevelWarn
	}
	return LevelOK
}

func statfs(path string) (total, avail int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bs := int64(st.Bsize)
	return int64(st.Blocks) * bs, int64(st.Bavail) * bs, nil
}

// Stat returns used and total bytes of the file system holding root.
// Space reserved for root counts as used: Vault cannot write there.
func Stat(root string) (used, total int64, err error) { return stat(root, nil) }

func stat(root string, f func(string) (int64, int64, error)) (used, total int64, err error) {
	if f == nil {
		f = statfs
	}
	total, avail, err := f(root)
	if err != nil {
		return 0, 0, err
	}
	if total <= 0 {
		return 0, 0, errors.New("disk: size unknown")
	}
	return total - avail, total, nil
}

// Monitor reports the media disk's usage and publishes disk.warning when
// its level changes to warn or full. The zero value with Root set works.
type Monitor struct {
	Root string
	// Offline says why the media disk is away ("" while it is there); nil:
	// never.
	Offline func() string
	// Bus gets disk.warning; nil: none.
	Bus *events.Bus
	// Statfs returns total and available bytes of path (tests); nil: the
	// system's.
	Statfs func(path string) (total, avail int64, err error)

	mu   sync.Mutex
	last string
}

// Usage is the disk's current usage. A disk that is away reports level ok
// with Offline set (Used and Total unknown).
func (m *Monitor) Usage() (Usage, error) {
	if m.Offline != nil {
		if why := m.Offline(); why != "" {
			return Usage{Level: LevelOK, Offline: why}, nil
		}
	}
	used, total, err := stat(m.Root, m.Statfs)
	if err != nil {
		return Usage{}, err
	}
	return Usage{Used: used, Total: total, Level: LevelOf(used, total)}, nil
}

// Check is Usage, publishing disk.warning when the level is warn or full
// and has changed since the last check.
func (m *Monitor) Check() (Usage, error) {
	u, err := m.Usage()
	if err != nil || u.Offline != "" {
		return u, err
	}
	m.mu.Lock()
	changed := u.Level != m.last
	m.last = u.Level
	m.mu.Unlock()
	if changed && u.Level != LevelOK && m.Bus != nil {
		m.Bus.Publish(events.Event{Type: events.DiskWarning, Data: u})
	}
	return u, nil
}

// Run checks the disk every interval until ctx ends.
func (m *Monitor) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		m.Check()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
