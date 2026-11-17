package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
)

// pendingCall is one library.pending call a fake addon got.
type pendingCall struct {
	body []byte
	user string // X-Antresol-User
}

// pendingEnv is a router with fake addons answering library.pending.
type pendingEnv struct {
	t     *testing.T
	h     http.Handler
	users *auth.Store
	ss    *auth.Sessions
	fake  *fakeAddons

	mu    sync.Mutex
	calls map[string][]pendingCall // addon id → calls
	logs  strings.Builder          // what the hook caller logged
}

func (e *pendingEnv) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.logs.Write(p)
}

func (e *pendingEnv) logged() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.logs.String()
}

func newPendingEnv(t *testing.T) *pendingEnv {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &pendingEnv{t: t, users: auth.NewStore(d), ss: auth.NewSessions(d), calls: map[string][]pendingCall{},
		fake: &fakeAddons{installed: map[string]addons.Installed{}, status: map[string]addons.Status{},
			sock: map[string]string{}, tokens: map[string]string{}, webRoot: t.TempDir()}}
	e.h = NewRouter(Deps{Users: e.users, Sessions: e.ss, DB: d,
		AddonHooks: &addons.Hooks{T: e.fake, Log: slog.New(slog.NewTextHandler(e, nil))}})
	return e
}

// addon starts addon id declaring library.pending; answer writes its reply.
func (e *pendingEnv) addon(id string, answer func(w http.ResponseWriter)) {
	e.t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pc")
	if err != nil {
		e.t.Fatal(err)
	}
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		e.t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.calls[id] = append(e.calls[id], pendingCall{body: b, user: r.Header.Get("X-Antresol-User")})
		e.mu.Unlock()
		if r.URL.Path != "/hooks/library.pending" {
			http.NotFound(w, r)
			return
		}
		answer(w)
	})}
	go srv.Serve(ln)
	e.t.Cleanup(func() { srv.Close(); os.RemoveAll(dir) })
	e.fake.mu.Lock()
	e.fake.installed[id] = addons.Installed{ID: id, Version: "1.0.0", Enabled: true,
		Manifest: addons.Manifest{ID: id, Version: "1.0.0", Contract: addons.ContractVersion, Hooks: []string{"library.pending"}}}
	e.fake.status[id] = addons.StatusRunning
	e.fake.sock[id] = sock
	e.fake.mu.Unlock()
}

// cards answers with these cards.
func cards(cs ...addonkit.PendingCard) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"cards": cs})
	}
}

func pcard(key, status string) addonkit.PendingCard {
	return addonkit.PendingCard{
		Card:    addonkit.PendingCardInfo{Kind: "movie", Title: "Фильм " + key, TmdbKind: "movie", TmdbID: 7},
		Pending: addonkit.PendingState{Key: key, Status: status, Done: 1, Size: 2},
	}
}

func (e *pendingEnv) user(n auth.NewUser) string {
	e.t.Helper()
	u, err := e.users.CreateUser(context.Background(), n)
	if err != nil {
		e.t.Fatal(err)
	}
	tok, err := e.ss.Create(context.Background(), u.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

// get asks GET /api/library/pending<q> as tok and decodes the answer.
func (e *pendingEnv) get(tok, q string) []LibraryPendingCard {
	e.t.Helper()
	r := httptest.NewRequest("GET", "/api/library/pending"+q, nil)
	r.RemoteAddr = loopAddr
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		e.t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var out []LibraryPendingCard
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out == nil {
		e.t.Fatalf("answer %q: %v (want a list, [] when empty)", w.Body, err)
	}
	return out
}

func (e *pendingEnv) callsOf(id string) []pendingCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]pendingCall(nil), e.calls[id]...)
}

func keysOf(cs []LibraryPendingCard) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Pending.Key)
	}
	return out
}

func TestPending_MergesAddons(t *testing.T) {
	e := newPendingEnv(t)
	// "hello" sorts after "dl": the answer keeps addon-id order
	e.addon("hello", cards(pcard("k2", "waiting")))
	e.addon("dl", cards(pcard("k1", "active")))
	tok := e.user(auth.NewUser{Name: "Аня"})
	got := e.get(tok, "")
	if k := strings.Join(keysOf(got), ","); k != "dl:k1,hello:k2" {
		t.Fatalf("keys %q", k)
	}
	c := got[0]
	if c.Kind != "movie" || c.Title != "Фильм k1" || c.TmdbKind != "movie" || c.TmdbID != 7 ||
		c.Pending.Status != "active" || c.Pending.Done != 1 || c.Pending.Size != 2 {
		t.Fatalf("card %+v", c)
	}
	// the profile rides along
	var u addonkit.User
	if err := json.Unmarshal([]byte(e.callsOf("dl")[0].user), &u); err != nil || u.Name != "Аня" || u.ID == 0 || len(u.UID) != 32 {
		t.Fatalf("user header %q: %v", e.callsOf("dl")[0].user, err)
	}
}

func TestPending_KidGetsNone(t *testing.T) {
	e := newPendingEnv(t)
	e.addon("dl", cards(pcard("k1", "active")))
	if got := e.get(e.user(auth.NewUser{Name: "Петя", IsKid: true}), ""); len(got) != 0 {
		t.Fatalf("kid got %v", keysOf(got))
	}
	if n := len(e.callsOf("dl")); n != 0 {
		t.Fatalf("hook called %d times for a kid", n)
	}
}

func TestPending_RatingLimitedGetsNone(t *testing.T) {
	e := newPendingEnv(t)
	e.addon("dl", cards(pcard("k1", "active")))
	if got := e.get(e.user(auth.NewUser{Name: "Лена", MaxRating: "12+"}), ""); len(got) != 0 {
		t.Fatalf("rating-limited got %v", keysOf(got))
	}
	if n := len(e.callsOf("dl")); n != 0 {
		t.Fatalf("hook called %d times for a rating-limited profile", n)
	}
}

func TestPending_BadHrefDropped(t *testing.T) {
	e := newPendingEnv(t)
	ok, proto, abs := pcard("ok", "active"), pcard("proto", "active"), pcard("abs", "active")
	ok.Pending.Href, proto.Pending.Href, abs.Pending.Href = "/addons/dl/x", "//x", "https://x"
	e.addon("dl", cards(ok, proto, abs))
	got := e.get(e.user(auth.NewUser{Name: "Аня"}), "")
	if len(got) != 3 {
		t.Fatalf("cards %v (a bad href drops the href, not the card)", keysOf(got))
	}
	want := []string{"/addons/dl/x", "", ""}
	for i, c := range got {
		if c.Pending.Href != want[i] {
			t.Errorf("%s: href %q, want %q", c.Pending.Key, c.Pending.Href, want[i])
		}
	}
}

func TestPending_ErrorOnlyAdmins(t *testing.T) {
	e := newPendingEnv(t)
	e.addon("dl", cards(pcard("bad", "error"), pcard("good", "paused")))
	if k := strings.Join(keysOf(e.get(e.user(auth.NewUser{Name: "Аня"}), "")), ","); k != "dl:good" {
		t.Fatalf("adult: %q", k)
	}
	if k := strings.Join(keysOf(e.get(e.user(auth.NewUser{Name: "Админ", IsAdmin: true, Password: "secret-pass-1"}), "")), ","); k != "dl:bad,dl:good" {
		t.Fatalf("admin: %q", k)
	}
}

func TestPending_UnknownStatusDropped(t *testing.T) {
	e := newPendingEnv(t)
	e.addon("dl", cards(pcard("a", "active"), pcard("w", "waiting"), pcard("p", "paused"), pcard("o", "organizing"),
		pcard("x", "bogus"), pcard("e", "")))
	if k := strings.Join(keysOf(e.get(e.user(auth.NewUser{Name: "Аня"}), "")), ","); k != "dl:a,dl:w,dl:p,dl:o" {
		t.Fatalf("keys %q", k)
	}
}

func TestPending_ScopeMine(t *testing.T) {
	e := newPendingEnv(t)
	e.addon("dl", cards())
	tok := e.user(auth.NewUser{Name: "Аня"})
	e.get(tok, "?scope=mine")
	e.get(tok, "")
	calls := e.callsOf("dl")
	if len(calls) != 2 {
		t.Fatalf("%d calls", len(calls))
	}
	for i, want := range []string{"mine", "all"} {
		var in struct{ Scope string }
		if err := json.Unmarshal(calls[i].body, &in); err != nil || in.Scope != want {
			t.Errorf("call %d body %q, want scope %q", i, calls[i].body, want)
		}
	}
}

func TestPending_SlowAddonLeftOut(t *testing.T) {
	e := newPendingEnv(t)
	e.addon("dl", func(w http.ResponseWriter) {
		time.Sleep(3 * time.Second)
		cards(pcard("late", "active"))(w)
	})
	e.addon("hello", cards(pcard("k2", "active")))
	tok := e.user(auth.NewUser{Name: "Аня"})
	start := time.Now()
	got := e.get(tok, "")
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Fatalf("answered after %v", d)
	}
	if k := strings.Join(keysOf(got), ","); k != "hello:k2" {
		t.Fatalf("keys %q", k)
	}
}

func TestPending_BadJSONLeftOut(t *testing.T) {
	e := newPendingEnv(t)
	e.addon("dl", func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"cards": "nope"}`)
	})
	e.addon("hello", cards(pcard("k2", "active")))
	if k := strings.Join(keysOf(e.get(e.user(auth.NewUser{Name: "Аня"}), "")), ","); k != "hello:k2" {
		t.Fatalf("keys %q", k)
	}
	if l := e.logged(); !strings.Contains(l, "addon hook: bad answer") || !strings.Contains(l, "addon=dl") ||
		!strings.Contains(l, "hook=library.pending") || strings.Contains(l, "addon=hello") {
		t.Fatalf("log %q", l)
	}
}

func TestPending_PosterName(t *testing.T) {
	e := newPendingEnv(t)
	name := strings.Repeat("ab", 20) + ".jpg" // a name POST /images gives
	cached, short, remote := pcard("c", "active"), pcard("s", "active"), pcard("r", "active")
	cached.Card.Poster, short.Card.Poster, remote.Card.Poster = name, "abc.jpg", "https://x/p.jpg"
	e.addon("dl", cards(cached, short, remote))
	got := e.get(e.user(auth.NewUser{Name: "Аня"}), "")
	want := []string{"/api/images/" + name, "", ""}
	if len(got) != len(want) {
		t.Fatalf("cards %v (a bad poster empties the poster, not the card)", keysOf(got))
	}
	for i, c := range got {
		if c.Poster != want[i] {
			t.Errorf("%s: poster %q, want %q", c.Pending.Key, c.Poster, want[i])
		}
	}
}

func TestPending_NoAddonsEmptyList(t *testing.T) {
	e := newPendingEnv(t)
	if got := e.get(e.user(auth.NewUser{Name: "Аня"}), ""); len(got) != 0 {
		t.Fatalf("got %v", keysOf(got))
	}
	r := httptest.NewRequest("GET", "/api/library/pending", nil)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", w.Code)
	}
}

func TestPending_HintCleaned(t *testing.T) {
	e := newPendingEnv(t)
	ctl, long := pcard("c", "active"), pcard("l", "active")
	ctl.Pending.Hint = " Ждёт\nместа\x00 на диске\t"
	long.Pending.Hint = strings.Repeat("я", 250)
	e.addon("dl", cards(ctl, long))
	got := e.get(e.user(auth.NewUser{Name: "Аня"}), "")
	if len(got) != 2 {
		t.Fatalf("cards %v", keysOf(got))
	}
	if h := got[0].Pending.Hint; h != "Ждётместа на диске" {
		t.Errorf("control characters: %q", h)
	}
	if h := got[1].Pending.Hint; h != strings.Repeat("я", 200) {
		t.Errorf("long hint: %d runes", len([]rune(h)))
	}
}

func TestPending_KindChecked(t *testing.T) {
	e := newPendingEnv(t)
	var cs []addonkit.PendingCard
	for _, k := range []string{"movie", "series", "album", "book", "audiobook", "comic", "episode", "film", ""} {
		c := pcard(k, "active")
		c.Card.Kind, c.Card.TmdbKind, c.Card.TmdbID = k, "", 0
		cs = append(cs, c)
	}
	for _, tk := range []string{"movie", "series", "tv"} {
		c := pcard("t-"+tk, "active")
		c.Card.TmdbKind = tk
		cs = append(cs, c)
	}
	e.addon("dl", cards(cs...))
	want := "dl:movie,dl:series,dl:album,dl:book,dl:audiobook,dl:comic,dl:t-movie,dl:t-series"
	if k := strings.Join(keysOf(e.get(e.user(auth.NewUser{Name: "Аня"}), "")), ","); k != want {
		t.Fatalf("keys %q\nwant %q", k, want)
	}
}
