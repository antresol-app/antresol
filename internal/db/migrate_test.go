package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// TestMigrateRefusesDuplicateVersion: two files of one version, across
// folders or in one, are an error at open and nothing is applied (before,
// the second was silently skipped as already applied).
func TestMigrateRefusesDuplicateVersion(t *testing.T) {
	sql := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	for name, dirs := range map[string][]migrationDir{
		"across folders": {
			{fstest.MapFS{"a/0001_one.sql": sql(`CREATE TABLE one (x INTEGER);`), "a/0073_core.sql": sql(`CREATE TABLE core (x INTEGER);`)}, "a"},
			{fstest.MapFS{"b/0073_src.sql": sql(`CREATE TABLE src (x INTEGER);`)}, "b"},
		},
		"in one folder": {
			{fstest.MapFS{"a/0001_one.sql": sql(`CREATE TABLE one (x INTEGER);`), "a/0073_core.sql": sql(`CREATE TABLE core (x INTEGER);`),
				"a/73_src.sql": sql(`CREATE TABLE src (x INTEGER);`)}, "a"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "v.db")
			all := migrationDirs
			migrationDirs = dirs
			d, err := Open(p)
			migrationDirs = all
			if err == nil {
				d.Close()
				t.Fatal("opened with a duplicate version")
			}
			if !strings.Contains(err.Error(), "73") {
				t.Fatalf("error does not name the version: %v", err)
			}
			raw, err := openRaw(p)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			var n int
			if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("tables after the refusal: %d %v", n, err)
			}
		})
	}
}

// TestMigrateOrdersByVersion: versions apply in numeric order whatever
// their width (a pack's 10001 after core's 9999).
func TestMigrateOrdersByVersion(t *testing.T) {
	sql := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	all := migrationDirs
	migrationDirs = []migrationDir{
		{fstest.MapFS{"a/9999_base.sql": sql(`CREATE TABLE base (x INTEGER);`)}, "a"},
		{fstest.MapFS{"b/10001_pack.sql": sql(`ALTER TABLE base ADD COLUMN y INTEGER;`)}, "b"},
	}
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	migrationDirs = all
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
}

// sealedCoreVersions are the core migrations up to 72. The numbers missing
// below 73 belong to the sources build (sealed there), so a new core file
// takes a number above 72 and below 10000 (packs start at 10001).
var sealedCoreVersions = []int{1, 2, 3, 4, 7, 9, 10, 11, 12, 13, 15, 17, 18, 19, 20, 21, 23, 24, 25, 26, 31,
	32, 33, 34, 35, 36, 37, 39, 43, 44, 45, 46, 47, 48, 50, 52, 54, 56, 60, 62, 64, 66, 67, 68, 69, 70, 71, 72}

func TestCoreMigrationNumbersInRange(t *testing.T) {
	sealed := map[int]bool{}
	for _, v := range sealedCoreVersions {
		sealed[v] = true
	}
	got := migrationVersions(t, "migrations")
	for v, name := range got {
		if v <= 72 && !sealed[v] || v > 72 && v >= 10000 {
			t.Errorf("%s: a new core migration takes a number above 72 and below 10000", name)
		}
	}
	for _, v := range sealedCoreVersions {
		if _, ok := got[v]; !ok {
			t.Errorf("core migration %04d is gone (applied numbers are never reused)", v)
		}
	}
}

// migrationVersions reads dir's .sql files: version -> file name.
func migrationVersions(t *testing.T, dir string) map[int]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]string{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		v, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		out[v] = e.Name()
	}
	return out
}

// openRaw opens the file without migrating it.
func openRaw(p string) (*sql.DB, error) { return sql.Open("sqlite", "file:"+p) }
