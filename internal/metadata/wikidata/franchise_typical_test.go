package wikidata

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// A typical library's Middle-earth and Star Wars (19.11: a library with
// the whole Hobbit trilogy and Star Wars' animated series), recorded from
// the live Wikidata (VAULT_RECORD_WIKIDATA=1).
var (
	typicalMovies = []int{120, 121, 122, 49051, 57158, 122917, 11, 1891, 1892, 1893, 1894, 1895, 140607, 181808, 181812, 330459, 348350, 12180}
	typicalTV     = []int{4194, 60554, 105971, 82856, 83867, 4196, 79093, 114478, 203085, 114461}
)

func TestTitleFactsTypicalLibrary(t *testing.T) {
	c := fixtureClient(t)
	ctx := context.Background()
	movies, err := c.TitleFacts(ctx, "movie", typicalMovies, "ru")
	if err != nil {
		t.Fatal(err)
	}
	tv, err := c.TitleFacts(ctx, "tv", typicalTV, "ru")
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("VAULT_RECORD_WIKIDATA") == "1" { // the channels' tests replay these facts
		b, _ := json.MarshalIndent(map[string]map[int]TitleFacts{"movie": movies, "tv": tv}, "", " ")
		if err := os.WriteFile("../../channels/testdata/franchise-facts-typical.json", append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	has := func(f TitleFacts, q string) bool { return slices.Contains(anchorSet(f), q) }
	// the whole Hobbit trilogy and the Lord of the Rings: Middle-earth (through
	// their film series, P179: none of them has P8345 or P1434 of its own)
	for _, id := range []int{120, 121, 122, 49051, 57158, 122917} {
		if f := movies[id]; !has(f, "Q133830447") || len(f.Franchise) != 0 || len(f.Universe) != 0 {
			t.Errorf("movie %d %+v", id, f)
		}
	}
	// Star Wars: every film, the Clone Wars film and the animated series
	// (The Clone Wars, Rebels, The Bad Batch, Resistance, Visions, Tales of the
	// Jedi) share the franchise item, P8345 on each
	for _, id := range typicalMovies[6:] {
		if !has(movies[id], "Q462") {
			t.Errorf("movie %d %+v", id, movies[id])
		}
	}
	for _, id := range []int{4194, 60554, 105971, 82856, 83867, 79093, 114478, 203085, 114461} {
		if f := tv[id]; !has(f, "Q462") || f.Anchors[0].Rank != RankFranchise {
			t.Errorf("tv %d %+v", id, f)
		}
	}
	if _, ok := tv[4196]; ok { // an id no item has
		t.Errorf("tv 4196 %+v", tv[4196])
	}
}
