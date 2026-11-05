package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// seedShows adds series with seasons and episodes, as the scanner makes them.
func seedShows(t *testing.T, p string, series int) {
	t.Helper()
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	id := 0
	add := func(q string, args ...any) {
		if _, err := tx.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for s := 0; s < series; s++ {
		id++
		sid := id
		add(`INSERT INTO items (id, kind, title, tmdb_id) VALUES (?, 'series', ?, ?)`, sid, fmt.Sprint("S", s), s)
		for se := 1; se <= 3; se++ {
			id++
			seid := id
			add(`INSERT INTO items (id, kind, parent_id, title, season_no) VALUES (?, 'season', ?, 'Сезон', ?)`, seid, sid, se)
			for ep := 1; ep <= 10; ep++ {
				id++
				add(`INSERT INTO items (id, kind, parent_id, title, season_no, episode_no) VALUES (?, 'episode', ?, 'Серия', ?, ?)`, id, seid, se, ep)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func explain(t *testing.T, d *sql.DB, q string, args ...any) string {
	t.Helper()
	rows, err := d.Query(`EXPLAIN QUERY PLAN `+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		sb.WriteString(detail + "\n")
	}
	return sb.String()
}

// episodesByParent is the shape of the show badges' lookups: an episode
// under the series, or under one of its seasons.
const episodesByParent = `SELECT e.id FROM items e WHERE e.kind='episode' AND (e.parent_id=?1 OR e.parent_id IN
  (SELECT s.id FROM items s WHERE s.kind='season' AND s.parent_id=?1))`

// The query planner gets statistics: a library opened again has them
// (sqlite_stat1), and with them an episode lookup by parent uses the
// parent index instead of walking every episode by kind.
func TestOpenGathersPlannerStatistics(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v.db")
	seedShows(t, p, 100)
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_stat1 WHERE tbl='items'`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("items statistics: %d %v", n, err)
	}
	if plan := explain(t, d, episodesByParent, 1); !strings.Contains(plan, "items_parent") || strings.Contains(plan, "items_tmdb") {
		t.Fatalf("plan:\n%s", plan)
	}
}

// The pool keeps its connections after a burst (a page load runs a dozen
// requests at once): database/sql's default keeps 2 of 8 and closes the
// rest, so the next burst opened them again, with cold page caches and
// their pragmas and schema to read anew.
func TestPoolKeepsConnections(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var conns []*sql.Conn
	for i := 0; i < 8; i++ {
		c, err := d.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		c.Close()
	}
	if st := d.Stats(); st.Idle != 8 || st.MaxIdleClosed != 0 {
		t.Fatalf("idle %d, closed for idle %d", st.Idle, st.MaxIdleClosed)
	}
}

// Optimize brings the statistics up to date after the library grew (the
// server runs it every few hours).
func TestOptimizeAfterGrowth(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v.db")
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	seedShows(t, p, 100)
	if err := Optimize(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_stat1 WHERE tbl='items'`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("items statistics: %d %v", n, err)
	}
}
