package wikidata

import (
	"context"
	"encoding/json"
	"testing"
)

// The fixtures of these titles are live answers recorded in 2026-10
// (VAULT_RECORD_WIKIDATA=1), the entities trimmed to their P345 and P444
// claims.
func TestRottenTomatoesFromWikidata(t *testing.T) {
	c := fixtureClient(t)
	for _, tc := range []struct {
		imdb string
		want ReviewScores
	}{
		{"tt15239678", ReviewScores{Tomatometer: 92}}, // Dune: Part Two
		{"tt1160419", ReviewScores{Tomatometer: 83}},  // Dune: "83%" and the average "7.6/10"
		{"tt0944947", ReviewScores{Tomatometer: 89}},  // Game of Thrones: "89%" without a method
		{"tt0903747", ReviewScores{}},                 // Breaking Bad: no Rotten Tomatoes statement
		{"tt9999999999", ReviewScores{}},              // no item
	} {
		got, err := c.RottenTomatoes(context.Background(), tc.imdb)
		if err != nil {
			t.Fatalf("%s: %v", tc.imdb, err)
		}
		if got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.imdb, got, tc.want)
		}
	}
}

func TestRottenTomatoesPicksStatements(t *testing.T) {
	const raw = `{"claims":{"P444":[
	 {"rank":"normal","mainsnak":{"datavalue":{"value":"71%"}},"qualifiers":{"P447":[{"datavalue":{"value":{"id":"Q105584"}}}],"P459":[{"datavalue":{"value":{"id":"Q108403393"}}}],"P585":[{"datavalue":{"value":{"time":"+2020-01-01T00:00:00Z"}}}]}},
	 {"rank":"normal","mainsnak":{"datavalue":{"value":"75%"}},"qualifiers":{"P447":[{"datavalue":{"value":{"id":"Q105584"}}}],"P459":[{"datavalue":{"value":{"id":"Q108403393"}}}],"P585":[{"datavalue":{"value":{"time":"+2024-01-01T00:00:00Z"}}}]}},
	 {"rank":"deprecated","mainsnak":{"datavalue":{"value":"10%"}},"qualifiers":{"P447":[{"datavalue":{"value":{"id":"Q105584"}}}],"P585":[{"datavalue":{"value":{"time":"+2026-01-01T00:00:00Z"}}}]}},
	 {"rank":"normal","mainsnak":{"datavalue":{"value":"64%"}},"qualifiers":{"P447":[{"datavalue":{"value":{"id":"Q105584"}}}],"P459":[{"datavalue":{"value":{"id":"Q131100566"}}}]}},
	 {"rank":"normal","mainsnak":{"datavalue":{"value":"99%"}},"qualifiers":{"P447":[{"datavalue":{"value":{"id":"Q59315430"}}}]}},
	 {"rank":"normal","mainsnak":{"datavalue":{"value":"8.1/10"}},"qualifiers":{"P447":[{"datavalue":{"value":{"id":"Q105584"}}}]}}
	]}}`
	var e entity
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	if got := e.rottenTomatoes(); got != (ReviewScores{Tomatometer: 75, Audience: 64}) {
		t.Fatalf("latest normal: %+v", got)
	}
	// a preferred statement wins over a later normal one
	e.Claims["P444"][0].Rank = "preferred"
	if got := e.rottenTomatoes(); got.Tomatometer != 71 {
		t.Fatalf("preferred: %+v", got)
	}
}
