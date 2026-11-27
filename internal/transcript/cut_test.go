package transcript

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A piece is a small 16 kHz mono MP3 of exactly the part asked for.
func TestCutPiece(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}
	dir := t.TempDir()
	// a silent stereo recording of 20 s, its name one ffmpeg could take for
	// a protocol
	src := filepath.Join(dir, "part: 1.m4a")
	if out, err := exec.Command(ffmpeg, "-loglevel", "error", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo", "-t", "20", "-c:a", "aac", "file:"+src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	got, err := cutPiece(context.Background(), ffmpeg, src, 5_000, 10_000, nil)
	if err != nil {
		t.Fatal(err)
	}
	piece := filepath.Join(dir, "piece.mp3")
	if err := os.WriteFile(piece, got, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_name,channels,sample_rate:format=duration", "-of", "csv=p=0", piece).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(out))
	if len(lines) != 2 || lines[0] != "mp3,16000,1" {
		t.Fatalf("probe %q", out)
	}
	if d, _ := strconv.ParseFloat(lines[1], 64); d < 9.8 || d > 10.3 {
		t.Errorf("duration %v", d)
	}
	if len(got) > 60_000 {
		t.Errorf("%d bytes for 10 s", len(got))
	}
	// past the end: next to nothing, not an error
	if tail, err := cutPiece(context.Background(), ffmpeg, src, 60_000, 10_000, nil); err != nil || len(tail) >= emptyPiece {
		t.Errorf("past the end: %d bytes, %v", len(tail), err)
	}
	if _, err := cutPiece(context.Background(), ffmpeg, filepath.Join(dir, "no.mp3"), 0, 1000, nil); err == nil {
		t.Error("a missing file")
	}
}
