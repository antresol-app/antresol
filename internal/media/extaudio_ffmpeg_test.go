package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
