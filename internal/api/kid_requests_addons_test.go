package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/notify"
)

// hookCall is one requests.* call a fake addon got.
type hookCall struct {
	addon, hook string
	body        string
	user        string // X-Antresol-User
}

// reqEnv is the library env with kids' requests and fake addons answering
// the requests.* hooks; the addons' Open is Deps.RequestsOpen.
type reqEnv struct {
	*libEnv
	fake  *fakeAddons
	reqs  *AddonRequests
	hooks *addons.Hooks

	mu      sync.Mutex
	calls   []hookCall
	answers map[string]func(w http.ResponseWriter, body string) // "<id> <hook>"
}

func newReqEnv(t *testing.T, cat RequestCatalog, opt func(*Deps)) *reqEnv {
	old := kidAskInterval
	kidAskInterval = 0
	t.Cleanup(func() { kidAskInterval = old })
	e := &reqEnv{answers: map[string]func(http.ResponseWriter, string){},
		fake: &fakeAddons{installed: map[string]addons.Installed{}, status: map[string]addons.Status{},
			sock: map[string]string{}, tokens: map[string]string{}, webRoot: t.TempDir()}}
	e.hooks = &addons.Hooks{T: e.fake}
	e.reqs = NewAddonRequests(e.hooks)
	e.libEnv = newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB, d.Requests, d.Addons, d.AddonHooks = db, cat, e.fake, e.hooks
		d.RequestsOpen = e.reqs.Open
		if opt != nil {
			opt(d)
		}
	})
	return e
}

// addon starts addon id (named «name») declaring hooks, with perms.
func (e *reqEnv) addon(id, name string, perms []string, hooks ...string) {
	e.t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rq")
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
		hook := strings.TrimPrefix(r.URL.Path, "/hooks/")
		e.mu.Lock()
		e.calls = append(e.calls, hookCall{addon: id, hook: hook, body: string(b), user: r.Header.Get("X-Antresol-User")})
		ans := e.answers[id+" "+hook]
		e.mu.Unlock()
		if ans == nil {
			http.NotFound(w, r)
			return
		}
		ans(w, string(b))
	})}
	go srv.Serve(ln)
	e.t.Cleanup(func() { srv.Close(); os.RemoveAll(dir) })
	e.fake.mu.Lock()
	e.fake.installed[id] = addons.Installed{ID: id, Version: "1.0.0", Enabled: true,
		Manifest: addons.Manifest{ID: id, Name: addons.Text{RU: name, EN: name}, Version: "1.0.0", Contract: addons.ContractVersion,
			Hooks: hooks, Permissions: perms}}
	e.fake.status[id], e.fake.sock[id], e.fake.tokens["tok-"+id] = addons.StatusRunning, sock, id
	e.fake.mu.Unlock()
	e.reqs.StatusChanged(id, addons.StatusRunning) // as the supervisor's OnStatus
}

// answer makes addon id answer hook with code and body.
func (e *reqEnv) answer(id, hook string, code int, body string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.answers[id+" "+hook] = func(w http.ResponseWriter, _ string) {
		if body != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

func (e *reqEnv) setStatus(id string, st addons.Status) {
	e.fake.mu.Lock()
	e.fake.status[id] = st
	e.fake.mu.Unlock()
	e.reqs.StatusChanged(id, st)
}

func (e *reqEnv) hookCalls(hook string) []hookCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []hookCall
	for _, c := range e.calls {
		if c.hook == hook {
			out = append(out, c)
		}
	}
	return out
}

func (e *reqEnv) canRequest(c *http.Cookie) bool {
	e.t.Helper()
	w := e.do("GET", "/api/me", "", c)
	if w.Code != 200 {
		e.t.Fatalf("me: %d %s", w.Code, w.Body)
	}
	return decodeJSON[struct {
		CanRequest bool `json:"canRequest"`
	}](e.t, w.Body.Bytes()).CanRequest
}

// No addon: closed; an addon answering open: open; that addon stopped:
// closed at once, its status change dropping the cached answer.
func TestRequestsOpen_FromAddon(t *testing.T) {
	e := newReqEnv(t, fakeRequests{}, nil)
	kid := e.kid("")
	if e.canRequest(kid) {
		t.Fatal("open without an addon")
	}
	e.addon("dl", "Каталог", nil, "requests.open")
	e.answer("dl", "requests.open", 200, `{"open":true}`)
	if !e.canRequest(kid) {
		t.Fatal("closed with an addon that opens requests")
	}
	e.fake.mu.Lock()
	e.fake.status["dl"] = addons.StatusStopped // the cached answer still says open
	e.fake.mu.Unlock()
	if !e.canRequest(kid) {
		t.Fatal("the cached answer was not used")
	}
	e.reqs.StatusChanged("dl", addons.StatusStopped) // the supervisor's OnStatus
	if e.canRequest(kid) {
		t.Fatal("still open after the addon stopped")
	}
	if w := e.do("POST", "/api/requests", `{"kind":"movie","tmdbId":603}`, kid); w.Code != http.StatusForbidden {
		t.Fatalf("ask while closed: %d %s", w.Code, w.Body)
	}
}

// Two /api/me within the cache window ask the addon once.
func TestRequestsOpen_Cached60s(t *testing.T) {
	e := newReqEnv(t, fakeRequests{}, nil)
	e.addon("dl", "Каталог", nil, "requests.open")
	e.answer("dl", "requests.open", 200, `{"open":true}`)
	kid := e.kid("")
	if !e.canRequest(kid) || !e.canRequest(kid) {
		t.Fatal("closed")
	}
	if n := len(e.hookCalls("requests.open")); n != 1 {
		t.Fatalf("requests.open called %d times", n)
	}
	now := time.Now().Add(requestsOpenTTL + time.Second)
	e.reqs.now = func() time.Time { return now }
	e.canRequest(kid)
	if n := len(e.hookCalls("requests.open")); n != 2 {
		t.Fatalf("after the window: %d calls", n)
	}
}

// A kid's notification topics include «Просьбы» while an addon opens
// requests (the notifier's RequestsOpen is the addons' Open).
func TestNotifyTopics_KidRequestsOpen(t *testing.T) {
	e := newReqEnv(t, fakeRequests{}, nil)
	svc := &notify.Service{RequestsOpen: e.reqs.Open}
	kid := auth.User{ID: 9, IsKid: true}
	if slices.Contains(svc.TopicsFor(context.Background(), kid), notify.Requests) {
		t.Fatal("requests topic without an addon")
	}
	e.addon("dl", "Каталог", nil, "requests.open")
	e.answer("dl", "requests.open", 200, `{"open":true}`)
	if !slices.Contains(svc.TopicsFor(context.Background(), kid), notify.Requests) {
		t.Fatal("no requests topic while an addon opens requests")
	}
}

// The requests.* hooks answer for the household: no profile header.
func TestRequestHooks_NoUserHeader(t *testing.T) {
	e := newReqEnv(t, fakeRequests{}, nil)
	e.addon("dl", "Каталог", nil, "requests.open", "requests.title", "requests.search")
	e.answer("dl", "requests.open", 200, `{"open":true}`)
	e.answer("dl", "requests.title", 200, `{"title":"Группа крови","subtitle":"Кино","year":1988}`)
	e.answer("dl", "requests.search", 200, `{"candidates":[]}`)
	kid := e.kid("")
	if w := e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-1","title":"x"}`, kid); w.Code != 200 {
		t.Fatalf("ask: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/requests/search?kind=music&q=kino", "", kid); w.Code != 200 {
		t.Fatalf("search: %d %s", w.Code, w.Body)
	}
	for _, h := range []string{"requests.open", "requests.title", "requests.search"} {
		cs := e.hookCalls(h)
		if len(cs) == 0 {
			t.Fatalf("%s not called", h)
		}
		for _, c := range cs {
			if c.user != "" {
				t.Fatalf("%s with a profile: %s", h, c.user)
			}
		}
	}
}

// openAddon is an env whose addon "dl" opens requests and names titles.
func openAddon(t *testing.T, title func(w http.ResponseWriter, body string)) *reqEnv {
	e := newReqEnv(t, fakeRequests{}, nil)
	e.addon("dl", "Каталог", []string{"requests"}, "requests.open", "requests.title")
	e.answer("dl", "requests.open", 200, `{"open":true}`)
	if title != nil {
		e.mu.Lock()
		e.answers["dl requests.title"] = title
		e.mu.Unlock()
	}
	return e
}

type askedRow struct {
	ID       int64  `json:"id"`
	Key      string `json:"key"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Year     int    `json:"year"`
}

// The addon's title replaces the client's, and the keys come from it: a
// second ask with another client title but the same extId is the first
// request.
func TestAsk_AlbumNamedByHook(t *testing.T) {
	e := openAddon(t, nil)
	e.answer("dl", "requests.title", 200, `{"title":"Группа крови","subtitle":"Кино","year":1988}`)
	kid := e.kid("")
	w := e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-1","title":"Подделка","subtitle":"Кто-то","year":2020}`, kid)
	if w.Code != 200 {
		t.Fatalf("ask: %d %s", w.Code, w.Body)
	}
	first := decodeJSON[askedRow](t, w.Body.Bytes())
	if first.Title != "Группа крови" || first.Subtitle != "Кино" || first.Year != 1988 || first.Key != "ext:mb-1" {
		t.Fatalf("ask: %s", w.Body)
	}
	if c := e.hookCalls("requests.title"); len(c) != 1 || c[0].body != `{"kind":"music","extId":"mb-1"}` {
		t.Fatalf("title calls: %+v", c)
	}
	var nameKey string
	if err := e.db.QueryRow(`SELECT name_key FROM kid_requests WHERE id=?`, first.ID).Scan(&nameKey); err != nil || nameKey != "name:кино — группа крови" {
		t.Fatalf("name key %q %v", nameKey, err)
	}
	w = e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-1","title":"Другое"}`, kid)
	if again := decodeJSON[askedRow](t, w.Body.Bytes()); w.Code != 200 || again.ID != first.ID || again.Title != "Группа крови" {
		t.Fatalf("again: %d %s", w.Code, w.Body)
	}
}

// The addon's cover is kept when it is a catalog's, else dropped.
func TestAsk_AlbumHookPoster(t *testing.T) {
	e := openAddon(t, func(w http.ResponseWriter, body string) {
		poster := "https://evil.example/a.jpg"
		if strings.Contains(body, "mb-good") {
			poster = "https://coverartarchive.org/release-group/x/front-250"
		}
		json.NewEncoder(w).Encode(map[string]any{"title": "Альбом", "poster": poster})
	})
	kid := e.kid("")
	for _, c := range []struct{ ext, want string }{
		{"mb-good", "https://coverartarchive.org/release-group/x/front-250"},
		{"mb-bad1", ""},
	} {
		if w := e.do("POST", "/api/requests", `{"kind":"music","extId":"`+c.ext+`","title":"x","poster":"https://coverartarchive.org/c.jpg"}`, kid); w.Code != 200 {
			t.Fatalf("%s: %d %s", c.ext, w.Code, w.Body)
		}
		var got string
		if err := e.db.QueryRow(`SELECT poster FROM kid_requests WHERE ext_id=?`, c.ext).Scan(&got); err != nil || got != c.want {
			t.Errorf("%s: poster %q %v, want %q", c.ext, got, err, c.want)
		}
	}
}

// An album the addon marks explicit is refused.
func TestAsk_AlbumExplicit(t *testing.T) {
	e := openAddon(t, nil)
	e.answer("dl", "requests.title", 200, `{"title":"Нельзя","explicit":true}`)
	kid := e.kid("")
	w := e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-1","title":"Можно"}`, kid)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Недоступно для этого профиля") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var n int
	if e.db.QueryRow(`SELECT COUNT(*) FROM kid_requests`).Scan(&n); n != 0 {
		t.Fatalf("stored %d", n)
	}
}

// No addon knows the title (204, 404): the client's text, as before; a
// refusal (409) is a 400 with the addon's text.
func TestAsk_AlbumHookUnknown(t *testing.T) {
	e := openAddon(t, nil)
	e.addon("lib", "Книги", nil, "requests.title")
	e.answer("dl", "requests.title", 204, "")
	kid := e.kid("")
	w := e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-1","title":"Группа крови","subtitle":"Кино"}`, kid)
	if got := decodeJSON[askedRow](t, w.Body.Bytes()); w.Code != 200 || got.Title != "Группа крови" || got.Subtitle != "Кино" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if n := len(e.hookCalls("requests.title")); n != 2 {
		t.Fatalf("asked %d addons", n)
	}
	e.answer("dl", "requests.title", 409, `{"error":"Этот каталог закрыт"}`)
	w = e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-2","title":"Звезда"}`, kid)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Этот каталог закрыт") {
		t.Fatalf("refusal: %d %s", w.Code, w.Body)
	}
	// without a catalog id the addons are not asked
	before := len(e.hookCalls("requests.title"))
	if w := e.do("POST", "/api/requests", `{"kind":"comic","title":"Берсерк"}`, kid); w.Code != 200 {
		t.Fatalf("comic: %d %s", w.Code, w.Body)
	}
	if n := len(e.hookCalls("requests.title")); n != before {
		t.Fatalf("asked for a request without extId")
	}
}

// The addon is down: 503 naming it; nothing stored.
func TestAsk_AlbumHookDown(t *testing.T) {
	e := openAddon(t, nil)
	e.addon("lib", "Книги", nil, "requests.title")
	e.setStatus("lib", addons.StatusCrashed)
	e.answer("dl", "requests.title", 404, "")
	kid := e.kid("")
	w := e.do("POST", "/api/requests", `{"kind":"book","extId":"/works/OL1W","title":"Сказки"}`, kid)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "Расширение «Книги» не ответило") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var n int
	if e.db.QueryRow(`SELECT COUNT(*) FROM kid_requests`).Scan(&n); n != 0 {
		t.Fatalf("stored %d", n)
	}
	// another addon that answers wins over the down one
	e.answer("dl", "requests.title", 200, `{"title":"Сказки","subtitle":"Пушкин"}`)
	if w := e.do("POST", "/api/requests", `{"kind":"book","extId":"/works/OL1W","title":"Сказки"}`, kid); w.Code != 200 {
		t.Fatalf("answered: %d %s", w.Code, w.Body)
	}
}

// profileID is the id of the profile with this name.
func (e *reqEnv) profileID(name string) int64 {
	e.t.Helper()
	var id int64
	if err := e.db.QueryRow(`SELECT id FROM users WHERE name=?`, name).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *reqEnv) approve(tok, body string) (int, string) {
	w := e.do("POST", "/api/addon-core/v1/requests/approve", body, nil, "Authorization", "Bearer "+tok, "Content-Type", "application/json")
	return w.Code, w.Body.String()
}

// An addon approves the requests of what an admin started getting: an
// album by its name key, the kid told; an adult that is not an admin is
// refused, as is an addon without the requests permission. Ported from
// the full build's approveFor cases (TestApproveByArtistAndTitle).
func TestApprove_FromAddon(t *testing.T) {
	var svc *notify.Service
	e := newReqEnv(t, fakeRequests{}, func(d *Deps) {
		svc = &notify.Service{DB: d.DB, Users: d.Users, Secret: []byte("s"), RequestsOpen: func(context.Context) bool { return true }}
		d.Notify = svc
	})
	e.addon("dl", "Каталог", []string{"requests"}, "requests.open")
	e.addon("hello", "Привет", nil)
	e.answer("dl", "requests.open", 200, `{"open":true}`)
	kid := e.kid("")
	kidID := e.profileID("Вася")
	if _, err := svc.SetPrefs(context.Background(), auth.User{ID: kidID, IsKid: true}, notify.Prefs{Email: "kid@example.com", Requests: true}); err != nil {
		t.Fatal(err)
	}
	w := e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-1","title":"Группа крови","subtitle":"Кино"}`, kid)
	if w.Code != 200 {
		t.Fatalf("ask: %d %s", w.Code, w.Body)
	}
	album := decodeJSON[askedRow](t, w.Body.Bytes())
	if w := e.do("POST", "/api/requests", `{"kind":"book","title":"Сказки","subtitle":"Пушкин"}`, kid); w.Code != 200 {
		t.Fatalf("ask book: %d %s", w.Code, w.Body)
	}
	e.login(`{"name":"Маша"}`)
	if code, body := e.approve("tok-dl", `{"adminId":`+itoa(e.profileID("Маша"))+`,"kind":"music","title":"группа  крови","artist":"КИНО"}`); code != http.StatusBadRequest {
		t.Fatalf("non-admin: %d %s", code, body)
	}
	if code, body := e.approve("tok-hello", `{"adminId":1,"kind":"music","title":"группа  крови","artist":"КИНО"}`); code != http.StatusForbidden {
		t.Fatalf("no permission: %d %s", code, body)
	}
	if code, body := e.approve("tok-dl", `{"adminId":1,"kind":"music","title":"группа  крови","artist":"КИНО"}`); code != 200 || body != `{"approved":1}`+"\n" {
		t.Fatalf("album: %d %s", code, body)
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM notify_events WHERE user_id=? AND topic='requests' AND key=?`, kidID, "req:"+itoa(album.ID)+":approved").Scan(&n)
	if n != 1 {
		t.Fatalf("approval events %d", n)
	}
	// a book by its catalog id and the author's name alike
	if code, body := e.approve("tok-dl", `{"adminId":1,"kind":"book","extId":"/works/OL9W","title":"Сказки","artist":"Пушкин"}`); code != 200 || body != `{"approved":1}`+"\n" {
		t.Fatalf("book: %d %s", code, body)
	}
	if code, body := e.approve("tok-dl", `{"adminId":1,"kind":"book","title":"Сказки","artist":"Пушкин"}`); code != 200 || body != `{"approved":0}`+"\n" {
		t.Fatalf("again: %d %s", code, body)
	}
	if code, body := e.approve("tok-dl", `{"adminId":1,"kind":"nope","title":"x"}`); code != http.StatusBadRequest {
		t.Fatalf("kind: %d %s", code, body)
	}
}

// /api/me names no capability of an addon's ways («can add» is the web's
// question): its can… fields are the core's own.
func TestMe_CapabilityKeysAreCore(t *testing.T) {
	e := newReqEnv(t, fakeRequests{}, nil)
	for _, c := range []*http.Cookie{e.admin, e.kid("")} {
		w := e.do("GET", "/api/me", "", c)
		var keys map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &keys); err != nil || w.Code != 200 || keys["id"] == nil {
			t.Fatalf("me %d %s", w.Code, w.Body)
		}
		set := map[string]bool{}
		for k := range keys {
			set[k] = true
		}
		for _, k := range capabilityKeys(set) {
			if k != "canRequest" {
				t.Fatalf("me has %s: %s", k, w.Body)
			}
		}
	}
}

// capabilityKeys are the can… keys of a JSON object's key set, sorted.
func capabilityKeys(keys map[string]bool) []string {
	var out []string
	for k := range keys {
		if strings.HasPrefix(k, "can") {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// A throttled kid (too soon, or 20 waiting) never runs requests.title.
func TestAsk_ThrottledNoTitleHook(t *testing.T) {
	e := openAddon(t, nil)
	e.answer("dl", "requests.title", 200, `{"title":"Группа крови","subtitle":"Кино"}`)
	kid := e.kid("")
	if w := e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-1","title":"x"}`, kid); w.Code != 200 {
		t.Fatalf("ask: %d %s", w.Code, w.Body)
	}
	if n := len(e.hookCalls("requests.title")); n != 1 {
		t.Fatalf("first ask: %d calls", n)
	}
	// asked already: answered from the database, no hook either
	if w := e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-1","title":"y"}`, kid); w.Code != 200 {
		t.Fatalf("again: %d %s", w.Code, w.Body)
	}
	kidAskInterval = time.Hour
	if w := e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-2","title":"x"}`, kid); w.Code != http.StatusTooManyRequests {
		t.Fatalf("too soon: %d %s", w.Code, w.Body)
	}
	kidAskInterval = 0
	uid := e.profileID("Вася")
	for i := range maxPendingPerKid {
		if _, err := e.db.Exec(`INSERT INTO kid_requests (user_id, kind, title_key, title) VALUES (?, 'comic', ?, 'x')`, uid, "name:x"+itoa(int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	if w := e.do("POST", "/api/requests", `{"kind":"music","extId":"mb-3","title":"x"}`, kid); w.Code != http.StatusTooManyRequests {
		t.Fatalf("too many: %d %s", w.Code, w.Body)
	}
	if n := len(e.hookCalls("requests.title")); n != 1 {
		t.Fatalf("throttled asks ran the hook: %d calls", n)
	}
}

// A stopped or incompatible addon is not asked to name a title (no 503
// for it); an addon's name replaces the client's aliases too.
func TestAsk_TitleSkipsIncompatible(t *testing.T) {
	e := openAddon(t, nil)
	e.answer("dl", "requests.title", 204, "")
	e.addon("lib", "Книги", nil, "requests.title")
	e.setStatus("lib", addons.StatusIncompatible)
	e.addon("off", "Выкл", nil, "requests.title")
	e.setStatus("off", addons.StatusStopped)
	kid := e.kid("")
	if w := e.do("POST", "/api/requests", `{"kind":"book","extId":"/works/OL1W","title":"Сказки","aliases":["a"]}`, kid); w.Code != 200 {
		t.Fatalf("ask: %d %s", w.Code, w.Body)
	}
	e.answer("dl", "requests.title", 200, `{"title":"Руслан и Людмила","subtitle":"Пушкин"}`)
	w := e.do("POST", "/api/requests", `{"kind":"book","extId":"/works/OL2W","title":"Чужое","aliases":["клиентское"]}`, kid)
	if w.Code != 200 || strings.Contains(w.Body.String(), "клиентское") || !strings.Contains(w.Body.String(), `"aliases":[]`) {
		t.Fatalf("named: %d %s", w.Code, w.Body)
	}
}
