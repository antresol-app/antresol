package media

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Review focus 1 with the real ffmpeg: a remux resumed at 4.2 s with an
// external AC-3 track starts both streams where the embedded track would
// (the video on its keyframe, the audio at the seek point), and the
// segment carries the external track (a 1 kHz tone, the film's own is 440 Hz).
func TestExternalAudioRemuxStartsWithTheVideo(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "Show.S01E02.mkv")
	ext := filepath.Join(dir, "RUS Sound", "Show.S01E02.LostFilm.mka")
	os.MkdirAll(filepath.Dir(ext), 0o755)
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	run(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=24:duration=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=10", "-c:v", "libx264", "-preset", "ultrafast", "-g", "24", "-pix_fmt", "yuv420p",
		"-c:a", "aac", video)
	run(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=1000:duration=10", "-c:a", "ac3", ext)
	ctx := context.Background()
	vp, err := Probe(ctx, probe, video)
	if err != nil {
		t.Fatal(err)
	}
	ap, err := Probe(ctx, probe, ext)
	if err != nil || len(ap.Audio) != 1 {
		t.Fatal(err, ap)
	}
	starts := func(seg string) map[string]float64 {
		out := map[string]float64{}
		for _, l := range strings.Split(run(probe, "-v", "error", "-show_entries", "stream=codec_name,start_time", "-of", "csv=p=0", seg), "\n") {
			if c, s, ok := strings.Cut(strings.TrimSpace(l), ","); ok {
				out[c], _ = strconv.ParseFloat(s, 64)
			}
		}
		return out
	}
	caps := ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac", "ac3"}, Containers: []string{"mp4"}}

	// the embedded track: the reference
	ref := filepath.Join(dir, "ref")
	os.MkdirAll(ref, 0o755)
	de := Decide(vp, caps, 1)
	de.Mode = "remux"
	run(ff, BuildArgs("none", video, de, 4.2, ref)...)
	want := starts(filepath.Join(ref, "seg00000.ts"))

	out := filepath.Join(dir, "ext")
	os.MkdirAll(out, 0o755)
	d := DecideExternal(vp, caps, ap.Audio[0], ext, 0)
	if d.Mode != "remux" || !d.AudioCopy {
		t.Fatalf("%+v", d)
	}
	run(ff, BuildArgs("none", video, d, 4.2, out)...)
	seg := filepath.Join(out, "seg00000.ts")
	got := starts(seg)
	if got["h264"] != want["h264"] || got["ac3"] < want["h264"] || got["ac3"]-want["aac"] > 0.3 {
		t.Fatalf("starts %v, embedded %v", got, want)
	}
	// the audio is the external tone: count its zero crossings
	pcm := filepath.Join(out, "a.raw")
	run(ff, "-hide_banner", "-loglevel", "error", "-y", "-i", seg, "-map", "0:a:0", "-ac", "1", "-ar", "48000", "-f", "s16le", pcm)
	b, _ := os.ReadFile(pcm)
	cross := 0
	for i := 2; i+1 < len(b); i += 2 {
		a, c := int16(uint16(b[i-2])|uint16(b[i-1])<<8), int16(uint16(b[i])|uint16(b[i+1])<<8)
		if (a < 0) != (c < 0) {
			cross++
		}
	}
	if secs := float64(len(b)/2) / 48000; secs < 1 || float64(cross)/2/secs < 900 {
		t.Fatalf("audio %.2f s, %d crossings", secs, cross)
	}
}

// The external audio read over loopback http (a stream served from this
// machine) ends up in the HLS output, with the same reconnect flags the
// video input uses.
func TestExternalAudioOverHTTPLoopback(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "v.mkv")
	ext := filepath.Join(dir, "a.mka")
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	run(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=24:duration=6",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "24", "-pix_fmt", "yuv420p", video)
	run(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=1000:duration=6", "-c:a", "ac3", ext)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, ext)
	}))
	defer srv.Close()
	if !IsLoopbackURL(srv.URL) {
		t.Skip("test server is not on loopback: " + srv.URL)
	}
	d := Decision{Mode: "remux", VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioIndex: 0,
		AudioInput: srv.URL + "/a.mka", AudioInputCodec: "ac3"}
	out := filepath.Join(dir, "out")
	os.MkdirAll(out, 0o755)
	run(ff, BuildArgs("none", video, d, 0, out)...)
	got := run(probe, "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", filepath.Join(out, "seg00000.ts"))
	if !strings.Contains(got, "audio") {
		t.Fatalf("no audio stream in the output: %q", got)
	}
}

// A raw AC-3 track over loopback http, resumed at 63 s: ffmpeg's first
// read of it starts at the seek point's byte offset (not at byte 0, which
// a -ss on the raw demuxer would read from), and the track stays in sync
// with the video (a beep every 10 s is heard 7 s after the resumed start).
func TestRawAudioOverHTTPByteSeek(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "v.mkv")
	ext := filepath.Join(dir, "a.ac3")
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	run(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=64x36:rate=10:duration=100",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "10", "-pix_fmt", "yuv420p", video)
	run(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "aevalsrc=if(lt(mod(t\\,10)\\,0.5)\\,sin(2*PI*1000*t)\\,0):s=48000:d=100",
		"-c:a", "ac3", "-b:a", "192k", ext)
	ap, err := Probe(context.Background(), probe, ext)
	if err != nil {
		t.Fatal(err)
	}
	bs := RawAudioByteSeek(ap)
	if bs.Format != "ac3" || bs.BytesPerSec != 24000 {
		t.Fatalf("%+v of %+v", bs, ap)
	}
	var mu sync.Mutex
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		http.ServeFile(w, r, ext)
	}))
	defer srv.Close()
	if !IsLoopbackURL(srv.URL) {
		t.Skip("test server is not on loopback: " + srv.URL)
	}
	beepAt := func(d Decision) float64 {
		t.Helper()
		out := t.TempDir()
		run(ff, BuildArgs("none", video, d, 63, out)...)
		pcm := filepath.Join(out, "a.raw")
		run(ff, "-hide_banner", "-loglevel", "error", "-y", "-i", filepath.Join(out, "index.m3u8"), "-map", "0:a:0", "-ac", "1", "-ar", "48000", "-f", "s16le", pcm)
		b, _ := os.ReadFile(pcm)
		for i := 0; i+1 < len(b); i += 2 {
			if v := int16(uint16(b[i]) | uint16(b[i+1])<<8); v > 8000 || v < -8000 {
				return float64(i/2) / 48000
			}
		}
		t.Fatal("no beep in the output")
		return 0
	}
	d := Decision{Mode: "remux", VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioIndex: 0,
		AudioInput: srv.URL + "/a.ac3", AudioInputCodec: "ac3", AudioInputByteSeek: bs}
	got := beepAt(d)
	mu.Lock()
	first := append([]string(nil), ranges...)
	mu.Unlock()
	if len(first) == 0 || !strings.HasPrefix(first[0], "bytes=") {
		t.Fatalf("ranges %q", first)
	}
	if off, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(first[0], "bytes="), "-"), 10, 64); off < 63*24000-2000 {
		t.Fatalf("first read at %q, want near byte %d (all reads %q)", first[0], 63*24000, first)
	}
	for _, r := range first {
		if r == "" || r == "bytes=0-" {
			t.Fatalf("read from the start: %q", first)
		}
	}
	// the reference: the local file seeked with -ss
	local := d
	local.AudioInput = ext
	want := beepAt(local)
	if math.Abs(got-7) > 0.5 || math.Abs(got-want) > 0.3 {
		t.Fatalf("beep at %.3f s over http, %.3f s from the file (want ~7)", got, want)
	}
}
