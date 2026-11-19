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
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/metadata/wikidata"
)

// flakyModel is an OpenAI-compatible endpoint that can be switched to
// failing (503) and counts its calls.
type flakyModel struct {
	fail  atomic.Bool
	calls atomic.Int32
}

func (m *flakyModel) llm(t *testing.T, answer func(in agentInput) string) *LLM {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.calls.Add(1)
		if m.fail.Load() {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Messages []struct{ Role, Content string }
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		var in agentInput
		_ = json.Unmarshal([]byte(body.Messages[1].Content), &in)
		out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer(in)}}}})
		w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return &LLM{URL: srv.URL + "/v1", Model: "m"}
}

// everyID answers with all the ids the model was given, in the given order.
func everyID(in agentInput) string {
	var ids []string
	for _, x := range in.Members {
		ids = append(ids, itoa(x.ID))
	}
	return `{"name":"","order":[` + strings.Join(ids, ",") + `],"drop":[]}`
}

func enabledFranchiseChannels(t *testing.T, st *Store) map[string]Channel {
	t.Helper()
	all, err := st.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Channel{}
	for _, c := range all {
		if strings.HasPrefix(c.AutoKey, "franchise:") && c.Enabled {
			out[c.AutoKey] = c
		}
	}
	return out
}

// The same three films come under another collection key (a collection with
// a smaller id, a cluster merge): the admin's order goes with them and the
// old key makes no second channel.
func TestFranchiseKeyChangeCarriesOrder(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := starWars(f)
	fr := &Franchises{Store: f.st, TMDB: tm}
	fr.Pass(ctx, false)
	if _, err := f.st.SetFranchiseOrder(ctx, "c10", &FranchiseManual{Order: []int64{l.i, l.iv, l.v}}); err != nil {
		t.Fatal(err)
	}
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := enabledFranchiseChannels(t, f.st)["franchise:c10"]; !ok {
		t.Fatal("no channel before the key changes")
	}
	tm.of = map[int]int{11: 5, 1891: 5, 1893: 5, 671: 20, 672: 20}
	tm.names[5] = "Star Wars (Collection)"
	tm.parts[5] = []int{11, 1891, 1893}
	if _, err := fr.Pass(ctx, true); err != nil {
		t.Fatal(err)
	}
	got := franchisesOf(t, f.st)
	if _, ok := got["c10"]; ok {
		t.Fatalf("the old key stays: %+v", got["c10"])
	}
	sw := got["c5"]
	if sw.Manual == nil || !slices.Equal(sw.Effective(), []int64{l.i, l.iv, l.v}) {
		t.Fatalf("the admin's order did not move to the new key: %+v effective %v", sw.Manual, sw.Effective())
	}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	chans := enabledFranchiseChannels(t, f.st)
	if len(chans) != 2 || chans["franchise:c5"].Rules.Picks[0].ID != l.i {
		t.Fatalf("channels %v", chans)
	}
	for k, c := range chans {
		if k == "franchise:c10" {
			t.Fatalf("a channel of the old key plays: %+v", c)
		}
	}
}

// Franchises merged into a cluster (Wikidata, the model): the order of the
// one that vanished moves to the cluster.
func TestFranchiseClusterMergeCarriesOrder(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	im := f.titled(f.movie("Железный человек", 120, itemOpt{year: 2008}), 1726)
	im2 := f.titled(f.movie("Железный человек 2", 120, itemOpt{year: 2010}), 10138)
	av := f.titled(f.movie("Мстители", 140, itemOpt{year: 2012}), 24428)
	av2 := f.titled(f.movie("Мстители: Эра Альтрона", 140, itemOpt{year: 2015}), 99861)
	tm := &fakeCollections{of: map[int]int{1726: 131292, 10138: 131292, 24428: 86311, 99861: 86311},
		names: map[int]string{131292: "Железный человек (Коллекция)", 86311: "Мстители (Коллекция)"},
		parts: map[int][]int{131292: {1726, 10138}, 86311: {24428, 99861}}}
	fr := &Franchises{Store: f.st, TMDB: tm}
	if _, err := fr.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := franchisesOf(t, f.st); len(got) != 2 {
		t.Fatalf("baseline %+v", got)
	}
	if _, err := f.st.SetFranchiseOrder(ctx, "c131292", &FranchiseManual{Order: []int64{im2, im}}); err != nil {
		t.Fatal(err)
	}
	mcu := []wikidata.Ref{{QID: "Q642878", Label: "Кинематографическая вселенная Marvel"}}
	fr.Wiki = &fakeFacts{facts: map[string]map[int]wikidata.TitleFacts{"movie": {1726: {QID: "Qa", Anchors: mcu}, 24428: {QID: "Qb", Anchors: mcu}}}}
	model := (&fakeModel{answer: func(in agentInput) string {
		return `{"name":"Marvel","order":[` + itoa(im) + `,` + itoa(im2) + `,` + itoa(av) + `,` + itoa(av2) + `],"drop":[]}`
	}}).llm(t)
	fr.Model = func(context.Context) *LLM { return model }
	if _, err := fr.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	got := franchisesOf(t, f.st)
	if _, ok := got["c131292"]; ok || len(got) != 1 {
		t.Fatalf("the merged franchise's old key stays: %+v", got)
	}
	mf := got["c86311"]
	if mf.Source != FranchiseAI || mf.Manual == nil || !slices.Equal(mf.Manual.Order, []int64{im2, im}) {
		t.Fatalf("cluster %+v (manual %+v)", mf, mf.Manual)
	}
}

func TestReconcileFranchises(t *testing.T) {
	order := func(ids ...int64) *FranchiseManual { return &FranchiseManual{Order: ids} }
	fr := func(key string, m *FranchiseManual, items ...int64) Franchise {
		return Franchise{Key: key, Items: items, Manual: m}
	}
	for _, tc := range []struct {
		name     string
		old      []Franchise
		list     []Franchise
		complete bool
		adopt    map[string]*FranchiseManual
		drop     []string
		shelve   []string
	}{
		{name: "same key: nothing to do", old: []Franchise{fr("c1", order(2, 1), 1, 2)}, list: []Franchise{fr("c1", nil, 1, 2)}, complete: true},
		{name: "its titles under a new key: the order moves", old: []Franchise{fr("c1", order(3, 1, 2), 1, 2, 3)}, list: []Franchise{fr("c0", nil, 1, 2, 3)}, complete: true,
			adopt: map[string]*FranchiseManual{"c0": order(3, 1, 2)}, drop: []string{"c1"}},
		{name: "the same in an incomplete pass", old: []Franchise{fr("c1", order(3, 1, 2), 1, 2, 3)}, list: []Franchise{fr("c0", nil, 1, 2, 3)}, complete: false,
			adopt: map[string]*FranchiseManual{"c0": order(3, 1, 2)}, drop: []string{"c1"}},
		{name: "merged into a bigger one: the order is limited to its titles", old: []Franchise{fr("c9", order(2, 1), 1, 2)}, list: []Franchise{fr("c5", nil, 1, 2, 3, 4)}, complete: true,
			adopt: map[string]*FranchiseManual{"c5": order(2, 1)}, drop: []string{"c9"}},
		{name: "superseded without an order: dropped, incomplete too", old: []Franchise{fr("c9", nil, 1, 2)}, list: []Franchise{fr("c5", nil, 1, 2, 3)}, complete: false, drop: []string{"c9"}},
		{name: "the new one has an order of its own: the old one waits", old: []Franchise{fr("c9", order(2, 1), 1, 2), fr("c5", order(3, 4), 3, 4)}, list: []Franchise{fr("c5", nil, 1, 2, 3, 4)}, complete: true,
			shelve: []string{"c9"}},
		{name: "less than half of it elsewhere: shelved", old: []Franchise{fr("c9", order(1, 2, 3, 4), 1, 2, 3, 4)}, list: []Franchise{fr("c5", nil, 4, 7)}, complete: true, shelve: []string{"c9"}},
		{name: "gone without an order", old: []Franchise{fr("c9", nil, 1, 2)}, list: nil, complete: true, drop: []string{"c9"}},
		{name: "gone, incomplete pass: as it was", old: []Franchise{fr("c9", nil, 1, 2), fr("c8", order(2, 1), 1, 2)}, list: nil, complete: false},
		{name: "shelved already: stays", old: []Franchise{{Key: "c9", Manual: order(2, 1)}}, list: nil, complete: true},
		{name: "shelved one is found again under another key", old: []Franchise{{Key: "c9", Manual: order(2, 1)}}, list: []Franchise{fr("c5", nil, 1, 2)}, complete: true,
			adopt: map[string]*FranchiseManual{"c5": order(2, 1)}, drop: []string{"c9"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adopt, drop, shelve := reconcileFranchises(tc.old, tc.list, tc.complete)
			if len(adopt) != len(tc.adopt) {
				t.Fatalf("adopt %v, want %v", adopt, tc.adopt)
			}
			for k, m := range tc.adopt {
				if adopt[k] == nil || !slices.Equal(adopt[k].Order, m.Order) {
					t.Fatalf("adopt %v, want %v", adopt, tc.adopt)
				}
			}
			if !slices.Equal(drop, tc.drop) || !slices.Equal(shelve, tc.shelve) {
				t.Fatalf("drop %v shelve %v, want %v %v", drop, shelve, tc.drop, tc.shelve)
			}
		})
	}
}

// A franchise TMDB no longer finds is kept for its admin's order but has no
// titles: it makes no channel; the titles coming back find the order.
func TestFranchiseKeptOrderPlaysNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := starWars(f)
	fr := &Franchises{Store: f.st, TMDB: tm}
	fr.Pass(ctx, false)
	f.st.SetFranchiseOrder(ctx, "c10", &FranchiseManual{Order: []int64{l.i, l.iv, l.v}})
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}}
	g.Sync(ctx)
	all := tm.of
	tm.of = map[int]int{671: 20, 672: 20}
	fr.Pass(ctx, true)
	kept, ok := franchisesOf(t, f.st)["c10"]
	if !ok || len(kept.Items) != 0 || kept.Manual == nil || len(kept.Effective()) != 0 {
		t.Fatalf("kept %+v", kept)
	}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if c, ok := enabledFranchiseChannels(t, f.st)["franchise:c10"]; ok {
		t.Fatalf("a franchise without titles plays: %+v", c)
	}
	tm.of = all
	fr.Pass(ctx, true)
	g.Sync(ctx)
	c, ok := enabledFranchiseChannels(t, f.st)["franchise:c10"]
	if !ok || c.Rules.Picks[0].ID != l.i {
		t.Fatalf("the titles came back: %+v", c)
	}
}

func TestFranchiseModelOutageRetried(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, tm := starWars(f)
	m := &flakyModel{}
	m.fail.Store(true)
	model := m.llm(t, everyID)
	fr := &Franchises{Store: f.st, TMDB: tm, Model: func(context.Context) *LLM { return model }}
	if _, err := fr.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	if sw := franchisesOf(t, f.st)["c10"]; sw.Source != FranchiseTMDB || sw.Error == "" {
		t.Fatalf("no baseline with an error: %+v", sw)
	}
	if n := m.calls.Load(); n != 2 { // star wars and harry potter
		t.Fatalf("%d asks", n)
	}
	// at once: its backoff is not over
	if ran, _ := fr.Pass(ctx, false); ran || m.calls.Load() != 2 {
		t.Fatalf("asked again at once: ran %v, %d calls", ran, m.calls.Load())
	}
	// the model is back, the 6 h tick comes: asked again, the answer replaces the baseline
	m.fail.Store(false)
	f.now = f.now.Add(6 * time.Hour)
	ran, err := fr.Pass(ctx, false)
	if err != nil || !ran || m.calls.Load() != 4 {
		t.Fatalf("the tick: ran %v err %v calls %d", ran, err, m.calls.Load())
	}
	if sw := franchisesOf(t, f.st)["c10"]; sw.Source != FranchiseAI || sw.Error != "" {
		t.Fatalf("not the model's answer: %+v", sw)
	}
	if st := fr.Status(ctx); st.Error != "" {
		t.Fatalf("status %+v", st)
	}
	if n := len(f.st.franchiseAsks(ctx)); n != 0 {
		t.Fatalf("%d failures stay after the success", n)
	}
	// and it is not asked again
	f.now = f.now.Add(6 * time.Hour)
	if ran, _ := fr.Pass(ctx, false); ran {
		t.Fatal("a pass with nothing to do ran")
	}
}

// A later failure leaves the last good answer in place (the library's new
// title put after its predecessor).
func TestFranchiseFailedAskKeepsGoodAnswer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := starWars(f)
	m := &flakyModel{}
	model := m.llm(t, func(in agentInput) string {
		if strings.Contains(in.Franchise, "Звёздные") {
			return fmt.Sprintf(`{"name":"Сага","order":[%d,%d,%d,%d],"drop":[]}`, l.i, l.clone, l.iv, l.v)
		}
		return everyID(in)
	})
	fr := &Franchises{Store: f.st, TMDB: tm, Wiki: swFacts(l), Model: func(context.Context) *LLM { return model }}
	fr.Pass(ctx, false)
	want := []int64{l.i, l.clone, l.iv, l.v}
	if sw := franchisesOf(t, f.st)["c10"]; sw.Source != FranchiseAI || !slices.Equal(sw.Items, want) {
		t.Fatalf("pass 1 %+v", sw)
	}
	vi := f.titled(f.movie("Звёздные войны: Эпизод 6", 130, itemOpt{year: 1983}), 1892)
	m.fail.Store(true)
	if _, err := fr.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	sw := franchisesOf(t, f.st)["c10"]
	// VI follows V in the collection's order (IV, V, VI, I): it goes after V
	if sw.Source != FranchiseAI || sw.Name != "Сага" || sw.Error == "" || !slices.Equal(sw.Items, []int64{l.i, l.clone, l.iv, l.v, vi}) {
		t.Fatalf("the good answer was lost: %+v", sw)
	}
	// the model is back: the new answer
	m.fail.Store(false)
	f.now = f.now.Add(time.Hour)
	fr.Pass(ctx, false)
	if sw := franchisesOf(t, f.st)["c10"]; sw.Error != "" || !slices.Contains(sw.Items, vi) {
		t.Fatalf("after the outage %+v", sw)
	}
}

func TestFranchiseBackoff(t *testing.T) {
	for n, want := range map[int]time.Duration{0: 30 * time.Minute, 1: 30 * time.Minute, 2: time.Hour, 3: 2 * time.Hour, 6: 16 * time.Hour,
		7: 24 * time.Hour, 20: 24 * time.Hour, 500: 24 * time.Hour} {
		if got := franchiseBackoff(n); got != want {
			t.Errorf("%d failures: %v, want %v", n, got, want)
		}
	}
}

// A cluster the model fails on is not asked again on every pass.
func TestFranchiseFailedClusterBacksOff(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, tm := starWars(f)
	m := &flakyModel{}
	model := m.llm(t, func(agentInput) string { return "I cannot help with that" })
	fr := &Franchises{Store: f.st, TMDB: tm, Model: func(context.Context) *LLM { return model }}
	change := func(n int) {
		f.titled(f.movie(fmt.Sprintf("Другой %d", n), 100, itemOpt{year: 2010}), 9000+n)
	}
	fr.Pass(ctx, false)
	if m.calls.Load() != 2 {
		t.Fatalf("%d asks", m.calls.Load())
	}
	for i := 0; i < 3; i++ { // the library changes, the clusters' inputs do not
		change(i)
		fr.Pass(ctx, false)
	}
	if m.calls.Load() != 2 {
		t.Fatalf("asked again %d times while backing off", m.calls.Load()-2)
	}
	// the backoff is over
	f.now = f.now.Add(31 * time.Minute)
	change(10)
	fr.Pass(ctx, false)
	if m.calls.Load() != 4 {
		t.Fatalf("%d asks after the backoff", m.calls.Load())
	}
	a := f.st.franchiseAsks(ctx)["c10"]
	if a.Failures != 2 || a.RetryAt != f.now.Add(time.Hour).Unix() || a.Error == "" {
		t.Fatalf("failures %+v", a)
	}
	// the admin's «Пересчитать» asks anyway
	fr.Pass(ctx, true)
	if m.calls.Load() != 6 {
		t.Fatalf("a forced pass: %d asks", m.calls.Load())
	}
	// days of failing: a day at most
	for i := 0; i < 12; i++ {
		f.now = f.now.Add(25 * time.Hour)
		change(20 + i)
		fr.Pass(ctx, false)
	}
	if a := f.st.franchiseAsks(ctx)["c10"]; a.Failures < 10 || a.RetryAt-f.now.Unix() != int64(24*time.Hour/time.Second) {
		t.Fatalf("capped backoff: %+v (now %d)", a, f.now.Unix())
	}
	// another model starts afresh
	m2 := &flakyModel{}
	model2 := m2.llm(t, func(agentInput) string { return "no" })
	fr.Model = func(context.Context) *LLM { return model2 }
	fr.Pass(ctx, false)
	if m2.calls.Load() != 2 {
		t.Fatalf("the new model was asked %d times", m2.calls.Load())
	}
	if a := f.st.franchiseAsks(ctx)["c10"]; a.Failures != 1 || a.Model != modelID(model2) {
		t.Fatalf("failures of the new model %+v", a)
	}
}

// TMDB not answering for everything: the model is not asked (the input
// would change when it does) and the last good answers stay.
func TestFranchiseIncompletePassDoesNotAsk(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := starWars(f)
	m := &flakyModel{}
	model := m.llm(t, func(in agentInput) string {
		if strings.Contains(in.Franchise, "Звёздные") {
			return fmt.Sprintf(`{"name":"Сага","order":[%d,%d,%d],"drop":[]}`, l.i, l.v, l.iv)
		}
		return everyID(in)
	})
	fr := &Franchises{Store: f.st, TMDB: tm, Model: func(context.Context) *LLM { return model }}
	fr.Pass(ctx, false)
	calls := m.calls.Load()
	tm.unknown = map[int]bool{672: true}
	f.titled(f.movie("Звёздные войны: Эпизод 6", 130, itemOpt{year: 1983}), 1892)
	fr.Pass(ctx, false)
	if m.calls.Load() != calls {
		t.Fatalf("asked on an incomplete pass: %d", m.calls.Load()-calls)
	}
	if sw := franchisesOf(t, f.st)["c10"]; sw.Source != FranchiseAI || sw.Name != "Сага" || len(sw.Items) != 4 {
		t.Fatalf("star wars %+v", sw)
	}
	if hp := franchisesOf(t, f.st)["c20"]; hp.Source != FranchiseAI {
		t.Fatalf("harry potter lost its answer: %+v", hp)
	}
}

func TestFranchiseNamesFit(t *testing.T) {
	long := strings.Repeat("слово ", 20)
	got := fitFranchiseName(long + "(Коллекция)")
	if n := len([]rune(got)); n > 80 || n < 40 || !strings.HasSuffix(got, "слово") {
		t.Fatalf("cut name %q (%d)", got, n)
	}
	if got := fitFranchiseName(strings.Repeat("Я", 90)); len([]rune(got)) != 80 {
		t.Fatalf("a name of one word: %d runes", len([]rune(got)))
	}
	if got := fitFranchiseName("  Star\nWars\x00\t<b>  "); got != "Star Wars <b>" {
		t.Fatalf("%q", got)
	}
	cl := cluster{colls: []coll{{id: 1, name: "Base", items: []int64{1, 2}}}, members: []int64{1, 2}}
	name, _, err := validAnswer("{\"name\":\"Star\\nWars\\u0000 x\",\"order\":[1,2],\"drop\":[]}", cl)
	if err != nil || name != "Star Wars x" {
		t.Fatalf("the model's name %q %v", name, err)
	}
}

// A collection TMDB gives no usable name, or one too long for a channel,
// does not stop the channels: the pass makes a fit name, Sync goes on.
func TestFranchiseBadNamesDoNotStopChannels(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, tm := starWars(f)
	tm.names[10] = "Коллекция"
	tm.names[20] = strings.Repeat("Гарри ", 30) + "(Коллекция)"
	fr := &Franchises{Store: f.st, TMDB: tm}
	if _, err := fr.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	got := franchisesOf(t, f.st)
	if got["c10"].Name != "Звёздные войны: Эпизод 4" {
		t.Fatalf("the name from the first title: %q", got["c10"].Name)
	}
	if n := len([]rune(got["c20"].Name)); n == 0 || n > 80 {
		t.Fatalf("the long name: %q", got["c20"].Name)
	}
	// a row from elsewhere (an older pass, a hand) with a name no channel takes
	f.exec(`UPDATE franchises SET name='' WHERE key='c10'`)
	f.exec(`UPDATE franchises SET name=? WHERE key='c20'`, strings.Repeat("Я", 120))
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	chans := enabledFranchiseChannels(t, f.st)
	if len(chans) != 2 || chans["franchise:c10"].Name != "Франшиза 10" || len([]rune(chans["franchise:c20"].Name)) != 80 {
		t.Fatalf("channels %+v", chans)
	}
	if name, _ := Display("en", chans["franchise:c10"]); name != "Franchise 10" {
		t.Fatalf("the fallback name in English: %q", name)
	}
	if name, _ := Display("en", chans["franchise:c20"]); name != chans["franchise:c20"].Name {
		t.Fatalf("a franchise's own name is not translated: %q", name)
	}
}

// One franchise channel the store refuses does not stop the others.
func TestSyncSkipsFranchiseItCannotMake(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, tm := starWars(f)
	fr := &Franchises{Store: f.st, TMDB: tm}
	fr.Pass(ctx, false)
	// a row of an external provider holds the key (Sync leaves those to their owner)
	f.exec(`INSERT INTO channels (number, name, rules_json, auto_key, kind, seed) VALUES (900, 'x', '{}', 'franchise:c10', 'external', 1)`)
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatalf("one franchise stopped the sync: %v", err)
	}
	if _, ok := enabledFranchiseChannels(t, f.st)["franchise:c20"]; !ok {
		t.Fatal("the other franchise has no channel")
	}
}

// An edition replaced by one of the same TMDB id is noticed.
func TestFranchiseFingerprintSeesReplacedEdition(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := starWars(f)
	fr := &Franchises{Store: f.st, TMDB: tm}
	fr.Pass(ctx, false)
	f.exec(`UPDATE items SET missing_since=1 WHERE id=?`, l.v)
	nv := f.titled(f.movie("Звёздные войны: Эпизод 5 (новый)", 120, itemOpt{year: 1980}), 1891)
	ran, err := fr.Pass(ctx, false)
	if err != nil || !ran {
		t.Fatalf("ran %v %v", ran, err)
	}
	if sw := franchisesOf(t, f.st)["c10"]; !slices.Equal(sw.Items, []int64{l.iv, nv, l.i}) {
		t.Fatalf("items %v", sw.Items)
	}
}

// Wikidata down when the cache has expired: the cached facts still serve,
// so the clusters' inputs stay as they were and nothing is asked again.
func TestFranchiseStaleFactsServeWhenWikidataDown(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	l, tm := starWars(f)
	m := &flakyModel{}
	model := m.llm(t, everyID)
	facts := swFacts(l)
	fr := &Franchises{Store: f.st, TMDB: tm, Wiki: facts, Model: func(context.Context) *LLM { return model }}
	fr.Pass(ctx, false)
	calls := m.calls.Load()
	f.now = f.now.Add(31 * 24 * time.Hour)
	facts.err = fmt.Errorf("down")
	f.titled(f.movie("Новый", 100, itemOpt{year: 2020}), 9999)
	if ran, err := fr.Pass(ctx, false); err != nil || !ran {
		t.Fatalf("ran %v %v", ran, err)
	}
	if n := m.calls.Load(); n != calls {
		t.Fatalf("%d asks with the same Wikidata facts", n-calls)
	}
	if sw := franchisesOf(t, f.st)["c10"]; sw.Source != FranchiseAI || sw.Error != "" {
		t.Fatalf("star wars %+v", sw)
	}
}
