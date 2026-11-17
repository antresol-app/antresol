package db

import (
	"path/filepath"
	"slices"
	"testing"
)

// foreignVersions are numbers a database may carry from migrations this
// build does not have (the sources build's folder and packs): the core
// build opens such a database, applies its own pending files around them
// and leaves the other tables alone.
var foreignVersions = []int{5, 6, 8, 14, 16, 22, 27, 28, 29, 30, 38, 40, 41, 42, 10001, 10002}

// coreTables lists the tables of a database (sqlite internals left out).
func coreTables(t *testing.T, file string) []string {
	t.Helper()
	raw, err := openRaw(file)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	rows, err := raw.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestMigrateToleratesForeignVersions(t *testing.T) {
	all := migrationDirs
	migrationDirs = []migrationDir{{migrationsFS, "migrations"}} // the core build's
	t.Cleanup(func() { migrationDirs = all })

	fresh := filepath.Join(t.TempDir(), "fresh.db")
	d, err := Open(fresh)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	want := coreTables(t, fresh)

	p := filepath.Join(t.TempDir(), "v.db")
	raw, err := openRaw(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`,
		`CREATE TABLE foreign_kept (x TEXT)`,
		`INSERT INTO foreign_kept (x) VALUES ('kept')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range foreignVersions {
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, v); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	d, err = Open(p)
	if err != nil {
		t.Fatalf("open with foreign versions: %v", err)
	}
	var x string
	if err := d.QueryRow(`SELECT x FROM foreign_kept`).Scan(&x); err != nil || x != "kept" {
		t.Fatalf("foreign row: %q %v", x, err)
	}
	for _, v := range foreignVersions {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, v).Scan(&n); err != nil || n != 1 {
			t.Fatalf("version %d: %d %v", v, n, err)
		}
	}
	d.Close()
	got := coreTables(t, p)
	for _, name := range append(want, "addon_file_refs", "addon_holds") {
		if !slices.Contains(got, name) {
			t.Errorf("table %s missing", name)
		}
	}
}
