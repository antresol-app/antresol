package media

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// segmentTiming is a served segment's real start (its keyframe: an open-GOP
// stream's leading B-frames present before it) and the span of its frames
// (ffprobe; an fMP4 segment behind its init.mp4).
func segmentTiming(t *testing.T, dir, seg string, fmp4 bool) (start, dur float64) {
	t.Helper()
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	file := seg
	if fmp4 {
		init, err := os.ReadFile(filepath.Join(dir, "init.mp4"))
		if err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(seg)
		if err != nil {
			t.Fatal(err)
		}
		file = filepath.Join(t.TempDir(), "joined.mp4")
		os.WriteFile(file, append(init, body...), 0o644)
	}
	out, err := exec.Command(probe, "-v", "error", "-select_streams", "v", "-show_entries", "packet=pts_time,duration_time,flags",
		"-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", seg, err)
	}
	key, first, last := math.NaN(), math.Inf(1), math.Inf(-1)
	for _, l := range strings.Fields(string(out)) {
		f := strings.Split(strings.TrimSuffix(l, ","), ",")
		pts, err := strconv.ParseFloat(f[0], 64)
		if err != nil {
			continue
		}
		d := 0.0
		if len(f) > 1 {
			d, _ = strconv.ParseFloat(f[1], 64)
		}
		if math.IsNaN(key) && len(f) > 2 && strings.HasPrefix(f[2], "K") {
			key = pts
		}
		first, last = math.Min(first, pts), math.Max(last, pts+d)
	}
	return key, last - first
}

// playlistSegments are the listed names and EXTINFs.
func playlistSegments(pl string) (names []string, durs []float64) {
	var d float64
	for _, l := range strings.Split(pl, "\n") {
		if v, ok := strings.CutPrefix(l, "#EXTINF:"); ok {
			d, _ = strconv.ParseFloat(strings.TrimSuffix(v, ","), 64)
		} else if l != "" && !strings.HasPrefix(l, "#") {
			names, durs = append(names, l), append(durs, d)
		}
	}
	return names, durs
}

// Listed segments == produced segments: a remux of MP4 and MKV files with
// irregular GOPs and B-frames, fMP4 and MPEG-TS, from the start and from
// mid-file: every listed segment is served, its real length is its EXTINF
// (±50 ms), and it starts where the ones before it end.
func TestRemuxCompletePlaylistMatchesSegments(t *testing.T) {
	ff := needFFmpeg(t)
	for _, ext := range []string{"mp4", "mkv"} {
		clip := irregularClip(t, ff, ext, 50)
		ki, err := ReadKeyIndex(clip)
		if err != nil {
			t.Fatal(err)
		}
		for _, fmp4 := range []bool{true, false} {
			at8 := ki.Keyframes[sort.SearchFloat64s(ki.Keyframes, 7.99)] // the keyframe forced at 8 s (on the frame grid)
			for _, from := range []float64{0, at8} {
				name := fmt.Sprintf("%s fmp4=%v from=%v", ext, fmp4, from)
				m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", ReadRate: -1}
				d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true, FMP4: fmp4}
				ss := from
				if from > 0 {
					ss = from + keyframeLeadSec
				}
				id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1,
					StartSec: ss, StreamStart: from, DurationSec: ki.Duration})
				if err != nil {
					t.Fatal(err)
				}
				pl, ok := m.CompletePlaylist(id)
				if !ok {
					t.Fatalf("%s: no complete playlist", name)
				}
				if !strings.Contains(pl, "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.HasSuffix(pl, "#EXT-X-ENDLIST\n") {
					t.Fatalf("%s:\n%s", name, pl)
				}
				if _, err := m.Playlist(id); err != nil {
					t.Fatal(err)
				}
				names, durs := playlistSegments(pl)
				var total float64
				var base float64
				for i, n := range names {
					p, err := m.Segment(id, n)
					if err != nil {
						t.Fatalf("%s: %s: %v", name, n, err)
					}
					start, dur := segmentTiming(t, filepath.Join(m.WorkDir, id), p, fmp4)
					if i == 0 {
						base = start
					}
					// starts (keyframes) are checked for every segment, which fixes
					// the lengths; the span of frames too, except the first (an
					// open GOP's leading B-frames live in the next GOP's file) and
					// the last (it ends where the video does)
					if i > 0 && i < len(names)-1 && math.Abs(dur-durs[i]) > 0.05 || math.Abs(dur-durs[i]) > 0.3 {
						t.Fatalf("%s: %s lasts %.3f s, listed %.3f s", name, n, dur, durs[i])
					}
					if math.Abs(start-base-total) > 0.05 {
						t.Fatalf("%s: %s starts at %.3f, listed at %.3f", name, n, start-base, total)
					}
					total += durs[i]
				}
				if math.Abs(total-(ki.Duration-from)) > 0.3 {
					t.Fatalf("%s: the playlist covers %.2f s of %.2f", name, total, ki.Duration-from)
				}
				t.Logf("%s: %d segments %v", name, len(names), durs)
				m.Close()
			}
		}
	}
}

// A request far past what ffmpeg has written restarts it at that segment's
// keyframe; the segment (and the next) match the playlist; an earlier one
// is still served.
func TestRemuxFarRequestRestarts(t *testing.T) {
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mkv", 90)
	ki, err := ReadKeyIndex(clip)
	if err != nil {
		t.Fatal(err)
	}
	// the production read rate (2× real time after a 30 s burst): the far
	// segment, ~70 s in, is well beyond what ffmpeg has read
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none"}
	defer m.Close()
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true, FMP4: true}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1, DurationSec: ki.Duration})
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := m.CompletePlaylist(id)
	names, durs := playlistSegments(pl)
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(m.WorkDir, id)
	p0, err := m.Segment(id, names[0])
	if err != nil {
		t.Fatal(err)
	}
	base, _ := segmentTiming(t, dir, p0, true)
	far := len(names) - 3
	began := time.Now()
	pf, err := m.Segment(id, names[far])
	if err != nil {
		t.Fatalf("far: %v", err)
	}
	if time.Since(began) > 15*time.Second {
		t.Fatalf("far segment took %v", time.Since(began))
	}
	var want float64
	for _, d := range durs[:far] {
		want += d
	}
	start, dur := segmentTiming(t, dir, pf, true)
	if math.Abs(start-base-want) > 0.05 || math.Abs(dur-durs[far]) > 0.05 {
		t.Fatalf("far segment at %.3f (%.3f s), listed at %.3f (%.3f s)", start-base, dur, want, durs[far])
	}
	if _, err := m.Segment(id, names[far+1]); err != nil {
		t.Fatalf("next: %v", err)
	}
	if _, err := m.Segment(id, names[0]); err != nil {
		t.Fatalf("earlier (assembled before the restart): %v", err)
	}
}

func TestRemuxGridOnKeyframes(t *testing.T) {
	ki := KeyIndex{Keyframes: []float64{0, 1.3, 2.1, 5.9, 7.7, 8.0, 15.2, 22.9, 23.4, 31.0, 44.4, 45.0}, Duration: 50}
	g, ok := newRemuxGrid(ki, 0, 50)
	if !ok {
		t.Fatal("grid")
	}
	// first keyframe at or after 6, 12, 18, 24, 30, 36…: 7.7, 15.2, 22.9, 31.0, 44.4
	same := func(a, b []float64) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if math.Abs(a[i]-b[i]) > 1e-9 {
				return false
			}
		}
		return true
	}
	if want := []float64{0, 7.7, 15.2, 22.9, 31.0, 44.4}; !same(g.bounds, want) || g.end != 50 {
		t.Fatalf("bounds %v end %v", g.bounds, g.end)
	}
	// from a keyframe mid-file, relative to it
	g, _ = newRemuxGrid(ki, 8.0, 50)
	if !same(g.bounds, []float64{0, 7.2, 14.9, 23, 36.4}) {
		t.Fatalf("from 8: %v", g.bounds)
	}
	if g.segmentAt(15) != 2 || g.segmentAt(0) != 0 || g.segmentAt(41) != 4 {
		t.Fatal("segmentAt")
	}
	// a sliver at the end joins the last segment
	g, _ = newRemuxGrid(KeyIndex{Keyframes: []float64{0, 6, 12, 17.8}, Duration: 18}, 0, 18)
	if !same(g.bounds, []float64{0, 6, 12}) {
		t.Fatalf("sliver: %v", g.bounds)
	}
	// keyframes too far apart: no grid
	if _, ok := newRemuxGrid(KeyIndex{Keyframes: []float64{0, 50}, Duration: 90}, 0, 90); ok {
		t.Fatal("a 40 s segment")
	}
	pl := g.playlist(true)
	if !strings.Contains(pl, "#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:6.000000,\nvseg00000.m4s\n") ||
		!strings.Contains(pl, "#EXTINF:6.000000,\nvseg00002.m4s\n#EXT-X-ENDLIST") {
		t.Fatalf("playlist:\n%s", pl)
	}
}

// No index (MPEG-TS has none; a live stream is not a file): the remux keeps
// ffmpeg's EVENT playlist, and the failure is cached, not retried.
func TestRemuxWithoutIndexKeepsEventPlaylist(t *testing.T) {
	ff := needFFmpeg(t)
	ts := filepath.Join(t.TempDir(), "film.ts")
	if b, err := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25", "-t", "8",
		"-c:v", "libx264", ts).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none"}
	defer m.Close()
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: -1}
	for _, in := range []string{ts, "http://127.0.0.1:8421/stream/x"} {
		id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: in, Decision: d, AudioIndex: -1, DurationSec: 8})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := m.CompletePlaylist(id); ok {
			t.Fatalf("%s: a complete playlist without an index", in)
		}
		if _, err := m.Segment(id, "vseg00000.ts"); !errors.Is(err, ErrNoSegment) {
			t.Fatalf("%s: vseg of an EVENT session: %v", in, err)
		}
		m.Stop(id)
	}
	es, _ := os.ReadDir(m.IndexDir)
	if len(es) != 1 {
		t.Fatalf("the failure was not cached once: %d entries", len(es))
	}
}

// The heartbeat of a remux counts 6 s steps of what ffmpeg wrote (the
// app multiplies by 6), not GOP files.
func TestRemuxTouchCountsSixSecondSteps(t *testing.T) {
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mp4", 50)
	ki, _ := ReadKeyIndex(clip)
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", ReadRate: -1}
	defer m.Close()
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1, DurationSec: ki.Duration})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "remux to finish", func() bool { st, _ := m.Touch(id); return !st.Running })
	st, _ := m.Touch(id)
	if st.Segments != int(ki.Duration/6) {
		t.Fatalf("segments %d, want %d", st.Segments, int(ki.Duration/6))
	}
	// ffmpeg wrote one file per GOP
	files, _ := filepath.Glob(filepath.Join(m.WorkDir, id, "seg*.ts"))
	sort.Strings(files)
	if len(files) != len(ki.Keyframes) || !regexp.MustCompile(`seg00000\.ts$`).MatchString(files[0]) {
		t.Fatalf("%d GOP files for %d keyframes", len(files), len(ki.Keyframes))
	}
}

// A real film (VAULT_TEST_REMUX_FILE, e.g. a large MKV on the server): the
// index is read cold, the first segments and a far one (a restart) match
// the playlist within 50 ms.
func TestRemuxRealFile(t *testing.T) {
	file := os.Getenv("VAULT_TEST_REMUX_FILE")
	if file == "" {
		t.Skip("VAULT_TEST_REMUX_FILE is not set")
	}
	ff := needFFmpeg(t)
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	ki, built, err := KeyIndexCache{Dir: t.TempDir()}.Get(file)
	took := time.Since(began)
	if err != nil {
		t.Fatalf("index: %v (after %v)", err, took)
	}
	t.Logf("%s: %.1f GB, %d keyframes over %.0f s, index read in %v (built %v)", filepath.Base(file), float64(st.Size())/1e9,
		len(ki.Keyframes), ki.Duration, took, built)
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", ReadRate: -1, IndexDir: t.TempDir()}
	defer m.Close()
	audio := -1
	if a := os.Getenv("VAULT_TEST_REMUX_AUDIO"); a != "" {
		audio, _ = strconv.Atoi(a)
	}
	codec := os.Getenv("VAULT_TEST_REMUX_CODEC")
	if codec == "" {
		codec = "h264"
	}
	// as Decide: a copied H.264 goes into MPEG-TS, anything else into fMP4
	fmp4 := codec != "h264"
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: codec, AudioIndex: audio, AudioCopy: false, FMP4: fmp4}
	// MPEG-TS keeps the source's timestamps; fMP4 takes ffmpeg's guessed
	// DTS for a Matroska file (it has none) and moves a GOP's timestamps
	// by up to the reorder delay (the frames in each segment are the same)
	tolerance := 0.05
	if fmp4 {
		tolerance = 0.2 // x265 with 4 B-frames: up to 4 frames at 24 fps
	}
	// VAULT_TEST_REMUX_FROM: start mid-film on the keyframe the API would
	// take from the index
	var from, ss float64
	if v := os.Getenv("VAULT_TEST_REMUX_FROM"); v != "" {
		at, _ := strconv.ParseFloat(v, 64)
		k, ok := m.IndexedKeyframe(file, at)
		if !ok {
			t.Fatal("no indexed keyframe")
		}
		from, ss = k, k+keyframeLeadSec
	}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: file, Decision: d, AudioIndex: -1,
		StartSec: ss, StreamStart: from, DurationSec: ki.Duration})
	if err != nil {
		t.Fatal(err)
	}
	pl, ok := m.CompletePlaylist(id)
	if !ok {
		t.Fatal("no complete playlist")
	}
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	names, durs := playlistSegments(pl)
	t.Logf("%d listed segments, first %v", len(names), durs[:min(8, len(durs))])
	dir := filepath.Join(m.WorkDir, id)
	var base, dev float64
	check := func(i int) {
		p, err := m.Segment(id, names[i])
		if err != nil {
			m.mu.Lock()
			rs := m.sessions[id].remux
			g := rs.grid
			from, to, runStart := g.bounds[i], g.segEnd(i), rs.runStart
			m.mu.Unlock()
			cur := currentRun(dir)
			var near []string
			for i, n := range cur.names {
				if at, err := m.gopStart(m.sessions[id], cur, i); err == nil && at > from-15 && at < to+15 {
					near = append(near, fmt.Sprintf("%s@%.3f", n, at))
				}
			}
			gops, ended := cur.names, cur.ended
			t.Fatalf("%s [%.3f, %.3f): %v; run from %.3f, %d GOPs (ended %v), near: %v", names[i], from, to, err, runStart, len(gops), ended, near)
		}
		start, dur := segmentTiming(t, dir, p, fmp4)
		if i == 1 {
			base = start // a film started at 0 has no keyframe before its first GOP, which comes out shifted
		}
		if i == 0 {
			return
		}
		var at float64
		for _, d := range durs[1:i] {
			at += d
		}
		dev = math.Max(dev, math.Abs(start-base-at))
		// keyframe starts (consecutive ones are the lengths; an open GOP's
		// leading frames make the span of frames differ). ffmpeg's copied
		// timestamps of an open-GOP Matroska file without DTS move up to two
		// frames (the cuts are the same frames).
		_ = dur
		if math.Abs(start-base-at) > tolerance {
			t.Fatalf("%s at %.3f (%.3f s), listed at %.3f (%.3f s)", names[i], start-base, dur, at, durs[i])
		}
	}
	for i := range min(8, len(names)) {
		check(i) // 0 is served, 1 is the base
	}
	far := len(names) / 2
	began = time.Now()
	check(far)
	check(far + 1)
	t.Logf("segments 0-7, %d and %d match the playlist (largest boundary deviation %.0f ms); the far one took %v",
		far, far+1, dev*1000, time.Since(began))
}

// A rewind behind the sweep (its GOP files and assembled copy deleted)
// while ffmpeg still runs restarts ffmpeg there, instead of answering 503
// every SegmentWait.
func TestRemuxRewindPastTheSweep(t *testing.T) {
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mkv", 90)
	ki, err := ReadKeyIndex(clip)
	if err != nil {
		t.Fatal(err)
	}
	// production read rate: ffmpeg is still running when the rewind comes
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", KeepBehind: 1, AheadSegments: -1, MaxBytes: -1,
		SegmentWait: 5 * time.Second}
	defer m.Close()
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1, DurationSec: ki.Duration})
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := m.CompletePlaylist(id)
	names, _ := playlistSegments(pl)
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	for _, n := range names[:4] {
		if _, err := m.Segment(id, n); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
	m.throttle(time.Now()) // deletes segment 0's GOP files and its copy
	dir := filepath.Join(m.WorkDir, id)
	if _, err := os.Stat(filepath.Join(dir, "seg00000.ts")); err == nil {
		t.Fatal("the sweep kept segment 0's GOP")
	}
	if _, err := os.Stat(filepath.Join(dir, names[0])); err == nil {
		t.Fatal("the sweep kept segment 0's copy")
	}
	if st, _ := m.Touch(id); !st.Running {
		t.Skip("ffmpeg finished before the rewind (a fast machine)")
	}
	began := time.Now()
	if _, err := m.Segment(id, names[0]); err != nil {
		t.Fatalf("rewind: %v after %v", err, time.Since(began))
	}
	if time.Since(began) > 4*time.Second {
		t.Fatalf("rewind took %v", time.Since(began))
	}
}

// The stream must start on a keyframe the index lists; otherwise the remux
// keeps the EVENT playlist (segment 0 could never be assembled).
func TestRemuxStartMustBeIndexed(t *testing.T) {
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mkv", 30)
	ki, err := ReadKeyIndex(clip)
	if err != nil {
		t.Fatal(err)
	}
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none"}
	defer m.Close()
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: -1}
	start := func(at float64) bool {
		id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1,
			StartSec: at + keyframeLeadSec, StreamStart: at, DurationSec: ki.Duration})
		if err != nil {
			t.Fatal(err)
		}
		defer m.Stop(id)
		_, ok := m.CompletePlaylist(id)
		return ok
	}
	at8 := ki.Keyframes[sort.SearchFloat64s(ki.Keyframes, 7.99)] // the keyframe forced at 8 s (on the frame grid)
	if !start(at8) {
		t.Fatal("an indexed keyframe start has no complete playlist")
	}
	if start(9.3) {
		t.Fatal("a start the index does not list got a complete playlist")
	}
	// the API asks the index for the keyframe to start on
	if k, ok := m.IndexedKeyframe(clip, 9.3); !ok || k != at8 {
		t.Fatalf("IndexedKeyframe(9.3) = %v %v", k, ok)
	}
	if _, ok := m.IndexedKeyframe("http://127.0.0.1:8421/stream/x", 9.3); ok {
		t.Fatal("a torrent has no index")
	}
}

// Sparse Matroska Cues (only some keyframes indexed): segments lie on the
// indexed keyframes, the unindexed ones just add GOPs inside a segment, and
// every listed segment is served and matches.
func TestRemuxSparseCues(t *testing.T) {
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mkv", 50)
	full, err := ReadKeyIndex(clip)
	if err != nil {
		t.Fatal(err)
	}
	sparse := KeyIndex{Duration: full.Duration}
	for i, k := range full.Keyframes {
		if i%2 == 0 {
			sparse.Keyframes = append(sparse.Keyframes, k)
		}
	}
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", ReadRate: -1}
	defer m.Close()
	m.init()
	c := KeyIndexCache{Dir: m.IndexDir}
	p, err := c.pathFor(clip)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.write(p, cachedIndex{Index: sparse}); err != nil {
		t.Fatal(err)
	}
	from := sparse.Keyframes[2]
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1,
		StartSec: from + keyframeLeadSec, StreamStart: from, DurationSec: full.Duration})
	if err != nil {
		t.Fatal(err)
	}
	pl, ok := m.CompletePlaylist(id)
	if !ok {
		t.Fatal("no complete playlist")
	}
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	names, durs := playlistSegments(pl)
	var base, at float64
	for i, n := range names {
		p, err := m.Segment(id, n)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		start, _ := segmentTiming(t, filepath.Join(m.WorkDir, id), p, false)
		if i == 0 {
			base = start
		}
		if math.Abs(start-base-at) > 0.05 {
			cur := currentRun(filepath.Join(m.WorkDir, id))
			var placed []string
			for j, g := range cur.names {
				at, _ := m.gopStart(m.sessions[id], cur, j)
				placed = append(placed, fmt.Sprintf("%s@%.3f", g, at))
			}
			t.Fatalf("%s starts at %.3f, listed at %.3f\nfull %v\nsparse %v from %.3f\nbounds %v\nplaced %v", n, start-base, at,
				full.Keyframes, sparse.Keyframes, from, m.sessions[id].remux.grid.bounds, placed)
		}
		at += durs[i]
	}
	t.Logf("%d of %d keyframes indexed: %d segments %v", len(sparse.Keyframes), len(full.Keyframes), len(names), durs)
}

// Every frame lands in exactly one listed segment, also when segments are
// assembled at ffmpeg's live edge: an unindexed keyframe 0.13 s before an
// indexed one (sparse Cues) snaps to it only provisionally while it is the
// last GOP ffmpeg listed; a segment ending there must wait for the next GOP
// instead of leaving the unindexed GOP out (it would then belong to neither
// segment). The run's listing is replayed GOP by GOP, as ffmpeg writes it.
func TestRemuxLiveEdgeSegmentsAreContiguous(t *testing.T) {
	ff := needFFmpeg(t)
	clip := filepath.Join(t.TempDir(), "close.mkv")
	if b, err := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=24000/1001",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "40",
		"-c:v", "libx264", "-bf", "3", "-x264-params", "keyint=170:min-keyint=2:scenecut=0",
		"-force_key_frames", "1.3,2.1,7.87,8.0,15.2,22.9,23.4,31.0",
		"-pix_fmt", "yuv420p", "-c:a", "aac", clip).CombinedOutput(); err != nil {
		t.Fatalf("clip: %v %s", err, b)
	}
	full, err := ReadKeyIndex(clip)
	if err != nil {
		t.Fatal(err)
	}
	sparse := KeyIndex{Duration: full.Duration}
	dropped := math.NaN()
	for _, k := range full.Keyframes {
		if math.Abs(k-7.87) < 0.03 {
			dropped = k
			continue
		}
		sparse.Keyframes = append(sparse.Keyframes, k)
	}
	if math.IsNaN(dropped) {
		t.Fatalf("no keyframe near 7.87 s: %v", full.Keyframes)
	}
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", ReadRate: -1}
	defer m.Close()
	m.init()
	c := KeyIndexCache{Dir: m.IndexDir}
	p, err := c.pathFor(clip)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.write(p, cachedIndex{Index: sparse}); err != nil {
		t.Fatal(err)
	}
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1, DurationSec: full.Duration})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	s := m.sessions[id]
	done := s.done
	m.mu.Unlock()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("ffmpeg did not finish")
	}
	dir := filepath.Join(m.WorkDir, id)
	all := currentRun(dir)
	pl, err := os.ReadFile(filepath.Join(dir, "index.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	// replay: ffmpeg running, its playlist listing one more GOP each step
	m.mu.Lock()
	s.done = make(chan struct{})
	rs := s.remux
	rs.starts, rs.raw, rs.calib = map[string]float64{}, map[string]float64{}, map[int]float64{}
	nseg := len(rs.grid.bounds)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		s.done = done
		m.mu.Unlock()
		os.WriteFile(filepath.Join(dir, "index.m3u8"), pl, 0o644)
	}()
	header := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:8\n#EXT-X-MEDIA-SEQUENCE:0\n"
	got := make([][]string, nseg)
	for n := 1; n <= len(all.names); n++ {
		var b strings.Builder
		b.WriteString(header)
		for _, g := range all.names[:n] {
			fmt.Fprintf(&b, "#EXTINF:1.000000,\n%s\n", g)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		for k := range nseg {
			if got[k] == nil {
				if parts, ok := m.remuxParts(s, k); ok {
					got[k] = parts
				}
			}
		}
	}
	m.mu.Lock()
	s.done = done
	m.mu.Unlock()
	os.WriteFile(filepath.Join(dir, "index.m3u8"), pl, 0o644)
	for k := range nseg {
		if got[k] == nil {
			if parts, ok := m.remuxParts(s, k); ok {
				got[k] = parts
			} else {
				t.Fatalf("segment %d never assembled", k)
			}
		}
	}
	var joined []string
	for _, parts := range got {
		joined = append(joined, parts...)
	}
	first := sort.SearchStrings(all.names, joined[0])
	if want := all.names[first:]; strings.Join(joined, " ") != strings.Join(want, " ") {
		t.Fatalf("segments %v\ndo not cover the GOPs %v exactly once\nbounds %v (dropped keyframe %.3f)", got, want, rs.grid.bounds, dropped)
	}
	t.Logf("%d segments from %d GOPs; bounds %v", nseg, len(joined), rs.grid.bounds)
}

// A segment whose boundary no GOP matches (timestamp jitter above the
// tolerance, or a keyframe ffmpeg does not split on) restarts ffmpeg at most
// once: the player's retries of the same segment must not kill and restart
// it each time (the new run fails the same way).
func TestRemuxUnmatchedSegmentRestartsOnce(t *testing.T) {
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mkv", 90)
	ki, err := ReadKeyIndex(clip)
	if err != nil {
		t.Fatal(err)
	}
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", SegmentWait: 1500 * time.Millisecond}
	defer m.Close()
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1, DurationSec: ki.Duration})
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := m.CompletePlaylist(id)
	names, _ := playlistSegments(pl)
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Segment(id, names[0]); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	s := m.sessions[id]
	s.remux.grid.bounds[3] += 0.1 // no GOP starts there
	m.mu.Unlock()
	// ffmpeg has passed segment 3 (the read burst), still running
	deadline := time.Now().Add(20 * time.Second)
	for m.coveredEnd(s) < s.remux.grid.segEnd(3)+1 {
		if time.Now().After(deadline) {
			t.Fatal("ffmpeg did not pass segment 3")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for range 4 {
		if _, err := m.Segment(id, names[3]); err == nil {
			parts, _ := m.remuxParts(s, 3)
			cur := currentRun(s.dir)
			var placed []string
			for j, g := range cur.names {
				at, _ := m.gopStart(s, cur, j)
				placed = append(placed, fmt.Sprintf("%s@%.3f", g, at))
			}
			t.Fatalf("an unmatched segment was served: bounds %v parts %v run %d placed %v", s.remux.grid.bounds, parts, s.remux.run, placed)
		}
	}
	m.mu.Lock()
	runs := s.remux.run
	m.mu.Unlock()
	if st, _ := m.Touch(id); !st.Running {
		t.Skip("ffmpeg finished during the retries (a fast machine)")
	}
	if runs > 1 {
		t.Fatalf("4 retries of an unmatched segment started %d runs", runs)
	}
	// playback goes on past it
	if _, err := m.Segment(id, names[4]); err != nil {
		t.Fatalf("next segment: %v", err)
	}
}

// A TS remux pins -muxdelay/-muxpreload, and the timestamps ffmpeg writes
// with them are what calibration expects: a GOP's first frame at its
// keyframe plus the run's offset plus tsMuxDelay. fMP4 has no mux delay.
func TestRemuxPinsTSMuxDelay(t *testing.T) {
	base := []string{"-i", "in.mkv", "-hls_time", "6", "out/index.m3u8"}
	ts := strings.Join(remuxArgs(base, Decision{}, 0, 1), " ")
	want := fmt.Sprintf("-muxdelay %v -muxpreload %v", tsMuxMaxDelay, tsMuxPreload)
	if !strings.Contains(ts, want) || !strings.HasSuffix(ts, " out/index.m3u8") {
		t.Fatalf("TS args %q lack %q before the output", ts, want)
	}
	if f := strings.Join(remuxArgs(base, Decision{FMP4: true}, 0, 1), " "); strings.Contains(f, "-muxdelay") {
		t.Fatalf("fMP4 args %q", f)
	}
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mkv", 10)
	ki, err := ReadKeyIndex(clip)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: -1}
	const offset = 1.0
	args := remuxArgs(BuildArgsThrottled("none", clip, d, 0, dir, Throttle{}), d, 0, offset)
	if b, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v %s", args, err, b)
	}
	run := currentRun(dir)
	if len(run.names) < 3 {
		t.Fatalf("GOP files %v", run.names)
	}
	for i := 1; i < 3; i++ {
		pts, err := tsFirstVideoPTS(filepath.Join(dir, run.names[i]))
		if err != nil {
			t.Fatal(err)
		}
		if got := pts - offset - ki.Keyframes[i]; math.Abs(got-tsMuxDelay) > 0.15 {
			t.Fatalf("GOP %d (keyframe %.3f) at %.3f: mux delay %.3f, calibration expects %.3f", i, ki.Keyframes[i], pts, got, tsMuxDelay)
		}
	}
}

// hls.js asks for the first segments back to back (and prefetches the
// next) right after the start, before ffmpeg has listed anything: within
// its burst ffmpeg gets there in a moment, so none of them restarts it.
func TestRemuxBackToBackStartDoesNotRestart(t *testing.T) {
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mkv", 90)
	ki, err := ReadKeyIndex(clip)
	if err != nil {
		t.Fatal(err)
	}
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none"} // the production read rate
	defer m.Close()
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true, FMP4: true}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1, DurationSec: ki.Duration})
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := m.CompletePlaylist(id)
	names, _ := playlistSegments(pl)
	if len(names) < 4 {
		t.Fatalf("%d segments", len(names))
	}
	errs := make(chan error, 3)
	for _, n := range names[:3] {
		go func() { _, err := m.Segment(id, n); errs <- err }()
	}
	for range 3 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	m.mu.Lock()
	run := m.sessions[id].remux.run
	m.mu.Unlock()
	if run != 0 {
		t.Fatalf("the remux restarted %d times at the start", run)
	}
}

// The web's EVENT playlist starts where the stream starts: the first run
// begins a keyframe early, and listing that GOP would put the player's
// clock (startSec + currentTime) ahead of the picture and the sound by its
// length — subtitles, drawn on that clock, came early.
func TestRemuxEventPlaylistStartsAtStreamStart(t *testing.T) {
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mkv", 50)
	ki, err := ReadKeyIndex(clip)
	if err != nil {
		t.Fatal(err)
	}
	at8 := ki.Keyframes[sort.SearchFloat64s(ki.Keyframes, 7.99)]
	for _, fmp4 := range []bool{false, true} {
		m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", ReadRate: -1}
		d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true, FMP4: fmp4}
		id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1,
			StartSec: at8 + keyframeLeadSec, StreamStart: at8, DurationSec: ki.Duration})
		if err != nil {
			t.Fatal(err)
		}
		pl, ok := m.CompletePlaylist(id)
		if !ok {
			t.Fatal("no complete playlist")
		}
		vnames, _ := playlistSegments(pl)
		vp, err := m.Segment(id, vnames[0])
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(m.WorkDir, id)
		want, _ := segmentTiming(t, dir, vp, fmp4)
		p, err := m.Playlist(id)
		if err != nil {
			t.Fatal(err)
		}
		// ffmpeg's own playlist does begin a GOP early (else this checks nothing)
		raw, _ := os.ReadFile(filepath.Join(dir, "index.m3u8"))
		if rn, _ := playlistSegments(string(raw)); len(rn) > 0 {
			if r0, _ := segmentTiming(t, dir, filepath.Join(dir, rn[0]), fmp4); want-r0 < 0.5 {
				t.Fatalf("fmp4=%v: ffmpeg's playlist starts %.3f s early: no lead GOP to test", fmp4, want-r0)
			}
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		names, _ := playlistSegments(string(b))
		if len(names) == 0 {
			t.Fatalf("fmp4=%v: no segments:\n%s", fmp4, b)
		}
		first, err := m.Segment(id, names[0])
		if err != nil {
			t.Fatal(err)
		}
		got, _ := segmentTiming(t, dir, first, fmp4)
		if math.Abs(got-want) > 0.05 {
			t.Fatalf("fmp4=%v: the EVENT playlist starts %.3f s before the stream start:\n%s", fmp4, want-got, b)
		}
		m.Close()
	}
}

func TestDropLead(t *testing.T) {
	head := "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:10\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:EVENT\n"
	// only the lead GOP so far: not ready
	if _, ok := dropLead(head+"#EXTINF:4.240000,\nseg00000.ts\n", 4.24); ok {
		t.Fatal("a playlist of the lead GOP alone is ready")
	}
	out, ok := dropLead(head+"#EXTINF:1.000000,\nseg00000.ts\n#EXTINF:3.240000,\nseg00001.ts\n#EXTINF:2.880000,\nseg00002.ts\n", 4.24)
	want := strings.Replace(head, "SEQUENCE:0", "SEQUENCE:2", 1) + "#EXTINF:2.880000,\nseg00002.ts\n"
	if !ok || out != want {
		t.Fatalf("got %v:\n%s", ok, out)
	}
	// a GOP mostly after the start stays; a restarted run's files are never dropped
	for _, pl := range []string{head + "#EXTINF:4.000000,\nseg00000.ts\n", head + "#EXTINF:2.000000,\nseg100000.ts\n"} {
		if out, ok := dropLead(pl, 1.5); !ok || out != pl {
			t.Fatalf("changed:\n%s", out)
		}
	}
	// a finished film whose GOPs all came before: the ENDLIST is served
	if _, ok := dropLead(head+"#EXTINF:1.000000,\nseg00000.ts\n#EXT-X-ENDLIST\n", 2); !ok {
		t.Fatal("an ended playlist is never ready")
	}
}
