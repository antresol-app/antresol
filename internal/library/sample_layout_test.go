package library

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildServerTree recreates a sample server layout (made-up titles in real-world naming patterns) (testdata/sample-media-files.txt) with empty files.
func buildServerTree(t *testing.T) (*Scanner, string, string) {
	t.Helper()
	s, mv, sr := newScanner(t)
	f, err := os.Open("testdata/sample-media-files.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		kind, rel, _ := strings.Cut(line, "/")
		root := mv
		if kind == "series" {
			root = sr
		}
		touch(t, filepath.Join(root, rel), "")
	}
	return s, mv, sr
}

func TestSampleServerLayout(t *testing.T) {
	s, mv, sr := buildServerTree(t)
	// a few nfo contents
	touch(t, filepath.Join(sr, "Green Earth II", "tvshow.nfo"),
		`<tvshow><title>Green Earth II</title><rating>9.4</rating><mpaa>TV-G</mpaa><uniqueid type="tvdb" default="true">900002</uniqueid><uniqueid type="imdb">tt9000002</uniqueid></tvshow>`)
	touch(t, filepath.Join(sr, "Green Earth II", "Green.Earth.II.Episode.3.1080p.BDRip.nfo"),
		`<episodedetails><title>Jungles</title><season>1</season><episode>3</episode><aired>2016-11-20</aired></episodedetails>`)
	touch(t, filepath.Join(sr, "Green Earth", "TVC.Green.Earth.7.Great.Plains.BDRip.1080p.Kettle.nfo"),
		`<episodedetails><title>Great Plains</title><season>1</season><episode>7</episode></episodedetails>`)
	touch(t, filepath.Join(mv, "Starship (2014)", "Звездолёт_Starship.2014.BDRemux.IMAX.nfo"),
		`<movie><title>Звездолёт</title><originaltitle>Starship</originaltitle><year>2014</year><uniqueid type="tmdb">900001</uniqueid><uniqueid type="imdb">tt9000001</uniqueid></movie>`)

	ctx := context.Background()
	st, err := s.ScanVideo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Skipped != 0 {
		t.Fatalf("skipped %d: %+v", st.Skipped, st)
	}
	movies, total, _ := s.Repo.List(ctx, ListQuery{Kind: "movie", Sort: "title"})
	if total != 6 {
		t.Fatalf("movies %d: %v", total, titles(movies))
	}
	byTitle := map[string]Item{}
	for _, m := range movies {
		byTitle[m.Title] = m
	}
	if m, ok := byTitle["Market Day"]; !ok || m.Year != 2007 {
		t.Fatalf("Market Day: %v", titles(movies))
	} else if got, _ := s.Repo.Search(ctx, "базарный день", 5); len(got) != 1 || got[0].ID != m.ID {
		t.Fatalf("russian alt title not searchable: %v", titles(got))
	}
	city := byTitle["The City Does Not Sleep at Noon"]
	if fs, _ := s.Repo.FilesOf(ctx, city.ID); len(fs) != 2 || !strings.HasSuffix(fs[0].Path, "cd1.mkv") || !strings.HasSuffix(fs[1].Path, "cd2.mkv") {
		t.Fatalf("city files %+v", fs)
	}
	inter, ok := byTitle["Звездолёт"]
	if !ok || inter.TmdbID != 900001 || !strings.Contains(inter.MetaJSON, `"imdb_id":"tt9000001"`) {
		t.Fatalf("starship %+v", inter)
	}
	if fs, _ := s.Repo.FilesOf(ctx, inter.ID); len(fs) != 1 || !strings.HasSuffix(fs[0].Path, ".m2ts") {
		t.Fatalf("starship files %+v", fs)
	}

	want := map[string][2]int{ // series -> {seasons, episodes}
		"Small Wonders": {2, 12}, "Green Earth": {1, 11}, "Green Earth II": {1, 6}, "Green Earth III": {1, 8},
		"Deep Blue II": {1, 7}, "Empty the Seas": {1, 10}, "Savanna": {1, 6}, "Five Lands, One Sky": {1, 6},
	}
	series, total, _ := s.Repo.List(ctx, ListQuery{Kind: "series"})
	if total != len(want) {
		t.Fatalf("series %d: %v", total, titles(series))
	}
	for _, sr := range series {
		w, ok := want[sr.Title]
		if !ok {
			t.Fatalf("unexpected series %q", sr.Title)
		}
		seasons, _ := s.Repo.Children(ctx, sr.ID)
		n := 0
		for _, se := range seasons {
			eps, _ := s.Repo.Children(ctx, se.ID)
			n += len(eps)
		}
		if len(seasons) != w[0] || n != w[1] {
			t.Errorf("%s: %d seasons %d episodes, want %v", sr.Title, len(seasons), n, w)
		}
		if sr.Title == "Green Earth II" {
			if sr.Rating != "0+" || !strings.Contains(sr.MetaJSON, `"tvdb_id":"900002"`) || !strings.Contains(sr.MetaJSON, `"imdb_id":"tt9000002"`) {
				t.Errorf("GE2 %+v", sr)
			}
			eps, _ := s.Repo.Children(ctx, seasons[0].ID)
			if eps[2].Title != "Jungles" {
				t.Errorf("episode 3 title %q", eps[2].Title)
			}
		}
	}
	// idempotent
	st2, _ := s.ScanVideo(ctx)
	if st2.Skipped != 0 || st2.Files != st.Files {
		t.Fatalf("rescan %+v vs %+v", st2, st)
	}
	_, total, _ = s.Repo.List(ctx, ListQuery{})
	if total != 14 {
		t.Fatalf("items after rescan %d", total)
	}
}

func TestEpisodeLeadingNumberInSeasonDir(t *testing.T) {
	s, _, sr := newScanner(t)
	touch(t, filepath.Join(sr, "Сериал", "Сезон 1", "01. Название серии.mkv"), "")
	touch(t, filepath.Join(sr, "Сериал", "Сезон 1", "02. Вторая.mkv"), "")
	touch(t, filepath.Join(sr, "Show", "Season 02", "Show - S02E03 - The Title WEBDL-1080p.mkv"), "")
	ctx := context.Background()
	st, err := s.ScanVideo(ctx)
	if err != nil || st.Skipped != 0 || st.Episodes != 3 {
		t.Fatalf("%+v %v", st, err)
	}
	series, _, _ := s.Repo.List(ctx, ListQuery{Kind: "series", Sort: "title"})
	for _, sr := range series {
		seasons, _ := s.Repo.Children(ctx, sr.ID)
		eps, _ := s.Repo.Children(ctx, seasons[0].ID)
		switch sr.Title {
		case "Сериал":
			if len(eps) != 2 || eps[0].Title != "Название серии" || eps[0].EpisodeNo != 1 {
				t.Fatalf("%+v", eps)
			}
		case "Show":
			if seasons[0].SeasonNo != 2 || eps[0].Title != "The Title" {
				t.Fatalf("%+v %+v", seasons, eps)
			}
		}
	}
}

func TestSeriesKeepsIDWhenNfoAppears(t *testing.T) {
	s, _, sr := newScanner(t)
	touch(t, filepath.Join(sr, "Shogun", "Season 1", "Shogun.S01E01.mkv"), "")
	ctx := context.Background()
	s.ScanVideo(ctx)
	before, _, _ := s.Repo.List(ctx, ListQuery{Kind: "series"})
	touch(t, filepath.Join(sr, "Shogun", "tvshow.nfo"), `<tvshow><title>Сёгун</title><uniqueid type="tvdb">1</uniqueid></tvshow>`)
	s.ScanVideo(ctx)
	after, total, _ := s.Repo.List(ctx, ListQuery{Kind: "series"})
	if total != 1 || after[0].ID != before[0].ID {
		t.Fatalf("series id changed: %+v -> %+v", before, after)
	}
	// and a later nfo with a different title but the same tvdb id
	touch(t, filepath.Join(sr, "Другая папка", "Other.S01E01.mkv"), "")
	touch(t, filepath.Join(sr, "Другая папка", "tvshow.nfo"), `<tvshow><title>Совсем другое</title><uniqueid type="tvdb">1</uniqueid></tvshow>`)
	s.ScanVideo(ctx)
	if _, total, _ = s.Repo.List(ctx, ListQuery{Kind: "series"}); total != 1 {
		t.Fatalf("tvdb id did not merge: %d", total)
	}
}

func TestRadarrUpgradeKeepsItem(t *testing.T) {
	s, mv, _ := newScanner(t)
	d := filepath.Join(mv, "A (2001)")
	old := filepath.Join(d, "A (2001) WEBDL-1080p.mkv")
	touch(t, old, "")
	ctx := context.Background()
	s.ScanVideo(ctx)
	before, _, _ := s.Repo.List(ctx, ListQuery{Kind: "movie"})
	os.Remove(old)
	touch(t, filepath.Join(d, "A (2001) Bluray-2160p.mkv"), "")
	s.ScanVideo(ctx)
	after, total, _ := s.Repo.List(ctx, ListQuery{Kind: "movie"})
	if total != 1 || after[0].ID != before[0].ID {
		t.Fatalf("item changed: %+v -> %+v", before, after)
	}
	if fs, _ := s.Repo.FilesOf(ctx, after[0].ID); len(fs) != 1 || !strings.Contains(fs[0].Path, "2160p") {
		t.Fatalf("files %+v", fs)
	}
}

func TestMultiVideoFolderReadsMovieNfo(t *testing.T) {
	s, mv, _ := newScanner(t)
	d := filepath.Join(mv, "B (2005)")
	touch(t, filepath.Join(d, "B (2005) - cd1.mkv"), "")
	touch(t, filepath.Join(d, "B (2005) - cd2.mkv"), "")
	touch(t, filepath.Join(d, "movie.nfo"), `<movie><title>Бэ</title><uniqueid type="tmdb">7</uniqueid></movie>`)
	touch(t, filepath.Join(d, "poster.jpg"), "x")
	ctx := context.Background()
	s.ScanVideo(ctx)
	items, total, _ := s.Repo.List(ctx, ListQuery{Kind: "movie"})
	if total != 1 || items[0].TmdbID != 7 || items[0].Poster == "" {
		t.Fatalf("%+v", items)
	}
}

func TestRatingForms(t *testing.T) {
	for in, want := range map[string]string{"16": "16+", "12": "12+", "PG13": "12+", "TV-Y7-FV": "6+", "TV-G": "0+", "Rated R": "16+", "RU-18+": "18+", "NR": "NR", "": ""} {
		if got := NormalizeRating(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
