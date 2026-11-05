package library

import (
	"context"
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
