package bookmeta

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setLimits shrinks the size limits for one test.
func setLimits(t *testing.T, xml, cover, fb2 int64) {
	t.Helper()
	ox, oc, of := maxXML, maxCover, maxFB2
	maxXML, maxCover, maxFB2 = xml, cover, fb2
	t.Cleanup(func() { maxXML, maxCover, maxFB2 = ox, oc, of })
}

// bigFB2 writes an FB2 whose body paragraph, illustration and cover are of
// the given sizes (bytes of text / of decoded binary data).
func bigFB2(t *testing.T, annotation, body, picture, cover int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
<description><title-info><author><last-name>Толстой</last-name></author><book-title>Большая книга</book-title>
<annotation><p>`)
	b.WriteString(strings.Repeat("а", annotation/2))
	b.WriteString(`</p></annotation><coverpage><image l:href="#cover.jpg"/></coverpage></title-info></description>
<body><section><p>`)
	b.WriteString(strings.Repeat("текст ", body/11))
	b.WriteString("</p></section></body>\n")
	bin := func(id string, n int) {
		data := bytes.Repeat([]byte{0xFF, 0xD8, 0xFF, 0xE0}, n/4)
		b.WriteString(`<binary id="` + id + `" content-type="image/jpeg">`)
		enc := base64.StdEncoding.EncodeToString(data)
		for len(enc) > 76 {
			b.WriteString(enc[:76] + "\n")
			enc = enc[76:]
		}
		b.WriteString(enc + "</binary>\n")
	}
	bin("pic.jpg", picture)
	bin("cover.jpg", cover)
	b.WriteString("</FictionBook>\n")
	p := filepath.Join(t.TempDir(), "big.fb2")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestFB2MemoryBounded(t *testing.T) {
	setLimits(t, 1<<20, 1<<20, 256<<20)
	// 8 MB body text, 8 MB illustration, a 3 MB cover (over the 1 MB limit)
	p := bigFB2(t, 1000, 8<<20, 8<<20, 3<<20)
	var m Meta
	var err error
	n := allocated(func() { m, err = Read(p) })
	if err != nil || m.Title != "Большая книга" || m.Cover != nil {
		t.Fatalf("err %v title %q cover %d", err, m.Title, len(m.Cover))
	}
	// bounded by the limits (the cover is decoded up to maxCover before it
	// is dropped), not by the 19 MB file
	if n > 3<<20 {
		t.Fatalf("Read allocated %d bytes for a 19 MB file", n)
	}

	// a cover under the limit after the big parts is still found
	p = bigFB2(t, 1000, 8<<20, 8<<20, 512<<10)
	n = allocated(func() { m, err = Read(p) })
	if err != nil || len(m.Cover) != 512<<10 || m.CoverType != "jpg" {
		t.Fatalf("err %v cover %d %q", err, len(m.Cover), m.CoverType)
	}
	if n > 3<<20 {
		t.Fatalf("Read allocated %d bytes for a 512 KB cover", n)
	}
}

func TestFB2DescriptionOverLimit(t *testing.T) {
	setLimits(t, 64<<10, 1<<20, 256<<20)
	if _, err := Read(bigFB2(t, 256<<10, 10, 10, 10)); err == nil {
		t.Fatal("description over maxXML: no error")
	}
}

func TestFB2FileLimit(t *testing.T) {
	setLimits(t, 1<<20, 1<<20, 4<<10)
	// the file is cut before </description> ends
	if _, err := Read(bigFB2(t, 16<<10, 10, 10, 10)); err == nil {
		t.Fatal("fb2 over maxFB2: no error")
	}
}

func epubWith(t *testing.T, opfPad, cover int) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	add("META-INF/container.xml", []byte(`<container><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`))
	add("content.opf", []byte(`<package><metadata><dc:title xmlns:dc="http://purl.org/dc/elements/1.1/">Книга</dc:title><meta name="cover" content="c"/></metadata>
<!--`+strings.Repeat("x", opfPad)+`-->
<manifest><item id="c" href="c.jpg" media-type="image/jpeg"/></manifest></package>`))
	add("c.jpg", bytes.Repeat([]byte{0xFF}, cover))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "b.epub")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEPUBLimits(t *testing.T) {
	setLimits(t, 8<<10, 16<<10, 256<<20)
	m, err := Read(epubWith(t, 100, 1000))
	if err != nil || m.Title != "Книга" || len(m.Cover) != 1000 {
		t.Fatalf("small: %v %q %d", err, m.Title, len(m.Cover))
	}
	if _, err := Read(epubWith(t, 16<<10, 1000)); err == nil {
		t.Fatal("OPF over maxXML: no error")
	}
	m, err = Read(epubWith(t, 100, 32<<10))
	if err != nil || m.Title != "Книга" || m.Cover != nil {
		t.Fatalf("cover over maxCover: %v %q %d", err, m.Title, len(m.Cover))
	}
}

// rawEPUB packs an OPF (at content/book.opf) and extra members.
func rawEPUB(t *testing.T, opf string, extra map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string][]byte{
		"META-INF/container.xml": []byte(`<container><rootfiles><rootfile full-path="content/book.opf"/></rootfiles></container>`),
		"content/book.opf":       []byte(opf),
	}
	for k, v := range extra {
		files[k] = v
	}
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "r.epub")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEPUBCoverFallbacks(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0000")
	// <meta name="cover"> holding an href instead of an id
	p := rawEPUB(t, `<package><metadata><meta name="cover" content="img/c.png"/></metadata>
<manifest><item id="x" href="img/c.png" media-type="image/png"/></manifest></package>`, map[string][]byte{"content/img/c.png": png})
	if m, err := Read(p); err != nil || m.CoverType != "png" || !bytes.Equal(m.Cover, png) {
		t.Fatalf("href in meta cover: %v %q", err, m.CoverType)
	}
	// only a guide reference to a cover page with an <img>
	p = rawEPUB(t, `<package><metadata/><manifest/><guide><reference type="cover" href="text/cover.xhtml#top"/></guide></package>`,
		map[string][]byte{
			"content/text/cover.xhtml":      []byte(`<html><body><div><img alt="" src="../img/Обложка%20книги.png"/></div></body></html>`),
			"content/img/Обложка книги.png": png,
		})
	if m, err := Read(p); err != nil || m.CoverType != "png" || !bytes.Equal(m.Cover, png) {
		t.Fatalf("guide cover page: %v %q", err, m.CoverType)
	}
	// a guide reference straight to an image
	p = rawEPUB(t, `<package><metadata/><manifest/><guide><reference type="Cover" href="c.png"/></guide></package>`,
		map[string][]byte{"content/c.png": png})
	if m, err := Read(p); err != nil || m.CoverType != "png" {
		t.Fatalf("guide image: %v %q", err, m.CoverType)
	}
}

func TestEPUBBareISBNNeedsCheckDigit(t *testing.T) {
	for id, want := range map[string]string{"978-0-306-40615-7": "978-0-306-40615-7", "9780306406158": "", "0306406152": "0306406152", "1234567890": ""} {
		p := rawEPUB(t, `<package><metadata><dc:identifier xmlns:dc="http://purl.org/dc/elements/1.1/">`+id+`</dc:identifier></metadata></package>`, nil)
		m, err := Read(p)
		if err != nil || m.ISBN != want {
			t.Errorf("identifier %s: ISBN %q, want %q (%v)", id, m.ISBN, want, err)
		}
	}
}

func TestFB2OtherCharsets(t *testing.T) {
	// cp866 is reached through htmlindex
	doc := "<?xml version=\"1.0\" encoding=\"ibm866\"?><FictionBook><description><title-info><book-title>\x8a\xad\xa8\xa3\xa0</book-title></title-info></description></FictionBook>"
	p := filepath.Join(t.TempDir(), "866.fb2")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, err := Read(p); err != nil || m.Title != "Книга" {
		t.Fatalf("cp866: %v %q", err, m.Title)
	}
}
