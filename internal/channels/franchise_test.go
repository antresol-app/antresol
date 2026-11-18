package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/metadata/wikidata"
)

type fakeCollections struct {
	mu      sync.Mutex
	of      map[int]int // movie → collection
	names   map[int]string
	parts   map[int][]int
	unknown map[int]bool // movies TMDB does not answer for
	calls   int
}

func (f *fakeCollections) MovieCollection(_ context.Context, id int) (int, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.unknown[id] {
		return 0, "", false
	}
	c := f.of[id]
	return c, f.names[c], true
}

func (f *fakeCollections) CollectionParts(_ context.Context, id int) (string, []int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.names[id], f.parts[id], true
}

func (f *fakeCollections) Language() string { return "ru-RU" }

type fakeFacts struct {
	facts map[string]map[int]wikidata.TitleFacts // kind → tmdb id → facts
	err   error
	calls int
}

func (f *fakeFacts) TitleFacts(_ context.Context, kind string, ids []int, _ string) (map[int]wikidata.TitleFacts, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := map[int]wikidata.TitleFacts{}
	for _, id := range ids {
		if x, ok := f.facts[kind][id]; ok {
			out[id] = x
		}
	}
	return out, nil
}

// fakeModel is an OpenAI-compatible endpoint answering with answer(input).
type fakeModel struct {
	mu     sync.Mutex
	asked  []agentInput
	answer func(in agentInput) string
}

func (m *fakeModel) llm(t *testing.T) *LLM {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Role, Content string }
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &body); err != nil || len(body.Messages) != 2 {
			t.Errorf("request %s", b)
		}
		var in agentInput
		if err := json.Unmarshal([]byte(body.Messages[1].Content), &in); err != nil {
			t.Errorf("input %v", err)
		}
		m.mu.Lock()
		m.asked = append(m.asked, in)
		m.mu.Unlock()
		out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": m.answer(in)}}}})
		w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return &LLM{URL: srv.URL + "/v1", Model: "m"}
}

// titled makes a movie or series with a TMDB id (and keywords).
func (f *fixture) titled(id int64, tmdb int, keywords ...string) int64 {
	meta, _ := json.Marshal(map[string]any{"keywords": keywords})
	f.exec(`UPDATE items SET tmdb_id=?, meta_json=? WHERE id=?`, tmdb, string(meta), id)
	return id
}

// starWars is a library of: three Star Wars films (collection 10, release
// order 11, 1891, 1893 = IV, V, I), two Harry Potter films (collection 20),
// a lone film, a film of a collection of one, and a Clone Wars series.
type swLib struct {
	iv, v, i, hp1, hp2, lone, single, clone int64
}

func starWars(f *fixture) (swLib, *fakeCollections) {
	var l swLib
	l.iv = f.titled(f.movie("Звёздные войны: Эпизод 4", 120, itemOpt{year: 1977}), 11)
	l.v = f.titled(f.movie("Звёздные войны: Эпизод 5", 120, itemOpt{year: 1980}), 1891)
	l.i = f.titled(f.movie("Звёздные войны: Эпизод 1", 130, itemOpt{year: 1999}), 1893)
	l.hp1 = f.titled(f.movie("Гарри Поттер и философский камень", 150, itemOpt{year: 2001}), 671)
	l.hp2 = f.titled(f.movie("Гарри Поттер и Тайная комната", 160, itemOpt{year: 2002}), 672)
	l.lone = f.titled(f.movie("Одинокий", 100, itemOpt{year: 2010}), 500)
	l.single = f.titled(f.movie("Один в коллекции", 100, itemOpt{year: 2011}), 501)
	s, _ := f.series("Звёздные войны: Войны клонов", 1, 10, 25, itemOpt{year: 2008})
	l.clone = f.titled(s, 4194)
	return l, &fakeCollections{
		of:    map[int]int{11: 10, 1891: 10, 1892: 10, 1893: 10, 671: 20, 672: 20, 501: 30},
		names: map[int]string{10: "Звёздные войны (Коллекция)", 20: "Гарри Поттер (Коллекция)", 30: "Одна коллекция"},
		parts: map[int][]int{10: {11, 1891, 1892, 1893}, 20: {671, 672, 673}, 30: {501}},
	}
}

func franchisesOf(t *testing.T, st *Store) map[string]Franchise {
	t.Helper()
	fs, err := st.Franchises(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Franchise{}
	for _, f := range fs {
		out[f.Key] = f
	}
	return out
}

func TestMergeOrder(t *testing.T) {
	for _, tc := range []struct{ manual, computed, want []int64 }{
		{nil, []int64{1, 2, 3}, []int64{1, 2, 3}},
		{[]int64{3, 1, 2}, []int64{1, 2, 3}, []int64{3, 1, 2}},
		{[]int64{3, 1}, []int64{1, 2, 3, 4}, []int64{3, 4, 1, 2}}, // new ones after their predecessors
		{[]int64{9, 2, 1}, []int64{0, 1, 2}, []int64{0, 2, 1}},    // a gone id drops, a new first one goes first
		{[]int64{2, 2, 1}, []int64{1, 2}, []int64{2, 1}},
	} {
		if got := MergeOrder(tc.manual, tc.computed); !slices.Equal(got, tc.want) {
			t.Errorf("MergeOrder(%v, %v) = %v, want %v", tc.manual, tc.computed, got, tc.want)
		}
	}
	f := Franchise{Items: []int64{1, 2, 3}, Manual: &FranchiseManual{Order: []int64{3, 2, 1}, Hidden: []int64{2}}}
	if got := f.Effective(); !slices.Equal(got, []int64{3, 1}) {
		t.Fatalf("effective %v", got)
	}
}

func TestFranchiseName(t *testing.T) {
	for in, want := range map[string]string{
		"Звёздные войны (Коллекция)":  "Звёздные войны",
		"Harry Potter Collection":     "Harry Potter",
		"Коллекция Матрица":           "Матрица",
		"Назад в будущее - Коллекция": "Назад в будущее",
		"Властелин колец":             "Властелин колец",
	} {
		if got := FranchiseName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestFranchisesBaseline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := starWars(f)
	fr := &Franchises{Store: f.st, TMDB: tm}
	ran, err := fr.Pass(ctx, false)
	if err != nil || !ran {
		t.Fatalf("pass: %v %v", ran, err)
	}
	got := franchisesOf(t, f.st)
	if len(got) != 2 {
		t.Fatalf("franchises %+v", got)
	}
	sw := got["c10"]
	if sw.Name != "Звёздные войны" || sw.Source != FranchiseTMDB || !slices.Equal(sw.Items, []int64{l.iv, l.v, l.i}) {
		t.Fatalf("star wars %+v", sw)
	}
	if hp := got["c20"]; !slices.Equal(hp.Items, []int64{l.hp1, l.hp2}) || hp.Name != "Гарри Поттер" {
		t.Fatalf("harry potter %+v", hp)
	}
	// nothing changed: nothing is asked
	calls := tm.calls
	if ran, err := fr.Pass(ctx, false); err != nil || ran || tm.calls != calls {
		t.Fatalf("second pass ran %v (%d calls) %v", ran, tm.calls-calls, err)
	}
	// a new title: a new pass
	f.titled(f.movie("Звёздные войны: Эпизод 6", 130, itemOpt{year: 1983}), 1892)
	if ran, _ := fr.Pass(ctx, false); !ran {
		t.Fatal("a new title did not recompute")
	}
	if sw := franchisesOf(t, f.st)["c10"]; len(sw.Items) != 4 || sw.Items[2] == l.i {
		t.Fatalf("release order with VI: %v", sw.Items)
	}
	// forced: runs anyway
	if ran, _ := fr.Pass(ctx, true); !ran {
		t.Fatal("forced pass did not run")
	}
}

func TestFranchisesIncompleteTriesAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, tm := starWars(f)
	tm.unknown = map[int]bool{672: true}
	fr := &Franchises{Store: f.st, TMDB: tm}
	if _, err := fr.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !fr.Status(ctx).Incomplete {
		t.Fatal("not incomplete")
	}
	if _, ok := franchisesOf(t, f.st)["c20"]; ok {
		t.Fatal("a one-film franchise")
	}
	tm.unknown = nil
	if ran, _ := fr.Pass(ctx, false); !ran {
		t.Fatal("an incomplete pass is not tried again")
	}
	if _, ok := franchisesOf(t, f.st)["c20"]; !ok || fr.Status(ctx).Incomplete {
		t.Fatal("harry potter after TMDB answered")
	}
	// TMDB failing again: what it does not answer for stays as it was
	tm.unknown = map[int]bool{671: true, 672: true}
	fr.Pass(ctx, true)
	if hp, ok := franchisesOf(t, f.st)["c20"]; !ok || len(hp.Items) != 2 {
		t.Fatalf("an incomplete pass pruned harry potter: %+v", hp)
	}
}

func TestFranchisesManualOrderSurvives(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := starWars(f)
	fr := &Franchises{Store: f.st, TMDB: tm}
	fr.Pass(ctx, false)
	if _, err := f.st.SetFranchiseOrder(ctx, "c10", &FranchiseManual{Order: []int64{l.lone}}); err != ErrFranchiseOrder {
		t.Fatalf("a title not of the franchise: %v", err)
	}
	if _, err := f.st.SetFranchiseOrder(ctx, "c10", &FranchiseManual{Order: []int64{l.i, l.iv, l.v}}); err != nil {
		t.Fatal(err)
	}
	vi := f.titled(f.movie("Звёздные войны: Эпизод 6", 130, itemOpt{year: 1983}), 1892)
	fr.Pass(ctx, false)
	sw := franchisesOf(t, f.st)["c10"]
	if want := []int64{l.i, l.iv, l.v, vi}; !slices.Equal(sw.Effective(), want) {
		t.Fatalf("effective %v, want %v", sw.Effective(), want)
	}
	// an ordered franchise TMDB no longer finds is kept (unused)
	tm.of = map[int]int{671: 20, 672: 20}
	fr.Pass(ctx, true)
	if _, ok := franchisesOf(t, f.st)["c10"]; !ok {
		t.Fatal("the admin's franchise was deleted")
	}
	if _, err := f.st.SetFranchiseOrder(ctx, "c10", nil); err != nil {
		t.Fatal(err)
	}
	fr.Pass(ctx, true)
	if _, ok := franchisesOf(t, f.st)["c10"]; ok {
		t.Fatal("a reset franchise TMDB no longer finds stays")
	}
}

func swFacts(l swLib) *fakeFacts {
	sw := []wikidata.Ref{{QID: "Q462", Label: "Звёздные войны"}}
	return &fakeFacts{facts: map[string]map[int]wikidata.TitleFacts{
		"movie": {11: {QID: "Q17738", Franchise: sw, FollowedBy: []string{"Q181795"}}, 1891: {QID: "Q181795", Franchise: sw, Follows: []string{"Q17738"}}},
		"tv":    {4194: {QID: "Q1", Franchise: sw}},
	}}
}

func TestFranchisesAgent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := starWars(f)
	m := &fakeModel{answer: func(in agentInput) string {
		if strings.Contains(in.Collections[0], "Звёздные") {
			// story order, the clone wars added, an unknown id, IV left out
			return fmt.Sprintf("```json\n{\"name\":\"Звёздные войны\",\"order\":[%d,%d,%d,999999],\"add\":[%d]}\n```", l.i, l.clone, l.v, l.clone)
		}
		return `{"name": "Гарри Поттер", "order": [` + itoa(l.hp1) + `,` + itoa(l.hp2) + `], "add": []}`
	}}
	model := m.llm(t)
	facts := swFacts(l)
	fr := &Franchises{Store: f.st, TMDB: tm, Wiki: facts, Model: func(context.Context) *LLM { return model }}
	if _, err := fr.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	sw := franchisesOf(t, f.st)["c10"]
	// IV goes back after its baseline predecessor: first (it was first)
	if want := []int64{l.iv, l.i, l.clone, l.v}; sw.Source != FranchiseAI || !slices.Equal(sw.Items, want) || sw.Error != "" {
		t.Fatalf("star wars %+v, want items %v", sw, want)
	}
	// what the model was given: members with Wikidata, the series as a candidate by Wikidata
	var swIn agentInput
	for _, in := range m.asked {
		if strings.Contains(in.Collections[0], "Звёздные") {
			swIn = in
		}
	}
	if len(swIn.Members) != 3 || len(swIn.Candidates) != 1 || swIn.Candidates[0].ID != l.clone || swIn.Candidates[0].Evidence != "wikidata" {
		t.Fatalf("input %+v", swIn)
	}
	if w := swIn.Members[0].Wikidata; w == nil || !slices.Equal(w.FollowedBy, []int64{l.v}) || w.Franchise[0] != "Звёздные войны" {
		t.Fatalf("evidence %+v", swIn.Members[0])
	}
	for _, c := range swIn.Candidates {
		if c.ID == l.lone || c.ID == l.single {
			t.Fatalf("an unrelated candidate %+v", c)
		}
	}
	// the facts are cached; an unchanged input is not asked again
	asked, wcalls := len(m.asked), facts.calls
	if _, err := fr.Pass(ctx, true); err != nil {
		t.Fatal(err)
	}
	if len(m.asked) != asked || facts.calls != wcalls {
		t.Fatalf("asked again: model %d, wikidata %d", len(m.asked)-asked, facts.calls-wcalls)
	}
	if sw2 := franchisesOf(t, f.st)["c10"]; !slices.Equal(sw2.Items, sw.Items) || sw2.Source != FranchiseAI {
		t.Fatalf("the stored answer was not kept: %+v", sw2)
	}
}

func TestFranchisesAgentFailures(t *testing.T) {
	for name, answer := range map[string]string{
		"prose":       "I cannot help with that",
		"extra field": `{"name":"x","order":[1],"add":[],"why":"..."}`,
		"made up ids": `{"name":"x","order":[900001,900002,900003],"add":[]}`,
		"empty":       `{"name":"x","order":[],"add":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			l, tm := starWars(f)
			model := (&fakeModel{answer: func(agentInput) string { return answer }}).llm(t)
			fr := &Franchises{Store: f.st, TMDB: tm, Model: func(context.Context) *LLM { return model }}
			if _, err := fr.Pass(ctx, false); err != nil {
				t.Fatal(err)
			}
			sw := franchisesOf(t, f.st)["c10"]
			if sw.Source != FranchiseTMDB || !slices.Equal(sw.Items, []int64{l.iv, l.v, l.i}) || sw.Error == "" {
				t.Fatalf("not the baseline with an error: %+v", sw)
			}
			if fr.Status(ctx).Error == "" {
				t.Fatal("no pass error")
			}
		})
	}
}

func TestFranchisesWikidataDownStillAsks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := starWars(f)
	m := &fakeModel{answer: func(in agentInput) string {
		ids := []string{}
		for _, x := range in.Members {
			ids = append(ids, itoa(x.ID))
		}
		return `{"name":"","order":[` + strings.Join(ids, ",") + `],"add":[]}`
	}}
	model := m.llm(t)
	fr := &Franchises{Store: f.st, TMDB: tm, Wiki: &fakeFacts{err: fmt.Errorf("down")}, Model: func(context.Context) *LLM { return model }}
	if _, err := fr.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	sw := franchisesOf(t, f.st)["c10"]
	if sw.Source != FranchiseAI || sw.Name != "Звёздные войны" {
		t.Fatalf("%+v", sw)
	}
	// the series is still a candidate: the collection's name is in its title
	for _, in := range m.asked {
		if strings.Contains(in.Collections[0], "Звёздные") && (len(in.Candidates) != 1 || in.Candidates[0].ID != l.clone || in.Candidates[0].Evidence != "title") {
			t.Fatalf("candidates %+v", in.Candidates)
		}
	}
}

func TestFranchiseClustersMerge(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	im := f.titled(f.movie("Железный человек", 120, itemOpt{year: 2008}), 1726, "marvel cinematic universe (mcu)")
	im2 := f.titled(f.movie("Железный человек 2", 120, itemOpt{year: 2010}), 10138, "marvel cinematic universe (mcu)")
	av := f.titled(f.movie("Мстители", 140, itemOpt{year: 2012}), 24428, "marvel cinematic universe (mcu)")
	av2 := f.titled(f.movie("Мстители: Эра Альтрона", 140, itemOpt{year: 2015}), 99861, "marvel cinematic universe (mcu)")
	s, _ := f.series("Локи", 1, 6, 50, itemOpt{year: 2021})
	loki := f.titled(s, 84958, "marvel cinematic universe (mcu)")
	other := f.titled(f.movie("Другое", 100, itemOpt{year: 2000}), 7, "superhero")
	f.titled(f.movie("Ещё", 100, itemOpt{year: 2001}), 8, "superhero")
	tm := &fakeCollections{of: map[int]int{1726: 131292, 10138: 131292, 24428: 86311, 99861: 86311},
		names: map[int]string{131292: "Железный человек (Коллекция)", 86311: "Мстители (Коллекция)"},
		parts: map[int][]int{131292: {1726, 10138}, 86311: {24428, 99861}}}
	mcu := []wikidata.Ref{{QID: "Q642878", Label: "Кинематографическая вселенная Marvel"}}
	facts := &fakeFacts{facts: map[string]map[int]wikidata.TitleFacts{"movie": {1726: {QID: "Qa", Universe: mcu}, 24428: {QID: "Qb", Universe: mcu}}}}
	m := &fakeModel{answer: func(in agentInput) string {
		ids := []string{}
		for _, x := range in.Members {
			ids = append(ids, itoa(x.ID))
		}
		for _, x := range in.Candidates {
			ids = append(ids, itoa(x.ID))
		}
		return `{"name":"Marvel","order":[` + strings.Join(ids, ",") + `],"add":[]}`
	}}
	model := m.llm(t)
	fr := &Franchises{Store: f.st, TMDB: tm, Wiki: facts, Model: func(context.Context) *LLM { return model }}
	if _, err := fr.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	got := franchisesOf(t, f.st)
	if len(got) != 1 || len(m.asked) != 1 {
		t.Fatalf("not one merged franchise: %+v (asked %d)", got, len(m.asked))
	}
	mf := got["c86311"]
	if want := []int64{im, im2, av, av2, loki}; mf.Name != "Marvel" || !slices.Equal(mf.Items, want) {
		t.Fatalf("marvel %+v, want %v", mf, want)
	}
	if c := m.asked[0].Candidates; len(c) != 1 || c[0].Evidence != "keyword" || c[0].ID == other {
		t.Fatalf("candidates %+v", c)
	}
}

func TestGeneratorFranchiseChannels(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := starWars(f)
	fr := &Franchises{Store: f.st, TMDB: tm}
	fr.Pass(ctx, false)
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := f.st.All(ctx)
	by := map[string]Channel{}
	for _, c := range all {
		by[c.AutoKey] = c
	}
	// star wars: 6 h 10 min; harry potter: 5 h 10 min; both at least 5 h
	sw, ok := by["franchise:c10"]
	if !ok {
		t.Fatalf("no star wars channel: %v", keys(by))
	}
	want := []Pick{{Kind: PickTitle, ID: l.iv}, {Kind: PickTitle, ID: l.v}, {Kind: PickTitle, ID: l.i}}
	if sw.Name != "Звёздные войны" || sw.Category != CatFranchises || !slices.Equal(sw.Rules.Picks, want) ||
		sw.Rules.Order != OrderOrdered || !sw.Rules.SkipSegments {
		t.Fatalf("star wars %+v", sw)
	}
	if _, ok := by["franchise:c20"]; !ok {
		t.Fatal("no harry potter channel")
	}
	// an admin's order reaches the channel; under the minimum it goes
	f.st.SetFranchiseOrder(ctx, "c10", &FranchiseManual{Order: []int64{l.i, l.iv, l.v}})
	g.FranchiseMinRuntime = 6*time.Hour + 5*time.Minute
	g.Sync(ctx)
	c, _ := f.st.Get(ctx, sw.ID)
	if c.Rules.Picks[0].ID != l.i {
		t.Fatalf("the admin's order: %+v", c.Rules.Picks)
	}
	hp, _ := f.st.Get(ctx, by["franchise:c20"].ID)
	if hp.Enabled {
		t.Fatal("a franchise under the minimum is still on")
	}
	// the schedule plays the picks in order
	b := f.builder()
	if err := b.Build(ctx, c); err != nil {
		t.Fatal(err)
	}
	es := f.entries(c.ID)
	if len(es) < 3 || es[0].ItemID != l.i || es[1].ItemID != l.iv || es[2].ItemID != l.v {
		t.Fatalf("schedule %+v", es[:min(3, len(es))])
	}
}
