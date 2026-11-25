package addons

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/addonkit"
)

func TestUserKeyBelongsToTheSocket(t *testing.T) {
	a, b := UserKey("/run/a.1.sock"), UserKey("/run/b.1.sock")
	if len(a) != 64 || a == b || a != UserKey("/run/a.1.sock") {
		t.Fatalf("keys: %q %q", a, b)
	}
	if UserKey("/run/a.2.sock") == a {
		t.Fatal("a restarted addon keeps its key")
	}
}

// What the server sets on a request for one addon process verifies in that
// process (addonkit.UserFrom with its key), and in no other.
func TestSetUserIsVerifiedByTheProcessOnTheSocket(t *testing.T) {
	const sock, other = "/run/alpha.1.sock", "/run/beta.1.sock"
	value := ASCIIJSON([]byte(`{"id":1,"uid":"u","name":"Ёжик","isAdmin":true}`))
	h := http.Header{}
	SetUser(h, sock, value)
	r := httptest.NewRequest("GET", "/api/admin/x", nil)
	r.Header = h

	t.Setenv(addonkit.UserKeyEnv, UserKey(sock))
	t.Setenv(addonkit.AcceptUnsignedEnv, "off")
	if u, ok := addonkit.UserFrom(r); !ok || u.Name != "Ёжик" || !u.IsAdmin {
		t.Fatalf("the addon on the socket: %+v %v", u, ok)
	}
	t.Setenv(addonkit.UserKeyEnv, UserKey(other))
	if _, ok := addonkit.UserFrom(r); ok {
		t.Fatal("another addon's process takes a header signed for this one")
	}
	// a header another process wrote itself, with no key of the process's
	t.Setenv(addonkit.UserKeyEnv, UserKey(sock))
	forged := httptest.NewRequest("GET", "/api/admin/x", nil)
	forged.Header.Set(addonkit.UserHeader, strings.Replace(value, `"isAdmin":true`, `"isAdmin":true,"id":2`, 1))
	if _, ok := addonkit.UserFrom(forged); ok {
		t.Fatal("an unsigned header is taken when signatures are required")
	}
	forged.Header.Set(addonkit.UserSigHeader, h.Get(addonkit.UserSigHeader))
	if _, ok := addonkit.UserFrom(forged); ok {
		t.Fatal("a signature of another header is taken")
	}
}

func TestChildEnvRefusesUnsignedUnlessTheAdminAllows(t *testing.T) {
	s := &Supervisor{Dir: t.TempDir()}
	env := func() map[string]string {
		m := map[string]string{}
		for _, kv := range s.childEnv("hello", "/run/hello.1.sock", "tok") {
			k, v, _ := strings.Cut(kv, "=")
			m[k] = v
		}
		return m
	}
	if e := env(); e["ANTRESOL_ADDON_ACCEPT_UNSIGNED"] != "off" || e["ANTRESOL_ADDON_USER_KEY"] != UserKey("/run/hello.1.sock") {
		t.Fatalf("default env: %v", e)
	}
	s.AcceptUnsigned = true
	if e := env(); e["ANTRESOL_ADDON_ACCEPT_UNSIGNED"] != "on" {
		t.Fatalf("escape hatch env: %v", e)
	}
}
