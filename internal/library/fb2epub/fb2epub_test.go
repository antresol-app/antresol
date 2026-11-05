package fb2epub

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func pngB64(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func sample(t *testing.T) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
<description><title-info>
  <author><first-name>Лев</first-name><last-name>Толстой</last-name></author>
  <book-title>Война &amp; мир</book-title>
  <coverpage><image l:href="#cover.png"/></coverpage>
  <lang>ru</lang>
  <sequence name="Классика" number="2"/>
</title-info></description>
<body>
  <title><p>Война и мир</p></title>
  <epigraph><p>Эпиграф</p><text-author>Автор</text-author></epigraph>
  <section id="ch1">
    <title><p>Глава</p><p>первая</p></title>
    <p id="dup">Текст <emphasis>курсив</emphasis> <strong>жирный</strong><a l:href="#n1" type="note">[1]</a> &lt;теги&gt;</p>
    <empty-line/>
    <image l:href="#pic.png"/>
    <image l:href="#missing.png"/>
    <section><title><p>Подглава</p></title><p id="dup">Вложенный <a l:href="#nowhere">без цели</a> и <a l:href="https://example.com/?a=1&amp;b=2">сайт</a></p></section>
    <poem><title><p>Стих</p></title><stanza><v>Строка</v></stanza><text-author>Поэт</text-author></poem>
    <cite><p>Цитата</p></cite>
    <table><tr><th colspan="2" onclick="x()">Шапка</th></tr><tr><td>1</td><td>2</td></tr></table>
  </section>
  <section><p>Без названия</p></section>
</body>
<body name="notes">
  <title><p>Примечания</p></title>
  <section id="n1"><title><p>1</p></title><p>Сноска</p></section>
</body>
<binary id="cover.png" content-type="image/png">` + pngB64(t) + `</binary>
<binary id="pic.png" content-type="image/png">` + pngB64(t) + `</binary>
<binary id="bad.png" content-type="image/png">not base64 !!!</binary>
</FictionBook>`
}

// open converts src and returns the EPUB's files by name.
func open(t *testing.T, src string, opt Options) (map[string]string, []string) {
	t.Helper()
	var out bytes.Buffer
	if err := Convert(strings.NewReader(src), &out, opt); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	var order []string
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
		order = append(order, f.Name)
		if f.Name == "mimetype" && (f.Method != zip.Store || f.Flags&0x8 != 0 || len(f.Extra) != 0) {
			t.Errorf("mimetype must be stored, sizes in its header: method %d, flags %#x, extra %d", f.Method, f.Flags, len(f.Extra))
		}
	}
	return files, order
}

func wellFormed(t *testing.T, name, doc string) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(doc))
	for {
		if _, err := d.Token(); err == io.EOF {
			return
		} else if err != nil {
			t.Errorf("%s: %v\n%s", name, err, doc)
			return
		}
	}
}

func all(files map[string]string) string {
	var b strings.Builder
	for n, c := range files {
		if strings.HasSuffix(n, ".xhtml") {
			b.WriteString(c)
		}
	}
	return b.String()
}

func TestStructureAndMetadata(t *testing.T) {
	files, order := open(t, sample(t), Options{})
	if order[0] != "mimetype" || files["mimetype"] != "application/epub+zip" {
		t.Fatalf("first entry %q = %q", order[0], files[order[0]])
	}
	if !strings.Contains(files["META-INF/container.xml"], `full-path="OEBPS/content.opf"`) {
		t.Error("container does not name the OPF")
	}
	for n, c := range files {
		if strings.HasSuffix(n, ".xhtml") || strings.HasSuffix(n, ".opf") || strings.HasSuffix(n, ".ncx") || strings.HasSuffix(n, ".xml") {
			wellFormed(t, n, c)
		}
	}
	opf := files["OEBPS/content.opf"]
	for _, want := range []string{
		`<dc:title>Война &amp; мир</dc:title>`,
		`<dc:creator>Лев Толстой</dc:creator>`,
		`<dc:language>ru</dc:language>`,
		`<meta name="cover" content="cover-image"/>`,
		`properties="cover-image"`,
		`properties="nav"`,
		`dcterms:modified`,
		`Классика`,
	} {
		if !strings.Contains(opf, want) {
			t.Errorf("OPF lacks %s:\n%s", want, opf)
		}
	}
}

func TestEveryManifestItemExistsAndViceVersa(t *testing.T) {
	files, _ := open(t, sample(t), Options{})
	var pkg struct {
		Items []struct {
			Href string `xml:"href,attr"`
		} `xml:"manifest>item"`
	}
	if err := xml.Unmarshal([]byte(files["OEBPS/content.opf"]), &pkg); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, it := range pkg.Items {
		listed["OEBPS/"+it.Href] = true
		if _, ok := files["OEBPS/"+it.Href]; !ok {
			t.Errorf("manifest item %s is not in the archive", it.Href)
		}
	}
	for n := range files {
		if strings.HasPrefix(n, "OEBPS/") && n != "OEBPS/content.opf" && !listed[n] {
			t.Errorf("%s is not in the manifest", n)
		}
	}
}

func TestContent(t *testing.T) {
	files, _ := open(t, sample(t), Options{})
	x := all(files)
	for _, want := range []string{
		`<em>курсив</em>`, `<strong>жирный</strong>`, `&lt;теги&gt;`,
		`Глава<br/>первая`,
		`Эпиграф`, `Цитата`, `Строка`, `Сноска`, `Без названия`,
		`href="https://example.com/?a=1&amp;b=2"`,
		`colspan="2"`,
		`epub:type="noteref"`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("no %s in the book", want)
		}
	}
	if strings.Contains(x, "onclick") {
		t.Error("an unknown attribute passed through")
	}
	if strings.Contains(x, "missing.png") {
		t.Error("a picture that does not exist is referenced")
	}
	if !strings.Contains(x, "без цели") || strings.Contains(x, "#nowhere") {
		t.Error("a link without a target must stay plain text")
	}
	if strings.Count(x, `id="dup"`) != 1 {
		t.Errorf("duplicate ids: %d", strings.Count(x, `id="dup"`))
	}
}

func TestNoteLinkPointsAtNotesFile(t *testing.T) {
	files, _ := open(t, sample(t), Options{})
	var target, holder string
	for n, c := range files {
		if i := strings.Index(c, `epub:type="noteref" href="`); i >= 0 {
			rest := c[i+len(`epub:type="noteref" href="`):]
			target = rest[:strings.IndexByte(rest, '"')]
		}
		if strings.HasSuffix(n, ".xhtml") && !strings.HasSuffix(n, "nav.xhtml") && strings.Contains(c, `id="n1"`) {
			holder = strings.TrimPrefix(n, "OEBPS/")
		}
	}
	if holder == "" || target != holder+"#n1" {
		t.Fatalf("note link %q, the note is in %q", target, holder)
	}
}

func TestTableOfContents(t *testing.T) {
	files, _ := open(t, sample(t), Options{})
	nav, ncx := files["OEBPS/nav.xhtml"], files["OEBPS/toc.ncx"]
	for _, want := range []string{"Глава первая", "Подглава", "Примечания"} {
		if !strings.Contains(nav, want) {
			t.Errorf("nav lacks %s:\n%s", want, nav)
		}
		if !strings.Contains(ncx, want) {
			t.Errorf("ncx lacks %s:\n%s", want, ncx)
		}
	}
	if strings.Contains(nav, "Без названия") {
		t.Error("an untitled section is in the table of contents")
	}
}

func TestPictures(t *testing.T) {
	files, _ := open(t, sample(t), Options{})
	n := 0
	for name, c := range files {
		if strings.HasPrefix(name, "OEBPS/images/") {
			n++
			if !strings.HasPrefix(c, "\x89PNG") {
				t.Errorf("%s is not the decoded picture", name)
			}
		}
	}
	if n != 2 {
		t.Errorf("%d pictures, want 2 (the broken one is dropped)", n)
	}
	if !strings.Contains(files["OEBPS/cover.xhtml"], "<img") {
		t.Error("no cover page")
	}
	if strings.Contains(files["OEBPS/content.opf"], "bad.png") {
		t.Error("the broken picture is listed")
	}
}

func TestWindows1251(t *testing.T) {
	src := `<?xml version="1.0" encoding="windows-1251"?><FictionBook><description><title-info><book-title>Заголовок</book-title></title-info></description><body><section><p>Привет</p></section></body></FictionBook>`
	enc, err := charmap.Windows1251.NewEncoder().String(src)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := open(t, enc, Options{})
	if !strings.Contains(all(files), "Привет") || !strings.Contains(files["OEBPS/content.opf"], "Заголовок") {
		t.Fatal("not decoded")
	}
}

func TestHTMLEntitiesAndUnknownTagsSurvive(t *testing.T) {
	files, _ := open(t, `<FictionBook><body><section><p>а&nbsp;б&mdash;в <blink>г</blink></p>лишний текст</section></body></FictionBook>`, Options{Title: "Из имени"})
	x := all(files)
	if !strings.Contains(x, "а\u00a0б—в г") {
		t.Errorf("text lost:\n%s", x)
	}
	if !strings.Contains(x, "<p>лишний текст</p>") {
		t.Errorf("stray text is not wrapped:\n%s", x)
	}
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:title>Из имени</dc:title>") {
		t.Error("no fallback title")
	}
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:language>ru</dc:language>") {
		t.Error("no default language")
	}
}

func TestBadLanguageGetsDefault(t *testing.T) {
	files, _ := open(t, `<FictionBook><description><title-info><book-title>Т</book-title><lang>&quot;&gt;&lt;x</lang></title-info></description><body><section><p>а</p></section></body></FictionBook>`, Options{})
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:language>ru</dc:language>") {
		t.Errorf("%s", files["OEBPS/content.opf"])
	}
}

func TestDocumentWithoutTitledSectionsStillHasToc(t *testing.T) {
	files, _ := open(t, `<FictionBook><body><p>Просто текст</p></body></FictionBook>`, Options{Title: "Книга"})
	if !strings.Contains(files["OEBPS/nav.xhtml"], "Книга") || !strings.Contains(files["OEBPS/toc.ncx"], "Книга") {
		t.Fatalf("empty table of contents:\n%s", files["OEBPS/nav.xhtml"])
	}
}

func TestNotFB2(t *testing.T) {
	for _, src := range []string{"", "not xml", `<html><body><p>а</p></body></html>`, `<FictionBook><description/></FictionBook>`} {
		var out bytes.Buffer
		if err := Convert(strings.NewReader(src), &out, Options{}); !errors.Is(err, ErrNotFB2) {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestHostileNestingFailsCleanly(t *testing.T) {
	src := `<FictionBook><body>` + strings.Repeat("<section>", 100000) + strings.Repeat("</section>", 100000) + `</body></FictionBook>`
	var out bytes.Buffer
	if err := Convert(strings.NewReader(src), &out, Options{}); !errors.Is(err, ErrNotFB2) {
		t.Fatalf("err = %v", err)
	}
}

func TestOutputLimitPropagates(t *testing.T) {
	var w failWriter
	if err := Convert(strings.NewReader(sample(t)), &w, Options{}); !errors.Is(err, errSink) {
		t.Fatalf("err = %v", err)
	}
}

var errSink = errors.New("full")

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errSink }
