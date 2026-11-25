package library

import (
	"path/filepath"
	"strings"
	"testing"
)

// An .nfo is read up to a limit: a huge file beside a title is not loaded.
func TestReadNFOIgnoresAHugeFile(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "a.nfo")
	touch(t, small, `<movie><title>Брат</title></movie>`)
	if n, ok := readNFO(small); !ok || n.Title != "Брат" {
		t.Fatalf("%+v %v", n, ok)
	}
	huge := filepath.Join(dir, "b.nfo")
	touch(t, huge, `<movie><title>Брат</title><plot>`+strings.Repeat("x", nfoLimit)+`</plot></movie>`)
	if _, ok := readNFO(huge); ok {
		t.Fatal("read a file over the limit")
	}
	if _, ok := readNFO(filepath.Join(dir, "none.nfo")); ok {
		t.Fatal("read a missing file")
	}
}
