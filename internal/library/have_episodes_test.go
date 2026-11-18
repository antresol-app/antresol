package library

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/antresol-app/antresol/internal/db"
)

func TestHaveEpisodesCountsRanges(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	r := NewRepo(d)
	ctx := context.Background()
	dir := t.TempDir()
	for _, c := range []struct {
		s, e int
		name string
	}{{1, 1, "Сёгун - S01E01-E02.mkv"}, {1, 3, "Shogun.S01E03E04.mkv"}, {2, 1, "Shogun.S02E01.mkv"}} {
		if _, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Сёгун", SeriesTmdbID: 42, Season: c.s, Episode: c.e,
			File: FileInput{Path: filepath.Join(dir, c.name), Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	have, err := r.HaveEpisodes(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	want := map[[2]int]bool{{1, 1}: true, {1, 2}: true, {1, 3}: true, {1, 4}: true, {2, 1}: true}
	if !reflect.DeepEqual(have, want) {
		t.Fatalf("got %v", have)
	}
	if have, err := r.HaveEpisodes(ctx, 7); err != nil || len(have) != 0 {
		t.Fatalf("%v %v", have, err)
	}
}

func TestEpisodeFilesPicksBestFile(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	r := NewRepo(d)
	ctx := context.Background()
	dir := t.TempDir()
	ep := map[string]int64{}
	for _, name := range []string{"Shogun.S01E01.720p.mkv", "Shogun.S01E03E04.mkv"} {
		var e int
		if name[7:13] == "S01E01" {
			e = 1
		} else {
			e = 3
		}
		id, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Сёгун", SeriesTmdbID: 42, Season: 1, Episode: e,
			File: FileInput{Path: filepath.Join(dir, name), Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		ep[name] = id
	}
	// a second, better file on S01E01
	if _, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Сёгун", SeriesTmdbID: 42, Season: 1, Episode: 1,
		File: FileInput{Path: filepath.Join(dir, "Shogun.S01E01.1080p.mkv"), Size: 1}}); err != nil {
		t.Fatal(err)
	}
	fs, _ := r.FilesOf(ctx, ep["Shogun.S01E01.720p.mkv"])
	if len(fs) != 2 {
		t.Fatalf("files %+v", fs)
	}
	for _, f := range fs {
		h := 720
		if filepath.Base(f.Path) == "Shogun.S01E01.1080p.mkv" {
			h = 1080
		}
		js := fmt.Sprintf(`{"Video":{"Width":%d,"Height":%d}}`, h*16/9, h)
		if err := r.SetProbe(ctx, f.ID, js, 1000); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.EpisodeFiles(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	best := fs[0]
	if filepath.Base(fs[1].Path) == "Shogun.S01E01.1080p.mkv" {
		best = fs[1]
	}
	if g := got[[2]int{1, 1}]; g.ItemID != ep["Shogun.S01E01.720p.mkv"] || g.FileID != best.ID || g.Resolution != 1080 {
		t.Fatalf("S01E01 %+v want file %d", g, best.ID)
	}
	if a, b := got[[2]int{1, 3}], got[[2]int{1, 4}]; a.ItemID == 0 || a != b {
		t.Fatalf("range %+v %+v", a, b)
	}
	if g, err := r.EpisodeFiles(ctx, 7); err != nil || len(g) != 0 {
		t.Fatalf("%v %v", g, err)
	}
}

func TestResolutionOf(t *testing.T) {
	for _, c := range []struct {
		w, h, want int
	}{{3840, 2160, 2160}, {1920, 1080, 1080}, {1920, 800, 1080}, {1280, 720, 720}, {720, 480, 480}, {0, 0, 0}} {
		if got := ResolutionOf(File{ProbeJSON: fmt.Sprintf(`{"Video":{"Width":%d,"Height":%d}}`, c.w, c.h)}); got != c.want {
			t.Errorf("%dx%d: %d want %d", c.w, c.h, got, c.want)
		}
	}
	if ResolutionOf(File{}) != 0 {
		t.Error("no probe")
	}
}

// EpisodeItems is HaveEpisodes with the episode item that holds each
// pair's file: a range file's later episodes carry its item, an episode of
// its own wins over a range that also covers it, and a missing file, item
// or season leaves the pair out.
func TestEpisodeItems(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	r := NewRepo(d)
	ctx := context.Background()
	dir := t.TempDir()
	add := func(season, ep int, name string) int64 {
		t.Helper()
		id, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Сёгун", SeriesTmdbID: 42, Season: season, Episode: ep,
			File: FileInput{Path: filepath.Join(dir, name), Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	s2e1 := add(2, 1, "Shogun.S02E01.mkv") // added first: the order of the answer is not the order of insertion
	s1e1 := add(1, 1, "Сёгун - S01E01-E02.mkv")
	s1e3 := add(1, 3, "Shogun.S01E03E04.mkv")
	var series int64
	if err := d.QueryRow(`SELECT parent_id FROM items WHERE id=(SELECT parent_id FROM items WHERE id=?)`, s1e1).Scan(&series); err != nil {
		t.Fatal(err)
	}
	if series == 0 {
		t.Fatal("no series")
	}
	ref := func(season, ep int, item int64) EpisodeRef {
		return EpisodeRef{Season: season, Episode: ep, ItemID: item, SeriesID: series}
	}

	got, err := r.EpisodeItems(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	want := []EpisodeRef{ref(1, 1, s1e1), ref(1, 2, s1e1), ref(1, 3, s1e3), ref(1, 4, s1e3), ref(2, 1, s2e1)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	// the pairs are HaveEpisodes' pairs
	have, err := r.HaveEpisodes(ctx, 42)
	if err != nil || len(have) != len(got) {
		t.Fatalf("have %v %v", have, err)
	}
	for _, e := range got {
		if !have[[2]int{e.Season, e.Episode}] {
			t.Errorf("S%dE%d is not in HaveEpisodes", e.Season, e.Episode)
		}
	}

	// an episode of its own, with its own file, beats the range over it
	s1e2 := add(1, 2, "Shogun.S01E02.mkv")
	if got, err = r.EpisodeItems(ctx, 42); err != nil || len(got) != 5 || got[1] != ref(1, 2, s1e2) || got[0] != ref(1, 1, s1e1) {
		t.Fatalf("own episode: %+v %v", got, err)
	}

	// a missing file takes its pair (and the range's pairs only with it)
	if _, err := d.Exec(`UPDATE files SET missing_since='2026-01-01T00:00:00Z' WHERE item_id=?`, s1e3); err != nil {
		t.Fatal(err)
	}
	if got, err = r.EpisodeItems(ctx, 42); err != nil || len(got) != 3 || got[2] != ref(2, 1, s2e1) {
		t.Fatalf("missing file: %+v %v", got, err)
	}
	// a missing episode item, and a missing season
	if _, err := d.Exec(`UPDATE items SET missing_since='2026-01-01T00:00:00Z' WHERE id=?`, s1e1); err != nil {
		t.Fatal(err)
	}
	if got, err = r.EpisodeItems(ctx, 42); err != nil || len(got) != 2 || got[0] != ref(1, 2, s1e2) {
		t.Fatalf("missing episode: %+v %v", got, err)
	}
	if _, err := d.Exec(`UPDATE items SET missing_since='2026-01-01T00:00:00Z' WHERE id=(SELECT parent_id FROM items WHERE id=?)`, s2e1); err != nil {
		t.Fatal(err)
	}
	if got, err = r.EpisodeItems(ctx, 42); err != nil || len(got) != 1 || got[0] != ref(1, 2, s1e2) {
		t.Fatalf("missing season: %+v %v", got, err)
	}
	// a series the library lacks: none
	if got, err := r.EpisodeItems(ctx, 7); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}
