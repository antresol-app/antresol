//go:build unix

package comics

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image/color"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// sevenZipCountBomb is a 41-byte .cb7 whose header claims 2^32-1 files:
// unpatched sevenzip v1.6.5 allocates make([]FileHeader, files) (~480 GB)
// before reading any of them.
func sevenZipCountBomb() []byte {
	hdr := []byte{0x01, 0x05, 0xF0, 0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00} // Header, FilesInfo, count, End, End
	start := make([]byte, 20)
	binary.LittleEndian.PutUint64(start[0:], 0)                // next header offset
	binary.LittleEndian.PutUint64(start[8:], uint64(len(hdr))) // next header size
	binary.LittleEndian.PutUint32(start[16:], crc32.ChecksumIEEE(hdr))
	out := []byte("7z\xbc\xaf\x27\x1c\x00\x04")
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(start))
	out = append(out, start...)
	return append(out, hdr...)
}

func TestSevenZipHeaderCountBomb(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bomb.cb7")
	if err := os.WriteFile(p, sevenZipCountBomb(), 0o644); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	if _, _, _, _, err := Peek(p); err == nil {
		t.Fatal("peek must fail")
	}
	if _, err := Open(p, filepath.Join(dir, "cache")); err == nil {
		t.Fatal("open must fail")
	}
	runtime.ReadMemStats(&after)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
	if grew := int64(after.Sys) - int64(before.Sys); grew > 512<<20 {
		t.Fatalf("memory reserved: %d MB", grew>>20)
	}
}

// Symlinks and other non-regular files are never pages or volumes: a link
// to a file outside the library must not be served.
func TestSymlinksAndFIFOsRefused(t *testing.T) {
	dir := t.TempDir()
	vol := filepath.Join(dir, "Том 1")
	if err := os.MkdirAll(vol, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("password"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := testJPEG(t, color.White)
	if err := os.WriteFile(filepath.Join(vol, "02.jpg"), j, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(vol, "01.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(vol, "03.jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(vol, "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if got := a.Pages(); !reflect.DeepEqual(got, []string{"02.jpg"}) {
		t.Fatalf("pages %q", got)
	}
	pages, _, _, cover, err := Peek(vol)
	if err != nil || !reflect.DeepEqual(pages, []string{"02.jpg"}) || !bytes.Equal(cover, j) {
		t.Fatalf("peek %q %v", pages, err)
	}
	// a page swapped for a symlink after listing is refused
	if err := os.Remove(filepath.Join(vol, "02.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(vol, "02.jpg")); err != nil {
		t.Fatal(err)
	}
	if rc, _, err := a.Page(0); err == nil {
		b, _ := io.ReadAll(rc)
		rc.Close()
		t.Fatalf("served through a symlink: %q", b)
	}
	// a symlinked archive and a FIFO archive are refused, without blocking
	link := filepath.Join(dir, "link.cbz")
	if err := os.Symlink("../../etc/hosts", link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo.cbr")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, p := range []string{link, fifo} {
			if _, _, _, _, err := Peek(p); !errors.Is(err, errNotRegular) {
				t.Errorf("peek %s: %v", p, err)
			}
			if _, err := Open(p, filepath.Join(dir, "cache")); !errors.Is(err, errNotRegular) {
				t.Errorf("open %s: %v", p, err)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked on a FIFO")
	}
}

// Peek decodes at most maxPeekBytes for its two reads; past that the pages
// are still listed and the error says why there is no cover.
func TestPeekBudget(t *testing.T) {
	old := maxPeekBytes
	maxPeekBytes = 100 // a fixture page is 600 bytes
	t.Cleanup(func() { maxPeekBytes = old })
	for _, f := range []string{"testdata/rar5.cbr", "testdata/three.cb7"} {
		pages, _, _, cover, err := Peek(f)
		if !errors.Is(err, errPeekBudget) || len(pages) != 3 || cover != nil {
			t.Fatalf("%s: %q %d %v", f, pages, len(cover), err)
		}
	}
	// a solid 7z: the third file costs what precedes it in the stream
	entries, closeFn, err := listArchive("testdata/three.cb7", format7z)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if entries[2].name != "3.jpg" || entries[2].cost != 1800 {
		t.Fatalf("%+v", entries[2])
	}
	// a zip read in reverse order still reads the first page once, by name
	maxPeekBytes = 700
	dir := t.TempDir()
	p := filepath.Join(dir, "rev.cbz")
	var es []zipEntry
	for _, n := range []string{"9.jpg", "8.jpg", "7.jpg", "1.jpg"} {
		es = append(es, zipEntry{name: n, data: testJPEG(t, color.White)})
	}
	writeZip(t, p, es)
	if _, _, _, cover, err := Peek(p); err != nil || cover == nil {
		t.Fatalf("reverse zip: %v", err)
	}
}

// The 7z zstd coder refuses a window over maxDictBytes.
func TestZstdWindowLimit(t *testing.T) {
	data := make([]byte, 4<<20)
	rand.New(rand.NewSource(1)).Read(data[:1<<20])
	copy(data[1<<20:], data[:1<<20]) // a match 1 MB back needs a 1 MB+ window
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf, zstd.WithWindowSize(4<<20), zstd.WithSingleSegment(false))
	if err != nil {
		t.Fatal(err)
	}
	enc.Write(data)
	enc.Close()
	read := func() error {
		rc, err := newZstdReader(nil, 0, []io.ReadCloser{io.NopCloser(bytes.NewReader(buf.Bytes()))})
		if err != nil {
			return err
		}
		defer rc.Close()
		_, err = io.Copy(io.Discard, rc)
		return err
	}
	if err := read(); err != nil {
		t.Fatalf("within the limit: %v", err)
	}
	old := maxDictBytes
	maxDictBytes = 64 << 10
	t.Cleanup(func() { maxDictBytes = old })
	if err := read(); err == nil {
		t.Fatal("a 4 MB window passed a 64 KB limit")
	}
}
