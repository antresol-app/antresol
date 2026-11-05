package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/subsonic"
)

type env struct {
	db    *sql.DB
	t     *testing.T
	h     http.Handler
	store *auth.Store
	// peer is the TCP peer of requests: a home address unless a test says
	// otherwise (httptest's default 192.0.2.1 is outside the home network).
	peer string
}

func newEnv(t *testing.T) *env {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st, ss := auth.NewStore(d), auth.NewSessions(d)
	return &env{db: d, t: t, store: st, h: NewRouter(Deps{Users: st, Sessions: ss})}
}

func (e *env) do(method, path, body string, c *http.Cookie, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "192.168.1.20:50000"
	if e.peer != "" {
		r.RemoteAddr = e.peer
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json") // as the web client sends it
	}
	if c != nil {
		r.AddCookie(c)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func session(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName && c.Value != "" {
			return c
		}
	}
	return nil
}

func (e *env) setup() *http.Cookie {
	w := e.do("POST", "/api/setup", `{"name":"Папа","password":"secret"}`, nil)
	if w.Code != 200 && w.Code != 201 {
		e.t.Fatalf("setup %d %s", w.Code, w.Body)
	}
	return session(w)
}

func TestSetupOnlyOnce(t *testing.T) {
	e := newEnv(t)
	var s struct{ NeedsSetup bool }
	json.Unmarshal(e.do("GET", "/api/setup", "", nil).Body.Bytes(), &s)
	if !s.NeedsSetup {
		t.Fatal("needsSetup should be true")
	}
	e.setup()
	if w := e.do("POST", "/api/setup", `{"name":"Хакер","password":"secret"}`, nil); w.Code != 403 {
		t.Fatalf("second setup: %d", w.Code)
	}
}

func TestLoginWrongPassword401(t *testing.T) {
	e := newEnv(t)
	e.setup()
	if w := e.do("POST", "/api/login", `{"userId":1,"password":"bad"}`, nil); w.Code != 401 || session(w) != nil {
		t.Fatalf("%d", w.Code)
	}
	if w := e.do("POST", "/api/login", `{"userId":1}`, nil); w.Code != 401 {
		t.Fatalf("admin without password: %d", w.Code)
	}
	if w := e.do("POST", "/api/login", `{"userId":1,"password":"secret"}`, nil); w.Code != 200 || session(w) == nil {
		t.Fatalf("%d", w.Code)
	}
}

func TestPasswordlessProfileLogin(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	w := e.do("POST", "/api/users", `{"name":"Маша"}`, admin)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var u auth.User
	json.Unmarshal(w.Body.Bytes(), &u)
	w = e.do("POST", "/api/login", `{"userId":`+itoa(u.ID)+`}`, nil)
	c := session(w)
	if w.Code != 200 || c == nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	me := e.do("GET", "/api/me", "", c)
	if me.Code != 200 || !strings.Contains(me.Body.String(), "Маша") {
		t.Fatalf("%d %s", me.Code, me.Body)
	}
	// profiles list is public and hides internals
	pr := e.do("GET", "/api/profiles", "", nil)
	if pr.Code != 200 || !strings.Contains(pr.Body.String(), `"hasPassword":true`) || strings.Contains(pr.Body.String(), "isAdmin") {
		t.Fatalf("%d %s", pr.Code, pr.Body)
	}
	e.do("POST", "/api/logout", "", c)
	if e.do("GET", "/api/me", "", c).Code != 401 {
		t.Fatal("session should be revoked")
	}
}

func itoa(i int64) string { b, _ := json.Marshal(i); return string(b) }

func TestKidCannotAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	if w := e.do("POST", "/api/users", `{"name":"Вася","isKid":true,"maxRating":"PG"}`, admin); w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	kid := session(e.do("POST", "/api/login", `{"userId":2}`, nil))
	if kid == nil {
		t.Fatal("kid login")
	}
	for _, c := range [][2]string{{"POST", "/api/users"}, {"PATCH", "/api/users/1"}, {"DELETE", "/api/users/1"}} {
		if w := e.do(c[0], c[1], `{"name":"x"}`, kid); w.Code != 403 {
			t.Fatalf("%v: %d", c, w.Code)
		}
	}
	if w := e.do("POST", "/api/users", `{"name":"x"}`, nil); w.Code != 401 {
		t.Fatalf("anon: %d", w.Code)
	}
}

func TestAdminMustHavePassword(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	if w := e.do("POST", "/api/users", `{"name":"Мама","isAdmin":true}`, admin); w.Code != 400 {
		t.Fatalf("%d", w.Code)
	}
	if w := e.do("POST", "/api/users", `{"name":"Мама","isAdmin":true,"password":"abcd"}`, admin); w.Code != 400 || !strings.Contains(w.Body.String(), "не короче 6") {
		t.Fatalf("short admin password: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/users", `{"name":"Мама","isAdmin":true,"password":"abcdef"}`, admin); w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/users", `{"name":"Мама"}`, admin); w.Code != 409 {
		t.Fatalf("dup: %d", w.Code)
	}
}

func TestSessionCookieFlags(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 400*24*3600 || c.Secure || c.Path != "/" {
		t.Fatalf("%+v", c)
	}
	w := e.do("POST", "/api/login", `{"userId":1,"password":"secret"}`, nil, "X-Forwarded-Proto", "https")
	if c := session(w); c == nil || !c.Secure {
		t.Fatalf("expected Secure behind https proxy: %+v", c)
	}
	_ = context.Background
}

func TestLogoutRevokeFailure500(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	e.db.Close()
	if w := e.do("POST", "/api/logout", "", c); w.Code != 500 {
		t.Fatalf("%d", w.Code)
	}
}

func TestPasswordTooLong400(t *testing.T) {
	e := newEnv(t)
	body := `{"name":"A","password":"` + strings.Repeat("x", 73) + `"}`
	if w := e.do("POST", "/api/setup", body, nil); w.Code != 400 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestAuthErrorsAreRussianJSON(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/api/me", "", nil)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "Нужно войти") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestAppPasswordEndpoints(t *testing.T) {
	e := newEnv(t)
	c := e.setup()
	sub := &subsonic.Server{Users: e.store}
	ping := func(pass string) int {
		r := httptest.NewRequest("GET", "/rest/ping?f=json&u=%D0%9F%D0%B0%D0%BF%D0%B0&p="+url.QueryEscape(pass), nil)
		r.RemoteAddr = "192.168.1.20:50000"
		w := httptest.NewRecorder()
		sub.ServeHTTP(w, r)
		var out struct {
			R struct {
				Status string `json:"status"`
				Error  *struct {
					Code int `json:"code"`
				} `json:"error"`
			} `json:"subsonic-response"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%v %s", err, w.Body)
		}
		if out.R.Error != nil {
			return out.R.Error.Code
		}
		return 0
	}
	// GET never carries the password: only whether one exists and since when.
	state := func() (bool, *string) {
		t.Helper()
		w := e.do("GET", "/api/me/app-password", "", c)
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || w.Code != 200 {
			t.Fatalf("GET %d %s", w.Code, w.Body)
		}
		if _, ok := m["password"]; ok {
			t.Fatalf("GET leaks the password: %s", w.Body)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET cache-control %q", w.Header().Get("Cache-Control"))
		}
		exists, _ := m["exists"].(bool)
		var created *string
		if s, ok := m["createdAt"].(string); ok {
			created = &s
		}
		return exists, created
	}
	if ex, at := state(); ex || at != nil {
		t.Fatalf("none yet: %v %v", ex, at)
	}
	if w := e.do("GET", "/api/me/app-password", "", nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	w := e.do("POST", "/api/me/app-password", "", c)
	var got struct {
		Password *string `json:"password"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Password == nil ||
		!regexp.MustCompile(`^[a-z0-9]{4}(-[a-z0-9]{4}){3}$`).MatchString(*got.Password) {
		t.Fatalf("POST %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("POST cache-control %q", w.Header().Get("Cache-Control"))
	}
	old := *got.Password
	if ex, at := state(); !ex || at == nil {
		t.Fatalf("after POST: %v %v", ex, at)
	} else if _, err := time.Parse(time.RFC3339, *at); err != nil {
		t.Fatalf("createdAt %q: %v", *at, err)
	}
	if code := ping(old); code != 0 {
		t.Fatalf("app password: code %d", code)
	}
	w = e.do("POST", "/api/me/app-password", "", c) // a new one replaces it
	json.Unmarshal(w.Body.Bytes(), &got)
	if *got.Password == old {
		t.Fatal("POST must make a new password")
	}
	if code := ping(old); code != 40 {
		t.Fatalf("old password after renewal: code %d", code)
	}
	if code := ping(*got.Password); code != 0 {
		t.Fatalf("new password: code %d", code)
	}
	if w := e.do("DELETE", "/api/me/app-password", "", c); w.Code != 204 {
		t.Fatalf("DELETE %d", w.Code)
	}
	if ex, at := state(); ex || at != nil {
		t.Fatalf("after DELETE: %v %v", ex, at)
	}
	if code := ping(*got.Password); code != 40 {
		t.Fatalf("deleted password: code %d", code)
	}
}

// subsonicPing returns the Subsonic error code of a ping as name/pass from
// home (0: ok).
func subsonicPing(t *testing.T, st *auth.Store, name, pass string) int {
	t.Helper()
	sub := &subsonic.Server{Users: st}
	r := httptest.NewRequest("GET", "/rest/ping?f=json&u="+url.QueryEscape(name)+"&p="+url.QueryEscape(pass), nil)
	r.RemoteAddr = "192.168.1.20:50000"
	w := httptest.NewRecorder()
	sub.ServeHTTP(w, r)
	var out struct {
		R struct {
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		} `json:"subsonic-response"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v %s", err, w.Body)
	}
	if out.R.Error != nil {
		return out.R.Error.Code
	}
	return 0
}

// F3: a new, changed or removed login password ends the app password too,
// so a session someone else held cannot keep /rest and /opds access.
func TestPasswordChangeClearsAppPassword(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	ctx := context.Background()
	w := e.do("POST", "/api/users", `{"name":"Маша","password":"masha1"}`, admin)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var masha auth.User
	json.Unmarshal(w.Body.Bytes(), &masha)
	for _, patch := range []string{`{"password":"masha2"}`, `{"password":""}`, `{"name":"Маша"}`} {
		app, err := e.store.SetAppPassword(ctx, masha.ID)
		if err != nil {
			t.Fatal(err)
		}
		if code := subsonicPing(t, e.store, "Маша", app); code != 0 {
			t.Fatalf("before %s: code %d", patch, code)
		}
		if w := e.do("PATCH", "/api/users/"+itoa(masha.ID), patch, admin); w.Code != 200 {
			t.Fatalf("PATCH %s: %d %s", patch, w.Code, w.Body)
		}
		want := 40
		if !strings.Contains(patch, "password") {
			want = 0 // other edits keep it
		}
		if code := subsonicPing(t, e.store, "Маша", app); code != want {
			t.Fatalf("after %s: code %d, want %d", patch, code, want)
		}
	}
	// the admin's own new password ends the admin's app password as well
	app, _ := e.store.SetAppPassword(ctx, 1)
	if w := e.do("PATCH", "/api/users/1", `{"password":"secret2"}`, admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if code := subsonicPing(t, e.store, "Папа", app); code != 40 {
		t.Fatalf("admin after own password change: code %d", code)
	}
}

// F3: the admin resets another profile's app password without its session.
func TestAdminResetsAppPassword(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	ctx := context.Background()
	w := e.do("POST", "/api/users", `{"name":"Вася","isKid":true,"maxRating":"PG"}`, admin)
	var kid auth.User
	json.Unmarshal(w.Body.Bytes(), &kid)
	app, err := e.store.SetAppPassword(ctx, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	kidC := session(e.do("POST", "/api/login", `{"userId":`+itoa(kid.ID)+`}`, nil))
	if w := e.do("DELETE", "/api/users/1/app-password", "", kidC); w.Code != 403 {
		t.Fatalf("kid: %d", w.Code)
	}
	if w := e.do("DELETE", "/api/users/"+itoa(kid.ID)+"/app-password", "", admin); w.Code != 204 {
		t.Fatalf("admin reset: %d %s", w.Code, w.Body)
	}
	if code := subsonicPing(t, e.store, "Вася", app); code != 40 {
		t.Fatalf("after reset: code %d", code)
	}
	if w := e.do("DELETE", "/api/users/999/app-password", "", admin); w.Code != 404 {
		t.Fatalf("unknown profile: %d", w.Code)
	}
}
