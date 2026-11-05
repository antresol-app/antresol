package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
)

type fakeTMDB struct {
	mu   sync.Mutex
	hits []string
	srv  *httptest.Server
}

func newFake(t *testing.T) *fakeTMDB {
	f := &fakeTMDB{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits = append(f.hits, r.URL.Path+"?"+r.URL.Query().Get("external_source"))
		f.mu.Unlock()
		p := strings.TrimPrefix(r.URL.Path, "/3")
		switch {
		case strings.HasSuffix(p, "/slow.jpg"):
			time.Sleep(400 * time.Millisecond)
		case strings.HasSuffix(p, "/broken.jpg"):
			http.NotFound(w, r)
		case strings.Contains(r.URL.Path, "/t/p/"):
			w.Write([]byte("IMG"))
		case p == "/movie/998":
			w.Write([]byte(`{"id":998,"title":"Медленный","poster_path":"/slow.jpg"}`))
		case p == "/movie/999":
			w.Write([]byte(`{"id":999,"title":"Битый","poster_path":"/broken.jpg","release_date":"2000-01-01"}`))
		case p == "/movie/157336":
			w.Write([]byte(movieJSON))
		case p == "/search/movie":
			w.Write([]byte(`{"results":[{"id":157336,"title":"Интерстеллар","release_date":"2014-11-05"}]}`))
		case strings.HasPrefix(p, "/find/318408"):
			w.Write([]byte(`{"tv_results":[{"id":1001}]}`))
		case p == "/tv/1001":
			w.Write([]byte(`{"id":1001,"name":"Планета Земля II","original_name":"Planet Earth II","first_air_date":"2016-11-06","overview":"Природа",
			 "poster_path":"/sp.jpg","backdrop_path":"/sb.jpg","content_ratings":{"results":[{"iso_3166_1":"US","rating":"TV-G"}]}}`))
		case p == "/tv/1001/season/1":
			w.Write([]byte(`{"season_number":1,"episodes":[{"episode_number":1,"name":"Острова","overview":"Об островах","still_path":"/e1.jpg"},{"episode_number":2,"name":"Горы","overview":"О горах","still_path":"/e2.jpg"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTMDB) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, h := range f.hits {
		if strings.HasPrefix(h, prefix) {
			n++
		}
	}
	return n
}

func setup(t *testing.T, base string) (*Enricher, *library.Repo) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	repo := library.NewRepo(d)
	return &Enricher{
		Client: &Client{Key: "k", BaseURL: base + "/3", ImageBase: base + "/t/p"},
		Repo:   repo,
		Images: &metadata.Images{Dir: filepath.Join(t.TempDir(), "images")},
	}, repo
}

func TestEnrichUsesNfoTmdbID(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	id, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "Interstellar", Year: 2014, TmdbID: 157336, File: library.FileInput{Path: "/m/i.mkv"}})
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count("/3/search") != 0 || f.count("/3/movie/157336") != 1 {
		t.Fatalf("hits %v", f.hits)
	}
	it, _ := repo.Item(ctx, id)
	if it.Overview != "Фантастика" || it.Rating != "12+" || it.Poster == "" || strings.HasPrefix(it.Poster, "file:") || it.Backdrop == "" ||
		!strings.Contains(it.MetaJSON, `"logo"`) || !strings.Contains(it.MetaJSON, "Matthew") || it.Title != "Interstellar" ||
		!strings.Contains(it.MetaJSON, `"tmdb_lang":"ru-RU"`) || !strings.Contains(it.MetaJSON, `"imdb_id":"tt0816692"`) {
		t.Fatalf("%+v", it)
	}
	// aliases are searchable
	if got, _ := repo.Search(ctx, "Интерстеллар", 5); len(got) != 1 {
		t.Fatalf("alias search: %v", got)
	}
	// second run does nothing
	n := len(f.hits)
	e.EnrichPending(ctx)
	if len(f.hits) != n {
		t.Fatalf("re-enriched: %v", f.hits[n:])
	}
}

// An item enriched before the IMDb id was kept is enriched once more for it;
// one TMDB knows no IMDb id for is not asked again.
func TestEnrichFillsIMDbIDOfEnrichedItems(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	id, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "Interstellar", Year: 2014, TmdbID: 157336, File: library.FileInput{Path: "/m/i.mkv"}})
	old := `{"tmdb_at":"` + time.Now().UTC().Format(time.RFC3339) + `","genreIds":[],"logo":"x.png"}`
	if err := repo.SetMetadata(ctx, id, library.Metadata{MetaJSON: old}); err != nil {
		t.Fatal(err)
	}
	none, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "Битый", TmdbID: 999, File: library.FileInput{Path: "/m/b.mkv"}})
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	it, _ := repo.Item(ctx, id)
	if !strings.Contains(it.MetaJSON, `"imdb_id":"tt0816692"`) || !strings.Contains(it.MetaJSON, `"logo":"x.png"`) {
		t.Fatalf("%s", it.MetaJSON)
	}
	if b, _ := repo.Item(ctx, none); !strings.Contains(b.MetaJSON, `"imdb_id":""`) {
		t.Fatalf("%s", b.MetaJSON)
	}
	n := f.count("/3/movie/")
	e.EnrichPending(ctx)
	if f.count("/3/movie/") != n {
		t.Fatalf("asked again: %v", f.hits)
	}
}

func TestEnrichSearchesByTitleAndKeepsLocalPoster(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	id, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "Interstellar", Year: 2014, Poster: "file:/m/poster.jpg", Overview: "мой текст",
		File: library.FileInput{Path: "/m/i.mkv"}})
	e.EnrichPending(ctx)
	it, _ := repo.Item(ctx, id)
	if it.TmdbID != 157336 || it.Poster != "file:/m/poster.jpg" || it.Overview != "мой текст" || it.Backdrop == "" {
		t.Fatalf("%+v", it)
	}
}

func TestEnrichFindsSeriesByTvdbAndFillsEpisodes(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	for _, n := range []int{1, 2} {
		repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Planet Earth II", Season: 1, Episode: n,
			File: library.FileInput{Path: "/s/pe2." + string(rune('0'+n)) + ".mkv"}})
	}
	items, _, _ := repo.List(ctx, library.ListQuery{Kind: "series"})
	sid := items[0].ID
	if err := repo.SetMetadata(ctx, sid, library.Metadata{MetaJSON: `{"tvdb_id":"318408","imdb_id":"tt5491994"}`}); err != nil {
		t.Fatal(err)
	}
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count("/3/search") != 0 || f.count("/3/find/318408?tvdb_id") != 1 {
		t.Fatalf("hits %v", f.hits)
	}
	s, _ := repo.Item(ctx, sid)
	if s.TmdbID != 1001 || s.Rating != "0+" || s.Overview != "Природа" || !strings.Contains(s.MetaJSON, "tvdb_id") ||
		!strings.Contains(s.MetaJSON, `"imdb_id":"tt5491994"`) { // the .nfo's id is kept
		t.Fatalf("%+v", s)
	}
	seasons, _ := repo.Children(ctx, sid)
	eps, _ := repo.Children(ctx, seasons[0].ID)
	if eps[0].Title != "Острова" || eps[1].Overview != "О горах" || eps[0].Poster == "" {
		t.Fatalf("%+v", eps)
	}
}

func TestEnrichOfflineNoError(t *testing.T) {
	f := newFake(t)
	base := f.srv.URL
	f.srv.Close()
	e, repo := setup(t, base)
	ctx := context.Background()
	id, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "Interstellar", Year: 2014, File: library.FileInput{Path: "/m/i.mkv"}})
	before, _ := repo.Item(ctx, id)
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatalf("offline must not error: %v", err)
	}
	after, _ := repo.Item(ctx, id)
	if before != after {
		t.Fatalf("changed: %+v -> %+v", before, after)
	}
}

func TestEnrichNotFoundMarksMissAndDoesNotRetryImmediately(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	repo.UpsertMovie(ctx, library.MovieInput{Title: "Nothing", File: library.FileInput{Path: "/m/n.mkv"}})
	repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Unknown Show", Season: 1, Episode: 1, File: library.FileInput{Path: "/s/u.mkv"}})
	e.EnrichPending(ctx)
	n := f.count("/3/search/tv")
	e.EnrichPending(ctx)
	if n == 0 || f.count("/3/search/tv") != n {
		t.Fatalf("tv searches %d -> %d", n, f.count("/3/search/tv"))
	}
}

func TestBrokenImageDoesNotBlockOthers(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	a, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "A", TmdbID: 999, File: library.FileInput{Path: "/m/a.mkv"}})
	b, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "B", TmdbID: 157336, File: library.FileInput{Path: "/m/b.mkv"}})
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	ia, _ := repo.Item(ctx, a)
	ib, _ := repo.Item(ctx, b)
	if ib.Overview == "" || ib.Poster == "" {
		t.Fatalf("B not enriched: %+v", ib)
	}
	if ia.Poster != "" || !strings.Contains(ia.MetaJSON, "images_incomplete") {
		t.Fatalf("A: %+v", ia)
	}
	n := len(f.hits)
	e.EnrichPending(ctx) // within backoff: no retry
	if len(f.hits) != n {
		t.Fatalf("retried too soon: %v", f.hits[n:])
	}
}

func TestPickResultPrefersYear(t *testing.T) {
	rs := []Result{{ID: 1, Year: 1990}, {ID: 2, Year: 2014}}
	if pickResult(rs, 2015) != 2 || pickResult(rs, 0) != 1 || pickResult(rs, 1950) != 1 {
		t.Fatal("pick")
	}
}

func TestImageTransportErrorAbortsWithoutBurningTries(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	a, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "B", TmdbID: 157336, File: library.FileInput{Path: "/m/b.mkv"}})
	// API works, image CDN is a closed server.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	e.Client.ImageBase = deadURL + "/t/p"
	for i := 0; i < 7; i++ {
		if err := e.EnrichPending(ctx); err != nil {
			t.Fatal(err)
		}
	}
	it, _ := repo.Item(ctx, a)
	if it.Poster != "" || it.Overview != "" || strings.Contains(it.MetaJSON, "images_tries") || strings.Contains(it.MetaJSON, "tmdb_at") {
		t.Fatalf("item must stay untouched/pending: %+v", it)
	}
	// CDN back: item gets its poster.
	e.Client.ImageBase = f.srv.URL + "/t/p"
	e.EnrichPending(ctx)
	if it, _ = repo.Item(ctx, a); it.Poster == "" {
		t.Fatalf("not recovered: %+v", it)
	}
}

func TestImageTimeoutWithReachableHostIsItemLocal(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	e.Images.HTTP = &http.Client{Timeout: 60 * time.Millisecond}
	ctx := context.Background()
	a, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "A", TmdbID: 998, File: library.FileInput{Path: "/m/a.mkv"}})
	b, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "B", TmdbID: 157336, File: library.FileInput{Path: "/m/b.mkv"}})
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	ia, _ := repo.Item(ctx, a)
	ib, _ := repo.Item(ctx, b)
	if ib.Poster == "" || ib.Overview == "" {
		t.Fatalf("B not enriched: %+v", ib)
	}
	if !strings.Contains(ia.MetaJSON, `"images_tries":1`) {
		t.Fatalf("A not counted item-local: %+v", ia)
	}
}

func TestEpisodeStillHostDownLeavesEpsAtUnset(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Planet Earth II", SeriesTmdbID: 1001, SeriesPoster: "file:/s/p.jpg",
		SeriesBackdrop: "file:/s/b.jpg", Season: 1, Episode: 1, File: library.FileInput{Path: "/s/1.mkv"}})
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	e.Client.ImageBase = deadURL + "/t/p"
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	items, _, _ := repo.List(ctx, library.ListQuery{Kind: "series"})
	if strings.Contains(items[0].MetaJSON, "eps_at") {
		t.Fatalf("eps_at set despite host down: %s", items[0].MetaJSON)
	}
	// host back: episodes filled, eps_at set
	e.Client.ImageBase = f.srv.URL + "/t/p"
	e.EnrichPending(ctx)
	items, _, _ = repo.List(ctx, library.ListQuery{Kind: "series"})
	if !strings.Contains(items[0].MetaJSON, "eps_at") {
		t.Fatalf("eps_at not set after recovery: %s", items[0].MetaJSON)
	}
}

// The TMDB poster's own address is kept (meta poster_url): a notification
// letter links it instead of attaching the cached copy.
func TestEnrichKeepsRemotePosterURL(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	id, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "Interstellar", Year: 2014, TmdbID: 157336, File: library.FileInput{Path: "/m/i.mkv"}})
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	it, _ := repo.Item(ctx, id)
	if !strings.Contains(it.MetaJSON, `"poster_url":"`+f.srv.URL+`/t/p/w185/`) {
		t.Fatalf("meta %s", it.MetaJSON)
	}
}
