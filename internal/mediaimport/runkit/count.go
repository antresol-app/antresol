package runkit

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"

	"github.com/antresol-app/antresol/internal/sqlitesnap"
)

// TableCount counts the rows of Table matching Where ("" = all).
type TableCount struct{ Table, Where string }

// CountIn sums the counts over a snapshot of the database at path; a table
// that does not exist counts 0, and so does a missing or empty file.
func CountIn(ctx context.Context, tmp, path string, counts ...TableCount) (int, error) {
	if Empty(path) {
		return 0, nil
	}
	d, err := SnapshotDB(tmp, path)
	if errors.Is(err, ErrNoSourceDB) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer d.Close()
	total := 0
	for _, c := range counts {
		ok, err := sqlitesnap.HasTable(ctx, d.DB, c.Table)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		q := `SELECT count(*) FROM "` + c.Table + `"`
		if c.Where != "" {
			q += " WHERE " + c.Where
		}
		var n int
		if err := d.QueryRowContext(ctx, q).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// Empty: no file, or one with nothing but blanks.
func Empty(path string) bool {
	if path == "" {
		return true
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return true
	}
	if st.Size() > 64 {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && len(bytes.TrimSpace(b)) == 0
}

// SrcDB is a read-only snapshot of another service's database (the
// importers of mediaimport read theirs through it too).
type SrcDB struct {
	*sql.DB
	files []string
	tmp   string // own temporary folder, removed on close
}

// Close closes the database and removes the snapshot's files.
func (d *SrcDB) Close() {
	d.DB.Close()
	for _, f := range d.files {
		os.Remove(f)
	}
	if d.tmp != "" {
		os.RemoveAll(d.tmp)
	}
}

// ErrNoSourceDB: SnapshotDB found no database file at the path.
var ErrNoSourceDB = errors.New("migrate: no database")

// SnapshotDB copies path into tmp (or a temporary folder of its own) and
// opens the copy read-only. A missing file is ErrNoSourceDB.
func SnapshotDB(tmp, path string) (*SrcDB, error) {
	if path == "" {
		return nil, ErrNoSourceDB
	}
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || err == nil && st.IsDir() {
		return nil, ErrNoSourceDB
	}
	if err != nil { // permission denied and the like: there, but unreadable
		return nil, err
	}
	d := &SrcDB{}
	if tmp == "" {
		t, err := os.MkdirTemp("", "vault-migrate-")
		if err != nil {
			return nil, err
		}
		tmp, d.tmp = t, t
	} else if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, err
	}
	snap, err := sqlitesnap.Snapshot(path, tmp)
	if err == nil {
		d.files = []string{snap, snap + "-wal", snap + "-shm"}
		d.DB, err = sqlitesnap.OpenRO(snap)
	}
	if err != nil {
		for _, f := range d.files {
			os.Remove(f)
		}
		if d.tmp != "" {
			os.RemoveAll(d.tmp)
		}
		return nil, err
	}
	return d, nil
}
