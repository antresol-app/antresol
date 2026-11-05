package progress

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
)

type env struct {
	t     *testing.T
	s     *Store
	repo  *library.Repo
	users *auth.Store
	clock time.Time
	n     int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &env{t: t, s: New(d), repo: library.NewRepo(d), users: auth.NewStore(d), clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	e.s.Now = func() time.Time { e.clock = e.clock.Add(time.Second); return e.clock }
	return e
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) user(name string) int64 {
	u, err := e.users.CreateUser(context.Background(), auth.NewUser{Name: name})
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

func (e *env) movie(title string) int64 {
	e.n++
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: title, File: library.FileInput{Path: "/m/" + title + ".mkv"}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) ep(series string, s, n int) int64 {
	id, err := e.repo.UpsertEpisode(context.Background(), library.EpisodeInput{SeriesTitle: series, Season: s, Episode: n,
		File: library.FileInput{Path: "/s/" + series + "/" + string(rune('0'+s)) + string(rune('0'+n)) + ".mkv"}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) seriesOf(ep int64) int64 {
	it, _ := e.repo.Item(context.Background(), ep)
	se, _ := e.repo.Item(context.Background(), it.ParentID)
	return se.ParentID
}

var ctx = context.Background()

func TestFinishedAt92Percent(t *testing.T) {
	e := newEnv(t)
	u, m := e.user("a"), e.movie("Film")
	e.must(e.s.Save(ctx, u, m, 91_000, 100_000))
	if g, _ := e.s.Get(ctx, u, m); g.Finished {
		t.Fatal("91% must not be finished")
	}
	e.must(e.s.Save(ctx, u, m, 92_000, 100_000))
	g, err := e.s.Get(ctx, u, m)
	if err != nil || !g.Finished || g.PositionMs != 92_000 {
		t.Fatalf("%+v %v", g, err)
	}
	if _, err := e.s.Get(ctx, u, 9999); err != ErrNotFound {
		t.Fatal(err)
	}
}

func TestListenFinishedAtTheRealEnd(t *testing.T) {
	e := newEnv(t)
	u, m := e.user("a"), e.movie("Книга")
	const hour = 3_600_000
	cases := []struct {
		pos, dur int64
		fin      bool
	}{
		{185 * hour / 10, 20 * hour, false}, // 92.5%: an hour and a half still unheard
		{196 * hour / 10, 20 * hour, true},  // 98%
		{570_000, 600_000, true},            // a 10-minute book: its last 30 s (95%)
		{569_000, 600_000, false},
		{20 * hour, 20 * hour, true},
		{1_000, 0, false},
	}
	clock := time.Now()
	e.s.Now = func() time.Time { return clock }
	for _, c := range cases {
		clock = clock.Add(time.Minute) // each save newer than the last
		e.must(e.s.SaveListenAt(ctx, u, m, c.pos, c.dur, time.Time{}))
		if g, _ := e.s.Get(ctx, u, m); g.Finished != c.fin {
			t.Fatalf("%d of %d: finished %v, want %v", c.pos, c.dur, g.Finished, c.fin)
		}
	}
}

func TestContinueWatchingOnePerSeries(t *testing.T) {
	e := newEnv(t)
	u := e.user("a")
	e1, e2, e3 := e.ep("Show", 1, 1), e.ep("Show", 1, 2), e.ep("Show", 1, 3)
	m := e.movie("Film")
	done := e.movie("Done")
	e.must(e.s.Save(ctx, u, done, 99, 100))
	e.must(e.s.Save(ctx, u, e1, 10, 100))
	e.must(e.s.Save(ctx, u, e2, 20, 100))
	e.must(e.s.Save(ctx, u, m, 30, 100))
	_ = e3
	got, err := e.s.ContinueWatching(ctx, u, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].ItemID != m || got[1].ItemID != e2 || got[1].SeriesID != e.seriesOf(e2) || got[1].PositionMs != 20 {
		t.Fatalf("%+v", got)
	}
	if lim, _ := e.s.ContinueWatching(ctx, u, 1); len(lim) != 1 {
		t.Fatal("limit")
	}
}

func TestNextEpisodeAfterFinished(t *testing.T) {
	e := newEnv(t)
	u := e.user("a")
	e1, e2 := e.ep("Show", 1, 1), e.ep("Show", 1, 2)
	series := e.seriesOf(e1)
	e.must(e.s.Save(ctx, u, e1, 99, 100))
	got, _ := e.s.ContinueWatching(ctx, u, 10)
	if len(got) != 1 || got[0].ItemID != e2 || got[0].PositionMs != 0 || got[0].SeriesID != series || got[0].UpdatedAt.IsZero() {
		t.Fatalf("%+v", got)
	}
	if id, ok, _ := e.s.NextEpisode(ctx, u, series); !ok || id != e2 {
		t.Fatal(id, ok)
	}
	e.must(e.s.Save(ctx, u, e2, 99, 100))
	if got, _ := e.s.ContinueWatching(ctx, u, 10); len(got) != 0 {
		t.Fatalf("all watched: %+v", got)
	}
	if _, ok, _ := e.s.NextEpisode(ctx, u, series); ok {
		t.Fatal("expected none")
	}
}

func TestNextEpisodeOrderAndSpecials(t *testing.T) {
	e := newEnv(t)
	u := e.user("a")
	sp := e.ep("Show", 0, 1)
	s2e1, s1e2, s1e1 := e.ep("Show", 2, 1), e.ep("Show", 1, 2), e.ep("Show", 1, 1)
	series := e.seriesOf(sp)
	if id, _, _ := e.s.NextEpisode(ctx, u, series); id != s1e1 {
		t.Fatal("fresh: first regular episode")
	}
	e.must(e.s.MarkWatched(ctx, u, s1e1, true))
	e.must(e.s.MarkWatched(ctx, u, s1e2, true))
	if id, _, _ := e.s.NextEpisode(ctx, u, series); id != s2e1 {
		t.Fatal("season rollover")
	}
	e.must(e.s.MarkWatched(ctx, u, s2e1, true))
	if id, ok, _ := e.s.NextEpisode(ctx, u, series); !ok || id != sp {
		t.Fatal("only specials remain")
	}
	// unwatching clears
	e.must(e.s.MarkWatched(ctx, u, s1e1, false))
	if _, err := e.s.Get(ctx, u, s1e1); err != ErrNotFound {
		t.Fatal(err)
	}
}

func TestProgressIsolatedPerUser(t *testing.T) {
	e := newEnv(t)
	a, b := e.user("a"), e.user("b")
	m := e.movie("Film")
	e.must(e.s.Save(ctx, a, m, 50, 100))
	if _, err := e.s.Get(ctx, b, m); err != ErrNotFound {
		t.Fatal(err)
	}
	if got, _ := e.s.ContinueWatching(ctx, b, 10); len(got) != 0 {
		t.Fatal(got)
	}
	e.must(e.s.AddToWatchlist(ctx, a, m))
	e.must(e.s.AddToWatchlist(ctx, a, m))
	if w, _ := e.s.Watchlist(ctx, a); len(w) != 1 || w[0] != m {
		t.Fatal(w)
	}
	if w, _ := e.s.Watchlist(ctx, b); len(w) != 0 {
		t.Fatal(w)
	}
	e.must(e.s.RemoveFromWatchlist(ctx, a, m))
	if w, _ := e.s.Watchlist(ctx, a); len(w) != 0 {
		t.Fatal(w)
	}
}

func TestNextEpisodeGapBeforeSpecial(t *testing.T) {
	e := newEnv(t)
	u := e.user("a")
	s1e1, s1e2, s2e1, sp := e.ep("Show", 1, 1), e.ep("Show", 1, 2), e.ep("Show", 2, 1), e.ep("Show", 0, 1)
	e.must(e.s.MarkWatched(ctx, u, s1e2, true))
	e.must(e.s.MarkWatched(ctx, u, s2e1, true))
	if id, ok, _ := e.s.NextEpisode(ctx, u, e.seriesOf(sp)); !ok || id != s1e1 {
		t.Fatalf("want gap episode %d, got %d", s1e1, id)
	}
}

func TestCascadeOnItemDelete(t *testing.T) {
	e := newEnv(t)
	a := e.user("a")
	m := e.movie("Film")
	e.must(e.s.Save(ctx, a, m, 50, 100))
	e.must(e.s.AddToWatchlist(ctx, a, m))
	if _, err := e.s.db.ExecContext(ctx, `DELETE FROM items WHERE id=?`, m); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Get(ctx, a, m); err != ErrNotFound {
		t.Fatal(err)
	}
	if w, _ := e.s.Watchlist(ctx, a); len(w) != 0 {
		t.Fatal(w)
	}
}

func TestCascadeOnUserDelete(t *testing.T) {
	e := newEnv(t)
	a, _ := e.user("a"), e.user("keep")
	m := e.movie("Film")
	e.must(e.s.Save(ctx, a, m, 50, 100))
	e.must(e.s.AddToWatchlist(ctx, a, m))
	if err := e.users.Delete(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Get(ctx, a, m); err != ErrNotFound {
		t.Fatal(err)
	}
}

// F8: an item whose file is in the trash keeps its progress but is not
// offered in «Продолжить»; the restore brings it back.
func TestContinueWatchingHidesMissing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("a")
	m := e.movie("Брат")
	e.must(e.s.Save(ctx, u, m, 60_000, 6_000_000))
	e.must(e.repo.RemoveMissingFiles(ctx, "/m", map[string]bool{}))
	got, err := e.s.ContinueWatching(ctx, u, 10)
	e.must(err)
	if len(got) != 0 {
		t.Fatalf("missing item offered: %+v", got)
	}
	e.movie("Брат") // the file is back (same path)
	got, err = e.s.ContinueWatching(ctx, u, 10)
	e.must(err)
	if len(got) != 1 || got[0].ItemID != m || got[0].PositionMs != 60_000 {
		t.Fatalf("after restore: %+v", got)
	}
}

func (e *env) book(title string) int64 {
	id, err := e.repo.UpsertBook(context.Background(), library.BookInput{Title: title,
		Files: []library.FileInput{{Path: "/b/" + title + "/" + title + ".epub"}}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestSaveLocatorRoundTripStore(t *testing.T) {
	e := newEnv(t)
	u, b := e.user("a"), e.book("Мастер")
	e.must(e.s.SaveLocator(ctx, u, b, "epubcfi(/6/4!/4/2)", 0.34, time.Time{}))
	g, err := e.s.Get(ctx, u, b)
	if err != nil || g.Locator != "epubcfi(/6/4!/4/2)" || g.PositionMs != 340_000 || g.DurationMs != 1_000_000 || g.Finished {
		t.Fatalf("%+v %v", g, err)
	}
	e.must(e.s.SaveLocator(ctx, u, b, "epubcfi(/6/40)", 0.98, time.Time{}))
	if g, _ := e.s.Get(ctx, u, b); !g.Finished {
		t.Fatalf("0.98 must be finished: %+v", g)
	}
}

func TestSaveAtIgnoresStale(t *testing.T) {
	e := newEnv(t)
	u, m := e.user("a"), e.movie("Film")
	t0 := time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC) // before the store clock's next tick
	e.must(e.s.Save(ctx, u, m, 5000, 10000))           // now = 00:00:0x
	e.must(e.s.SaveAt(ctx, u, m, 1000, 10000, t0.Add(-time.Hour)))
	if g, _ := e.s.Get(ctx, u, m); g.PositionMs != 5000 {
		t.Fatalf("stale write applied: %+v", g)
	}
	// a write from the future is clamped to now and still applies
	e.must(e.s.SaveAt(ctx, u, m, 7000, 10000, time.Now().Add(24*365*time.Hour)))
	if g, _ := e.s.Get(ctx, u, m); g.PositionMs != 7000 || g.UpdatedAt.After(e.clock) {
		t.Fatalf("future write: %+v", g)
	}
	e.must(e.s.Save(ctx, u, m, 8000, 10000))
	if g, _ := e.s.Get(ctx, u, m); g.PositionMs != 8000 {
		t.Fatalf("live write after a clamped one: %+v", g)
	}
}

func TestContinueWatchingVideoOnlyStore(t *testing.T) {
	e := newEnv(t)
	u, m, b := e.user("a"), e.movie("Film"), e.book("Мастер")
	e.must(e.s.Save(ctx, u, m, 10, 100))
	e.must(e.s.SaveLocator(ctx, u, b, "page:3", 0.1, time.Time{}))
	got, err := e.s.ContinueWatching(ctx, u, 10)
	if err != nil || len(got) != 1 || got[0].ItemID != m {
		t.Fatalf("%+v %v", got, err)
	}
	in, err := e.s.InProgress(ctx, u, []string{library.KindBook}, 10)
	if err != nil || len(in) != 1 || in[0].ItemID != b || in[0].Locator != "page:3" {
		t.Fatalf("in progress %+v %v", in, err)
	}
	b2 := e.book("Идиот")
	e.must(e.s.MarkWatched(ctx, u, b2, true))
	fin, err := e.s.FinishedOf(ctx, u, []string{library.KindBook}, 10)
	if err != nil || len(fin) != 1 || fin[0].ItemID != b2 {
		t.Fatalf("finished %+v %v", fin, err)
	}
}

// Every regular episode finished = the series is watched; specials do not count.
func TestSeriesWatched(t *testing.T) {
	e := newEnv(t)
	u := e.user("Папа")
	e1, e2 := e.ep("Сёгун", 1, 1), e.ep("Сёгун", 1, 2)
	e.ep("Сёгун", 0, 1)
	sid := e.seriesOf(e1)
	check := func(want bool) {
		t.Helper()
		if got, err := e.s.SeriesWatched(ctx, u, sid); err != nil || got != want {
			t.Fatalf("SeriesWatched = %v, %v; want %v", got, err, want)
		}
	}
	check(false)
	e.must(e.s.MarkWatched(ctx, u, e1, true))
	check(false)
	e.must(e.s.MarkWatched(ctx, u, e2, true))
	check(true)
	if got, _ := e.s.SeriesWatched(ctx, e.user("Мама"), sid); got {
		t.Fatal("another profile has not watched it")
	}
}

// WatchedOf answers SeriesWatched and Finished for many ids at once.
func TestWatchedOf(t *testing.T) {
	e := newEnv(t)
	u := e.user("Папа")
	a1, a2 := e.ep("Сёгун", 1, 1), e.ep("Сёгун", 1, 2)
	b1 := e.ep("Тьма", 1, 1)
	m1, m2 := e.movie("Дюна"), e.movie("Солярис")
	sa, sb := e.seriesOf(a1), e.seriesOf(b1)
	e.must(e.s.MarkWatched(ctx, u, a1, true))
	e.must(e.s.MarkWatched(ctx, u, a2, true))
	e.must(e.s.MarkWatched(ctx, u, m2, true))
	e.must(e.s.Save(ctx, u, m1, 1000, 100000))
	got, err := e.s.WatchedOf(ctx, u, []int64{m1, m2}, []int64{sa, sb})
	if err != nil || len(got) != 2 || !got[m2] || !got[sa] {
		t.Fatalf("WatchedOf = %v, %v", got, err)
	}
	if got, err := e.s.WatchedOf(ctx, u, nil, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v, %v", got, err)
	}
}

// «Снять отметку»: a tombstone with its own time orders every write.
func TestUnwatchedTombstone(t *testing.T) {
	e := newEnv(t)
	u, m := e.user("a"), e.movie("Film")
	base := e.clock
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	e.clock = at(100)

	// watched on the phone at 10, unmarked on the TV at 50
	e.must(e.s.SaveAt(ctx, u, m, 9500, 10000, at(10)))
	e.must(e.s.MarkWatchedAt(ctx, u, m, false, at(50)))
	if _, err := e.s.Get(ctx, u, m); err != ErrNotFound {
		t.Fatalf("unmarked row kept: %v", err)
	}
	if c, ok, err := e.s.ClearedAt(ctx, u, m); err != nil || !ok || !c.Equal(at(50)) {
		t.Fatalf("cleared at: %v %v %v", c, ok, err)
	}
	// the phone's offline queue comes back: a «finished» position of 40, a
	// position of 50 (a tie: the mark wins), a played report, an older mark
	e.must(e.s.SaveAt(ctx, u, m, 10000, 10000, at(40)))
	e.must(e.s.SaveAt(ctx, u, m, 3000, 10000, at(50)))
	e.must(e.s.PlayedAt(ctx, u, m, 9800, 10000, at(45)))
	e.must(e.s.MarkWatchedAt(ctx, u, m, true, at(30)))
	if g, err := e.s.Get(ctx, u, m); err != ErrNotFound {
		t.Fatalf("stale write resurrected the item: %+v %v", g, err)
	}
	if fin, _ := e.s.WatchedOf(ctx, u, []int64{m}, nil); fin[m] {
		t.Fatal("stale finished write marked it watched")
	}
	// a newer position starts it afresh
	e.must(e.s.SaveAt(ctx, u, m, 2000, 10000, at(60)))
	if g, _ := e.s.Get(ctx, u, m); g.PositionMs != 2000 || g.Finished {
		t.Fatalf("newer write: %+v", g)
	}
	// an unmark older than the stored row deletes nothing; the tombstone never moves back
	e.must(e.s.MarkWatchedAt(ctx, u, m, false, at(55)))
	if g, _ := e.s.Get(ctx, u, m); g.PositionMs != 2000 {
		t.Fatalf("older unmark deleted newer progress: %+v", g)
	}
	e.must(e.s.MarkWatchedAt(ctx, u, m, false, at(52)))
	if c, _, _ := e.s.ClearedAt(ctx, u, m); !c.Equal(at(55)) {
		t.Fatalf("tombstone %v", c)
	}
	// a newer watched mark wins; one at the same time as the unmark too
	e.must(e.s.MarkWatchedAt(ctx, u, m, true, at(70)))
	if g, _ := e.s.Get(ctx, u, m); !g.Finished {
		t.Fatalf("newer watched mark: %+v", g)
	}
	e.must(e.s.MarkWatchedAt(ctx, u, m, false, at(80)))
	e.must(e.s.MarkWatchedAt(ctx, u, m, true, at(80)))
	if g, _ := e.s.Get(ctx, u, m); !g.Finished {
		t.Fatalf("watched mark tied with the unmark: %+v", g)
	}
	// an unmark from the future is clamped to now: later live writes apply
	e.must(e.s.MarkWatchedAt(ctx, u, m, false, time.Now().Add(24*365*time.Hour)))
	e.must(e.s.Save(ctx, u, m, 4000, 10000))
	if g, _ := e.s.Get(ctx, u, m); g.PositionMs != 4000 {
		t.Fatalf("live write after a clamped unmark: %+v", g)
	}
	// another profile is not affected
	v := e.user("b")
	e.must(e.s.SaveAt(ctx, v, m, 9600, 10000, at(1)))
	if g, _ := e.s.Get(ctx, v, m); !g.Finished {
		t.Fatalf("other profile: %+v", g)
	}
}

func TestUnwatchedTombstoneCascade(t *testing.T) {
	e := newEnv(t)
	u, m := e.user("a"), e.movie("Film")
	e.must(e.s.MarkWatched(ctx, u, m, false))
	if _, err := e.s.db.Exec(`DELETE FROM items WHERE id=?`, m); err != nil {
		t.Fatal(err)
	}
	var n int
	e.must(e.s.db.QueryRow(`SELECT COUNT(*) FROM progress_cleared`).Scan(&n))
	if n != 0 {
		t.Fatalf("tombstone outlived its item: %d", n)
	}
}
