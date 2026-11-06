// Package db opens SQLite and applies embedded migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationDir is one embedded folder of migrations. Versions are unique
// across all folders (migrate refuses a duplicate) and applied in version
// order; a version missing from a build (the core build has no
// migrations_full) is simply applied by the build that has it, whenever
// that one opens the database. Numbering: README.md in migrations/.
type migrationDir struct {
	fs  fs.FS
	dir string
}

// migrationDirs: the library's migrations, plus the sources build's own
// (db_full.go).
var migrationDirs = []migrationDir{{migrationsFS, "migrations"}}

// Open opens (creating if needed) the database at path with WAL and foreign
// keys enabled, and applies pending migrations in order.
func Open(file string) (*sql.DB, error) { return OpenDriver("sqlite", file) }

// OpenDriver is Open through the database/sql driver registered as name:
// "sqlite" (modernc), or a wrapper of it (the perf harness counts queries).
func OpenDriver(name, file string) (*sql.DB, error) {
	// synchronous(NORMAL): in WAL mode a commit then only writes the -wal
	// (fsync at checkpoints, which run without the write lock). With the
	// default FULL every commit fsyncs while holding the write lock; on the
	// media disk under load (another client writing) that took
	// seconds, and the other writers ran out of busy_timeout (SQLITE_BUSY:
	// lost piece-completion and download writes). NORMAL never corrupts the
	// database; a power cut can roll back the last commits.
	dsn := "file:" + file + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
	if n := mmapBytes(); n > 0 {
		dsn += fmt.Sprintf("&_pragma=mmap_size(%d)", n)
	}
	d, err := sql.Open(name, dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(8) // pragmas are per-connection via DSN; _txlock=immediate + busy_timeout serialise writers
	// and all 8 stay open between bursts (database/sql keeps 2 by default):
	// a reopened connection starts with a cold page cache and reads its
	// pragmas and the schema again, on the slow disk
	d.SetMaxIdleConns(8)
	if err := migrate(d); err != nil {
		d.Close()
		return nil, err
	}
	if err := Optimize(context.Background(), d); err != nil {
		log.Printf("db: optimize: %v", err) // only slower queries
	}
	return d, nil
}

// DefaultMmapBytes: no mmap unless VAULT_DB_MMAP_BYTES asks for it.
const DefaultMmapBytes = 0

// mmapBytes is mmap_size: VAULT_DB_MMAP_BYTES (opt-in; 0 or unset: off).
// With it the pages a query reads come from the OS page cache, shared by
// all the connections, instead of a pread system call each (half or more
// of the CPU of the busiest calls; they ran 1.8-3 times faster). It is off
// by default because with the modernc driver an I/O error on a mapped page
// is an unrecoverable fault that kills the whole server, not one failed
// query, and the database may share a spinning disk with the media.
func mmapBytes() int64 {
	v := os.Getenv("VAULT_DB_MMAP_BYTES")
	if v == "" {
		return DefaultMmapBytes
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		log.Printf("db: VAULT_DB_MMAP_BYTES=%q is not a byte count; using %d", v, DefaultMmapBytes)
		return DefaultMmapBytes
	}
	return n
}

// Optimize keeps the query planner's statistics (sqlite_stat1) current:
// PRAGMA optimize analyzes the tables that have none yet or have grown
// 25-fold since, each from a sample (analysis_limit), so it takes
// milliseconds when nothing changed. Without statistics SQLite guesses,
// and picked an index on kind for lookups by parent (every episode of the
// library read for each series card). Open runs it; the server runs it
// again every few hours (OptimizeEvery) as the library grows.
func Optimize(ctx context.Context, d *sql.DB) error {
	c, err := d.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `PRAGMA analysis_limit=1000`); err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, `PRAGMA optimize=0x10002`)
	return err
}

// OptimizeEvery runs Optimize every interval until ctx ends.
func OptimizeEvery(ctx context.Context, d *sql.DB, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := Optimize(ctx, d); err != nil && ctx.Err() == nil {
				log.Printf("db: optimize: %v", err)
			}
		}
	}
}

// migration is one embedded .sql file.
type migration struct {
	ver  int
	name string
	md   migrationDir
}

// migrations lists every folder's files in version order. Two files of
// one version, in one folder or across folders, are an error: the
// database records only the version, so the second file would be taken
// for applied and silently skipped.
func migrations() ([]migration, error) {
	var out []migration
	seen := map[int]string{}
	for _, md := range migrationDirs {
		entries, err := fs.ReadDir(md.fs, md.dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			ver, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
			if err != nil {
				return nil, fmt.Errorf("migration %s: bad version prefix", e.Name())
			}
			file := path.Join(md.dir, e.Name())
			if prev, dup := seen[ver]; dup {
				return nil, fmt.Errorf("migration version %d used twice: %s and %s", ver, prev, file)
			}
			seen[ver] = file
			out = append(out, migration{ver, e.Name(), md})
		}
	}
	// by number, not by name: a pack's 10001 comes after core's 9999
	sort.Slice(out, func(i, j int) bool { return out[i].ver < out[j].ver })
	return out, nil
}

func migrate(d *sql.DB) error {
	// checked before the database is touched: a refused set applies nothing
	all, err := migrations()
	if err != nil {
		return err
	}
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	for _, m := range all {
		ver, name, md := m.ver, m.name, m.md
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, ver).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := fs.ReadFile(md.fs, path.Join(md.dir, name))
		if err != nil {
			return err
		}
		tx, err := d.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, ver); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
