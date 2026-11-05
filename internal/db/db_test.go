package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOpenMigratesIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v.db")
	for i := 0; i < 2; i++ {
		d, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		var files []string
		for _, md := range migrationDirs {
			f, _ := filepath.Glob(md.dir + "/*.sql")
			files = append(files, f...)
		}
		if n != len(files) || n < 3 {
			t.Fatalf("run %d: %d migrations applied, %d files", i, n, len(files))
		}
		if _, err := d.Exec(`INSERT OR REPLACE INTO settings(key,value) VALUES('a','b')`); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		d.Close()
	}
}

func TestPragmas(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var fk int
	var jm string
	if err := d.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys=%d err=%v", fk, err)
	}
	if err := d.QueryRow(`PRAGMA journal_mode`).Scan(&jm); err != nil || jm != "wal" {
		t.Fatalf("journal_mode=%q err=%v", jm, err)
	}
}

// synchronous=NORMAL on every pooled connection: with WAL's default FULL a
// commit fsyncs the -wal while holding the write lock, and on a busy media
// disk that fsync took seconds (SQLITE_BUSY for every other writer).
func TestSynchronousNormalOnEveryConnection(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	var conns []*sql.Conn
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < 4; i++ { // distinct connections: held open together
		c, err := d.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		var sync int
		if err := c.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&sync); err != nil || sync != 1 {
			t.Fatalf("conn %d: synchronous=%d err=%v, want 1 (NORMAL)", i, sync, err)
		}
	}
}

func TestQueryWhileIterating(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, k := range []string{"a", "b", "c"} {
		if _, err := d.Exec(`INSERT INTO settings(key,value) VALUES(?, 'x')`, k); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() {
		rows, err := d.Query(`SELECT key FROM settings`)
		if err != nil {
			done <- err
			return
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				done <- err
				return
			}
			var v string
			if err := d.QueryRow(`SELECT value FROM settings WHERE key=?`, k).Scan(&v); err != nil {
				done <- err
				return
			}
		}
		// query on db while a tx is open
		tx, err := d.Begin()
		if err != nil {
			done <- err
			return
		}
		defer tx.Rollback()
		var n int
		done <- d.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&n)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock")
	}
}

func TestMigration0010(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v.db")
	for run := 0; run < 2; run++ {
		d, err := Open(p)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		for _, want := range [][2]string{{"table", "tracks"}, {"table", "chapters"}, {"index", "tracks_file_start"}, {"index", "items_match"}} {
			var n int
			if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type=? AND name=?`, want[0], want[1]).Scan(&n); err != nil || n != 1 {
				t.Fatalf("run %d: %s %s: n=%d err=%v", run, want[0], want[1], n, err)
			}
		}
		for _, col := range [][2]string{{"progress", "locator"}, {"items", "match_key"}} {
			var n int
			if err := d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, col[0], col[1]).Scan(&n); err != nil || n != 1 {
				t.Fatalf("run %d: column %s.%s: n=%d err=%v", run, col[0], col[1], n, err)
			}
		}
		d.Close()
	}
}

func TestMusicYearSanityMigration(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	body, err := migrationsFS.ReadFile("migrations/0023_music_year_sanity.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range []struct {
		kind string
		year int
	}{{"album", 1}, {"album", 1973}, {"album", 3000}, {"track", 1}, {"track", 2001}, {"movie", 1}} {
		if _, err := d.Exec(`INSERT INTO items(kind,title,sort_title,year,added_at,updated_at) VALUES(?,?,?,?,'t','t')`,
			c.kind, "x", "x", c.year); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if _, err := d.Exec(string(body)); err != nil {
		t.Fatal(err)
	}
	rows, err := d.Query(`SELECT kind, year FROM items ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []int{0, 1973, 0, 0, 2001, 1}
	for i := 0; rows.Next(); i++ {
		var k string
		var y int
		if err := rows.Scan(&k, &y); err != nil {
			t.Fatal(err)
		}
		if y != want[i] {
			t.Errorf("row %d (%s): year %d, want %d", i, k, y, want[i])
		}
	}
}

// Tables that moved from migrations_full to migrations exist in both editions.
func TestMovedTablesInBothEditions(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, table := range []string{"kid_requests", "import_jobs", "import_groups", "import_tracks", "import_playlists",
		"import_playlist_entries", "import_albums"} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s: %d %v", table, n, err)
		}
	}
}

// A version number is used by one file across migrations and
// migrations_full (the folders share one numbering).
func TestMigrationVersionsUnique(t *testing.T) {
	seen := map[int]string{}
	for _, dir := range []string{"migrations", "migrations_full"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			v, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
			if err != nil {
				t.Fatalf("%s: %v", e.Name(), err)
			}
			if prev, dup := seen[v]; dup {
				t.Errorf("version %d: %s and %s/%s", v, prev, dir, e.Name())
			}
			seen[v] = dir + "/" + e.Name()
		}
	}
}
