package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
)

// GET /api/addons/helpers: the admin's; each enabled addon's helper asked
// on its compose address, a disabled addon's never.
func TestAddonHelpersHealth(t *testing.T) {
	old := probeHelper
	t.Cleanup(func() { probeHelper = old })
	var asked []string
	probeHelper = func(_ context.Context, base string, h addons.Helper) error {
		asked = append(asked, base)
		return &net.DNSError{Err: "no such host", Name: h.Name, IsNotFound: true}
	}
	e := newAddonEnv(t)
	if w := e.do("GET", "/api/addons/helpers", "", e.user); w.Code != 403 {
		t.Fatalf("as a user: %d", w.Code)
	}
	w := e.do("GET", "/api/addons/helpers", "", e.admin)
	var out []helperHealth
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(out) != 1 || out[0].Addon != "lib" || out[0].Name != "cache-svc" || out[0].Address != "http://cache-svc:8191" || out[0].OK || out[0].Problem != "dns" || out[0].Custom {
		t.Fatalf("%s", w.Body)
	}
	if len(asked) != 1 || asked[0] != "http://cache-svc:8191" {
		t.Fatalf("asked %v", asked)
	}
	probeHelper = func(context.Context, string, addons.Helper) error { return nil }
	w = e.do("GET", "/api/addons/helpers", "", e.admin)
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out[0].OK {
		t.Fatalf("%s", w.Body)
	}

	// the admin's own address (as the addon reads it): asked instead, never shown
	t.Setenv("ANTRESOL_LIB_CACHE_SVC_URL", "http://user:pw@10.0.0.9:8191/")
	asked = nil
	probeHelper = func(_ context.Context, base string, _ addons.Helper) error {
		asked = append(asked, base)
		return errors.New("dial tcp 10.0.0.9:8191: connect: connection refused")
	}
	w = e.do("GET", "/api/addons/helpers", "", e.admin)
	out = nil
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out[0].Custom || out[0].Address != "" || out[0].Problem != "refused" || out[0].Error != "" ||
		strings.Contains(w.Body.String(), "pw@") || len(asked) != 1 || asked[0] != "http://user:pw@10.0.0.9:8191/" {
		t.Fatalf("custom: %s asked %v", w.Body, asked)
	}

	// at its own address any HTTP answer is up (the service's root may say 404)
	probeHelper = func(context.Context, string, addons.Helper) error { return statusErr(404) }
	w = e.do("GET", "/api/addons/helpers", "", e.admin)
	out = nil
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out[0].OK {
		t.Fatalf("custom 404: %s", w.Body)
	}
}

// An addon answering helpers.status is shown as it told (its own
// settings' address, its last real request), never probed by the core.
func TestAddonHelpersHealthTold(t *testing.T) {
	oldP, oldS := probeHelper, helperStates
	t.Cleanup(func() { probeHelper, helperStates = oldP, oldS })
	probed := 0
	probeHelper = func(context.Context, string, addons.Helper) error { probed++; return nil }
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	helperStates = func(_ context.Context, _ *addons.Hooks, declared func(id, name string) (addons.Helper, bool)) map[string][]addonkit.HelperState {
		if _, ok := declared("lib", "cache-svc"); !ok {
			t.Error("lib's helper not declared")
		}
		if _, ok := declared("off", "cache-svc"); ok {
			t.Error("a disabled addon's helper declared")
		}
		return map[string][]addonkit.HelperState{"lib": {{Name: "cache-svc", Source: addonkit.HelperSettings, OK: true, CheckedAt: at, Live: true}}}
	}
	e := newAddonEnv(t)
	w := e.do("GET", "/api/addons/helpers", "", e.admin)
	var out []helperHealth
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if probed != 0 || len(out) != 1 || !out[0].OK || !out[0].Custom || out[0].Source != "settings" || out[0].Address != "" || !out[0].Live || !out[0].CheckedAt.Equal(at) {
		t.Fatalf("probed %d: %s", probed, w.Body)
	}
}

// The core's own probe: 401/403 at the admin's address is a refused
// login, not an answer.
func TestAddonHelpersHealthAuth(t *testing.T) {
	old := probeHelper
	t.Cleanup(func() { probeHelper = old })
	probeHelper = func(context.Context, string, addons.Helper) error { return statusErr(401) }
	t.Setenv("ANTRESOL_LIB_CACHE_SVC_URL", "http://10.0.0.9:8191/")
	e := newAddonEnv(t)
	w := e.do("GET", "/api/addons/helpers", "", e.admin)
	var out []helperHealth
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out[0].OK || out[0].Problem != "auth" {
		t.Fatalf("%s", w.Body)
	}
}
