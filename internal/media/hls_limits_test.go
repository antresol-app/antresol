package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// procState is the ps state letter of pid ("T" = stopped).
func procState(pid int) string {
	b, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	return s[:1]
}

// segWriter is a fake ffmpeg that writes a segment of `size` bytes every
// `every` seconds into the output dir and lists it in the playlist.
func segWriter(t *testing.T, size int, every string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("process tests skipped in -short")
	}
	script := fmt.Sprintf(`#!/bin/sh
for a; do last=$a; done
dir=$(dirname "$last")
i=0
printf '#EXTM3U\n' > "$last"
while :; do
  n=$(printf %%05d $i)
  head -c %d /dev/zero > "$dir/seg$n.ts"
  printf '#EXTINF:6.0,\nseg%%s.ts\n' "$n" >> "$last"
  i=$((i+1))
  sleep %s
done
`, size, every)
	p := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

var tdec = Decision{Mode: "transcode", VideoCodec: "h264", AudioIndex: -1}

func start(t *testing.T, m *HLSManager, uid int64) string {
	t.Helper()
	id, err := m.Start(context.Background(), StartOptions{UserID: uid, FileID: 1, Input: "in", Decision: tdec, AudioIndex: -1})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func segCount(dir string) int {
	es, _ := os.ReadDir(dir)
	n := 0
	for _, e := range es {
		if strings.HasPrefix(e.Name(), "seg") {
			n++
		}
	}
	return n
}

func TestHLSWipesStaleSessionDirsOnStart(t *testing.T) {
	work := t.TempDir()
	stale := filepath.Join(work, "0123456789abcdef")
	os.MkdirAll(stale, 0o755)
	os.WriteFile(filepath.Join(stale, "seg00000.ts"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(work, "movies"), 0o755) // not ours: a misconfigured dir must survive
	os.WriteFile(filepath.Join(work, "keep.txt"), []byte("x"), 0o644)
	m := &HLSManager{FFmpeg: "ffmpeg", WorkDir: work, HW: "none"}
	m.Close()
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale session dir kept: %v", err)
	}
	for _, n := range []string{"movies", "keep.txt"} {
		if _, err := os.Stat(filepath.Join(work, n)); err != nil {
			t.Fatalf("%s removed: %v", n, err)
		}
	}
}

func TestHLSSessionLimits(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none", UserLimit: 2, GlobalLimit: 3}
	defer m.Close()
	a := start(t, m, 1)
	if _, err := m.Start(context.Background(), StartOptions{UserID: 1, FileID: 2, Input: "in", Decision: tdec, AudioIndex: -1}); err != nil {
		t.Fatal(err)
	}
	_, err := m.Start(context.Background(), StartOptions{UserID: 1, Input: "in", Decision: tdec, AudioIndex: -1})
	if !errors.Is(err, ErrUserLimit) {
		t.Fatalf("third session of a user: %v, want ErrUserLimit", err)
	}
	// A reload replaces its own session instead of counting against the limit.
	b, err := m.Start(context.Background(), StartOptions{UserID: 1, Input: "in", Decision: tdec, AudioIndex: -1, Replace: a})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if _, err := m.Touch(a); !errors.Is(err, ErrNoSession) {
		t.Fatalf("replaced session still alive: %v", err)
	}
	// Another user cannot replace (stop) someone else's session.
	if _, err := m.Start(context.Background(), StartOptions{UserID: 2, Input: "in", Decision: tdec, AudioIndex: -1, Replace: b}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Touch(b); err != nil {
		t.Fatalf("other user's replace stopped the session: %v", err)
	}
	_, err = m.Start(context.Background(), StartOptions{UserID: 3, Input: "in", Decision: tdec, AudioIndex: -1})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("global limit: %v, want ErrBusy", err)
	}
}

func TestHLSRefusesStartWhenDiskIsFull(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none", MinFreeBytes: 1 << 62}
	defer m.Close()
	_, err := m.Start(context.Background(), StartOptions{UserID: 1, Input: "in", Decision: tdec, AudioIndex: -1})
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err=%v, want ErrNoSpace", err)
	}
}

func TestHLSPausesFFmpegFarAheadAndResumes(t *testing.T) {
	m := &HLSManager{FFmpeg: segWriter(t, 10, "0.02"), WorkDir: t.TempDir(), HW: "none",
		AheadSegments: 6, SweepInterval: 20 * time.Millisecond}
	defer m.Close()
	id := start(t, m, 1)
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	pid := m.pid(id)
	waitFor(t, 5*time.Second, "ffmpeg paused", func() bool { return procState(pid) == "T" })
	n := segCount(filepath.Join(m.WorkDir, id))
	if n > 6+4 {
		t.Fatalf("%d segments written with nothing requested, want about 6", n)
	}
	time.Sleep(150 * time.Millisecond)
	if n2 := segCount(filepath.Join(m.WorkDir, id)); n2 != n {
		t.Fatalf("segments grew while paused: %d → %d", n, n2)
	}
	if st, _ := m.Touch(id); !st.Paused || !st.Running {
		t.Fatalf("status %+v, want paused and running", st)
	}
	// The player catches up: ffmpeg continues.
	if _, err := m.Segment(id, fmt.Sprintf("seg%05d.ts", n-1)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "ffmpeg resumed", func() bool { return segCount(filepath.Join(m.WorkDir, id)) > n })
	// Stop kills a paused ffmpeg too.
	waitFor(t, 5*time.Second, "paused again", func() bool { return procState(pid) == "T" })
	m.Stop(id)
	if alive(pid) {
		t.Fatal("paused ffmpeg survived Stop")
	}
}

func TestHLSTrimsSegmentsFarBehind(t *testing.T) {
	m := &HLSManager{FFmpeg: segWriter(t, 10, "0.01"), WorkDir: t.TempDir(), HW: "none",
		AheadSegments: 1000, KeepBehind: 3, SweepInterval: 20 * time.Millisecond}
	defer m.Close()
	id := start(t, m, 1)
	dir := filepath.Join(m.WorkDir, id)
	waitFor(t, 5*time.Second, "segments", func() bool { return segCount(dir) > 12 })
	if _, err := m.Segment(id, "seg00010.ts"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "trim", func() bool {
		_, err := os.Stat(filepath.Join(dir, "seg00006.ts"))
		return errors.Is(err, os.ErrNotExist)
	})
	if _, err := os.Stat(filepath.Join(dir, "seg00007.ts")); err != nil {
		t.Fatalf("kept window trimmed: %v", err)
	}
	if _, err := m.Segment(id, "seg00002.ts"); !errors.Is(err, ErrSegmentGone) {
		t.Fatalf("trimmed segment: %v, want ErrSegmentGone", err)
	}
}

func TestHLSByteBudgetEvictsOldestIdleSession(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := &HLSManager{FFmpeg: segWriter(t, 4096, "0.01"), WorkDir: t.TempDir(), HW: "none",
		MaxBytes: 64 << 10, AheadSegments: 1000, KeepBehind: 1000, IdleTimeout: time.Hour,
		SweepInterval: 20 * time.Millisecond, Now: clk.Now}
	defer m.Close()
	old := start(t, m, 1)
	clk.Add(10 * time.Minute) // `old` has not been watched for 10 minutes
	cur := start(t, m, 2)
	waitFor(t, 10*time.Second, "eviction", func() bool { _, err := m.Touch(old); return errors.Is(err, ErrNoSession) })
	if _, err := m.Touch(cur); err != nil {
		t.Fatalf("the session being watched was evicted: %v", err)
	}
}

func TestHLSTouchKeepsPausedSessionAlive(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none",
		IdleTimeout: 60 * time.Second, SweepInterval: 10 * time.Millisecond, Now: clk.Now}
	defer m.Close()
	id := start(t, m, 1)
	for i := 0; i < 5; i++ {
		clk.Add(40 * time.Second)
		if _, err := m.Touch(id); err != nil {
			t.Fatalf("heartbeat %d: %v", i, err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if _, err := m.Touch(id); err != nil {
		t.Fatalf("session with heartbeats expired: %v", err)
	}
	clk.Add(61 * time.Second)
	waitFor(t, 5*time.Second, "idle kill", func() bool { return m.pid(id) == 0 })
}

func TestHLSPlaylistNotReadyWhileFFmpegRuns(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none", PlaylistWait: 100 * time.Millisecond}
	defer m.Close()
	id := start(t, m, 1)
	if _, err := m.Playlist(id); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err=%v, want ErrNotReady", err)
	}
}

func TestHLSCloseIsQuickWithManySessions(t *testing.T) {
	// Ignores SIGTERM like a stuck ffmpeg; Close must still be bounded and parallel.
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none", UserLimit: -1, GlobalLimit: -1}
	for i := 0; i < 6; i++ {
		start(t, m, int64(i))
	}
	t0 := time.Now()
	m.Close()
	if d := time.Since(t0); d > 4*time.Second {
		t.Fatalf("Close took %v", d)
	}
	es, _ := os.ReadDir(m.WorkDir)
	if len(es) != 0 {
		t.Fatalf("session dirs left after Close: %d", len(es))
	}
}

func TestBuildArgsThrottleAndTorrentInput(t *testing.T) {
	d := Decision{Mode: "remux", VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioIndex: 1}
	args := strings.Join(BuildArgsThrottled("none", "http://127.0.0.1:1/s", d, 30, "/w", Throttle{Rate: 2, Burst: 30}), " ")
	i := strings.Index(args, "-i http://")
	for _, want := range []string{"-readrate 2", "-readrate_initial_burst 30"} {
		j := strings.Index(args, want)
		if j < 0 || j > i {
			t.Errorf("%q missing or after -i: %s", want, args)
		}
	}
	// A swarm stall must not end the stream: no short I/O timeout, reconnects
	// do not give up after a few seconds.
	if strings.Contains(args, "-rw_timeout 30000000") || strings.Contains(args, "-reconnect_delay_max 5 ") {
		t.Errorf("short torrent timeouts: %s", args)
	}
	for _, want := range []string{"-reconnect_max_retries -1", "-reconnect_delay_max 30"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q: %s", want, args)
		}
	}
	if plain := strings.Join(BuildArgs("none", "/m/a.mkv", d, 0, "/w"), " "); strings.Contains(plain, "readrate") {
		t.Errorf("BuildArgs throttles: %s", plain)
	}
}

func TestBuildArgsQSVScaleKeepsTimestamps(t *testing.T) {
	// vpp_qsv scaling duplicated ~40 frames at the start (A/V offset);
	// passthrough keeps the source timestamps.
	d := Decision{Mode: "transcode", TargetHeight: 720, VideoCodec: "h264", SourceHeight: 1080, AudioIndex: 1}
	args := strings.Join(BuildArgs("qsv", "/m/in.mkv", d, 0, "/w"), " ")
	if !strings.Contains(args, "vpp_qsv=w=") || !strings.Contains(args, "-fps_mode passthrough") {
		t.Fatalf("%s", args)
	}
}

func TestKeyframeBefore(t *testing.T) {
	ff := needFFmpeg(t)
	clip := filepath.Join(t.TempDir(), "gop.mkv")
	// Keyframes every 2 s (GOP 50 at 25 fps).
	if b, err := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25",
		"-t", "12", "-c:v", "libx264", "-g", "50", "-keyint_min", "50", "-sc_threshold", "0", "-pix_fmt", "yuv420p", clip).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	k, err := KeyframeBefore(context.Background(), "ffprobe", clip, 5.3)
	if err != nil {
		t.Fatal(err)
	}
	if k < 3.99 || k > 4.01 {
		t.Fatalf("keyframe before 5.3 = %v, want 4", k)
	}
	if k, err := KeyframeBefore(context.Background(), "ffprobe", clip, 6); err != nil || k < 5.99 || k > 6.01 {
		t.Fatalf("keyframe at 6 = %v, %v", k, err)
	}
	if _, err := KeyframeBefore(context.Background(), "ffprobe", "http://10.0.0.1/x", 5); !errors.Is(err, ErrUnsafeInput) {
		t.Fatalf("LAN url: %v", err)
	}
}

func TestHLSRemuxReadRateIsThrottled(t *testing.T) {
	ff := needFFmpeg(t)
	clip := makeClip(t, ff, 40)
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", ReadRate: 1, ReadBurst: 6}
	defer m.Close()
	d := Decision{Mode: "remux", VideoCopy: true, AudioCopy: false, VideoCodec: "h264", AudioIndex: 1}
	id, err := m.Start(context.Background(), StartOptions{UserID: 1, FileID: 1, Input: clip, Decision: d, AudioIndex: -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	b, _ := os.ReadFile(filepath.Join(m.WorkDir, id, "index.m3u8"))
	if strings.Contains(string(b), "#EXT-X-ENDLIST") {
		t.Fatalf("a 40 s clip was remuxed to the end within seconds: readrate not applied\n%s", b)
	}
}

func TestHLSSameFileStartReplacesOldSession(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none", UserLimit: 2, GlobalLimit: 4}
	defer m.Close()
	a := start(t, m, 1) // file 1
	b := start(t, m, 1) // a page reload: same user, same file
	c := start(t, m, 1) // and again, without any Replace
	if _, err := m.Touch(a); !errors.Is(err, ErrNoSession) {
		t.Fatalf("first orphan alive: %v", err)
	}
	if _, err := m.Touch(b); !errors.Is(err, ErrNoSession) {
		t.Fatalf("second orphan alive: %v", err)
	}
	if _, err := m.Touch(c); err != nil {
		t.Fatalf("latest session: %v", err)
	}
	// Another profile watching the same file is untouched.
	d := start(t, m, 2)
	if _, err := m.Touch(c); err != nil {
		t.Fatalf("other user's start stopped it: %v", err)
	}
	_ = d
}

func TestHLSStartRefusedWhenBudgetFull(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none", MaxBytes: 1 << 20}
	defer m.Close()
	a := start(t, m, 1)
	m.mu.Lock()
	m.sessions[a].bytes = 2 << 20 // an active session holding more than the budget
	m.mu.Unlock()
	_, err := m.Start(context.Background(), StartOptions{UserID: 2, FileID: 9, Input: "in", Decision: tdec, AudioIndex: -1})
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("start over budget: %v, want ErrNoSpace", err)
	}
	es, _ := os.ReadDir(m.WorkDir)
	if len(es) != 1 {
		t.Fatalf("refused start left a dir behind: %d entries", len(es))
	}
}
