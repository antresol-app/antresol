package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata/wikidata"
)

// A typical library (19.11: a viewer's server kept the Hobbit apart from
// the Lord of the Rings and Star Wars without its animated series): the
// whole Hobbit trilogy, the Lord of the Rings, Star Wars films of three
// trilogies, the Clone Wars film and the animated series, with Wikidata's
// facts recorded from the live Wikidata by the wikidata package's
// TestTitleFactsTypicalLibrary and TMDB's real collections.
type typicalLib struct {
	lotr, hobbit         []int64
	films, toons, series []int64
}

func typicalLibrary(t *testing.T, f *fixture) (typicalLib, *fakeCollections, *fakeFacts) {
	var l typicalLib
	movie := func(title string, year, tmdb int) int64 {
		return f.titled(f.movie(title, 150, itemOpt{year: year}), tmdb)
	}
	series := func(title string, year, tmdb int) int64 {
		s, _ := f.series(title, 1, 10, 25, itemOpt{year: year, genres: []string{"мультфильм"}})
		return f.titled(s, tmdb)
	}
	l.lotr = []int64{movie("Властелин колец: Братство Кольца", 2001, 120), movie("Властелин колец: Две крепости", 2002, 121), movie("Властелин колец: Возвращение короля", 2003, 122)}
	l.hobbit = []int64{movie("Хоббит: Нежданное путешествие", 2012, 49051), movie("Хоббит: Пустошь Смауга", 2013, 57158), movie("Хоббит: Битва пяти воинств", 2014, 122917)}
	l.films = []int64{movie("Звёздные войны: Эпизод 4", 1977, 11), movie("Звёздные войны: Эпизод 5", 1980, 1891), movie("Звёздные войны: Эпизод 6", 1983, 1892),
		movie("Звёздные войны: Эпизод 1", 1999, 1893), movie("Звёздные войны: Пробуждение силы", 2015, 140607)}
	l.toons = []int64{movie("Звёздные войны: Войны клонов", 2008, 12180)}
	l.series = []int64{series("Звёздные войны: Войны клонов", 2008, 4194), series("Звёздные войны: Повстанцы", 2014, 60554), series("Звёздные войны: Бракованная партия", 2021, 105971)}
	b, err := os.ReadFile("testdata/franchise-facts-typical.json")
	if err != nil {
		t.Fatal(err)
	}
	var facts map[string]map[int]wikidata.TitleFacts
	if err := json.Unmarshal(b, &facts); err != nil {
		t.Fatal(err)
	}
	// TMDB: the Clone Wars film is in no collection
	return l, &fakeCollections{
		of:    map[int]int{120: 119, 121: 119, 122: 119, 49051: 121938, 57158: 121938, 122917: 121938, 11: 10, 1891: 10, 1892: 10, 1893: 10, 140607: 10},
		names: map[int]string{119: "Властелин колец (Коллекция)", 121938: "Хоббит (Коллекция)", 10: "Звёздные войны (Коллекция)"},
		parts: map[int][]int{119: {120, 121, 122}, 121938: {49051, 57158, 122917}, 10: {11, 1891, 1892, 1893, 1894, 1895, 140607}},
	}, &fakeFacts{facts: facts}
}

func TestFranchisesTypicalLibrary(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm, facts := typicalLibrary(t, f)
	sw := slices.Concat(l.films, l.toons, l.series)

	// without Wikidata: the collections alone, nothing tells the Hobbit is
	// Middle-earth; the evidence says so
	fr := &Franchises{Store: f.st, TMDB: tm}
	fr.Pass(ctx, false)
	got := franchisesOf(t, f.st)
	if _, ok := got["c121938"]; !ok || len(got["c119"].Items) != 3 {
		t.Fatalf("without Wikidata %v", got)
	}
	if ev, _ := fr.Evidence(ctx); ev.Wikidata || ev.Tied != 0 || !ev.Untied[l.hobbit[0]] {
		t.Fatalf("evidence without Wikidata %+v", ev)
	}

	// with it: Middle-earth whole, Star Wars with the film and the series of no collection
	fr.Wiki = facts
	fr.Pass(ctx, true)
	got = franchisesOf(t, f.st)
	if me := got["c119"]; !slices.Equal(me.Items, slices.Concat(l.lotr, l.hobbit)) {
		t.Fatalf("middle-earth %+v", me)
	}
	if _, ok := got["c121938"]; ok {
		t.Fatal("the Hobbit is still apart")
	}
	if s := got["c10"]; len(s.Items) != len(sw) || slices.ContainsFunc(sw, func(id int64) bool { return !slices.Contains(s.Items, id) }) {
		t.Fatalf("star wars %+v, want %v", s, sw)
	}
	if ev, _ := fr.Evidence(ctx); !ev.Wikidata || ev.Read != ev.Titles || ev.Tied != ev.Titles {
		t.Fatalf("evidence %+v", ev)
	}

	// a light model that drops every Wikidata title as a spin-off: the
	// animated series share the franchise's own item with the films, they stay
	m := &fakeModel{answer: func(in agentInput) string {
		var drop []string
		for _, x := range in.Members {
			if x.Via != "collection" {
				drop = append(drop, fmt.Sprint(x.ID))
			}
		}
		return `{"name":"","order":[` + strings.Join(idsOf(in), ",") + `],"drop":[` + strings.Join(drop, ",") + `]}`
	}}
	model := m.llm(t)
	fr.Model = func(context.Context) *LLM { return model }
	fr.Pass(ctx, true)
	if s := franchisesOf(t, f.st)["c10"]; s.Source != FranchiseAI || len(s.Items) != len(sw) {
		t.Fatalf("star wars after the model %+v", s)
	}

	// the channel: Middle-earth (6 × 150 min) and Star Wars are under two
	// days, so no franchise channel; the animated series have no own channel either
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := f.st.All(ctx)
	for _, c := range all {
		if strings.HasPrefix(c.AutoKey, "franchise:") || strings.HasPrefix(c.AutoKey, "series:") {
			t.Errorf("channel %s under two days", c.AutoKey)
		}
	}
}
