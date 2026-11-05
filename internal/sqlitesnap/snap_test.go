package sqlitesnap

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

type stamp struct {
	size  int64
	mtime int64
}

func stat(t *testing.T, p string) stamp {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return stamp{st.Size(), st.ModTime().UnixNano()}
}

// A WAL database with a row that is only in the -wal file: the snapshot
// must carry it, and the source must not be touched.
func TestSnapshotWAL(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "app.db")
	live, err := sql.Open("sqlite", "file:"+src+"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	live.SetMaxOpenConns(1)
	if _, err := live.Exec(`CREATE TABLE t(v TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Exec(`INSERT INTO t VALUES ('только в wal')`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src + "-wal"); err != nil {
		t.Fatalf("no -wal: %v", err)
	}
	before := [2]stamp{stat(t, src), stat(t, src+"-wal")}

	out := t.TempDir()
	cp, err := Snapshot(src, out)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(cp) != out {
		t.Fatalf("snapshot %s not in %s", cp, out)
	}
	ro, err := OpenRO(cp)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var v string
	if err := ro.QueryRow(`SELECT v FROM t`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "только в wal" {
		t.Fatalf("row %q", v)
	}
	if after := [2]stamp{stat(t, src), stat(t, src+"-wal")}; after != before {
		t.Fatalf("source changed: %v → %v", before, after)
	}
}

// query_only holds on every connection of the pool.
func TestOpenROQueryOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.db")
	w, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`CREATE TABLE t(v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	w.Close()

	ro, err := OpenRO(p)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	ctx := context.Background()
	c1, err := ro.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c2, err := ro.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	for i, c := range []*sql.Conn{c1, c2} {
		if _, err := c.ExecContext(ctx, `INSERT INTO t VALUES (1)`); err == nil {
			t.Fatalf("connection %d accepted an INSERT", i+1)
		}
	}
}
