package api

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/events"
)

const internetPeer = "203.0.113.7:40000"

// newNetEnv is newEnv with a Network: npm is the trusted proxy.
func newNetEnv(t *testing.T, n *auth.Network) *env {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st, ss := auth.NewStore(d), auth.NewSessions(d)
	return &env{db: d, t: t, store: st, h: NewRouter(Deps{Users: st, Sessions: ss, Network: n})}
}

func (e *env) addUser(body string, admin *http.Cookie) auth.User {
	e.t.Helper()
	w := e.do("POST", "/api/users", body, admin)
	if w.Code != 201 {
		e.t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var u auth.User
	json.Unmarshal(w.Body.Bytes(), &u)
	return u
}

func remoteOf(t *testing.T, w *httptest.ResponseRecorder) bool {
	t.Helper()
	var m struct{ Remote *bool }
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || m.Remote == nil {
		t.Fatalf("no remote flag in %s", w.Body)
	}
	return *m.Remote
}

func TestRemotePasswordlessLoginRefused(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	masha := e.addUser(`{"name":"Маша"}`, admin)
	e.addUser(`{"name":"Бабушка","password":"bab123"}`, admin)

	e.peer = internetPeer
	w := e.do("POST", "/api/login", `{"userId":`+itoa(masha.ID)+`}`, nil)
	if w.Code != 403 || session(w) != nil || !strings.Contains(w.Body.String(), "Для входа не из дома у профиля должен быть пароль") {
		t.Fatalf("remote passwordless: %d %s", w.Code, w.Body)
	}
	// the picker still lists it
	if pr := e.do("GET", "/api/profiles", "", nil); !strings.Contains(pr.Body.String(), "Маша") {
		t.Fatalf("profiles: %s", pr.Body)
	}
	// a password works from anywhere, and the client learns it is remote
	w = e.do("POST", "/api/login", `{"userId":3,"password":"bab123"}`, nil)
	if w.Code != 200 || !remoteOf(t, w) {
		t.Fatalf("remote password login: %d %s", w.Code, w.Body)
	}
	if me := e.do("GET", "/api/me", "", session(w)); me.Code != 200 || !remoteOf(t, me) {
		t.Fatalf("me: %d %s", me.Code, me.Body)
	}

	e.peer = "[fd00::5]:1234" // IPv6 ULA is home
	w = e.do("POST", "/api/login", `{"userId":`+itoa(masha.ID)+`}`, nil)
	if w.Code != 200 || remoteOf(t, w) {
		t.Fatalf("home passwordless: %d %s", w.Code, w.Body)
	}
}

func TestForwardedForOnlyFromTrustedProxy(t *testing.T) {
	npm := netip.MustParsePrefix("192.168.1.2/32")
	e := newNetEnv(t, &auth.Network{Proxies: []netip.Prefix{npm}, Trusted: []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10")}})
	admin := e.setup()
	masha := e.addUser(`{"name":"Маша"}`, admin)
	login := `{"userId":` + itoa(masha.ID) + `}`

	// through NPM from the internet: remote
	e.peer = "192.168.1.2:3333"
	if w := e.do("POST", "/api/login", login, nil, "X-Forwarded-For", "203.0.113.9", "X-Real-IP", "203.0.113.9"); w.Code != 403 {
		t.Fatalf("via npm from internet: %d", w.Code)
	}
	// a spoofed left-most hop does not help: the right-most non-proxy hop counts
	if w := e.do("POST", "/api/login", login, nil, "X-Forwarded-For", "10.0.0.5, 203.0.113.9"); w.Code != 403 {
		t.Fatalf("spoofed xff: %d", w.Code)
	}
	// through NPM from home, and from an extra trusted net (Tailscale)
	if w := e.do("POST", "/api/login", login, nil, "X-Forwarded-For", "192.168.1.50"); w.Code != 200 {
		t.Fatalf("via npm from home: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/login", login, nil, "X-Real-IP", "100.100.1.1"); w.Code != 200 {
		t.Fatalf("via npm from trusted net: %d %s", w.Code, w.Body)
	}
	// a LAN peer that is not the proxy but forwards headers: unknown proxy, remote
	e.peer = "192.168.1.77:3333"
	if w := e.do("POST", "/api/login", login, nil, "X-Forwarded-For", "192.168.1.50"); w.Code != 403 {
		t.Fatalf("unknown proxy: %d", w.Code)
	}
	// an internet peer cannot claim a home address
	e.peer = internetPeer
	if w := e.do("POST", "/api/login", login, nil, "X-Forwarded-For", "127.0.0.1"); w.Code != 403 {
		t.Fatalf("spoof from internet: %d", w.Code)
	}
}

func TestDefaultNetworkTreatsUnconfiguredProxyAsRemote(t *testing.T) {
	// No VAULT_TRUSTED_PROXIES: NPM on the LAN forwards an internet client.
	e := newEnv(t)
	admin := e.setup()
	masha := e.addUser(`{"name":"Маша"}`, admin)
	e.peer = "192.168.1.2:3333"
	if w := e.do("POST", "/api/login", `{"userId":`+itoa(masha.ID)+`}`, nil, "X-Forwarded-For", "203.0.113.9"); w.Code != 403 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestSetupOnlyFromHome(t *testing.T) {
	e := newEnv(t)
	e.peer = internetPeer
	g := e.do("GET", "/api/setup", "", nil)
	if !remoteOf(t, g) || !strings.Contains(g.Body.String(), `"needsSetup":true`) {
		t.Fatalf("get setup: %s", g.Body)
	}
	w := e.do("POST", "/api/setup", `{"name":"Хакер","password":"secret"}`, nil)
	if w.Code != 403 || session(w) != nil || !strings.Contains(w.Body.String(), "только из домашней сети") {
		t.Fatalf("remote setup: %d %s", w.Code, w.Body)
	}
	if need, _ := e.store.NeedsSetup(context.Background()); !need {
		t.Fatal("remote setup created a user")
	}
	e.peer = "127.0.0.1:9999"
	if w := e.do("POST", "/api/setup", `{"name":"Папа","password":"secret"}`, nil); w.Code != 200 || remoteOf(t, w) {
		t.Fatalf("home setup: %d %s", w.Code, w.Body)
	}
}

func TestSetupAdminPasswordMin6(t *testing.T) {
	e := newEnv(t)
	if w := e.do("POST", "/api/setup", `{"name":"Папа","password":"12345"}`, nil); w.Code != 400 || !strings.Contains(w.Body.String(), "не короче 6") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestLoginThrottle(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.peer = internetPeer
	bad := `{"userId":1,"password":"wrong1"}`
	for i := 0; i < 5; i++ {
		if w := e.do("POST", "/api/login", bad, nil); w.Code != 401 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	w := e.do("POST", "/api/login", bad, nil)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "Слишком много неудачных попыток") {
		t.Fatalf("6th attempt: %d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	// locked even with the right password, and from another internet address
	if w := e.do("POST", "/api/login", `{"userId":1,"password":"secret"}`, nil); w.Code != 429 {
		t.Fatalf("right password while locked: %d", w.Code)
	}
	e.peer = "198.51.100.3:1"
	if w := e.do("POST", "/api/login", `{"userId":1,"password":"secret"}`, nil); w.Code != 429 {
		t.Fatalf("profile lock from another address: %d", w.Code)
	}
	// the stranger cannot lock the family out at home
	e.peer = "192.168.1.20:1"
	if w := e.do("POST", "/api/login", `{"userId":1,"password":"secret"}`, nil); w.Code != 200 {
		t.Fatalf("home login while remote locked: %d %s", w.Code, w.Body)
	}
}

func TestLoginThrottlePerAddress(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	for _, n := range []string{"А", "Б", "В", "Г", "Д", "Е"} {
		e.addUser(`{"name":"`+n+`","password":"pass`+n+`"}`, admin)
	}
	e.peer = internetPeer
	// spraying one guess over many profiles locks the address
	for id := 2; id <= 6; id++ {
		if w := e.do("POST", "/api/login", `{"userId":`+itoa(int64(id))+`,"password":"guess1"}`, nil); w.Code != 401 {
			t.Fatalf("profile %d: %d", id, w.Code)
		}
	}
	if w := e.do("POST", "/api/login", `{"userId":7,"password":"guess1"}`, nil); w.Code != 429 {
		t.Fatalf("address not locked: %d", w.Code)
	}
}

func TestLoginLongPasswordRejectedCheaply(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	w := e.do("POST", "/api/login", `{"userId":1,"password":"secret`+strings.Repeat("x", 1000)+`"}`, nil)
	if w.Code != 401 {
		t.Fatalf("%d", w.Code)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestRemovePasswordAndRevokeOnSet(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	u := e.addUser(`{"name":"Дед","password":"ded1"}`, admin)
	path := "/api/users/" + itoa(u.ID)

	// «Убрать пароль»
	w := e.do("PATCH", path, `{"password":""}`, admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"hasPassword":false`) {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	c := session(e.do("POST", "/api/login", `{"userId":`+itoa(u.ID)+`}`, nil))
	if c == nil {
		t.Fatal("passwordless login at home")
	}
	// setting a password ends sessions opened without it, not the admin's own
	if w := e.do("PATCH", path, `{"password":"ded12"}`, admin); w.Code != 200 {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}
	if e.do("GET", "/api/me", "", c).Code != 401 {
		t.Fatal("old session survived a new password")
	}
	if e.do("GET", "/api/me", "", admin).Code != 200 {
		t.Fatal("admin session revoked")
	}
	// an admin's own password change keeps the current session
	if w := e.do("PATCH", "/api/users/1", `{"password":"newsecret"}`, admin); w.Code != 200 {
		t.Fatalf("own: %d %s", w.Code, w.Body)
	}
	if e.do("GET", "/api/me", "", admin).Code != 200 {
		t.Fatal("own session revoked")
	}
}

func TestEventsKidGetsNoLibraryDetails(t *testing.T) {
	bus := &events.Bus{}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Events = bus })
	kid := e.kid("6+")
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events", nil)
	req.AddCookie(kid)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for bus.Subscribers() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	bus.Publish(events.Event{Type: events.LibraryUpdated, Data: map[string]any{"title": "Пила", "tmdbId": 176, "kind": "movie"}})
	typ, data := readEvent(t, bufio.NewReader(resp.Body))
	if typ != events.LibraryUpdated || data != "{}" {
		t.Fatalf("kid event %s %s", typ, data)
	}
}

// readEvent reads one SSE message (event + data) from r.
func readEvent(t *testing.T, r *bufio.Reader) (typ, data string) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			typ = line[7:]
		case strings.HasPrefix(line, "data: "):
			data = line[6:]
		case line == "" && typ != "":
			return typ, data
		}
	}
}
