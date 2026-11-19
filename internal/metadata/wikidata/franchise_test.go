package wikidata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata"
)

// A fake Wikidata: two films found by their TMDB movie ids, one of them
// part of a series with an ordinal, following the other; their franchise's
// and universe's labels.
func fakeFranchiseWiki(t *testing.T) (*Client, *[]string) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		asked = append(asked, q.Get("action")+" "+q.Get("srsearch")+q.Get("ids"))
		switch {
		case q.Get("action") == "query":
			if q.Get("srsearch") != "haswbstatement:P4947=11|P4947=1891|P4947=999" {
				t.Errorf("search %q", q.Get("srsearch"))
			}
			w.Write([]byte(`{"query":{"search":[{"title":"Q17738"},{"title":"Q181795"},{"title":"Property:P1"}]}}`))
		case q.Get("action") == "wbgetentities" && q.Get("props") == "claims":
			w.Write([]byte(`{"entities":{
			 "Q17738":{"id":"Q17738","claims":{"P4947":[{"mainsnak":{"datavalue":{"value":"11"}}}],
			   "P8345":[{"mainsnak":{"datavalue":{"value":{"id":"Q462"}}}}],
			   "P1434":[{"mainsnak":{"datavalue":{"value":{"id":"Q13417250"}}}}],
			   "P156":[{"mainsnak":{"datavalue":{"value":{"id":"Q181795"}}}}]}},
			 "Q181795":{"id":"Q181795","claims":{"P4947":[{"mainsnak":{"datavalue":{"value":"1891"}}}],
			   "P179":[{"mainsnak":{"datavalue":{"value":{"id":"Q22092344"}}},"qualifiers":{"P1545":[{"datavalue":{"value":"5"}}]}}],
			   "P155":[{"mainsnak":{"datavalue":{"value":{"id":"Q17738"}}}}],
			   "P8345":[{"rank":"deprecated","mainsnak":{"datavalue":{"value":{"id":"Q1"}}}}]}}}}`))
		case q.Get("action") == "wbgetentities" && q.Get("props") == "labels":
			w.Write([]byte(`{"entities":{"Q462":{"id":"Q462","labels":{"ru":{"value":"Звёздные войны"},"en":{"value":"Star Wars"}}},
			 "Q13417250":{"id":"Q13417250","labels":{"en":{"value":"Star Wars universe"}}},
			 "Q22092344":{"id":"Q22092344","labels":{"en":{"value":"Skywalker saga"}}}}}`))
		default:
			t.Errorf("unexpected %s", r.URL.RawQuery)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Limiter: &metadata.Limiter{}}, &asked
}

func TestTitleFacts(t *testing.T) {
	c, asked := fakeFranchiseWiki(t)
	got, err := c.TitleFacts(context.Background(), "movie", []int{11, 1891, 999}, "ru")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d facts: %+v", len(got), got)
	}
	a, b := got[11], got[1891]
	if a.QID != "Q17738" || len(a.Franchise) != 1 || a.Franchise[0].Label != "Звёздные войны" || a.Universe[0].Label != "Star Wars universe" ||
		strings.Join(a.FollowedBy, ",") != "Q181795" {
		t.Fatalf("a: %+v", a)
	}
	if b.QID != "Q181795" || len(b.Series) != 1 || b.Series[0].Label != "Skywalker saga" || b.Series[0].Ordinal != "5" ||
		strings.Join(b.Follows, ",") != "Q17738" || len(b.Franchise) != 0 {
		t.Fatalf("b: %+v", b)
	}
	// the titles' search and claims, their links' claims (anchors looked
	// for: this fake has none), the labels
	if len(*asked) != 4 {
		t.Fatalf("requests: %q", *asked)
	}
}

func TestTitleFactsKinds(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", Limiter: &metadata.Limiter{}}
	if _, err := c.TitleFacts(context.Background(), "book", []int{1}, "ru"); err == nil {
		t.Fatal("a book kind was asked")
	}
	if got, err := c.TitleFacts(context.Background(), "tv", nil, "ru"); err != nil || len(got) != 0 {
		t.Fatalf("nothing asked: %v %v", got, err)
	}
}

// The franchise library of the channels' tests, recorded from the live
// Wikidata (VAULT_RECORD_WIKIDATA=1): TMDB movie and series ids.
var (
	anchorMovies = []int{11, 1891, 1893, 120, 121, 122, 49051, 57158, 1726, 24428, 10195, 953, 27205, 808, 809}
	anchorTV     = []int{82856, 83867, 84773, 85271, 84958, 68595, 1396}
)

// anchorSet is the anchors' QIDs of a title.
func anchorSet(f TitleFacts) []string {
	var out []string
	for _, r := range f.Anchors {
		out = append(out, r.QID)
	}
	return out
}

func TestTitleFactsAnchorsRecorded(t *testing.T) {
	c := fixtureClient(t)
	ctx := context.Background()
	movies, err := c.TitleFacts(ctx, "movie", anchorMovies, "ru")
	if err != nil {
		t.Fatal(err)
	}
	tv, err := c.TitleFacts(ctx, "tv", anchorTV, "ru")
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("VAULT_RECORD_WIKIDATA") == "1" { // the channels' tests replay these facts
		b, _ := json.MarshalIndent(map[string]map[int]TitleFacts{"movie": movies, "tv": tv}, "", " ")
		if err := os.WriteFile("../../channels/testdata/franchise-facts.json", append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range anchorMovies {
		t.Logf("movie %d %s %v", id, movies[id].QID, movies[id].Anchors)
	}
	for _, id := range anchorTV {
		t.Logf("tv %d %s %v", id, tv[id].QID, tv[id].Anchors)
	}
	shares := func(a, b TitleFacts) bool {
		return slices.ContainsFunc(anchorSet(a), func(q string) bool { return slices.Contains(anchorSet(b), q) })
	}
	// Star Wars: the films and the series share the franchise item
	for _, x := range []TitleFacts{movies[1891], movies[1893], tv[82856], tv[83867]} {
		if !shares(movies[11], x) {
			t.Errorf("not Star Wars: %+v", x)
		}
	}
	// Middle-earth: the Lord of the Rings, the Hobbit (through their film
	// series) and the Rings of Power (through the screen adaptations)
	for _, x := range []TitleFacts{movies[121], movies[122], movies[49051], movies[57158], tv[84773]} {
		if !shares(movies[120], x) {
			t.Errorf("not Middle-earth: %+v", x)
		}
	}
	// Marvel Cinematic Universe
	for _, x := range []TitleFacts{movies[24428], movies[10195], tv[85271], tv[84958]} {
		if !shares(movies[1726], x) {
			t.Errorf("not Marvel: %+v", x)
		}
	}
	// unrelated: none shares with another franchise
	groups := []TitleFacts{movies[11], movies[120], movies[1726], movies[808]}
	for _, x := range []TitleFacts{movies[953], movies[27205], tv[68595], tv[1396]} {
		for _, g := range groups {
			if shares(g, x) {
				t.Errorf("%s shares an anchor with %s: %v %v", x.QID, g.QID, x.Anchors, g.Anchors)
			}
		}
	}
	if len(movies[27205].Anchors) != 0 || len(tv[68595].Anchors) != 0 {
		t.Errorf("anchors of Inception %v, of Planet Earth II %v", movies[27205].Anchors, tv[68595].Anchors)
	}
	if a := movies[11].Anchors; len(a) == 0 || a[0].QID != "Q462" || a[0].Rank != RankFranchise || a[0].Label == "" {
		t.Errorf("Star Wars's first anchor %+v", a)
	}
	if movies[11].Version != FactsVersion {
		t.Errorf("version %d", movies[11].Version)
	}
}
