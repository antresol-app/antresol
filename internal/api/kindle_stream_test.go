package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/mailer"
)

// bigBook writes an EPUB of about blobMB megabytes of data that does not
// compress, with a file mail servers refuse in it, straight to the library
// folder (never held in the test's memory either), and adds the book.
func (e *libEnv) bigBook(title, name string, blobMB int) int64 {
	e.t.Helper()
	p := filepath.Join(e.media, name)
	f, err := os.Create(p)
	if err != nil {
		e.t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	add := func(name string, m uint16, write func(io.Writer)) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: m})
		if err != nil {
			e.t.Fatal(err)
		}
		write(w)
	}
	add("mimetype", zip.Store, func(w io.Writer) { io.WriteString(w, "application/epub+zip") })
	add("stamp-site.exe", zip.Store, func(w io.Writer) { io.WriteString(w, "MZ") })
	add("OEBPS/a.xhtml", zip.Deflate, func(w io.Writer) { io.WriteString(w, "текст") })
	add("OEBPS/blob.bin", zip.Store, func(w io.Writer) {
		src := rand.NewChaCha8([32]byte{1})
		if _, err := io.CopyN(w, src, int64(blobMB)<<20); err != nil {
			e.t.Fatal(err)
		}
	})
	if err := zw.Close(); err != nil {
		e.t.Fatal(err)
	}
	st, _ := f.Stat()
	f.Close()
	id, err := e.repo.UpsertBook(context.Background(), library.BookInput{Title: title, Files: []library.FileInput{{Path: p, Size: st.Size()}}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// sinkKindle takes the mail's attachment to a file in dir at once (the stub
// of stubKindle holds it in memory) and tells where.
func sinkKindle(t *testing.T, dir string, err error) (path func() string) {
	t.Helper()
	dst := filepath.Join(dir, "mailed")
	old := kindleSend
	kindleSend = func(_ context.Context, _ mailer.Config, m mailer.Message) error {
		f, cerr := os.Create(dst)
		if cerr != nil {
			return cerr
		}
		defer f.Close()
		n, cerr := io.Copy(f, m.Attachment)
		if cerr == nil && n != m.Size {
			cerr = io.ErrShortWrite
		}
		if cerr != nil {
			return cerr
		}
		return err
	}
	t.Cleanup(func() { kindleSend = old })
	return func() string { return dst }
}

// spoolDir makes the temp files of this test countable.
func spoolDir(t *testing.T) func() []string {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	return func() []string {
		ents, _ := os.ReadDir(dir)
		var out []string
		for _, e := range ents {
			out = append(out, e.Name())
		}
		return out
	}
}

// The mail of a large book is built on disk: the heap does not grow with the
// book. A 40 MB book with a refused file in it is cleaned and tried for
// shrinking (it has no picture to shrink, so it goes as it is), which used to
// hold it three or four times over.
func TestKindleSendOfALargeBookDoesNotHoldItInMemory(t *testing.T) {
	e := newKindleEnv(t)
	mailed := sinkKindle(t, t.TempDir(), nil)
	left := spoolDir(t)
	e.configureMail()
	c := e.kindleProfile("")
	id := e.bigBook("Толстая", "big.epub", 40)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if w := e.do("POST", "/api/books/"+itoa(id)+"/kindle", "", c); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 12<<20 {
		t.Errorf("sending a 40 MB book allocated %d MB", got>>20)
	}
	zr, err := zip.OpenReader(mailed())
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Name == "OEBPS/blob.bin" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			want := sha256.New()
			io.CopyN(want, rand.NewChaCha8([32]byte{1}), 40<<20)
			got := sha256.New()
			if _, err := io.Copy(got, rc); err != nil || !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
				t.Errorf("the book's data changed: %v", err)
			}
			rc.Close()
		}
	}
	if len(names) != 3 || names[0] != "mimetype" {
		t.Errorf("entries %v: want the book without the refused file, mimetype first", names)
	}
	if got := left(); len(got) != 0 {
		t.Errorf("temp files left: %v", got)
	}
}

// Whatever ends a send, no copy of the book stays on disk.
func TestKindleSendLeavesNoTempFiles(t *testing.T) {
	e := newKindleEnv(t)
	out := t.TempDir()
	left := spoolDir(t)
	e.configureMail()
	c := e.kindleProfile("")
	fb2 := e.bookFile("Название", "a.fb2", []byte(testFB2))
	dirty := e.bigBook("Грязная", "dirty.epub", 1)
	huge := e.bigBook("Огромная", "huge.epub", 51)
	for _, tc := range []struct {
		name string
		id   int64
		err  error
		code int
	}{
		{"fb2 sent", fb2, nil, 204},
		{"fb2 refused by the server", fb2, mailer.ErrAuth, 502},
		{"epub sent", dirty, nil, 204},
		{"epub refused by the server", dirty, mailer.ErrTooLarge, 413},
		{"epub over what Kindle takes", huge, nil, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sinkKindle(t, out, tc.err)
			if w := e.do("POST", "/api/books/"+itoa(tc.id)+"/kindle", "", c); w.Code != tc.code {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if got := left(); len(got) != 0 {
				t.Errorf("temp files left: %v", got)
			}
		})
	}
}
