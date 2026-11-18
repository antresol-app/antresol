package addons

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

var channelHooks = []string{"channels.ready", "channels.candidates", "channels.resolve", "channels.release"}

// chanAddon is an in-process addon answering channels.* from its fields
// and serving its streams.
type chanAddon struct {
	*relayAddon
	mu       sync.Mutex
	ready    bool
	titles   []addonkit.ChannelTitle
	stream   addonkit.ChannelStream
	hookLog  []string
	bodies   map[string][]json.RawMessage
	released chan addonkit.ChannelRelease
}

func (c *chanAddon) hooks() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.hookLog)
}

func (c *chanAddon) count(name string) int {
	n := 0
	for _, h := range c.hooks() {
		if h == name {
			n++
		}
	}
	return n
}

func (c *chanAddon) body(name string) json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.bodies[name]
	if len(b) == 0 {
		return nil
	}
	return b[len(b)-1]
}

// chanTarget is the HookTarget of the adapter's tests.
type chanTarget struct {
	mu     sync.Mutex
	ins    map[string]Installed
	addons map[string]*chanAddon
	status map[string]Status
}

func (c *chanTarget) Get(_ context.Context, id string) (Installed, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	in, ok := c.ins[id]
	if !ok {
		return Installed{}, ErrNotFound
	}
	return in, nil
}

func (c *chanTarget) List(context.Context) ([]Installed, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Installed
	for _, in := range c.ins {
		out = append(out, in)
	}
	return out, nil
}

func (c *chanTarget) Serving(id string) (Status, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.addons[id]
	if !ok || c.status[id] != StatusRunning {
		return c.status[id], ""
	}
	return StatusRunning, a.sock
}

func (c *chanTarget) setStatus(id string, st Status) {
	c.mu.Lock()
	c.status[id] = st
	c.mu.Unlock()
}

// add installs and starts an addon answering channels.* with access.
func (c *chanTarget) add(t *testing.T, id, access string) *chanAddon {
	t.Helper()
	ca := &chanAddon{ready: true, bodies: map[string][]json.RawMessage{}, released: make(chan addonkit.ChannelRelease, 4),
		stream: addonkit.ChannelStream{Path: "/ch/" + id + "/index.m3u8"}}
	mux := &addonkit.HookMux{}
	rec := func(name string, in json.RawMessage) {
		ca.mu.Lock()
		ca.hookLog = append(ca.hookLog, name)
		ca.bodies[name] = append(ca.bodies[name], slices.Clone(in))
		ca.mu.Unlock()
	}
	mux.Handle("channels.ready", func(_ context.Context, _ *addonkit.User, in json.RawMessage) (any, error) {
		rec("channels.ready", in)
		ca.mu.Lock()
		defer ca.mu.Unlock()
		return addonkit.ChannelReady{Ready: ca.ready}, nil
	})
	mux.Handle("channels.candidates", func(_ context.Context, _ *addonkit.User, in json.RawMessage) (any, error) {
		rec("channels.candidates", in)
		ca.mu.Lock()
		defer ca.mu.Unlock()
		return addonkit.ChannelCandidatesAnswer{Titles: ca.titles}, nil
	})
	mux.Handle("channels.resolve", func(_ context.Context, _ *addonkit.User, in json.RawMessage) (any, error) {
		rec("channels.resolve", in)
		ca.mu.Lock()
		defer ca.mu.Unlock()
		return ca.stream, nil
	})
	mux.Handle("channels.release", func(_ context.Context, _ *addonkit.User, in json.RawMessage) (any, error) {
		rec("channels.release", in)
		var r addonkit.ChannelRelease
		json.Unmarshal(in, &r)
		ca.released <- r
		return nil, nil
	})
	ca.relayAddon = newRelayAddon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/hooks/") {
			mux.ServeHTTP(w, r)
			return
		}
		io.WriteString(w, "#EXTM3U\n")
	}))
	c.mu.Lock()
	c.ins[id] = Installed{ID: id, Version: "1.0.0", Enabled: true,
		Manifest: Manifest{ID: id, Version: "1.0.0", Contract: ContractVersion, Access: access, Hooks: channelHooks}}
	c.addons[id] = ca
	c.status[id] = StatusRunning
	c.mu.Unlock()
	return ca
}

// fakeLibrary has the library items of the rating tests.
type fakeLibrary struct {
	items map[string]map[int64]library.Item // kind → TMDB id → item
	err   error
}

func (f *fakeLibrary) ItemIDsByTMDB(_ context.Context, kind string, ids []int64) (map[int64]int64, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[int64]int64{}
	for _, id := range ids {
		if it, ok := f.items[kind][id]; ok {
			out[id] = it.ID
		}
	}
	return out, nil
}

func (f *fakeLibrary) ItemsByID(_ context.Context, ids []int64) (map[int64]library.Item, error) {
	out := map[int64]library.Item{}
	for _, m := range f.items {
		for _, it := range m {
			if slices.Contains(ids, it.ID) {
				out[it.ID] = it
			}
		}
	}
	return out, nil
}

type chanEnv struct {
	x   *ChannelExtras
	tg  *chanTarget
	sup *Supervisor
	now time.Time
}

func newChanEnv(t *testing.T) *chanEnv {
	t.Helper()
	tg := &chanTarget{ins: map[string]Installed{}, addons: map[string]*chanAddon{}, status: map[string]Status{}}
	h := &Hooks{T: tg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	sup := &Supervisor{}
	e := &chanEnv{tg: tg, sup: sup, now: time.Now()}
	e.x = NewChannelExtras(h, sup, NewRelay(tg.Serving, func() int { return 8420 }))
	e.x.now = func() time.Time { return e.now }
	e.x.Users = func(_ context.Context, id int64) (auth.User, error) {
		switch id {
		case 1:
			return auth.User{ID: 1, IsAdmin: true}, nil
		case 2:
			return auth.User{ID: 2}, nil
		case 3:
			return auth.User{ID: 3, IsKid: true}, nil
		}
		return auth.User{}, auth.ErrNotFound
	}
	return e
}

// status tells the adapter's OnStatus listener, as the supervisor would.
func (e *chanEnv) status(id string, st Status) {
	e.tg.setStatus(id, st)
	e.sup.mu.Lock()
	e.sup.setStatusLocked(e.sup.entryLocked(id), st)
	e.sup.mu.Unlock()
}

var extrasRules = channels.Rules{Extras: true, Genres: []string{"Драма"}}

func admin(ctx context.Context) context.Context {
	return auth.WithUser(ctx, auth.User{ID: 1, IsAdmin: true})
}

func TestChannelExtras_ReadyCached60s(t *testing.T) {
	e := newChanEnv(t)
	a := e.tg.add(t, "tv", "adults")
	ctx := admin(context.Background())
	if !e.x.Ready(ctx) || !e.x.Ready(ctx) {
		t.Fatal("not ready")
	}
	if n := a.count("channels.ready"); n != 1 {
		t.Fatalf("asked %d times within 60 s", n)
	}
	a.mu.Lock()
	a.ready = false
	a.mu.Unlock()
	e.now = e.now.Add(59 * time.Second)
	if !e.x.Ready(ctx) {
		t.Fatal("the cached answer was not used")
	}
	e.now = e.now.Add(2 * time.Second)
	if e.x.Ready(ctx) || a.count("channels.ready") != 2 {
		t.Fatalf("after 60 s: asked %d times", a.count("channels.ready"))
	}
	if b := a.body("channels.ready"); string(b) != "{}" {
		t.Fatalf("body %s", b)
	}
	// a kid never gets extras
	if e.x.Ready(auth.WithUser(context.Background(), auth.User{ID: 3, IsKid: true})) {
		t.Fatal("ready for a kid")
	}
}

func TestChannelExtras_InvalidatedOnStatus(t *testing.T) {
	e := newChanEnv(t)
	a := e.tg.add(t, "tv", "adults")
	ctx := admin(context.Background())
	if !e.x.Ready(ctx) {
		t.Fatal("not ready")
	}
	a.mu.Lock()
	a.ready = false
	a.mu.Unlock()
	e.status("tv", StatusStarting)
	e.status("tv", StatusRunning)
	if e.x.Ready(ctx) || a.count("channels.ready") != 2 {
		t.Fatalf("status change kept the cache: asked %d", a.count("channels.ready"))
	}
}

func TestChannelExtras_DownMeansNotReady(t *testing.T) {
	e := newChanEnv(t)
	e.tg.add(t, "tv", "adults")
	ctx := admin(context.Background())
	if !e.x.Ready(ctx) {
		t.Fatal("not ready")
	}
	e.status("tv", StatusCrashed)
	if e.x.Ready(ctx) {
		t.Fatal("ready while the addon is down")
	}
	ts, err := e.x.Candidates(ctx, extrasRules, 5)
	if len(ts) != 0 || err == nil {
		t.Fatalf("candidates of a down addon: %v %v", ts, err)
	}
	// none declares the hooks: not ready, no candidates, no error
	e2 := newChanEnv(t)
	if e2.x.Ready(ctx) {
		t.Fatal("ready without an addon")
	}
	if ts, err := e2.x.Candidates(ctx, extrasRules, 5); len(ts) != 0 || err != nil {
		t.Fatalf("candidates without an addon: %v %v", ts, err)
	}
}

func movieTitle(key string, tmdb int64, rating string) addonkit.ChannelTitle {
	return addonkit.ChannelTitle{Key: key, Kind: "movie", TMDBID: tmdb, Title: "Фильм " + key, Year: 1999, Rating: rating,
		Genres: []string{"Драма"}, Poster: "/p.jpg", RuntimeMs: 95 * 60000}
}

func TestChannelExtras_CandidatesOnlyWhenExtrasOn(t *testing.T) {
	e := newChanEnv(t)
	a := e.tg.add(t, "tv", "adults")
	a.titles = []addonkit.ChannelTitle{movieTitle("a", 1, "12+"), movieTitle("b", 2, "12+"), movieTitle("c", 3, "12+"),
		{Key: "", Kind: "movie", Title: "без ключа", RuntimeMs: 1}, {Key: "d", Kind: "music", Title: "не фильм", RuntimeMs: 1},
		{Key: "e", Kind: "movie", Title: "", RuntimeMs: 1}}
	ctx := context.Background()
	for _, off := range []channels.Rules{{}, {Extras: true, Picks: []channels.Pick{{Kind: channels.PickTitle, ID: 1}}},
		{Extras: true, Order: channels.OrderOrdered}, {Extras: true, Kinds: []string{library.KindSeries}}} {
		if ts, err := e.x.Candidates(ctx, off, 5); len(ts) != 0 || err != nil {
			t.Fatalf("rules %+v: %v %v", off, ts, err)
		}
	}
	if a.count("channels.candidates") != 0 {
		t.Fatal("asked for rules without extras")
	}
	r := channels.Rules{Extras: true, Genres: []string{"Драма"}, YearFrom: 1990, YearTo: 1999, Countries: []string{"FR"},
		Languages: []string{"fr"}, Keywords: []string{"heist"}}
	ts, err := e.x.Candidates(ctx, r, 2)
	if err != nil || len(ts) != 2 {
		t.Fatalf("candidates %v %v", ts, err)
	}
	if ts[0].Key != "addon:tv:a" || ts[0].Kind != library.KindMovie || ts[0].TMDBID != 1 || ts[0].RuntimeMs != 95*60000 || ts[0].Poster != "/p.jpg" {
		t.Fatalf("title %+v", ts[0])
	}
	var in addonkit.ChannelCandidates
	if err := json.Unmarshal(a.body("channels.candidates"), &in); err != nil || in.Limit != 2 || in.Rules.YearFrom != 1990 ||
		in.Rules.YearTo != 1999 || in.Rules.Countries[0] != "FR" || in.Rules.Languages[0] != "fr" || in.Rules.Keywords[0] != "heist" || in.Rules.Genres[0] != "Драма" {
		t.Fatalf("body %s", a.body("channels.candidates"))
	}
	ts, _ = e.x.Candidates(ctx, r, 10)
	if len(ts) != 3 {
		t.Fatalf("the invalid titles were kept: %+v", ts)
	}
}

func TestChannelExtras_CandidateOverLimitDropped(t *testing.T) {
	e := newChanEnv(t)
	a := e.tg.add(t, "tv", "adults")
	a.titles = []addonkit.ChannelTitle{movieTitle("a", 1, "12+"), movieTitle("b", 2, "18+"), movieTitle("c", 3, "6+")}
	r := extrasRules
	r.MaxRating = "12+"
	ts, err := e.x.Candidates(context.Background(), r, 10)
	if err != nil || len(ts) != 2 || ts[0].Key != "addon:tv:a" || ts[1].Key != "addon:tv:c" {
		t.Fatalf("%+v %v", ts, err)
	}
	var in addonkit.ChannelCandidates
	json.Unmarshal(a.body("channels.candidates"), &in)
	if in.Rules.MaxRating != "12+" {
		t.Fatalf("the limit was not sent: %s", a.body("channels.candidates"))
	}
}

func TestChannelExtras_UnknownRatingDroppedWhenLimited(t *testing.T) {
	e := newChanEnv(t)
	a := e.tg.add(t, "tv", "adults")
	a.titles = []addonkit.ChannelTitle{movieTitle("a", 1, ""), movieTitle("b", 2, "12+"), movieTitle("c", 3, "чепуха")}
	r := extrasRules
	r.MaxRating = "16+"
	ts, err := e.x.Candidates(context.Background(), r, 10)
	if err != nil || len(ts) != 1 || ts[0].Key != "addon:tv:b" {
		t.Fatalf("%+v %v", ts, err)
	}
	// no limit: the unrated one stays
	if ts, _ := e.x.Candidates(context.Background(), extrasRules, 10); len(ts) != 3 {
		t.Fatalf("without a limit %+v", ts)
	}
}

func TestChannelExtras_LibraryRatingWins(t *testing.T) {
	e := newChanEnv(t)
	a := e.tg.add(t, "tv", "adults")
	a.titles = []addonkit.ChannelTitle{movieTitle("a", 1, "6+"), movieTitle("b", 2, ""),
		{Key: "e", Kind: "episode", TMDBID: 9, Title: "Серия", Rating: "0+", RuntimeMs: 60000, Season: 1, Episode: 1}}
	e.x.Library = &fakeLibrary{items: map[string]map[int64]library.Item{
		library.KindMovie:  {1: {ID: 11, Kind: library.KindMovie, Rating: "18+"}, 2: {ID: 12, Kind: library.KindMovie, Rating: "12+"}},
		library.KindSeries: {9: {ID: 19, Kind: library.KindSeries, Rating: "18+"}},
	}}
	r := extrasRules
	r.MaxRating = "12+"
	ts, err := e.x.Candidates(context.Background(), r, 10)
	if err != nil || len(ts) != 1 || ts[0].Key != "addon:tv:b" || ts[0].Rating != "12+" {
		t.Fatalf("%+v %v", ts, err)
	}
	// without a limit the library's rating is what the channel keeps
	ts, _ = e.x.Candidates(context.Background(), extrasRules, 10)
	if len(ts) != 3 || ts[0].Rating != "18+" || ts[2].Rating != "18+" {
		t.Fatalf("%+v", ts)
	}
	// the ratings cannot be read: no candidates (fail closed)
	e.x.Library = &fakeLibrary{err: errors.New("disk")}
	if ts, err := e.x.Candidates(context.Background(), r, 10); len(ts) != 0 || err == nil {
		t.Fatalf("lookup failure: %+v %v", ts, err)
	}
}

func TestChannelExtras_AccessAdminsOnly(t *testing.T) {
	e := newChanEnv(t)
	a := e.tg.add(t, "tv", "admins")
	a.titles = []addonkit.ChannelTitle{movieTitle("a", 1, "12+")}
	adult := auth.WithUser(context.Background(), auth.User{ID: 2})
	if e.x.Ready(adult) {
		t.Fatal("an admins-only addon is ready for an adult")
	}
	if !e.x.Ready(admin(context.Background())) {
		t.Fatal("not ready for an admin")
	}
	shared := channels.WithChannel(context.Background(), channels.Channel{ID: 5})
	if ts, _ := e.x.Candidates(shared, extrasRules, 5); len(ts) != 0 {
		t.Fatalf("an admins-only addon answered a shared channel: %+v", ts)
	}
	if ts, _ := e.x.Candidates(context.Background(), extrasRules, 5); len(ts) != 0 {
		t.Fatalf("an admins-only addon answered a channel of nobody known: %+v", ts)
	}
	for uid, want := range map[int64]int{1: 1, 2: 0, 3: 0, 99: 0} {
		ctx := channels.WithChannel(context.Background(), channels.Channel{ID: 5, UserID: uid})
		if ts, _ := e.x.Candidates(ctx, extrasRules, 5); len(ts) != want {
			t.Errorf("personal channel of %d: %d titles", uid, len(ts))
		}
	}
	mine := channels.WithChannel(context.Background(), channels.Channel{ID: 5, UserID: 1})
	ts, _ := e.x.Candidates(mine, extrasRules, 5)
	if _, err := e.x.Resolve(shared, 5, ts[0]); err == nil {
		t.Fatal("resolved an admins-only addon's title for a shared channel")
	}
	st, err := e.x.Resolve(mine, 5, ts[0])
	if err != nil {
		t.Fatal(err)
	}
	st.Release()
	// an adults addon answers a shared channel, and a kid's own gets nothing
	e2 := newChanEnv(t)
	b := e2.tg.add(t, "tv", "adults")
	b.titles = a.titles
	if ts, _ := e2.x.Candidates(shared, extrasRules, 5); len(ts) != 1 {
		t.Fatalf("adults addon, shared channel: %+v", ts)
	}
	kid := channels.WithChannel(context.Background(), channels.Channel{ID: 5, UserID: 3})
	if ts, _ := e2.x.Candidates(kid, extrasRules, 5); len(ts) != 0 {
		t.Fatalf("a kid's channel got extras: %+v", ts)
	}
}

func TestChannelExtras_ResolveGivesLoopbackURL(t *testing.T) {
	e := newChanEnv(t)
	a := e.tg.add(t, "tv", "adults")
	a.titles = []addonkit.ChannelTitle{movieTitle("a", 1, "12+")}
	a.stream = addonkit.ChannelStream{Path: "/ch/t/index.m3u8", Headers: map[string]string{"X-Stream": "s1"}}
	ctx := context.Background()
	ts, _ := e.x.Candidates(ctx, extrasRules, 5)
	st, err := e.x.Resolve(ctx, 7, ts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer st.Release()
	if err := (media.LiveInput{URL: st.URL, Headers: st.Headers}).Check(); err != nil {
		t.Fatalf("%q: %v", st.URL, err)
	}
	if !strings.HasPrefix(st.URL, "http://127.0.0.1:8420/api/addon-relay/") || !strings.HasSuffix(st.URL, "/index.m3u8") {
		t.Fatalf("url %q", st.URL)
	}
	var in addonkit.ChannelResolve
	if err := json.Unmarshal(a.body("channels.resolve"), &in); err != nil || in.ChannelID != 7 || in.Title.Key != "a" || in.Title.TMDBID != 1 {
		t.Fatalf("body %s", a.body("channels.resolve"))
	}
	// the relay asks the addon with its headers
	w := relayGet(t, e.x.Relay, "GET", st.URL, nil)
	if w.Code != 200 || a.last().URL.Path != "/ch/t/index.m3u8" || a.last().Header.Get("X-Stream") != "s1" {
		t.Fatalf("%d, asked %v %v", w.Code, a.paths(), a.last().Header)
	}
	// a key of no addon, or a path the relay refuses: no stream
	if _, err := e.x.Resolve(ctx, 7, channels.ExtraTitle{Key: "tmdb:movie:1", Kind: "movie"}); err == nil {
		t.Fatal("resolved a key of no addon")
	}
	a.stream = addonkit.ChannelStream{Path: "../x"}
	if _, err := e.x.Resolve(ctx, 7, ts[0]); err == nil {
		t.Fatal("resolved a bad path")
	}
	a.stream = addonkit.ChannelStream{Path: "/ok.ts", Headers: map[string]string{"Bad\r\nName": "x"}}
	if _, err := e.x.Resolve(ctx, 7, ts[0]); err == nil {
		t.Fatal("resolved with a bad header")
	}
}

func TestChannelExtras_ReleaseHookOnStreamRelease(t *testing.T) {
	e := newChanEnv(t)
	a := e.tg.add(t, "tv", "adults")
	a.titles = []addonkit.ChannelTitle{movieTitle("a", 1, "12+")}
	ctx := context.Background()
	ts, _ := e.x.Candidates(ctx, extrasRules, 5)
	st, err := e.x.Resolve(ctx, 7, ts[0])
	if err != nil {
		t.Fatal(err)
	}
	if w := relayGet(t, e.x.Relay, "GET", st.URL, nil); w.Code != 200 {
		t.Fatalf("before release %d", w.Code)
	}
	tok := a.last().Header.Get("X-Antresol-Relay")
	st.Release()
	st.Release()
	select {
	case r := <-a.released:
		if r.Token != tok || !hex32(r.Token) || r.Path != "/ch/tv/index.m3u8" {
			t.Fatalf("release %+v, relay token %q", r, tok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channels.release was not called")
	}
	if w := relayGet(t, e.x.Relay, "GET", st.URL, nil); w.Code != 404 {
		t.Fatalf("after release %d", w.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if n := a.count("channels.release"); n != 1 {
		t.Fatalf("released %d times", n)
	}
}

// The channels.* deadlines: ready 2 s, candidates 30 s, resolve 60 s,
// release 5 s.
func TestHookTimeouts_Channels(t *testing.T) {
	for name, want := range map[string]time.Duration{"channels.ready": 2 * time.Second, "channels.candidates": 30 * time.Second,
		"channels.resolve": 60 * time.Second, "channels.release": 5 * time.Second} {
		if got := HookTimeout(name); got != want {
			t.Errorf("%s: %v", name, got)
		}
		if !knownHook(name) {
			t.Errorf("%s is not a known hook", name)
		}
	}
}

// A kid's resolve, should one come, is refused; the same title resolves
// for an adult (D9).
func TestChannelExtras_KidResolveRefused(t *testing.T) {
	e := newChanEnv(t)
	a := e.tg.add(t, "tv", "adults")
	a.titles = []addonkit.ChannelTitle{movieTitle("a", 1, "12+")}
	ts, _ := e.x.Candidates(context.Background(), extrasRules, 5)
	if len(ts) != 1 || !IsChannelExtraKey(ts[0].Key) || IsChannelExtraKey("tmdb:movie:1") {
		t.Fatalf("%+v", ts)
	}
	kid := auth.WithUser(context.Background(), auth.User{ID: 3, IsKid: true})
	if _, err := e.x.Resolve(kid, 7, ts[0]); err == nil || a.count("channels.resolve") != 0 {
		t.Fatalf("a kid's resolve: %v, asked %d", err, a.count("channels.resolve"))
	}
	st, err := e.x.Resolve(auth.WithUser(context.Background(), auth.User{ID: 2}), 7, ts[0])
	if err != nil {
		t.Fatal(err)
	}
	st.Release()
}
