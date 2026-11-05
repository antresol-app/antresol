package imgcache

import (
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeJPEG(t *testing.T, p string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y += 7 {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
}

func size(t *testing.T, p string) (int, int) {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	return c.Width, c.Height
}

func TestResizedMaxWidth(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "poster.jpg")
	writeJPEG(t, src, 1000, 1500)
	out, err := Resized(dir, src, 300, 0)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(out) != filepath.Join(dir, "resized") {
		t.Fatalf("out %s", out)
	}
	if w, h := size(t, out); w != 300 || h != 450 {
		t.Fatalf("%dx%d", w, h)
	}
	st1, _ := os.Stat(out)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(out, old, old)
	out2, err := Resized(dir, src, 300, 0)
	if err != nil || out2 != out {
		t.Fatalf("second call %s %v", out2, err)
	}
	st2, _ := os.Stat(out2)
	if !st2.ModTime().Equal(old) || st1.Size() != st2.Size() {
		t.Fatalf("re-encoded: %v", st2.ModTime())
	}
	// fitting in a box: the height limits
	out3, _ := Resized(dir, src, 300, 300)
	if w, h := size(t, out3); w != 200 || h != 300 {
		t.Fatalf("box %dx%d", w, h)
	}
	// never upscaled: the original
	if out4, _ := Resized(dir, src, 4000, 0); out4 != src {
		t.Fatalf("upscale %s", out4)
	}
	if out5, _ := Resized(dir, src, 0, 0); out5 != src {
		t.Fatalf("no box %s", out5)
	}
}

func TestResizedSvgGifPassthrough(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"logo.svg", "anim.gif"} {
		p := filepath.Join(dir, n)
		os.WriteFile(p, []byte("x"), 0o644)
		if out, err := Resized(dir, p, 100, 100); err != nil || out != p {
			t.Fatalf("%s: %s %v", n, out, err)
		}
	}
	// not decodable: the original (the caller serves it as before)
	p := filepath.Join(dir, "broken.jpg")
	os.WriteFile(p, []byte("\xff\xd8\xff\xe0 jpeg"), 0o644)
	if out, err := Resized(dir, p, 100, 100); err != nil || out != p {
		t.Fatalf("broken: %s %v", out, err)
	}
}

func TestResolveMatchesAPI(t *testing.T) {
	dir := "/data/images"
	name := "0123456789abcdef0123456789abcdef01234567.jpg"
	if p, err := Resolve(dir, name); err != nil || p != filepath.Join(dir, name) {
		t.Fatalf("cache name: %s %v", p, err)
	}
	if p, err := Resolve(dir, "file:/media/Movie/poster.JPG"); err != nil || p != "/media/Movie/poster.JPG" {
		t.Fatalf("file: %s %v", p, err)
	}
	for _, ref := range []string{"", "https://image.tmdb.org/t/p/w500/x.jpg", "file:/media/x.txt", "file:rel/poster.jpg",
		"../etc/passwd", "0123456789abcdef0123456789abcdef01234567.exe", "file:/media/x.svg"} {
		if _, err := Resolve(dir, ref); !errors.Is(err, ErrNoImage) {
			t.Fatalf("%q: %v", ref, err)
		}
	}
	if _, err := Resolve("", name); !errors.Is(err, ErrNoImage) {
		t.Fatalf("no image dir: %v", err)
	}
}
