package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
)

func newAPIKeyEnv(t *testing.T) *libEnv {
	return newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db })
}

// newKey creates a key through the API as who and returns it.
func (e *libEnv) newKey(who *http.Cookie, scope string) string {
	e.t.Helper()
	w := e.do("POST", "/api/me/api-keys", `{"name":"Claude","scope":"`+scope+`"}`, who)
	if w.Code != 200 {
		e.t.Fatalf("create %s: %d %s", scope, w.Code, w.Body)
	}
	var out struct {
		Key  string
		Info auth.APIKey
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !strings.HasPrefix(out.Key, auth.APIKeyPrefix) {
		e.t.Fatalf("create body %s", w.Body)
	}
	return out.Key
}

func TestAPIKeyLifecycle(t *testing.T) {
	e := newAPIKeyEnv(t)
	key := e.newKey(e.admin, auth.ScopeRead)
	if w := e.do("GET", "/api/me", "", nil, bearer(key)...); w.Code != 200 || !strings.Contains(w.Body.String(), `"isAdmin":false`) {
		t.Fatalf("me with read key: %d %s", w.Code, w.Body)
	}
	// stored hashed: the key itself is nowhere in the table
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE hash=? OR hint=? OR name=?`, key, key, key).Scan(&n)
	if n != 0 {
		t.Fatal("the key is stored in the clear")
	}
	w := e.do("GET", "/api/me/api-keys", "", e.admin)
	var list struct {
		Keys     []auth.APIKey
		CanAdmin bool
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Keys) != 1 || !list.CanAdmin || list.Keys[0].Hint != key[len(key)-4:] || list.Keys[0].LastUsedAt == nil {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), key) {
		t.Fatal("the list shows the key")
	}
	if w := e.do("DELETE", "/api/me/api-keys/"+itoa(list.Keys[0].ID), "", e.admin); w.Code != 204 {
		t.Fatalf("revoke %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(key)...); w.Code != 401 {
		t.Fatalf("revoked key %d", w.Code)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(auth.APIKeyPrefix+"00")...); w.Code != 401 {
		t.Fatalf("unknown key %d", w.Code)
	}
}

func TestAPIKeyScopes(t *testing.T) {
	e := newAPIKeyEnv(t)
	id, _ := e.movie("Матрица", "", "x")
	read, act, admin := e.newKey(e.admin, auth.ScopeRead), e.newKey(e.admin, auth.ScopeAct), e.newKey(e.admin, auth.ScopeAdmin)
	watched := "/api/items/" + itoa(id) + "/watched"
	if w := e.do("POST", watched, `{"watched":true}`, nil, bearer(read)...); w.Code != 403 {
		t.Fatalf("read key wrote: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", watched, `{"watched":true}`, nil, bearer(act)...); w.Code >= 300 {
		t.Fatalf("act key: %d %s", w.Code, w.Body)
	}
	// admin routes: only the admin scope of an admin
	for _, k := range []string{read, act} {
		if w := e.do("GET", "/api/users", "", nil, bearer(k)...); w.Code != 403 {
			t.Fatalf("non-admin key on /api/users: %d", w.Code)
		}
	}
	if w := e.do("GET", "/api/users", "", nil, bearer(admin)...); w.Code != 200 {
		t.Fatalf("admin key: %d %s", w.Code, w.Body)
	}
	// no key manages credentials, whatever its scope
	for _, c := range [][2]string{{"POST", "/api/me/api-keys"}, {"GET", "/api/me/api-keys"}, {"POST", "/api/me/app-password"}, {"POST", "/api/auth/pair/approve"}, {"PATCH", "/api/users/1"}} {
		if w := e.do(c[0], c[1], `{"name":"x","scope":"admin"}`, nil, bearer(admin)...); w.Code != 403 {
			t.Fatalf("%s %s with a key: %d %s", c[0], c[1], w.Code, w.Body)
		}
	}
	// the writes and the refusals are in the log
	w := e.do("GET", "/api/me/api-keys/log", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), watched) || !strings.Contains(w.Body.String(), `"status":403`) {
		t.Fatalf("log %d %s", w.Code, w.Body)
	}
}

// An administration key reads as its admin does but writes only what the
// MCP tools need: it cannot install, update or remove an addon, change the
// settings or touch files (a leaked key would run code or redirect mail).
func TestAPIKeyAdminWritesAreAllowListed(t *testing.T) {
	e := newAddonEnv(t)
	k := &libEnv{env: e.env, admin: e.admin}
	admin, act := k.newKey(e.admin, auth.ScopeAdmin), k.newKey(e.admin, auth.ScopeAct)
	for _, key := range []string{admin, act} {
		for _, c := range [][2]string{{"POST", "/api/addons"}, {"POST", "/api/addons/preview"}, {"POST", "/api/addons/hello/update"},
			{"DELETE", "/api/addons/hello?data=1"}, {"PUT", "/api/settings"}} {
			w := e.do(c[0], c[1], `{"url":"github.com/o/r"}`, nil, bearer(key)...)
			if w.Code != 403 || !strings.Contains(w.Body.String(), "Ключом API") {
				t.Errorf("%s %s with a key: %d %s", c[0], c[1], w.Code, w.Body)
			}
		}
	}
	if len(e.fake.calls) != 0 {
		t.Fatalf("a key reached the runtime: %v", e.fake.calls)
	}
	for _, p := range []string{"/api/addons", "/api/users", "/api/settings"} {
		if w := e.do("GET", p, "", nil, bearer(admin)...); w.Code != 200 {
			t.Errorf("admin key GET %s: %d %s", p, w.Code, w.Body)
		}
	}
	for _, p := range []string{"/api/addons/hello/disable", "/api/addons/hello/enable"} {
		if w := e.do("POST", p, "", nil, bearer(admin)...); w.Code >= 300 {
			t.Errorf("admin key POST %s: %d %s", p, w.Code, w.Body)
		}
	}
	// no admin rights without the admin scope
	if w := e.do("POST", "/api/addons/hello/disable", "", nil, bearer(act)...); w.Code != 403 {
		t.Errorf("act key restarted an addon: %d", w.Code)
	}
}

func TestAPIKeyWhoMayCreate(t *testing.T) {
	e := newAPIKeyEnv(t)
	kid := e.kid("")
	if w := e.do("POST", "/api/me/api-keys", `{"name":"x","scope":"read"}`, kid); w.Code != 403 {
		t.Fatalf("kid created a key: %d %s", w.Code, w.Body)
	}
	adult := e.login(`{"name":"Мама"}`)
	if w := e.do("POST", "/api/me/api-keys", `{"name":"x","scope":"admin"}`, adult); w.Code != 403 {
		t.Fatalf("non-admin admin key: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/me/api-keys", `{"name":"","scope":"read"}`, adult); w.Code != 400 {
		t.Fatalf("no name: %d", w.Code)
	}
	if w := e.do("POST", "/api/me/api-keys", `{"name":"x","scope":"all"}`, adult); w.Code != 400 {
		t.Fatalf("bad scope: %d", w.Code)
	}
	e.newKey(adult, auth.ScopeAct)
}

func TestAPIKeyRateLimit(t *testing.T) {
	e := newAPIKeyEnv(t)
	key := e.newKey(e.admin, auth.ScopeRead)
	var last int
	for range 100 {
		w := e.do("GET", "/api/me", "", nil, bearer(key)...)
		if last = w.Code; last == 429 {
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("no Retry-After")
			}
			return
		}
	}
	t.Fatalf("100 requests at once were all let in (last %d)", last)
}

func TestAPIKeyPasswordlessRemote(t *testing.T) {
	e := newAPIKeyEnv(t)
	masha := e.login(`{"name":"Маша"}`)
	key := e.newKey(masha, auth.ScopeRead)
	if w := e.do("GET", "/api/me", "", nil, bearer(key)...); w.Code != 200 {
		t.Fatalf("at home %d", w.Code)
	}
	if w := e.do("GET", "/api/me", "", nil, append(bearer(key), "X-Forwarded-For", "8.8.8.8")...); w.Code != 403 {
		t.Fatalf("remote %d", w.Code)
	}
}
