package channels

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeExtras gives n movies of 100 minutes and resolves them to url
// (fail: their keys that do not play).
type fakeExtras struct {
	mu       sync.Mutex
	n        int
	asked    int
	rules    []Rules
	limits   []int
	fail     map[string]bool
	resolved []string
	url      string
	ready    bool
	err      error
}

func (f *fakeExtras) Ready(context.Context) bool { return f.ready }

func (f *fakeExtras) Candidates(_ context.Context, r Rules, limit int) ([]ExtraTitle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	f.rules, f.limits = append(f.rules, r), append(f.limits, limit)
	if f.err != nil {
		return nil, f.err
	}
	var out []ExtraTitle
	for i := 1; i <= f.n && len(out) < limit; i++ {
		out = append(out, ExtraTitle{Key: fmt.Sprintf("movie:%d", 1000+i), Kind: "movie", TMDBID: 1000 + i, Title: fmt.Sprintf("Extra %d", i),
			Year: 2001, Overview: "o", Genres: []string{"Комедия"}, Poster: "/p.jpg", Backdrop: "/b.jpg", RuntimeMs: 100 * 60000})
	}
	return out, nil
}

func (f *fakeExtras) Resolve(_ context.Context, ch int64, t ExtraTitle) (Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = append(f.resolved, t.Key)
	if f.fail[t.Key] {
		return Stream{}, errors.New("source failed")
	}
	return Stream{URL: f.url, Headers: map[string]string{"X-K": "v"}}, nil
}

// extrasFixture: 6 comedies in the library and a comedy channel with
// extras on.
func extrasFixture(t *testing.T, ex *fakeExtras, rules Rules) (*fixture, *Service, Channel) {
	f := newFixture(t)
	for i := 0; i < 6; i++ {
		f.movie(fmt.Sprintf("Lib %d", i), 90, itemOpt{year: 2000, genres: []string{"комедия"}})
	}
	svc := New(f.db, nil)
	svc.Store.Now = f.st.Now
	if ex != nil {
		svc.SetExtras(ex)
	}
	c := f.create(Channel{Name: "Комедии", Rules: rules})
	return f, svc, c
}

func countExtras(es []Entry) (extras, lib int) {
	for _, e := range es {
		if e.ExtraID != 0 {
			extras++
			if e.ItemID != -e.ExtraID || e.FileID != 0 || e.UnitID >= 0 {
				panic(fmt.Sprintf("extra entry %+v", e))
			}
		} else {
			lib++
		}
	}
	return
}

func TestExtrasOffByDefault(t *testing.T) {
	ex := &fakeExtras{n: 20}
	f, svc, c := extrasFixture(t, ex, Rules{Genres: []string{"комедия"}})
	if err := svc.BuildChannel(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if n, _ := countExtras(f.entries(c.ID)); n != 0 || ex.asked != 0 {
		t.Fatalf("extras without the rule: %d entries, asked %d", n, ex.asked)
	}
	// the rule without an implementation (the core build) changes nothing
	f2, svc2, c2 := extrasFixture(t, nil, Rules{Genres: []string{"комедия"}, Extras: true})
	if err := svc2.BuildChannel(context.Background(), c2); err != nil {
		t.Fatal(err)
	}
	if n, lib := countExtras(f2.entries(c2.ID)); n != 0 || lib == 0 {
		t.Fatalf("core build: %d extras, %d library", n, lib)
	}
}

func TestExtrasMixAtMostHalfNeverTwoInARow(t *testing.T) {
	ex := &fakeExtras{n: 50} // far more candidates than library titles
	f, svc, c := extrasFixture(t, ex, Rules{Genres: []string{"комедия"}, Extras: true})
	if err := svc.BuildChannel(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	es := f.entries(c.ID)
	n, lib := countExtras(es)
	if n == 0 || n > lib {
		t.Fatalf("%d extras, %d library programmes", n, lib)
	}
	for i := 1; i < len(es); i++ {
		if es[i].ExtraID != 0 && es[i-1].ExtraID != 0 {
			t.Fatalf("two extras in a row at %d", i)
		}
	}
	if es[0].ExtraID != 0 {
		t.Fatal("a channel starts with an extra")
	}
	// at most as many candidates as library titles were asked for and kept
	if ex.limits[0] != 6 || !ex.rules[0].Extras {
		t.Fatalf("limit %v", ex.limits)
	}
	var kept int
	f.db.QueryRow(`SELECT count(*) FROM channel_extras WHERE channel_id=?`, c.ID).Scan(&kept)
	if kept != 6 {
		t.Fatalf("kept %d", kept)
	}
	distinct := map[int64]bool{}
	for _, e := range es {
		if e.ExtraID != 0 {
			distinct[e.ExtraID] = true
			if d := e.End.Sub(e.Start); d != 100*time.Minute {
				t.Fatalf("an extra lasts %v (its runtime)", d)
			}
		}
	}
	if len(distinct) > 6 {
		t.Fatalf("%d distinct extras", len(distinct))
	}
}

func TestExtrasDeterministic(t *testing.T) {
	run := func() []Entry {
		f, svc, c := extrasFixture(t, &fakeExtras{n: 10}, Rules{Genres: []string{"комедия"}, Extras: true})
		if err := svc.BuildChannel(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		return f.entries(c.ID)
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("%d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ItemID != b[i].ItemID || a[i].UnitID != b[i].UnitID || !a[i].Start.Equal(b[i].Start) {
			t.Fatalf("entry %d differs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestExtrasAskedDailyAndKeptThroughCuts(t *testing.T) {
	ex := &fakeExtras{n: 10}
	f, svc, c := extrasFixture(t, ex, Rules{Genres: []string{"комедия"}, Extras: true})
	ctx := context.Background()
	if err := svc.BuildChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	// a file check does not cut the extras (they have no file)
	if cut, err := svc.Store.CutMissing(ctx, c.ID); err != nil || cut {
		t.Fatalf("CutMissing cut an extra: %v %v", cut, err)
	}
	f.now = f.now.Add(2 * time.Hour)
	if err := svc.BuildChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	if ex.asked != 1 {
		t.Fatalf("asked %d times within a day", ex.asked)
	}
	f.now = f.now.Add(25 * time.Hour)
	if err := svc.BuildChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	if ex.asked != 2 {
		t.Fatalf("asked %d times after a day", ex.asked)
	}
	// a failed ask keeps the old candidates and asks again in an hour
	ex.err = errors.New("tmdb down")
	f.now = f.now.Add(25 * time.Hour)
	if err := svc.BuildChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(30 * time.Minute)
	svc.BuildChannel(ctx, c)
	if ex.asked != 3 {
		t.Fatalf("asked %d (retry before the hour)", ex.asked)
	}
	f.now = f.now.Add(31 * time.Minute)
	svc.BuildChannel(ctx, c)
	if ex.asked != 4 {
		t.Fatalf("asked %d (no retry after the hour)", ex.asked)
	}
	// new rules: asked again at once
	c.Rules.Genres = []string{"драма", "комедия"}
	if _, err := svc.Store.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	ex.err = nil
	svc.BuildChannel(ctx, c)
	if ex.asked != 5 {
		t.Fatalf("asked %d after an edit", ex.asked)
	}
}

func TestExtrasOnlyForShuffledMovieChannels(t *testing.T) {
	for _, r := range []Rules{
		{Extras: true, Order: OrderOrdered},
		{Extras: true, Titles: []int64{1}},
		{Extras: true, Kinds: []string{"series"}},
	} {
		if ExtrasOn(r) {
			t.Errorf("ExtrasOn(%+v)", r)
		}
	}
	if !ExtrasOn(Rules{Extras: true, Kinds: []string{"movie", "series"}}) || ExtrasOn(Rules{}) {
		t.Error("ExtrasOn")
	}
}

// firstExtra is the first entry of a title outside the library.
func firstExtra(t *testing.T, es []Entry) Entry {
	t.Helper()
	for _, e := range es {
		if e.ExtraID != 0 {
			return e
		}
	}
	t.Fatal("no extra in the schedule")
	return Entry{}
}

func TestProgramResolvesAnExtraAndDescribesIt(t *testing.T) {
	ex := &fakeExtras{n: 10, url: "http://127.0.0.1:9/s.m3u8"}
	f, svc, c := extrasFixture(t, ex, Rules{Genres: []string{"комедия"}, Extras: true})
	ctx := context.Background()
	if err := svc.BuildChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	e := firstExtra(t, f.entries(c.ID))
	p, err := svc.Program(ctx, c.ID, e.Start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if p.Seq != e.Seq || p.URL != ex.url || p.Headers["X-K"] != "v" || p.Path != "" || p.Skip {
		t.Fatalf("%+v", p)
	}
	info, err := svc.Describe(ctx, []int64{e.ItemID})
	if err != nil {
		t.Fatal(err)
	}
	x := info[e.ItemID]
	if !x.External || x.Title == "" || x.Year != 2001 || x.Poster != "/p.jpg" || x.Backdrop != "/b.jpg" || x.Kind != "movie" {
		t.Fatalf("%+v", x)
	}
}

func TestProgramReplacesAnExtraThatWouldNotPlay(t *testing.T) {
	ex := &fakeExtras{n: 10, url: "http://127.0.0.1:9/s.m3u8", fail: map[string]bool{}}
	f, svc, c := extrasFixture(t, ex, Rules{Genres: []string{"комедия"}, Extras: true})
	ctx := context.Background()
	if err := svc.BuildChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10; i++ { // nothing outside the library plays
		ex.fail[fmt.Sprintf("movie:%d", 1000+i)] = true
	}
	e := firstExtra(t, f.entries(c.ID))
	f.now = e.Start.Add(-time.Second) // it is about to air
	p, err := svc.Program(ctx, c.ID, e.Start)
	if err != nil {
		t.Fatal(err)
	}
	if p.Skip {
		// every try failed: the slot is skipped, never an error that stops the channel
		return
	}
	if p.ExtraID != 0 || p.Path == "" {
		t.Fatalf("the slot plays %+v", p)
	}
	var failed int
	f.db.QueryRow(`SELECT count(*) FROM channel_extras WHERE failed_until_ms > 0`).Scan(&failed)
	if failed == 0 {
		t.Fatal("the failed title is not left out")
	}
	// the schedule after it was made again, the failed ones left out
	for _, x := range f.entries(c.ID) {
		if x.Start.Before(e.Start) {
			continue
		}
		var until int64
		if x.ExtraID != 0 {
			f.db.QueryRow(`SELECT failed_until_ms FROM channel_extras WHERE id=?`, x.ExtraID).Scan(&until)
			if until > 0 {
				t.Fatalf("a failed title is scheduled again: %+v", x)
			}
		}
	}
}

// The stream may run ahead of the schedule (a stream shorter than its
// runtime ends early): a title resolved ahead whose time has not come yet
// is resolved at once when it is due, never waited for.
func TestProgramDoesNotWaitForAPrefetchThatIsNotDueYet(t *testing.T) {
	ex := &fakeExtras{n: 10, url: "http://127.0.0.1:9/s.m3u8"}
	f, svc, c := extrasFixture(t, ex, Rules{Genres: []string{"комедия"}, Extras: true})
	ctx := context.Background()
	if err := svc.BuildChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	es := f.entries(c.ID)
	var prev Entry
	for i, e := range es {
		if e.ExtraID != 0 && i > 0 && e.Start.Sub(f.now) > 10*time.Minute {
			prev = es[i-1]
			break
		}
	}
	if prev.Seq == 0 {
		t.Skip("no extra far enough ahead")
	}
	if _, err := svc.Program(ctx, c.ID, prev.Start); err != nil { // its prefetch waits for its time
		t.Fatal(err)
	}
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	p, err := svc.Program(tctx, c.ID, prev.End)
	if err != nil || p.ExtraID == 0 || p.URL != ex.url {
		t.Fatalf("%+v %v", p, err)
	}
}
