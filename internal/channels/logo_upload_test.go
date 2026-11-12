package channels

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
)

func testImage(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	return img
}

func TestFitLogo(t *testing.T) {
	var p, j bytes.Buffer
	png.Encode(&p, testImage(1600, 400))
	jpeg.Encode(&j, testImage(300, 900), nil)
	for _, c := range []struct {
		in   []byte
		w, h int
	}{{p.Bytes(), 512, 128}, {j.Bytes(), 170, 512}} {
		out, err := FitLogo(c.in)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(out))
		if err != nil || img.Bounds().Dx() != c.w || img.Bounds().Dy() != c.h {
			t.Fatalf("%v %v, want %dx%d", img.Bounds(), err, c.w, c.h)
		}
	}
	// a small one keeps its size
	var s bytes.Buffer
	png.Encode(&s, testImage(64, 32))
	out, _ := FitLogo(s.Bytes())
	if img, _ := png.Decode(bytes.NewReader(out)); img.Bounds().Dx() != 64 {
		t.Fatalf("small %v", img.Bounds())
	}
	// WebP (a 1×1 lossless image)
	webp := []byte{0x52, 0x49, 0x46, 0x46, 0x1a, 0, 0, 0, 0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x4c,
		0x0d, 0, 0, 0, 0x2f, 0, 0, 0, 0x10, 0x07, 0x10, 0x11, 0x11, 0x88, 0x88, 0xfe, 0x07, 0}
	if out, err := FitLogo(webp); err != nil {
		t.Fatalf("webp: %v", err)
	} else if img, _ := png.Decode(bytes.NewReader(out)); img.Bounds().Dx() != 1 {
		t.Fatalf("webp %v", img.Bounds())
	}
	for _, bad := range [][]byte{[]byte("GIF89a......"), []byte("not an image"), p.Bytes()[:100]} {
		if _, err := FitLogo(bad); err != ErrLogoFormat {
			t.Fatalf("%q: %v", bad[:6], err)
		}
	}
}

func TestSaveLogoVersionAndIPTV(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := New(f.db, nil)
	s.Store = f.st
	s.DataDir = t.TempDir()
	c := f.create(Channel{Name: "Logo"})
	if c.LogoVersion != 0 {
		t.Fatal("new channel has a logo")
	}
	var p bytes.Buffer
	png.Encode(&p, testImage(10, 10))
	c, err := s.SaveLogo(ctx, c, p.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := f.st.Get(ctx, c.ID)
	if got.LogoVersion == 0 || got.LogoVersion != c.LogoVersion {
		t.Fatalf("version %d / %d", got.LogoVersion, c.LogoVersion)
	}
	b, up, err := s.LogoPNG(got)
	if err != nil || !up || !bytes.Equal(b, p.Bytes()) {
		t.Fatalf("logo: %v %v", up, err)
	}
	// a second upload is a new version (a new URL)
	again, _ := s.SaveLogo(ctx, got, p.Bytes())
	if again.LogoVersion <= got.LogoVersion {
		t.Fatalf("version %d after %d", again.LogoVersion, got.LogoVersion)
	}
	var m bytes.Buffer
	WriteM3U(&m, "https://h/iptv/t", []Channel{again})
	q := LogoQuery(again)
	if !strings.Contains(m.String(), `tvg-logo="https://h/iptv/t/logo/`+itoa(again.ID)+`.png`+q+`"`) || q == "" {
		t.Fatalf("m3u %s", m.String())
	}
	var x bytes.Buffer
	WriteXMLTV(&x, Guide{Base: "https://h/iptv/t", Channels: []Channel{again}})
	if !strings.Contains(x.String(), `/logo/`+itoa(again.ID)+`.png?v=`) {
		t.Fatalf("xmltv %s", x.String())
	}
	if err := s.RemoveLogo(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = f.st.Get(ctx, c.ID)
	if _, err := os.Stat(LogoFile(s.DataDir, c.ID)); !os.IsNotExist(err) || got.LogoVersion != 0 {
		t.Fatalf("removed: %v %d", err, got.LogoVersion)
	}
	if _, up, _ := s.LogoPNG(got); up {
		t.Fatal("generated tile expected")
	}
}
