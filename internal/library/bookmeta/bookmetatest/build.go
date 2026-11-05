// Package bookmetatest builds small EPUB and FB2 files for tests of the
// book reader and the book scanner.
package bookmetatest

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"github.com/antresol-app/antresol/internal/library/bookmeta"
)

func esc(s string) string { return html.EscapeString(s) }

func mimeOf(ext string) string {
	switch ext {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	}
	return "application/octet-stream"
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// Zip packs files into a ZIP archive in the order of names; an entry
// named mimetype is stored uncompressed (as EPUB requires).
func Zip(t testing.TB, names []string, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		method := zip.Deflate
		if n == "mimetype" {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(files[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const container = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`

// EPUB builds an EPUB 2 book: Dublin Core metadata, the series in
// calibre:series / calibre:series_index, the cover via <meta name="cover">.
// The description is written as escaped HTML (<p>…</p>).
func EPUB(t testing.TB, m bookmeta.Meta) []byte {
	t.Helper()
	return epub(t, m, false)
}

// EPUB3 builds an EPUB 3 book: the series in belongs-to-collection with
// group-position, the cover as a manifest item with properties="cover-image".
func EPUB3(t testing.TB, m bookmeta.Meta) []byte {
	t.Helper()
	return epub(t, m, true)
}

func epub(t testing.TB, m bookmeta.Meta, v3 bool) []byte {
	t.Helper()
	var md strings.Builder
	fmt.Fprintf(&md, "    <dc:title>%s</dc:title>\n", esc(m.Title))
	for i, a := range m.Authors {
		if v3 {
			fmt.Fprintf(&md, "    <dc:creator id=\"c%d\">%s</dc:creator>\n", i, esc(a))
			fmt.Fprintf(&md, "    <meta refines=\"#c%d\" property=\"role\" scheme=\"marc:relators\">aut</meta>\n", i)
		} else {
			fmt.Fprintf(&md, "    <dc:creator opf:role=\"aut\">%s</dc:creator>\n", esc(a))
		}
	}
	if !v3 {
		// an illustrator is not an author
		md.WriteString("    <dc:creator opf:role=\"ill\">Художник Иллюстраторов</dc:creator>\n")
	}
	if m.Lang != "" {
		fmt.Fprintf(&md, "    <dc:language>%s</dc:language>\n", esc(m.Lang))
	}
	if m.Description != "" {
		fmt.Fprintf(&md, "    <dc:description>%s</dc:description>\n", esc("<p>"+esc(m.Description)+"</p>"))
	}
	md.WriteString("    <dc:identifier id=\"uid\">urn:uuid:0b6f7a52-5b9d-4c3e-9a51-1f0c2f2d1a11</dc:identifier>\n")
	if m.ISBN != "" {
		if v3 {
			fmt.Fprintf(&md, "    <dc:identifier>urn:isbn:%s</dc:identifier>\n", esc(m.ISBN))
		} else {
			fmt.Fprintf(&md, "    <dc:identifier opf:scheme=\"ISBN\">%s</dc:identifier>\n", esc(m.ISBN))
		}
	}
	if m.Publisher != "" {
		fmt.Fprintf(&md, "    <dc:publisher>%s</dc:publisher>\n", esc(m.Publisher))
	}
	if m.Year != 0 {
		fmt.Fprintf(&md, "    <dc:date>%04d-01-01</dc:date>\n", m.Year)
	}
	if m.Series != "" {
		if v3 {
			fmt.Fprintf(&md, "    <meta property=\"belongs-to-collection\" id=\"s1\">%s</meta>\n", esc(m.Series))
			md.WriteString("    <meta refines=\"#s1\" property=\"collection-type\">series</meta>\n")
			if m.SeriesIndex != 0 {
				fmt.Fprintf(&md, "    <meta refines=\"#s1\" property=\"group-position\">%s</meta>\n", num(m.SeriesIndex))
			}
		} else {
			fmt.Fprintf(&md, "    <meta name=\"calibre:series\" content=\"%s\"/>\n", esc(m.Series))
			if m.SeriesIndex != 0 {
				fmt.Fprintf(&md, "    <meta name=\"calibre:series_index\" content=\"%s\"/>\n", num(m.SeriesIndex))
			}
		}
	}
	var man strings.Builder
	man.WriteString("    <item id=\"text\" href=\"text/ch1.xhtml\" media-type=\"application/xhtml+xml\"/>\n")
	files := map[string][]byte{
		"mimetype":               []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(container),
		"OEBPS/text/ch1.xhtml":   []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Глава 1</p></body></html>`),
	}
	names := []string{"mimetype", "META-INF/container.xml", "OEBPS/content.opf", "OEBPS/text/ch1.xhtml"}
	if len(m.Cover) > 0 {
		href := "images/Обложка книги." + m.CoverType
		if v3 {
			fmt.Fprintf(&man, "    <item id=\"img\" href=\"%s\" media-type=\"%s\" properties=\"cover-image\"/>\n",
				esc(strings.ReplaceAll(href, " ", "%20")), mimeOf(m.CoverType))
		} else {
			md.WriteString("    <meta name=\"cover\" content=\"cover-img\"/>\n")
			fmt.Fprintf(&man, "    <item id=\"cover-img\" href=\"%s\" media-type=\"%s\"/>\n",
				esc(strings.ReplaceAll(href, " ", "%20")), mimeOf(m.CoverType))
		}
		// a decoy image listed before the cover
		man.WriteString("    <item id=\"pic\" href=\"images/map.png\" media-type=\"image/png\"/>\n")
		files["OEBPS/images/map.png"] = []byte("not the cover")
		files["OEBPS/"+href] = m.Cover
		names = append(names, "OEBPS/images/map.png", "OEBPS/"+href)
	}
	version := "2.0"
	if v3 {
		version = "3.0"
	}
	opf := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="%s" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
%s  </metadata>
  <manifest>
%s  </manifest>
  <spine><itemref idref="text"/></spine>
</package>`, version, md.String(), man.String())
	files["OEBPS/content.opf"] = []byte(opf)
	return Zip(t, names, files)
}

// FB2 builds a FictionBook 2 file encoded as enc ("utf-8", "windows-1251"
// or "koi8-r"; the prolog names it). An author's words go to first-name,
// middle-name and last-name (one word: last-name only; two: first and
// last). The cover is a base64 <binary> referenced from <coverpage>.
func FB2(t testing.TB, m bookmeta.Meta, enc string) []byte {
	t.Helper()
	var ti strings.Builder
	ti.WriteString("      <genre>prose_classic</genre>\n")
	for _, a := range m.Authors {
		w := strings.Fields(a)
		ti.WriteString("      <author>")
		switch len(w) {
		case 0:
		case 1:
			fmt.Fprintf(&ti, "<last-name>%s</last-name>", esc(w[0]))
		case 2:
			fmt.Fprintf(&ti, "<first-name>%s</first-name><last-name>%s</last-name>", esc(w[0]), esc(w[1]))
		default:
			fmt.Fprintf(&ti, "<first-name>%s</first-name><middle-name>%s</middle-name><last-name>%s</last-name>",
				esc(w[0]), esc(strings.Join(w[1:len(w)-1], " ")), esc(w[len(w)-1]))
		}
		ti.WriteString("</author>\n")
	}
	fmt.Fprintf(&ti, "      <book-title>%s</book-title>\n", esc(m.Title))
	if m.Description != "" {
		fmt.Fprintf(&ti, "      <annotation><p>%s</p></annotation>\n", esc(m.Description))
	}
	if m.Year != 0 {
		fmt.Fprintf(&ti, "      <date value=\"%04d-01-01\">%04d</date>\n", m.Year, m.Year)
	}
	if len(m.Cover) > 0 {
		fmt.Fprintf(&ti, "      <coverpage><image l:href=\"#cover.%s\"/></coverpage>\n", m.CoverType)
	}
	if m.Lang != "" {
		fmt.Fprintf(&ti, "      <lang>%s</lang>\n", esc(m.Lang))
	}
	if m.Series != "" {
		if m.SeriesIndex != 0 {
			fmt.Fprintf(&ti, "      <sequence name=\"%s\" number=\"%s\"/>\n", esc(m.Series), num(m.SeriesIndex))
		} else {
			fmt.Fprintf(&ti, "      <sequence name=\"%s\"/>\n", esc(m.Series))
		}
	}
	var pi strings.Builder
	if m.Publisher != "" {
		fmt.Fprintf(&pi, "      <publisher>%s</publisher>\n", esc(m.Publisher))
	}
	if m.ISBN != "" {
		fmt.Fprintf(&pi, "      <isbn>%s</isbn>\n", esc(m.ISBN))
	}
	var bin strings.Builder
	// an illustration before the cover
	bin.WriteString("  <binary id=\"pic1.png\" content-type=\"image/png\">bm90IHRoZSBjb3Zlcg==</binary>\n")
	if len(m.Cover) > 0 {
		b64 := base64.StdEncoding.EncodeToString(m.Cover)
		var wrapped strings.Builder
		for len(b64) > 60 {
			wrapped.WriteString(b64[:60] + "\n")
			b64 = b64[60:]
		}
		wrapped.WriteString(b64)
		fmt.Fprintf(&bin, "  <binary id=\"cover.%s\" content-type=\"%s\">\n%s\n</binary>\n", m.CoverType, mimeOf(m.CoverType), wrapped.String())
	}
	doc := fmt.Sprintf(`<?xml version="1.0" encoding="%s"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
  <description>
    <title-info>
%s    </title-info>
    <document-info><author><nickname>scanner</nickname></author><program-used>FictionBook Editor</program-used></document-info>
    <publish-info>
%s    </publish-info>
  </description>
  <body><title><p>%s</p></title><section><p>Текст&#160;книги.</p></section></body>
%s</FictionBook>
`, enc, ti.String(), pi.String(), esc(m.Title), bin.String())
	switch strings.ToLower(enc) {
	case "utf-8", "":
		return []byte(doc)
	case "windows-1251":
		b, err := charmap.Windows1251.NewEncoder().Bytes([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		return b
	case "koi8-r":
		b, err := charmap.KOI8R.NewEncoder().Bytes([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	t.Fatalf("bookmetatest: unsupported encoding %q", enc)
	return nil
}

// FB2Zip packs an FB2 file into a .fb2.zip archive under name (a README
// goes first, as some archives have one).
func FB2Zip(t testing.TB, name string, fb2 []byte) []byte {
	t.Helper()
	return Zip(t, []string{"readme.txt", name}, map[string][]byte{"readme.txt": []byte("a book site"), name: fb2})
}
