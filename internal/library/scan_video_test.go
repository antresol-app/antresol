package library

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

func newRepo(t *testing.T) *Repo {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return NewRepo(d)
}

func touch(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newScanner(t *testing.T) (*Scanner, string, string) {
	t.Helper()
	root := t.TempDir()
	mv, sr := filepath.Join(root, "movies"), filepath.Join(root, "series")
	os.MkdirAll(mv, 0o755)
	os.MkdirAll(sr, 0o755)
	return &Scanner{Repo: newRepo(t), Movies: mv, Series: sr}, mv, sr
}

func TestScanMovies(t *testing.T) {
	s, mv, _ := newScanner(t)
	touch(t, filepath.Join(mv, "Oppenheimer (2023)", "Oppenheimer (2023) Bluray-1080p.mkv"), "")
	touch(t, filepath.Join(mv, "Oppenheimer (2023)", "movie.nfo"),
		`<?xml version="1.0" encoding="utf-8"?><movie><title>Оппенгеймер</title><originaltitle>Oppenheimer</originaltitle><year>2023</year><mpaa>R</mpaa><tmdbid>693134</tmdbid><uniqueid type="tmdb" default="true">693134</uniqueid></movie>`)
	touch(t, filepath.Join(mv, "Oppenheimer (2023)", "poster.jpg"), "x")
	touch(t, filepath.Join(mv, "Брат 2 (2000) BDRip 1080p.mkv"), "")
	st, err := s.ScanVideo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Movies != 2 {
		t.Fatalf("stats %+v", st)
	}
	items, total, err := s.Repo.List(context.Background(), ListQuery{Kind: "movie", Sort: "title"})
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("list %v %d %v", items, total, err)
	}
	var opp, brat Item
	for _, it := range items {
		if it.TmdbID == 693134 {
			opp = it
		} else {
			brat = it
		}
	}
	if opp.Title != "Оппенгеймер" || opp.Year != 2023 || opp.Rating != "16+" {
		t.Fatalf("opp %+v", opp)
	}
	if opp.Poster != "file:"+filepath.Join(mv, "Oppenheimer (2023)", "poster.jpg") {
		t.Fatalf("poster %q", opp.Poster)
	}
	if brat.Title != "Брат 2" || brat.Year != 2000 || brat.TmdbID != 0 {
		t.Fatalf("brat %+v", brat)
	}
	fs, _ := s.Repo.FilesOf(context.Background(), opp.ID)
	if len(fs) != 1 {
		t.Fatalf("files %v", fs)
	}
	// original title is searchable
	got, _ := s.Repo.Search(context.Background(), "oppenheimer", 10)
	if len(got) != 1 || got[0].ID != opp.ID {
		t.Fatalf("search %v", got)
	}
}

func TestScanSeriesNested(t *testing.T) {
	s, _, sr := newScanner(t)
	touch(t, filepath.Join(sr, "Сёгун", "Season 1", "Сёгун.S01E01.mkv"), "")
	touch(t, filepath.Join(sr, "Сёгун", "Season 1", "Сёгун.S01E02.mkv"), "")
	touch(t, filepath.Join(sr, "Сёгун", "tvshow.nfo"), `<tvshow><title>Сёгун</title><year>2024</year><uniqueid type="tmdb">126308</uniqueid></tvshow>`)
	if _, err := s.ScanVideo(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	series, _, _ := s.Repo.List(ctx, ListQuery{Kind: "series"})
	if len(series) != 1 || series[0].TmdbID != 126308 || series[0].Year != 2024 {
		t.Fatalf("series %+v", series)
	}
	seasons, _ := s.Repo.Children(ctx, series[0].ID)
	if len(seasons) != 1 || seasons[0].Kind != "season" || seasons[0].SeasonNo != 1 {
		t.Fatalf("seasons %+v", seasons)
	}
	eps, _ := s.Repo.Children(ctx, seasons[0].ID)
	if len(eps) != 2 || eps[0].EpisodeNo != 1 || eps[1].EpisodeNo != 2 {
		t.Fatalf("eps %+v", eps)
	}
}

func TestScanFlatReleaseFolder(t *testing.T) {
	s, _, sr := newScanner(t)
	touch(t, filepath.Join(sr, "The.Bear.S03.1080p", "The.Bear.S03E01.mkv"), "")
	touch(t, filepath.Join(sr, "The.Bear.S03.1080p", "The.Bear.S03E02.mkv"), "")
	touch(t, filepath.Join(sr, "The.Bear.S03.1080p", "Sample", "sample.mkv"), "")
	if _, err := s.ScanVideo(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	series, _, _ := s.Repo.List(ctx, ListQuery{Kind: "series"})
	if len(series) != 1 || series[0].Title != "The Bear" {
		t.Fatalf("series %+v", series)
	}
	seasons, _ := s.Repo.Children(ctx, series[0].ID)
	if len(seasons) != 1 || seasons[0].SeasonNo != 3 {
		t.Fatalf("seasons %+v", seasons)
	}
	eps, _ := s.Repo.Children(ctx, seasons[0].ID)
	if len(eps) != 2 {
		t.Fatalf("eps %+v", eps)
	}
}

func TestSeriesMergedAcrossReleaseFolders(t *testing.T) {
	s, _, sr := newScanner(t)
	touch(t, filepath.Join(sr, "The.Bear.S02.1080p", "The.Bear.S02E01.mkv"), "")
	touch(t, filepath.Join(sr, "The Bear (2022)", "Season 01", "The Bear - S01E01 - System WEBDL-1080p.mkv"), "")
	if _, err := s.ScanVideo(context.Background()); err != nil {
		t.Fatal(err)
	}
	series, _, _ := s.Repo.List(context.Background(), ListQuery{Kind: "series"})
	if len(series) != 1 {
		t.Fatalf("series %+v", series)
	}
}

func TestRescanIdempotent(t *testing.T) {
	s, mv, sr := newScanner(t)
	touch(t, filepath.Join(mv, "A (2001)", "A (2001).mkv"), "")
	touch(t, filepath.Join(sr, "Show", "Season 01", "Show.S01E01.mkv"), "")
	ctx := context.Background()
	count := func(q string) int {
		var n int
		s.Repo.db.QueryRow(q).Scan(&n)
		return n
	}
	s.ScanVideo(ctx)
	a, b, c := count(`SELECT COUNT(*) FROM items`), count(`SELECT COUNT(*) FROM files`), count(`SELECT COUNT(*) FROM item_titles`)
	if a != 4 || b != 2 {
		t.Fatalf("items=%d files=%d", a, b)
	}
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	if count(`SELECT COUNT(*) FROM items`) != a || count(`SELECT COUNT(*) FROM files`) != b || count(`SELECT COUNT(*) FROM item_titles`) != c {
		t.Fatal("rescan changed counts")
	}
}

func TestRescanKeepsEnrichedMetadata(t *testing.T) {
	s, mv, _ := newScanner(t)
	touch(t, filepath.Join(mv, "A (2001)", "A (2001).mkv"), "")
	ctx := context.Background()
	s.ScanVideo(ctx)
	items, _, _ := s.Repo.List(ctx, ListQuery{Kind: "movie"})
	s.Repo.SetMetadata(ctx, items[0].ID, Metadata{Title: "Ай", Overview: "про букву", TmdbID: 5, Rating: "12+"})
	s.ScanVideo(ctx)
	it, _ := s.Repo.Item(ctx, items[0].ID)
	if it.Title != "Ай" || it.TmdbID != 5 || it.Overview != "про букву" {
		t.Fatalf("clobbered: %+v", it)
	}
}

func TestRemovedFileDropped(t *testing.T) {
	s, mv, sr := newScanner(t)
	a := filepath.Join(mv, "A (2001)", "A (2001).mkv")
	b := filepath.Join(sr, "Show", "Season 01", "Show.S01E01.mkv")
	touch(t, a, "")
	touch(t, b, "")
	ctx := context.Background()
	s.ScanVideo(ctx)
	os.Remove(a)
	os.Remove(b)
	touch(t, filepath.Join(mv, "B (2002)", "B (2002).mkv"), "")
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	var items, files int
	s.Repo.db.QueryRow(`SELECT COUNT(*) FROM items WHERE missing_since IS NULL`).Scan(&items)
	s.Repo.db.QueryRow(`SELECT COUNT(*) FROM files WHERE missing_since IS NULL`).Scan(&files)
	if items != 1 || files != 1 {
		t.Fatalf("items=%d files=%d", items, files)
	}
	// After the grace period the missing rows are deleted for good.
	old := MissingGrace
	MissingGrace = -time.Hour
	defer func() { MissingGrace = old }()
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	s.Repo.db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&items)
	s.Repo.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&files)
	if items != 1 || files != 1 {
		t.Fatalf("after purge items=%d files=%d", items, files)
	}
	var n int
	s.Repo.db.QueryRow(`SELECT COUNT(*) FROM items_fts`).Scan(&n)
	if n != 1 {
		t.Fatalf("fts rows %d", n)
	}
}

func TestMissingRootDoesNotWipe(t *testing.T) {
	s, mv, _ := newScanner(t)
	touch(t, filepath.Join(mv, "A (2001)", "A (2001).mkv"), "")
	ctx := context.Background()
	s.ScanVideo(ctx)
	os.RemoveAll(mv) // e.g. unmounted disk
	if _, err := s.ScanVideo(ctx); err == nil {
		t.Fatal("expected error for missing root")
	}
	var n int
	s.Repo.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&n)
	if n != 1 {
		t.Fatalf("files wiped: %d", n)
	}
}

func TestSampleIgnored(t *testing.T) {
	s, mv, _ := newScanner(t)
	touch(t, filepath.Join(mv, "C (2003)", "C (2003).mkv"), "")
	touch(t, filepath.Join(mv, "C (2003)", "Sample", "C-sample.mkv"), "")
	touch(t, filepath.Join(mv, "C (2003)", "c.2003.sample.mkv"), "")
	st, _ := s.ScanVideo(context.Background())
	if st.Movies != 1 || st.Files != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestSearchByAltTitle(t *testing.T) {
	s, _, sr := newScanner(t)
	touch(t, filepath.Join(sr, "Сёгун", "Season 1", "Сёгун.S01E01.mkv"), "")
	ctx := context.Background()
	s.ScanVideo(ctx)
	series, _, _ := s.Repo.List(ctx, ListQuery{Kind: "series"})
	if got, _ := s.Repo.Search(ctx, "shogun", 10); len(got) != 0 {
		t.Fatalf("premature hit %v", got)
	}
	if err := s.Repo.AddTitles(ctx, series[0].ID, []string{"Shogun"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Repo.Search(ctx, "shogun", 10)
	if err != nil || len(got) != 1 || got[0].ID != series[0].ID {
		t.Fatalf("search %v %v", got, err)
	}
	if got, _ := s.Repo.Search(ctx, "сегун", 10); len(got) != 1 { // ё→е
		t.Fatalf("search ru %v", got)
	}
	if got, _ := s.Repo.Search(ctx, "sho", 10); len(got) != 1 { // prefix
		t.Fatalf("search prefix %v", got)
	}
}

func TestLocalArtAndEpisodeNfo(t *testing.T) {
	s, _, sr := newScanner(t)
	d := filepath.Join(sr, "Show (2020)")
	touch(t, filepath.Join(d, "poster.jpg"), "x")
	touch(t, filepath.Join(d, "fanart.jpg"), "x")
	touch(t, filepath.Join(d, "Season 01", "Show.S01E01.mkv"), "")
	touch(t, filepath.Join(d, "Season 01", "Show.S01E01.nfo"), `<episodedetails><title>Пилот</title><season>1</season><episode>1</episode></episodedetails>`)
	ctx := context.Background()
	s.ScanVideo(ctx)
	series, _, _ := s.Repo.List(ctx, ListQuery{Kind: "series"})
	if series[0].Poster != "file:"+filepath.Join(d, "poster.jpg") || series[0].Backdrop != "file:"+filepath.Join(d, "fanart.jpg") {
		t.Fatalf("art %+v", series[0])
	}
	seasons, _ := s.Repo.Children(ctx, series[0].ID)
	eps, _ := s.Repo.Children(ctx, seasons[0].ID)
	if len(eps) != 1 || eps[0].Title != "Пилот" {
		t.Fatalf("eps %+v", eps)
	}
}

// <plot> and <genre> of movie, tvshow and episode nfo files: they fill an
// empty overview and a missing genres list, never overwrite stored ones.
func TestNfoPlotAndGenres(t *testing.T) {
	s, mv, sr := newScanner(t)
	touch(t, filepath.Join(mv, "Film (2001)", "Film (2001).mkv"), "")
	touch(t, filepath.Join(mv, "Film (2001)", "movie.nfo"),
		`<movie><title>Фильм</title><plot>О фильме.</plot><genre>Драма</genre><genre>Комедия / Драма</genre></movie>`)
	d := filepath.Join(sr, "Show")
	touch(t, filepath.Join(d, "tvshow.nfo"), `<tvshow><title>Шоу</title><plot>О сериале.</plot><genre>Документальный</genre></tvshow>`)
	touch(t, filepath.Join(d, "Season 01", "Show.S01E01.mkv"), "")
	touch(t, filepath.Join(d, "Season 01", "Show.S01E01.nfo"), `<episodedetails><title>Пилот</title><season>1</season><episode>1</episode><plot>О серии.</plot></episodedetails>`)
	ctx := context.Background()
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	genres := func(it Item) []string {
		var m struct{ Genres []string }
		json.Unmarshal([]byte(it.MetaJSON), &m)
		return m.Genres
	}
	movies, _, _ := s.Repo.List(ctx, ListQuery{Kind: "movie"})
	if len(movies) != 1 || movies[0].Overview != "О фильме." || !slices.Equal(genres(movies[0]), []string{"Драма", "Комедия"}) {
		t.Fatalf("movie %+v %v", movies, genres(movies[0]))
	}
	series, _, _ := s.Repo.List(ctx, ListQuery{Kind: "series"})
	if len(series) != 1 || series[0].Overview != "О сериале." || !slices.Equal(genres(series[0]), []string{"Документальный"}) {
		t.Fatalf("series %+v", series)
	}
	seasons, _ := s.Repo.Children(ctx, series[0].ID)
	eps, _ := s.Repo.Children(ctx, seasons[0].ID)
	if len(eps) != 1 || eps[0].Overview != "О серии." {
		t.Fatalf("eps %+v", eps)
	}
	// stored metadata (TMDB's) wins on a rescan
	if _, err := s.Repo.db.Exec(`UPDATE items SET overview='TMDB', meta_json='{"genres":["Drama"]}' WHERE id=?`, series[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	it, _ := s.Repo.Item(ctx, series[0].ID)
	if it.Overview != "TMDB" || !slices.Equal(genres(it), []string{"Drama"}) {
		t.Fatalf("rescan overwrote: %+v", it)
	}
}
