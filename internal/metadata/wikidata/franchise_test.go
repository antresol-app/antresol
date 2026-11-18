package wikidata

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	if len(*asked) != 3 {
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
