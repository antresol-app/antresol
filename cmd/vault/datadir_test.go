package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecureDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"vault.db", "vault.db-wal"} { // no -shm: it may not exist yet
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	secureDataDir(dir, "vault.db")
	for p, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "vault.db"): 0o600, filepath.Join(dir, "vault.db-wal"): 0o600} {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %o", filepath.Base(p), st.Mode().Perm(), err, want)
		}
	}
	secureDataDir(filepath.Join(dir, "gone"), "vault.db") // a missing folder is not an error
}
