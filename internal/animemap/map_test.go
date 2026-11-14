package animemap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

const dataset = `[
 {"mal_id": 1, "anidb_id": 10, "themoviedb_id": 100, "type": "TV", "season": {"tmdb": 2}, "episode_offset": {"tmdb": 12}},
 {"mal_id": 2, "anidb_id": 11, "themoviedb_id": 200, "type": "MOVIE"},
 {"mal_id": 3, "anidb_id": 12, "type": "TV"},
 {"mal_id": 4, "themoviedb_id": "300", "type": "ONA"}
]`

func newMap(t *testing.T, url string) *Map {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return &Map{DB: d, URL: url}
}

func count(t *testing.T, m *Map) int {
	t.Helper()
	var n int
	if err := m.DB.QueryRow(`SELECT COUNT(*) FROM anime_map`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRefreshLoadsDataset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(dataset)) }))
	defer srv.Close()
	m := newMap(t, srv.URL)
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, m); n != 3 {
		t.Fatalf("rows = %d, want 3", n)
	}
	e, ok, err := m.Resolve(context.Background(), Title{MALID: 1})
	if err != nil || !ok {
		t.Fatalf("resolve: %v %v", ok, err)
	}
	want := Entry{MALID: 1, Kind: "series", TMDBID: 100, Season: 2, Offset: 12, AniDBID: 10}
	if e != want {
		t.Fatalf("got %+v want %+v", e, want)
	}
	e, _, _ = m.Resolve(context.Background(), Title{MALID: 2})
	if e.Kind != "movie" || e.TMDBID != 200 {
		t.Fatalf("movie entry %+v", e)
	}
}

// The published dataset keys the TMDB id by its kind: {"tv": n} or
// {"movie": [n, …]}; the key, not "type", says which (an OVA can be a TMDB
// movie, a MOVIE a TMDB series' special).
func TestRefreshReadsKeyedTMDBIDs(t *testing.T) {
	const keyed = `[
 {"type":"TV","anidb_id":1,"mal_id":290,"themoviedb_id":{"tv":26209},"tvdb_id":72025,"season":{"tvdb":1,"tmdb":1}},
 {"type":"MOVIE","anidb_id":7,"mal_id":164,"themoviedb_id":{"movie":[128]}},
 {"type":"OVA","anidb_id":11,"mal_id":821,"themoviedb_id":{"movie":[1390599, 5]},"season":{"tvdb":0},"episode_offset":{"tvdb":2}},
 {"type":"MOVIE","anidb_id":8,"mal_id":500,"themoviedb_id":{"tv":31},"season":{"tvdb":0,"tmdb":0},"episode_offset":{"tvdb":2,"tmdb":2}},
 {"anidb_id":9,"mal_id":501,"themoviedb_id":{"tv":32},"season":{"tmdb":3}},
 {"type":"TV","anidb_id":2,"mal_id":null,"themoviedb_id":{"tv":40}},
 {"type":"TV","anidb_id":3,"mal_id":502,"themoviedb_id":null}
]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(keyed)) }))
	defer srv.Close()
	m := newMap(t, srv.URL)
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, m); n != 5 {
		t.Fatalf("rows = %d, want 5", n)
	}
	for _, want := range []Entry{
		{MALID: 290, Kind: "series", TMDBID: 26209, Season: 1, AniDBID: 1},
		{MALID: 164, Kind: "movie", TMDBID: 128, Season: 1, AniDBID: 7},
		{MALID: 821, Kind: "movie", TMDBID: 1390599, Season: 1, AniDBID: 11},
		{MALID: 500, Kind: "series", TMDBID: 31, Season: 0, Offset: 2, AniDBID: 8},
		{MALID: 501, Kind: "series", TMDBID: 32, Season: 3, AniDBID: 9},
	} {
		e, ok, err := m.Resolve(context.Background(), Title{MALID: want.MALID})
		if err != nil || !ok || e != want {
			t.Fatalf("mal %d: got %+v %v %v, want %+v", want.MALID, e, ok, err, want)
		}
	}
}

func TestRefreshKeepsOldOnError(t *testing.T) {
	var mode atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 0:
			w.Write([]byte(dataset))
		case 1:
			http.Error(w, "boom", 500)
		default:
			w.Write([]byte(`[{"mal_id": 9, "themoviedb_id": 9, "type": "TV"}, {"mal_id": 8, "the`))
		}
	}))
	defer srv.Close()
	m := newMap(t, srv.URL)
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, md := range []int32{1, 2} {
		mode.Store(md)
		if err := m.Refresh(context.Background()); err == nil {
			t.Fatalf("mode %d: want error", md)
		}
		if n := count(t, m); n != 3 {
			t.Fatalf("mode %d: rows = %d, want 3", md, n)
		}
	}
}

func TestRefreshUsesETag(t *testing.T) {
	var full atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		full.Add(1)
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte(dataset))
	}))
	defer srv.Close()
	m := newMap(t, srv.URL)
	for i := 0; i < 2; i++ {
		if err := m.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if full.Load() != 1 {
		t.Fatalf("full downloads = %d, want 1", full.Load())
	}
	if n := count(t, m); n != 3 {
		t.Fatalf("rows = %d", n)
	}
}

type fakeSearch struct {
	tv, movie, calls int
	cands            []Candidate
	err              error
	years            []int
	queries          []string
}

func (f *fakeSearch) rec(q string, year int) ([]Candidate, error) {
	f.calls++
	f.queries = append(f.queries, q)
	f.years = append(f.years, year)
	if f.err != nil && f.calls == 1 {
		return nil, f.err
	}
	return f.cands, nil
}
func (f *fakeSearch) SearchTV(_ context.Context, q string, year int) ([]Candidate, error) {
	f.tv++
	return f.rec(q, year)
}
func (f *fakeSearch) SearchMovie(_ context.Context, q string, year int) ([]Candidate, error) {
	f.movie++
	return f.rec(q, year)
}

func hit(id int64) []Candidate {
	return []Candidate{{TMDBID: id, Name: "Some Show", Year: 2021, Animation: true, OriginalLanguage: "ja"}}
}

func TestResolveFallsBackToSearch(t *testing.T) {
	m := newMap(t, "")
	fs := &fakeSearch{cands: hit(555)}
	m.Search = fs
	in := Title{MALID: 77, Name: "Some Show!", Russian: "Шоу", Kind: "tv", AiredOn: "2021-04-03"}
	e, ok, err := m.Resolve(context.Background(), in)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if e.TMDBID != 555 || e.Kind != "series" || e.Season != 1 || fs.tv != 1 || fs.years[0] != 2021 || fs.queries[0] != "Some Show!" {
		t.Fatalf("%+v %+v", e, fs)
	}
	if count(t, m) != 1 {
		t.Fatal("not cached")
	}
	if _, ok, _ := m.Resolve(context.Background(), in); !ok || fs.calls != 1 {
		t.Fatalf("second call searched: calls=%d", fs.calls)
	}
}

func TestResolveSearchMissAndRussianFallback(t *testing.T) {
	m := newMap(t, "")
	fs := &fakeSearch{}
	m.Search = fs
	if _, ok, err := m.Resolve(context.Background(), Title{MALID: 5, Name: "A", Russian: "Б", Kind: "tv"}); ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if fs.calls != 2 || fs.queries[1] != "Б" || count(t, m) != 0 {
		t.Fatalf("%+v rows=%d", fs, count(t, m))
	}
}

func TestResolveSearchErrorStillTriesRussian(t *testing.T) {
	m := newMap(t, "")
	fs := &fakeSearch{cands: []Candidate{{TMDBID: 8, Name: "Шоу", Animation: true, OriginalLanguage: "ja"}}, err: errors.New("boom")}
	m.Search = fs
	e, ok, err := m.Resolve(context.Background(), Title{MALID: 5, Name: "A", Russian: "Шоу", Kind: "tv"})
	if err != nil || !ok || e.TMDBID != 8 || fs.calls != 2 {
		t.Fatalf("%+v %v %v calls=%d", e, ok, err, fs.calls)
	}
}

func TestResolveRejectsLiveActionAndMismatch(t *testing.T) {
	m := newMap(t, "")
	m.Search = &fakeSearch{cands: []Candidate{
		{TMDBID: 1, Name: "Some Show", Year: 2021, Animation: false, OriginalLanguage: "en"},
		{TMDBID: 2, Name: "Other", Year: 2010, Animation: true, OriginalLanguage: "ja"},
	}}
	if _, ok, err := m.Resolve(context.Background(), Title{MALID: 5, Name: "Some Show", Kind: "tv", AiredOn: "2021-01-01"}); ok || err != nil {
		t.Fatalf("accepted a bad candidate: %v %v", ok, err)
	}
	if count(t, m) != 0 {
		t.Fatal("cached a miss")
	}
}

func TestResolvePrefersAsianOriginal(t *testing.T) {
	m := newMap(t, "")
	m.Search = &fakeSearch{cands: []Candidate{
		{TMDBID: 1, Name: "Some Show", Animation: true, OriginalLanguage: "en"},
		{TMDBID: 2, Name: "Some Show", Animation: true, OriginalLanguage: "ja"},
	}}
	e, ok, _ := m.Resolve(context.Background(), Title{MALID: 5, Name: "Some Show", Kind: "tv"})
	if !ok || e.TMDBID != 2 {
		t.Fatalf("%+v", e)
	}
}

func TestResolveSequelIsMiss(t *testing.T) {
	for _, n := range []string{"Some Show 2", "Some Show Season 3", "Some Show 2nd Season", "Some Show II", "Шоу 2 сезон", "Шоу второй сезон"} {
		m := newMap(t, "")
		fs := &fakeSearch{cands: hit(1)}
		m.Search = fs
		if _, ok, _ := m.Resolve(context.Background(), Title{MALID: 5, Name: n, Kind: "tv"}); ok || fs.calls != 0 {
			t.Fatalf("%q: ok=%v calls=%d", n, ok, fs.calls)
		}
	}
	m := newMap(t, "")
	m.Search = &fakeSearch{cands: []Candidate{{TMDBID: 3, Name: "Mob Psycho 100", Animation: true}}}
	if _, ok, _ := m.Resolve(context.Background(), Title{MALID: 6, Name: "Mob Psycho 100", Kind: "tv"}); !ok {
		t.Fatal("100 is not a season marker")
	}
}

func TestResolveSearchRowExpires(t *testing.T) {
	m := newMap(t, "")
	fs := &fakeSearch{cands: hit(555)}
	m.Search = fs
	now := time.Now()
	m.Now = func() time.Time { return now }
	in := Title{MALID: 77, Name: "Some Show", Kind: "tv"}
	m.Resolve(context.Background(), in)
	now = now.Add(29 * 24 * time.Hour)
	m.Resolve(context.Background(), in)
	if fs.calls != 1 {
		t.Fatalf("searched before expiry: %d", fs.calls)
	}
	now = now.Add(2 * 24 * time.Hour)
	m.Resolve(context.Background(), in)
	if fs.calls != 2 {
		t.Fatalf("did not re-search after expiry: %d", fs.calls)
	}
}

func TestRefreshKeepsSearchRowsAndDatasetWins(t *testing.T) {
	var body atomic.Value
	body.Store(dataset)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body.Load().(string))) }))
	defer srv.Close()
	m := newMap(t, srv.URL)
	ctx := context.Background()
	for _, id := range []int{900, 1} { // 900 search-only; 1 is also in the dataset
		if _, err := m.DB.Exec(`INSERT INTO anime_map(mal_id, kind, tmdb_id, tmdb_season, source, cached_at) VALUES(?, 'series', 7, 1, 'search', ?)`, id, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, m); n != 4 {
		t.Fatalf("rows = %d, want 3 dataset + 1 search", n)
	}
	var src string
	var tm int64
	m.DB.QueryRow(`SELECT source, tmdb_id FROM anime_map WHERE mal_id=1`).Scan(&src, &tm)
	if src != "dataset" || tm != 100 {
		t.Fatalf("dataset did not replace search row: %s %d", src, tm)
	}
	// Dataset rows absent from a new dataset go; search rows stay.
	body.Store(`[{"mal_id": 2, "themoviedb_id": 200, "type": "MOVIE"}, {"mal_id": 4, "themoviedb_id": 300, "type": "ONA"}]`)
	m.DB.Exec(`DELETE FROM settings`)
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, m); n != 3 {
		t.Fatalf("rows = %d, want mal 2, 4 + search 900", n)
	}
	if _, ok, _ := m.Resolve(ctx, Title{MALID: 900}); !ok {
		t.Fatal("search row lost")
	}
}

func TestRefreshMissingSeasonIsOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(dataset)) }))
	defer srv.Close()
	m := newMap(t, srv.URL)
	m.Refresh(context.Background())
	e, _, _ := m.Resolve(context.Background(), Title{MALID: 2})
	if e.Season != 1 {
		t.Fatalf("season = %d", e.Season)
	}
}

func TestRefreshCapsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("["))
		for i := 0; i < 100000; i++ {
			if _, err := w.Write([]byte(`{"mal_id": 1, "themoviedb_id": 1, "type": "TV"},`)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	old := maxBody
	maxBody = 4096
	defer func() { maxBody = old }()
	m := newMap(t, srv.URL)
	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if count(t, m) != 0 {
		t.Fatal("table touched")
	}
}

func TestResolveMovieKind(t *testing.T) {
	m := newMap(t, "")
	fs := &fakeSearch{cands: []Candidate{{TMDBID: 9, Name: "Film", Year: 2016, Animation: true, OriginalLanguage: "ja"}}}
	m.Search = fs
	e, ok, err := m.Resolve(context.Background(), Title{MALID: 6, Name: "Film", Kind: "movie", AiredOn: "2016-08-26"})
	if err != nil || !ok || e.Kind != "movie" || fs.movie != 1 || fs.tv != 0 {
		t.Fatalf("%+v %v %v %+v", e, ok, err, fs)
	}
}

func TestHasTMDB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(dataset)) }))
	defer srv.Close()
	m := newMap(t, srv.URL)
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		kind string
		id   int64
		want bool
	}{{"series", 100, true}, {"movie", 200, true}, {"movie", 100, false}, {"series", 999, false}} {
		if got, err := m.HasTMDB(context.Background(), c.kind, c.id); err != nil || got != c.want {
			t.Errorf("%+v: %v %v", c, got, err)
		}
	}
}

func TestFirstAnime(t *testing.T) {
	m := newMap(t, "")
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO anime_map(mal_id, kind, tmdb_id, tmdb_season, tmdb_offset) VALUES(30, 'series', 7, 1, 12)`,
		`INSERT INTO anime_map(mal_id, kind, tmdb_id, tmdb_season, tmdb_offset) VALUES(31, 'series', 7, 2, 0)`,
		`INSERT INTO anime_map(mal_id, kind, tmdb_id, tmdb_season, source) VALUES(5, 'series', 7, 1, 'search')`,
		`INSERT INTO anime_map(mal_id, kind, tmdb_id, tmdb_season, tmdb_offset) VALUES(29, 'series', 7, 1, 0)`,
		`INSERT INTO anime_map(mal_id, kind, tmdb_id) VALUES(40, 'movie', 8)`,
	} {
		if _, err := m.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if e, ok, err := m.FirstAnime(ctx, "series", 7); err != nil || !ok || e.MALID != 29 || e.Season != 1 {
		t.Fatalf("series %+v %v %v", e, ok, err)
	}
	if e, ok, _ := m.FirstAnime(ctx, "movie", 8); !ok || e.MALID != 40 {
		t.Fatalf("movie %+v %v", e, ok)
	}
	if _, ok, _ := m.FirstAnime(ctx, "series", 8); ok {
		t.Fatal("kind ignored")
	}
}

// A refresh that parses to nothing, or to under half the dataset rows held
// now (a format drift), is refused: the old rows stay and fetched_at is not
// bumped, so the next run retries.
func TestRefreshRefusesCollapsedDataset(t *testing.T) {
	var body atomic.Value
	body.Store(dataset)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body.Load().(string))) }))
	defer srv.Close()
	m := newMap(t, srv.URL)
	ctx := context.Background()
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	stamp := m.getSetting(ctx, keyFetchedAt)
	m.DB.Exec(`UPDATE settings SET value='2000-01-01T00:00:00Z' WHERE key=?`, keyFetchedAt)
	for _, b := range []string{
		`[]`,
		`[{"mal_id": 1, "themoviedb_id": {"unknown": 5}}, {"mal_id": 2}]`, // drifted: nothing parses
		`[{"mal_id": 2, "themoviedb_id": 200, "type": "MOVIE"}]`,          // 1 of 3
	} {
		body.Store(b)
		if err := m.Refresh(ctx); err == nil {
			t.Fatalf("%s: refresh accepted", b)
		}
		if n := count(t, m); n != 3 {
			t.Fatalf("%s: rows = %d, want 3", b, n)
		}
		if v := m.getSetting(ctx, keyFetchedAt); v != "2000-01-01T00:00:00Z" {
			t.Fatalf("%s: fetched_at bumped to %s (was %s)", b, v, stamp)
		}
	}
}
