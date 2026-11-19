package channels

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// A franchise channel gets the logo of its title named as the franchise
// (19.11: «Звёздные войны» had an empty logo); a series channel its
// series' logo; an SVG logo is passed over; an upload wins; a removed
// logo is not looked for again.
func TestAutoLogos(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dir := t.TempDir()
	s := &Service{Store: f.st, DataDir: dir}
	os.MkdirAll(filepath.Join(dir, "images"), 0o755)
	logo := func(name string, w int) {
		var b bytes.Buffer
		png.Encode(&b, testImage(w, 100))
		os.WriteFile(filepath.Join(dir, "images", name), b.Bytes(), 0o644)
	}
	logo("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png", 300) // episode V's
	logo("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.png", 200) // the series'
	os.WriteFile(filepath.Join(dir, "images", "cccccccccccccccccccccccccccccccccccccccc.svg"), []byte("<svg/>"), 0o644)
	logo("dddddddddddddddddddddddddddddddddddddddd.png", 400) // «Звездные войны»'s
	v := f.movie("Звёздные войны: Эпизод 5", 120, itemOpt{year: 1980})
	f.exec(`UPDATE items SET meta_json=json_set(meta_json,'$.logo','cccccccccccccccccccccccccccccccccccccccc.svg') WHERE id=?`, f.movie("Изгой-один", 120, itemOpt{year: 2016}))
	iv := f.movie("Звездные войны", 120, itemOpt{year: 1977})
	f.exec(`UPDATE items SET meta_json=json_set(meta_json,'$.logo','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png') WHERE id=?`, v)
	f.exec(`UPDATE items SET meta_json=json_set(meta_json,'$.logo','dddddddddddddddddddddddddddddddddddddddd.png') WHERE id=?`, iv)
	ser, _ := f.series("Мандалорец", 1, 2, 30, itemOpt{})
	f.exec(`UPDATE items SET meta_json=json_set(meta_json,'$.logo','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.png') WHERE id=?`, ser)
	if err := f.st.saveFranchises(ctx, []Franchise{{Key: "c10", Name: "Звёздные войны", Source: FranchiseTMDB, Items: []int64{v, iv}}}, true, nil); err != nil {
		t.Fatal(err)
	}
	mk := func(name, key string, picks ...int64) Channel {
		var r Rules
		for _, id := range picks {
			r.Picks = append(r.Picks, Pick{Kind: PickTitle, ID: id})
		}
		if key == "series:x" {
			key = "series:" + itoa(ser)
			r = Rules{Titles: []int64{ser}}
		}
		c, err := f.st.Create(ctx, Channel{Name: name, Number: 0, AutoKey: key, Enabled: true, Rules: r})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	fr := mk("Звёздные войны", "franchise:c10", v, iv)
	se := mk("Мандалорец", "series:x")
	genre := mk("Комедии", "genre:35", v)
	s.autoLogos(ctx)
	width := func(c Channel) int {
		c, _ = f.st.Get(ctx, c.ID)
		if c.LogoVersion == 0 {
			return 0
		}
		b, _, err := s.LogoPNG(c)
		img, err2 := png.Decode(bytes.NewReader(b))
		if err != nil || err2 != nil || c.LogoAuto != LogoAutoFound {
			t.Fatalf("%s: %v %v auto %d", c.Name, err, err2, c.LogoAuto)
		}
		return img.Bounds().Dx()
	}
	if w := width(fr); w != 400 {
		t.Fatalf("franchise logo %d wide, want the one named as the franchise (400)", w)
	}
	if w := width(se); w != 200 {
		t.Fatalf("series logo %d", w)
	}
	if w := width(genre); w != 0 {
		t.Fatalf("a genre channel got a logo %d", w)
	}
	// removed: not looked for again
	if err := s.RemoveLogo(ctx, fr.ID); err != nil {
		t.Fatal(err)
	}
	s.autoLogos(ctx)
	if c, _ := f.st.Get(ctx, fr.ID); c.LogoVersion != 0 || c.LogoAuto != LogoAutoRemoved {
		t.Fatalf("removed logo came back: %+v", c)
	}
	// an upload is the admin's
	var b bytes.Buffer
	png.Encode(&b, testImage(50, 50))
	c, _ := f.st.Get(ctx, se.ID)
	if c, err := s.SaveLogo(ctx, c, b.Bytes()); err != nil || c.LogoAuto != LogoAutoNone {
		t.Fatalf("upload %+v %v", c, err)
	}
}
