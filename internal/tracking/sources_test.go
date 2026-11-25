package tracking

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

type fakeTMDB struct {
	tv, movie tmdb.Details
	seasons   map[int]tmdb.SeasonDetails
	err       error
	delay     time.Duration // each season takes this long

	mu       sync.Mutex
	asked    []int
	inFlight int
	most     int // the most seasons asked at once
}

func (f *fakeTMDB) TV(context.Context, int) (tmdb.Details, error)    { return f.tv, f.err }
func (f *fakeTMDB) Movie(context.Context, int) (tmdb.Details, error) { return f.movie, f.err }
func (f *fakeTMDB) Season(_ context.Context, _ int, n int) (tmdb.SeasonDetails, error) {
	f.mu.Lock()
	f.asked = append(f.asked, n)
	f.inFlight++
	f.most = max(f.most, f.inFlight)
	f.mu.Unlock()
	time.Sleep(f.delay)
	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	s, ok := f.seasons[n]
	if !ok {
		return tmdb.SeasonDetails{}, errors.New("no season")
	}
	return s, nil
}

func TestTMDBSkeletonSeries(t *testing.T) {
	f := &fakeTMDB{
		tv: tmdb.Details{Seasons: []tmdb.SeasonCount{{Number: 0, Episodes: 1}, {Number: 1, Episodes: 2}, {Number: 2, Episodes: 1}}},
		seasons: map[int]tmdb.SeasonDetails{
			1: {Number: 1, Episodes: []tmdb.Episode{{Number: 1, Name: "A", AirDate: "2026-01-01", Still: "/a.jpg"}, {Number: 2, Name: "B", AirDate: "2026-01-08"}}},
			2: {Number: 2, Episodes: []tmdb.Episode{{Number: 1, Name: "C", AirDate: "2026-04-01"}}},
		},
	}
	sk := NewTMDBSkeleton(f)
	sk.(*tmdbSkeleton).now = func() time.Time { return tnow }
	eps, status, err := sk.Episodes(context.Background(), Title{ID: 5, Kind: "series", TMDBID: 9})
	if err != nil {
		t.Fatal(err)
	}
	want := []Episode{{5, 1, 1, "A", "2026-01-01", "/a.jpg"}, {5, 1, 2, "B", "2026-01-08", ""}, {5, 2, 1, "C", "2026-04-01", ""}}
	slices.Sort(f.asked)
	if !reflect.DeepEqual(eps, want) || status != "airing" || !reflect.DeepEqual(f.asked, []int{1, 2}) {
		t.Fatalf("eps %+v status %q asked %v", eps, status, f.asked)
	}
}

// TestTMDBSkeletonSeasonsAreAskedTogether: a long series is not read one
// season after another (an untracked title's page waits for this), and the
// episodes still come in season order.
func TestTMDBSkeletonSeasonsAreAskedTogether(t *testing.T) {
	f := &fakeTMDB{seasons: map[int]tmdb.SeasonDetails{}, delay: 20 * time.Millisecond}
	var want []Episode
	for n := 0; n <= 10; n++ {
		f.tv.Seasons = append(f.tv.Seasons, tmdb.SeasonCount{Number: n, Episodes: 2})
		f.seasons[n] = tmdb.SeasonDetails{Number: n, Episodes: []tmdb.Episode{{Number: 1, Name: "A"}, {Number: 2, Name: "B"}}}
		if n > 0 {
			want = append(want, Episode{TitleID: 5, Season: n, Number: 1, Name: "A"}, Episode{TitleID: 5, Season: n, Number: 2, Name: "B"})
		}
	}
	eps, _, err := NewTMDBSkeleton(f).Episodes(context.Background(), Title{ID: 5, Kind: "series", TMDBID: 9})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(eps, want) {
		t.Fatalf("episodes %+v", eps)
	}
	if f.most < 2 || f.most > seasonsAtOnce {
		t.Fatalf("%d seasons asked at once, want 2..%d", f.most, seasonsAtOnce)
	}
	delete(f.seasons, 7)
	if _, _, err := NewTMDBSkeleton(f).Episodes(context.Background(), Title{ID: 5, Kind: "series", TMDBID: 9}); err == nil {
		t.Fatal("a season that cannot be read left the list incomplete without an error")
	}
}

func TestSeriesStatus(t *testing.T) {
	cases := []struct {
		dates []string
		want  string
	}{
		{nil, "unknown"},
		{[]string{"", ""}, "unknown"},
		{[]string{"2026-04-01"}, "upcoming"},
		{[]string{"2026-02-01", "2026-04-01"}, "airing"},
		{[]string{"2026-02-01", "2026-03-05"}, "airing"}, // last aired within two weeks
		{[]string{"2025-02-01", "2025-03-05"}, "ended"},
	}
	for _, c := range cases {
		var eps []Episode
		for _, d := range c.dates {
			eps = append(eps, Episode{AirDate: d})
		}
		if got := seriesStatus(eps, tnow); got != c.want {
			t.Errorf("%v: %q, want %q", c.dates, got, c.want)
		}
	}
}

func TestTMDBSkeletonMovie(t *testing.T) {
	f := &fakeTMDB{movie: tmdb.Details{ReleaseDate: "2026-05-01"}}
	sk := NewTMDBSkeleton(f)
	sk.(*tmdbSkeleton).now = func() time.Time { return tnow }
	eps, status, err := sk.Episodes(context.Background(), Title{ID: 3, Kind: "movie", TMDBID: 9})
	if err != nil || status != "upcoming" || !reflect.DeepEqual(eps, []Episode{{TitleID: 3, AirDate: "2026-05-01"}}) {
		t.Fatalf("%+v %q %v", eps, status, err)
	}
	f.movie.ReleaseDate = "2025-01-01"
	if _, status, _ = sk.Episodes(context.Background(), Title{Kind: "movie"}); status != "ended" {
		t.Fatal(status)
	}
	f.err = errors.New("down")
	if _, _, err := sk.Episodes(context.Background(), Title{Kind: "movie"}); err == nil {
		t.Fatal("error lost")
	}
}

type fakeLibrary struct {
	items map[string]library.Item
	files map[[2]int]library.EpisodeFile
	movie []library.File
}

func (f *fakeLibrary) ItemByTMDB(_ context.Context, kind string, _ int64) (library.Item, error) {
	it, ok := f.items[kind]
	if !ok {
		return library.Item{}, library.ErrNotFound
	}
	return it, nil
}

func (f *fakeLibrary) EpisodeFiles(context.Context, int64) (map[[2]int]library.EpisodeFile, error) {
	return f.files, nil
}

func (f *fakeLibrary) FilesOf(context.Context, int64) ([]library.File, error) { return f.movie, nil }

func TestLibraryScanner(t *testing.T) {
	ctx := context.Background()
	f := &fakeLibrary{items: map[string]library.Item{"series": {ID: 11}, "movie": {ID: 12}},
		files: map[[2]int]library.EpisodeFile{{1, 2}: {ItemID: 22, FileID: 32, Resolution: 720}, {1, 1}: {ItemID: 21, FileID: 31, Resolution: 1080}},
		movie: []library.File{{ID: 40, ProbeJSON: `{"Video":{"Width":3840,"Height":2160}}`}}}
	sc := NewLibraryScanner(f)
	if sc.Kind() != "library" {
		t.Fatal(sc.Kind())
	}
	got, err := sc.Scan(ctx, Title{Kind: "series", TMDBID: 1}, nil)
	want := []Source{
		{Season: 1, Episode: 1, Kind: "library", Ref: "item:21", Quality: "1080p", Meta: SourceMeta{ItemID: 21, FileID: 31}},
		{Season: 1, Episode: 2, Kind: "library", Ref: "item:22", Quality: "720p", Meta: SourceMeta{ItemID: 22, FileID: 32}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("series %+v %v", got, err)
	}
	got, err = sc.Scan(ctx, Title{Kind: "movie", TMDBID: 1}, nil)
	if err != nil || !reflect.DeepEqual(got, []Source{{Kind: "library", Ref: "item:12", Quality: "2160p", Meta: SourceMeta{ItemID: 12, FileID: 40}}}) {
		t.Fatalf("movie %+v %v", got, err)
	}
	f.items = nil
	if got, err = sc.Scan(ctx, Title{Kind: "series", TMDBID: 1}, nil); err != nil || len(got) != 0 {
		t.Fatalf("absent %+v %v", got, err)
	}
}
