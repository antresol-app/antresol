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

// recordedFacts are Wikidata's facts of the evidence library, recorded
// from the live Wikidata by the wikidata package's
// TestTitleFactsAnchorsRecorded (VAULT_RECORD_WIKIDATA=1).
func recordedFacts(t *testing.T) *fakeFacts {
	b, err := os.ReadFile("testdata/franchise-facts.json")
	if err != nil {
		t.Fatal(err)
	}
	var facts map[string]map[int]wikidata.TitleFacts
	if err := json.Unmarshal(b, &facts); err != nil {
		t.Fatal(err)
	}
	return &fakeFacts{facts: facts}
}

// evidenceLib is a library of Star Wars films and series, the Lord of the
// Rings, the Hobbit and the Rings of Power, a part of Marvel (one film of
// each collection in the library), Shrek, and unrelated titles: Madagascar
// (a collection of one), Inception, Planet Earth II, Breaking Bad.
type evidenceLib struct {
	iv, v, i, mando, andor                    int64
	fellowship, towers, king, hob1, hob2, rop int64
	ironMan, thor, avengers, wanda, loki      int64
	shrek, shrek2                             int64
	madagascar, inception, planet, bad        int64
}

func evidenceLibrary(f *fixture) (evidenceLib, *fakeCollections) {
	var l evidenceLib
	movie := func(title string, year, tmdb int) int64 {
		return f.titled(f.movie(title, 120, itemOpt{year: year}), tmdb)
	}
	series := func(title string, year, tmdb int) int64 {
		s, _ := f.series(title, 1, 8, 45, itemOpt{year: year})
		return f.titled(s, tmdb)
	}
	l.iv = movie("Звёздные войны: Эпизод 4 – Новая надежда", 1977, 11)
	l.v = movie("Звёздные войны: Эпизод 5 – Империя наносит ответный удар", 1980, 1891)
	l.i = movie("Звёздные войны: Эпизод 1 – Скрытая угроза", 1999, 1893)
	l.mando = series("Мандалорец", 2019, 82856)
	l.andor = series("Андор", 2022, 83867)
	l.fellowship = movie("Властелин колец: Братство Кольца", 2001, 120)
	l.towers = movie("Властелин колец: Две крепости", 2002, 121)
	l.king = movie("Властелин колец: Возвращение короля", 2003, 122)
	l.hob1 = movie("Хоббит: Нежданное путешествие", 2012, 49051)
	l.hob2 = movie("Хоббит: Пустошь Смауга", 2013, 57158)
	l.rop = series("Властелин колец: Кольца власти", 2022, 84773)
	l.ironMan = movie("Железный человек", 2008, 1726)
	l.thor = movie("Тор", 2011, 10195)
	l.avengers = movie("Мстители", 2012, 24428)
	l.wanda = series("ВандаВижен", 2021, 85271)
	l.loki = series("Локи", 2021, 84958)
	l.shrek = movie("Шрек", 2001, 808)
	l.shrek2 = movie("Шрек 2", 2004, 809)
	l.madagascar = movie("Мадагаскар", 2005, 953)
	l.inception = movie("Начало", 2010, 27205)
	l.planet = series("Планета Земля 2", 2016, 68595)
	l.bad = series("Во все тяжкие", 2008, 1396)
	// TMDB's collections of these films
	return l, &fakeCollections{
		of: map[int]int{11: 10, 1891: 10, 1893: 10, 120: 119, 121: 119, 122: 119, 49051: 121938, 57158: 121938,
			1726: 131292, 10195: 131296, 24428: 86311, 808: 2150, 809: 2150, 953: 14740},
		names: map[int]string{10: "Звёздные войны (Коллекция)", 119: "Властелин колец (Коллекция)", 121938: "Хоббит (Коллекция)",
			131292: "Железный человек (Коллекция)", 131296: "Тор (Коллекция)", 86311: "Мстители (Коллекция)", 2150: "Шрек (Коллекция)",
			14740: "Мадагаскар (Коллекция)"},
		parts: map[int][]int{10: {11, 1891, 1892, 1893, 1894, 1895}, 119: {120, 121, 122}, 121938: {49051, 57158, 122917},
			131292: {1726, 10138, 68721}, 131296: {10195, 76338}, 86311: {24428, 99861}, 2150: {808, 809, 810}, 14740: {953, 10527}},
	}
}

// What forms on the evidence library from the recorded Wikidata facts and
// TMDB's collections: Star Wars with its series, Middle-earth with the
// Hobbit and the Rings of Power, Marvel from films of three collections of
// one and its series, Shrek; nothing of the unrelated titles.
func TestFranchisesFromRecordedEvidence(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := evidenceLibrary(f)
	fr := &Franchises{Store: f.st, TMDB: tm, Wiki: recordedFacts(t)}
	if _, err := fr.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	got := franchisesOf(t, f.st)
	for k, x := range got {
		t.Logf("%s %q %s %v", k, x.Name, x.Source, x.Items)
	}
	want := map[string]Franchise{
		"c10":      {Name: "Звёздные войны", Source: FranchiseWikidata, Items: []int64{l.iv, l.v, l.i, l.mando, l.andor}},
		"c119":     {Name: "Средиземье", Source: FranchiseWikidata, Items: []int64{l.fellowship, l.towers, l.king, l.hob1, l.hob2, l.rop}},
		"wQ642878": {Name: "Кинематографическая вселенная Marvel", Source: FranchiseWikidata, Items: []int64{l.ironMan, l.thor, l.avengers, l.wanda, l.loki}},
		"c2150":    {Name: "Шрек", Source: FranchiseTMDB, Items: []int64{l.shrek, l.shrek2}},
	}
	if len(got) != len(want) {
		t.Fatalf("%d franchises, want %d", len(got), len(want))
	}
	for k, w := range want {
		g := got[k]
		if g.Name != w.Name || g.Source != w.Source || !slices.Equal(g.Items, w.Items) {
			t.Errorf("%s: %q %s %v, want %q %s %v", k, g.Name, g.Source, g.Items, w.Name, w.Source, w.Items)
		}
	}
	for _, x := range got {
		for _, id := range []int64{l.madagascar, l.inception, l.planet, l.bad} {
			if slices.Contains(x.Items, id) {
				t.Errorf("an unrelated title %d in %s", id, x.Key)
			}
		}
	}

	// with the model: it only orders (the story's time), what it was given
	// is the evidence's franchise
	m := &fakeModel{answer: func(in agentInput) string {
		if strings.Contains(in.Franchise, "Звёздные") { // I, Andor, IV, V, the Mandalorian
			return fmt.Sprintf(`{"name":"Звёздные войны","order":[%d,%d,%d,%d,%d],"drop":[]}`, l.i, l.andor, l.iv, l.v, l.mando)
		}
		return `{"name":"","order":[` + strings.Join(idsOf(in), ",") + `],"drop":[]}`
	}}
	model := m.llm(t)
	fr.Model = func(context.Context) *LLM { return model }
	if _, err := fr.Pass(ctx, true); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	asked := m.asked
	m.mu.Unlock()
	if len(asked) != 4 {
		t.Fatalf("asked %d times", len(asked))
	}
	sw := franchisesOf(t, f.st)["c10"]
	if sw.Source != FranchiseAI || !slices.Equal(sw.Items, []int64{l.i, l.andor, l.iv, l.v, l.mando}) {
		t.Fatalf("story order %+v", sw)
	}
	for _, in := range asked {
		if strings.Contains(in.Franchise, "Средиземье") && (len(in.Members) != 6 || in.Members[5].Via != "wikidata" || len(in.Collections) != 2) {
			t.Fatalf("middle-earth input %+v", in)
		}
	}
}
