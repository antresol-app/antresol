package mediaimport

import (
	"bytes"
	"context"
	"errors"
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
	d, err := snapshotDB(tmp, path)
	if errors.Is(err, errNoSourceDB) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer d.close()
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
