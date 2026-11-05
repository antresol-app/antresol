package media

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A remux (copied video) gets a complete VOD playlist too, built from the
// container's keyframe index (KeyIndex): segment k covers [B_k, B_k+1),
// where B_k is the first keyframe at or after each 6 s step from the stream
// start, so every EXTINF is exact.
//
// ffmpeg cannot be told where to cut a copied video, so it is told to cut at
// every keyframe (-hls_time 0.001: one file per GOP, the same files whatever
// the start). A listed segment is assembled from consecutive GOP files of
// one run, from the one starting at B_k to the one before B_k+1; a GOP
// file's start is its first frame's timestamp (gop_pts.go; ffmpeg's EXTINFs
// follow an open GOP's reordered B-frames, not the keyframes): MPEG-TS files are
// concatenated, fMP4 fragments keep their moof/mdat pairs (styp/sidx of
// the GOP files dropped). A keyframe the index does not list (sparse
// Matroska cues) just adds a GOP inside a range. A request far from what
// ffmpeg has written restarts it at that segment's keyframe (as for a
// transcode). The web player keeps ffmpeg's own per-GOP EVENT playlist.

// gopTolerance: a GOP's place on the timeline and a segment boundary match
// within this. A GOP the index lists is placed exactly on its keyframe
// (snapKeyframe): ffmpeg's copy of an open-GOP Matroska file without DTS
// moves each GOP's timestamps by a few frames (the cuts stay on the same
// frames), so its timestamps only find the keyframe.
const gopTolerance = 0.05

// snapLimit: a GOP's timestamp finds its indexed keyframe within this (and
// within less than half the gap to the neighbouring keyframes): above
// ffmpeg's timestamp error (≤ 0.13 s measured), below the distance to a
// keyframe sparse Cues leave out.
const snapLimit = 0.2

// runSpan separates the GOP file numbers of ffmpeg runs (run r writes
// seg<r*runSpan + i>), so a restart never reuses a name.
const runSpan = 100_000

// remuxSliver: a last segment shorter than this joins the one before it.
const remuxSliver = 0.5

// vsegName is a listed segment of a remux's complete playlist.
var vsegName = regexp.MustCompile(`^vseg(\d{5})\.(ts|m4s)$`)

// remuxGrid: the listed segments of a remux, relative to the stream start.
type remuxGrid struct {
	bounds []float64 // segment k starts at bounds[k]
	end    float64   // the film's end
}

func (g *remuxGrid) segEnd(k int) float64 {
	if k+1 < len(g.bounds) {
		return g.bounds[k+1]
	}
	return g.end
}

// newRemuxGrid lays the segments on the index's keyframes from streamStart
// (the keyframe the stream starts on) to duration; false = unusable.
func newRemuxGrid(ki KeyIndex, streamStart, duration float64) (*remuxGrid, bool) {
	if duration <= 0 {
		duration = ki.Duration
	}
	end := duration - streamStart
	if end < 1 {
		return nil, false
	}
	g := &remuxGrid{bounds: []float64{0}, end: end}
	next := float64(segmentSeconds)
	for _, k := range ki.Keyframes {
		rel := k - streamStart
		if rel < next-1e-6 || rel >= end-remuxSliver {
			continue
		}
		g.bounds = append(g.bounds, rel)
		next = (math.Floor(rel/segmentSeconds+1e-9) + 1) * segmentSeconds
	}
	for k := range g.bounds {
		if g.segEnd(k)-g.bounds[k] > maxKeyframeGap+segmentSeconds {
			return nil, false
		}
	}
	return g, true
}

func (g *remuxGrid) playlist(fmp4 bool) string {
	var b strings.Builder
	target := segmentSeconds
	for k := range g.bounds {
		target = max(target, int(g.segEnd(k)-g.bounds[k]+0.5))
	}
	version, ext := 3, "ts"
	if fmp4 {
		version, ext = 7, "m4s"
	}
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:%d\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n", version, target)
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	if fmp4 {
		b.WriteString("#EXT-X-MAP:URI=\"init.mp4\"\n")
	}
	for k := range g.bounds {
		fmt.Fprintf(&b, "#EXTINF:%.6f,\nvseg%05d.%s\n", g.segEnd(k)-g.bounds[k], k, ext)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// segmentAt is the listed segment containing rel.
func (g *remuxGrid) segmentAt(rel float64) int {
	return max(sort.Search(len(g.bounds), func(i int) bool { return g.bounds[i] > rel+1e-6 })-1, 0)
}

// runList is what one ffmpeg run listed, in order.
type runList struct {
	names []string
	ended bool
}

// remuxState is a remux session's grid and what its ffmpeg runs wrote.
// Guarded by HLSManager.mu.
type remuxState struct {
	grid      *remuxGrid
	streamAbs float64   // the stream start on the file's timeline
	run       int       // the running ffmpeg's number
	runStart  float64   // where it started (relative; a keyframe)
	runs      []runList // what earlier runs listed (files may be trimmed)
	runStarts []float64 // where each run started (relative; run r writes seg<r*runSpan+i>)
	// starts: a GOP file's first frame on the stream's timeline; calib: per
	// run, its timestamp offset (see calibrateRun)
	starts  map[string]float64
	raw     map[string]float64 // before snapping
	calib   map[int]float64
	video   fmp4Video // fMP4: the video track (from init.mp4)
	reqVSeg int       // the last listed segment served
	// restartedFor: the listed segment the running ffmpeg was restarted
	// for, until a segment is served (-1: none). A segment that run passes
	// without assembling it does not match any GOP; retrying it must not
	// restart ffmpeg again and again.
	restartedFor int
	waiting      int // requests waiting for GOPs (ffmpeg is not paused meanwhile)
	drifted      bool
	keyframes    []float64 // absolute, for the restart's -ss
	// margin lifts every run's timestamps: our margin plus how far before
	// the stream start the first run begins (a keyframe early), so no run's
	// timestamps start below 0 (ffmpeg would shift such a run as a whole)
	margin float64
	// gopsPerStep: the film's keyframes per 6 s (the sweep's limits count
	// 6 s segments, a remux's files are GOPs)
	gopsPerStep float64
	// firstSS: the first run's -ss. Every run seeks (even at 0) and offsets
	// its timestamps by its -ss minus the stream start (plus a margin), so a
	// keyframe gets the same timestamp in every run.
	firstSS float64
}

// remuxArgs makes ffmpeg write one file per GOP, numbered for its run, with
// timestamps continuing the stream's timeline after a restart.
func remuxArgs(args []string, d Decision, run int, offset float64) []string {
	out := make([]string, 0, len(args)+12)
	for i := 0; i < len(args)-1; i++ {
		out = append(out, args[i])
		if args[i] == "-hls_time" && i+1 < len(args)-1 {
			out = append(out, "0.001")
			i++
		}
	}
	if d.FMP4 {
		// the offset goes into each fragment's tfdt (same init.mp4 for every run)
		out = append(out, "-hls_segment_options", "movflags=+frag_discont")
	} else {
		// the MPEG-TS muxer delays every timestamp by twice -muxdelay
		// (tsMuxDelay, which calibration expects): pinned, not ffmpeg's default
		out = append(out, "-muxdelay", strconv.FormatFloat(tsMuxMaxDelay, 'f', -1, 64),
			"-muxpreload", strconv.FormatFloat(tsMuxPreload, 'f', -1, 64))
	}
	// every run: a keyframe's timestamp is its distance from the stream
	// start plus a margin (never negative, which ffmpeg would shift a whole run for)
	out = append(out, "-output_ts_offset", strconv.FormatFloat(offset, 'f', 6, 64))
	if run > 0 {
		out = append(out, "-start_number", strconv.Itoa(run*runSpan))
	}
	return append(out, args[len(args)-1])
}

// currentRun reads the running ffmpeg's playlist.
func currentRun(dir string) runList {
	b, err := os.ReadFile(filepath.Join(dir, "index.m3u8"))
	if err != nil {
		return runList{}
	}
	var r runList
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "#EXT-X-ENDLIST":
			r.ended = true
		case line != "" && !strings.HasPrefix(line, "#"):
			r.names = append(r.names, line)
		}
	}
	return r
}

// filePTS is the timestamp of a GOP file's first frame.
func (m *HLSManager) filePTS(s *session, name string) (float64, error) {
	p := filepath.Join(s.dir, name)
	if !s.d.FMP4 {
		return tsFirstVideoPTS(p)
	}
	m.mu.Lock()
	v := s.remux.video
	m.mu.Unlock()
	if v.scale == 0 {
		var err error
		if v, err = readFMP4Video(filepath.Join(s.dir, "init.mp4")); err != nil {
			return 0, err
		}
		m.mu.Lock()
		s.remux.video = v
		m.mu.Unlock()
	}
	return fmp4FirstPTS(p, v)
}

// runOf is the ffmpeg run that wrote a GOP file, and whether the file is
// that run's first.
func runOf(name string) (run int, first bool) {
	idx, _ := segIndex(name)
	return idx / runSpan, idx%runSpan == 0
}

// errNotCalibrated: the run has not written enough GOPs to place them yet.
var errNotCalibrated = errors.New("media: remux run not calibrated yet")

// gopStart is GOP file l.names[i]'s start on the stream's timeline (cached).
// A run's first file is its start keyframe by construction; the others are
// their first frame's timestamp minus the run's offset, snapped to the
// indexed keyframe it lands on, unless a neighbouring GOP lies closer to
// that keyframe (two GOPs can be closer together than ffmpeg's timestamp
// error, and sparse Cues may list only one of them).
func (m *HLSManager) gopStart(s *session, l runList, i int) (float64, error) {
	name := l.names[i]
	m.mu.Lock()
	at, ok := s.remux.starts[name]
	m.mu.Unlock()
	if ok {
		return at, nil
	}
	r, err := m.rawStart(s, l, i)
	if err != nil {
		return 0, err
	}
	kf, ok := m.nearestKeyframe(s, r)
	if !ok {
		m.cacheStart(s, name, r)
		return r, nil
	}
	final := true
	for _, j := range []int{i - 1, i + 1} {
		if j < 0 {
			continue
		}
		if j >= len(l.names) {
			final = l.ended // the next GOP may still come, closer to kf
			continue
		}
		rj, err := m.rawStart(s, l, j)
		if err != nil {
			continue
		}
		if math.Abs(rj-kf) < math.Abs(r-kf) {
			if final {
				m.cacheStart(s, name, r)
			}
			return r, nil // the keyframe is the neighbour's
		}
	}
	if !final {
		// provisional: the next GOP may lie closer to kf (an unindexed
		// keyframe just before an indexed one). Placing this one on kf now
		// would let a segment ending at kf leave it out, and the next
		// segment start after it: the GOP would be in neither.
		return 0, errProvisional
	}
	m.cacheStart(s, name, kf)
	return kf, nil
}

// errProvisional: a GOP's place depends on the GOP ffmpeg has not listed yet.
var errProvisional = errors.New("media: remux GOP placement waits for the next GOP")

func (m *HLSManager) cacheStart(s *session, name string, at float64) {
	m.mu.Lock()
	s.remux.starts[name] = at
	m.mu.Unlock()
}

// rawStart is a GOP file's start from its timestamp (cached).
func (m *HLSManager) rawStart(s *session, l runList, i int) (float64, error) {
	name := l.names[i]
	m.mu.Lock()
	rs := s.remux
	at, ok := rs.raw[name]
	r, _ := runOf(name)
	c, calibrated := rs.calib[r]
	m.mu.Unlock()
	if ok {
		return at, nil
	}
	if !calibrated {
		var err error
		if c, err = m.calibrateRun(s, l, r); err != nil {
			return 0, err
		}
	}
	pts, err := m.filePTS(s, name)
	if err != nil {
		return 0, err
	}
	at = pts - c
	m.mu.Lock()
	rs.raw[name] = at
	m.mu.Unlock()
	return at, nil
}

// calibrateRun finds a run's timestamp offset (a GOP's place = its first
// frame's timestamp minus it). ffmpeg's timestamps for a copied stream are
// its own (an MPEG-TS mux delay, our margin, an open-GOP shift of a few
// frames), and where a run really starts is ffmpeg's seek's choice. So the
// first run's offset is a consensus: of the offsets that put one of its
// first GOPs on a nearby indexed keyframe, the one under which most of
// its GOPs land on indexed keyframes. The offset is the same for every
// run (the same muxer settings and margin); a later run refines it by the
// median over its GOPs.
func (m *HLSManager) calibrateRun(s *session, l runList, r int) (float64, error) {
	n := min(len(l.names), 12)
	if n < 2 && !l.ended {
		return 0, errNotCalibrated
	}
	if n == 0 {
		return 0, errNotCalibrated
	}
	pts := make([]float64, n)
	for j := range n {
		p, err := m.filePTS(s, l.names[j])
		if err != nil {
			return 0, err
		}
		pts[j] = p
	}
	m.mu.Lock()
	rs := s.remux
	base, known := rs.calib[0]
	m.mu.Unlock()
	if r == 0 || !known {
		var err error
		if base, err = m.consensusOffset(s, pts); err != nil {
			return 0, err
		}
	}
	// refine: the median offset of the GOPs that land near a keyframe
	var found []float64
	for _, p := range pts {
		if kf, ok := m.nearestKeyframe(s, p-base); ok {
			found = append(found, p-kf)
		}
	}
	c := base
	if len(found) > 0 {
		sort.Float64s(found)
		c = found[len(found)/2]
	}
	m.mu.Lock()
	rs.calib[r] = c
	m.mu.Unlock()
	return c, nil
}

// tsMuxMaxDelay, tsMuxPreload: the -muxdelay/-muxpreload a TS remux pins
// (ffmpeg's defaults). The MPEG-TS muxer adds 2 × max_delay to every
// timestamp (mpegtsenc.c; preload does not move them), so a TS run's
// timestamps start tsMuxDelay late (1.4 s, measured on ffmpeg 7.1 and 9.0).
const (
	tsMuxMaxDelay = 0.7
	tsMuxPreload  = 0.5
	tsMuxDelay    = 2 * tsMuxMaxDelay
)

// consensusOffset is the offset under which most of the GOP timestamps
// land on indexed keyframes. Candidates pair each of the first GOPs with
// the keyframes within 0.5 s of where the expected offset (our margin, plus
// the TS mux delay) puts it: less than half a GOP, so regularly spaced
// GOPs cannot fit one GOP off.
func (m *HLSManager) consensusOffset(s *session, pts []float64) (float64, error) {
	m.mu.Lock()
	keys, streamAbs := s.remux.keyframes, s.remux.streamAbs
	m.mu.Unlock()
	m.mu.Lock()
	expected := s.remux.margin
	m.mu.Unlock()
	if !s.d.FMP4 {
		expected += tsMuxDelay
	}
	best, bestScore := math.NaN(), -1
	for j := 0; j < min(len(pts), 3); j++ {
		guess := streamAbs + pts[j] - expected
		lo := sort.SearchFloat64s(keys, guess-0.5)
		for k := lo; k < len(keys) && keys[k] <= guess+0.5; k++ {
			c := pts[j] - (keys[k] - streamAbs)
			score := 0
			for _, p := range pts {
				if _, ok := m.nearestKeyframe(s, p-c); ok {
					score++
				}
			}
			if score > bestScore || (score == bestScore && math.Abs(c-expected) < math.Abs(best-expected)) {
				best, bestScore = c, score
			}
		}
	}
	if math.IsNaN(best) {
		return 0, fmt.Errorf("%w: no keyframe near the first GOPs", errNoPTS)
	}
	return best, nil
}

// nearestKeyframe is the indexed keyframe (relative) a GOP's timestamp
// lands on: within snapLimit and less than half the gap to its neighbours.
func (m *HLSManager) nearestKeyframe(s *session, rel float64) (float64, bool) {
	m.mu.Lock()
	keys, streamAbs := s.remux.keyframes, s.remux.streamAbs
	m.mu.Unlock()
	abs := streamAbs + rel
	i := sort.SearchFloat64s(keys, abs)
	n := -1
	for _, c := range []int{i - 1, i} {
		if c >= 0 && c < len(keys) && (n < 0 || math.Abs(keys[c]-abs) < math.Abs(keys[n]-abs)) {
			n = c
		}
	}
	if n < 0 {
		return 0, false
	}
	limit := snapLimit
	if n > 0 {
		limit = min(limit, (keys[n]-keys[n-1])/2)
	}
	if n+1 < len(keys) {
		limit = min(limit, (keys[n+1]-keys[n])/2)
	}
	if math.Abs(keys[n]-abs) < limit {
		return keys[n] - streamAbs, true
	}
	return 0, false
}

// remuxParts are the GOP files of listed segment k in order (consecutive
// files of one run, from B_k to before B_k+1), or false when they are not
// all written yet (or a file is gone).
func (m *HLSManager) remuxParts(s *session, k int) ([]string, bool) {
	m.mu.Lock()
	rs := s.remux
	lists := append(append([]runList{}, rs.runs...), runList{})
	done := isDone(s.done)
	from, to := rs.grid.bounds[k], rs.grid.segEnd(k)
	last := k == len(rs.grid.bounds)-1
	m.mu.Unlock()
	cur := currentRun(s.dir)
	cur.ended = cur.ended || done
	lists[len(lists)-1] = cur
	for li := len(lists) - 1; li >= 0; li-- {
		l := lists[li]
		start := func(i int) float64 {
			at, err := m.gopStart(s, l, i)
			if err != nil {
				return math.NaN()
			}
			return at
		}
		// the first GOP at or after from (starts grow within a run)
		i := sort.Search(len(l.names), func(i int) bool {
			at := start(i)
			return math.IsNaN(at) || at >= from-gopTolerance
		})
		if i == len(l.names) || math.IsNaN(start(i)) || math.Abs(start(i)-from) > gopTolerance {
			continue
		}
		j := i + 1
		for ; j < len(l.names); j++ {
			at := start(j)
			if math.IsNaN(at) {
				return nil, false
			}
			if at >= to-gopTolerance {
				break
			}
		}
		if j == len(l.names) {
			// the run has not reached the segment's end (or it is the film's
			// last segment and the run is not done)
			if !(last && l.ended) {
				return nil, false
			}
		} else if math.Abs(start(j)-to) > gopTolerance {
			m.logRemuxDrift(s, k, start(j)-from)
		}
		parts := l.names[i:j]
		for _, p := range parts {
			if _, err := os.Stat(filepath.Join(s.dir, p)); err != nil {
				return nil, false
			}
		}
		return parts, true
	}
	return nil, false
}

// coveredEnd is where the running ffmpeg's GOPs reach (relative): the
// last placed GOP's start (the last written one's place may wait for the
// next GOP: then the one before it), or the film's end once ffmpeg finished.
func (m *HLSManager) coveredEnd(s *session) float64 {
	m.mu.Lock()
	runStart, end := s.remux.runStart, s.remux.grid.end
	m.mu.Unlock()
	cur := currentRun(s.dir)
	if cur.ended {
		return end
	}
	if len(cur.names) == 0 {
		return runStart
	}
	last := len(cur.names) - 1
	at, err := m.gopStart(s, cur, last)
	if errors.Is(err, errProvisional) && last > 0 {
		at, err = m.gopStart(s, cur, last-1)
	}
	if err != nil {
		return runStart
	}
	return at
}

// assemble writes listed segment k from its GOP files (atomically).
func assemble(dir, name string, parts []string, fmp4 bool) error {
	tmp, err := os.CreateTemp(dir, ".vseg-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriterSize(tmp, 1<<20)
	for i, p := range parts {
		b, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil {
			tmp.Close()
			return err
		}
		if fmp4 {
			b, err = fragmentBoxes(b, i == 0)
			if err != nil {
				tmp.Close()
				return fmt.Errorf("%s: %w", p, err)
			}
		}
		if _, err := w.Write(b); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

// fragmentBoxes keeps a GOP file's moof/mdat pairs (and the first part's
// styp): its sidx describes the GOP alone, a second styp would sit mid-segment.
func fragmentBoxes(b []byte, first bool) ([]byte, error) {
	var out []byte
	for off := 0; off < len(b); {
		if off+8 > len(b) {
			return nil, fmt.Errorf("truncated box at %d", off)
		}
		size := int(binary.BigEndian.Uint32(b[off:]))
		typ := string(b[off+4 : off+8])
		if size == 1 {
			if off+16 > len(b) {
				return nil, fmt.Errorf("truncated box at %d", off)
			}
			size = int(binary.BigEndian.Uint64(b[off+8:]))
		} else if size == 0 {
			size = len(b) - off
		}
		if size < 8 || off+size > len(b) {
			return nil, fmt.Errorf("bad box %q at %d", typ, off)
		}
		if typ != "sidx" && (typ != "styp" || first) {
			out = append(out, b[off:off+size]...)
		}
		off += size
	}
	return out, nil
}

// remuxSegment serves listed segment k of a remux: assembled once from its
// GOP files; a request far from what ffmpeg writes restarts it there.
func (m *HLSManager) remuxSegment(s *session, k int, name string) (string, error) {
	m.mu.Lock()
	rs := s.remux
	if k >= len(rs.grid.bounds) {
		m.mu.Unlock()
		return "", ErrNoSegment
	}
	rs.waiting++
	if s.paused {
		m.resumeLocked(s)
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		rs.waiting--
		m.mu.Unlock()
	}()
	out := filepath.Join(s.dir, name)
	if _, err := os.Stat(out); err == nil {
		m.servedVSeg(s, k, nil)
		return out, nil
	}
	deadline := time.Now().Add(m.SegmentWait)
	checkedFar, forced := false, false
	for {
		m.mu.Lock()
		gone, restarting, done := m.sessions[s.id] != s, s.restarting, isDone(s.done)
		m.mu.Unlock()
		if gone {
			return "", ErrNoSession
		}
		if parts, ok := m.remuxParts(s, k); ok {
			if _, err := os.Stat(out); err != nil {
				if err := assemble(s.dir, name, parts, s.d.FMP4); err != nil {
					log.Printf("media: hls %s: assemble %s: %v", s.id, name, err)
					return "", ErrNoSegment
				}
			}
			m.servedVSeg(s, k, parts)
			return out, nil
		}
		if !restarting && !checkedFar {
			checkedFar = true
			restarted, err := m.remuxRestartIfFar(s, k)
			if err != nil {
				return "", err
			}
			if restarted {
				deadline = time.Now().Add(m.SegmentWait)
				continue
			}
		}
		if done && !restarting {
			// ffmpeg ended without this segment's GOPs: once, a run made for it
			if forced {
				return "", ErrNoSegment
			}
			forced = true
			restarted, err := m.forceRemuxRestart(s, k)
			if err != nil {
				return "", err
			}
			if !restarted {
				return "", ErrNoSegment
			}
			deadline = time.Now().Add(m.SegmentWait)
			continue
		}
		if !time.Now().Before(deadline) {
			return "", ErrNotReady
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// servedVSeg moves the player's position: the listed segment, and the last
// GOP file in it (the sweep counts GOP files).
func (m *HLSManager) servedVSeg(s *session, k int, parts []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.remux.reqVSeg = k
	s.remux.restartedFor = -1
	if n := len(parts); n > 0 {
		if idx, ok := segIndex(parts[n-1]); ok {
			s.reqSeg = idx
		}
	}
}

// gops converts a count of ~6 s segments into GOP files.
func (rs *remuxState) gops(segments int) int {
	if segments <= 0 {
		return segments
	}
	return max(1, int(math.Ceil(float64(segments)*rs.gopsPerStep)))
}

// nearWaitSeconds: the longest a request waits for a throttled remux
// before a restart is the quicker way (see remuxRestartIfFar).
const nearWaitSeconds = 1.0

// remuxRestartIfFar restarts ffmpeg at segment k when k starts before the
// running ffmpeg's first GOP or more than farAhead segments past its last
// (unless its burst, still read at full speed, reaches k), or when the running ffmpeg has passed k but k cannot be assembled (a
// rewind behind the sweep, which deleted its GOP files) and the running
// ffmpeg was not started for k (then k's boundaries match no GOP: a restart
// would fail the same way).
func (m *HLSManager) remuxRestartIfFar(s *session, k int) (bool, error) {
	m.mu.Lock()
	rs := s.remux
	from, to := rs.grid.bounds[k], rs.grid.segEnd(k)
	runStart, restartedFor := rs.runStart, rs.restartedFor
	m.mu.Unlock()
	covered := m.coveredEnd(s)
	// past its burst ffmpeg reads at ReadRate× real time: a segment that
	// would take it more than ~1 s to finish (a 6 s one: 3 s at 2×) is
	// quicker from a restart, which brings a fresh burst (~0.4 s)
	throttled := m.ReadRate > 0 && covered-runStart >= m.ReadBurst
	slow := throttled && to > covered+m.ReadRate*nearWaitSeconds
	// within its burst ffmpeg reads at full speed: a segment the burst
	// reaches comes in a moment (hls.js asks for the first segments back
	// to back before anything is listed; restarting for each thrashed)
	inBurst := m.ReadRate > 0 && !throttled && to <= runStart+m.ReadBurst
	ahead := from > covered+float64(farAhead*segmentSeconds) && !inBurst
	far := from < runStart-gopTolerance || ahead || slow ||
		to < covered-gopTolerance && restartedFor != k // passed, yet not assemblable: its files are gone
	if !far {
		return false, nil
	}
	return m.forceRemuxRestart(s, k)
}

// forceRemuxRestart restarts ffmpeg at listed segment k's keyframe.
func (m *HLSManager) forceRemuxRestart(s *session, k int) (bool, error) {
	m.mu.Lock()
	if m.sessions[s.id] != s {
		m.mu.Unlock()
		return false, ErrNoSession
	}
	if s.restarting {
		m.mu.Unlock()
		return false, nil
	}
	s.restarting = true
	old := s.done
	s.cancel()
	m.mu.Unlock()
	select {
	case <-old:
	case <-time.After(restartExitWait):
	}
	finished := currentRun(s.dir) // what the stopped run listed (its files stay until trimmed)
	m.mu.Lock()
	defer m.mu.Unlock()
	s.restarting = false
	if m.sessions[s.id] != s {
		return false, ErrNoSession
	}
	if s.done != old || !isDone(old) {
		return false, nil
	}
	rs := s.remux
	finished.ended = true
	rs.runs = append(rs.runs, finished)
	os.Remove(filepath.Join(s.dir, "index.m3u8"))
	rs.run++
	// the run starts a keyframe before the segment's (one it can really seek
	// to): a run's first GOP comes out shifted by the reorder delay, so it
	// must not be one the player gets
	rs.runStart = rs.keyframeBefore(rs.streamAbs+rs.grid.bounds[k]) - rs.streamAbs
	rs.runStarts = append(rs.runStarts, rs.runStart)
	rs.restartedFor = k
	log.Printf("media: hls %s: request for segment %d (%.1f s): restarting the remux there", s.id, k, rs.runStart)
	return true, m.launchLocked(s, s.hw)
}

// remuxStart is the -ss of the running ffmpeg: a little after the keyframe
// it must start on (ffmpeg's B-frame heuristic seeks up to 3/23 s before
// -ss), less when the next keyframe is that close.
func (rs *remuxState) remuxStart() float64 {
	if rs.run == 0 {
		return rs.firstSS
	}
	return rs.ssAt(rs.streamAbs + rs.runStart)
}

func (rs *remuxState) ssAt(kf float64) float64 {
	lead := keyframeLeadSec
	i := sort.SearchFloat64s(rs.keyframes, kf+1e-6)
	if i < len(rs.keyframes) {
		lead = min(lead, (rs.keyframes[i]-kf)/2)
	}
	return kf + lead
}

// keyframeLeadSec mirrors the API's keyframeLead: ffmpeg's copy seek lands
// on the keyframe before -ss unless -ss is more than ~3/23 s past it.
const keyframeLeadSec = 0.2

// remuxTSMargin lifts every run's timestamps by a second: a run whose first
// keyframe's DTS would be negative (B-frames) is shifted as a whole by
// ffmpeg, the others are not, and the runs would disagree.
const remuxTSMargin = 1.0

// keyframeBefore is the latest seekable keyframe before kf (kf itself
// when there is none: the film's first).
func (rs *remuxState) keyframeBefore(kf float64) float64 {
	if i := sort.SearchFloat64s(rs.keyframes, kf-1e-3); i > 0 {
		return rs.safeKeyframe(rs.keyframes[i-1])
	}
	return kf
}

// safeKeyframe is the latest keyframe at or before kf whose next keyframe
// is far enough for the lead (a restart then really starts on it).
func (rs *remuxState) safeKeyframe(kf float64) float64 {
	i := sort.SearchFloat64s(rs.keyframes, kf+1e-6) - 1
	for n := 0; i >= 0 && n < 20; i, n = i-1, n+1 {
		if i+1 >= len(rs.keyframes) || rs.keyframes[i+1]-rs.keyframes[i] >= keyframeLeadSec+0.1 {
			return rs.keyframes[i]
		}
	}
	return kf
}

// logRemuxDrift logs, once per session, a listed segment whose next GOP
// does not start where the index said (an index that disagrees with the
// file, or a restart that did not land on its keyframe).
func (m *HLSManager) logRemuxDrift(s *session, k int, got float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs := s.remux
	if rs.drifted {
		return
	}
	rs.drifted = true
	want := rs.grid.segEnd(k) - rs.grid.bounds[k]
	log.Printf("media: hls %s (remux): segment %d is %.3f s, the complete playlist lists %.3f s", s.id, k, got, want)
}

// trimRemux removes assembled segments far behind the player.
func trimRemux(dir string, reqVSeg, keep int) {
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		mm := vsegName.FindStringSubmatch(e.Name())
		if mm == nil {
			continue
		}
		if k, _ := strconv.Atoi(mm[1]); k < reqVSeg-keep {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// remuxFor is a copied video's remux state when its container has a usable
// keyframe index, else nil (the EVENT playlist). A failure is logged once
// per file (the cache remembers it).
func (m *HLSManager) remuxFor(d Decision, o StartOptions) *remuxState {
	if !d.VideoCopy || d.VideoCodec == "" || isHTTP(o.Input) || o.DurationSec <= 0 {
		return nil
	}
	if _, err := os.Stat(o.Input); err != nil {
		return nil
	}
	start := time.Now()
	ki, built, err := KeyIndexCache{Dir: m.IndexDir}.Get(o.Input)
	if err != nil {
		if built {
			log.Printf("media: %s: no complete playlist for the remux (EVENT instead): %v", filepath.Base(o.Input), err)
		}
		return nil
	}
	if built {
		log.Printf("media: %s: keyframe index of %d keyframes read in %v", filepath.Base(o.Input), len(ki.Keyframes), time.Since(start).Round(time.Millisecond))
	}
	streamStart := max(o.StreamStart, 0)
	if streamStart == 0 {
		// copy starts on the keyframe at or before -ss, and a film whose
		// first keyframe is a little after 0 starts there
		streamStart = ki.Keyframes[0]
		if i := sort.SearchFloat64s(ki.Keyframes, o.StartSec+1e-6); i > 0 {
			streamStart = ki.Keyframes[i-1]
		}
	} else if !indexed(ki.Keyframes, streamStart) {
		// the stream starts on a keyframe the index does not know (the API's
		// ffprobe found it): segment 0 could never be assembled
		log.Printf("media: %s: start %.3f s is not an indexed keyframe: EVENT playlist", filepath.Base(o.Input), streamStart)
		return nil
	}
	grid, ok := newRemuxGrid(ki, streamStart, o.DurationSec)
	if !ok {
		return nil
	}
	rs := &remuxState{grid: grid, streamAbs: streamStart, starts: map[string]float64{}, raw: map[string]float64{}, calib: map[int]float64{},
		runStarts: []float64{0}, keyframes: ki.Keyframes, restartedFor: -1,
		gopsPerStep: max(0.1, float64(len(ki.Keyframes))*segmentSeconds/max(ki.Duration, 1))}
	// the first run, too, starts a keyframe early when there is one (its
	// first GOP comes out shifted by the reorder delay)
	first := rs.keyframeBefore(streamStart)
	rs.margin = remuxTSMargin + (streamStart - first)
	rs.runStarts[0] = first - streamStart
	rs.runStart = rs.runStarts[0]
	rs.firstSS = rs.ssAt(first)
	return rs
}

// indexed: t is one of the index's keyframes (within 5 ms).
func indexed(keys []float64, t float64) bool {
	i := sort.SearchFloat64s(keys, t-0.005)
	return i < len(keys) && math.Abs(keys[i]-t) <= 0.005
}

// IndexedKeyframe is the keyframe at or before t from the file's keyframe
// index (built and cached on first use); false when the file has none. The
// API starts a copy there, so the stream starts on a keyframe the complete
// playlist knows (with sparse Matroska Cues ffprobe may find another one).
func (m *HLSManager) IndexedKeyframe(input string, t float64) (float64, bool) {
	m.init()
	if isHTTP(input) {
		return 0, false
	}
	ki, _, err := KeyIndexCache{Dir: m.IndexDir}.Get(input)
	if err != nil {
		return 0, false
	}
	i := sort.SearchFloat64s(ki.Keyframes, t+1e-6)
	if i == 0 {
		return 0, false
	}
	return ki.Keyframes[i-1], true
}
