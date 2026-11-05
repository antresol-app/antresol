package library

import (
	"context"
	"testing"
)

func TestSearchSeriesYoWildcardsAndEpisodes(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Ёжик в тумане", Season: 1, Episode: 1, File: FileInput{Path: "/s/1.mkv"}})
	r.UpsertMovie(ctx, MovieInput{Title: "100% жизни", File: FileInput{Path: "/m.mkv"}})
	r.UpsertMovie(ctx, MovieInput{Title: "Другое", File: FileInput{Path: "/o.mkv"}})
	got, _ := r.Search(ctx, "ежик", 10)
	if len(got) != 1 || got[0].Kind != KindSeries {
		t.Fatalf("series by е for ё: %v", titles(got))
	}
	for _, q := range []string{"%", "_", "%%", "*", "a OR b", "(", "-"} {
		if got, err := r.Search(ctx, q, 10); err != nil || len(got) > 1 {
			t.Fatalf("%q: %v %v", q, titles(got), err)
		}
	}
	if got, _ := r.Search(ctx, "тумане", 10); len(got) != 1 {
		t.Fatalf("episodes must not be listed: %v", titles(got))
	}
}
