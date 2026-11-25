package media

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// makeTS is a 4 s MPEG-TS of a test picture (H.264) and a tone in codec.
func makeTS(t *testing.T, codec string, channels int) []byte {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out := filepath.Join(t.TempDir(), "in.ts")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "4", "-c:v", "libx264", "-preset", "ultrafast", "-g", "25",
		"-pix_fmt", "yuv420p", "-c:a", codec, "-b:a", "128k", "-ac", strconv.Itoa(channels), "-f", "mpegts", out}
	if b, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Skipf("this ffmpeg cannot make %s audio: %v\n%s", codec, err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLooksLikeTS(t *testing.T) {
	ts := makeTS(t, "aac", 2)
	if !LooksLikeTS(ts) || !LooksLikeTS(ts[:3*188]) {
		t.Error("a transport stream was not recognised")
	}
	if LooksLikeTS(ts[:2*188]) || LooksLikeTS(nil) || LooksLikeTS([]byte("#EXTM3U\n#EXT-X-VERSION:3\n")) || LooksLikeTS(bytes.Repeat([]byte{0x47}, 100)) {
		t.Error("something else was taken for a transport stream")
	}
}

func TestProbeTSHeadSeesWhichAudioNeedsFixing(t *testing.T) {
	for _, tc := range []struct {
		codec, name string
		fix         bool
	}{
		{"mp2", "mp2", true},
		{"ac3", "ac3", true},
		{"eac3", "eac3", true},
		{"aac", "aac", false},
		{"libmp3lame", "mp3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := makeTS(t, tc.codec, 2)
			// the start of a live stream is all there is to see
			p, err := ProbeTSHead(context.Background(), "ffprobe", ts[:min(len(ts), 100*188)])
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Audio) != 1 || p.Video == nil || p.Video.Codec != "h264" {
				t.Fatalf("%+v", p)
			}
			codec, fix := p.FirstAudioToFix()
			if fix != tc.fix || (fix && codec != tc.name) {
				t.Errorf("audio %q: needs fixing %v (%q), want %v", p.Audio[0].Codec, fix, codec, tc.fix)
			}
		})
	}
	if _, err := ProbeTSHead(context.Background(), "ffprobe", []byte("not a stream, just words")); err == nil {
		t.Error("garbage probed")
	}
}

// The video goes through as it was, the audio comes out AAC stereo, and
// the picture and the sound are still where they were in time.
func TestRemuxAudioCopiesTheVideoAndEncodesTheAudio(t *testing.T) {
	for _, codec := range []string{"mp2", "ac3"} {
		t.Run(codec, func(t *testing.T) {
			in := makeTS(t, codec, 2)
			var out bytes.Buffer
			if err := RemuxAudio(context.Background(), "ffmpeg", bytes.NewReader(in), &out); err != nil {
				t.Fatal(err)
			}
			src := probeBytes(t, in)
			got := probeBytes(t, out.Bytes())
			if got.Video == nil || got.Video.Codec != "h264" || got.Video.Width != 160 || got.Video.Height != 90 {
				t.Fatalf("video %+v", got.Video)
			}
			if len(got.Audio) != 1 || got.Audio[0].Codec != "aac" || got.Audio[0].Channels != 2 {
				t.Fatalf("audio %+v", got.Audio)
			}
			if _, fix := got.FirstAudioToFix(); fix {
				t.Error("still needs fixing")
			}
			if a, b := videoPackets(t, in), videoPackets(t, out.Bytes()); a != b {
				t.Errorf("video packets differ:\nin  %s\nout %s", a, b)
			}
			if s, g := *src.StartMs, *got.StartMs; g < s-100 || g > s+100 {
				t.Errorf("starts at %.0f ms, the source at %.0f ms", g, s)
			}
			if d := got.DurationMs - src.DurationMs; d < -300 || d > 300 {
				t.Errorf("lasts %d ms, the source %d ms", got.DurationMs, src.DurationMs)
			}
		})
	}
}

// A 5.1 track comes out stereo.
func TestRemuxAudioMixesSurroundDown(t *testing.T) {
	in := makeTS(t, "ac3", 6)
	var out bytes.Buffer
	if err := RemuxAudio(context.Background(), "ffmpeg", bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if a := probeBytes(t, out.Bytes()).Audio; len(a) != 1 || a[0].Codec != "aac" || a[0].Channels != 2 {
		t.Fatalf("%+v", a)
	}
}

func TestRemuxAudioStopsWithItsContext(t *testing.T) {
	in := makeTS(t, "mp2", 2)
	ctx, cancel := context.WithCancel(context.Background())
	r, w, _ := os.Pipe() // a source that never ends
	defer r.Close()
	defer w.Close()
	w.Write(in[:50*188])
	done := make(chan error, 1)
	go func() { done <- RemuxAudio(ctx, "ffmpeg", r, new(bytes.Buffer)) }()
	cancel()
	if err := <-done; err == nil {
		t.Error("no error from a stopped run")
	}
}

func TestRemuxAudioOpensNothingBut(t *testing.T) {
	args := strings.Join(RemuxAudioArgs(), " ")
	for _, want := range []string{"-protocol_whitelist pipe", "-format_whitelist mpegts", "-f mpegts -probesize", "-i pipe:0", "pipe:1", "-c:v copy", "-c:a aac", "-ac 2"} {
		if !strings.Contains(args, want) {
			t.Errorf("%q is not in %s", want, args)
		}
	}
	if strings.Contains(args, "://") || strings.Contains(args, "file:") {
		t.Errorf("an address in %s", args)
	}
}

func probeBytes(t *testing.T, b []byte) ProbeResult {
	t.Helper()
	p, err := ProbeTSHead(context.Background(), "ffprobe", b)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// videoPackets is every video packet's size, pts and dts, one per line.
func videoPackets(t *testing.T, b []byte) string {
	t.Helper()
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-protocol_whitelist", "pipe", "-f", "mpegts",
		"-show_entries", "packet=size,pts,dts", "-of", "csv", "-i", "pipe:0")
	cmd.Stdin = bytes.NewReader(b)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
