package bookmeta_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/antresol-app/antresol/internal/library/bookmeta"
	"github.com/antresol-app/antresol/internal/library/bookmeta/bookmetatest"
)

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 6))
	img.Set(1, 1, color.RGBA{200, 10, 10, 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 3))
	img.Set(0, 0, color.RGBA{0, 0, 255, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func write(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) bookmeta.Meta {
	t.Helper()
	m, err := bookmeta.Read(p)
	if err != nil {
		t.Fatalf("Read(%s): %v", filepath.Base(p), err)
	}
	return m
}

func TestEPUBMeta(t *testing.T) {
	want := bookmeta.Meta{
		Title: "Мастер и Маргарита", Authors: []string{"Михаил Булгаков"},
		Series: "Классика", SeriesIndex: 2, Lang: "ru",
		Description: "Роман о дьяволе & Москве", ISBN: "978-5-17-090814-7", Publisher: "АСТ", Year: 1967,
		Cover: jpegBytes(t), CoverType: "jpg",
	}
	got := read(t, write(t, "Мастер и Маргарита.epub", bookmetatest.EPUB(t, want)))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", withoutCover(got), withoutCover(want))
	}
}

func withoutCover(m bookmeta.Meta) bookmeta.Meta {
	m.Cover = []byte{byte(len(m.Cover) % 256)}
	return m
}

func TestEPUB3CoverImage(t *testing.T) {
	want := bookmeta.Meta{
		Title: "Пикник на обочине", Authors: []string{"Аркадий Стругацкий", "Борис Стругацкий"},
		Series: "Мир Полудня", SeriesIndex: 1.5, Lang: "ru", ISBN: "9785170908147", Year: 1972,
		Cover: pngBytes(t), CoverType: "png",
	}
	got := read(t, write(t, "Пикник.epub", bookmetatest.EPUB3(t, want)))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", withoutCover(got), withoutCover(want))
	}
}

func warAndPeace(t *testing.T) bookmeta.Meta {
	return bookmeta.Meta{
		Title: "Война и мир", Authors: []string{"Лев Николаевич Толстой"},
		Series: "Эпопея", SeriesIndex: 1, Lang: "ru", Description: "Том первый «Войны и мира»",
		ISBN: "5-699-12014-7", Publisher: "Эксмо", Year: 1869,
		Cover: jpegBytes(t), CoverType: "jpg",
	}
}

func TestFB2CP1251(t *testing.T) {
	want := warAndPeace(t)
	data := bookmetatest.FB2(t, want, "windows-1251")
	if bytes.Contains(data, []byte("Война")) {
		t.Fatal("fixture is not CP1251")
	}
	got := read(t, write(t, "Война и мир.fb2", data))
	if got.Title != "Война и мир" || !reflect.DeepEqual(got.Authors, []string{"Лев Николаевич Толстой"}) {
		t.Fatalf("title/authors: %q %q", got.Title, got.Authors)
	}
	if !bytes.Equal(got.Cover, want.Cover) || got.CoverType != "jpg" {
		t.Fatalf("cover not decoded: %d bytes, %q", len(got.Cover), got.CoverType)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", withoutCover(got), withoutCover(want))
	}
}

func TestFB2KOI8R(t *testing.T) {
	want := warAndPeace(t)
	want.Description = "Том первый" // KOI8-R has no guillemets
	got := read(t, write(t, "w.fb2", bookmetatest.FB2(t, want, "koi8-r")))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", withoutCover(got), withoutCover(want))
	}
}

func TestFB2Zip(t *testing.T) {
	want := warAndPeace(t)
	fb2 := bookmetatest.FB2(t, want, "windows-1251")
	got := read(t, write(t, "Tolstoj_Vojna_i_mir.12345.FB2.ZIP", bookmetatest.FB2Zip(t, "Tolstoj_Vojna_i_mir.fb2", fb2)))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", withoutCover(got), withoutCover(want))
	}
}

func TestReadNameOnlyFormats(t *testing.T) {
	for _, n := range []string{"a.pdf", "b.mobi", "c.AZW3"} {
		got := read(t, write(t, n, []byte("%PDF-1.4 garbage")))
		if !reflect.DeepEqual(got, bookmeta.Meta{}) {
			t.Fatalf("%s: %+v", n, got)
		}
	}
}

func TestReadBroken(t *testing.T) {
	garbage := bytes.Repeat([]byte{0xde, 0xad}, 50)
	for _, n := range []string{"x.epub", "x.fb2", "x.fb2.zip"} {
		if _, err := bookmeta.Read(write(t, n, garbage)); err == nil {
			t.Fatalf("%s: no error for garbage", n)
		}
	}
	// a zip without an fb2 inside
	z := bookmetatest.Zip(t, []string{"a.txt"}, map[string][]byte{"a.txt": []byte("x")})
	if _, err := bookmeta.Read(write(t, "y.fb2.zip", z)); err == nil {
		t.Fatal("fb2.zip without fb2: no error")
	}
	if _, err := bookmeta.Read(write(t, "y.epub", z)); err == nil {
		t.Fatal("epub without container.xml: no error")
	}
}

func TestFB2UndeclaredCP1251(t *testing.T) {
	want := warAndPeace(t)
	data := bookmetatest.FB2(t, want, "windows-1251")
	// a prolog that names no charset (or wrongly says UTF-8)
	for _, prolog := range []string{`<?xml version="1.0"?>`, `<?xml version="1.0" encoding="UTF-8"?>`} {
		fixed := append([]byte(prolog), data[bytes.IndexByte(data, '>')+1:]...)
		got := read(t, write(t, "w.fb2", fixed))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %+v", prolog, withoutCover(got))
		}
	}
}
