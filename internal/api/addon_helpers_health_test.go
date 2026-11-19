package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/antresol-app/antresol/internal/addons"
)

// GET /api/addons/helpers: the admin's; each enabled addon's helper asked
// on its compose address, a disabled addon's never.
func TestAddonHelpersHealth(t *testing.T) {
	old := probeHelper
	t.Cleanup(func() { probeHelper = old })
	var asked []string
	probeHelper = func(_ context.Context, h addons.Helper) error {
		asked = append(asked, h.Name)
		return errors.New("dial tcp: lookup cache-svc: no such host")
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
	if len(out) != 1 || out[0].Addon != "lib" || out[0].Name != "cache-svc" || out[0].Address != "http://cache-svc:8191" || out[0].OK || out[0].Error == "" {
		t.Fatalf("%s", w.Body)
	}
	if len(asked) != 1 {
		t.Fatalf("asked %v", asked)
	}
	probeHelper = func(context.Context, addons.Helper) error { return nil }
	w = e.do("GET", "/api/addons/helpers", "", e.admin)
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out[0].OK {
		t.Fatalf("%s", w.Body)
	}
}
