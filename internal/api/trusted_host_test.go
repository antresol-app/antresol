package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
)

// atHost: a request from a home address that names the server host.
func (e *libEnv) atHost(host, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr, r.Host = "192.168.1.20:50000", host
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

// Home is a home address that reaches the server by its own name: by IP,
// a home name or the public address. A page whose own name points at the
// server's address is away from home (DNS rebinding), whatever the address.
func TestHomeMeansTheServersOwnName(t *testing.T) {
	n := &auth.Network{}
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.Network, d.DB = n, db })
	w := e.do("POST", "/api/users", `{"name":"Мама"}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	mom := e.userID("Мама")
	n.Hosts = &auth.HostPolicy{Extra: []string{"media.lan.example"}, Public: func() string { return "https://media.example.org" }}
	login := `{"userId":` + strconv.FormatInt(mom, 10) + `}`
	for _, host := range []string{"192.168.1.5:8420", "[fd00::5]:8420", "antresol", "antresol.local:8420", "media.example.org", "media.lan.example"} {
		if w := e.atHost(host, "POST", "/api/login", login); w.Code != http.StatusOK {
			t.Errorf("a passwordless sign-in by %q: %d %s", host, w.Code, w.Body)
		}
		if w := e.atHost(host, "POST", "/api/auth/pair/start", `{"device":{"id":"tv-living-01","name":"TV"}}`); w.Code != http.StatusOK {
			t.Errorf("pairing start by %q: %d %s", host, w.Code, w.Body)
		}
	}
	for _, host := range []string{"evil.example.net", "evil.example.net:8420", "media.example.org.evil.net"} {
		w := e.atHost(host, "POST", "/api/login", login)
		if w.Code != http.StatusForbidden || w.Header().Get("Set-Cookie") != "" {
			t.Errorf("a passwordless sign-in by %q: %d %s", host, w.Code, w.Body)
		}
		if w := e.atHost(host, "GET", "/api/setup", ""); !strings.Contains(w.Body.String(), `"remote":true`) {
			t.Errorf("setup by %q: %s", host, w.Body)
		}
	}
}
