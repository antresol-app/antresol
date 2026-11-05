package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
)

func tokenBody(userID int64, password, deviceID string) string {
	b, _ := json.Marshal(map[string]any{"userId": userID, "password": password,
		"device": map[string]string{"id": deviceID, "name": "iPhone Маши", "model": "iPhone17,1", "version": "1.0"}})
	return string(b)
}

// deviceLogin signs a device in through /api/auth/token and returns its token.
func (e *env) deviceLogin(userID int64, password, deviceID string) string {
	e.t.Helper()
	w := e.do("POST", "/api/auth/token", tokenBody(userID, password, deviceID), nil)
	if w.Code != 200 {
		e.t.Fatalf("token %d %s", w.Code, w.Body)
	}
	var out struct{ Token string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Token == "" {
		e.t.Fatalf("token body %s", w.Body)
	}
	return out.Token
}

func bearer(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

func TestRequireAcceptsBearer(t *testing.T) {
	e := newEnv(t)
	e.setup()
	tok := e.deviceLogin(1, "secret", "phone-0001")
	if w := e.do("GET", "/api/me", "", nil, bearer(tok)...); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":1`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer("nope")...); w.Code != 401 {
		t.Fatalf("unknown bearer %d", w.Code)
	}
}

func TestRequireCookieCarriesNativeToken(t *testing.T) {
	e := newEnv(t)
	e.setup()
	tok := e.deviceLogin(1, "secret", "phone-0001")
	if w := e.do("GET", "/api/me", "", &http.Cookie{Name: auth.CookieName, Value: tok}); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestNativePasswordlessRemoteRefused(t *testing.T) {
	e := newLibEnv(t)
	masha := e.addUser(`{"name":"Маша"}`, e.admin)
	tok := e.deviceLogin(masha.ID, "", "ipad-00001")
	web := session(e.do("POST", "/api/login", `{"userId":`+itoa(masha.ID)+`}`, nil))
	if web == nil {
		t.Fatal("web login")
	}
	for _, p := range []string{"/api/home", "/api/me"} {
		e.peer = "127.0.0.1:5000"
		if w := e.do("GET", p, "", nil, bearer(tok)...); w.Code != 200 {
			t.Fatalf("%s at home: %d %s", p, w.Code, w.Body)
		}
		hdr := append(bearer(tok), "X-Forwarded-For", "8.8.8.8")
		w := e.do("GET", p, "", nil, hdr...)
		if w.Code != 403 || w.Header().Get("X-Vault-Error") != "remote-passwordless" ||
			!strings.Contains(w.Body.String(), auth.MsgRemotePasswordless) {
			t.Fatalf("%s remote: %d %v %s", p, w.Code, w.Header(), w.Body)
		}
		// web sessions keep their behaviour
		if w := e.do("GET", p, "", web, "X-Forwarded-For", "8.8.8.8"); w.Code != 200 {
			t.Fatalf("%s web remote: %d %s", p, w.Code, w.Body)
		}
	}
}

func TestMeUsesConfiguredNetwork(t *testing.T) {
	npm := netip.MustParsePrefix("192.168.1.2/32")
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Network = &auth.Network{Proxies: []netip.Prefix{npm}} })
	masha := e.addUser(`{"name":"Маша"}`, e.admin)
	tok := e.deviceLogin(masha.ID, "", "ipad-00001")
	e.peer = "192.168.1.2:3333"
	hdr := append(bearer(tok), "X-Forwarded-For", "192.168.1.5")
	for _, p := range []string{"/api/me", "/api/home"} {
		if w := e.do("GET", p, "", nil, hdr...); w.Code != 200 {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body)
		}
	}
}

func TestTokenLoginSharesThrottle(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.peer = internetPeer
	for i := 0; i < 5; i++ {
		if w := e.do("POST", "/api/auth/token", tokenBody(1, "wrong1", "phone-0001"), nil); w.Code != 401 {
			t.Fatalf("attempt %d: %d %s", i, w.Code, w.Body)
		}
	}
	w := e.do("POST", "/api/login", `{"userId":1,"password":"secret"}`, nil)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestTokenLoginRemotePasswordlessRefused(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	masha := e.addUser(`{"name":"Маша"}`, admin)
	e.peer = internetPeer
	w := e.do("POST", "/api/auth/token", tokenBody(masha.ID, "", "phone-0001"), nil)
	if w.Code != 403 || !strings.Contains(w.Body.String(), auth.MsgRemotePasswordless) || strings.Contains(w.Body.String(), "token") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestTokenLoginNoCookie(t *testing.T) {
	e := newEnv(t)
	e.setup()
	w := e.do("POST", "/api/auth/token", tokenBody(1, "secret", "phone-0001"), nil)
	if w.Code != 200 || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	var out struct {
		Token string
		User  struct {
			ID     int64
			Remote *bool
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Token == "" || out.User.ID != 1 || out.User.Remote == nil {
		t.Fatalf("%s", w.Body)
	}
}

func TestTokenRequiresDevice(t *testing.T) {
	e := newEnv(t)
	e.setup()
	for _, id := range []string{"", "short", "has space!", strings.Repeat("a", 65), "абвгдежзи"} {
		w := e.do("POST", "/api/auth/token", tokenBody(1, "secret", id), nil)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "Не указано устройство") {
			t.Fatalf("%q: %d %s", id, w.Code, w.Body)
		}
	}
	if w := e.do("POST", "/api/auth/token", `{"userId":1,"password":"secret"}`, nil); w.Code != 400 {
		t.Fatalf("no device: %d", w.Code)
	}
}

func TestTokenTrimsDeviceFields(t *testing.T) {
	e := newEnv(t)
	e.setup()
	b, _ := json.Marshal(map[string]any{"userId": 1, "password": "secret",
		"device": map[string]string{"id": "phone-0001", "name": "  " + strings.Repeat("я", 150) + "  ", "model": strings.Repeat("m", 100), "version": strings.Repeat("v", 100)}})
	w := e.do("POST", "/api/auth/token", string(b), nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &out)
	_, s, err := auth.NewSessions(e.db).LookupSession(context.Background(), out.Token)
	if err != nil || len([]rune(s.Device.Name)) != 100 || s.Device.Name[0] == ' ' || len(s.Device.Model) != 64 || len(s.Device.Version) != 64 {
		t.Fatalf("%+v %v", s.Device, err)
	}
}

func TestPasswordChangeRevokesDevices(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	u := e.addUser(`{"name":"Дед","password":"ded1"}`, admin)
	tok := e.deviceLogin(u.ID, "ded1", "phone-0001")
	if w := e.do("PATCH", "/api/users/"+itoa(u.ID), `{"password":"ded12"}`, admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(tok)...); w.Code != 401 {
		t.Fatalf("device survived: %d", w.Code)
	}
	// an admin changing their own password from the app keeps that device
	atok := e.deviceLogin(1, "secret", "mac-000001")
	if w := e.do("PATCH", "/api/users/1", `{"password":"newsecret"}`, nil, bearer(atok)...); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(atok)...); w.Code != 200 {
		t.Fatalf("own device revoked: %d", w.Code)
	}
}

func TestAppPasswordResetKeepsDevices(t *testing.T) {
	e := newEnv(t)
	e.setup()
	tok := e.deviceLogin(1, "secret", "phone-0001")
	if w := e.do("POST", "/api/me/app-password", `{}`, nil, bearer(tok)...); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("DELETE", "/api/me/app-password", "", nil, bearer(tok)...); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(tok)...); w.Code != 200 {
		t.Fatalf("device revoked: %d", w.Code)
	}
}

func TestDeleteProfileRevokesDevices(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	u := e.addUser(`{"name":"Дед","password":"ded1"}`, admin)
	tok := e.deviceLogin(u.ID, "ded1", "phone-0001")
	if w := e.do("DELETE", "/api/users/"+itoa(u.ID), "", admin); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(tok)...); w.Code != 401 {
		t.Fatalf("%d", w.Code)
	}
}

type deviceItem struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Model     string `json:"model"`
	Version   string `json:"version"`
	CreatedAt string `json:"createdAt"`
	LastSeen  string `json:"lastSeen"`
	Current   bool   `json:"current"`
	UserID    int64  `json:"userId"`
	UserName  string `json:"userName"`
}

func (e *env) devices(c *http.Cookie, hdr ...string) []deviceItem {
	e.t.Helper()
	w := e.do("GET", "/api/me/devices", "", c, hdr...)
	if w.Code != 200 {
		e.t.Fatalf("devices %d %s", w.Code, w.Body)
	}
	var out struct{ Items []deviceItem }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Items == nil {
		e.t.Fatalf("%s", w.Body)
	}
	return out.Items
}

func TestDevicesListAndRevoke(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	u := e.addUser(`{"name":"Дед","password":"ded1"}`, admin)
	phone := e.deviceLogin(1, "secret", "phone-0001")
	mac := e.deviceLogin(1, "secret", "mac-000001")
	other := e.deviceLogin(u.ID, "ded1", "ipad-00001")

	// an admin sees every profile's devices, each with its profile
	list := e.devices(nil, bearer(phone)...)
	if len(list) != 4 {
		t.Fatalf("%+v", list)
	}
	var cur, macID, webID, otherID int64
	for _, d := range list {
		if d.UserID == u.ID {
			otherID = d.ID
			if d.UserName != "Дед" || d.Current {
				t.Fatalf("other's %+v", d)
			}
			continue
		}
		if d.UserID != 1 || d.UserName == "" {
			t.Fatalf("admin's %+v", d)
		}
		if d.Kind == "web" {
			webID = d.ID
			if d.Current || d.Name != "" {
				t.Fatalf("web %+v", d)
			}
			continue
		}
		if d.Kind != "native" {
			t.Fatalf("kind %+v", d)
		}
		if d.Current {
			cur = d.ID
		} else {
			macID = d.ID
		}
		if d.Name != "iPhone Маши" || d.Model != "iPhone17,1" || d.Version != "1.0" || d.CreatedAt == "" || d.LastSeen == "" {
			t.Fatalf("%+v", d)
		}
	}
	if cur == 0 || macID == 0 || webID == 0 {
		t.Fatalf("current %+v", list)
	}
	// the browser sees itself as the current one
	for _, d := range e.devices(admin) {
		if d.Current != (d.ID == webID) {
			t.Fatalf("web view %+v", d)
		}
	}
	// everyone else sees only their own, and cannot sign out the admin's
	if own := e.devices(nil, bearer(other)...); len(own) != 1 || own[0].ID != otherID || own[0].UserName != "Дед" {
		t.Fatalf("other's view %+v", own)
	}
	if w := e.do("DELETE", "/api/me/devices/"+itoa(macID), "", nil, bearer(other)...); w.Code != 404 || !strings.Contains(w.Body.String(), "Устройство не найдено") {
		t.Fatalf("admin's from other: %d %s", w.Code, w.Body)
	}
	if w := e.do("DELETE", "/api/me/devices/"+itoa(macID), "", admin); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(mac)...); w.Code != 401 {
		t.Fatalf("revoked mac: %d", w.Code)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(other)...); w.Code != 200 {
		t.Fatalf("other user's device: %d", w.Code)
	}
	// the admin signs out another profile's device
	if w := e.do("DELETE", "/api/me/devices/"+itoa(otherID), "", nil, bearer(phone)...); w.Code != 204 {
		t.Fatalf("other's by admin: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(other)...); w.Code != 401 {
		t.Fatalf("revoked other: %d", w.Code)
	}
	// the phone ends the browser's session
	if w := e.do("DELETE", "/api/me/devices/"+itoa(webID), "", nil, bearer(phone)...); w.Code != 204 {
		t.Fatalf("web rowid: %d", w.Code)
	}
	if w := e.do("GET", "/api/me", "", admin); w.Code != 401 {
		t.Fatalf("revoked browser: %d", w.Code)
	}
}

func TestLogoutRevokesBearer(t *testing.T) {
	e := newEnv(t)
	e.setup()
	tok := e.deviceLogin(1, "secret", "phone-0001")
	if w := e.do("POST", "/api/logout", "", nil, bearer(tok)...); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(tok)...); w.Code != 401 {
		t.Fatalf("%d", w.Code)
	}
}

func TestHealthServerIDStable(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	get := func() string {
		w := httptest.NewRecorder()
		NewRouter(Deps{DB: d}).ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
		var b struct {
			OK       bool
			ServerID string `json:"serverId"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || !b.OK {
			t.Fatalf("%s", w.Body)
		}
		return b.ServerID
	}
	a, b := get(), get()
	if len(a) != 32 || a != b {
		t.Fatalf("%q %q", a, b)
	}
	if id, err := LoadServerID(context.Background(), d); err != nil || id != a {
		t.Fatalf("%q %v", id, err)
	}
}
