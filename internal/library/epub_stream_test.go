package library

import (
	"archive/zip"
	"bytes"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// shrinkBytes is ShrinkEPUB of an in-memory book, its result read back (the
// book itself when nothing got smaller).
func shrinkBytes(t *testing.T, in []byte, target int) ([]byte, bool) {
	t.Helper()
	f, ok, err := ShrinkEPUB(bytes.NewReader(in), int64(len(in)), int64(target), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return readTemp(t, f, in), ok
}

// cleanBytes is CleanEPUB of an in-memory book, its result read back (the
// book itself when it stays as it is).
func cleanBytes(t *testing.T, in []byte) []byte {
	t.Helper()
	f, err := CleanEPUB(bytes.NewReader(in), int64(len(in)), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return readTemp(t, f, in)
}

func readTemp(t *testing.T, f *os.File, unchanged []byte) []byte {
	t.Helper()
	if f == nil {
		return unchanged
	}
	defer os.Remove(f.Name())
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func dirEntries(t *testing.T, dir string) int {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

// A stage that changes nothing leaves nothing behind; one that does leaves
// its one file, for the caller to remove.
func TestStagesLeaveOnlyTheirResult(t *testing.T) {
	clean := tZip(t, tEntry{"mimetype", "application/epub+zip", true}, tEntry{"OEBPS/a.xhtml", "x", false})
	dirty := tZip(t, tEntry{"mimetype", "application/epub+zip", true}, tEntry{"stamp.exe", "x", true})
	pics := imageBook(t)
	for _, tc := range []struct {
		name string
		run  func(dir string) (*os.File, error)
		want int
	}{
		{"clean book", func(d string) (*os.File, error) { return CleanEPUB(bytes.NewReader(clean), int64(len(clean)), d) }, 0},
		{"not a zip", func(d string) (*os.File, error) { return CleanEPUB(strings.NewReader("not a zip"), 9, d) }, 0},
		{"dirty book", func(d string) (*os.File, error) { return CleanEPUB(bytes.NewReader(dirty), int64(len(dirty)), d) }, 1},
		{"fits", func(d string) (*os.File, error) {
			f, _, err := ShrinkEPUB(bytes.NewReader(pics), int64(len(pics)), int64(len(pics)), d)
			return f, err
		}, 0},
		{"no picture to shrink", func(d string) (*os.File, error) {
			f, ok, err := ShrinkEPUB(bytes.NewReader(clean), int64(len(clean)), 10, d)
			if ok {
				t.Error("a book without pictures reached 10 bytes")
			}
			return f, err
		}, 0},
		{"shrunk", func(d string) (*os.File, error) {
			f, ok, err := ShrinkEPUB(bytes.NewReader(pics), int64(len(pics)), int64(len(pics)/3), d)
			if !ok {
				t.Error("did not fit")
			}
			return f, err
		}, 1},
		{"shrunk as far as it goes", func(d string) (*os.File, error) {
			f, ok, err := ShrinkEPUB(bytes.NewReader(pics), int64(len(pics)), 100, d)
			if ok {
				t.Error("fits in 100 bytes")
			}
			return f, err
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			f, err := tc.run(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := dirEntries(t, dir); got != tc.want {
				t.Errorf("%d files left, want %d", got, tc.want)
			}
			if f == nil {
				return
			}
			if pos, _ := f.Seek(0, io.SeekCurrent); pos != 0 {
				t.Errorf("the result is not rewound: at %d", pos)
			}
			discardTemp(f)
			if got := dirEntries(t, dir); got != 0 {
				t.Errorf("%d files left after the caller let go", got)
			}
		})
	}
}

// Cleaning a large book takes no memory by its size.
func TestCleanEPUBDoesNotHoldTheBook(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, e := range []struct {
		name string
		n    int64
	}{{"mimetype", 0}, {"stamp.exe", 2}, {"OEBPS/blob.bin", 48 << 20}} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if e.n == 0 {
			io.WriteString(w, "application/epub+zip")
		} else if _, err := io.CopyN(w, rand.NewChaCha8([32]byte{1}), e.n); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	st, _ := f.Stat()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	out, err := CleanEPUB(f, st.Size(), t.TempDir())
	runtime.ReadMemStats(&after)
	f.Close()
	if err != nil || out == nil {
		t.Fatalf("%v %v", out, err)
	}
	defer discardTemp(out)
	if got := after.TotalAlloc - before.TotalAlloc; got > 8<<20 {
		t.Errorf("cleaning a %d MB book allocated %d MB", st.Size()>>20, got>>20)
	}
}
