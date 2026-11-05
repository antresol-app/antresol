package api

import (
	"archive/zip"
	"bytes"
	"image"
	"image/jpeg"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zipEntry struct {
	name, body string
	stored     bool
}

func buildZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, e := range entries {
		m := zip.Deflate
		if e.stored {
			m = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: m})
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, e.body)
	}
	zw.Close()
	return b.Bytes()
}

func readZip(t *testing.T, data []byte) (map[string]string, []*zip.File) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		x, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(x)
	}
	return out, zr.File
}

func TestKindleSendsTheCleanCopyAndKeepsTheFile(t *testing.T) {
	e := newKindleEnv(t)
	sent := stubKindle(t, nil)
	e.configureMail()
	c := e.kindleProfile("")
	in := buildZip(t, zipEntry{"mimetype", "application/epub+zip", true}, zipEntry{"stamp-site.com", "x", true}, zipEntry{"a.xhtml", "текст", false})
	id := e.bookFile("Книга", "a.epub", in)
	if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	m := sent()[0]
	got, _ := readZip(t, m.data)
	if _, ok := got["stamp-site.com"]; ok || got["a.xhtml"] != "текст" || m.msg.Size != int64(len(m.data)) {
		t.Errorf("%v size %d/%d", got, m.msg.Size, len(m.data))
	}
	if onDisk := e.readMedia("a.epub"); !bytes.Equal(onDisk, in) {
		t.Error("the library's file changed")
	}
}

func TestUploadCleansTheBookAndNoticesTheSameBookAgain(t *testing.T) {
	e := newUploadEnv(t)
	in := buildZip(t, zipEntry{"mimetype", "application/epub+zip", true}, zipEntry{"stamp-site.com", "x", true}, zipEntry{"a.xhtml", "текст", false})
	if w := e.post("Книга.epub", in, e.admin, true); w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	have, err := os.ReadFile(filepath.Join(e.root, "Книга.epub"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := readZip(t, have); got["a.xhtml"] != "текст" || got["stamp-site.com"] != "" {
		t.Errorf("%v", got)
	}
	w := e.post("Книга.epub", in, e.admin, true)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"duplicate":true`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := e.names(); len(got) != 1 {
		t.Errorf("files %v", got)
	}
}

func TestKindleShrinksThePicturesOfALargeBookOnlyInTheMail(t *testing.T) {
	e := newKindleEnv(t)
	sent := stubKindle(t, nil)
	e.configureMail()
	c := e.kindleProfile("")
	img := image.NewRGBA(image.Rect(0, 0, 1800, 2400))
	rand.New(rand.NewSource(1)).Read(img.Pix)
	var pic bytes.Buffer
	if err := jpeg.Encode(&pic, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	in := buildZip(t, zipEntry{"mimetype", "application/epub+zip", true}, zipEntry{"a.xhtml", "текст", false}, zipEntry{"p.jpg", pic.String(), true})
	old := kindleMailTarget
	kindleMailTarget = len(in) / 2
	t.Cleanup(func() { kindleMailTarget = old })
	id := e.bookFile("С картинками", "a.epub", in)
	if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	m := sent()[0]
	if len(m.data) > kindleMailTarget || m.msg.Size != int64(len(m.data)) {
		t.Errorf("sent %d bytes (size %d), target %d", len(m.data), m.msg.Size, kindleMailTarget)
	}
	got, _ := readZip(t, m.data)
	if got["a.xhtml"] != "текст" || got["p.jpg"] == "" {
		t.Error("the book changed")
	}
	if !bytes.Equal(e.readMedia("a.epub"), in) {
		t.Error("the library's file changed")
	}
}
