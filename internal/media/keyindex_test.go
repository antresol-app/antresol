package media

import (
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// irregularClip encodes a clip with B-frames and keyframes at irregular
// times (short and long GOPs) into ext (mp4/mkv/mov).
func irregularClip(t *testing.T, ff, ext string, seconds int) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "irregular."+ext)
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=24000/1001",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", strconv.Itoa(seconds),
		"-c:v", "libx264", "-bf", "3", "-x264-params", "keyint=170:min-keyint=5:scenecut=0",
		"-force_key_frames", "1.3,2.1,7.7,8.0,15.2,22.9,23.4,31.0,44.4,45.0",
		"-pix_fmt", "yuv420p", "-c:a", "aac"}
	if ext == "mp4" {
		args = append(args, "-movflags", "+faststart")
	}
	if b, err := exec.Command(ff, append(args, out)...).CombinedOutput(); err != nil {
		t.Fatalf("clip: %v %s", err, b)
	}
	return out
}

// probedKeyframes are the keyframe times ffprobe finds by decoding them all.
func probedKeyframes(t *testing.T, file string) []float64 {
	t.Helper()
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	out, err := exec.Command(probe, "-v", "error", "-select_streams", "v", "-skip_frame", "nokey",
		"-show_entries", "frame=pts_time", "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatal(err)
	}
	var ks []float64
	for _, l := range strings.Fields(string(out)) {
		f, err := strconv.ParseFloat(strings.TrimSuffix(l, ","), 64)
		if err == nil {
			ks = append(ks, f)
		}
	}
	return ks
}

func TestKeyIndexMatchesFFprobe(t *testing.T) {
	ff := needFFmpeg(t)
	for _, ext := range []string{"mp4", "mkv", "mov"} {
		clip := irregularClip(t, ff, ext, 50)
		start := time.Now()
		ki, err := ReadKeyIndex(clip)
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		took := time.Since(start)
		want := probedKeyframes(t, clip)
		if len(ki.Keyframes) != len(want) {
			t.Fatalf("%s: index %v\nffprobe %v", ext, ki.Keyframes, want)
		}
		for i := range want {
			if math.Abs(ki.Keyframes[i]-want[i]) > 0.002 {
				t.Fatalf("%s: keyframe %d at %.4f, ffprobe %.4f", ext, i, ki.Keyframes[i], want[i])
			}
		}
		if ki.Duration < 49.5 || ki.Duration > 50.5 {
			t.Fatalf("%s: duration %v", ext, ki.Duration)
		}
		t.Logf("%s: %d keyframes, %.1f s, read in %v", ext, len(want), ki.Duration, took)
	}
}

func TestKeyIndexRefusesOtherFiles(t *testing.T) {
	ff := needFFmpeg(t)
	dir := t.TempDir()
	// fragmented MP4: no sample tables in moov
	frag := filepath.Join(dir, "frag.mp4")
	if b, err := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25", "-t", "4",
		"-c:v", "libx264", "-movflags", "+frag_keyframe+empty_moov", frag).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	// MPEG-TS has no index
	ts := filepath.Join(dir, "a.ts")
	if b, err := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25", "-t", "4",
		"-c:v", "libx264", ts).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	junk := filepath.Join(dir, "junk.mkv")
	os.WriteFile(junk, []byte{0x1A, 0x45, 0xDF, 0xA3, 0x80, 0xFF, 0xFF}, 0o644)
	for _, f := range []string{frag, ts, junk} {
		if _, err := ReadKeyIndex(f); !errors.Is(err, ErrNoKeyIndex) {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
	}
}

func TestKeyIndexCache(t *testing.T) {
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mp4", 12)
	c := KeyIndexCache{Dir: t.TempDir()}
	a, built, err := c.Get(clip)
	if err != nil || !built {
		t.Fatalf("first: %v %v", built, err)
	}
	b, built, err := c.Get(clip)
	if err != nil || built || len(b.Keyframes) != len(a.Keyframes) {
		t.Fatalf("cached: %v %v %v", built, err, b)
	}
	// a changed file is read again
	later := time.Now().Add(time.Minute)
	os.Chtimes(clip, later, later)
	if _, built, _ := c.Get(clip); !built {
		t.Fatal("a modified file came from the cache")
	}
	// a failure is cached too
	junk := filepath.Join(t.TempDir(), "x.mkv")
	os.WriteFile(junk, []byte("not a film"), 0o644)
	if _, built, err := c.Get(junk); !built || !errors.Is(err, ErrNoKeyIndex) {
		t.Fatalf("junk: %v %v", built, err)
	}
	if _, built, err := c.Get(junk); built || !errors.Is(err, ErrNoKeyIndex) {
		t.Fatalf("junk cached: %v %v", built, err)
	}
}

// A cache entry with no keyframes (truncated, edited by hand) is a miss,
// not an index: it is built again.
func TestKeyIndexCacheEmptyEntryIsAMiss(t *testing.T) {
	c := KeyIndexCache{Dir: t.TempDir()}
	junk := filepath.Join(t.TempDir(), "x.mkv")
	os.WriteFile(junk, []byte("not a film"), 0o644)
	p, err := c.pathFor(junk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"index":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, built, err := c.Get(junk); !built || !errors.Is(err, ErrNoKeyIndex) {
		t.Fatalf("empty entry trusted: built %v, %v", built, err)
	}
	if _, built, err := c.Get(junk); built || !errors.Is(err, ErrNoKeyIndex) {
		t.Fatalf("rebuilt verdict not cached: built %v, %v", built, err)
	}
}
