package mediaimport

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// onlyLine returns the only line of rep.
func onlyLine(t *testing.T, rep *Report) Line {
	t.Helper()
	if len(rep.Lines) != 1 {
		t.Fatalf("lines %+v", rep.Lines)
	}
	return rep.Lines[0]
}

// treeHash is a SHA-256 over the names, sizes and contents of every file
// under dir.
func treeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		h.Write([]byte(rel + "\x00"))
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func fixtureDB(t *testing.T, name string, stmts []string, inserts func(*sql.DB)) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	d, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	inserts(d)
	return path
}

func mustExec(t *testing.T, d *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
