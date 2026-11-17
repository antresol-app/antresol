package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/library"
)

// fakeAddons is an addonRuntime over a map, with one addon process (an
// httptest server on a Unix socket) for the proxy.
type fakeAddons struct {
	mu        sync.Mutex
	installed map[string]addons.Installed
	status    map[string]addons.Status
	sock      map[string]string
	tokens    map[string]string // token → id
	webRoot   string
	calls     []string
	pinned    map[string]bool
	failed    []addons.Pin // failed pins
}

func (f *fakeAddons) PinFailed(id string) bool {
	for _, p := range f.FailedPins() {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (f *fakeAddons) FailedPins() []addons.Pin {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failed
}

func (f *fakeAddons) Pinned(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pinned[id]
}

// refusePinned is the manager's ErrPinned for a pinned id.
func (f *fakeAddons) refusePinned(id string) error {
	if f.Pinned(id) {
		return fmt.Errorf("addon %s: %w", id, addons.ErrPinned)
	}
	return nil
}

func (f *fakeAddons) record(c string) { f.mu.Lock(); f.calls = append(f.calls, c); f.mu.Unlock() }

func (f *fakeAddons) List(context.Context) ([]addons.Installed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []addons.Installed
	for _, in := range f.installed {
		out = append(out, in)
	}
	slices.SortFunc(out, func(a, b addons.Installed) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (f *fakeAddons) Get(_ context.Context, id string) (addons.Installed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in, ok := f.installed[id]
	if !ok {
		return addons.Installed{}, addons.ErrNotFound
	}
	return in, nil
}

func (f *fakeAddons) Status(id string) addons.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.status[id]; ok {
		return s
	}
	return addons.StatusStopped
}

func (f *fakeAddons) Serving(id string) (addons.Status, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.status[id]
	if !ok {
		st = addons.StatusStopped
	}
	if st != addons.StatusRunning {
		return st, ""
	}
	return st, f.sock[id]
}

func (f *fakeAddons) WebDir(id, version string) string {
	return filepath.Join(f.webRoot, id, version, "web")
}
func (f *fakeAddons) LogTail(id string, n int) []string {
	return []string{fmt.Sprintf("%s: %d", id, n)}
}

func (f *fakeAddons) IDForToken(_ context.Context, tok string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.tokens[tok]; ok {
		return id, nil
	}
	return "", addons.ErrNotFound
}

func (f *fakeAddons) Preview(_ context.Context, url string) (addons.Manifest, error) {
	f.record("preview " + url)
	return addons.Manifest{ID: "new", Version: "1.0.0"}, nil
}

func (f *fakeAddons) Install(_ context.Context, url, version string) (addons.Installed, error) {
	f.record("install " + url + " " + version)
	if url == "local:hello" { // the pinned id from another source
		if err := f.refusePinned("hello"); err != nil {
			return addons.Installed{}, err
		}
	}
	if url == "github.com/o/boom" {
		return addons.Installed{}, fmt.Errorf("open /var/secret/addons: permission denied")
	}
	if url == "github.com/o/user" { // a user message, wrapped
		return addons.Installed{}, fmt.Errorf("install: %w", addons.ErrNoLibrary)
	}
	return addons.Installed{ID: "new", Version: "1.0.0", Enabled: true}, nil
}

func (f *fakeAddons) Update(_ context.Context, id, version string) (addons.Installed, error) {
	f.record("update " + id + " " + version)
	if err := f.refusePinned(id); err != nil {
		return addons.Installed{}, err
	}
	return f.Get(context.Background(), id)
}

func (f *fakeAddons) SetEnabled(_ context.Context, id string, on bool) error {
	f.record(fmt.Sprintf("enable %s %v", id, on))
	if on && f.PinFailed(id) {
		return addons.ErrPinNotApplied
	}
	return nil
}

func (f *fakeAddons) Remove(_ context.Context, id string, data bool) error {
	f.record(fmt.Sprintf("remove %s %v", id, data))
	return f.refusePinned(id)
}

func (f *fakeAddons) UpdateAvailable(_ context.Context, id string) (string, bool) {
	if id == "hello" {
		return "1.1.0", true
	}
	return "", false
}

// echo is a fake addon process on a Unix socket.
type echo struct {
	sock string
	srv  *httptest.Server
	slow chan struct{} // gets a value when /api/slow has started
	hits *atomic.Int64 // requests with a path the core must refuse
}

// echoAddon answers on a Unix socket: /api/echo with what it got (JSON),
// /api/page an HTML page, /api/stream an event stream; all set a cookie.
// /api/slow answers nothing until its connection is closed.
func echoAddon(t *testing.T) *echo {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ah")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	slow := make(chan struct{}, 1)
	hits := &atomic.Int64{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/slow" {
			slow <- struct{}{}
			<-r.Context().Done()
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "evil", Value: "1"})
		if strings.Contains(r.URL.Path, "..") || strings.Contains(r.URL.Path, "//") || strings.ContainsAny(r.URL.Path, `%;\`) {
			hits.Add(1) // a path the core should have refused
		}
		switch r.URL.Path {
		case "/api/echo":
			w.Header().Set("Content-Type", "application/json")
			for _, h := range []string{"Clear-Site-Data", "Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Service-Worker-Allowed"} {
				w.Header().Set(h, "*")
			}
			json.NewEncoder(w).Encode(map[string]any{"path": r.URL.Path, "query": r.URL.RawQuery, "method": r.Method, "headers": r.Header})
		case "/api/foo", "/api/admin/foo":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true}`)
		case "/api/page":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<script>alert(1)</script>")
		case "/api/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: 1\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(func() {
		srv.Close()
		os.RemoveAll(dir)
	})
	return &echo{sock: sock, srv: srv, slow: slow, hits: hits}
}

type addonEnv struct {
	*env
	fake        *fakeAddons
	echo        *echo
	bus         *events.Bus
	admin, user *http.Cookie
	kid         *http.Cookie
}

// newAddonEnv: the router over fakeAddons; more sets other Deps.
func newAddonEnv(t *testing.T, more ...func(*Deps)) *addonEnv {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	web := t.TempDir()
	hello := addons.Installed{ID: "hello", Source: "github.com/o/hello", Version: "1.0.0", Enabled: true,
		Manifest: addons.Manifest{ID: "hello", Name: addons.Text{RU: "Привет", EN: "Hello"}, Version: "1.0.0",
			Contract: addons.ContractVersion, Web: &addons.Platform{}, Permissions: []string{"events"}}}
	lib := hello
	lib.ID, lib.Manifest.ID, lib.Manifest.Permissions = "lib", "lib", []string{"library.read"}
	off := hello
	off.ID, off.Manifest.ID, off.Enabled = "off", "off", false
	ea := echoAddon(t)
	fake := &fakeAddons{
		installed: map[string]addons.Installed{"hello": hello, "lib": lib, "off": off},
		status:    map[string]addons.Status{"hello": addons.StatusRunning, "lib": addons.StatusCrashed},
		sock:      map[string]string{"hello": ea.sock},
		tokens:    map[string]string{"tok-hello": "hello", "tok-lib": "lib"},
		webRoot:   web,
	}
	wd := fake.WebDir("hello", "1.0.0")
	if err := os.MkdirAll(filepath.Join(wd, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "index.js"), []byte("export default 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(web, "secret"), []byte("secret"), 0o644)
	bus := &events.Bus{}
	st, ss := auth.NewStore(d), auth.NewSessions(d)
	e := &addonEnv{env: &env{db: d, t: t, store: st}, fake: fake, echo: ea, bus: bus}
	deps := Deps{Users: st, Sessions: ss, Events: bus, Addons: fake, Library: library.NewRepo(d), DB: d}
	for _, f := range more {
		f(&deps)
	}
	e.h = NewRouter(deps)
	e.admin = e.setup()
	login := func(body string) *http.Cookie {
		w := e.do("POST", "/api/users", body, e.admin)
		if w.Code != 201 {
			t.Fatalf("create user: %d %s", w.Code, w.Body)
		}
		var u auth.User
		json.Unmarshal(w.Body.Bytes(), &u)
		c := session(e.do("POST", "/api/login", `{"userId":`+itoa(u.ID)+`}`, nil))
		if c == nil {
			t.Fatal("login")
		}
		return c
	}
	e.user = login(`{"name":"Маша"}`)
	e.kid = login(`{"name":"Вася","isKid":true,"maxRating":"PG"}`)
	return e
}

func TestAddonAdminRoutesNeedAdmin(t *testing.T) {
	e := newAddonEnv(t)
	routes := [][2]string{{"GET", "/api/addons"}, {"POST", "/api/addons/preview"}, {"POST", "/api/addons"},
		{"POST", "/api/addons/hello/enable"}, {"POST", "/api/addons/hello/disable"}, {"POST", "/api/addons/hello/update"},
		{"DELETE", "/api/addons/hello?data=1"}, {"GET", "/api/addons/hello/log"}}
	for _, c := range []*http.Cookie{e.user, e.kid} {
		for _, rt := range routes {
			if w := e.do(rt[0], rt[1], `{"url":"github.com/o/r"}`, c); w.Code != 403 {
				t.Errorf("%v as non-admin: %d", rt, w.Code)
			}
		}
	}
	if len(e.fake.calls) != 0 {
		t.Fatalf("calls by non-admins: %v", e.fake.calls)
	}
	for _, rt := range routes {
		if w := e.do(rt[0], rt[1], `{"url":"github.com/o/r"}`, nil); w.Code != 401 {
			t.Errorf("%v anonymous: %d", rt, w.Code)
		}
	}
}

func TestAddonAdminRoutes(t *testing.T) {
	e := newAddonEnv(t)
	w := e.do("GET", "/api/addons", "", e.admin, "Accept-Language", "en")
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var list []map[string]any
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 3 {
		t.Fatalf("list = %s", w.Body)
	}
	h := list[0]
	if h["id"] != "hello" || h["name"] != "Hello" || h["version"] != "1.0.0" || h["source"] != "github.com/o/hello" ||
		h["enabled"] != true || h["status"] != "running" || h["update"] != "1.1.0" {
		t.Fatalf("hello = %v", h)
	}
	if list[1]["id"] != "lib" || list[1]["update"] != nil || list[2]["id"] != "off" || list[2]["status"] != "stopped" {
		t.Fatalf("list = %s", w.Body)
	}

	if w := e.do("POST", "/api/addons/preview", `{"url":"github.com/o/new"}`, e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"new"`) {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/addons", `{"url":"github.com/o/new","version":"1.0.0"}`, e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"new"`) {
		t.Fatalf("install: %d %s", w.Code, w.Body)
	}
	for _, rt := range [][3]string{{"POST", "/api/addons/hello/enable"}, {"POST", "/api/addons/hello/disable"},
		{"POST", "/api/addons/hello/update", `{"version":"1.1.0"}`}, {"DELETE", "/api/addons/hello?data=1"}, {"DELETE", "/api/addons/lib"}} {
		if w := e.do(rt[0], rt[1], rt[2], e.admin); w.Code != 200 && w.Code != 204 {
			t.Fatalf("%v: %d %s", rt, w.Code, w.Body)
		}
	}
	want := []string{"preview github.com/o/new", "install github.com/o/new 1.0.0", "enable hello true", "enable hello false",
		"update hello 1.1.0", "remove hello true", "remove lib false"}
	if fmt.Sprint(e.fake.calls) != fmt.Sprint(want) {
		t.Fatalf("calls = %v", e.fake.calls)
	}
	if w := e.do("GET", "/api/addons/hello/log?n=5", "", e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), "hello: 5") {
		t.Fatalf("log: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/addons/nope/update", `{"version":"1.0.0"}`, e.admin); w.Code != 404 {
		t.Fatalf("update of an unknown addon: %d", w.Code)
	}
}

// TestAddonPinnedLocked409: a pinned addon is listed as pinned with no
// update; install, update and remove of it answer 409 with the message;
// enable and disable still work.
func TestAddonPinnedLocked409(t *testing.T) {
	e := newAddonEnv(t)
	e.fake.pinned = map[string]bool{"hello": true}
	w := e.do("GET", "/api/addons", "", e.admin)
	var list []map[string]any
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list) != 3 || list[0]["id"] != "hello" || list[0]["pinned"] != true || list[0]["update"] != nil ||
		list[1]["pinned"] != false {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	for _, rt := range [][3]string{{"POST", "/api/addons", `{"url":"local:hello","version":"1.1.0"}`},
		{"POST", "/api/addons/hello/update", `{"version":"1.1.0"}`}, {"DELETE", "/api/addons/hello?data=1", ""}, {"DELETE", "/api/addons/hello", ""}} {
		w := e.do(rt[0], rt[1], rt[2], e.admin)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "Расширение закреплено при развёртывании") {
			t.Errorf("%v: %d %s", rt, w.Code, w.Body)
		}
	}
	if w := e.do("POST", "/api/addons/hello/update", `{"version":"1.1.0"}`, e.admin, "Accept-Language", "en"); w.Code != 409 ||
		!strings.Contains(w.Body.String(), "The addon is pinned by the deployment") {
		t.Errorf("en: %d %s", w.Code, w.Body)
	}
	for _, p := range []string{"/api/addons/hello/disable", "/api/addons/hello/enable"} {
		if w := e.do("POST", p, "", e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"pinned":true`) {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}

	// failed pins: an installed one is marked, a first install that failed
	// is listed; neither is switched on
	e.fake.pinned["gone"] = true
	e.fake.failed = []addons.Pin{{ID: "gone", Version: "2.0.0"}, {ID: "hello", Version: "2.0.0"}}
	w = e.do("GET", "/api/addons", "", e.admin)
	list = nil
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 4 || list[0]["id"] != "hello" || list[0]["pinFailed"] != true || list[1]["pinFailed"] != false ||
		list[3]["id"] != "gone" || list[3]["version"] != "2.0.0" || list[3]["pinned"] != true || list[3]["pinFailed"] != true ||
		list[3]["status"] != "stopped" {
		t.Fatalf("list with failed pins: %s", w.Body)
	}
	if w := e.do("POST", "/api/addons/hello/enable", "", e.admin, "Accept-Language", "en"); w.Code != 409 ||
		!strings.Contains(w.Body.String(), "The pinned version of the addon is not installed") {
		t.Errorf("enable of a failed pin: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/addons/hello/disable", "", e.admin); w.Code != 200 {
		t.Errorf("disable of a failed pin: %d %s", w.Code, w.Body)
	}
}

// TestAddonInstallNeedsVersion: install and update pin the previewed
// version; without one nothing runs.
func TestAddonInstallNeedsVersion(t *testing.T) {
	e := newAddonEnv(t)
	for _, rt := range [][3]string{{"POST", "/api/addons", `{"url":"github.com/o/new"}`}, {"POST", "/api/addons/hello/update", `{}`}} {
		if w := e.do(rt[0], rt[1], rt[2], e.admin); w.Code != 400 || !strings.Contains(w.Body.String(), "Не указана версия расширения") {
			t.Errorf("%v: %d %s", rt, w.Code, w.Body)
		}
	}
	if len(e.fake.calls) != 0 {
		t.Fatalf("calls = %v", e.fake.calls)
	}
}

// TestAddonErrHidesInternalErrors: an error that is no user message
// reaches the user as a generic one.
func TestAddonErrHidesInternalErrors(t *testing.T) {
	e := newAddonEnv(t)
	w := e.do("POST", "/api/addons", `{"url":"github.com/o/boom","version":"1.0.0"}`, e.admin, "Accept-Language", "en")
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), "The addon action failed") {
		t.Fatalf("internal error: %d %s", w.Code, w.Body)
	}
	w = e.do("POST", "/api/addons", `{"url":"github.com/o/user","version":"1.0.0"}`, e.admin, "Accept-Language", "en")
	if w.Code != 400 || !strings.Contains(w.Body.String(), "The library is unavailable") {
		t.Fatalf("user message: %d %s", w.Code, w.Body)
	}
}

func TestAddonWebList(t *testing.T) {
	e := newAddonEnv(t)
	w := e.do("GET", "/api/addons/web", "", e.admin)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `[{"id":"hello","entry":"/addons/hello/ui/index.js?v=1.0.0"}]` {
		t.Fatalf("web: %d %s", w.Code, w.Body)
	}
}

// TestAddonWebStyle: a bundle with index.css lists it beside its entry,
// versioned the same way; the core serves it as CSS to the profiles that may
// load the bundle (a kid gets nothing), and the app's CSP allows a
// stylesheet from its own origin.
func TestAddonWebStyle(t *testing.T) {
	e := newAddonEnv(t)
	wd := e.fake.WebDir("hello", "1.0.0")
	if err := os.WriteFile(filepath.Join(wd, "index.css"), []byte("._a_x{color:var(--label)}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := e.do("GET", "/api/addons/web", "", e.admin)
	want := `[{"id":"hello","entry":"/addons/hello/ui/index.js?v=1.0.0","style":"/addons/hello/ui/index.css?v=1.0.0"}]`
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != want {
		t.Fatalf("web: %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/addons/hello/ui/index.css?v=1.0.0", "", e.admin)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("css: %d %q %q", w.Code, w.Header().Get("Content-Type"), w.Header().Get("Cache-Control"))
	}
	if w := e.do("GET", "/addons/hello/ui/index.css", "", e.kid); w.Code != 403 {
		t.Fatalf("css as a kid: %d", w.Code)
	}
	if !strings.Contains(appCSP, "style-src 'self'") {
		t.Fatalf("the app's CSP has no style-src 'self': %s", appCSP)
	}
}

// TestAddonsAdminOnly: the bundles and the proxy of an addon without
// access "adults" are for admins; kids get their own message.
func TestAddonsAdminOnly(t *testing.T) {
	e := newAddonEnv(t)
	if w := e.do("GET", "/api/addons/web", "", e.user); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("web list as a user: %d %s", w.Code, w.Body)
	}
	for _, p := range []string{"/addons/hello/ui/index.js", "/api/addons/hello/x/echo"} {
		if w := e.do("GET", p, "", e.user); w.Code != 403 || !strings.Contains(w.Body.String(), "Недостаточно прав") {
			t.Errorf("%s as a user: %d %s", p, w.Code, w.Body)
		}
		if w := e.do("GET", p, "", e.kid); w.Code != 403 || !strings.Contains(w.Body.String(), "Недоступно в детском профиле") {
			t.Errorf("%s as a kid: %d %s", p, w.Code, w.Body)
		}
		if w := e.do("GET", p, "", e.admin); w.Code != 200 {
			t.Errorf("%s as admin: %d %s", p, w.Code, w.Body)
		}
	}
}

func TestAddonProxyHeaders(t *testing.T) {
	e := newAddonEnv(t)
	w := e.do("POST", "/api/addons/hello/x/echo?q=1", `{"a":1}`, e.admin,
		"Authorization", "Bearer secret", "X-Antresol-User", `{"isAdmin":true}`, "X-Antresol-Other", "x", "Accept-Encoding", "gzip")
	if w.Code != 200 {
		t.Fatalf("proxy: %d %s", w.Code, w.Body)
	}
	var got struct {
		Path, Query, Method string
		Headers             http.Header
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s (encoding %q)", err, w.Body, w.Header().Get("Content-Encoding"))
	}
	if got.Path != "/api/echo" || got.Query != "q=1" || got.Method != "POST" {
		t.Fatalf("got %+v", got)
	}
	for _, h := range []string{"Cookie", "Authorization", "X-Antresol-Other"} {
		if v := got.Headers.Values(h); len(v) != 0 {
			t.Errorf("%s reached the addon: %v", h, v)
		}
	}
	users := got.Headers.Values("X-Antresol-User")
	if len(users) != 1 {
		t.Fatalf("X-Antresol-User = %v", users)
	}
	for _, c := range users[0] {
		if c > 127 {
			t.Fatalf("non-ASCII in X-Antresol-User: %q", users[0])
		}
	}
	var u struct {
		ID             int64
		Name, Lang     string
		IsAdmin, IsKid bool
	}
	if err := json.Unmarshal([]byte(users[0]), &u); err != nil {
		t.Fatal(err)
	}
	if u.ID != 1 || u.Name != "Папа" || !u.IsAdmin || u.IsKid || u.Lang != "ru" {
		t.Fatalf("user = %+v (%s)", u, users[0])
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Error("addon set a cookie")
	}
	for _, h := range []string{"Clear-Site-Data", "Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Service-Worker-Allowed"} {
		if w.Header().Get(h) != "" {
			t.Errorf("%s reached the browser", h)
		}
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("json headers: %v", w.Header())
	}

	w = e.do("GET", "/api/addons/hello/x/page", "", e.admin, "Accept-Encoding", "gzip")
	if w.Code != 200 || w.Header().Get("Content-Security-Policy") != "sandbox" || w.Header().Get("Set-Cookie") != "" ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("page: %d %v", w.Code, w.Header())
	}
	if w.Header().Get("Content-Encoding") != "" {
		t.Fatal("proxied response gzipped")
	}
	w = e.do("GET", "/api/addons/hello/x/stream", "", e.admin)
	if w.Code != 200 || w.Header().Get("Content-Security-Policy") != "" || !strings.Contains(w.Body.String(), "data: 1") {
		t.Fatalf("stream: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if w := e.do("GET", "/api/addons/hello/x/echo", "", nil); w.Code != 401 {
		t.Fatalf("anonymous proxy: %d", w.Code)
	}
}

func TestAddonProxyNotRunning(t *testing.T) {
	e := newAddonEnv(t)
	for _, id := range []string{"lib", "off", "nope"} {
		w := e.do("GET", "/api/addons/"+id+"/x/echo", "", e.admin)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "Расширение не запущено") {
			t.Fatalf("%s: %d %s", id, w.Code, w.Body)
		}
	}
	// running per status, but the process is gone
	e.fake.sock["hello"] = "/tmp/no-such-addon.sock"
	if w := e.do("GET", "/api/addons/hello/x/echo", "", e.admin); w.Code != 503 {
		t.Fatalf("dead socket: %d %s", w.Code, w.Body)
	}
}

func TestAddonProxyRefusesKids(t *testing.T) {
	e := newAddonEnv(t)
	w := e.do("GET", "/api/addons/hello/x/echo", "", e.kid)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "Недоступно в детском профиле") {
		t.Fatalf("kid: %d %s", w.Code, w.Body)
	}
}

// TestAddonProxyStoppedMidRequest: an addon disabled while a request is in
// flight answers 503 «not running», not 502.
func TestAddonProxyStoppedMidRequest(t *testing.T) {
	e := newAddonEnv(t)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.do("GET", "/api/addons/hello/x/slow", "", e.admin) }()
	select {
	case <-e.echo.slow:
	case <-time.After(5 * time.Second):
		t.Fatal("slow request never reached the addon")
	}
	e.fake.mu.Lock()
	e.fake.status["hello"] = addons.StatusStopped
	e.fake.mu.Unlock()
	e.echo.srv.CloseClientConnections()
	select {
	case w := <-done:
		if w.Code != 503 || !strings.Contains(w.Body.String(), "Расширение не запущено") {
			t.Fatalf("in-flight request: %d %s", w.Code, w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request hangs")
	}
}

func TestAddonTokenRefusedOnSessionRoutes(t *testing.T) {
	e := newAddonEnv(t)
	for _, p := range []string{"/api/addons", "/api/addons/web", "/api/me", "/api/addons/hello/x/echo", "/addons/hello/ui/index.js"} {
		if w := e.do("GET", p, "", nil, "Authorization", "Bearer tok-hello"); w.Code != 401 {
			t.Errorf("%s with an addon token: %d", p, w.Code)
		}
	}
}

func TestAddonCoreAPI(t *testing.T) {
	e := newAddonEnv(t)
	bearer := func(tok string) []string { return []string{"Authorization", "Bearer " + tok} }
	w := e.do("GET", "/api/addon-core/v1/info", "", nil, bearer("tok-hello")...)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"id":"hello","version":"1.0.0","contract":1,"ffmpeg":"","ffprobe":"","publicUrl":"",`+
		`"libraryRoots":{"movies":"","series":"","music":"","ebooks":"","audiobooks":"","comics":""},"mediaRoot":"","fileRoots":[],"missingRoots":[]}` {
		t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	// a session is no addon
	if w := e.do("GET", "/api/addon-core/v1/info", "", e.admin); w.Code != 401 {
		t.Fatalf("info with a session: %d", w.Code)
	}
	if w := e.do("GET", "/api/addon-core/v1/info", "", nil, bearer("bogus")...); w.Code != 401 {
		t.Fatalf("bad token: %d", w.Code)
	}

	// library.read: stage 1's id and title keep their names in the full Item
	movie, err := library.NewRepo(e.db).UpsertMovie(context.Background(), library.MovieInput{Title: "Матрица", Year: 1999, TmdbID: 603,
		File: library.FileInput{Path: "/m/Matrix.mkv", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if w := e.do("GET", "/api/addon-core/v1/library/tmdb/movie/603", "", nil, bearer("tok-hello")...); w.Code != 403 {
		t.Fatalf("library without permission: %d", w.Code)
	}
	if w := e.do("GET", "/api/addon-core/v1/library/tmdb/movie/603", "", nil, bearer("tok-lib")...); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"id":`+itoa(movie)+`,`) || !strings.Contains(w.Body.String(), `"title":"Матрица"`) {
		t.Fatalf("library: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/addon-core/v1/library/tmdb/movie/1", "", nil, bearer("tok-lib")...); w.Code != 404 {
		t.Fatalf("missing title: %d", w.Code)
	}
	for _, p := range []string{"book/603", "movie/x", "movie/-1"} {
		if w := e.do("GET", "/api/addon-core/v1/library/tmdb/"+p, "", nil, bearer("tok-lib")...); w.Code != 400 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}

	// events
	ch, cancel := e.bus.Subscribe()
	defer cancel()
	if w := e.do("POST", "/api/addon-core/v1/events", `{"name":"ping","data":{"n":1}}`, nil, bearer("tok-hello")...); w.Code != 204 {
		t.Fatalf("event: %d %s", w.Code, w.Body)
	}
	select {
	case ev := <-ch:
		b, _ := json.Marshal(ev.Data)
		if ev.Type != "addon.hello.ping" || string(b) != `{"n":1}` {
			t.Fatalf("event = %s %s", ev.Type, b)
		}
	case <-time.After(time.Second):
		t.Fatal("no event published")
	}
	if w := e.do("POST", "/api/addon-core/v1/events", `{"name":"Bad Name"}`, nil, bearer("tok-hello")...); w.Code != 400 {
		t.Fatalf("bad event name: %d", w.Code)
	}
	if w := e.do("POST", "/api/addon-core/v1/events", `{"name":"ping"}`, nil, bearer("tok-lib")...); w.Code != 403 {
		t.Fatalf("event without permission: %d", w.Code)
	}
	select {
	case ev := <-ch:
		t.Fatalf("refused event published: %v", ev)
	default:
	}
	if w := e.do("GET", "/api/addon-core/v1/nope", "", nil, bearer("tok-hello")...); w.Code != 404 {
		t.Fatalf("unknown core route: %d", w.Code)
	}
}

func TestAddonStatic(t *testing.T) {
	e := newAddonEnv(t)
	w := e.do("GET", "/addons/hello/ui/index.js", "", e.admin)
	if w.Code != 200 || w.Body.String() != "export default 1\n" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("bundle: %d %v %s", w.Code, w.Header(), w.Body)
	}
	// the entry the web list gives (?v=<version>) is the same file
	if w := e.do("GET", "/addons/hello/ui/index.js?v=1.0.0", "", e.admin); w.Code != 200 || w.Body.String() != "export default 1\n" {
		t.Fatalf("bundle with ?v: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/addons/hello/ui/index.js", "", e.kid); w.Code != 403 {
		t.Fatalf("kid: %d", w.Code)
	}
	if w := e.do("GET", "/addons/hello/ui/index.js", "", nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	for _, p := range []string{"/addons/hello/ui/", "/addons/hello/ui/sub", "/addons/hello/ui/sub/", "/addons/hello/ui/../../../secret",
		"/addons/hello/ui/%2e%2e/%2e%2e/%2e%2e/secret", "/addons/hello/ui/missing.js", "/addons/off/ui/index.js", "/addons/nope/ui/index.js"} {
		if w := e.do("GET", p, "", e.admin); w.Code != 404 {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
}

// TestEventVisibilityAddons: addon events never reach kids; addon.<id>.admin.*
// reaches admins only.
func TestEventVisibilityAddons(t *testing.T) {
	admin := auth.User{ID: 1, IsAdmin: true}
	user := auth.User{ID: 2}
	kid := auth.User{ID: 3, IsKid: true}
	for _, c := range []struct {
		u    auth.User
		typ  string
		want bool
	}{
		{admin, "addon.hello.ping", true},
		{user, "addon.hello.ping", true},
		{kid, "addon.hello.ping", false},
		{admin, "addon.hello.admin.sync", true},
		{user, "addon.hello.admin.sync", false},
		{kid, "addon.hello.admin.sync", false},
		{user, "addon.hello.administer", true},
		{kid, events.LibraryUpdated, true},
	} {
		if got := eventVisible(c.u, c.typ, nil); got != c.want {
			t.Errorf("%+v %s: %v, want %v", c.u, c.typ, got, c.want)
		}
	}
}

// TestMeAddons: /api/me says whether the server runs addons.
func TestMeAddons(t *testing.T) {
	e := newAddonEnv(t)
	if w := e.do("GET", "/api/me", "", e.admin); !strings.Contains(w.Body.String(), `"addons":true`) {
		t.Fatalf("addons on: %s", w.Body)
	}
	off := newEnv(t)
	if w := off.do("GET", "/api/me", "", off.setup()); !strings.Contains(w.Body.String(), `"addons":false`) {
		t.Fatalf("addons off: %s", w.Body)
	}
}

// TestProxy_StartingAnswers503: an addon that is up but not ready yet
// gets no proxied requests.
func TestProxy_StartingAnswers503(t *testing.T) {
	e := newAddonEnv(t)
	e.fake.mu.Lock()
	e.fake.status["hello"] = addons.StatusStarting
	e.fake.mu.Unlock()
	w := e.do("GET", "/api/addons/hello/x/echo", "", e.admin)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Расширение запускается") {
		t.Fatalf("starting: %d %s", w.Code, w.Body)
	}
	// still not in the bundle list
	if w := e.do("GET", "/api/addons/web", "", e.admin); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("web while starting: %d %s", w.Code, w.Body)
	}
}

// TestCoreInfo_Fields: /info tells the addon the server's tools, public
// address and library folders.
func TestCoreInfo_Fields(t *testing.T) {
	e := newAddonEnv(t, func(d *Deps) {
		d.FFmpeg, d.FFprobe, d.PublicURL = "/usr/bin/ffmpeg", "/usr/bin/ffprobe", "https://media.example.org"
		d.Jobs = &library.Jobs{Scanner: &library.Scanner{Movies: "/m/movies", Series: "/m/series", Music: "/m/music",
			Ebooks: "/m/books", Audiobooks: "/m/audio", Comics: "/m/comics"}}
	})
	w := e.do("GET", "/api/addon-core/v1/info", "", nil, "Authorization", "Bearer tok-lib")
	if w.Code != 200 {
		t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	var got addonkit.Info
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := addonkit.Info{ID: "lib", Version: "1.0.0", Contract: addons.ContractVersion,
		FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe", PublicURL: "https://media.example.org",
		LibraryRoots: addonkit.LibraryRoots{Movies: "/m/movies", Series: "/m/series", Music: "/m/music",
			Ebooks: "/m/books", Audiobooks: "/m/audio", Comics: "/m/comics"}, FileRoots: []string{}, MissingRoots: []string{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("info = %+v\nwant %+v", got, want)
	}
}

// TestAddonCore_InfoMediaRoot: /info names the media folder and the file
// browser's roots with their symlinks resolved, the admin-only disk root
// included; a root not on disk is left out, one reached twice is listed once.
func TestAddonCore_InfoMediaRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(base, "media")
	real := filepath.Join(base, "elsewhere", "shows")
	for _, d := range []string{filepath.Join(media, "movies"), real} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(real, filepath.Join(media, "series")); err != nil {
		t.Fatal(err)
	}
	roots := []files.Root{
		{ID: "movies", Title: "Фильмы", Dir: filepath.Join(media, "movies")},
		{ID: "series", Title: "Сериалы", Dir: filepath.Join(media, "series")}, // a symlink
		{ID: "music", Title: "Музыка", Dir: filepath.Join(media, "music")},    // not on disk
		{ID: "shows", Title: "Шоу", Dir: real},                                // the series root again
		{ID: "disk", Title: "Весь диск", Dir: media, AdminOnly: true},
	}
	e := newAddonEnv(t, func(d *Deps) {
		d.MediaRoot = media
		d.Files = files.NewService(files.ServiceConfig{Sandbox: files.NewSandbox(media, roots), DB: d.DB})
	})
	w := e.do("GET", "/api/addon-core/v1/info", "", nil, "Authorization", "Bearer tok-lib")
	if w.Code != 200 {
		t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	var got addonkit.Info
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.MediaRoot != media {
		t.Fatalf("mediaRoot %q, want %q", got.MediaRoot, media)
	}
	want := []string{filepath.Join(media, "movies"), real, media}
	if !reflect.DeepEqual(got.FileRoots, want) {
		t.Fatalf("fileRoots %v\nwant %v", got.FileRoots, want)
	}
	if want := []string{filepath.Join(media, "music")}; !reflect.DeepEqual(got.MissingRoots, want) {
		t.Fatalf("missingRoots %v\nwant %v", got.MissingRoots, want)
	}
	// a root made after start is listed from then on
	if err := os.MkdirAll(filepath.Join(media, "music"), 0o755); err != nil {
		t.Fatal(err)
	}
	w = e.do("GET", "/api/addon-core/v1/info", "", nil, "Authorization", "Bearer tok-lib")
	json.Unmarshal(w.Body.Bytes(), &got)
	want = []string{filepath.Join(media, "movies"), real, filepath.Join(media, "music"), media}
	if !reflect.DeepEqual(got.FileRoots, want) {
		t.Fatalf("fileRoots after mkdir %v\nwant %v", got.FileRoots, want)
	}
	if len(got.MissingRoots) != 0 {
		t.Fatalf("missingRoots after mkdir %v", got.MissingRoots)
	}
}

// withAdultsAddon adds "fam" (access "adults", running, with a web part)
// beside "hello" (default access).
func withAdultsAddon(t *testing.T, e *addonEnv) {
	t.Helper()
	fam := e.fake.installed["hello"]
	fam.ID, fam.Manifest.ID, fam.Manifest.Access = "fam", "fam", "adults"
	e.fake.mu.Lock()
	e.fake.installed["fam"] = fam
	e.fake.status["fam"] = addons.StatusRunning
	e.fake.sock["fam"] = e.echo.sock
	e.fake.mu.Unlock()
	wd := e.fake.WebDir("fam", "1.0.0")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "index.js"), []byte("export default 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAddonAccess: who gets an addon's proxy and bundle. Admins get every
// addon; adults those with access "adults", never x/admin/*; kids nothing.
func TestAddonAccess(t *testing.T) {
	e := newAddonEnv(t)
	withAdultsAddon(t, e)
	const (
		forbidden = "Недостаточно прав"
		kidMsg    = "Недоступно в детском профиле"
		badPath   = "Не удалось разобрать запрос"
	)
	who := map[string]*http.Cookie{"admin": e.admin, "adult": e.user, "kid": e.kid}
	for _, c := range []struct {
		who, path string
		code      int
		msg       string
	}{
		{"admin", "/api/addons/fam/x/foo", 200, ""},
		{"admin", "/api/addons/fam/x/admin/foo", 200, ""},
		{"admin", "/api/addons/hello/x/foo", 200, ""},
		{"admin", "/api/addons/hello/x/admin/foo", 200, ""},
		{"admin", "/addons/fam/ui/index.js", 200, ""},
		{"admin", "/addons/hello/ui/index.js", 200, ""},
		{"adult", "/api/addons/fam/x/foo", 200, ""},
		{"adult", "/api/addons/fam/x/admin/foo", 403, forbidden},
		{"adult", "/api/addons/hello/x/foo", 403, forbidden},
		{"adult", "/api/addons/hello/x/admin/foo", 403, forbidden},
		{"adult", "/addons/fam/ui/index.js", 200, ""},
		{"adult", "/addons/hello/ui/index.js", 403, forbidden},
		{"adult", "/api/addons/nope/x/foo", 403, forbidden},
		{"adult", "/addons/nope/ui/index.js", 403, forbidden},
		{"kid", "/api/addons/fam/x/foo", 403, kidMsg},
		{"kid", "/api/addons/fam/x/admin/foo", 403, kidMsg},
		{"kid", "/api/addons/hello/x/foo", 403, kidMsg},
		{"kid", "/api/addons/hello/x/admin/foo", 403, kidMsg},
		{"kid", "/addons/fam/ui/index.js", 403, kidMsg},
		{"kid", "/addons/hello/ui/index.js", 403, kidMsg},
		{"kid", "/api/addons/web", 403, kidMsg},
		// x/admin however it is spelled
		{"adult", "/api/addons/fam/x/admin", 403, forbidden},
		{"adult", "/api/addons/fam/x/admin/", 403, forbidden},
		{"adult", "/api/addons/fam/x/admin?x=1", 403, forbidden},
		{"adult", "/api/addons/fam/x/ADMIN/foo", 403, forbidden},
		{"adult", "/api/addons/fam/x/Admin/foo", 403, forbidden},
		{"adult", "/api/addons/fam/x/%61dmin/foo", 403, forbidden},
		{"adult", "/api/addons/fam/x/admin%2Ffoo", 403, forbidden},
		{"adult", "/api/addons/fam/x/administer", 404, ""}, // not admin/: reaches the addon
		// segment forms another framework may trim or fold to admin
		{"adult", "/api/addons/fam/x/admin%20/users", 403, forbidden},
		{"adult", "/api/addons/fam/x/admin./users", 403, forbidden},
		{"adult", "/api/addons/fam/x/admin../users", 403, forbidden},
		{"adult", "/api/addons/fam/x/%20admin/users", 403, forbidden},
		{"adult", "/api/addons/fam/x/admin%C2%A0/users", 403, forbidden},
		{"adult", "/api/addons/fam/x/admin%20", 403, forbidden},
		// control bytes and invalid UTF-8: refused for everyone
		{"adult", "/api/addons/fam/x/admin%09/users", 400, badPath},
		{"adult", "/api/addons/fam/x/admin%00/users", 400, badPath},
		{"adult", "/api/addons/fam/x/admin%7F/users", 400, badPath},
		{"adult", "/api/addons/fam/x/%C0%AEadmin/users", 400, badPath},
		{"adult", "/api/addons/fam/x/admin%C0/users", 400, badPath},
		{"adult", "/api/addons/fam/x/foo%0Abar", 400, badPath},
		{"admin", "/api/addons/fam/x/admin%09/users", 400, badPath},
		// a path the addon would read otherwise than the core: refused for everyone
		{"adult", "/api/addons/fam/x//admin/foo", 400, badPath},
		{"adult", "/api/addons/fam/x/./admin/foo", 400, badPath},
		{"adult", "/api/addons/fam/x/foo/../admin/foo", 400, badPath},
		{"adult", "/api/addons/fam/x/admin/%252e%252e", 400, badPath},
		{"adult", "/api/addons/fam/x/admin/%252e%252e/foo", 400, badPath},
		{"adult", "/api/addons/fam/x/%252E", 400, badPath},
		{"adult", "/api/addons/fam/x/%2561dmin/foo", 400, badPath},
		{"adult", "/api/addons/fam/x/admin;x/foo", 400, badPath},
		{"adult", "/api/addons/fam/x/admin%5Cfoo", 400, badPath},
		{"adult", `/api/addons/fam/x/admin\foo`, 400, badPath},
		{"admin", "/api/addons/fam/x/admin/%252e%252e", 400, badPath},
		{"admin", "/api/addons/fam/x/foo/../echo", 400, badPath},
		{"admin", "/api/addons/fam/x/admin/foo/", 404, ""}, // a trailing slash is fine
	} {
		w := e.do("GET", c.path, "", who[c.who])
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.msg) {
			t.Errorf("%s %s: %d %s, want %d %q", c.who, c.path, w.Code, w.Body, c.code, c.msg)
		}
	}
	// the bundle list: an admin's has both, an adult's the adults addon only
	if w := e.do("GET", "/api/addons/web", "", e.admin); w.Code != 200 ||
		strings.TrimSpace(w.Body.String()) != `[{"id":"fam","entry":"/addons/fam/ui/index.js?v=1.0.0"},{"id":"hello","entry":"/addons/hello/ui/index.js?v=1.0.0"}]` {
		t.Errorf("web as admin: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/addons/web", "", e.user); w.Code != 200 ||
		strings.TrimSpace(w.Body.String()) != `[{"id":"fam","entry":"/addons/fam/ui/index.js?v=1.0.0"}]` {
		t.Errorf("web as adult: %d %s", w.Code, w.Body)
	}
	// the management routes stay admins-only, adults addon or not
	for _, p := range []string{"/api/addons", "/api/addons/fam/log"} {
		if w := e.do("GET", p, "", e.user); w.Code != 403 {
			t.Errorf("%s as adult: %d", p, w.Code)
		}
	}
	// none of the refused paths reached the addon
	if n := e.echo.hits.Load(); n != 0 {
		t.Errorf("refused paths reached the addon %d times", n)
	}
	// the addon learns who asks: an adult, not an admin
	w := e.do("POST", "/api/addons/fam/x/echo", `{}`, e.user)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `\"isAdmin\":false`) {
		t.Errorf("echo as adult: %d %s", w.Code, w.Body)
	}
}

// TestAddonEventBodyCap: POST /events takes bodies over the general 64 KiB
// (a download list of hundreds of rows) up to addonEventBody; over it is
// 413 and nothing is published.
func TestAddonEventBodyCap(t *testing.T) {
	e := newAddonEnv(t)
	ch, cancel := e.bus.Subscribe()
	defer cancel()
	body := func(n int) string { return `{"name":"ping","data":"` + strings.Repeat("x", n) + `"}` }
	if w := e.do("POST", "/api/addon-core/v1/events", body(1<<16+1024), nil, bearer("tok-hello")...); w.Code != 204 {
		t.Fatalf("event over 64 KiB: %d %s", w.Code, w.Body)
	}
	select {
	case ev := <-ch:
		if b, _ := json.Marshal(ev.Data); ev.Type != "addon.hello.ping" || len(b) != 1<<16+1024+2 {
			t.Fatalf("event = %s, %d bytes", ev.Type, len(b))
		}
	case <-time.After(time.Second):
		t.Fatal("no event published")
	}
	w := e.do("POST", "/api/addon-core/v1/events", body(addonEventBody), nil, bearer("tok-hello")...)
	if w.Code != 413 || !strings.Contains(w.Body.String(), "1 МБ") {
		t.Fatalf("event over the cap: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/addon-core/v1/events", `{"name":`, nil, bearer("tok-hello")...); w.Code != 400 {
		t.Fatalf("not JSON: %d %s", w.Code, w.Body)
	}
	select {
	case ev := <-ch:
		t.Fatalf("refused event published: %v", ev.Type)
	default:
	}
}

// TestAddonEventsForAdults: an adult gets the events of an addon with
// access "adults" only (never its admin.* ones); an admin gets them all.
func TestAddonEventsForAdults(t *testing.T) {
	e := newAddonEnv(t)
	withAdultsAddon(t, e)
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close) // after the streams' cleanups (LIFO)
	open := func(c *http.Cookie) *bufio.Reader {
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events", nil)
		req.AddCookie(c)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cancel(); resp.Body.Close() })
		return bufio.NewReader(resp.Body)
	}
	adminR, userR := open(e.admin), open(e.user)
	for deadline := time.Now().Add(5 * time.Second); e.bus.Subscribers() < 2; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no subscribers")
		}
	}
	for _, typ := range []string{"addon.hello.ping", "addon.fam.admin.ping", "addon.fam.ping"} {
		e.bus.Publish(events.Event{Type: typ, Data: map[string]int{"n": 1}})
	}
	for _, want := range []string{"addon.hello.ping", "addon.fam.admin.ping", "addon.fam.ping"} {
		if typ, _ := readEvent(t, adminR); typ != want {
			t.Fatalf("admin got %s, want %s", typ, want)
		}
	}
	if typ, _ := readEvent(t, userR); typ != "addon.fam.ping" {
		t.Fatalf("adult got %s first, want addon.fam.ping", typ)
	}
}

// TestAddonProxyHeaders_MaxRating: X-Antresol-User carries the profile's
// rating limit as the core's own gate applies it (auth.User.RatingLimit),
// and leaves it out for a profile without one.
func TestAddonProxyHeaders_MaxRating(t *testing.T) {
	e := newAddonEnv(t)
	withAdultsAddon(t, e)
	w := e.do("POST", "/api/users", `{"name":"Гость","maxRating":"12+"}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body)
	}
	var lim auth.User
	json.Unmarshal(w.Body.Bytes(), &lim)
	limited := session(e.do("POST", "/api/login", `{"userId":`+itoa(lim.ID)+`}`, nil))
	header := func(c *http.Cookie) map[string]any {
		t.Helper()
		w := e.do("POST", "/api/addons/fam/x/echo", `{}`, c)
		var got struct{ Headers http.Header }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatalf("proxy: %d %s", w.Code, w.Body)
		}
		var u map[string]any
		if err := json.Unmarshal([]byte(got.Headers.Get("X-Antresol-User")), &u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	lu := header(limited)
	if lu["maxRating"] != "12+" || lu["isKid"] != false || len(fmt.Sprint(lu["uid"])) != 32 {
		t.Fatalf("limited adult: %v", lu)
	}
	for name, c := range map[string]*http.Cookie{"admin": e.admin, "adult": e.user} {
		// uid: the profile's stable key, its own
		if u := header(c); u["maxRating"] != nil || len(fmt.Sprint(u["uid"])) != 32 || u["uid"] == lu["uid"] {
			t.Fatalf("%s: %v", name, u)
		}
	}
}
