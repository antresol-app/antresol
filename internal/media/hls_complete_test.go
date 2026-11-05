package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCompleteTotalOnlyForTranscodes(t *testing.T) {
	transcode := Decision{Mode: "transcode", VideoCodec: "hevc", AudioIndex: 1}
	cases := []struct {
		name  string
		d     Decision
		input string
		dur   float64
		start float64
		want  float64
	}{
		{"transcode", transcode, "/media/film.mkv", 7200, 0, 7200},
		{"transcode from 70:00", transcode, "/media/film.mkv", 7200, 4200, 3000},
		{"copied video: segment lengths unknown", Decision{Mode: "remux", VideoCopy: true, VideoCodec: "hevc"}, "/media/film.mkv", 7200, 0, 0},
		{"no video stream", Decision{Mode: "transcode"}, "/media/a.mkv", 7200, 0, 0},
		{"torrent may stall", transcode, "http://127.0.0.1:8421/stream/x", 7200, 0, 0},
		{"unknown duration", transcode, "/media/film.mkv", 0, 0, 0},
		{"start at the very end", transcode, "/media/film.mkv", 100, 99.5, 0},
	}
	for _, c := range cases {
		if got := completeTotal(c.d, c.input, c.dur, c.start); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCompletePlaylistLayout(t *testing.T) {
	got := completePlaylist(20.5, true)
	want := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n" +
		"#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-MAP:URI=\"init.mp4\"\n" +
		"#EXTINF:6.000000,\nseg00000.m4s\n#EXTINF:6.000000,\nseg00001.m4s\n#EXTINF:6.000000,\nseg00002.m4s\n" +
		"#EXTINF:2.500000,\nseg00003.m4s\n#EXT-X-ENDLIST\n"
	if got != want {
		t.Fatalf("fmp4:\n%s\nwant:\n%s", got, want)
	}
	// a tail under a second joins the last segment; MPEG-TS has no init segment
	ts := completePlaylist(18.4, false)
	if !strings.Contains(ts, "#EXT-X-VERSION:3\n") || strings.Contains(ts, "EXT-X-MAP") ||
		!strings.HasSuffix(ts, "#EXTINF:6.400000,\nseg00002.ts\n#EXT-X-ENDLIST\n") || strings.Contains(ts, "seg00003") {
		t.Fatalf("ts:\n%s", ts)
	}
	// a short clip is one segment; the target duration covers the longest
	if one := completePlaylist(4.2, false); !strings.Contains(one, "#EXTINF:4.200000,\nseg00000.ts\n#EXT-X-ENDLIST") {
		t.Fatalf("short:\n%s", one)
	}
	// the durations add up to the film (a 2 h film from 0)
	var sum float64
	durs := completeDurations(7200.04)
	for _, d := range durs {
		sum += d
	}
	if len(durs) != 1200 || sum < 7200.039 || sum > 7200.041 {
		t.Fatalf("%d segments, %v s", len(durs), sum)
	}
}

// startComplete starts a transcode session with a fake ffmpeg (which writes
// nothing): the test plays ffmpeg by writing the files itself.
func startComplete(t *testing.T, m *HLSManager, d Decision) (string, string) {
	t.Helper()
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: "in", Decision: d, AudioIndex: -1, DurationSec: 60})
	if err != nil {
		t.Fatal(err)
	}
	return id, filepath.Join(m.WorkDir, id)
}

func TestCompleteSegmentWaitsUntilFFmpegListsIt(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none", SegmentWait: 3 * time.Second}
	defer m.Close()
	id, dir := startComplete(t, m, Decision{Mode: "transcode", VideoCodec: "h264", AudioIndex: -1, FMP4: true})
	pl, ok := m.CompletePlaylist(id)
	if !ok || !strings.Contains(pl, "seg00009.m4s\n#EXT-X-ENDLIST") {
		t.Fatalf("complete playlist %v:\n%s", ok, pl)
	}
	// ffmpeg is still writing seg00001: it is on disk but not listed yet
	os.WriteFile(filepath.Join(dir, "seg00001.m4s"), []byte("half"), 0o644)
	os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:6.0,\nseg00000.m4s\n"), 0o644)
	got := make(chan error, 1)
	var path string
	go func() {
		p, err := m.Segment(id, "seg00001.m4s")
		path = p
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("a half-written segment was served (err %v)", err)
	case <-time.After(400 * time.Millisecond):
	}
	// ffmpeg finishes it and lists it
	os.WriteFile(filepath.Join(dir, "seg00001.m4s"), []byte("whole"), 0o644)
	os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:6.0,\nseg00000.m4s\n#EXTINF:6.0,\nseg00001.m4s\n"), 0o644)
	select {
	case err := <-got:
		if err != nil || path != filepath.Join(dir, "seg00001.m4s") {
			t.Fatalf("segment %q %v", path, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the listed segment was not served")
	}
}

func TestCompleteSegmentWaitIsBounded(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none", SegmentWait: 300 * time.Millisecond}
	defer m.Close()
	id, _ := startComplete(t, m, Decision{Mode: "transcode", VideoCodec: "h264", AudioIndex: -1})
	start := time.Now()
	if _, err := m.Segment(id, "seg00005.ts"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("far segment: %v", err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 2*time.Second {
		t.Fatalf("waited %v", d)
	}
	// a session stopped while a request waits
	m.SegmentWait = 5 * time.Second
	got := make(chan error, 1)
	go func() { _, err := m.Segment(id, "seg00006.ts"); got <- err }()
	time.Sleep(200 * time.Millisecond)
	m.Stop(id)
	select {
	case err := <-got:
		if !errors.Is(err, ErrNoSession) {
			t.Fatalf("stopped: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wait did not end with the session")
	}
}

func TestCopySessionKeepsEventPlaylist(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none", SegmentWait: 3 * time.Second}
	defer m.Close()
	id, _ := startComplete(t, m, Decision{Mode: "remux", VideoCopy: true, VideoCodec: "hevc", AudioIndex: -1})
	if _, ok := m.CompletePlaylist(id); ok {
		t.Fatal("a remux has no complete playlist")
	}
	start := time.Now()
	if _, err := m.Segment(id, "seg00003.ts"); !errors.Is(err, ErrNoSegment) || time.Since(start) > time.Second {
		t.Fatalf("remux segment must not wait: %v after %v", err, time.Since(start))
	}
	if _, ok := m.CompletePlaylist("nope"); ok {
		t.Fatal("unknown session")
	}
}

// The complete playlist's promise against a real ffmpeg transcode: the
// segments ffmpeg writes have the listed count and lengths (NTSC frame
// rate, a start mid-file, fMP4 and MPEG-TS).
func TestCompletePlaylistMatchesFFmpeg(t *testing.T) {
	ff := needFFmpeg(t)
	clip := filepath.Join(t.TempDir(), "ntsc.mkv")
	cmd := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=24000/1001",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "33",
		"-c:v", "libx264", "-g", "250", "-pix_fmt", "yuv420p", "-c:a", "aac", clip)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clip: %v %s", err, b)
	}
	encoders := []string{"none"}
	if runtime.GOOS == "darwin" {
		encoders = append(encoders, "videotoolbox") // the hardware encoder honours the forced keyframes too
	}
	if hw := os.Getenv("VAULT_TEST_HW"); hw != "" {
		encoders = strings.Split(hw, ",") // e.g. qsv on the server (/dev/dri)
	}
	for _, hw := range encoders {
		for _, fmp4 := range []bool{true, false} {
			m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: hw, ReadRate: -1}
			d := Decision{Mode: "transcode", TargetHeight: 144, VideoCodec: "h264", AudioIndex: 1, FMP4: fmp4}
			id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1, StartSec: 3, DurationSec: 33})
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, 60*time.Second, "transcode to finish", func() bool { st, _ := m.Touch(id); return !st.Running })
			real, err := os.ReadFile(filepath.Join(m.WorkDir, id, "index.m3u8"))
			if err != nil {
				t.Fatal(err)
			}
			var got []float64
			for _, line := range strings.Split(string(real), "\n") {
				if v, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
					f, _ := strconv.ParseFloat(strings.TrimSuffix(v, ","), 64)
					got = append(got, f)
				}
			}
			want := completeDurations(30)
			if len(got) != len(want) {
				t.Fatalf("%s fmp4=%v: ffmpeg wrote %v, the complete playlist lists %v\n%s", hw, fmp4, got, want, real)
			}
			for i := range want {
				// the last one ends where the source does (the clip runs a
				// little past the 33 s the test claims; a probe knows better)
				if i < len(want)-1 && math.Abs(got[i]-want[i]) > 0.1 {
					t.Fatalf("%s fmp4=%v: segment %d is %.3f s, listed %.3f s (%v)", hw, fmp4, i, got[i], want[i], got)
				}
			}
			t.Logf("%s fmp4=%v: %v", hw, fmp4, got)
			pl, _ := m.CompletePlaylist(id)
			for i := range want {
				name := filepath.Base(strings.Split(strings.Split(pl, "#EXTINF:")[i+1], "\n")[1])
				if _, err := m.Segment(id, name); err != nil {
					t.Fatalf("fmp4=%v: %s: %v", fmp4, name, err)
				}
			}
			m.Close()
		}
	}
}

// A request far past what ffmpeg has written (AVPlayer's own seek) restarts
// ffmpeg there, on the playlist's grid: the segment is served, it starts
// 6·n s after segment 0, and earlier segments stay served.
func TestCompleteFarRequestRestartsOnTheGrid(t *testing.T) {
	ff := needFFmpeg(t)
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	clip := filepath.Join(t.TempDir(), "long.mkv")
	if b, err := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "90", "-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "aac", clip).CombinedOutput(); err != nil {
		t.Fatalf("clip: %v %s", err, b)
	}
	// real-time reading: ffmpeg is nowhere near segment 10 when it is asked for
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", ReadRate: 1, ReadBurst: 1}
	defer m.Close()
	d := Decision{Mode: "transcode", TargetHeight: 144, VideoCodec: "h264", AudioIndex: 1}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1, StartSec: 0, DurationSec: 90})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	first, err := m.Segment(id, "seg00000.ts")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	far, err := m.Segment(id, "seg00010.ts")
	if err != nil {
		t.Fatalf("far segment: %v", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatalf("far segment took %v (no restart?)", time.Since(start))
	}
	startTime := func(p string) float64 {
		out, err := exec.Command(probe, "-v", "error", "-show_entries", "format=start_time", "-of", "csv=p=0", p).Output()
		if err != nil {
			t.Fatalf("ffprobe %s: %v", p, err)
		}
		f, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
		return f
	}
	if gap := startTime(far) - startTime(first); math.Abs(gap-60) > 0.3 {
		t.Fatalf("segment 10 starts %.3f s after segment 0, want 60", gap)
	}
	if got, ok := listedDuration(filepath.Join(m.WorkDir, id), "seg00010.ts"); !ok || math.Abs(got-6) > 0.1 {
		t.Fatalf("segment 10 lasts %v (%v)", got, ok)
	}
	// the restarted ffmpeg goes on from there, and the earlier run's segment stays
	if _, err := m.Segment(id, "seg00011.ts"); err != nil {
		t.Fatalf("next segment: %v", err)
	}
	if _, err := m.Segment(id, "seg00000.ts"); err != nil {
		t.Fatalf("earlier segment: %v", err)
	}
}

// A far request that is not served (still waiting) must not move the
// player's position: the sweep keeps the segments around the real playhead.
func TestUnservedRequestDoesNotTrimThePlayhead(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none", SegmentWait: 300 * time.Millisecond,
		KeepBehind: 2, AheadSegments: -1, MaxBytes: -1}
	defer m.Close()
	id, dir := startComplete(t, m, Decision{Mode: "transcode", VideoCodec: "h264", AudioIndex: -1})
	var pl strings.Builder
	pl.WriteString("#EXTM3U\n")
	for i := range 6 {
		name := fmt.Sprintf("seg%05d.ts", i)
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
		fmt.Fprintf(&pl, "#EXTINF:6.0,\n%s\n", name)
	}
	os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte(pl.String()), 0o644)
	if _, err := m.Segment(id, "seg00005.ts"); err != nil { // the playhead
		t.Fatal(err)
	}
	if _, err := m.Segment(id, "seg00090.ts"); !errors.Is(err, ErrNotReady) { // AVPlayer's own far seek, not ready in time
		t.Fatalf("far: %v", err)
	}
	m.throttle(time.Now())
	for _, keep := range []string{"seg00003.ts", "seg00004.ts", "seg00005.ts"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Fatalf("%s was trimmed: the unserved request moved the position", keep)
		}
	}
	// the playhead's segments are still served (listed by the earlier run)
	if _, err := m.Segment(id, "seg00005.ts"); err != nil {
		t.Fatalf("playhead after the restart: %v", err)
	}
}

func TestDriftLoggedOnce(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none", SegmentWait: time.Second}
	defer m.Close()
	id, dir := startComplete(t, m, Decision{Mode: "transcode", VideoCodec: "h264", AudioIndex: -1})
	os.WriteFile(filepath.Join(dir, "seg00000.ts"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "seg00001.ts"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "seg00002.ts"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:6.000,\nseg00000.ts\n#EXTINF:4.200,\nseg00001.ts\n#EXTINF:7.900,\nseg00002.ts\n"), 0o644)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	for _, n := range []string{"seg00000.ts", "seg00001.ts", "seg00002.ts"} {
		if _, err := m.Segment(id, n); err != nil {
			t.Fatal(n, err)
		}
	}
	out := buf.String()
	if !strings.Contains(out, "segment 1 is 4.200 s, the complete playlist lists 6.000 s") || strings.Count(out, "the complete playlist lists") != 1 {
		t.Fatalf("log:\n%s", out)
	}
}
