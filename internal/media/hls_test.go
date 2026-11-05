package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func needFFmpeg(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test skipped in -short")
	}
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	return p
}

func makeClip(t *testing.T, ffmpeg string, seconds int) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "clip.mkv")
	cmd := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440",
		"-t", strconv.Itoa(seconds), "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "ac3", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clip: %v %s", err, b)
	}
	return out
}

func alive(pid int) bool {
	// signal 0 succeeds for zombies too, so also check ps state.
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	b, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(string(b)), "Z") && strings.TrimSpace(string(b)) != ""
}

func waitFor(t *testing.T, d time.Duration, what string, f func() bool) {
	t.Helper()
	dl := time.Now().Add(d)
	for time.Now().Before(dl) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestHLSEndToEnd(t *testing.T) {
	ff := needFFmpeg(t)
	clip := makeClip(t, ff, 8)
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none"}
	defer m.Close()

	p, err := Probe(context.Background(), "ffprobe", clip)
	if err != nil {
		t.Fatal(err)
	}
	if p.Video == nil || p.Video.Codec != "h264" || len(p.Audio) != 1 || p.DurationMs < 7000 {
		t.Fatalf("probe: %+v", p)
	}
	d := Decide(p, chromeCaps, p.Audio[0].Index)
	if d.Mode == "direct" || !d.VideoCopy || d.AudioCopy {
		t.Fatalf("decision: %+v", d)
	}

	start := time.Now()
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: d.AudioIndex, StartSec: 0})
	if err != nil {
		t.Fatal(err)
	}
	pl, err := m.Playlist(id)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("playlist took %v", time.Since(start))
	}
	b, _ := os.ReadFile(pl)
	var seg string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasSuffix(l, ".ts") {
			seg = l
			break
		}
	}
	if seg == "" {
		t.Fatalf("no segment in playlist:\n%s", b)
	}
	sp, err := m.Segment(id, seg)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(sp); err != nil || st.Size() == 0 {
		t.Fatalf("segment: %v", err)
	}
	if _, err := m.Segment(id, "../../etc/passwd"); err == nil {
		t.Fatal("path traversal accepted")
	}
	m.Stop(id)
	if _, err := m.Playlist(id); err == nil {
		t.Fatal("playlist after stop should fail")
	}
}

func TestHLSTranscodeSoftware(t *testing.T) {
	ff := needFFmpeg(t)
	clip := makeClip(t, ff, 8)
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none"}
	defer m.Close()
	d := Decision{Mode: "transcode", TargetHeight: 144, VideoCodec: "h264", AudioIndex: -1}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1, StartSec: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
}

func fakeFFmpeg(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("process tests skipped in -short")
	}
	p := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexec sleep 1000\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHLSStopKillsProcess(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none"}
	defer m.Close()
	// Endless live source: ffmpeg would run forever unless killed.
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: "in", Decision: Decision{Mode: "transcode", VideoCodec: "h264", AudioIndex: -1}, AudioIndex: -1, StartSec: 0})
	if err != nil {
		t.Fatal(err)
	}
	pid := m.pid(id)
	if pid == 0 || !alive(pid) {
		t.Fatalf("ffmpeg not running, pid=%d", pid)
	}
	m.Stop(id)
	if alive(pid) {
		t.Fatal("ffmpeg still alive after Stop")
	}
}

func TestHLSCtxCancelKeepsSessionCloseKills(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none"}
	ctx, cancel := context.WithCancel(context.Background())
	id, err := m.Start(ctx, StartOptions{FileID: 1, Input: "in", Decision: Decision{Mode: "transcode", VideoCodec: "h264", AudioIndex: -1}, AudioIndex: -1, StartSec: 0})
	if err != nil {
		t.Fatal(err)
	}
	pid := m.pid(id)
	cancel()
	time.Sleep(200 * time.Millisecond)
	if !alive(pid) || m.pid(id) != pid {
		t.Fatal("session died with the caller ctx")
	}
	m.Close()
	if alive(pid) {
		t.Fatal("ffmpeg alive after Close")
	}
	if _, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: "in", Decision: Decision{AudioIndex: -1}, AudioIndex: -1, StartSec: 0}); err == nil {
		t.Fatal("Start after Close must fail")
	}
}

func TestHLSStartCanceledCtx(t *testing.T) {
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none"}
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Start(ctx, StartOptions{FileID: 1, Input: "in", Decision: Decision{AudioIndex: -1}, AudioIndex: -1, StartSec: 0}); err == nil {
		t.Fatal("want error")
	}
}

func TestHLSSoftwareFallback(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	// Fails when asked for qsv, otherwise writes a playlist and idles.
	script := "#!/bin/sh\ncase \"$*\" in *qsv*) echo qsv-boom >&2; exit 1;; esac\n" +
		"for a; do last=$a; done\nprintf '#EXTM3U\\n#EXTINF:6.0,\\nseg00000.ts\\n' > \"$last\"\nexec sleep 1000\n"
	p := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &HLSManager{FFmpeg: p, WorkDir: t.TempDir(), HW: "qsv"}
	defer m.Close()
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: "in", Decision: Decision{Mode: "transcode", VideoCodec: "h264", AudioIndex: -1}, AudioIndex: -1, StartSec: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	if hw := m.sessionHW(id); hw != "none" {
		t.Fatalf("hw=%s, want none after fallback", hw)
	}
	// With software also failing, the error is reported (no retry loop).
	bad := filepath.Join(t.TempDir(), "ffmpeg")
	os.WriteFile(bad, []byte("#!/bin/sh\necho dead >&2\nexit 1\n"), 0o755)
	m2 := &HLSManager{FFmpeg: bad, WorkDir: t.TempDir(), HW: "qsv"}
	defer m2.Close()
	id2, _ := m2.Start(context.Background(), StartOptions{FileID: 1, Input: "in", Decision: Decision{Mode: "transcode", AudioIndex: -1}, AudioIndex: -1, StartSec: 0})
	if _, err := m2.Playlist(id2); err == nil || !strings.Contains(err.Error(), "dead") {
		t.Fatalf("err=%v", err)
	}
}

func TestHLSFallbackOnEncoderInitAfterPlaylist(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	// In qsv mode it writes a playlist header without any segment, then dies
	// like a QSV encoder that fails to open; software produces a segment.
	script := "#!/bin/sh\nfor a; do last=$a; done\n" +
		"case \"$*\" in *qsv*) echo '#EXTM3U' > \"$last\"; sleep 0.3\n" +
		"  echo 'Error initializing output stream: some encoding parameters are not supported by the QSV runtime' >&2; exit 218;; esac\n" +
		"printf '#EXTM3U\\n#EXTINF:6.0,\\nseg00000.ts\\n' > \"$last\"\nexec sleep 1000\n"
	p := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &HLSManager{FFmpeg: p, WorkDir: t.TempDir(), HW: "qsv"}
	defer m.Close()
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: "in", Decision: Decision{Mode: "transcode", VideoCodec: "hevc", AudioIndex: -1}, AudioIndex: -1, StartSec: 0})
	if err != nil {
		t.Fatal(err)
	}
	pl, err := m.Playlist(id)
	if err != nil {
		t.Fatal(err)
	}
	if hw := m.sessionHW(id); hw != "none" {
		t.Fatalf("hw=%s, want none after encoder-init failure", hw)
	}
	if b, _ := os.ReadFile(pl); !strings.Contains(string(b), "#EXTINF") {
		t.Fatalf("playlist without segments returned: %q", b)
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestHLSIdleSessionKilled(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := &HLSManager{FFmpeg: fakeFFmpeg(t), WorkDir: t.TempDir(), HW: "none",
		IdleTimeout: 60 * time.Second, SweepInterval: 10 * time.Millisecond, Now: clk.Now}
	defer m.Close()
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: "in", Decision: Decision{Mode: "transcode", VideoCodec: "h264", AudioIndex: -1}, AudioIndex: -1, StartSec: 0})
	if err != nil {
		t.Fatal(err)
	}
	pid := m.pid(id)
	time.Sleep(60 * time.Millisecond)
	if !alive(pid) {
		t.Fatal("session killed before idle timeout")
	}
	clk.Add(59 * time.Second)
	time.Sleep(60 * time.Millisecond)
	if !alive(pid) {
		t.Fatal("session killed too early")
	}
	clk.Add(2 * time.Second)
	waitFor(t, 5*time.Second, "idle kill", func() bool { return !alive(pid) })
	waitFor(t, 2*time.Second, "dir removal", func() bool { _, err := os.Stat(filepath.Join(m.WorkDir, id)); return err != nil })
}

func TestHLSFlacRemuxUsesFMP4(t *testing.T) {
	ff := needFFmpeg(t)
	clip := filepath.Join(t.TempDir(), "flac.mkv")
	if b, err := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440", "-t", "8", "-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "flac", clip).CombinedOutput(); err != nil {
		t.Skipf("cannot build flac clip: %v %s", err, b)
	}
	p, err := Probe(context.Background(), "ffprobe", clip)
	if err != nil {
		t.Fatal(err)
	}
	d := Decide(p, chromeCaps, -1)
	if !d.FMP4 || !d.AudioCopy || !d.VideoCopy {
		t.Fatalf("%+v", d)
	}
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none"}
	defer m.Close()
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: d.AudioIndex, StartSec: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	init, err := m.Segment(id, "init.mp4")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,codec_type", "-of", "csv=p=0", init).CombinedOutput()
	if err != nil || strings.Contains(string(out), "bin_data") || !strings.Contains(string(out), "flac") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestTailBufRedactsStreamToken(t *testing.T) {
	tb := &tailBuf{max: 4096}
	tb.Write([]byte(`http://127.0.0.1:8420/api/torrent-stream/ab/0?k=0123abcd: Connection refused`))
	if s := tb.String(); strings.Contains(s, "0123abcd") || !strings.Contains(s, "?k=…") {
		t.Fatalf("%q", s)
	}
}

func TestBuildArgsBurnsImageSubtitle(t *testing.T) {
	d := Decision{Mode: "transcode", AudioIndex: 1, AudioCopy: true, VideoCodec: "h264", SourcePixFmt: "yuv420p", BurnSub: 4}
	for _, hw := range []string{"", "qsv"} {
		a := strings.Join(BuildArgs(hw, "/m/x.mkv", d, 0, "/tmp/o"), " ")
		if !strings.Contains(a, "-filter_complex [0:v:0][0:4]overlay=") || !strings.Contains(a, "-map [v]") ||
			strings.Contains(a, "-vf") || strings.Contains(a, "-hwaccel") || strings.Contains(a, "-map 0:v:0?") {
			t.Fatalf("%s: %s", hw, a)
		}
	}
}

// A play or a seek makes the manager IOBusy for ioBusyWindow; then it is free.
func TestIOBusyAfterStart(t *testing.T) {
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mkv", 30)
	now := time.Now()
	m := &HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none", Now: func() time.Time { return now }}
	defer m.Close()
	if m.IOBusy() {
		t.Fatal("busy with no session")
	}
	d := Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true, FMP4: true}
	if _, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: clip, Decision: d, AudioIndex: -1, DurationSec: 30}); err != nil {
		t.Fatal(err)
	}
	if !m.IOBusy() {
		t.Fatal("not busy right after the start")
	}
	now = now.Add(ioBusyWindow + time.Second)
	if m.IOBusy() {
		t.Fatal("still busy after the window")
	}
}
