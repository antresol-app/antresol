package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/tracking"
)

func TestRunsRegistry(t *testing.T) {
	var r Runs
	end, ok := r.Start(1, "p")
	if !ok || !r.Active(1, "p") {
		t.Fatal("first Start refused or not active")
	}
	if _, ok := r.Start(1, "p"); ok {
		t.Fatal("a second sync of one profile and provider started")
	}
	if r.Active(2, "p") || r.Active(1, "q") {
		t.Fatal("another profile or provider reads as active")
	}
	end2, ok := r.Start(2, "p")
	if !ok {
		t.Fatal("another profile refused")
	}
	end()
	end() // twice is fine
	if r.Active(1, "p") || !r.Active(2, "p") {
		t.Fatal("end released the wrong one")
	}
	end2()
	if _, ok := r.Start(1, "p"); !ok {
		t.Fatal("not startable after end")
	}
	// a nil registry registers nothing and refuses nothing
	var none *Runs
	e, ok := none.Start(1, "p")
	if !ok || none.Active(1, "p") {
		t.Fatal("nil registry")
	}
	e()
}

// syncState is the part of GET /api/accounts the sync-now tests read.
type syncState struct {
	Linked    bool    `json:"linked"`
	Status    string  `json:"status"`
	LastError string  `json:"lastError"`
	LastSync  *string `json:"lastSync"`
	Syncing   bool    `json:"syncing"`
}

func (e *apiEnv) states(w *httptest.ResponseRecorder) (m map[string]syncState) {
	e.t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		e.t.Fatalf("%v: %s", err, w.Body)
	}
	return m
}

// held makes the API's background work wait: what it was given is run by
// release, in order, so a test can look at the sync while it is in flight.
type held struct {
	mu   sync.Mutex
	work []func()
}

func (h *held) add(f func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.work = append(h.work, f)
}

func (h *held) n() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.work)
}

func (h *held) release() {
	h.mu.Lock()
	w := h.work
	h.work = nil
	h.mu.Unlock()
	for _, f := range w {
		f()
	}
}

func TestSyncNowShikimori(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, e.now.Add(-time.Hour))}

	w := e.do("POST", "/api/accounts/shikimori/sync", "")
	if w.Code != 202 {
		t.Fatalf("sync: %d %s", w.Code, w.Body)
	}
	if e.remote.calls != 1 || e.mark("series", 101) == nil {
		t.Fatalf("not pulled (calls %d)", e.remote.calls)
	}
	s := e.states(w)["shikimori"]
	if !s.Linked || s.Status != StatusOK || s.LastSync == nil || s.Syncing {
		t.Fatalf("state after: %+v", s)
	}

	// just done: a second ask within the cooldown is refused with the wait
	w = e.do("POST", "/api/accounts/shikimori/sync", "")
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" || !strings.Contains(w.Body.String(), `"error":"L:`) {
		t.Fatalf("again: %d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	e.now = e.now.Add(45 * time.Second)
	if w = e.do("POST", "/api/accounts/shikimori/sync", ""); w.Code != 429 || w.Header().Get("Retry-After") != "15" {
		t.Fatalf("again at 45 s: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	if e.remote.calls != 1 {
		t.Fatalf("a refused ask read the list: %d calls", e.remote.calls)
	}
	// after the cooldown it syncs again, and the refusal released its claim
	e.now = e.now.Add(time.Minute)
	if w = e.do("POST", "/api/accounts/shikimori/sync", ""); w.Code != 202 || e.remote.calls != 2 {
		t.Fatalf("after cooldown: %d, %d calls", w.Code, e.remote.calls)
	}
	if e.sy.Runs().Active(e.uid, Shikimori) {
		t.Fatal("still registered after the run")
	}

	// a failed read is the result: the status says so, the time is the attempt's
	e.now = e.now.Add(time.Hour)
	e.remote.err = errors.New("boom")
	w = e.do("POST", "/api/accounts/shikimori/sync", "")
	if w.Code != 202 {
		t.Fatalf("failing: %d %s", w.Code, w.Body)
	}
	if s := e.states(w)["shikimori"]; s.Status != StatusError || s.Syncing || s.LastSync == nil {
		t.Fatalf("failed state: %+v", s)
	}
	l, _, _ := e.acc.Link(ctx, e.uid, Shikimori)
	if !l.LastSync.Equal(e.now) {
		t.Fatalf("last sync %v, want the attempt's %v", l.LastSync, e.now)
	}
}

// Two asks, or an ask and the periodic loop, never read the list twice at once.
func TestSyncNowRunsOnce(t *testing.T) {
	e := newAPIEnv(t)
	h := &held{}
	e.api.Background = h.add
	e.sy.Sleep = func(context.Context, time.Duration) error { return nil }
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, e.now.Add(-time.Hour))}

	w := e.do("POST", "/api/accounts/shikimori/sync", "")
	if w.Code != 202 || h.n() != 1 || !e.states(w)["shikimori"].Syncing {
		t.Fatalf("first: %d, %d queued, %s", w.Code, h.n(), w.Body)
	}
	// the second one joins the first: 202, syncing, nothing more queued
	w = e.do("POST", "/api/accounts/shikimori/sync", "")
	if w.Code != 202 || h.n() != 1 || !e.states(w)["shikimori"].Syncing {
		t.Fatalf("second: %d, %d queued, %s", w.Code, h.n(), w.Body)
	}
	if s := e.states(e.do("GET", "/api/accounts", ""))["shikimori"]; !s.Syncing {
		t.Fatalf("GET does not say syncing: %+v", s)
	}
	// the periodic loop leaves the profile to the run in flight
	e.sy.tick(context.Background())
	if e.remote.calls != 0 {
		t.Fatalf("the loop read the list during the run: %d calls", e.remote.calls)
	}
	// relinking meanwhile starts no second sync either
	if w := e.do("PUT", "/api/accounts/shikimori", `{"nickname":"tester"}`); w.Code != 200 || h.n() != 1 {
		t.Fatalf("relink: %d, %d queued", w.Code, h.n())
	}

	h.release()
	if e.remote.calls != 1 {
		t.Fatalf("calls %d, want the one run's", e.remote.calls)
	}
	s := e.states(e.do("GET", "/api/accounts", ""))["shikimori"]
	if s.Syncing || s.Status != StatusOK || s.LastSync == nil {
		t.Fatalf("after: %+v", s)
	}
}

// The pull right after linking is registered like any sync: «sync now» during
// it joins it, GET says it is syncing.
func TestLinkPullIsARun(t *testing.T) {
	e := newAPIEnv(t)
	h := &held{}
	e.api.Background = h.add
	ctx := context.Background()
	must(t, e.acc.Unlink(ctx, e.uid, Shikimori))
	w := e.do("PUT", "/api/accounts/shikimori", `{"nickname":"tester"}`)
	if w.Code != 200 || h.n() != 1 || !e.states(w)["shikimori"].Syncing {
		t.Fatalf("link: %d, %d queued, %s", w.Code, h.n(), w.Body)
	}
	if w := e.do("POST", "/api/accounts/shikimori/sync", ""); w.Code != 202 || h.n() != 1 {
		t.Fatalf("sync during the first pull: %d, %d queued", w.Code, h.n())
	}
	h.release()
	if s := e.states(e.do("GET", "/api/accounts", ""))["shikimori"]; s.Syncing || s.LastSync == nil {
		t.Fatalf("after: %+v", s)
	}
}

// A push waiting out its backoff goes with «sync now».
func TestSyncNowPushesDespiteBackoff(t *testing.T) {
	e := newAPIEnv(t)
	e.local("series", 101, tracking.Planned, e.now.Add(-time.Hour))
	e.w.writeErr = errors.New("boom")
	e.push()
	if items := e.outbox(); len(items) != 1 || !items[0].NextAt.After(e.now) {
		t.Fatalf("outbox %+v", items)
	}
	e.w.writeErr = nil
	e.remote.rates = nil
	// the periodic push would wait: the item is not due
	e.push()
	if len(e.w.creates) != 0 {
		t.Fatalf("pushed inside the backoff: %+v", e.w.creates)
	}
	if w := e.do("POST", "/api/accounts/shikimori/sync", ""); w.Code != 202 {
		t.Fatalf("sync: %d %s", w.Code, w.Body)
	}
	if len(e.w.creates) != 1 || len(e.outbox()) != 0 {
		t.Fatalf("creates %+v, outbox %+v", e.w.creates, e.outbox())
	}
}

// A failed pull leaves the queue alone: a remote that fails a read fails a write.
func TestSyncNowFailedPullPushesNothing(t *testing.T) {
	e := newAPIEnv(t)
	e.local("series", 101, tracking.Planned, e.now.Add(-time.Hour))
	e.remote.err = errors.New("boom")
	if w := e.do("POST", "/api/accounts/shikimori/sync", ""); w.Code != 202 {
		t.Fatalf("sync: %d", w.Code)
	}
	if len(e.w.creates) != 0 || len(e.outbox()) != 1 {
		t.Fatalf("creates %+v, outbox %+v", e.w.creates, e.outbox())
	}
}

func TestSyncNowRefusals(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	for _, p := range []string{"trakt", "admin", "myshows", "goodreads"} { // unknown, or not offered here
		if w := e.do("POST", "/api/accounts/"+p+"/sync", ""); w.Code != 404 {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body)
		}
	}
	// not linked: nothing to sync
	must(t, e.acc.Unlink(ctx, e.uid, Shikimori))
	if w := e.do("POST", "/api/accounts/shikimori/sync", ""); w.Code != 409 || !strings.Contains(w.Body.String(), `"error":"L:`) {
		t.Fatalf("unlinked: %d %s", w.Code, w.Body)
	}
	// a link with no remote account yet (only a sign-in stored) is not linked either
	must(t, e.acc.SetNickname(ctx, e.uid, Shikimori, "", 0))
	if w := e.do("POST", "/api/accounts/shikimori/sync", ""); w.Code != 409 {
		t.Fatalf("no remote account: %d", w.Code)
	}
	if e.remote.calls != 0 {
		t.Fatal("read a list for a profile with no link")
	}
	// a kid has no accounts; no viewer, no session
	e.viewer = Viewer{ID: e.uid, IsKid: true}
	if w := e.do("POST", "/api/accounts/shikimori/sync", ""); w.Code != 403 {
		t.Fatalf("kid: %d", w.Code)
	}
}

// A profile syncs its own link only: another profile's link is never touched,
// whoever asks (the address names no profile).
func TestSyncNowOnlyOwnLink(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	other, err := e.acc.DB.Exec(`INSERT INTO users(name, avatar) VALUES('v', 'a')`)
	must(t, err)
	oid, _ := other.LastInsertId()
	must(t, e.acc.SetNickname(ctx, oid, Shikimori, "other", 77))
	e.viewer = Viewer{ID: oid, IsAdmin: true}
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, e.now.Add(-time.Hour))}
	if w := e.do("POST", "/api/accounts/shikimori/sync", ""); w.Code != 202 {
		t.Fatalf("sync: %d %s", w.Code, w.Body)
	}
	if l, _, _ := e.acc.Link(ctx, e.uid, Shikimori); !l.LastSync.IsZero() {
		t.Fatalf("another profile's link was synced: %+v", l)
	}
	if l, _, _ := e.acc.Link(ctx, oid, Shikimori); l.LastSync.IsZero() {
		t.Fatalf("the viewer's own link was not: %+v", l)
	}
}

func TestSyncNowMyShowsAndGoodreads(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	ms := &fakeMyShows{}
	gr := &fakeGoodreads{st: e.sy.Accounts}
	e.api.MyShows, e.api.Goodreads = ms, gr
	// offered, not linked
	for _, p := range []string{"myshows", "goodreads"} {
		if w := e.do("POST", "/api/accounts/"+p+"/sync", ""); w.Code != 409 {
			t.Fatalf("%s unlinked: %d %s", p, w.Code, w.Body)
		}
	}
	must(t, e.acc.SetNickname(ctx, e.uid, myShowsProvider, "tester", 0))
	must(t, e.acc.SetNickname(ctx, e.uid, goodreadsProvider, "reader", 123))
	h := &held{}
	e.api.Background = h.add

	for _, c := range []struct {
		provider string
		pulls    func() int
		timeout  time.Duration
	}{
		{"myshows", func() int { return len(ms.pulls) }, linkPullTimeout},
		{"goodreads", func() int { return len(gr.pulls) }, 5 * time.Minute},
	} {
		w := e.do("POST", "/api/accounts/"+c.provider+"/sync", "")
		if w.Code != 202 || h.n() != 1 || !e.states(w)[c.provider].Syncing {
			t.Fatalf("%s: %d, %d queued, %s", c.provider, w.Code, h.n(), w.Body)
		}
		if w := e.do("POST", "/api/accounts/"+c.provider+"/sync", ""); w.Code != 202 || h.n() != 1 {
			t.Fatalf("%s joined: %d, %d queued", c.provider, w.Code, h.n())
		}
		// the other providers' syncs are independent of this one
		other := "goodreads"
		if c.provider == "goodreads" {
			other = "myshows"
		}
		if e.states(e.do("GET", "/api/accounts", ""))[other].Syncing {
			t.Fatalf("%s syncing during %s's run", other, c.provider)
		}
		h.release()
		if c.pulls() != 1 {
			t.Fatalf("%s pulls %d", c.provider, c.pulls())
		}
		if e.states(e.do("GET", "/api/accounts", ""))[c.provider].Syncing {
			t.Fatalf("%s still syncing after its run", c.provider)
		}
	}
	// the pulls were given the provider's own time budget
	if len(ms.budget) != 1 || ms.budget[0] < 2*time.Hour {
		t.Fatalf("myshows budget %v", ms.budget)
	}
}

func TestSyncNowCooldownFromLastSync(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	e.api.MyShows = &fakeMyShows{}
	must(t, e.acc.SetNickname(ctx, e.uid, myShowsProvider, "tester", 0))
	must(t, e.acc.SetStatus(ctx, e.uid, myShowsProvider, StatusOK, "", e.now.Add(-30*time.Second)))
	if w := e.do("POST", "/api/accounts/myshows/sync", ""); w.Code != 429 || w.Header().Get("Retry-After") != "30" {
		t.Fatalf("30 s after the last sync: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	must(t, e.acc.SetStatus(ctx, e.uid, myShowsProvider, StatusError, "x", e.now.Add(-time.Minute)))
	if w := e.do("POST", "/api/accounts/myshows/sync", ""); w.Code != 202 {
		t.Fatalf("a minute after: %d", w.Code)
	}
}
