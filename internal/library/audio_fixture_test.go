package library

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeAudio writes a short sine-wave audio file (format by extension:
// .flac, .mp3, .m4a = ALAC) with the given ffmpeg metadata keys (artist, album,
// album_artist, date, track, disc, title, compilation, lyrics, ...) and an
// optional embedded front cover. Skips the test when ffmpeg is missing.
func writeAudio(t *testing.T, path string, tags map[string]string, seconds int, coverJPEG []byte) {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if seconds <= 0 {
		seconds = 1
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=22050:duration=" + strconv.Itoa(seconds)}
	if coverJPEG != nil {
		cover := filepath.Join(t.TempDir(), "cover.jpg")
		if err := os.WriteFile(cover, coverJPEG, 0o644); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-i", cover, "-map", "0:a", "-map", "1:v", "-c:v", "copy", "-disposition:v", "attached_pic",
			"-metadata:s:v", "comment=Cover (front)")
	}
	args = append(args, "-ac", "1")
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac":
		args = append(args, "-c:a", "flac")
	case ".mp3":
		args = append(args, "-c:a", "libmp3lame", "-b:a", "64k", "-id3v2_version", "3")
	case ".m4a":
		args = append(args, "-c:a", "alac") // Apple Lossless
	}
	for k, v := range tags {
		args = append(args, "-metadata", k+"="+v)
	}
	args = append(args, "-f", map[string]string{".flac": "flac", ".mp3": "mp3", ".m4a": "mp4"}[strings.ToLower(filepath.Ext(path))], path)
	if out, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %s: %v\n%s", path, err, out)
	}
}

// testJPEG returns a small JPEG image of one colour.
func testJPEG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
