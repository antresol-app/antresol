package tracking

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// feedFixture: a series marked watching by u with two episodes that got a
// source at different times, one that never did, and an old one.
func feedFixture(t *testing.T) (*Store, int64, Title) {
	t.Helper()
	s, ctx := newStore(t), context.Background()
	u := addUser(t, s, "one")
	ti, _ := s.EnsureTitle(ctx, "series", 7, "Show", "/p.jpg")
	s.SetMark(ctx, Mark{UserID: u, TitleID: ti.ID, State: Watching})
	if err := s.ReplaceEpisodes(ctx, ti.ID, []Episode{
		{Season: 1, Number: 1, Name: "Old", AirDate: "2020-01-01"},
		{Season: 2, Number: 1, Name: "One", AirDate: "2026-03-01"},
		{Season: 2, Number: 2, Name: "Two", AirDate: "2026-03-08"},
		{Season: 2, Number: 3, Name: "Three", AirDate: "2026-03-15"},
	}); err != nil {
		t.Fatal(err)
	}
	day := func(d int) time.Time { return time.Date(2026, 3, d, 10, 0, 0, 0, time.UTC) }
	s.UpsertSources(ctx, ti.ID, "torrent", []Source{{Season: 1, Episode: 1, Ref: "a"}, {Season: 2, Episode: 1, Ref: "b"}}, day(2))
	s.UpsertSources(ctx, ti.ID, "library", []Source{{Season: 2, Episode: 1, Ref: "9"}}, day(9))
	s.UpsertSources(ctx, ti.ID, "online", []Source{{Season: 2, Episode: 2, Ref: "c"}}, day(9))
	return s, u, ti
}

func TestFeedNewestFirstWithKinds(t *testing.T) {
	s, u, _ := feedFixture(t)
	got, err := s.Feed(context.Background(), u, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), nil)
	if err != nil {
		t.Fatal(err)
	}
	// S1E1 aired years ago: its first source is the baseline, not news
	if len(got) != 2 || got[0].Episode != 2 || got[1].Episode != 1 {
		t.Fatalf("%+v", got)
	}
	if got[0].Name != "Two" || got[0].Title.Name != "Show" || !got[1].FirstSeen.Equal(time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", got)
	}
	if !slices.Equal(got[1].Kinds, []string{"library", "torrent"}) {
		t.Fatalf("kinds %v", got[1].Kinds)
	}
	// since after S2E1's first source: only S2E2
	got, _ = s.Feed(context.Background(), u, time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), nil)
	if len(got) != 1 || got[0].Episode != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestFeedExcludesWatched(t *testing.T) {
	s, u, ti := feedFixture(t)
	w := func(_ context.Context, uid int64, x Title) (map[[2]int]bool, error) {
		if uid != u || x.ID != ti.ID {
			t.Fatalf("watched asked for %d %+v", uid, x)
		}
		return map[[2]int]bool{{2, 2}: true}, nil
	}
	got, err := s.Feed(context.Background(), u, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), w)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Episode != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestFeedOnlyOwnLiveMarks(t *testing.T) {
	s, u, ti := feedFixture(t)
	other := addUser(t, s, "two")
	since := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if got, _ := s.Feed(context.Background(), other, since, nil); len(got) != 0 {
		t.Fatalf("other profile %+v", got)
	}
	s.SetMark(context.Background(), Mark{UserID: u, TitleID: ti.ID, State: Dropped})
	if got, _ := s.Feed(context.Background(), u, since, nil); len(got) != 0 {
		t.Fatalf("dropped %+v", got)
	}
}

func TestCalendarRange(t *testing.T) {
	s, u, _ := feedFixture(t)
	days, err := s.Calendar(context.Background(), u, "2026-03-01", "2026-03-15")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 3 || days[0].Date != "2026-03-01" || days[2].Date != "2026-03-15" {
		t.Fatalf("%+v", days)
	}
	if it := days[0].Items[0]; it.FirstSeen.IsZero() || it.Name != "One" || !slices.Equal(it.Kinds, []string{"library", "torrent"}) {
		t.Fatalf("%+v", it)
	}
	if it := days[2].Items[0]; !it.FirstSeen.IsZero() || it.Episode != 3 || it.Kinds == nil {
		t.Fatalf("not yet available must have zero FirstSeen: %+v", it)
	}
	days, _ = s.Calendar(context.Background(), u, "2026-03-02", "2026-03-07")
	if len(days) != 0 {
		t.Fatalf("%+v", days)
	}
}

func TestSetInfoKeepsNamesFillsCert(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a, _ := s.EnsureTitle(ctx, "movie", 1, "", "")
	b, _ := s.EnsureTitle(ctx, "movie", 2, "Mine", "/m.jpg")
	s.SetInfo(ctx, a.ID, TitleInfo{Name: "TMDB", Poster: "/t.jpg", Cert: "16+"})
	s.SetInfo(ctx, b.ID, TitleInfo{Name: "TMDB", Poster: "/t.jpg", Cert: "12+"})
	s.SetInfo(ctx, b.ID, TitleInfo{})
	a, _, _ = s.TitleByID(ctx, a.ID)
	b, _, _ = s.TitleByID(ctx, b.ID)
	if a.Name != "TMDB" || a.Poster != "/t.jpg" || a.Cert != "16+" {
		t.Fatalf("%+v", a)
	}
	if b.Name != "Mine" || b.Poster != "/m.jpg" || b.Cert != "12+" {
		t.Fatalf("%+v", b)
	}
}

type infoSkeleton struct {
	fakeSkeleton
	info TitleInfo
}

func (f *infoSkeleton) EpisodesInfo(ctx context.Context, t Title) ([]Episode, string, TitleInfo, error) {
	eps, st, err := f.Episodes(ctx, t)
	return eps, st, f.info, err
}

func TestRefreshStoresInfo(t *testing.T) {
	sk := &infoSkeleton{fakeSkeleton: fakeSkeleton{status: "ended"}, info: TitleInfo{Name: "N", Cert: "18+"}}
	svc, _, _ := newService(t, sk)
	ti, _ := svc.Store.EnsureTitle(context.Background(), "movie", 5, "", "")
	if err := svc.Refresh(context.Background(), ti.ID); err != nil {
		t.Fatal(err)
	}
	ti, _, _ = svc.Store.TitleByID(context.Background(), ti.ID)
	if ti.Name != "N" || ti.Cert != "18+" {
		t.Fatalf("%+v", ti)
	}
}

type fakeWatchLib struct{ items map[int64][]library.Item }

func (f fakeWatchLib) ItemByTMDB(_ context.Context, kind string, tmdbID int64) (library.Item, error) {
	if kind == library.KindSeries && tmdbID == 7 {
		return library.Item{ID: 1, Kind: kind}, nil
	}
	if kind == library.KindMovie && tmdbID == 8 {
		return library.Item{ID: 50, Kind: kind}, nil
	}
	return library.Item{}, library.ErrNotFound
}

func (f fakeWatchLib) Children(_ context.Context, id int64) ([]library.Item, error) {
	return f.items[id], nil
}

type fakeWatchProg struct{ done map[int64]bool }

func (f fakeWatchProg) WatchedOf(_ context.Context, _ int64, items, _ []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	for _, id := range items {
		if f.done[id] {
			out[id] = true
		}
	}
	return out, nil
}

func TestLibraryWatched(t *testing.T) {
	lib := fakeWatchLib{items: map[int64][]library.Item{
		1: {{ID: 2, Kind: library.KindSeason, SeasonNo: 1}},
		2: {{ID: 3, Kind: library.KindEpisode, EpisodeNo: 1}, {ID: 4, Kind: library.KindEpisode, EpisodeNo: 2}},
	}}
	w := LibraryWatched(lib, fakeWatchProg{done: map[int64]bool{4: true, 50: true}})
	got, err := w(context.Background(), 1, Title{Kind: "series", TMDBID: 7})
	if err != nil || len(got) != 1 || !got[[2]int{1, 2}] {
		t.Fatalf("%v %v", got, err)
	}
	got, _ = w(context.Background(), 1, Title{Kind: "movie", TMDBID: 8})
	if !got[[2]int{0, 0}] {
		t.Fatalf("movie %v", got)
	}
	got, err = w(context.Background(), 1, Title{Kind: "series", TMDBID: 99})
	if err != nil || len(got) != 0 {
		t.Fatalf("absent %v %v", got, err)
	}
}

func TestServiceWatchedUnion(t *testing.T) {
	svc := &Service{Watched: []WatchedFunc{
		func(context.Context, int64, Title) (map[[2]int]bool, error) {
			return map[[2]int]bool{{1, 1}: true}, nil
		},
		func(context.Context, int64, Title) (map[[2]int]bool, error) {
			return map[[2]int]bool{{1, 2}: true}, nil
		},
	}}
	got, err := svc.WatchedBy()(context.Background(), 1, Title{})
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
}

// A library title on a profile's watchlist reads as «Хочу посмотреть»
// (planned) without a mark row: it is tracked like one (its title row made
// by EnsureWatchlistTitles), the profile hears about it and sees it in the
// feed and calendar. A mark of its own wins (dropped: not tracked for it).
func TestWatchlistedLibraryTitleIsTracked(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	u := addUser(t, s, "one")
	other := addUser(t, s, "two")
	r, err := s.DB.Exec(`INSERT INTO items(kind, title, tmdb_id) VALUES('series', 'Show', 55)`)
	if err != nil {
		t.Fatal(err)
	}
	item, _ := r.LastInsertId()
	s.DB.Exec(`INSERT INTO items(kind, title, tmdb_id) VALUES('series', 'No TMDB', NULL)`)
	for _, uid := range []int64{u, other} {
		if _, err := s.DB.Exec(`INSERT INTO watchlist(user_id, item_id, added_at) VALUES(?, ?, '2026-01-01T00:00:00Z')`, uid, item); err != nil {
			t.Fatal(err)
		}
	}
	s.DB.Exec(`INSERT INTO watchlist(user_id, item_id, added_at) VALUES(?, ?, '2026-01-01T00:00:00Z')`, u, item+1)
	if err := s.EnsureWatchlistTitles(ctx); err != nil {
		t.Fatal(err)
	}
	ti, ok, err := s.TitleByTMDB(ctx, "series", 55)
	if err != nil || !ok || ti.Name != "Show" {
		t.Fatalf("title %+v %v %v", ti, ok, err)
	}
	if n := 0; s.DB.QueryRow(`SELECT count(*) FROM tracked_titles`).Scan(&n) == nil && n != 1 {
		t.Fatalf("%d titles", n)
	}
	tracked, _ := s.Tracked(ctx)
	due, _ := s.Due(ctx, time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC), 10)
	if len(tracked) != 1 || tracked[0].ID != ti.ID || len(due) != 1 {
		t.Fatalf("tracked %+v due %+v", tracked, due)
	}
	s.SetMark(ctx, Mark{UserID: other, TitleID: ti.ID, State: Dropped})
	if got, _ := s.Interested(ctx, ti.ID); !slices.Equal(got, []int64{u}) {
		t.Fatalf("interested %v", got)
	}
	s.ReplaceEpisodes(ctx, ti.ID, []Episode{{Season: 1, Number: 1, AirDate: "2026-03-01"}})
	s.UpsertSources(ctx, ti.ID, "torrent", []Source{{Season: 1, Episode: 1, Ref: "a"}}, time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC))
	since := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if feed, err := s.Feed(ctx, u, since, nil); err != nil || len(feed) != 1 {
		t.Fatalf("feed %+v %v", feed, err)
	}
	if feed, _ := s.Feed(ctx, other, since, nil); len(feed) != 0 {
		t.Fatalf("dropped profile's feed %+v", feed)
	}
	if cal, err := s.Calendar(ctx, u, "2026-03-01", "2026-03-31"); err != nil || len(cal) != 1 {
		t.Fatalf("calendar %+v %v", cal, err)
	}
	// both off the list: nothing tracked
	s.DB.Exec(`DELETE FROM watchlist`)
	if tracked, _ := s.Tracked(ctx); len(tracked) != 0 {
		t.Fatalf("tracked %+v", tracked)
	}
}
