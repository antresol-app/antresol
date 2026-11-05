package media

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A complete playlist lists a transcoded session's whole film from the
// start: PLAYLIST-TYPE VOD, every segment with its duration, ENDLIST. AVPlayer
// then treats the item as a film, not as live content: Picture in Picture
// shows play/pause and the skip buttons instead of a stop button, and the
// item has a duration. ffmpeg's own playlist is a growing EVENT playlist
// without ENDLIST until the transcode ends.
//
// Only a re-encoded video has segments of a known length: the encoder is
// forced to a keyframe every segmentSeconds (BuildArgs), and the HLS muxer
// cuts at the first keyframe at or after each 6 s mark, so segment n covers
// [6n, 6n+6). A copied video is cut at the source's keyframes: its complete
// playlist comes from the container's keyframe index (hls_remux.go), and
// without one it keeps the EVENT playlist. A remote input may stall for
// minutes, which a playlist that already lists the segments cannot express:
// it keeps the EVENT playlist.
//
// A segment the player asks for before ffmpeg has written it waits (see
// Segment) until ffmpeg lists it, so a half-written file is never served.
// A request far from what ffmpeg is about to write (a seek by AVPlayer
// itself: an AirPlay receiver's scrubber, PiP's ±15 s) restarts ffmpeg at
// that segment on the same grid (-ss start+6n, -start_number n,
// -output_ts_offset 6n), so the playlist stays true.

// farAhead: a request more than this many segments past the last one
// written (or before the running ffmpeg's first) restarts ffmpeg there;
// closer, it waits. One: past its burst ffmpeg reads at ReadRate (2×) real
// time, so waiting for a segment 5 ahead took ~15 s, a restart ~0.4 s.
const farAhead = 1

// completeTail: a last piece shorter than this joins the segment before it
// (ffmpeg may or may not write a sliver of a few frames as its own segment).
const completeTail = 1.0

// restartExitWait bounds how long a restart waits for the old ffmpeg to
// exit before it gives up (and asks again) instead of touching its files.
var restartExitWait = 5 * time.Second

// completeTotal is the seconds a session's complete playlist covers, or 0
// when the session keeps ffmpeg's EVENT playlist.
func completeTotal(d Decision, input string, durationSec, startSec float64) float64 {
	if d.VideoCopy || d.VideoCodec == "" || isHTTP(input) || durationSec <= 0 {
		return 0
	}
	total := durationSec - max(startSec, 0)
	if total < completeTail {
		return 0
	}
	return total
}

// completeDurations are the segment durations of a complete playlist.
func completeDurations(total float64) []float64 {
	n := int(total / segmentSeconds)
	rest := total - float64(n)*segmentSeconds
	out := make([]float64, 0, n+1)
	for range n {
		out = append(out, segmentSeconds)
	}
	switch {
	case rest >= completeTail || n == 0:
		out = append(out, rest)
	default:
		out[n-1] += rest
	}
	return out
}

// completePlaylist is the VOD playlist of a session covering total seconds.
func completePlaylist(total float64, fmp4 bool) string {
	var b strings.Builder
	durs := completeDurations(total)
	target := segmentSeconds
	for _, d := range durs {
		target = max(target, int(d+0.5))
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
	for i, d := range durs {
		fmt.Fprintf(&b, "#EXTINF:%.6f,\nseg%05d.%s\n", d, i, ext)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// TimelineOffset is the timestamp (seconds) the start of a session's
// stream has in its segments: what a WebVTT rendition's X-TIMESTAMP-MAP
// ties cue time 0 to. A TS segment is late by the muxer's delay
// (tsMuxDelay; a transcode keeps ffmpeg's default, a remux pins it), a
// remux with a complete playlist by its margin too. false: no such session.
func (m *HLSManager) TimelineOffset(id string) (float64, bool) {
	m.init()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return 0, false
	}
	off := 0.0
	if !s.d.FMP4 {
		off += tsMuxDelay
	}
	if s.remux != nil {
		off += s.remux.margin
	}
	return off, true
}

// CompletePlaylist is the session's complete VOD playlist, or false when the
// session keeps ffmpeg's EVENT playlist (a copied video, a remote stream, an
// unknown duration).
func (m *HLSManager) CompletePlaylist(id string) (string, bool) {
	m.init()
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s != nil && s.remux != nil {
		return s.remux.grid.playlist(s.d.FMP4), true
	}
	if s == nil || s.complete <= 0 {
		return "", false
	}
	return completePlaylist(s.complete, s.d.FMP4), true
}

// listedDuration is the EXTINF ffmpeg's playlist gives the segment (it
// lists a segment only once the file is complete); false = not listed.
func listedDuration(dir, name string) (float64, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "index.m3u8"))
	if err != nil {
		return 0, false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	var extinf float64
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
			extinf, _ = strconv.ParseFloat(strings.TrimSuffix(strings.SplitN(v, ",", 2)[0], ","), 64)
			continue
		}
		if line == name {
			return extinf, true
		}
	}
	return 0, false
}

// listed: the running ffmpeg lists the segment, or an earlier run did.
func (m *HLSManager) listed(s *session, name string) bool {
	m.mu.Lock()
	earlier := s.earlier[name]
	m.mu.Unlock()
	if earlier {
		return true
	}
	_, ok := listedDuration(s.dir, name)
	return ok
}

// fromSegment makes ffmpeg's output start at segment n of the 6 s grid:
// its files are numbered from n and its timestamps start at 6n.
func fromSegment(args []string, n int) []string {
	out := append([]string{}, args[:len(args)-1]...)
	out = append(out, "-start_number", strconv.Itoa(n), "-output_ts_offset", strconv.Itoa(n*segmentSeconds))
	return append(out, args[len(args)-1])
}

// restartIfFar moves ffmpeg to segment idx when the request lies before the
// running ffmpeg's first segment or more than farAhead past the last one
// written. Segments earlier runs listed are kept; later ones are dropped.
//
// again = the check could not be made now: another request is restarting
// ffmpeg, the old run had not exited by restartExitWait, or another run
// replaced it meanwhile (Playlist's hw→software fallback). The caller asks
// again once no restart is in progress (awaitListed), so the request is
// judged against the ffmpeg that is really running.
func (m *HLSManager) restartIfFar(s *session, idx int, name string) (again bool, err error) {
	if m.listed(s, name) {
		return false, nil
	}
	m.mu.Lock()
	if m.sessions[s.id] != s {
		m.mu.Unlock()
		return false, ErrNoSession
	}
	written := max(lastWritten(s.dir), s.firstSeg-1)
	far := idx < s.firstSeg || idx > written+farAhead
	if !far {
		m.mu.Unlock()
		return false, nil
	}
	if s.restarting {
		m.mu.Unlock()
		return true, nil
	}
	s.restarting = true
	old := s.done
	s.cancel()
	m.mu.Unlock()
	select {
	case <-old:
	case <-time.After(restartExitWait):
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.restarting = false
	if m.sessions[s.id] != s {
		return false, ErrNoSession
	}
	if s.done != old || !isDone(old) {
		// a run that replaced old is not ours to kill unseen, and a run
		// still alive may still write: touch nothing, check again
		return true, nil
	}
	if s.earlier == nil {
		s.earlier = map[string]bool{}
	}
	if b, err := os.ReadFile(filepath.Join(s.dir, "index.m3u8")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if _, ok := segIndex(line); ok {
				if _, err := os.Stat(filepath.Join(s.dir, line)); err == nil { // not trimmed
					s.earlier[line] = true
				}
			}
		}
	}
	for n := range s.earlier {
		if i, _ := segIndex(n); i >= idx {
			delete(s.earlier, n)
		}
	}
	es, err := os.ReadDir(s.dir)
	if err != nil {
		log.Printf("media: hls %s: restart: %v", s.id, err)
	}
	for _, e := range es {
		i, ok := segIndex(e.Name())
		switch {
		case ok && (i >= idx || !s.earlier[e.Name()]):
			os.Remove(filepath.Join(s.dir, e.Name())) // regenerated, or half-written
		case ok:
		case e.Name() == "index.m3u8":
			os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
	log.Printf("media: hls %s: request for segment %d, ffmpeg at %d: restarting there", s.id, idx, written)
	s.firstSeg = idx
	return false, m.launchLocked(s, s.hw)
}

// lastWritten is the highest segment index on disk, -1 = none.
func lastWritten(dir string) int {
	return usage(dir).maxSeg
}

// checkDrift logs, once per session, a segment whose length differs from
// the complete playlist's by more than half a second (an encoder that does
// not cut on the forced keyframes would break the playlist's promise).
func (m *HLSManager) checkDrift(s *session, idx int, name string) {
	got, ok := listedDuration(s.dir, name)
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	durs := completeDurations(s.complete)
	if s.drifted || idx >= len(durs) || idx == len(durs)-1 {
		return // the last segment's sliver handling makes it differ legitimately
	}
	if d := got - durs[idx]; d > 0.5 || d < -0.5 {
		s.drifted = true
		log.Printf("media: hls %s (%s): segment %d is %.3f s, the complete playlist lists %.3f s", s.id, s.hw, idx, got, durs[idx])
	}
}

// awaitListed waits up to SegmentWait for ffmpeg to list a segment idx of
// a complete-playlist session. ffmpeg ended without it → ErrNoSegment; still
// working at the deadline → ErrNotReady (the player asks again). again (from
// restartIfFar): once no restart is in progress, the request's own restart
// check runs, and a restart it makes gets a fresh SegmentWait.
func (m *HLSManager) awaitListed(s *session, idx int, name string, again bool) error {
	deadline := time.Now().Add(m.SegmentWait)
	for {
		m.mu.Lock()
		gone := m.sessions[s.id] != s
		done, restarting := s.done, s.restarting
		m.mu.Unlock()
		if gone {
			return ErrNoSession
		}
		if m.listed(s, name) {
			return nil
		}
		if again && !restarting {
			m.mu.Lock()
			before := s.done
			m.mu.Unlock()
			var err error
			if again, err = m.restartIfFar(s, idx, name); err != nil {
				return err
			}
			m.mu.Lock()
			restarted := s.done != before
			done, restarting = s.done, s.restarting
			m.mu.Unlock()
			if restarted {
				deadline = time.Now().Add(m.SegmentWait)
			}
		}
		if isDone(done) && !restarting {
			if m.listed(s, name) {
				return nil
			}
			return ErrNoSegment
		}
		if !time.Now().Before(deadline) {
			return ErrNotReady
		}
		time.Sleep(100 * time.Millisecond)
	}
}
