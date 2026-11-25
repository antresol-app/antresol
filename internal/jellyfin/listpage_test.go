package jellyfin

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/antresol-app/antresol/internal/db"
)

// queries counts the reads the counting driver saw.
var queries atomic.Int64

type countDriver struct{ driver.Driver }

func (d countDriver) Open(name string) (driver.Conn, error) {
	c, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return countConn{c}, nil
}

type countConn struct{ driver.Conn }

func (c countConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
}

func (c countConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
}

func (c countConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return e.ExecContext(ctx, q, args)
}

func (c countConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	e, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	queries.Add(1)
	return e.QueryContext(ctx, q, args)
}

func (c countConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c countConn) CheckNamedValue(v *driver.NamedValue) error {
	if ch, ok := c.Conn.(driver.NamedValueChecker); ok {
		return ch.CheckNamedValue(v)
	}
	return driver.ErrSkip
}

var registerCountDriver sync.Once

// newCountedLibEnv is newLibEnv on a database that counts its reads.
func newCountedLibEnv(t *testing.T) *libEnv {
	t.Helper()
	registerCountDriver.Do(func() {
		reg, _ := sql.Open("sqlite", "") // the registered driver: it has the database's SQL functions
		sql.Register("sqlite-count", countDriver{reg.Driver()})
		reg.Close()
	})
	was := openDB
	openDB = func(file string) (*sql.DB, error) { return db.OpenDriver("sqlite-count", file) }
	defer func() { openDB = was }()
	return newLibEnv(t)
}

// The commonest lists (films and series by name) are served by a paged
// query; the answer is the one the general path (load, filter, sort, cut)
// gives, ties and titles without letters included: filters=NoSuchFlag takes
// that path and changes nothing else.
func TestListFromSQLMatchesTheGeneralPath(t *testing.T) {
	e := newLibEnv(t)
	for i := 0; i < 22; i++ {
		e.movie(fmt.Sprintf("Фильм %02d", i), 2000+i, []string{"0+", "6+", "12+"}[i%3], 0, fmt.Sprintf("m%02d.mkv", i), "")
	}
	// a film named like the series (a tie: films first) and one with no sort
	// title (no letters or digits: ordered by its title)
	e.movie("Маша и Медведь", 2011, "0+", 0, "masha.mkv", "")
	e.movie("…", 2012, "0+", 0, "dots.mkv", "")
	views := []string{ViewID("movies"), ViewID("tvshows")}
	for name, who := range map[string]struct {
		tok string
		id  int64
	}{"adult": {e.anyaTok, e.anya.ID}, "kid": {e.mishaTok, e.misha.ID}} {
		tok := who.tok
		for _, q := range []string{
			"/Items?recursive=true&sortBy=SortName&limit=7",
			"/Items?recursive=true&sortBy=IsFolder,SortName&sortOrder=Ascending,Ascending&limit=7&startIndex=3",
			"/Items?recursive=true&includeItemTypes=Movie,Series&startIndex=5&limit=10&fields=MediaSources,Path",
			"/Items?recursive=true&includeItemTypes=Movie&startIndex=20&limit=10",
			"/Items?recursive=true&includeItemTypes=Series",
			"/Items?recursive=true&excludeItemTypes=Series&limit=5",
			"/Items?recursive=true&excludeItemTypes=Boxset,Series",
			"/Items?parentId=" + views[0] + "&limit=4&startIndex=2",
			"/Items?parentId=" + views[1] + "&recursive=true&limit=4",
			"/Items?recursive=true&limit=5&startIndex=500",
			"/Items?recursive=true",
			"/Users/" + UserID(who.id) + "/Items?recursive=true&limit=9",
		} {
			fast, slow := e.get(tok, q), e.get(tok, q+"&filters=NoSuchFlag")
			if fast.Code != 200 || fast.Body.String() != slow.Body.String() || !strings.Contains(fast.Body.String(), "TotalRecordCount") {
				t.Errorf("%s %s:\nsql     %d %.300s\ngeneral %d %.300s", name, q, fast.Code, fast.Body, slow.Code, slow.Body)
			}
		}
	}
}

// What is not a plain list by name takes the general path.
func TestPlainNameList(t *testing.T) {
	for q, want := range map[string]bool{
		"": true, "sortBy=SortName&limit=5": true, "sortBy=Name,SortName": true, "sortOrder=Ascending": true,
		"sortBy=DateCreated": false, "sortBy=SortName,ProductionYear": false, "sortBy=Random": false, "sortOrder=Descending": false,
		"genres=Драма": false, "years=2014": false, "isPlayed=false": false, "isFavorite=true": false, "isFavorite=false": true,
		"filters=IsUnplayed": false, "ids=1": false, "searchTerm=x": false,
		"parentId=" + ItemID(7): false, "parentId=" + ViewID("movies"): true,
	} {
		r := httptest.NewRequest("GET", "/Items?"+q, nil)
		if got := plainNameList(r); got != want {
			t.Errorf("%q: %v, want %v", q, got, want)
		}
	}
}

// A page costs the same number of reads whatever its length: the files,
// progress, episode counts, seasons and series of its items come in a few
// queries for all of them, not a few per item.
func TestListQueriesDoNotGrowWithThePage(t *testing.T) {
	e := newCountedLibEnv(t)
	for i := 0; i < 30; i++ {
		e.movie(fmt.Sprintf("Фильм %02d", i), 2000+i, "12+", 0, fmt.Sprintf("m%02d.mkv", i), "")
	}
	for _, id := range e.eps[:2] {
		if err := e.prog.Save(context.Background(), e.anya.ID, id, 120_000, 600_000); err != nil {
			t.Fatal(err)
		}
	}
	reads := func(path string) int64 {
		e.t.Helper()
		queries.Store(0)
		if w := e.get(e.anyaTok, path); w.Code != 200 {
			e.t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		return queries.Load()
	}
	for _, c := range []struct {
		name, path   string
		small, large int // pages of the same kinds of item, one longer
	}{
		{"films and series by name", "/Items?recursive=true&includeItemTypes=Movie,Series&fields=MediaSources,MediaStreams,Path&limit=", 6, 34},
		{"films with a filter", "/Items?recursive=true&includeItemTypes=Movie&filters=IsUnplayed&fields=MediaSources&limit=", 3, 33},
		{"episodes by date played", "/Items?recursive=true&includeItemTypes=Episode&sortBy=DatePlayed&limit=", 2, 4},
		{"seasons and episodes of a series", "/Items?recursive=true&parentId=" + ItemID(e.series) + "&limit=", 3, 6},
	} {
		small, large := reads(c.path+fmt.Sprint(c.small)), reads(c.path+fmt.Sprint(c.large))
		if small != large || large > 25 {
			t.Errorf("%s: %d reads for %d items, %d for %d (a longer page should not cost more, and it should not cost many)",
				c.name, small, c.small, large, c.large)
		}
	}
}
