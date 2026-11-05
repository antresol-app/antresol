package library

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tEntry struct {
	name, body string
	stored     bool
}

func tZip(t *testing.T, entries ...tEntry) []byte {
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

func tUnzip(t *testing.T, data []byte) (map[string]string, []string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	var order []string
	for _, f := range zr.File {
		rc, _ := f.Open()
		x, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(x)
		order = append(order, f.Name)
	}
	return out, order
}

const tOPF = `<package><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Чистая книга</dc:title></metadata><manifest>
<item id="a" href="a.xhtml" media-type="application/xhtml+xml"/>
<item href="js/app.js" id="s" media-type="text/javascript"/>
<item
  id="p" href="pic.png" media-type="image/png"/>
</manifest></package>`

// dirtyEPUB is shaped like a site-stamped one: mimetype not first, a .com entry.
func dirtyEPUB(t *testing.T) []byte {
	return tZip(t,
		tEntry{"META-INF/container.xml", `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/c.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`, false},
		tEntry{"stamp-site.com", "stamp-site.com\n", true},
		tEntry{"mimetype", "application/epub+zip", true},
		tEntry{"OEBPS/c.opf", tOPF, false},
		tEntry{"OEBPS/a.xhtml", `<a href="https://stamp-site.com">сайт</a>`, false},
		tEntry{"OEBPS/pic.png", "PNG", false},
		tEntry{"OEBPS/js/app.js", "alert(1)", false},
		tEntry{"OEBPS/APP.EXE", "MZ", false},
	)
}

func TestCleanEPUBRemovesWhatMailRefuses(t *testing.T) {
	got, order := tUnzip(t, CleanEPUB(dirtyEPUB(t)))
	for _, gone := range []string{"stamp-site.com", "OEBPS/js/app.js", "OEBPS/APP.EXE"} {
		if _, ok := got[gone]; ok {
			t.Errorf("%s stayed", gone)
		}
	}
	if order[0] != "mimetype" {
		t.Errorf("order %v", order)
	}
	if !strings.Contains(got["OEBPS/a.xhtml"], "https://stamp-site.com") || got["OEBPS/pic.png"] != "PNG" {
		t.Error("the book's own content changed")
	}
	opf := got["OEBPS/c.opf"]
	if strings.Contains(opf, "app.js") || !strings.Contains(opf, "a.xhtml") || !strings.Contains(opf, "pic.png") {
		t.Errorf("manifest:\n%s", opf)
	}
}

func TestCleanEPUBLeavesOthersAlone(t *testing.T) {
	clean := tZip(t, tEntry{"mimetype", "application/epub+zip", true}, tEntry{"OEBPS/a.xhtml", "x", false})
	if !bytes.Equal(CleanEPUB(clean), clean) {
		t.Error("a clean book was rewritten")
	}
	if got := CleanEPUB([]byte("not a zip")); string(got) != "not a zip" {
		t.Error("garbage changed")
	}
	p := filepath.Join(t.TempDir(), "a.epub")
	os.WriteFile(p, []byte("not a zip"), 0o644)
	if ch, err := CleanEPUBFile(p); ch || err != nil {
		t.Errorf("%v %v", ch, err)
	}
}

func TestMailRefusedNames(t *testing.T) {
	for name, want := range map[string]bool{
		"stamp-site.com": true, "a/b/X.EXE": true, "OEBPS/s.js": true, "x.jar": true,
		"a.xhtml": false, "com": false, "fonts/a.otf": false, "pic.png": false, "OEBPS/com/a.xhtml": false,
	} {
		if mailRefusedName(name) != want {
			t.Errorf("%s: want %v", name, want)
		}
	}
}

func TestCleanEPUBFileKeepsAHardLinkIntact(t *testing.T) {
	dir := t.TempDir()
	lib, seed := filepath.Join(dir, "lib.epub"), filepath.Join(dir, "seed.epub")
	orig := dirtyEPUB(t)
	os.WriteFile(seed, orig, 0o640)
	if err := os.Link(seed, lib); err != nil {
		t.Skip(err)
	}
	if ch, err := CleanEPUBFile(lib); !ch || err != nil {
		t.Fatalf("%v %v", ch, err)
	}
	if have, _ := os.ReadFile(seed); !bytes.Equal(have, orig) {
		t.Error("the seeding copy changed")
	}
	st, _ := os.Stat(lib)
	if st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
	have, _ := os.ReadFile(lib)
	if got, _ := tUnzip(t, have); got["stamp-site.com"] != "" {
		t.Error("not cleaned")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 2 {
		t.Errorf("left behind: %v", ents)
	}
}

func TestScanCleansANewBookOnce(t *testing.T) {
	s, root := bookScanner(t)
	ctx := context.Background()
	p := filepath.Join(root, "Книга.epub")
	os.WriteFile(p, dirtyEPUB(t), 0o644)
	if err := s.ScanBooks(ctx); err != nil {
		t.Fatal(err)
	}
	have, _ := os.ReadFile(p)
	got, _ := tUnzip(t, have)
	if _, ok := got["stamp-site.com"]; ok {
		t.Fatal("not cleaned")
	}
	st, _ := os.Stat(p)
	var size, mtime int64
	if err := s.Repo.db.QueryRowContext(ctx, `SELECT size, mtime FROM files WHERE path=?`, p).Scan(&size, &mtime); err != nil {
		t.Fatal(err)
	}
	if size != st.Size() || mtime != st.ModTime().Unix() {
		t.Errorf("stored %d/%d, on disk %d/%d", size, mtime, st.Size(), st.ModTime().Unix())
	}
	// the next scan finds the book unchanged and reads nothing
	before := bookReads.Load()
	if err := s.ScanBooks(ctx); err != nil {
		t.Fatal(err)
	}
	if bookReads.Load() != before {
		t.Error("the clean book was read again")
	}
}

func TestScanLeavesABookAlreadyInTheLibrary(t *testing.T) {
	s, root := bookScanner(t)
	ctx := context.Background()
	p := filepath.Join(root, "Книга.epub")
	orig := tZip(t, tEntry{"mimetype", "application/epub+zip", true}, tEntry{"a.xhtml", "x", false})
	os.WriteFile(p, orig, 0o644)
	if err := s.ScanBooks(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if _, err := s.Repo.db.ExecContext(ctx, `UPDATE files SET size=?, mtime=? WHERE path=?`, st.Size(), st.ModTime().Unix(), p); err != nil {
		t.Fatal(err)
	}
	if err := s.ScanBooks(ctx); err != nil {
		t.Fatal(err)
	}
	if have, _ := os.ReadFile(p); !bytes.Equal(have, orig) {
		t.Error("changed")
	}
}

func TestScanKeepsABookItCannotClean(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	s, root := bookScanner(t)
	dir := filepath.Join(root, "ro")
	os.Mkdir(dir, 0o755)
	p := filepath.Join(dir, "Книга.epub")
	orig := dirtyEPUB(t)
	os.WriteFile(p, orig, 0o644)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if err := s.ScanBooks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if have, _ := os.ReadFile(p); !bytes.Equal(have, orig) {
		t.Error("changed")
	}
	var n int
	s.Repo.db.QueryRow(`SELECT count(*) FROM files WHERE path=?`, p).Scan(&n)
	if n != 1 {
		t.Error("the book is not in the library")
	}
}
