package library

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"strings"
	"testing"
)

// noisyImage does not compress away, as a scanned page does not.
func noisyImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(1))
	for i := range img.Pix {
		img.Pix[i] = byte(r.Intn(256))
	}
	return img
}

func tJPEG(t *testing.T, w, h int) string {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, noisyImage(w, h), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func imageBook(t *testing.T, extra ...tEntry) []byte {
	entries := []tEntry{
		{"mimetype", "application/epub+zip", true},
		{"META-INF/container.xml", "<container/>", false},
		{"OEBPS/c.opf", `<package><manifest><item href="images/p1.jpg"/></manifest></package>`, false},
		{"OEBPS/a.xhtml", "текст книги", false},
		{"OEBPS/images/p1.jpg", tJPEG(t, 1800, 2600), true},
		{"OEBPS/images/p2.JPEG", tJPEG(t, 1800, 2600), true},
	}
	return tZip(t, append(entries, extra...)...)
}

func TestShrinkReachesTheTargetAndKeepsTheBook(t *testing.T) {
	in := imageBook(t)
	target := len(in) / 3
	out, ok := ShrinkEPUB(in, target)
	if !ok || len(out) > target {
		t.Fatalf("ok=%v, %d > %d", ok, len(out), target)
	}
	got, order := tUnzip(t, out)
	if order[0] != "mimetype" {
		t.Errorf("order %v", order)
	}
	if got["OEBPS/a.xhtml"] != "текст книги" || !strings.Contains(got["OEBPS/c.opf"], "images/p1.jpg") {
		t.Error("the text changed")
	}
	for _, n := range []string{"OEBPS/images/p1.jpg", "OEBPS/images/p2.JPEG"} {
		cfg, err := jpeg.DecodeConfig(strings.NewReader(got[n]))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if cfg.Width > 1600 || cfg.Height > 1600 {
			t.Errorf("%s is %dx%d", n, cfg.Width, cfg.Height)
		}
		if cfg.Height*1800 < cfg.Width*2600-2600 || cfg.Height*1800 > cfg.Width*2600+2600 {
			t.Errorf("%s lost its shape: %dx%d", n, cfg.Width, cfg.Height)
		}
	}
}

func TestShrinkTakesTheGentlestStepThatFits(t *testing.T) {
	in := imageBook(t)
	gentle, _ := ShrinkEPUB(in, len(in)-1)
	if len(gentle) >= len(in) {
		t.Fatalf("no smaller: %d", len(gentle))
	}
	// just under what the gentlest step gives: the next step has to do it
	harsh, ok := ShrinkEPUB(in, len(gentle)-1)
	if !ok || len(harsh) >= len(gentle) {
		t.Errorf("ok=%v, gentle %d, harsh %d", ok, len(gentle), len(harsh))
	}
}

func TestShrinkOfWhatFitsChangesNothing(t *testing.T) {
	in := imageBook(t)
	out, ok := ShrinkEPUB(in, len(in))
	if !ok || !bytes.Equal(out, in) {
		t.Errorf("ok=%v, changed=%v", ok, !bytes.Equal(out, in))
	}
}

func TestShrinkOutOfReachSaysSo(t *testing.T) {
	in := imageBook(t)
	out, ok := ShrinkEPUB(in, 100)
	if ok {
		t.Error("claimed to fit")
	}
	if len(out) >= len(in) {
		t.Errorf("no smaller: %d", len(out))
	}
	if _, order := tUnzip(t, out); len(order) != 6 {
		t.Errorf("%v", order)
	}
}

func TestShrinkPNGStaysPNGWithItsAlpha(t *testing.T) {
	img := noisyImage(1800, 1800)
	img.Set(5, 5, color.RGBA{0, 0, 0, 0})
	var b bytes.Buffer
	png.Encode(&b, img)
	in := imageBook(t, tEntry{"OEBPS/images/p.png", b.String(), true})
	out, _ := ShrinkEPUB(in, len(in)-1)
	got, _ := tUnzip(t, out)
	dec, err := png.Decode(strings.NewReader(got["OEBPS/images/p.png"]))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Bounds().Dx() > 1600 {
		t.Errorf("not scaled: %v", dec.Bounds())
	}
	if _, ok := dec.(*image.NRGBA); !ok {
		t.Errorf("alpha lost: %T", dec)
	}
}

func TestShrinkLeavesWhatItCannotImprove(t *testing.T) {
	small := tJPEG(t, 40, 40)
	in := imageBook(t, tEntry{"OEBPS/images/s.jpg", small, true}, tEntry{"OEBPS/images/broken.jpg", strings.Repeat("x", 200<<10), true}, tEntry{"OEBPS/images/a.gif", "GIF89a", true})
	out, _ := ShrinkEPUB(in, len(in)-1)
	got, _ := tUnzip(t, out)
	if got["OEBPS/images/s.jpg"] != small {
		t.Error("a small picture was recompressed")
	}
	if got["OEBPS/images/broken.jpg"] != strings.Repeat("x", 200<<10) {
		t.Error("a broken picture was lost")
	}
	if got["OEBPS/images/a.gif"] != "GIF89a" {
		t.Error("a gif changed")
	}
}

func TestShrinkRefusesAPictureBomb(t *testing.T) {
	// a header claiming a huge picture must not be decoded
	var b bytes.Buffer
	png.Encode(&b, image.NewGray(image.Rect(0, 0, 20000, 20000)))
	bomb := b.String()
	in := imageBook(t, tEntry{"OEBPS/images/bomb.png", bomb, false})
	out, _ := ShrinkEPUB(in, len(in)-1)
	got, _ := tUnzip(t, out)
	if got["OEBPS/images/bomb.png"] != bomb {
		t.Error("the bomb was decoded")
	}
}

func TestShrinkOfGarbageReturnsIt(t *testing.T) {
	if out, ok := ShrinkEPUB([]byte("not a zip"), 1); ok || string(out) != "not a zip" {
		t.Errorf("%v %q", ok, out)
	}
}
