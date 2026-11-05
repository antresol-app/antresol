package subtitles

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeBytes(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "v.mkv")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHashZeroFile(t *testing.T) {
	h, err := Hash(writeBytes(t, make([]byte, 131072)))
	if err != nil || h != "0000000000020000" {
		t.Fatalf("%q %v", h, err)
	}
}

func TestHashKnownWords(t *testing.T) {
	b := make([]byte, 131072)
	binary.LittleEndian.PutUint64(b[0:], 1)
	binary.LittleEndian.PutUint64(b[len(b)-8:], 2)
	h, err := Hash(writeBytes(t, b))
	if err != nil || h != "0000000000020003" {
		t.Fatalf("%q %v", h, err)
	}
}

func TestHashOverflowWraps(t *testing.T) {
	b := make([]byte, 131072)
	binary.LittleEndian.PutUint64(b[0:], ^uint64(0)) // + size 0x20000 wraps
	h, err := Hash(writeBytes(t, b))
	if err != nil || h != "000000000001ffff" {
		t.Fatalf("%q %v", h, err)
	}
}

func TestHashTooSmall(t *testing.T) {
	if _, err := Hash(writeBytes(t, make([]byte, 131071))); !errors.Is(err, ErrTooSmall) {
		t.Fatalf("%v", err)
	}
}
