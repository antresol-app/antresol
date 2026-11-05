package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const segmentSeconds = 6

var (
	segmentName = regexp.MustCompile(`^(seg(\d{5,9})\.(ts|m4s)|init\.mp4)$`)
	// sessionDir is the name of a session directory (Start makes 8 random bytes in hex).
	sessionDir = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

var (
	ErrNoSession = errors.New("media: hls session not found")
	ErrNoSegment = errors.New("media: segment not found")
	// ErrSegmentGone: the segment lay too far behind the player and was deleted.
	ErrSegmentGone = errors.New("media: segment was trimmed")
	// ErrNotReady: ffmpeg is still working on the first segment.
	ErrNotReady = errors.New("media: hls playlist not ready yet")
	// ErrUserLimit: the profile already has UserLimit sessions.
	ErrUserLimit = errors.New("media: too many hls sessions for this profile")
	// ErrBusy: the server already runs GlobalLimit sessions.
	ErrBusy = errors.New("media: too many hls sessions")
	// ErrNoSpace: the transcode dir has less than MinFreeBytes free.
	ErrNoSpace = errors.New("media: not enough free space for transcoding")
)

// HLSManager runs ffmpeg HLS sessions in WorkDir (one sub-directory each).
//
// The disk use is bounded: ffmpeg reads at ReadRate× real time after a
// ReadBurst-second burst, is paused (SIGSTOP) once it is AheadSegments
// ahead of the last segment the player asked for and resumed when the
// player gets within half of that; segments more than KeepBehind behind
// the player are deleted; when all sessions together hold more than
// MaxBytes, the sessions nobody has watched for EvictIdle are stopped,
// oldest first. At most UserLimit sessions per profile and GlobalLimit in
// total run at once, and none starts while the disk has less than
// MinFreeBytes free. Stale session dirs (a crash, a SIGKILL) are removed
// when the manager starts.
//
// Zero values pick defaults (IdleTimeout 120s, SweepInterval 1s,
// PlaylistWait 15s, SegmentWait 20s, UserLimit 2, GlobalLimit 4, MaxBytes 20 GiB,
// AheadSegments 50, KeepBehind 100, ReadRate 2, ReadBurst 30s,
// MinFreeBytes 2 GiB, EvictIdle 2m, Now = time.Now); a negative value
// turns that limit off. Call Close when done: it kills every ffmpeg.
type HLSManager struct {
	FFmpeg  string
	WorkDir string
	HW      string // "qsv" | "videotoolbox" | "none"

	IdleTimeout   time.Duration
	SweepInterval time.Duration
	PlaylistWait  time.Duration
	// SegmentWait bounds how long a segment request of a complete-playlist
	// session waits for ffmpeg to write that segment (hls_complete.go).
	SegmentWait time.Duration
	// IndexDir caches the keyframe indexes of remuxed files (default
	// WorkDir/keyindex; Start's stale-dir sweep leaves it alone).
	IndexDir string
	Now      func() time.Time

	UserLimit, GlobalLimit int
	MaxBytes               int64
	AheadSegments          int
	KeepBehind             int
	ReadRate, ReadBurst    float64
	MinFreeBytes           int64
	EvictIdle              time.Duration

	once     sync.Once
	root     context.Context // lifetime of all ffmpeg processes; cancelled by Close
	rootStop context.CancelFunc
	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
}

// StartOptions describe a new session.
type StartOptions struct {
	UserID int64 // the profile; counts against UserLimit
	FileID int64
	// Input is a local file or a loopback http URL (the local stream);
	// other URLs are ErrUnsafeInput.
	Input    string
	Decision Decision
	// AudioIndex overrides Decision.AudioIndex when >= 0.
	AudioIndex int
	StartSec   float64
	// DurationSec is the source's duration (0 = unknown). With it a
	// transcoded session also has a complete VOD playlist (CompletePlaylist).
	DurationSec float64
	// StreamStart is where the stream starts on the file's timeline (a
	// copied video: the keyframe before StartSec; default StartSec).
	StreamStart float64
	// Replace is the profile's previous session (a seek or track change):
	// it is stopped first, so a reload never hits the limit.
	Replace string
}

// SessionStatus is what the player's heartbeat learns about its session.
type SessionStatus struct {
	Segments int  `json:"segments"` // segments written so far
	Running  bool `json:"running"`  // ffmpeg has not finished
	Paused   bool `json:"paused"`   // ffmpeg waits: it is far ahead of the player
}

type session struct {
	// launchedAt: when the running ffmpeg started (IOBusy)
	launchedAt time.Time
	id         string
	uid        int64
	fileID     int64
	dir        string
	input      string
	d          Decision
	startSec   float64
	// guarded by HLSManager.mu
	hw       string
	retried  bool
	cancel   context.CancelFunc
	cmd      *exec.Cmd
	done     chan struct{}
	stderr   *tailBuf
	last     time.Time // any request, heartbeats included (idle timeout)
	lastData time.Time // last playlist/segment request (byte budget)
	lastSeg  time.Time // last segment served (Reading)
	reqSeg   int       // last requested segment index, -1 = none yet
	paused   bool
	segs     int   // highest segment index written + 1
	bytes    int64 // size of the dir at the last sweep
	trimmed  int   // segments below this index were deleted
	// complete: the seconds the complete VOD playlist covers, 0 = none
	complete float64
	// firstSeg: the segment the running ffmpeg started at (a far request
	// restarts it there, on the same 6 s grid; hls_complete.go)
	firstSeg int
	// earlier: segments listed by earlier ffmpeg runs of this session
	earlier map[string]bool
	// restarting: a far request is moving ffmpeg (others just wait)
	restarting bool
	// drifted: the once-per-session log of a segment off the listed length
	drifted bool
	// remux: a copied video's complete playlist from its keyframe index
	// (hls_remux.go); nil = the EVENT playlist
	remux *remuxState
	// webMu: one web.m3u8 rewrite at a time (an older copy never replaces a newer one)
	webMu sync.Mutex
}

func (m *HLSManager) init() {
	m.once.Do(func() {
		def := func(v *int, d int) {
			if *v == 0 {
				*v = d
			}
		}
		if m.IdleTimeout == 0 {
			m.IdleTimeout = 120 * time.Second
		}
		if m.SweepInterval == 0 {
			m.SweepInterval = time.Second
		}
		if m.PlaylistWait == 0 {
			m.PlaylistWait = 15 * time.Second
		}
		if m.SegmentWait == 0 {
			m.SegmentWait = 20 * time.Second
		}
		if m.IndexDir == "" {
			m.IndexDir = filepath.Join(m.WorkDir, "keyindex")
		}
		if m.Now == nil {
			m.Now = time.Now
		}
		def(&m.UserLimit, 2)
		def(&m.GlobalLimit, 4)
		def(&m.AheadSegments, 50)
		def(&m.KeepBehind, 100)
		if m.MaxBytes == 0 {
			m.MaxBytes = 20 << 30
		}
		if m.ReadRate == 0 {
			m.ReadRate = 2
		}
		if m.ReadBurst == 0 {
			m.ReadBurst = 30
		}
		if m.MinFreeBytes == 0 {
			m.MinFreeBytes = 2 << 30
		}
		if m.EvictIdle == 0 {
			m.EvictIdle = 2 * time.Minute
		}
		m.sessions = map[string]*session{}
		m.root, m.rootStop = context.WithCancel(context.Background())
		m.wipeStale()
		go m.sweepLoop()
	})
}

// wipeStale removes session dirs left by an earlier process. Only names
// that Start creates are touched: a WorkDir pointed at the wrong place
// loses nothing else.
func (m *HLSManager) wipeStale() {
	es, err := os.ReadDir(m.WorkDir)
	if err != nil {
		return
	}
	for _, e := range es {
		if e.IsDir() && sessionDir.MatchString(e.Name()) {
			if err := os.RemoveAll(filepath.Join(m.WorkDir, e.Name())); err != nil {
				log.Printf("media: remove stale hls dir %s: %v", e.Name(), err)
			}
		}
	}
}

func (m *HLSManager) sweepLoop() {
	t := time.NewTicker(m.SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-m.root.Done():
			return
		case <-t.C:
			now := m.Now()
			var idle []string
			m.mu.Lock()
			for id, s := range m.sessions {
				if now.Sub(s.last) > m.IdleTimeout {
					idle = append(idle, id)
				}
			}
			m.mu.Unlock()
			for _, id := range idle {
				m.Stop(id)
			}
			m.throttle(now)
		}
	}
}

// ioBusyWindow: how long after an ffmpeg start (a play or a seek: the
// player waits for its first segments, read at full speed) IOBusy holds.
const ioBusyWindow = 20 * time.Second

// IOBusy reports a session whose ffmpeg started within ioBusyWindow:
// background reads of the media disk (subtitle extraction) should wait
// meanwhile. On the server's spinning disk a full-file read next to a
// seek made the first segment take 2-9 s instead of 0.1 s.
func (m *HLSManager) IOBusy() bool {
	m.init()
	now := m.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if !s.launchedAt.IsZero() && now.Sub(s.launchedAt) < ioBusyWindow && !isDone(s.done) {
			return true
		}
	}
	return false
}

// Reading reports an HLS session reading the media disk: its ffmpeg runs
// (not held paused far ahead of the player) or it served a segment within
// d. A live but idle session (a paused player's heartbeats) is not: the
// skip-segment analysis may read meanwhile.
func (m *HLSManager) Reading(d time.Duration) bool {
	m.init()
	now := m.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if (s.done != nil && !isDone(s.done) && !s.paused) || (!s.lastSeg.IsZero() && now.Sub(s.lastSeg) < d) {
			return true
		}
	}
	return false
}

func (m *HLSManager) throttleArgs() Throttle {
	if m.ReadRate <= 0 {
		return Throttle{}
	}
	return Throttle{Rate: m.ReadRate, Burst: max(m.ReadBurst, 0)}
}

// launchLocked starts (or restarts) ffmpeg for s with the given hw mode.
// m.mu must be held.
func (m *HLSManager) launchLocked(s *session, hw string) error {
	sctx, cancel := context.WithCancel(m.root)
	args := BuildArgsThrottled(hw, s.input, s.d, s.startSec+float64(s.firstSeg*segmentSeconds), s.dir, m.throttleArgs())
	if s.firstSeg > 0 {
		args = fromSegment(args, s.firstSeg)
	}
	if s.remux != nil {
		ss := s.remux.remuxStart()
		args = remuxArgs(BuildArgsThrottled(hw, s.input, s.d, ss, s.dir, m.throttleArgs()), s.d, s.remux.run, ss-s.remux.streamAbs+s.remux.margin)
	}
	cmd := exec.CommandContext(sctx, m.FFmpeg, args...)
	cmd.WaitDelay = 2 * time.Second
	tail := &tailBuf{max: 4096}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("ffmpeg: %w", err)
	}
	s.launchedAt = m.Now()
	done := make(chan struct{})
	s.hw, s.cancel, s.cmd, s.done, s.stderr = hw, cancel, cmd, done, tail
	s.paused, s.segs, s.trimmed = false, 0, 0
	go func() {
		cmd.Wait() // reaps the process; no zombies
		close(done)
	}()
	return nil
}

// Start launches ffmpeg for o.Input. ctx only bounds start-up; the process
// lives until Stop, idle timeout, eviction or Close, so a per-request ctx is
// fine.
func (m *HLSManager) Start(ctx context.Context, o StartOptions) (string, error) {
	m.init()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, _, err := InputArgs(o.Input); err != nil {
		return "", err
	}
	d := o.Decision
	if o.AudioIndex >= 0 {
		d.AudioIndex = o.AudioIndex
	}
	if o.Replace != "" {
		m.mu.Lock()
		r := m.sessions[o.Replace]
		own := r != nil && r.uid == o.UserID
		m.mu.Unlock()
		if own {
			m.Stop(o.Replace)
		}
	}
	if o.FileID != 0 {
		// A reload or reopen of the same file by the same profile replaces
		// the orphan the old page left behind (it would idle out in 2 min).
		m.mu.Lock()
		var dup []string
		for id, x := range m.sessions {
			if x.uid == o.UserID && x.fileID == o.FileID {
				dup = append(dup, id)
			}
		}
		m.mu.Unlock()
		for _, id := range dup {
			m.Stop(id)
		}
	}
	if err := os.MkdirAll(m.WorkDir, 0o755); err != nil {
		return "", err
	}
	if m.MinFreeBytes > 0 {
		if free, err := freeBytes(m.WorkDir); err == nil && free < m.MinFreeBytes {
			return "", ErrNoSpace
		}
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	dir := filepath.Join(m.WorkDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	s := &session{id: id, uid: o.UserID, fileID: o.FileID, dir: dir, input: o.Input, d: d, startSec: o.StartSec, reqSeg: -1,
		complete: completeTotal(d, o.Input, o.DurationSec, o.StartSec), remux: m.remuxFor(d, o)}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		os.RemoveAll(dir)
		return "", errors.New("media: hls manager closed")
	}
	mine := 0
	for _, x := range m.sessions {
		if x.uid == o.UserID {
			mine++
		}
	}
	var held int64
	for _, x := range m.sessions {
		held += x.bytes
	}
	switch {
	case m.MaxBytes > 0 && held >= m.MaxBytes:
		// Active sessions already fill the budget (eviction only takes idle
		// ones): do not add another that would push the disk further.
		os.RemoveAll(dir)
		return "", ErrNoSpace
	case m.UserLimit > 0 && mine >= m.UserLimit:
		os.RemoveAll(dir)
		return "", ErrUserLimit
	case m.GlobalLimit > 0 && len(m.sessions) >= m.GlobalLimit:
		os.RemoveAll(dir)
		return "", ErrBusy
	}
	if err := m.launchLocked(s, m.HW); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	s.last = m.Now()
	s.lastData = s.last
	m.sessions[id] = s
	return id, nil
}

// get returns the session and marks it used; data = a playlist or segment
// request (the player is watching), not just a heartbeat.
func (m *HLSManager) get(id string, data bool) (*session, error) {
	m.init()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return nil, ErrNoSession
	}
	s.last = m.Now()
	if data {
		s.lastData = s.last
	}
	return s, nil
}

func isDone(c chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// Touch is the player's heartbeat: it keeps a paused player's session from
// the idle timeout and reports how the session is doing.
func (m *HLSManager) Touch(id string) (SessionStatus, error) {
	s, err := m.get(id, false)
	if err != nil {
		return SessionStatus{}, err
	}
	m.mu.Lock()
	remux := s.remux != nil
	m.mu.Unlock()
	var covered float64
	if remux {
		covered = m.coveredEnd(s) // a remux counts 6 s steps of what ffmpeg wrote
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	segs := s.segs
	if remux {
		segs = int(covered / segmentSeconds)
	}
	return SessionStatus{Segments: segs, Running: !isDone(s.done), Paused: s.paused}, nil
}

// Playlist returns the playlist path, waiting up to PlaylistWait for ffmpeg
// to list the first segment; still working by then is ErrNotReady (the
// player asks again). If a hardware-accelerated ffmpeg dies before that
// (e.g. the encoder fails to open), the session is restarted once in
// software mode.
func (m *HLSManager) Playlist(id string) (string, error) {
	s, err := m.get(id, true)
	if err != nil {
		return "", err
	}
	p := filepath.Join(s.dir, "index.m3u8")
	m.mu.Lock()
	lead := 0.0
	if s.remux != nil {
		lead = -s.remux.runStarts[0]
	}
	m.mu.Unlock()
	// Ready once ffmpeg listed a segment: an ffmpeg whose encoder fails to
	// open may already have written a bare playlist header.
	ready := func() bool { b, err := os.ReadFile(p); return err == nil && bytes.Contains(b, []byte("#EXTINF")) }
	if lead > 0 {
		// A remux's first run begins a keyframe before the stream start: the
		// player's clock is startSec + currentTime, so the playlist it gets
		// (the web, an app session without a complete one) leaves that GOP out.
		web := filepath.Join(s.dir, webPlaylist)
		p0 := p
		p = web
		ready = func() bool {
			s.webMu.Lock()
			defer s.webMu.Unlock()
			b, err := os.ReadFile(p0)
			if err != nil {
				return false
			}
			out, ok := dropLead(string(b), lead)
			if !ok {
				return false
			}
			return writeAtomic(web, []byte(out)) == nil
		}
	}
	deadline := time.NewTimer(m.PlaylistWait)
	defer deadline.Stop()
	for {
		m.mu.Lock()
		done := s.done
		m.mu.Unlock()
		select {
		case <-done:
			if ready() {
				return p, nil
			}
			m.mu.Lock()
			if m.sessions[id] != s {
				m.mu.Unlock()
				return "", ErrNoSession
			}
			if s.done != done { // someone else already restarted it
				m.mu.Unlock()
				continue
			}
			if s.hw == "none" || s.hw == "" || s.d.VideoCopy || s.retried {
				msg := s.stderr.String()
				m.mu.Unlock()
				return "", fmt.Errorf("media: ffmpeg exited without a playlist: %s", msg)
			}
			s.retried = true
			log.Printf("media: hls %s: %s ffmpeg exited before the first segment, retrying in software: %s",
				id, s.hw, s.stderr.String())
			cleanDir(s.dir)
			err := m.launchLocked(s, "none")
			m.mu.Unlock()
			if err != nil {
				return "", err
			}
			continue
		case <-deadline.C:
			if ready() {
				return p, nil
			}
			return "", ErrNotReady
		case <-time.After(50 * time.Millisecond):
			if ready() {
				return p, nil
			}
		}
	}
}

// webPlaylist is the EVENT playlist without a remux's lead GOP (Playlist).
const webPlaylist = "web.m3u8"

// dropLead leaves out the first run's GOPs that end before the stream
// start, lead seconds after the run's start (a GOP mostly before it goes).
// false: nothing after them is listed yet.
func dropLead(playlist string, lead float64) (string, bool) {
	lines := strings.SplitAfter(playlist, "\n")
	var b strings.Builder
	t, dropped, trimming, listed := -lead, 0, true, false
	for i := 0; i < len(lines); i++ {
		body := strings.TrimSpace(lines[i])
		if v, ok := strings.CutPrefix(body, "#EXTINF:"); ok && i+1 < len(lines) {
			d, _ := strconv.ParseFloat(strings.SplitN(v, ",", 2)[0], 64)
			n, isSeg := segIndex(strings.TrimSpace(lines[i+1]))
			if trimming && isSeg && n < runSpan && t+d/2 < 0 {
				t += d
				dropped++
				i++ // its URI
				continue
			}
			trimming, listed = false, true
		}
		b.WriteString(lines[i])
	}
	out := b.String()
	if dropped > 0 {
		out = mediaSequence.ReplaceAllStringFunc(out, func(m string) string {
			n, _ := strconv.Atoi(m[len("#EXT-X-MEDIA-SEQUENCE:"):])
			return "#EXT-X-MEDIA-SEQUENCE:" + strconv.Itoa(n+dropped)
		})
	}
	return out, listed || strings.Contains(out, "#EXT-X-ENDLIST")
}

var mediaSequence = regexp.MustCompile(`#EXT-X-MEDIA-SEQUENCE:\d+`)

// writeAtomic replaces path with b (a reader never sees half of it).
func writeAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), path)
}

func cleanDir(dir string) {
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		os.Remove(filepath.Join(dir, e.Name()))
	}
}

// segIndex is the number of a segment file name (seg00042.ts → 42).
func segIndex(name string) (int, bool) {
	m := segmentName.FindStringSubmatch(name)
	if m == nil || m[2] == "" {
		return 0, false
	}
	n, err := strconv.Atoi(m[2])
	return n, err == nil
}

// Segment returns the path of a segment (or init.mp4) of the session. A
// request moves the player's position: ffmpeg resumes when it is no longer
// far ahead.
func (m *HLSManager) Segment(id, name string) (string, error) {
	s, err := m.get(id, true)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	s.lastSeg = m.Now()
	m.mu.Unlock()
	if mm := vsegName.FindStringSubmatch(name); mm != nil {
		m.mu.Lock()
		remux := s.remux != nil
		m.mu.Unlock()
		if !remux {
			return "", ErrNoSegment
		}
		k, _ := strconv.Atoi(mm[1])
		return m.remuxSegment(s, k, name)
	}
	if !segmentName.MatchString(name) {
		return "", ErrNoSegment
	}
	if idx, ok := segIndex(name); ok {
		m.mu.Lock()
		if idx < s.trimmed {
			m.mu.Unlock()
			return "", ErrSegmentGone
		}
		if s.paused && s.segs-1-idx <= m.AheadSegments/2 {
			m.resumeLocked(s)
		}
		complete := s.complete > 0
		m.mu.Unlock()
		if complete {
			// the complete playlist lists segments ffmpeg has not written yet:
			// far away, ffmpeg moves there; near, the request waits for it
			again, err := m.restartIfFar(s, idx, name)
			if err != nil {
				return "", err
			}
			if err := m.awaitListed(s, idx, name, again); err != nil {
				return "", err
			}
			m.checkDrift(s, idx, name)
		}
	}
	p := filepath.Join(s.dir, name)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNoSegment
	}
	if idx, ok := segIndex(name); ok {
		// only a served segment is the player's position (the sweep trims
		// behind it and pauses ffmpeg ahead of it)
		m.mu.Lock()
		s.reqSeg = idx
		m.mu.Unlock()
	}
	return p, nil
}

// Stop kills the session's ffmpeg, waits for it to exit and removes its files.
func (m *HLSManager) Stop(id string) {
	m.init()
	m.mu.Lock()
	s := m.sessions[id]
	delete(m.sessions, id)
	if s != nil {
		s.cancel()
	}
	m.mu.Unlock()
	if s != nil {
		reap([]*session{s}, 5*time.Second)
	}
}

// reap waits (up to wait in total) for the killed sessions' ffmpeg to exit
// and removes their dirs.
func reap(list []*session, wait time.Duration) {
	dl := time.NewTimer(wait)
	defer dl.Stop()
	for _, s := range list {
		select {
		case <-s.done:
		case <-dl.C:
		}
	}
	for _, s := range list {
		if err := os.RemoveAll(s.dir); err != nil {
			log.Printf("media: remove hls dir %s: %v", s.id, err)
		}
	}
}

// Close kills every ffmpeg at once and stops the idle sweeper. It returns
// within a few seconds whatever the sessions do (shutdown has a budget).
func (m *HLSManager) Close() {
	m.init()
	m.mu.Lock()
	m.closed = true
	list := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.sessions = map[string]*session{}
	m.mu.Unlock()
	m.rootStop() // cancels every ffmpeg's context: SIGKILL, paused or not
	reap(list, 3*time.Second)
}

// pid and sessionHW are for tests.
func (m *HLSManager) pid(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil && s.cmd.Process != nil {
		return s.cmd.Process.Pid
	}
	return 0
}

func (m *HLSManager) sessionHW(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil {
		return s.hw
	}
	return ""
}

func isHTTP(in string) bool {
	return strings.HasPrefix(in, "http://") || strings.HasPrefix(in, "https://")
}

// Throttle limits how fast ffmpeg reads its input: Rate× real time after
// Burst seconds read at full speed. The zero value reads at full speed.
type Throttle struct{ Rate, Burst float64 }

// BuildArgs assembles the ffmpeg command line (without the binary name).
// The audio track is d.AudioIndex (-1 = no audio), or the external file
// d.AudioInput (a second input seeked like the first).
func BuildArgs(hw, input string, d Decision, startSec float64, outDir string) []string {
	return BuildArgsThrottled(hw, input, d, startSec, outDir, Throttle{})
}

// BuildArgsThrottled is BuildArgs with a read rate limit (see Throttle).
func BuildArgsThrottled(hw, input string, d Decision, startSec float64, outDir string, th Throttle) []string {
	a := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	transcode := !d.VideoCopy
	burn := transcode && d.BurnSub > 0
	if transcode && !burn { // overlay runs on system frames: no GPU decode
		a = append(a, videoDecodeArgs(hw, d.VideoCodec, d.SourcePixFmt)...)
	}
	if startSec > 0 {
		a = append(a, "-ss", strconv.FormatFloat(startSec, 'f', -1, 64))
	}
	opts, operand, err := InputArgs(input)
	if err != nil {
		// Start refuses such inputs; never hand ffmpeg a LAN URL anyway.
		opts, operand = []string{"-protocol_whitelist", "file"}, "file:/dev/null"
	}
	a = append(a, opts...)
	if th.Rate > 0 {
		a = append(a, "-readrate", strconv.FormatFloat(th.Rate, 'f', -1, 64),
			"-readrate_initial_burst", strconv.FormatFloat(th.Burst, 'f', -1, 64))
	}
	if isHTTP(operand) {
		// The local stream: a read waits while pieces are missing and the
		// server keeps the request open as long as the source lives, so a
		// swarm stall must not end the stream (ffmpeg would write ENDLIST).
		// The I/O timeout is only a safety net; reconnects never give up.
		a = append(a, "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_on_network_error", "1",
			"-reconnect_delay_max", "30", "-reconnect_max_retries", "-1", "-reconnect_delay_total_max", "4294",
			"-rw_timeout", "600000000")
	}
	a = append(a, "-i", operand)
	if d.AudioInput != "" {
		// an external audio file (extaudio): the same timeline, so the same
		// seek; it comes from the same source as the video, file protocol only
		a = append(a, inputOffsetArgs(d.AudioInputOffsetMs)...)
		if startSec > 0 {
			// at a tempo the track runs faster or slower on its own time:
			// video time s is its time s·tempo (vofit's fit)
			ss := startSec
			if tempoOn(d.AudioInputTempo) {
				ss = math.Round(startSec*d.AudioInputTempo*1000) / 1000
			}
			a = append(a, "-ss", strconv.FormatFloat(ss, 'f', -1, 64))
		}
		a = append(a, AudioInputArgs(d.AudioInput)...)
	}
	if !burn {
		a = append(a, "-map", "0:v:0?")
	}
	if d.AudioInput != "" && d.AudioIndex >= 0 {
		a = append(a, "-map", "1:a:0")
	} else if d.AudioIndex >= 0 {
		a = append(a, "-map", "0:"+strconv.Itoa(d.AudioIndex))
	} else {
		a = append(a, "-an")
	}
	a = append(a, "-sn", "-dn")

	if !transcode {
		a = append(a, "-c:v", "copy")
		if normCodec(d.VideoCodec) == "hevc" {
			a = append(a, "-tag:v", "hvc1")
		}
	} else {
		enc := videoEncodeArgs(hw, d.VideoCodec, d.SourcePixFmt, d.TargetHeight, d.SourceHeight, d.MaxBitrate)
		if burn {
			enc = burnFilter(enc, d.BurnSub)
		}
		a = append(a, enc...)
		a = append(a, "-force_key_frames", "expr:gte(t,n_forced*"+strconv.Itoa(segmentSeconds)+")")
	}

	if d.AudioIndex >= 0 {
		if d.AudioCopy {
			a = append(a, "-c:a", "copy")
			if d.AudioInput != "" && startSec > 0 {
				// the external file's demuxer starts at the cue before the
				// seek (a Matroska cluster: seconds) and a copy keeps those
				// packets: the muxer would then move the whole stream's start
				// to them. Dropped, the track starts where the video does.
				a = append(a, "-bsf:a", `noise=drop=lt(pts\,0)`)
			}
		} else {
			if d.AudioInput != "" {
				a = append(a, atempoArgs(d.AudioInputTempo)...)
			}
			a = append(a, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
		}
	}

	a = append(a, "-f", "hls", "-hls_time", strconv.Itoa(segmentSeconds),
		"-hls_playlist_type", "event", "-hls_list_size", "0",
		"-hls_flags", "independent_segments")
	if d.FMP4 {
		a = append(a, "-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4")
		if !d.VideoCopy {
			// a transcode may be restarted at a far segment (hls_complete.go):
			// frag_discont puts -output_ts_offset into each fragment's tfdt
			// instead of an edit list in init.mp4, so every run writes the same
			// init.mp4 and the segments of all runs share one timeline
			a = append(a, "-hls_segment_options", "movflags=+frag_discont")
		}
		a = append(a, "-hls_segment_filename", filepath.Join(outDir, "seg%05d.m4s"))
	} else {
		a = append(a, "-hls_segment_type", "mpegts",
			"-hls_segment_filename", filepath.Join(outDir, "seg%05d.ts"))
	}
	return append(a, filepath.Join(outDir, "index.m3u8"))
}

// videoDecodeArgs are the input options for decoding a transcode's source
// on the GPU: qsv when the iGPU decodes the codec, videotoolbox on macOS.
func videoDecodeArgs(hw, codec, pixFmt string) []string {
	switch {
	case hw == "qsv" && qsvDecodable(codec, pixFmt):
		return []string{"-hwaccel", "qsv", "-hwaccel_output_format", "qsv"}
	case hw == "videotoolbox":
		return []string{"-hwaccel", "videotoolbox"}
	}
	return nil
}

// videoEncodeArgs are the output options re-encoding the video to H.264
// (filter, encoder, rate control) at targetHeight (0 = the source's),
// capped by maxBitrate (0 = none). HLS and device copies share them.
func videoEncodeArgs(hw, codec, pixFmt string, targetHeight, sourceHeight int, maxBitrate int64) []string {
	qsvDecode := hw == "qsv" && qsvDecodable(codec, pixFmt)
	var a []string
	h := targetHeight &^ 1 // encoders need even dimensions
	rate := []string{}
	if maxBitrate > 0 {
		rate = []string{"-maxrate", strconv.FormatInt(maxBitrate, 10), "-bufsize", strconv.FormatInt(2*maxBitrate, 10)}
	}
	switch hw {
	case "qsv":
		// Frames stay on the iGPU when it decoded them; vpp_qsv scales and
		// converts 10-bit to nv12 (no tone mapping: HDR comes out washed
		// out). Otherwise software decode feeds nv12 system frames.
		vf := scaleFilter(h, "nv12")
		if qsvDecode {
			vf = "vpp_qsv=format=nv12"
			if h > 0 {
				vf = "vpp_qsv=w=trunc(oh*a/2)*2:h=" + strconv.Itoa(h) + ":format=nv12"
			}
		}
		// Alder Lake + iHD only opens the encoder in low-power (VDEnc)
		// mode with bitrate or CQP rate control; ICQ (-global_quality) fails.
		br, maxrate := qsvBitrate(h, sourceHeight, maxBitrate)
		// passthrough: with vpp_qsv scaling the default CFR sync
		// duplicated ~40 frames at the start (picture behind the sound).
		a = append(a, "-vf", vf, "-fps_mode", "passthrough", "-c:v", "h264_qsv", "-low_power", "1", "-preset", "veryfast",
			"-b:v", strconv.FormatInt(br, 10), "-maxrate", strconv.FormatInt(maxrate, 10),
			"-bufsize", strconv.FormatInt(2*maxrate, 10), "-forced_idr", "1")
		rate = nil
	case "videotoolbox":
		br := "6M"
		if maxBitrate > 0 && maxBitrate < 6_000_000 {
			br = strconv.FormatInt(maxBitrate, 10)
		}
		a = append(a, "-vf", swFilter(h), "-c:v", "h264_videotoolbox", "-b:v", br)
	default:
		a = append(a, "-vf", swFilter(h), "-c:v", "libx264", "-preset", "veryfast",
			"-crf", "23", "-sc_threshold", "0")
	}
	return append(a, rate...)
}

func swFilter(h int) string { return scaleFilter(h, "yuv420p") }

func scaleFilter(h int, pixFmt string) string {
	if h > 0 {
		return "scale=-2:" + strconv.Itoa(h) + ",format=" + pixFmt
	}
	return "format=" + pixFmt
}

// qsvDecodable reports whether the Alder Lake iGPU decodes the source:
// H.264 8-bit, HEVC/VP9/AV1 8- and 10-bit, 4:2:0 only. Unknown pixel
// formats are assumed 8-bit 4:2:0.
func qsvDecodable(codec, pixFmt string) bool {
	switch pixFmt {
	case "", "yuv420p", "yuvj420p", "nv12":
	case "yuv420p10le", "p010le":
		if normCodec(codec) == "h264" {
			return false
		}
	default:
		return false
	}
	switch normCodec(codec) {
	case "h264", "hevc", "vp9", "av1":
		return true
	}
	return false
}

// qsvBitrate picks the H.264 target bitrate for the output height (the
// target, else the source; unknown = 1080p) and the VBV max rate, both
// capped by the client's limit.
func qsvBitrate(target, source int, limit int64) (br, maxrate int64) {
	h := target
	if h <= 0 {
		h = source
	}
	switch {
	case h <= 0:
		br = 8_000_000
	case h <= 480:
		br = 2_000_000
	case h <= 720:
		br = 4_000_000
	case h <= 1080:
		br = 8_000_000
	case h <= 1440:
		br = 12_000_000
	default:
		br = 20_000_000
	}
	maxrate = br * 3 / 2
	if limit > 0 {
		br, maxrate = min(br, limit), min(maxrate, limit)
	}
	return br, maxrate
}

// tailBuf keeps the last max bytes written to it.
type tailBuf struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

// reSecretParam matches the internal stream token in URLs ffmpeg prints.
var reSecretParam = regexp.MustCompile(`([?&]k=)[^&\s"']+`)

// String returns the tail with URL tokens redacted (it ends up in logs).
func (t *tailBuf) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return reSecretParam.ReplaceAllString(strings.TrimSpace(string(t.b)), "${1}…")
}

// burnFilter moves the -vf chain of the encoder options into a
// filter_complex that first overlays the image subtitle stream sub.
func burnFilter(enc []string, sub int) []string {
	out := make([]string, 0, len(enc)+4)
	chain := "null"
	for i := 0; i < len(enc); i++ {
		if enc[i] == "-vf" && i+1 < len(enc) {
			chain = enc[i+1]
			i++
			continue
		}
		out = append(out, enc[i])
	}
	fc := "[0:v:0][0:" + strconv.Itoa(sub) + "]overlay=eof_action=pass:format=auto," + chain + "[v]"
	return append([]string{"-filter_complex", fc, "-map", "[v]"}, out...)
}
