package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// planOf is the EXPLAIN QUERY PLAN of q, its details joined.
func planOf(t *testing.T, d *sql.DB, q string, args ...any) string {
	t.Helper()
	rows, err := d.Query(`EXPLAIN QUERY PLAN `+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	return strings.Join(out, "; ")
}

// withLibraryStats opens a database whose planner statistics are those of
// a 37 700-item library (the perf harness's, after PRAGMA optimize).
func withLibraryStats(t *testing.T) *sql.DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1) // the reloaded statistics are per connection
	for _, q := range []string{
		`ANALYZE sqlite_schema`,
		`DELETE FROM sqlite_stat1`,
		`INSERT INTO sqlite_stat1(tbl, idx, stat) VALUES
		  ('items','items_kind_sort','37700 667 2'), ('items','items_tmdb','37700 667 667'),
		  ('items','items_parent','37700 17'), ('items','items_kind_added','37700 667 56'),
		  ('items','items_match','37700 667 1'), ('items','items_missing','0 0'),
		  ('tracks', NULL, '24000')`,
		`ANALYZE sqlite_schema`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return d
}

// Top-level lookups (parent_id IS NULL: thousands of rows) do not walk
// items_parent; lookups by parent still use it.
func TestItemsParentIndexPartial(t *testing.T) {
	d := withLibraryStats(t)
	top := planOf(t, d, `SELECT tmdb_id, MIN(id) FROM items WHERE kind=? AND tmdb_id IN (?,?,?)
	  AND parent_id IS NULL AND missing_since IS NULL GROUP BY tmdb_id`, "movie", 1, 2, 3)
	if strings.Contains(top, "items_parent") {
		t.Errorf("top-level lookup: %s", top)
	}
	for _, q := range []string{
		`SELECT id FROM items WHERE parent_id=?`,
		`SELECT id FROM items WHERE parent_id IN (?,?)`,
	} {
		if p := planOf(t, d, q, 1, 2)[:]; !strings.Contains(p, "items_parent") {
			t.Errorf("%s: %s", q, p)
		}
	}
}
