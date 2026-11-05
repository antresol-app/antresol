package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseRawFingerprint(t *testing.T) {
	got := parseRawFingerprint([]byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 9})
	if !reflect.DeepEqual(got, []uint32{1, 0xffffffff}) {
		t.Fatalf("%v", got)
	}
}

func TestParseBlackdetect(t *testing.T) {
	log := "[blackdetect @ 0x55] black_start:1.5 black_end:4.25 black_duration:2.75\n" +
		"frame= 10\n[blackdetect @ 0x55] black_start:30 black_end:31 black_duration:1\n"
	got := parseBlackdetect(log, 100)
	if !reflect.DeepEqual(got, []BlackRun{{101.5, 104.25}, {130, 131}}) {
		t.Fatalf("%v", got)
	}
}

// ffmpegFixture makes a 20 s clip: 5 s black, then a test pattern, with a
// tone; nil when ffmpeg (with lavfi) is missing.
func ffmpegFixture(t *testing.T) string {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	p := filepath.Join(t.TempDir(), "clip.mkv")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=black:s=320x240:d=5:r=10",
		"-f", "lavfi", "-i", "testsrc=s=320x240:d=15:r=10", "-f", "lavfi", "-i", "sine=f=440:d=20",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1[v]", "-map", "[v]", "-map", "2:a", "-c:v", "mpeg4", "-g", "10", "-c:a", "flac", p)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg fixture: %v %s", err, out)
	}
	return p
}

func TestBlackRunsLive(t *testing.T) {
	p := ffmpegFixture(t)
	runs, err := BlackRuns(context.Background(), "ffmpeg", p, 0, 20, 1, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Start > 0.2 || runs[0].End < 4.8 || runs[0].End > 5.2 {
		t.Fatalf("%+v", runs)
	}
}

// Needs the chromaprint muxer (the server image's ffmpeg); skipped elsewhere.
func TestFingerprintLive(t *testing.T) {
	p := ffmpegFixture(t)
	if !HasMuxer(context.Background(), "ffmpeg", "chromaprint") {
		t.Skip("ffmpeg without the chromaprint muxer")
	}
	fp, err := Fingerprint(context.Background(), "ffmpeg", p, 1, 2, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	// ~0.1238 s an item: 10 s is about 80 (the first few seconds fill the window)
	if len(fp) < 40 || len(fp) > 90 {
		t.Fatalf("%d items", len(fp))
	}
	_ = os.Remove(p)
}
