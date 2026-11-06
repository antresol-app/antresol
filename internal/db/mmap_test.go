package db

import (
	"path/filepath"
	"testing"
)

// mmap_size is 0 (off) unless VAULT_DB_MMAP_BYTES asks for it.
func TestMmapSize(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int64
	}{{"", 0}, {"0", 0}, {"67108864", 64 << 20}, {"junk", 0}, {"-5", 0}} {
		t.Setenv("VAULT_DB_MMAP_BYTES", c.env)
		d, err := Open(filepath.Join(t.TempDir(), "v.db"))
		if err != nil {
			t.Fatal(err)
		}
		var got int64
		if err := d.QueryRow(`PRAGMA mmap_size`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		d.Close()
		if got != c.want {
			t.Errorf("VAULT_DB_MMAP_BYTES=%q: mmap_size %d, want %d", c.env, got, c.want)
		}
	}
}
