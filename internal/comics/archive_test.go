package comics

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"
)

func testJPEG(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type zipEntry struct {
	name    string
	data    []byte
	nonUTF8 bool
}

func writeZip(t *testing.T, path string, entries []zipEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate, NonUTF8: e.nonUTF8})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func readPage(t *testing.T, a *Archive, i int) ([]byte, string) {
	t.Helper()
	rc, ct, err := a.Page(i)
	if err != nil {
		t.Fatalf("page %d: %v", i, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("page %d read: %v", i, err)
	}
	return b, ct
}

func emptyDir(t *testing.T, dir string) {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(es) != 0 {
		var names []string
		for _, e := range es {
			names = append(names, e.Name())
		}
		t.Fatalf("%s not empty: %v", dir, names)
	}
}

const comicInfoXML = `<?xml version="1.0" encoding="utf-8"?>
<ComicInfo xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <Series>Берсерк</Series><Title>Чёрный мечник</Title><Number>3.5</Number><Volume>1</Volume>
  <Summary>Гатс.</Summary><Writer>Кэнтаро Миура</Writer><Year>1990</Year><PageCount>3</PageCount>
  <LanguageISO>ru</LanguageISO><Manga>YesAndRightToLeft</Manga><AgeRating>Adults Only 18+</AgeRating>
</ComicInfo>`

func TestPagesFilterAndNaturalSort(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "v01.cbz")
	j := testJPEG(t, color.White)
	writeZip(t, p, []zipEntry{
		{name: "__MACOSX/._001.jpg", data: []byte("junk")},
		{name: "Thumbs.db", data: []byte("junk")},
		{name: "ComicInfo.xml", data: []byte(comicInfoXML)},
		{name: "credits.txt", data: []byte("x")},
		{name: "chapter 2/10.jpg", data: j},
		{name: "chapter 1/1.png", data: j},
		{name: "chapter 2/2.jpg", data: j},
		{name: ".hidden.jpg", data: j},
		{name: "chapter 2/.DS_Store", data: []byte("x")},
	})
	want := []string{"chapter 1/1.png", "chapter 2/2.jpg", "chapter 2/10.jpg"}
	a, err := Open(p, filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if got := a.Pages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("pages %q", got)
	}
	info, ok := a.Info()
	if !ok || info.Series != "Берсерк" || info.Number != 3.5 || info.Volume != 1 || info.Writer != "Кэнтаро Миура" ||
		info.Year != 1990 || info.PageCount != 3 || info.LanguageISO != "ru" || info.Title != "Чёрный мечник" || info.Summary != "Гатс." {
		t.Fatalf("info %+v %v", info, ok)
	}
	if _, ct := readPage(t, a, 0); ct != "image/png" {
		t.Fatalf("content type by name: %q", ct)
	}
	if _, _, err := a.Page(3); err == nil {
		t.Fatal("page out of range")
	}
	emptyDir(t, filepath.Join(dir, "cache")) // a zip is read in place

	pages, pinfo, has, cover, err := Peek(p)
	if err != nil || !reflect.DeepEqual(pages, want) || !has || pinfo != info || !bytes.Equal(cover, j) {
		t.Fatalf("peek %q %v %v %d", pages, has, err, len(cover))
	}
}

func TestOpenCBRRar5(t *testing.T) {
	testFixture(t, "testdata/rar5.cbr")
}

func TestOpenCB7(t *testing.T) {
	testFixture(t, "testdata/three.cb7")
}

func testFixture(t *testing.T, fixture string) {
	cache := t.TempDir()
	a, err := Open(fixture, cache)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Pages(); !reflect.DeepEqual(got, []string{"1.jpg", "2.jpg", "3.jpg"}) {
		t.Fatalf("pages %q", got)
	}
	b, ct := readPage(t, a, 2)
	if ct != "image/jpeg" {
		t.Fatalf("content type %q", ct)
	}
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if r, g, bl, _ := img.At(4, 4).RGBA(); bl < 0xc000 || r > 0x4000 || g > 0x4000 {
		t.Fatalf("page 3 is not the blue one: %x %x %x", r, g, bl)
	}
	if _, ok := a.Info(); ok {
		t.Fatal("no ComicInfo.xml in the fixture")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	es, _ := os.ReadDir(cache)
	if len(es) != 1 {
		t.Fatalf("cache entries: %d", len(es))
	}
	// the second open reuses the unpacked pages
	a2, err := Open(fixture, cache)
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	if b2, _ := readPage(t, a2, 2); !bytes.Equal(b, b2) {
		t.Fatal("cached page differs")
	}

	pages, _, has, cover, err := Peek(fixture)
	if err != nil || len(pages) != 3 || has {
		t.Fatalf("peek %q %v %v", pages, has, err)
	}
	if first, _ := readPage(t, a2, 0); !bytes.Equal(cover, first) {
		t.Fatal("peek cover is not page 1")
	}
}

func TestPeekNoExtract(t *testing.T) {
	// Peek has no cache dir; it must not spill into the temp dir either
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, f := range []string{"testdata/rar5.cbr", "testdata/three.cb7"} {
		pages, _, _, cover, err := Peek(f)
		if err != nil || len(pages) != 3 || len(cover) == 0 {
			t.Fatalf("%s: %q %d %v", f, pages, len(cover), err)
		}
	}
	emptyDir(t, tmp)
}

func TestZipCP1251Names(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "v.cbz")
	name, err := charmap.Windows1251.NewEncoder().String("Глава 1/01.jpg")
	if err != nil {
		t.Fatal(err)
	}
	j := testJPEG(t, color.Black)
	writeZip(t, p, []zipEntry{
		{name: name, data: j, nonUTF8: true},
		{name: "Глава 1/02.jpg", data: j}, // valid UTF-8 stays as it is
	})
	a, err := Open(p, filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if got := a.Pages(); !reflect.DeepEqual(got, []string{"Глава 1/01.jpg", "Глава 1/02.jpg"}) {
		t.Fatalf("pages %q", got)
	}
}

func TestComicInfoDirection(t *testing.T) {
	for in, want := range map[string]string{
		"YesAndRightToLeft": "rtl", "yesandrighttoleft": "rtl", "No": "ltr", "Yes": "", "": "", "Unknown": "",
	} {
		if got := (ComicInfo{Manga: in}).Direction(); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestAgeRatingMap(t *testing.T) {
	for in, want := range map[string]string{
		"Adults Only 18+": "18+", "X18+": "18+", "R18+": "18+",
		"Mature 17+": "16+", "M": "16+",
		"Teen": "12+", "T": "12+",
		"Everyone 10+": "6+",
		"Everyone":     "0+", "G": "0+", "Early Childhood": "0+", " everyone ": "0+",
		"Rating Pending": "", "Unknown": "", "": "", "MA15+": "", "Kids to Adults": "", "PG": "",
	} {
		if got := ageRating(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if (ComicInfo{AgeRating: "Teen"}).Rating() != "12+" {
		t.Fatal("Rating()")
	}
}

func TestParseComicInfoLenient(t *testing.T) {
	ci, err := parseComicInfo([]byte(`<?xml version="1.0" encoding="windows-1251"?><ComicInfo><Series>` +
		mustCP1251(t, "Наруто") + `</Series><Number>12a</Number><Volume>bad</Volume><Year> 2001 </Year></ComicInfo>`))
	if err != nil || ci.Series != "Наруто" || ci.Number != 12 || ci.Volume != 0 || ci.Year != 2001 {
		t.Fatalf("%+v %v", ci, err)
	}
	if _, err := parseComicInfo([]byte("<html>")); err == nil {
		t.Fatal("not ComicInfo")
	}
}

func mustCP1251(t *testing.T, s string) string {
	t.Helper()
	out, err := charmap.Windows1251.NewEncoder().String(s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCacheEvictsLRU(t *testing.T) {
	old := cacheLimit
	cacheLimit = 2500 // one fixture (1800 bytes of pages) fits, two do not
	t.Cleanup(func() { cacheLimit = old })
	cache := t.TempDir()
	a, err := Open("testdata/rar5.cbr", cache)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	es, _ := os.ReadDir(cache)
	if len(es) != 1 {
		t.Fatalf("after first open: %d entries", len(es))
	}
	first := es[0].Name()
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(cache, first), past, past); err != nil {
		t.Fatal(err)
	}
	b, err := Open("testdata/three.cb7", cache)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	es, _ = os.ReadDir(cache)
	if len(es) != 1 || es[0].Name() == first {
		t.Fatalf("old entry not evicted: %v", es)
	}
	if _, _, err := b.Page(0); err != nil {
		t.Fatal("the open archive must survive eviction:", err)
	}
}

func TestCacheKeepsOpenArchives(t *testing.T) {
	old := cacheLimit
	cacheLimit = 2500 // one fixture (1800 bytes of pages) fits, two do not
	t.Cleanup(func() { cacheLimit = old })
	cache := t.TempDir()
	a, err := Open("testdata/rar5.cbr", cache)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open("testdata/three.cb7", cache)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, _, err := a.Page(1); err != nil {
		t.Fatal("an archive in use was evicted:", err)
	}
}

func TestExtractLimit(t *testing.T) {
	old := maxExtractBytes
	maxExtractBytes = 1000 // the fixture unpacks to 1800 bytes of pages
	t.Cleanup(func() { maxExtractBytes = old })
	cache := t.TempDir()
	if _, err := Open("testdata/three.cb7", cache); err == nil {
		t.Fatal("over the limit must fail")
	}
	emptyDir(t, cache) // nothing half-written is left
}

// A page that inflates past the page limit is cut off with an error, never
// read whole into memory; Peek gives no cover for it.
func TestZipBombPage(t *testing.T) {
	old := maxPageBytes
	maxPageBytes = 1 << 10
	t.Cleanup(func() { maxPageBytes = old })
	dir := t.TempDir()
	p := filepath.Join(dir, "bomb.cbz")
	writeZip(t, p, []zipEntry{{name: "1.jpg", data: bytes.Repeat([]byte{0}, 1<<20)}, {name: "2.jpg", data: testJPEG(t, color.White)}})
	a, err := Open(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	rc, _, err := a.Page(0)
	if err == nil {
		n, rerr := io.Copy(io.Discard, rc)
		rc.Close()
		if rerr == nil || n > maxPageBytes {
			t.Fatalf("read %d bytes, err %v", n, rerr)
		}
	}
	pages, _, _, cover, _ := Peek(p)
	if len(pages) != 2 || cover != nil {
		t.Fatalf("peek %q cover %d", pages, len(cover))
	}
}

func TestUnsafeNames(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc/x.jpg": "", "/abs/1.jpg": "abs/1.jpg", `win\dir\2.jpg`: "win/dir/2.jpg", "./a/./3.jpg": "a/3.jpg",
		"a/../../4.jpg": "", "C:/5.jpg": "", "ok.jpg": "ok.jpg", "": "", "a/": "",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestOpenFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Том 1")
	j := testJPEG(t, color.White)
	for _, n := range []string{"10.jpg", "2.jpg", "1.webp", ".hidden.jpg", "Thumbs.db", "notes.txt"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), j, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "ComicInfo.xml"), []byte(comicInfoXML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if got := a.Pages(); !reflect.DeepEqual(got, []string{"1.webp", "2.jpg", "10.jpg"}) {
		t.Fatalf("pages %q", got)
	}
	if _, ct := readPage(t, a, 0); ct != "image/webp" {
		t.Fatal(ct)
	}
	if info, ok := a.Info(); !ok || info.Series != "Берсерк" {
		t.Fatal("folder ComicInfo.xml")
	}
	pages, _, has, cover, err := Peek(dir)
	if err != nil || len(pages) != 3 || !has || !bytes.Equal(cover, j) {
		t.Fatalf("peek %q %v %v", pages, has, err)
	}
}

func TestBrokenArchives(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"x.cbz", "x.cbr", "x.cb7"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("not an archive at all"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, _, _, err := Peek(p); err == nil {
			t.Errorf("%s: peek must fail", n)
		}
		if _, err := Open(p, filepath.Join(dir, "cache")); err == nil {
			t.Errorf("%s: open must fail", n)
		}
	}
	if _, err := Open(filepath.Join(dir, "x.pdf"), dir); err == nil {
		t.Error("unknown format")
	}
	emptyDir(t, filepath.Join(dir, "cache"))
}

func TestCover(t *testing.T) {
	j := testJPEG(t, color.White)
	if out, ext := Cover(j); !bytes.Equal(out, j) || ext != "jpg" {
		t.Fatalf("small jpeg as is: %q", ext)
	}
	// a big PNG is shrunk to a 600 px wide JPEG
	img := image.NewNRGBA(image.Rect(0, 0, 1200, 1800))
	rnd := rand.New(rand.NewSource(1))
	rnd.Read(img.Pix)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if buf.Len() <= maxCoverAsIs {
		t.Fatalf("fixture too small: %d", buf.Len())
	}
	out, ext := Cover(buf.Bytes())
	if ext != "jpg" {
		t.Fatalf("ext %q", ext)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != 600 || cfg.Height != 900 {
		t.Fatalf("%+v %v", cfg, err)
	}
	avif := append([]byte{0, 0, 0, 0x1c}, []byte("ftypavif")...)
	if out, ext := Cover(append(avif, make([]byte, 64)...)); out != nil || ext != "" {
		t.Fatal("avif gives no cover")
	}
	if out, _ := Cover([]byte(strings.Repeat("x", 100))); out != nil {
		t.Fatal("garbage gives no cover")
	}
	// too many pixels: not decoded
	old := maxCoverPixels
	maxCoverPixels = 100
	t.Cleanup(func() { maxCoverPixels = old })
	if out, _ := Cover(buf.Bytes()); out != nil {
		t.Fatal("pixel bomb decoded")
	}
}

// An archive whose decoder would need more memory than maxDictBytes is
// refused before the dictionary is allocated (RAR option, 7z wrappers).
func TestDecoderMemoryLimit(t *testing.T) {
	old := maxDictBytes
	maxDictBytes = 100
	t.Cleanup(func() { maxDictBytes = old })
	for _, f := range []string{"testdata/rar5.cbr", "testdata/three.cb7"} {
		if _, err := Open(f, t.TempDir()); err == nil {
			t.Errorf("%s: open must fail", f)
		}
		if _, _, _, cover, err := Peek(f); err == nil && cover != nil {
			t.Errorf("%s: peek decoded a page", f)
		}
	}
}
