package media

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// slowExitFFmpeg is a fake ffmpeg that takes ~2 s to be reaped after the
// kill (a child holds its stderr until cmd.WaitDelay): a restart's wait for
// the old run is a window other requests can land in.
func slowExitFFmpeg(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("process tests skipped in -short")
	}
	p := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nsleep 3 &\nexec sleep 1000\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// startSlow starts a 10-minute transcode session on slowExitFFmpeg and
// gives the fake time to start its stderr-holding child.
func startSlow(t *testing.T, m *HLSManager) string {
	t.Helper()
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: "in", AudioIndex: -1, DurationSec: 600,
		Decision: Decision{Mode: "transcode", VideoCodec: "h264", AudioIndex: -1}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	return id
}

func (m *HLSManager) state(id string) (cmd *exec.Cmd, done chan struct{}, firstSeg int, restarting bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	return s.cmd, s.done, s.firstSeg, s.restarting
}

// list writes the segment and an ffmpeg playlist listing it (the test
// plays ffmpeg).
func list(t *testing.T, dir, name string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:6.0,\n"+name+"\n"), 0o644)
}

// A far request that lands while another far request is restarting
// ffmpeg gets its own restart check once that one is done, instead of
// waiting the whole SegmentWait for a segment the new run will not reach.
func TestConcurrentFarRequestRestartsAgain(t *testing.T) {
	m := &HLSManager{FFmpeg: slowExitFFmpeg(t), WorkDir: t.TempDir(), HW: "none", SegmentWait: 8 * time.Second}
	defer m.Close()
	id := startSlow(t, m)
	dir := filepath.Join(m.WorkDir, id)
	go m.Segment(id, "seg00010.ts") // A: restarts at 10, waits ~2 s for the old run
	waitFor(t, time.Second, "A's restart", func() bool { _, _, _, r := m.state(id); return r })
	got := make(chan error, 1)
	go func() { _, err := m.Segment(id, "seg00050.ts"); got <- err }() // B, during A's restart
	waitFor(t, 6*time.Second, "B's own restart", func() bool { _, _, f, r := m.state(id); return f == 50 && !r })
	list(t, dir, "seg00050.ts")
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("B: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("B was not served")
	}
}

// A run that replaced the one a restart waited for (Playlist's hw→software
// fallback) is not orphaned: the restart does not launch over it, and the
// session ends up with one ffmpeg, at the requested segment.
func TestRestartDoesNotOrphanAReplacedRun(t *testing.T) {
	m := &HLSManager{FFmpeg: slowExitFFmpeg(t), WorkDir: t.TempDir(), HW: "none", SegmentWait: 10 * time.Second}
	defer m.Close()
	id := startSlow(t, m)
	dir := filepath.Join(m.WorkDir, id)
	got := make(chan error, 1)
	go func() { _, err := m.Segment(id, "seg00010.ts"); got <- err }()
	waitFor(t, time.Second, "the restart", func() bool { _, _, _, r := m.state(id); return r })
	// the fallback relaunches while the restart waits for the old run
	m.mu.Lock()
	s := m.sessions[id]
	if err := m.launchLocked(s, "none"); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	replacement := s.cmd.Process.Pid
	m.mu.Unlock()
	waitFor(t, 8*time.Second, "the restart at 10", func() bool { _, _, f, r := m.state(id); return f == 10 && !r })
	waitFor(t, 3*time.Second, "the replaced run to end", func() bool { return !alive(replacement) })
	if cur := m.pid(id); cur == replacement || !alive(cur) {
		t.Fatalf("session ffmpeg %d (replacement %d)", cur, replacement)
	}
	list(t, dir, "seg00010.ts")
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("segment: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("not served")
	}
}

// When the old ffmpeg has not exited by restartExitWait (a stalled disk),
// the restart neither deletes its files nor launches beside it: it asks
// again, and relaunches only once the old run is gone.
func TestRestartWaitsForTheOldRunToExit(t *testing.T) {
	defer func(d time.Duration) { restartExitWait = d }(restartExitWait)
	restartExitWait = 200 * time.Millisecond
	m := &HLSManager{FFmpeg: slowExitFFmpeg(t), WorkDir: t.TempDir(), HW: "none", SegmentWait: 10 * time.Second}
	defer m.Close()
	id := startSlow(t, m)
	dir := filepath.Join(m.WorkDir, id)
	oldCmd, oldDone, _, _ := m.state(id)
	// a segment the old run is still writing (unlisted)
	os.WriteFile(filepath.Join(dir, "seg00000.ts"), []byte("half"), 0o644)
	got := make(chan error, 1)
	go func() { _, err := m.Segment(id, "seg00010.ts"); got <- err }()
	deadline := time.Now().Add(6 * time.Second)
	for {
		cmd, _, _, _ := m.state(id)
		if cmd != oldCmd {
			if !isDone(oldDone) {
				t.Fatal("relaunched while the old ffmpeg had not exited")
			}
			break
		}
		if !isDone(oldDone) {
			if _, err := os.Stat(filepath.Join(dir, "seg00000.ts")); err != nil {
				t.Fatal("the old run's file was deleted while it was still running")
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("never relaunched")
		}
		time.Sleep(20 * time.Millisecond)
	}
	list(t, dir, "seg00010.ts")
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("segment: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("not served")
	}
}

// The restart against a real ffmpeg with fMP4 segments (Apple's HEVC
// path): the far segment's decode time continues the grid (tfdt), and the
// rewritten init.mp4 still decodes the earlier run's segment.
func TestCompleteFarRequestRestartsFMP4(t *testing.T) {
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
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", ReadRate: 1, ReadBurst: 1}
	defer m.Close()
	d := Decision{Mode: "transcode", TargetHeight: 144, VideoCodec: "h264", AudioIndex: 1, FMP4: true}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1, DurationSec: 90})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(m.WorkDir, id)
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	first, err := m.Segment(id, "seg00000.m4s")
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := os.ReadFile(first)
	far, err := m.Segment(id, "seg00010.m4s")
	if err != nil {
		t.Fatalf("far segment: %v", err)
	}
	init, err := m.Segment(id, "init.mp4")
	if err != nil {
		t.Fatal(err)
	}
	initBytes, _ := os.ReadFile(init)
	// init.mp4 + segment = a playable fragment; its start time is the tfdt
	startTime := func(seg []byte) float64 {
		p := filepath.Join(t.TempDir(), "frag.mp4")
		os.WriteFile(p, append(append([]byte{}, initBytes...), seg...), 0o644)
		out, err := exec.Command(probe, "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time",
			"-read_intervals", "%+#1", "-of", "csv=p=0", p).Output()
		if err != nil {
			t.Fatalf("ffprobe: %v", err)
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.Split(string(out), "\n")[0]), 64)
		if err != nil {
			t.Fatalf("pts %q: %v", out, err)
		}
		return f
	}
	farBytes, _ := os.ReadFile(far)
	if gap := startTime(farBytes) - startTime(firstBytes); math.Abs(gap-60) > 0.3 {
		t.Fatalf("segment 10 starts %.3f s after segment 0, want 60", gap)
	}
	if got, ok := listedDuration(dir, "seg00010.m4s"); !ok || math.Abs(got-6) > 0.1 {
		t.Fatalf("segment 10 lasts %v (%v)", got, ok)
	}
	if _, err := m.Segment(id, "seg00011.m4s"); err != nil {
		t.Fatalf("next segment: %v", err)
	}
	if _, err := m.Segment(id, "seg00000.m4s"); err != nil {
		t.Fatalf("earlier segment: %v", err)
	}
}
