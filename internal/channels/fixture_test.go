package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

type fixture struct {
	t   *testing.T
	db  *sql.DB
	now time.Time
	st  *Store
	n   int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	f := &fixture{t: t, db: d, now: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
	f.st = &Store{DB: d, Now: func() time.Time { return f.now }}
	return f
}

func (f *fixture) exec(q string, args ...any) int64 {
	f.t.Helper()
	res, err := f.db.Exec(q, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (f *fixture) file(item int64, minutes int) int64 {
	f.n++
	return f.exec(`INSERT INTO files (item_id, path, duration_ms) VALUES (?,?,?)`, item, fmt.Sprintf("/m/f%04d.mkv", f.n), int64(minutes)*60000)
}

type itemOpt struct {
	year   int
	rating string
	genres []string
}

func (f *fixture) item(kind, title string, parent int64, o itemOpt) int64 {
	meta, _ := json.Marshal(map[string]any{"genres": o.genres})
	var p any
	if parent != 0 {
		p = parent
	}
	return f.exec(`INSERT INTO items (kind, parent_id, title, sort_title, year, rating, meta_json) VALUES (?,?,?,?,?,?,?)`,
		kind, p, title, title, o.year, o.rating, string(meta))
}

func (f *fixture) movie(title string, minutes int, o itemOpt) int64 {
	id := f.item("movie", title, 0, o)
	f.file(id, minutes)
	return id
}

// series makes a series with seasons × episodes of minutes each; it
// returns the series id and the episode ids in order.
func (f *fixture) series(title string, seasons, episodes, minutes int, o itemOpt) (int64, []int64) {
	s := f.item("series", title, 0, o)
	var eps []int64
	for sn := 1; sn <= seasons; sn++ {
		se := f.exec(`INSERT INTO items (kind, parent_id, title, season_no) VALUES ('season',?,?,?)`, s, fmt.Sprintf("Season %d", sn), sn)
		for en := 1; en <= episodes; en++ {
			e := f.exec(`INSERT INTO items (kind, parent_id, title, season_no, episode_no) VALUES ('episode',?,?,?,?)`, se, fmt.Sprintf("E%d", en), sn, en)
			f.file(e, minutes)
			eps = append(eps, e)
		}
	}
	return s, eps
}

func (f *fixture) user(name string) int64 {
	return f.exec(`INSERT INTO users (name, avatar) VALUES (?, 'a')`, name)
}

func (f *fixture) builder() *Builder {
	return &Builder{Store: f.st, Catalog: Catalog{DB: f.db}}
}

func (f *fixture) create(c Channel) Channel {
	f.t.Helper()
	c.Enabled = true
	if c.Seed == 0 {
		c.Seed = 42
	}
	out, err := f.st.Create(context.Background(), c)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) entries(id int64) []Entry {
	f.t.Helper()
	es, err := f.st.Range(context.Background(), id, time.Unix(0, 0), f.now.Add(1000*time.Hour))
	if err != nil {
		f.t.Fatal(err)
	}
	return es
}
