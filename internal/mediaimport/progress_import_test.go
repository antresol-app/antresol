package mediaimport

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
)

var (
	jfT1 = "2026-10-01 19:30:00.1234567"
	jfT2 = "2026-10-03 08:00:00"
	t1   = time.Date(2026, 10, 1, 19, 30, 0, 123456700, time.UTC)
	t2   = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) // between jfT1 and jfT2
)

const jfMedia = "/srv/media/media"

// jfOpts maps Jellyfin's host paths into the env and picks «alex» for
// the Jellyfin user U1.
func (e *progEnv) jfOpts(dbPath string) ProgressOptions {
	o := e.opts()
	o.JellyfinDB = dbPath
	o.Paths = PathMap{{From: jfMedia, To: filepath.Join(e.root, "media")}}
	o.UserMap = map[string]int64{"jellyfin:U1": e.uid}
	return o
}

var jfUsers = []jfUser{{"U1", "Alex"}, {"U2", "Гость"}}

// interstellar is a 10 000 000 ms movie in Vault and in Jellyfin.
func (e *progEnv) interstellar() (vaultID int64, jf jfItem) {
	p := e.file("movies/Interstellar (2014)/Interstellar.mkv")
	id := e.movie("Interstellar", 157336, p, 10_000_000)
	return id, jfItem{ID: "M1", Type: jfMovie, Name: "Interstellar", Path: jfMedia + "/movies/Interstellar (2014)/Interstellar.mkv",
		RunTimeTicks: 100_000_000_000, Tmdb: "157336"}
}

func TestJellyfinImportByPath(t *testing.T) {
	e := newProgEnv(t)
	id, m := e.interstellar()
	m.Tmdb = "" // the path alone finds it
	dbp := jellyfinDB(t, jfUsers, []jfItem{m}, []jfUserData{{ItemID: "M1", UserID: "U1", PosTicks: 40_000_000_000, LastPlayed: jfT1}})
	rep := e.run(ImportJellyfin, e.jfOpts(dbp))
	r, ok := e.row(id)
	if !ok || r.Pos != 4_000_000 || r.Dur != 10_000_000 || r.Finished || r.UpdatedAt != tsOf(t1) {
		t.Fatalf("progress %+v %v, lines %+v", r, ok, rep.Lines)
	}
	l := onlyLine(t, rep)
	if l.Status != StatusImported || l.Source != SourceJellyfin || l.Kind != "progress" || l.Ref != "item:"+strconv.FormatInt(id, 10) {
		t.Fatalf("line %+v", l)
	}
}

func TestJellyfinPlayedFinished(t *testing.T) {
	e := newProgEnv(t)
	id, m := e.interstellar()
	dbp := jellyfinDB(t, jfUsers, []jfItem{m}, []jfUserData{{ItemID: "M1", UserID: "U1", Played: true, LastPlayed: jfT1}})
	e.run(ImportJellyfin, e.jfOpts(dbp))
	if r, ok := e.row(id); !ok || !r.Finished || r.Pos != 10_000_000 || r.UpdatedAt != tsOf(t1) {
		t.Fatalf("progress %+v %v", r, ok)
	}
}

func TestJellyfinPlayedNoDurationKeepsNewer(t *testing.T) {
	e := newProgEnv(t)
	ctx := context.Background()
	newer := e.movie("Newer", 1, e.file("movies/A/a.mkv"), 0)
	fresh := e.movie("Fresh", 2, e.file("movies/B/b.mkv"), 0)
	if err := e.ps.SaveAt(ctx, e.uid, newer, 5_000, 100_000, t2); err != nil {
		t.Fatal(err)
	}
	dbp := jellyfinDB(t, jfUsers, []jfItem{
		{ID: "A", Type: jfMovie, Name: "Newer", Path: jfMedia + "/movies/A/a.mkv"},
		{ID: "B", Type: jfMovie, Name: "Fresh", Path: jfMedia + "/movies/B/b.mkv"},
	}, []jfUserData{
		{ItemID: "A", UserID: "U1", Played: true, LastPlayed: jfT1},
		{ItemID: "B", UserID: "U1", Played: true, LastPlayed: jfT1},
	})
	e.run(ImportJellyfin, e.jfOpts(dbp))
	if r, _ := e.row(newer); r.Pos != 5_000 || r.Finished || r.UpdatedAt != tsOf(t2) {
		t.Fatalf("newer Vault progress changed: %+v", r)
	}
	if r, ok := e.row(fresh); !ok || !r.Finished || r.UpdatedAt != tsOf(t1) {
		t.Fatalf("fresh %+v %v (want finished at the source time, not now)", r, ok)
	}
}

func TestJellyfinImportByTmdbEpisode(t *testing.T) {
	e := newProgEnv(t)
	e.episode("Breaking Bad", 1396, 1, 1, e.file("series/BB/S01E01.mkv"))
	want := e.episode("Breaking Bad", 1396, 1, 2, e.file("series/BB/S01E02.mkv"))
	dbp := jellyfinDB(t, jfUsers, []jfItem{
		{ID: "S", Type: jfSeries, Name: "Breaking Bad", Path: "/old/shows/Breaking Bad", Tmdb: "1396"},
		{ID: "SE", Type: jfSeason, Name: "Season 1", Index: 1, SeriesID: "S"},
		{ID: "E2", Type: jfEpisode, Name: "Cat's in the Bag", Path: "/old/shows/Breaking Bad/s1e2.mkv", Index: 2, ParentIndex: 1, SeriesID: "S",
			RunTimeTicks: 30_000_000_000},
	}, []jfUserData{{ItemID: "E2", UserID: "U1", PosTicks: 6_000_000_000, LastPlayed: jfT1}})
	rep := e.run(ImportJellyfin, e.jfOpts(dbp))
	r, ok := e.row(want)
	if !ok || r.Pos != 600_000 || r.Dur != 3_000_000 {
		t.Fatalf("S01E02 progress %+v %v; lines %+v", r, ok, rep.Lines)
	}
	if n := e.count("progress"); n != 1 {
		t.Fatalf("%d progress rows", n)
	}
}

func TestJellyfinUnmatchedReported(t *testing.T) {
	e := newProgEnv(t)
	dbp := jellyfinDB(t, jfUsers, []jfItem{{ID: "X", Type: jfMovie, Name: "Nowhere", Path: "/old/Nowhere.mkv", Tmdb: "999"}},
		[]jfUserData{{ItemID: "X", UserID: "U1", PosTicks: 10_000, LastPlayed: jfT1}})
	l := onlyLine(t, e.run(ImportJellyfin, e.jfOpts(dbp)))
	if l.Status != StatusFailed || !strings.HasPrefix(l.Reason, "Не найдено в Antresol:") || !strings.Contains(l.Reason, "Nowhere (/old/Nowhere.mkv)") {
		t.Fatalf("line %+v", l)
	}
}

func TestJellyfinNewerVaultProgressKept(t *testing.T) {
	e := newProgEnv(t)
	id, m := e.interstellar()
	if err := e.ps.SaveAt(context.Background(), e.uid, id, 9_000_000, 10_000_000, t2); err != nil {
		t.Fatal(err)
	}
	dbp := jellyfinDB(t, jfUsers, []jfItem{m}, []jfUserData{{ItemID: "M1", UserID: "U1", PosTicks: 10_000_000_000, LastPlayed: jfT1}})
	l := onlyLine(t, e.run(ImportJellyfin, e.jfOpts(dbp)))
	if r, _ := e.row(id); r.Pos != 9_000_000 || r.UpdatedAt != tsOf(t2) {
		t.Fatalf("progress changed: %+v", r)
	}
	if l.Status != StatusSkipped || l.Reason != "В Antresol позиция новее" {
		t.Fatalf("line %+v", l)
	}
}

func (e *progEnv) progressRows() [][]any {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT user_id, item_id, position_ms, duration_ms, finished, updated_at, locator FROM progress ORDER BY 1, 2`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		var a, b, c, d, f int64
		var u, l string
		if err := rows.Scan(&a, &b, &c, &d, &f, &u, &l); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, []any{a, b, c, d, f, u, l})
	}
	return out
}

func TestJellyfinIdempotent(t *testing.T) {
	e := newProgEnv(t)
	_, m := e.interstellar()
	e.episode("Breaking Bad", 1396, 1, 1, e.file("series/BB/S01E01.mkv"))
	dbp := jellyfinDB(t, jfUsers, []jfItem{m,
		{ID: "S", Type: jfSeries, Name: "Breaking Bad", Tmdb: "1396"},
		{ID: "E1", Type: jfEpisode, Name: "Pilot", Path: jfMedia + "/series/BB/S01E01.mkv", Index: 1, ParentIndex: 1, SeriesID: "S"},
	}, []jfUserData{
		{ItemID: "M1", UserID: "U1", PosTicks: 40_000_000_000, LastPlayed: jfT1},
		{ItemID: "E1", UserID: "U1", Played: true}, // no LastPlayedDate
	})
	e.run(ImportJellyfin, e.jfOpts(dbp))
	first := e.progressRows()
	if len(first) != 2 {
		t.Fatalf("rows %v", first)
	}
	rep := e.run(ImportJellyfin, e.jfOpts(dbp))
	if got := e.progressRows(); !reflect.DeepEqual(got, first) {
		t.Fatalf("second run changed progress:\n%v\n%v", first, got)
	}
	if len(rep.Lines) != 2 {
		t.Fatalf("lines %+v", rep.Lines)
	}
	for _, l := range rep.Lines {
		if l.Status != StatusSkipped || l.Reason != "Уже в Antresol" {
			t.Fatalf("second run: line %+v", l)
		}
	}
}

func TestJellyfinDuplicateUserDataNewest(t *testing.T) {
	e := newProgEnv(t)
	id, m := e.interstellar()
	dbp := jellyfinDB(t, jfUsers, []jfItem{m}, []jfUserData{
		{ItemID: "M1", UserID: "U1", Key: "a", PosTicks: 50_000_000_000, LastPlayed: jfT2},
		{ItemID: "M1", UserID: "U1", Key: "b", PosTicks: 10_000_000_000, LastPlayed: jfT1},
	})
	rep := e.run(ImportJellyfin, e.jfOpts(dbp))
	if r, _ := e.row(id); r.Pos != 5_000_000 {
		t.Fatalf("progress %+v, lines %+v", r, rep.Lines)
	}
	onlyLine(t, rep)
}

func TestJellyfinFavoritesToWatchlist(t *testing.T) {
	e := newProgEnv(t)
	movie, m := e.interstellar()
	e.episode("Breaking Bad", 1396, 1, 1, e.file("series/BB/S01E01.mkv"))
	series, err := e.lib.ItemByTMDB(context.Background(), "series", 1396)
	if err != nil {
		t.Fatal(err)
	}
	dbp := jellyfinDB(t, jfUsers, []jfItem{m, {ID: "S", Type: jfSeries, Name: "Breaking Bad", Path: "/old/Breaking Bad", Tmdb: "1396"}},
		[]jfUserData{{ItemID: "M1", UserID: "U1", Favorite: true}, {ItemID: "S", UserID: "U1", Favorite: true}})
	for run := 0; run < 2; run++ {
		rep := e.run(ImportJellyfin, e.jfOpts(dbp))
		got := e.watchlist()
		if len(got) != 2 || !(got[0] == movie && got[1] == series.ID || got[1] == movie && got[0] == series.ID) {
			t.Fatalf("run %d: watchlist %v (movie %d, series %d); lines %+v", run, got, movie, series.ID, rep.Lines)
		}
		wantStatus, wantReason := StatusImported, "В «Хочу посмотреть»"
		if run == 1 {
			wantStatus, wantReason = StatusSkipped, "Уже в «Хочу посмотреть»"
		}
		for _, l := range rep.Lines {
			if l.Status != wantStatus || l.Reason != wantReason {
				t.Fatalf("run %d: line %+v", run, l)
			}
		}
	}
	if n := e.count("progress"); n != 0 {
		t.Fatalf("favourites wrote %d progress rows", n)
	}
}

func TestJellyfinLiveTVWarning(t *testing.T) {
	e := newProgEnv(t)
	_, m := e.interstellar()
	dbp := jellyfinDB(t, jfUsers, []jfItem{m, {ID: "C1", Type: jfChannel, Name: "Первый"}, {ID: "C2", Type: jfChannel, Name: "Второй"}},
		[]jfUserData{{ItemID: "M1", UserID: "U1", PosTicks: 1, LastPlayed: jfT1}})
	w := linesWith(e.run(ImportJellyfin, e.jfOpts(dbp)), StatusWarning)
	if len(w) != 1 || w[0].Reason != "Jellyfin: ТВ-каналы не переносятся (2 канала)" {
		t.Fatalf("warnings %+v", w)
	}
}

func TestJellyfinUnmappedUserSkipped(t *testing.T) {
	e := newProgEnv(t)
	_, m := e.interstellar()
	dbp := jellyfinDB(t, jfUsers, []jfItem{m}, []jfUserData{
		{ItemID: "M1", UserID: "U2", PosTicks: 10_000_000, LastPlayed: jfT1},
		{ItemID: "M1", UserID: "U2", Key: "fav", Favorite: true},
	})
	rep := e.run(ImportJellyfin, e.jfOpts(dbp))
	if len(rep.Lines) != 2 {
		t.Fatalf("lines %+v", rep.Lines)
	}
	for _, l := range rep.Lines {
		if l.Status != StatusSkipped || l.Reason != "Профиль не выбран" {
			t.Fatalf("line %+v", l)
		}
	}
	if e.count("progress")+e.count("watchlist") != 0 {
		t.Fatal("wrote for an unmapped user")
	}
}

func TestJellyfinDryRunNoWrites(t *testing.T) {
	e := newProgEnv(t)
	_, m := e.interstellar()
	dbp := jellyfinDB(t, jfUsers, []jfItem{m}, []jfUserData{
		{ItemID: "M1", UserID: "U1", PosTicks: 10_000_000, LastPlayed: jfT1},
		{ItemID: "M1", UserID: "U1", Key: "fav", Favorite: true},
	})
	o := e.jfOpts(dbp)
	o.DryRun = true
	rep := e.run(ImportJellyfin, o)
	if e.count("progress")+e.count("watchlist") != 0 {
		t.Fatal("dry run wrote")
	}
	if len(rep.Lines) != 2 || len(linesWith(rep, StatusPlanned)) != 2 {
		t.Fatalf("lines %+v", rep.Lines)
	}
}

func TestJellyfinFiltersTypes(t *testing.T) {
	e := newProgEnv(t)
	dbp := jellyfinDB(t, jfUsers, []jfItem{{ID: "C1", Type: jfChannel, Name: "Первый"}, {ID: "P1", Type: jfPerson, Name: "Someone"}},
		[]jfUserData{
			{ItemID: "C1", UserID: "U1", Played: true, PosTicks: 5, LastPlayed: jfT1, Favorite: true},
			{ItemID: "P1", UserID: "U1", Played: true, PosTicks: 5, LastPlayed: jfT1, Favorite: true},
		})
	rep := e.run(ImportJellyfin, e.jfOpts(dbp))
	for _, l := range rep.Lines {
		if l.Status != StatusWarning && l.Reason != "Нет данных для переноса" {
			t.Fatalf("line %+v", l)
		}
	}
	if e.count("progress")+e.count("watchlist") != 0 {
		t.Fatal("wrote progress for a channel or person")
	}
	w := linesWith(rep, StatusWarning)
	if len(w) != 2 || w[1].Reason != "Jellyfin: не переносятся 1 отметка (люди)" {
		t.Fatalf("warnings %+v", w)
	}
}

// A source time in the future is no time: else the store clamps it to
// "now" at every run, and a re-run overwrites what was watched in Vault
// since.
func TestJellyfinFutureTimeNeverOverwrites(t *testing.T) {
	e := newProgEnv(t)
	id, m := e.interstellar()
	dbp := jellyfinDB(t, jfUsers, []jfItem{m}, []jfUserData{{ItemID: "M1", UserID: "U1", PosTicks: 40_000_000_000, LastPlayed: "2099-01-01 00:00:00"}})
	l := onlyLine(t, e.run(ImportJellyfin, e.jfOpts(dbp)))
	if l.Status != StatusImported || l.Reason != "Время в источнике в будущем — перенесено без даты" {
		t.Fatalf("run 1: line %+v", l)
	}
	if r, _ := e.row(id); r.Pos != 4_000_000 || r.UpdatedAt != tsOf(neverTime) {
		t.Fatalf("run 1: progress %+v", r)
	}
	if err := e.ps.SaveAt(context.Background(), e.uid, id, 9_000_000, 10_000_000, time.Time{}); err != nil { // watched on in Vault
		t.Fatal(err)
	}
	l = onlyLine(t, e.run(ImportJellyfin, e.jfOpts(dbp)))
	if r, _ := e.row(id); r.Pos != 9_000_000 {
		t.Fatalf("run 2 overwrote Vault: %+v", r)
	}
	if l.Status != StatusSkipped || l.Reason != "В Antresol позиция новее" {
		t.Fatalf("run 2: line %+v", l)
	}
}

// «Москва слезам не верит» is cd1 + cd2 in the real library: a position
// counts across both parts, so 70 of 120 minutes is not finished.
func TestJellyfinMultiPartDuration(t *testing.T) {
	e := newProgEnv(t)
	cd1 := e.file("movies/Moscow (1980)/Moscow cd1.mkv")
	cd2 := e.file("movies/Moscow (1980)/Moscow cd2.mkv")
	id, err := e.lib.UpsertMovie(context.Background(), library.MovieInput{Title: "Москва слезам не верит", Year: 1980, TmdbID: 21028,
		Files: []library.FileInput{{Path: cd1, Size: 1, Mtime: 1}, {Path: cd2, Size: 1, Mtime: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, e.db, `UPDATE files SET duration_ms=3600000 WHERE item_id=?`, id)
	const min = 60 * 1000 * 10_000 // ticks
	for _, tc := range []struct {
		name string
		path string
	}{{"by path", jfMedia + "/movies/Moscow (1980)/Moscow cd1.mkv"}, {"by tmdb", "/old/Moscow.mkv"}} {
		mustExec(t, e.db, `DELETE FROM progress`)
		dbp := jellyfinDB(t, jfUsers, []jfItem{{ID: "M", Type: jfMovie, Name: "Москва слезам не верит", Path: tc.path,
			RunTimeTicks: 100 * min, Tmdb: "21028"}}, // Jellyfin's own length is off: Vault's parts win
			[]jfUserData{{ItemID: "M", UserID: "U1", PosTicks: 70 * min, LastPlayed: jfT1}})
		rep := e.run(ImportJellyfin, e.jfOpts(dbp))
		if r, ok := e.row(id); !ok || r.Pos != 4_200_000 || r.Dur != 7_200_000 || r.Finished {
			t.Fatalf("%s: progress %+v %v; lines %+v", tc.name, r, ok, rep.Lines)
		}
	}
}

// Without every part's length Jellyfin's RunTimeTicks is the duration.
func TestJellyfinDurationFallsBackToRunTime(t *testing.T) {
	e := newProgEnv(t)
	id := e.movie("Nolen", 5, e.file("movies/N/n.mkv"), 0)
	dbp := jellyfinDB(t, jfUsers, []jfItem{{ID: "N", Type: jfMovie, Name: "Nolen", Path: jfMedia + "/movies/N/n.mkv", RunTimeTicks: 72_000_000_000}},
		[]jfUserData{{ItemID: "N", UserID: "U1", PosTicks: 36_000_000_000, LastPlayed: jfT1}})
	e.run(ImportJellyfin, e.jfOpts(dbp))
	if r, _ := e.row(id); r.Dur != 7_200_000 || r.Pos != 3_600_000 {
		t.Fatalf("progress %+v", r)
	}
}

func TestUnreadableDatabaseFailed(t *testing.T) {
	e := newProgEnv(t)
	p := jellyfinDB(t, jfUsers, nil, nil)
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o644) })
	if f, err := os.Open(p); err == nil { // root reads anyway
		f.Close()
		t.Skip("running as root")
	}
	o := e.opts()
	o.JellyfinDB = p
	l := onlyLine(t, e.run(ImportJellyfin, o))
	if l.Status != StatusFailed || l.Reason != "Не удалось прочитать базу Jellyfin" {
		t.Fatalf("line %+v", l)
	}
}

func TestJellyfinRetainedRowsIgnored(t *testing.T) {
	e := newProgEnv(t)
	id, m := e.interstellar()
	dbp := jellyfinDB(t, jfUsers, []jfItem{m}, []jfUserData{{ItemID: "M1", UserID: "U1", PosTicks: 10_000_000, LastPlayed: jfT1, Retention: jfT2}})
	e.run(ImportJellyfin, e.jfOpts(dbp))
	if _, ok := e.row(id); ok {
		t.Fatal("imported a row kept only for retention")
	}
}

// --- Kavita ---

func (e *progEnv) bookPaths() PathMap {
	return PathMap{
		{From: "/books/ebooks", To: filepath.Join(e.root, "media", "books", "ebooks")},
		{From: "/books/manga", To: filepath.Join(e.root, "media", "books", "manga")},
		{From: "/audiobooks", To: filepath.Join(e.root, "media", "books", "audiobooks")},
		{From: "/ebooks", To: filepath.Join(e.root, "media", "books", "ebooks")},
	}
}

func (e *progEnv) comic(rel string) int64 {
	e.t.Helper()
	id, err := e.lib.UpsertComicVolume(context.Background(), library.ComicInput{File: library.FileInput{Path: e.file(rel), Size: 1, Mtime: 1}, Pages: 40})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *progEnv) book(rel string) int64 {
	e.t.Helper()
	id, err := e.lib.UpsertBook(context.Background(), library.BookInput{Files: []library.FileInput{{Path: e.file(rel), Size: 1, Mtime: 1}}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestKavitaPages(t *testing.T) {
	e := newProgEnv(t)
	v1 := e.comic("books/manga/Berserk/Berserk v01.cbz")
	v2 := e.comic("books/manga/Berserk/Berserk v02.cbz")
	bk := e.book("books/ebooks/Tolstoy/War and Peace.epub")
	dbp := kavitaDB(t, []kvUser{{1, "Alex"}}, []kvChapter{
		{1, 40, "/books/manga/Berserk/Berserk v01.cbz"},
		{2, 40, "/books/manga/Berserk/Berserk v02.cbz"},
		{3, 100, "/books/ebooks/Tolstoy/War and Peace.epub"},
	}, []kvProgress{{1, 1, 12, jfT1}, {1, 2, 0, jfT1}, {1, 3, 25, jfT1}})
	o := e.opts()
	o.KavitaDB, o.Paths, o.UserMap = dbp, e.bookPaths(), map[string]int64{"kavita:1": e.uid}
	rep := e.run(ImportKavita, o)
	if r, ok := e.row(v1); !ok || r.Locator != "page:12" || r.Pos != 300_000 || r.Dur != 1_000_000 || r.UpdatedAt != tsOf(t1) {
		t.Fatalf("v01 %+v %v; lines %+v", r, ok, rep.Lines)
	}
	if r, ok := e.row(v2); !ok || r.Locator != "page:1" || r.Pos != 0 {
		t.Fatalf("v02 %+v %v", r, ok)
	}
	if r, ok := e.row(bk); !ok || r.Locator != "" || r.Pos != 250_000 {
		t.Fatalf("epub %+v %v", r, ok)
	}
	var epub *Line
	for i, l := range rep.Lines {
		if l.Status != StatusImported {
			t.Fatalf("line %+v", l)
		}
		if l.Ref == "item:"+strconv.FormatInt(bk, 10) {
			epub = &rep.Lines[i]
		}
	}
	if epub == nil || epub.Reason != "Позиция внутри книги — по проценту" {
		t.Fatalf("epub line %+v", epub)
	}
}

// --- Audiobookshelf ---

func (e *progEnv) absOpts(dbp string) ProgressOptions {
	o := e.opts()
	o.ABSDB, o.Paths, o.UserMap = dbp, e.bookPaths(), map[string]int64{"audiobookshelf:u-1": e.uid}
	return o
}

func TestABSAudiobookFolder(t *testing.T) {
	e := newProgEnv(t)
	var files []library.FileInput
	for _, n := range []string{"01.mp3", "02.mp3", "03.mp3"} {
		files = append(files, library.FileInput{Path: e.file("books/audiobooks/Author/Book/" + n), Size: 1, Mtime: 1})
	}
	id, err := e.lib.UpsertAudiobook(context.Background(), library.AudiobookInput{Dir: filepath.Join(e.root, "media/books/audiobooks/Author/Book"), Files: files, Title: "Book"})
	if err != nil {
		t.Fatal(err)
	}
	dbp := absDB(t, []absUser{{"u-1", "alex"}}, []absItem{{"b-1", "/audiobooks/Author/Book", "Book"}},
		[]absProgress{{UserID: "u-1", MediaID: "b-1", Duration: 3600, Current: 1200.5, Updated: "2026-10-01 21:30:00.123 +02:00"}})
	rep := e.run(ImportABS, e.absOpts(dbp))
	r, ok := e.row(id)
	if !ok || r.Pos != 1_200_500 || r.Dur != 3_600_000 || r.Finished || r.UpdatedAt != tsOf(time.Date(2026, 10, 1, 19, 30, 0, 123e6, time.UTC)) {
		t.Fatalf("progress %+v %v; lines %+v", r, ok, rep.Lines)
	}
	if l := onlyLine(t, rep); l.Status != StatusImported || l.Source != SourceABS {
		t.Fatalf("line %+v", l)
	}
}

func TestABSSingleFileEbook(t *testing.T) {
	e := newProgEnv(t)
	id := e.book("books/ebooks/Book.epub")
	dbp := absDB(t, []absUser{{"u-1", "alex"}}, []absItem{{"b-1", "/ebooks/Book.epub", "Book"}},
		[]absProgress{{UserID: "u-1", MediaID: "b-1", EbookLoc: "epubcfi(/6/4!/4/2/1:0)", EbookProgress: 0.25, Updated: "2026-10-01 19:30:00.000 +00:00"}})
	rep := e.run(ImportABS, e.absOpts(dbp))
	if r, ok := e.row(id); !ok || r.Pos != 250_000 {
		t.Fatalf("progress %+v %v; lines %+v", r, ok, rep.Lines)
	}
}

func TestABSEbookCFI(t *testing.T) {
	e := newProgEnv(t)
	epub := e.book("books/ebooks/A/Book.epub")
	pdf := e.book("books/ebooks/B/Doc.pdf")
	dbp := absDB(t, []absUser{{"u-1", "alex"}}, []absItem{{"b-1", "/ebooks/A/Book.epub", "Book"}, {"b-2", "/ebooks/B/Doc.pdf", "Doc"}},
		[]absProgress{
			{UserID: "u-1", MediaID: "b-1", EbookLoc: "epubcfi(/6/14!/4/2/1:0)", EbookProgress: 0.5, Updated: "2026-10-01 19:30:00.000 +00:00"},
			{UserID: "u-1", MediaID: "b-2", EbookLoc: "17", EbookProgress: 0.17, Updated: "2026-10-01 19:30:00.000 +00:00"},
		})
	e.run(ImportABS, e.absOpts(dbp))
	if r, _ := e.row(epub); r.Locator != "epubcfi(/6/14!/4/2/1:0)" || r.Pos != 500_000 {
		t.Fatalf("epub %+v", r)
	}
	if r, _ := e.row(pdf); r.Locator != "page:17" || r.Pos != 170_000 {
		t.Fatalf("pdf %+v", r)
	}
}

func TestABSPageZeroAndPodcasts(t *testing.T) {
	e := newProgEnv(t)
	pdf := e.book("books/ebooks/B/Doc.pdf")
	dbp := absDB(t, []absUser{{"u-1", "alex"}}, []absItem{{"b-2", "/ebooks/B/Doc.pdf", "Doc"}},
		[]absProgress{
			{UserID: "u-1", MediaID: "b-2", EbookLoc: "0", EbookProgress: 0.01, Updated: "2026-10-01 19:30:00.000 +00:00"},
			{UserID: "u-1", MediaID: "pe-1", ItemType: "podcastEpisode", Duration: 100, Current: 50, Updated: "2026-10-01 19:30:00.000 +00:00"},
			{UserID: "u-1", MediaID: "pe-2", ItemType: "podcastEpisode", Duration: 100, Current: 50, Updated: "2026-10-01 19:30:00.000 +00:00"},
		})
	rep := e.run(ImportABS, e.absOpts(dbp))
	if r, _ := e.row(pdf); r.Locator != "page:1" {
		t.Fatalf("pdf %+v", r)
	}
	w := linesWith(rep, StatusWarning)
	if len(w) != 1 || w[0].Reason != "Audiobookshelf: не переносятся 2 отметки (подкасты)" {
		t.Fatalf("warnings %+v", w)
	}
}

func TestABSUnmatchedReported(t *testing.T) {
	e := newProgEnv(t)
	dbp := absDB(t, []absUser{{"u-1", "alex"}}, []absItem{{"b-1", "/audiobooks/Gone", "Gone"}},
		[]absProgress{{UserID: "u-1", MediaID: "b-1", Duration: 100, Current: 10, Updated: "2026-10-01 19:30:00.000 +00:00"}})
	l := onlyLine(t, e.run(ImportABS, e.absOpts(dbp)))
	if l.Status != StatusFailed || l.Reason != "Не найдено в Antresol: Gone (/audiobooks/Gone)" {
		t.Fatalf("line %+v", l)
	}
}

// --- all sources ---

func TestEmptyDatabases(t *testing.T) {
	e := newProgEnv(t)
	o := e.opts()
	o.JellyfinDB = jellyfinDB(t, nil, nil, nil)
	o.KavitaDB = kavitaDB(t, nil, nil, nil)
	o.ABSDB = absDB(t, nil, nil, nil)
	for _, f := range []importFunc{ImportJellyfin, ImportKavita, ImportABS} {
		l := onlyLine(t, e.run(f, o))
		if l.Status != StatusSkipped || l.Reason != "Нет данных для переноса" {
			t.Fatalf("line %+v", l)
		}
	}
}

func TestMissingDatabase(t *testing.T) {
	e := newProgEnv(t)
	o := e.opts()
	missing := filepath.Join(e.root, "nope.db")
	o.JellyfinDB, o.KavitaDB, o.ABSDB = missing, missing, missing
	want := []string{"Jellyfin не найден", "Kavita не найден", "Audiobookshelf не найден"}
	for i, f := range []importFunc{ImportJellyfin, ImportKavita, ImportABS} {
		l := onlyLine(t, e.run(f, o))
		if l.Status != StatusSkipped || l.Reason != want[i] {
			t.Fatalf("line %+v", l)
		}
	}
}

func TestProposeUsersByName(t *testing.T) {
	e := newProgEnv(t)
	_, m := e.interstellar()
	o := e.opts()
	o.JellyfinDB = jellyfinDB(t, append(jfUsers, jfUser{"U3", "Пустой"}), []jfItem{m}, []jfUserData{
		{ItemID: "M1", UserID: "U1", PosTicks: 10, LastPlayed: jfT1},
		{ItemID: "M1", UserID: "U1", Key: "fav", Favorite: true},
		{ItemID: "M1", UserID: "U2", Played: true},
	})
	o.KavitaDB = kavitaDB(t, []kvUser{{7, "ALEX"}}, []kvChapter{{1, 10, "/books/manga/x.cbz"}}, []kvProgress{{7, 1, 3, jfT1}})
	o.ABSDB = filepath.Join(e.root, "nope.db")
	got, err := ProposeUsers(context.Background(), o, e.users)
	if err != nil {
		t.Fatal(err)
	}
	uid := e.uid
	want := []UserProposal{
		{Source: SourceJellyfin, SourceID: "U1", SourceName: "Alex", VaultUserID: &uid, Rows: 2},
		{Source: SourceJellyfin, SourceID: "U2", SourceName: "Гость", Rows: 1},
		{Source: SourceKavita, SourceID: "7", SourceName: "ALEX", VaultUserID: &uid, Rows: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

// Cyrillic case folds; a name two profiles share byte-insensitively
// matches neither; an unreadable database is a row with Error.
func TestProposeUsersUnicodeAmbiguousUnreadable(t *testing.T) {
	e := newProgEnv(t)
	ctx := context.Background()
	petya, err := e.users.CreateUser(ctx, auth.NewUser{Name: "Петя"})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Саша", "саша"} {
		if _, err := e.users.CreateUser(ctx, auth.NewUser{Name: n}); err != nil {
			t.Fatal(err)
		}
	}
	_, m := e.interstellar()
	o := e.opts()
	o.JellyfinDB = jellyfinDB(t, []jfUser{{"P", "ПЕТЯ"}, {"S", "Саша"}}, []jfItem{m}, []jfUserData{
		{ItemID: "M1", UserID: "P", Played: true},
		{ItemID: "M1", UserID: "S", Played: true},
	})
	o.KavitaDB = filepath.Join(e.root, "kavita.db")
	if err := os.WriteFile(o.KavitaDB, []byte("not a database, just text long enough to be read"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ProposeUsers(ctx, o, e.users)
	if err != nil {
		t.Fatal(err)
	}
	pid := petya.ID
	want := []UserProposal{
		{Source: SourceJellyfin, SourceID: "P", SourceName: "ПЕТЯ", VaultUserID: &pid, Rows: 1},
		{Source: SourceJellyfin, SourceID: "S", SourceName: "Саша", Rows: 1},
		{Source: SourceKavita, SourceName: "Kavita", Error: "Не удалось прочитать базу Kavita"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestProgressSourcesUnchanged(t *testing.T) {
	e := newProgEnv(t)
	_, m := e.interstellar()
	o := e.jfOpts(jellyfinDB(t, jfUsers, []jfItem{m}, []jfUserData{{ItemID: "M1", UserID: "U1", PosTicks: 10, LastPlayed: jfT1, Favorite: true}}))
	o.KavitaDB = kavitaDB(t, []kvUser{{1, "Alex"}}, []kvChapter{{1, 10, "/books/manga/x.cbz"}}, []kvProgress{{1, 1, 3, jfT1}})
	o.ABSDB = absDB(t, []absUser{{"u-1", "alex"}}, []absItem{{"b-1", "/ebooks/x.epub", "x"}},
		[]absProgress{{UserID: "u-1", MediaID: "b-1", EbookLoc: "3", Updated: "2026-10-01 19:30:00.000 +00:00"}})
	o.UserMap["kavita:1"], o.UserMap["audiobookshelf:u-1"] = e.uid, e.uid
	dirs := []string{filepath.Dir(o.JellyfinDB), filepath.Dir(o.KavitaDB), filepath.Dir(o.ABSDB)}
	var before []string
	for _, d := range dirs {
		before = append(before, treeHash(t, d))
	}
	for _, f := range []importFunc{ImportJellyfin, ImportKavita, ImportABS} {
		e.run(f, o)
	}
	if _, err := ProposeUsers(context.Background(), o, e.users); err != nil {
		t.Fatal(err)
	}
	for i, d := range dirs {
		if h := treeHash(t, d); h != before[i] {
			t.Fatalf("%s changed", d)
		}
	}
}
