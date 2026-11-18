package auth

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func testPNG(w, h int, c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func jpegSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a JPEG: %v", err)
	}
	return cfg.Width, cfg.Height
}

func TestFitPhotoCutsTheMiddleSquare(t *testing.T) {
	// a wide picture: red left third, green middle, blue right third; the middle square is the green
	img := image.NewNRGBA(image.Rect(0, 0, 900, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 900; x++ {
			switch {
			case x < 300:
				img.Set(x, y, color.NRGBA{255, 0, 0, 255})
			case x < 600:
				img.Set(x, y, color.NRGBA{0, 255, 0, 255})
			default:
				img.Set(x, y, color.NRGBA{0, 0, 255, 255})
			}
		}
	}
	var src bytes.Buffer
	png.Encode(&src, img)
	out, err := FitPhoto(src.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if w, h := jpegSize(t, out); w != 300 || h != 300 {
		t.Fatalf("size %dx%d, want 300x300 (a smaller picture keeps its size)", w, h)
	}
	got, _ := jpeg.Decode(bytes.NewReader(out))
	r, g, b, _ := got.At(150, 150).RGBA()
	if g>>8 < 200 || r>>8 > 60 || b>>8 > 60 {
		t.Fatalf("the middle is not green: %d %d %d", r>>8, g>>8, b>>8)
	}
}

func TestFitPhotoShrinksToTheBox(t *testing.T) {
	out, err := FitPhoto(testPNG(2000, 1500, color.NRGBA{10, 20, 30, 255}))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := jpegSize(t, out); w != PhotoBox || h != PhotoBox {
		t.Fatalf("size %dx%d", w, h)
	}
}

func TestFitPhotoLaysTransparencyOnAGround(t *testing.T) {
	out, err := FitPhoto(testPNG(64, 64, color.NRGBA{255, 255, 255, 0}))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := jpeg.Decode(bytes.NewReader(out))
	r, _, _, _ := got.At(32, 32).RGBA()
	if r>>8 > 80 {
		t.Fatalf("transparent pixel came out light: %d", r>>8)
	}
}

func TestFitPhotoRefusesWhatIsNotAPicture(t *testing.T) {
	for name, data := range map[string][]byte{
		"text":  []byte("not a picture"),
		"empty": nil,
		"gif":   []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
		"cut":   testPNG(40, 40, color.NRGBA{1, 2, 3, 255})[:30],
	} {
		if _, err := FitPhoto(data); !errors.Is(err, ErrPhotoFormat) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPhotoURL(t *testing.T) {
	if PhotoURL(7, 0) != "" {
		t.Fatal("a profile without a photo has no address")
	}
	if got := PhotoURL(7, 1234); got != "/api/profiles/7/photo?v=1234" {
		t.Fatal(got)
	}
}

func TestStorePhoto(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newStoresDB(t)
	if _, err := s.CreateFirstAdmin(ctx, "Папа", "secret"); err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser(ctx, NewUser{Name: "Маша"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Photo != "" || u.PhotoVersion != 0 {
		t.Fatalf("new profile has a photo: %+v", u)
	}
	if _, _, err := s.Photo(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("photo before upload: %v", err)
	}
	a, err := s.SetPhoto(ctx, u.ID, []byte("jpeg-a"))
	if err != nil || a.PhotoVersion == 0 || a.Photo != PhotoURL(u.ID, a.PhotoVersion) {
		t.Fatalf("set: %+v %v", a, err)
	}
	// another upload is another version, even within the same second
	b, err := s.SetPhoto(ctx, u.ID, []byte("jpeg-b"))
	if err != nil || b.PhotoVersion <= a.PhotoVersion {
		t.Fatalf("second: %+v %v", b, err)
	}
	if data, v, err := s.Photo(ctx, u.ID); err != nil || string(data) != "jpeg-b" || v != b.PhotoVersion {
		t.Fatalf("read: %q %d %v", data, v, err)
	}
	// an edit of the profile keeps its photo
	name := "Маша К."
	if err := s.Update(ctx, u.ID, UserPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, u.ID); got.PhotoVersion != b.PhotoVersion {
		t.Fatalf("an edit lost the photo: %+v", got)
	}
	// listed with the profiles
	if list, _ := s.List(ctx); len(list) != 2 || list[1].Photo != b.Photo {
		t.Fatalf("list: %+v", list)
	}
	c, err := s.RemovePhoto(ctx, u.ID)
	if err != nil || c.Photo != "" || c.PhotoVersion != 0 {
		t.Fatalf("remove: %+v %v", c, err)
	}
	if _, _, err := s.Photo(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("photo after remove: %v", err)
	}
	if _, err := s.SetPhoto(ctx, 999, []byte("x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown profile: %v", err)
	}
}

func TestDeleteProfileDropsItsPhoto(t *testing.T) {
	ctx := context.Background()
	s, _, d := newStoresDB(t)
	if _, err := s.CreateFirstAdmin(ctx, "Папа", "secret"); err != nil {
		t.Fatal(err)
	}
	u, _ := s.CreateUser(ctx, NewUser{Name: "Гость"})
	if _, err := s.SetPhoto(ctx, u.ID, []byte("face")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM user_photos`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("photos left: %d %v", n, err)
	}
}
