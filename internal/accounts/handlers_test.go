package accounts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/tracking"
)

// fakeAccount resolves nicknames and plays the OAuth side.
type fakeAccount struct {
	users  map[string]RemoteUser // by nickname
	whoami RemoteUser
	codes  []string
}

func (f *fakeAccount) UserByNick(_ context.Context, nick string) (RemoteUser, error) {
	u, ok := f.users[nick]
	if !ok {
		return RemoteUser{}, ErrRemoteNotFound
	}
	return u, nil
}

func (f *fakeAccount) AuthorizeURL(c OAuthClient, state string) string {
	return "https://auth.example/authorize?" + url.Values{"client_id": {c.ID}, "redirect_uri": {c.RedirectURI}, "state": {state}}.Encode()
}

func (f *fakeAccount) Exchange(_ context.Context, c OAuthClient, code string) (Tokens, error) {
	f.codes = append(f.codes, code)
	return Tokens{Access: "acc-" + code, Refresh: "ref-" + code, Expires: time.Now().Add(24 * time.Hour)}, nil
}

func (f *fakeAccount) Whoami(context.Context, string) (RemoteUser, error) { return f.whoami, nil }

type apiEnv struct {
	*pushEnv
	acct   *fakeAccount
	api    *API
	srv    http.Handler
	viewer Viewer
}

// newAPIEnv: the push env behind the accounts routes, viewed by its
// profile (not an admin); background work runs inline.
func newAPIEnv(t *testing.T) *apiEnv {
	e := &apiEnv{pushEnv: newPushEnv(t), acct: &fakeAccount{users: map[string]RemoteUser{"tester": {ID: 42, Nickname: "tester"}, "other": {ID: 77, Nickname: "other"}},
		whoami: RemoteUser{ID: 42, Nickname: "tester"}}}
	e.viewer = Viewer{ID: e.uid}
	e.api = &API{Sync: e.sy, Account: e.acct,
		Viewer:     func(*http.Request) (Viewer, bool) { return e.viewer, true },
		Localize:   func(_ context.Context, k string) string { return "L:" + k },
		Background: func(f func()) { f() }, Log: e.sy.Log}
	r := chi.NewRouter()
	e.api.Routes(r)
	e.api.BrowserRoutes(r)
	e.srv = r
	return e
}

func (e *apiEnv) do(method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.srv.ServeHTTP(w, req)
	return w
}

type accountsJSON struct {
	Shikimori struct {
		Linked     bool    `json:"linked"`
		Nickname   string  `json:"nickname"`
		Status     string  `json:"status"`
		LastError  string  `json:"lastError"`
		LastSync   *string `json:"lastSync"`
		WriteState string  `json:"writeState"`
		CanWrite   bool    `json:"canWrite"`
		Unmapped   int     `json:"unmapped"`
	} `json:"shikimori"`
}

func (e *apiEnv) get() accountsJSON {
	e.t.Helper()
	w := e.do("GET", "/api/accounts", "")
	if w.Code != 200 {
		e.t.Fatalf("GET: %d %s", w.Code, w.Body)
	}
	var out accountsJSON
	must(e.t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}

func TestAccountsRoutes(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	must(t, e.acc.Unlink(ctx, e.uid, Shikimori))
	if g := e.get(); g.Shikimori.Linked || g.Shikimori.CanWrite != true {
		t.Fatalf("unlinked %+v", g)
	}
	// unknown nickname
	if w := e.do("PUT", "/api/accounts/shikimori", `{"nickname":"nobody"}`); w.Code != 404 || !strings.Contains(w.Body.String(), "L:") {
		t.Fatalf("404: %d %s", w.Code, w.Body)
	}
	// link: resolved, stored and pulled at once
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, e.now.Add(-time.Hour)), rate(13, 3, "planned", 0, e.now.Add(-time.Hour))}
	w := e.do("PUT", "/api/accounts/shikimori", `{"nickname":"tester"}`)
	if w.Code != 200 {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	if e.remote.calls != 1 || e.mark("series", 101) == nil {
		t.Fatalf("not pulled (calls %d)", e.remote.calls)
	}
	g := e.get()
	s := g.Shikimori
	if !s.Linked || s.Nickname != "tester" || s.Status != "ok" || s.LastSync == nil || s.WriteState != "unasked" || !s.CanWrite || s.Unmapped != 1 {
		t.Fatalf("linked %+v", s)
	}
	// a status message is translated for the viewer
	must(t, e.acc.SetStatus(ctx, e.uid, Shikimori, StatusPrivate, privateMsg, e.now))
	if s := e.get().Shikimori; s.LastError != "L:"+privateMsg {
		t.Fatalf("lastError %q", s.LastError)
	}
	// unlink
	if w := e.do("DELETE", "/api/accounts/shikimori", ""); w.Code != 204 {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body)
	}
	if g := e.get(); g.Shikimori.Linked {
		t.Fatalf("still linked %+v", g)
	}
	// no https public URL: no write-back offered
	e.sy.OAuth.PublicURL = "http://media.example"
	if g := e.get(); g.Shikimori.CanWrite {
		t.Fatal("canWrite over http")
	}
}

func TestDeclineClearsOutbox(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	must(t, e.acc.SetWriteState(ctx, e.uid, Shikimori, "unasked"))
	e.local("series", 101, tracking.Planned, e.now)
	if len(e.outbox()) != 1 {
		t.Fatal("nothing queued")
	}
	if w := e.do("POST", "/api/accounts/shikimori/write", `{"enable":false}`); w.Code != 204 {
		t.Fatalf("decline: %d %s", w.Code, w.Body)
	}
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("outbox %+v", items)
	}
	if l, _, _ := e.acc.Link(ctx, e.uid, Shikimori); l.WriteState != "declined" {
		t.Fatalf("write state %q", l.WriteState)
	}
	if tok, _, _ := e.acc.Tokens(ctx, e.uid, Shikimori); tok.Access != "" || tok.Refresh != "" {
		t.Fatalf("tokens kept: %+v", tok)
	}
}

// authorize asks for write-back and returns the state sent to the provider.
func (e *apiEnv) authorize() string {
	e.t.Helper()
	w := e.do("POST", "/api/accounts/shikimori/write", `{"enable":true}`)
	if w.Code != 200 {
		e.t.Fatalf("enable: %d %s", w.Code, w.Body)
	}
	var out struct {
		AuthorizeURL string `json:"authorizeUrl"`
	}
	must(e.t, json.Unmarshal(w.Body.Bytes(), &out))
	u, err := url.Parse(out.AuthorizeURL)
	must(e.t, err)
	if u.Query().Get("redirect_uri") != "https://media.example/api/accounts/shikimori/callback" || u.Query().Get("client_id") != "cid" {
		e.t.Fatalf("authorize url %s", out.AuthorizeURL)
	}
	st := u.Query().Get("state")
	if len(st) < 40 {
		e.t.Fatalf("weak state %q", st)
	}
	return st
}

func (e *apiEnv) callback(state, code string) *httptest.ResponseRecorder {
	e.t.Helper()
	w := e.do("GET", "/api/accounts/shikimori/callback?"+url.Values{"code": {code}, "state": {state}}.Encode(), "")
	if w.Code != http.StatusFound {
		e.t.Fatalf("callback: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		e.t.Fatalf("headers %v", w.Header())
	}
	return w
}

func TestCallbackChecksStateAndUser(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	must(t, e.acc.SetWriteState(ctx, e.uid, Shikimori, "unasked"))
	must(t, e.acc.SetFirstPullDone(ctx, e.uid, Shikimori))
	// wrong state
	e.authorize()
	w := e.callback("forged", "c1")
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/settings?account=shikimori&error=") {
		t.Fatalf("location %q", loc)
	}
	if len(e.acct.codes) != 0 {
		t.Fatal("code exchanged on a forged state")
	}
	// no session (or a kid): back to the page, nothing exchanged
	st0 := e.authorize()
	for _, v := range []*Viewer{nil, {ID: e.uid, IsKid: true}} {
		if v == nil {
			e.api.Viewer = func(*http.Request) (Viewer, bool) { return Viewer{}, false }
		} else {
			kid := *v
			e.api.Viewer = func(*http.Request) (Viewer, bool) { return kid, true }
		}
		w = e.callback(st0, "c0")
		if loc := w.Header().Get("Location"); loc != "/settings?account=shikimori&error=session" || len(e.acct.codes) != 0 {
			t.Fatalf("no session: %q %v", loc, e.acct.codes)
		}
	}
	e.api.Viewer = func(*http.Request) (Viewer, bool) { return e.viewer, true }
	// a state issued to another profile
	st := e.authorize()
	e.viewer = Viewer{ID: e.uid + 1}
	w = e.callback(st, "c2")
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "error=") || len(e.acct.codes) != 0 {
		t.Fatalf("other profile: %q %v", loc, e.acct.codes)
	}
	e.viewer = Viewer{ID: e.uid}
	// another Shikimori account than the linked one
	e.acct.whoami = RemoteUser{ID: 77, Nickname: "other"}
	st = e.authorize()
	w = e.callback(st, "c3")
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "error=") {
		t.Fatalf("other user: %q", loc)
	}
	if l, _, _ := e.acc.Link(ctx, e.uid, Shikimori); l.WriteState == "enabled" {
		t.Fatal("enabled for another account")
	}
	tok, _, _ := e.acc.Tokens(ctx, e.uid, Shikimori)
	if tok.Access == "acc-c3" {
		t.Fatal("other account's tokens stored")
	}
	// the right account: tokens stored, write-back on, the queue pushed
	e.acct.whoami = RemoteUser{ID: 42, Nickname: "tester"}
	e.local("series", 101, tracking.Planned, e.now)
	st = e.authorize()
	w = e.callback(st, "c4")
	if loc := w.Header().Get("Location"); loc != "/settings?account=shikimori&ok=1" {
		t.Fatalf("ok: %q", loc)
	}
	tok, _, _ = e.acc.Tokens(ctx, e.uid, Shikimori)
	l, _, _ := e.acc.Link(ctx, e.uid, Shikimori)
	if tok.Access != "acc-c4" || l.WriteState != "enabled" || len(e.w.creates) != 1 {
		t.Fatalf("tokens %q state %q creates %+v", tok.Access, l.WriteState, e.w.creates)
	}
	// a state works once
	w = e.callback(st, "c5")
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "error=") {
		t.Fatalf("replayed state: %q", loc)
	}
}

func TestAdminClientSecretNeverReturned(t *testing.T) {
	e := newAPIEnv(t)
	e.sy.OAuth.EnvClientID, e.sy.OAuth.EnvClientSecret = "", ""
	if w := e.do("GET", "/api/accounts/admin/shikimori", ""); w.Code != 403 {
		t.Fatalf("non-admin GET: %d", w.Code)
	}
	if w := e.do("PUT", "/api/accounts/admin/shikimori", `{"clientId":"x","clientSecret":"y"}`); w.Code != 403 {
		t.Fatalf("non-admin PUT: %d", w.Code)
	}
	if e.get().Shikimori.CanWrite {
		t.Fatal("canWrite without a client")
	}
	e.viewer.IsAdmin = true
	if w := e.do("PUT", "/api/accounts/admin/shikimori", `{"clientId":"my-id","clientSecret":"top-secret"}`); w.Code != 204 {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	w := e.do("GET", "/api/accounts/admin/shikimori", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "top-secret") {
		t.Fatalf("GET: %d %s", w.Code, w.Body)
	}
	var out map[string]any
	must(t, json.Unmarshal(w.Body.Bytes(), &out))
	if out["clientId"] != "my-id" || out["hasSecret"] != true || out["scope"] != "user_rates" ||
		out["redirectUri"] != "https://media.example/api/accounts/shikimori/callback" {
		t.Fatalf("GET %v", out)
	}
	// an empty secret keeps the stored one
	if w := e.do("PUT", "/api/accounts/admin/shikimori", `{"clientId":"id2"}`); w.Code != 204 {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	if id, sec, _, _ := e.acc.ClientSettings(context.Background(), Shikimori); id != "id2" || sec != "top-secret" {
		t.Fatalf("stored %q %q", id, sec)
	}
	if !e.get().Shikimori.CanWrite {
		t.Fatal("canWrite with a stored client")
	}
	// no OAuth configuration at all
	e.sy.OAuth = nil
	if w := e.do("GET", "/api/accounts/admin/shikimori", ""); w.Code != 409 {
		t.Fatalf("GET without OAuth: %d %s", w.Code, w.Body)
	}
	if e.get().Shikimori.CanWrite {
		t.Fatal("canWrite without OAuth")
	}
}

func TestAskedMovesOnlyUnasked(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	state := func() string { l, _, _ := e.acc.Link(ctx, e.uid, Shikimori); return l.WriteState }
	must(t, e.acc.SetWriteState(ctx, e.uid, Shikimori, "unasked"))
	if w := e.do("POST", "/api/accounts/shikimori/asked", ""); w.Code != 204 || state() != "asked" {
		t.Fatalf("asked: %d %q", w.Code, state())
	}
	for _, s := range []string{"enabled", "declined"} {
		must(t, e.acc.SetWriteState(ctx, e.uid, Shikimori, s))
		if w := e.do("POST", "/api/accounts/shikimori/asked", ""); w.Code != 204 || state() != s {
			t.Fatalf("%s: %d %q", s, w.Code, state())
		}
	}
	e.viewer.IsKid = true
	if w := e.do("POST", "/api/accounts/shikimori/asked", ""); w.Code != 403 {
		t.Fatalf("kid: %d", w.Code)
	}
}

// Pending sign-in states are capped in memory; the oldest go first.
func TestOAuthStatesCapped(t *testing.T) {
	a := &API{}
	first, err := a.newState(1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxStates+10; i++ {
		if _, err := a.newState(1); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(a.states); n != maxStates {
		t.Fatalf("states = %d", n)
	}
	if a.takeState(first, 1) {
		t.Fatal("oldest state kept")
	}
}

// fakeMyShows plays the MyShows side of the routes.
type fakeMyShows struct {
	logins map[string]error // login → check result (absent: unknown)
	pulls  []int64
	budget []time.Duration // time the pull's context allows
}

func (f *fakeMyShows) Check(_ context.Context, login string) error {
	err, ok := f.logins[login]
	if !ok {
		return ErrRemoteNotFound
	}
	return err
}

func (f *fakeMyShows) Pull(ctx context.Context, uid int64) error {
	if d, ok := ctx.Deadline(); ok {
		f.budget = append(f.budget, time.Until(d))
	}
	f.pulls = append(f.pulls, uid)
	return nil
}

func TestMyShowsRoutes(t *testing.T) {
	e := newAPIEnv(t)
	ms := &fakeMyShows{logins: map[string]error{"tester": nil, "hidden": ErrRemoteForbidden, "down": context.DeadlineExceeded}}
	e.api.MyShows = ms
	body := func(w *httptest.ResponseRecorder) (m struct {
		MyShows *struct {
			Linked    bool    `json:"linked"`
			Nickname  string  `json:"nickname"`
			Status    string  `json:"status"`
			LastError string  `json:"lastError"`
			LastSync  *string `json:"lastSync"`
			Unmapped  int     `json:"unmapped"`
		} `json:"myshows"`
	}) {
		t.Helper()
		must(t, json.Unmarshal(w.Body.Bytes(), &m))
		return m
	}

	m := body(e.do("GET", "/api/accounts", ""))
	if m.MyShows == nil || m.MyShows.Linked || m.MyShows.Status != StatusOK {
		t.Fatalf("unlinked: %+v", m.MyShows)
	}

	for login, code := range map[string]int{"nobody": 404, "hidden": 422, "down": 502} {
		if w := e.do("PUT", "/api/accounts/myshows", `{"nickname":"`+login+`"}`); w.Code != code || !strings.Contains(w.Body.String(), `"error":"L:`) {
			t.Fatalf("%s: %d %s", login, w.Code, w.Body)
		}
	}
	if w := e.do("PUT", "/api/accounts/myshows", `{"nickname":"  "}`); w.Code != 400 {
		t.Fatalf("empty: %d", w.Code)
	}
	if m := body(e.do("GET", "/api/accounts", "")); m.MyShows.Linked || len(ms.pulls) != 0 {
		t.Fatalf("a failed check linked or pulled: %+v %v", m.MyShows, ms.pulls)
	}

	w := e.do("PUT", "/api/accounts/myshows", `{"nickname":" tester "}`)
	if w.Code != 200 {
		t.Fatalf("link: %d %s", w.Code, w.Body)
	}
	if m := body(w); m.MyShows == nil || !m.MyShows.Linked || m.MyShows.Nickname != "tester" {
		t.Fatalf("link body: %+v", m.MyShows)
	}
	if len(ms.pulls) != 1 || ms.pulls[0] != e.uid {
		t.Fatalf("pulls: %v", ms.pulls)
	}
	// a first read of ~400 shows takes up to ten minutes: no short deadline
	if len(ms.budget) != 1 || ms.budget[0] < 30*time.Minute {
		t.Fatalf("link-time pull budget %v", ms.budget)
	}

	if w := e.do("DELETE", "/api/accounts/myshows", ""); w.Code != 204 {
		t.Fatalf("unlink: %d", w.Code)
	}
	if m := body(e.do("GET", "/api/accounts", "")); m.MyShows.Linked {
		t.Fatal("still linked")
	}

	e.viewer.IsKid = true
	for _, c := range [][2]string{{"PUT", `{"nickname":"tester"}`}, {"DELETE", ""}} {
		if w := e.do(c[0], "/api/accounts/myshows", c[1]); w.Code != 403 {
			t.Fatalf("kid %s: %d", c[0], w.Code)
		}
	}
}

func TestMyShowsAbsentWithoutRemote(t *testing.T) {
	e := newAPIEnv(t)
	if strings.Contains(e.do("GET", "/api/accounts", "").Body.String(), "myshows") {
		t.Fatal("myshows must be absent without a remote")
	}
	if w := e.do("PUT", "/api/accounts/myshows", `{"nickname":"tester"}`); w.Code != 404 {
		t.Fatalf("PUT: %d", w.Code)
	}
}
