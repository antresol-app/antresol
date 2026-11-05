package comics

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// A folder volume swapped for a symlink after Open (the folder or a
// parent) keeps serving the folder that was opened, never the target.
func TestFolderVolumeSwapAfterOpen(t *testing.T) {
	base := t.TempDir()
	vol := filepath.Join(base, "Том 1")
	other := filepath.Join(base, "secret")
	for _, d := range []string{vol, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(vol, "01.jpg"), []byte("page"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "01.jpg"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(vol, "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := os.Rename(vol, filepath.Join(base, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, vol); err != nil {
		t.Fatal(err)
	}
	rc, _, err := a.Page(0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "page" {
		t.Fatalf("served %q through the swapped folder", b)
	}
}
