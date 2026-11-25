package imgcache

import (
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"sync"
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

func TestSnap(t *testing.T) {
	for in, want := range map[int]int{-5: 0, 0: 0, 1: 160, 160: 160, 161: 240, 300: 320, 1920: 1920, 1921: 0, 4096: 0} {
		if got := Snap(in); got != want {
			t.Errorf("Snap(%d) = %d, want %d", in, got, want)
		}
	}
}

// Many requests for one copy shrink it once and all get the same file.
func TestResizedOncePerCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "poster.jpg")
	writeJPEG(t, src, 800, 1200)
	outs := make([]string, 12)
	var wg sync.WaitGroup
	for i := range outs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := Resized(dir, src, 240, 0)
			if err != nil {
				t.Error(err)
			}
			outs[i] = p
		}()
	}
	wg.Wait()
	for _, p := range outs {
		if p != outs[0] || p == src {
			t.Fatalf("outs %v", outs)
		}
	}
	if ents, _ := os.ReadDir(filepath.Join(dir, "resized")); len(ents) != 1 {
		t.Fatalf("%d files in the cache", len(ents))
	}
}

// A caller that missed the copy just before the flight that made it ended
// does not make it again.
func TestMakeCopyChecksAgain(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "poster.jpg")
	writeJPEG(t, src, 800, 1200)
	rd := filepath.Join(dir, "resized")
	os.MkdirAll(rd, 0o755)
	out := filepath.Join(rd, "copy.jpg")
	if p, err := makeCopy(src, rd, out, 240, 0); err != nil || p != out {
		t.Fatalf("first: %s %v", p, err)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(out, old, old)
	if p, err := makeCopy(src, rd, out, 240, 0); err != nil || p != out {
		t.Fatalf("second: %s %v", p, err)
	}
	if st, _ := os.Stat(out); !st.ModTime().Equal(old) {
		t.Fatalf("made again: %v", st.ModTime())
	}
}

// Whatever boxes are asked, an image has one copy per step of Widths, as
// wide as the box makes it, rounded up.
func TestThumbOneCopyPerStep(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "poster.jpg")
	writeJPEG(t, src, 1000, 1500)
	copies := map[string]bool{}
	for _, box := range [][2]int{{200, 0}, {0, 300}, {200, 300}, {200, 400}, {190, 285}, {240, 0}, {230, 345}, {100, 150},
		{1000, 450}, {300, 0}, {0, 480}, {310, 1000}, {321, 0}, {450, 0}} {
		out, err := Thumb(dir, src, box[0], box[1])
		if err != nil {
			t.Fatal(err)
		}
		if out != src {
			copies[out] = true
			if w, _ := size(t, out); !slices.Contains(Widths, w) {
				t.Errorf("box %v: width %d is not a step", box, w)
			}
		}
	}
	if len(copies) > len(Widths) || len(copies) < 3 {
		t.Fatalf("%d copies for 14 boxes: %v", len(copies), copies)
	}
	// 200×400 of a 2:3 image is 200 wide: the 240 step, aspect kept
	out, _ := Thumb(dir, src, 200, 400)
	if w, h := size(t, out); w != 240 || h != 360 {
		t.Errorf("200x400: %dx%d", w, h)
	}
	// a box the image already fits, no box, a width past the last step: the original
	big := filepath.Join(dir, "backdrop.jpg")
	writeJPEG(t, big, 3000, 2000)
	for _, c := range []struct {
		p    string
		w, h int
	}{{src, 5000, 5000}, {src, 0, 0}, {src, 1000, 0}, {big, 2500, 0}, {big, 0, 1700}} {
		if out, err := Thumb(dir, c.p, c.w, c.h); err != nil || out != c.p {
			t.Errorf("%s %dx%d: %s %v", filepath.Base(c.p), c.w, c.h, out, err)
		}
	}
	// not an image: the original, as Resized gives; missing: an error
	bad := filepath.Join(dir, "bad.jpg")
	os.WriteFile(bad, []byte("\xff\xd8 jpeg"), 0o644)
	if out, err := Thumb(dir, bad, 100, 100); err != nil || out != bad {
		t.Errorf("not an image: %s %v", out, err)
	}
	if _, err := Thumb(dir, filepath.Join(dir, "none.jpg"), 100, 100); err == nil {
		t.Errorf("missing file: no error")
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	rd := filepath.Join(dir, "resized")
	os.MkdirAll(rd, 0o755)
	put := func(name string, size int, age time.Duration) {
		p := filepath.Join(rd, name)
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		os.Chtimes(p, at, at)
	}
	day := 24 * time.Hour
	put("ancient.jpg", 10, 100*day)
	put("old.jpg", 100, 10*day)
	put("mid.jpg", 100, 5*day)
	put("new.jpg", 100, time.Hour)
	put(".resize-dead", 10, 3*time.Hour)
	put(".resize-live", 10, time.Minute)
	if n := Prune(dir, 90*day, 250); n != 3 { // ancient, the dead temp, then old (LRU) to fit 250
		t.Fatalf("removed %d", n)
	}
	var left []string
	ents, _ := os.ReadDir(rd)
	for _, e := range ents {
		left = append(left, e.Name())
	}
	if len(left) != 3 || left[0] != ".resize-live" || left[1] != "mid.jpg" || left[2] != "new.jpg" {
		t.Fatalf("left %v", left)
	}
	if n := Prune(filepath.Join(dir, "none"), day, 0); n != 0 {
		t.Fatalf("missing dir: %d", n)
	}
}

// Resized prunes on its first use of a cache directory, and a hit on an
// old copy keeps it young.
func TestResizedPrunesAndTouches(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "poster.jpg")
	writeJPEG(t, src, 1000, 1500)
	out, err := Resized(dir, src, 300, 0)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "resized", "0123456789abcdef.jpg")
	os.WriteFile(stale, []byte("x"), 0o644)
	ancient := time.Now().Add(-2 * pruneAge)
	os.Chtimes(stale, ancient, ancient)
	os.Chtimes(out, ancient, ancient)
	pruneMu.Lock()
	delete(prunedAt, dir)
	pruneMu.Unlock()
	if p, err := Resized(dir, src, 300, 0); err != nil || p != out {
		t.Fatalf("hit: %s %v", p, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale copy kept: %v", err)
	}
	if st, err := os.Stat(out); err != nil || time.Since(st.ModTime()) > time.Minute {
		t.Fatalf("hit did not refresh the copy before the prune: %v %v", st, err)
	}
}
