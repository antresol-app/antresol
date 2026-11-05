package files

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestThumbVideo(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	src := filepath.Join(t.TempDir(), "Сёгун.S01E03.WEB-DL.1080p.mkv")
	out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=640x360:rate=25",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", src).CombinedOutput()
	if err != nil {
		t.Fatalf("make clip: %v %s", err, out)
	}
	th := &Thumbnailer{FFmpeg: ff, FFprobe: "ffprobe", Dir: t.TempDir()}
	p, err := th.Thumb(context.Background(), src, "video", 320)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || !bytes.HasPrefix(b, []byte{0xFF, 0xD8}) {
		t.Fatalf("not a jpeg: %v %x", err, b[:min(4, len(b))])
	}
	th.FFmpeg, th.FFprobe = "/nonexistent/ffmpeg", "/nonexistent/ffprobe" // second request must come from cache
	p2, err := th.Thumb(context.Background(), src, "video", 300)
	if err != nil || p2 != p {
		t.Fatalf("cache miss: %q %v (first %q)", p2, err, p)
	}
	if _, err := th.Thumb(context.Background(), src, "video", 1280); err == nil {
		t.Fatal("new size with broken ffmpeg must fail")
	}
}

func TestThumbFolderUsesPoster(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "color=c=red:size=600x900", "-frames:v", "1",
		filepath.Join(dir, "poster.jpg")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	th := &Thumbnailer{FFmpeg: ff, Dir: t.TempDir()}
	if _, err := th.Thumb(context.Background(), dir, "folder", 160); err != nil {
		t.Fatal(err)
	}
	if _, err := th.Thumb(context.Background(), t.TempDir(), "folder", 160); err != ErrNoThumb {
		t.Fatalf("empty folder: %v", err)
	}
}

// argRecorder is a fake ffmpeg that records its arguments and fails.
func argRecorder(t *testing.T) (bin, argsFile string) {
	dir := t.TempDir()
	bin, argsFile = filepath.Join(dir, "ffmpeg"), filepath.Join(dir, "args")
	script := "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\"; done >> " + argsFile + "\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

func TestThumbInputIsFileOnly(t *testing.T) {
	bin, argsFile := argRecorder(t)
	dir := t.TempDir()
	for _, n := range []string{"v.mkv", "i%d.png"} {
		write(t, filepath.Join(dir, n), 10)
	}
	th := &Thumbnailer{FFmpeg: bin, Dir: t.TempDir()}
	th.Thumb(context.Background(), filepath.Join(dir, "v.mkv"), "video", 160)
	b, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(b), "-protocol_whitelist\nfile\n-i\nfile:"+filepath.Join(dir, "v.mkv")+"\n") {
		t.Fatalf("video args:\n%s", b)
	}
	os.Remove(argsFile)
	th.Thumb(context.Background(), filepath.Join(dir, "i%d.png"), "image", 160)
	b, _ = os.ReadFile(argsFile)
	if !strings.Contains(string(b), "-protocol_whitelist\nfile\n-f\nimage2\n-pattern_type\nnone\n-i\nfile:"+filepath.Join(dir, "i%d.png")+"\n") {
		t.Fatalf("image args:\n%s", b)
	}
}

// A downloaded file named x.mkv whose content is a playlist must not make
// ffmpeg/ffprobe fetch URLs (SSRF into the LAN).
func TestThumbHostilePlaylistDoesNotFetch(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	dir := t.TempDir()
	hls := "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\n" + srv.URL + "/seg.ts\n#EXT-X-ENDLIST\n"
	for n, c := range map[string]string{"x.mkv": hls, "x.m3u8": hls, "poster.jpg": hls,
		"cat.mkv": "ffconcat version 1.0\nfile " + srv.URL + "/c.mkv\n"} {
		os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644)
	}
	th := &Thumbnailer{FFmpeg: ff, FFprobe: "ffprobe", Dir: t.TempDir()}
	for _, n := range []string{"x.mkv", "x.m3u8", "cat.mkv"} {
		th.Thumb(context.Background(), filepath.Join(dir, n), "video", 160)
	}
	th.Thumb(context.Background(), filepath.Join(dir, "poster.jpg"), "image", 160)
	th.Thumb(context.Background(), dir, "folder", 160)
	if n := hits.Load(); n != 0 {
		t.Fatalf("ffmpeg fetched %d URLs from hostile files", n)
	}
}

// poster.jpg that is a symlink (to a file outside the library) is not used.
func TestFolderPosterIgnoresSymlink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	write(t, filepath.Join(outside, "secret.jpg"), 10)
	if err := os.Symlink(filepath.Join(outside, "secret.jpg"), filepath.Join(dir, "poster.jpg")); err != nil {
		t.Fatal(err)
	}
	if p, ok := folderPoster(dir); ok {
		t.Fatalf("symlinked poster used: %s", p)
	}
	if hasThumb(dir, "folder") {
		t.Fatal("folder with a symlinked poster has a thumbnail")
	}
	write(t, filepath.Join(dir, "cover.png"), 10)
	if p, ok := folderPoster(dir); !ok || filepath.Base(p) != "cover.png" {
		t.Fatalf("regular cover: %q %v", p, ok)
	}
}
