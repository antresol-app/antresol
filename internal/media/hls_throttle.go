package media

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// dirUsage is what a session dir holds: its size and the highest segment
// index written.
type dirUsage struct {
	bytes  int64
	maxSeg int // -1 = none
	segs   map[int]string
}

func usage(dir string) dirUsage {
	u := dirUsage{maxSeg: -1, segs: map[int]string{}}
	es, err := os.ReadDir(dir)
	if err != nil {
		return u
	}
	for _, e := range es {
		if info, err := e.Info(); err == nil {
			u.bytes += info.Size()
		}
		if i, ok := segIndex(e.Name()); ok {
			u.segs[i] = e.Name()
			u.maxSeg = max(u.maxSeg, i)
		}
	}
	return u
}

// pauseLocked / resumeLocked stop and continue ffmpeg. m.mu must be held.
func (m *HLSManager) pauseLocked(s *session) {
	if s.cmd == nil || s.cmd.Process == nil || s.paused || isDone(s.done) {
		return
	}
	if err := pauseProcess(s.cmd.Process); err != nil {
		log.Printf("media: hls %s: pause ffmpeg: %v", s.id, err)
		return
	}
	s.paused = true
}

func (m *HLSManager) resumeLocked(s *session) {
	if !s.paused {
		return
	}
	s.paused = false
	if s.cmd == nil || s.cmd.Process == nil || isDone(s.done) {
		return
	}
	if err := resumeProcess(s.cmd.Process); err != nil {
		log.Printf("media: hls %s: resume ffmpeg: %v", s.id, err)
	}
}

// throttle bounds what the sessions keep on disk: pauses ffmpeg far ahead of
// the player, deletes segments far behind it and, over MaxBytes, stops the
// sessions nobody watches (oldest first).
func (m *HLSManager) throttle(now time.Time) {
	m.mu.Lock()
	list := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.mu.Unlock()

	var total int64
	for _, s := range list {
		u := usage(s.dir)
		var drop []string
		m.mu.Lock()
		if m.sessions[s.id] != s {
			m.mu.Unlock()
			continue
		}
		s.segs = u.maxSeg + 1
		pos := max(s.reqSeg, 0)
		// the limits count ~6 s segments; a remux's files are GOPs
		keep, aheadMax := m.KeepBehind, m.AheadSegments
		if s.remux != nil {
			keep, aheadMax = s.remux.gops(keep), s.remux.gops(aheadMax)
		}
		if keep > 0 && s.reqSeg-keep > s.trimmed {
			below := s.reqSeg - keep
			for i, n := range u.segs {
				if i < below {
					drop = append(drop, n)
				}
			}
			s.trimmed = below
		}
		if aheadMax > 0 {
			ahead := u.maxSeg - pos
			switch {
			case !s.paused && ahead > aheadMax && (s.remux == nil || s.remux.waiting == 0):
				m.pauseLocked(s)
			case s.paused && ahead <= aheadMax/2:
				m.resumeLocked(s)
			}
		}
		var remuxAt int
		if s.remux != nil {
			remuxAt = s.remux.reqVSeg
		}
		m.mu.Unlock()
		if s.remux != nil {
			trimRemux(s.dir, remuxAt, max(m.KeepBehind/2, 1)) // assembled copies: half as many
		}
		for _, n := range drop {
			p := filepath.Join(s.dir, n)
			if info, err := os.Stat(p); err == nil {
				u.bytes -= info.Size()
			}
			os.Remove(p)
		}
		m.mu.Lock()
		s.bytes = u.bytes
		m.mu.Unlock()
		total += u.bytes
	}

	if m.MaxBytes <= 0 || total <= m.MaxBytes {
		return
	}
	m.mu.Lock()
	var idle []*session
	for _, s := range m.sessions {
		if now.Sub(s.lastData) > m.EvictIdle {
			idle = append(idle, s)
		}
	}
	m.mu.Unlock()
	sort.Slice(idle, func(i, j int) bool { return idle[i].lastData.Before(idle[j].lastData) })
	for _, s := range idle {
		if total <= m.MaxBytes {
			return
		}
		log.Printf("media: hls sessions hold %d MB (limit %d MB): stopping %s, unwatched for %s",
			total>>20, m.MaxBytes>>20, s.id, now.Sub(s.lastData).Round(time.Second))
		m.Stop(s.id)
		total -= s.bytes
	}
}
