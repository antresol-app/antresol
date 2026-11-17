package api

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/addonkit/addonkittest"
)

// addonCoreExempt: the addon-core routes that have no golden answer in
// addonkit/testdata/golden, each with the reason. Keyed "METHOD pattern",
// the pattern as chi.Walk prints it. A route here that gets a golden
// fails the guard too: the line goes.
var addonCoreExempt = map[string]string{
	"GET /api/addon-core/v1/metadata/{source}/*":  "pass-through to the metadata provider: the answer is the provider's, not the core's (addon_core_metadata_test.go)",
	"HEAD /api/addon-core/v1/metadata/{source}/*": "pass-through to the metadata provider (addon_core_metadata_test.go)",
	"GET /api/addon-core/v1/metadata/{source}":    "pass-through to the metadata provider (addon_core_metadata_test.go)",
	"HEAD /api/addon-core/v1/metadata/{source}":   "pass-through to the metadata provider (addon_core_metadata_test.go)",
}

// addonCoreRoutes: every "METHOD pattern" of the addon-core API, from the
// router the server mounts.
func addonCoreRoutes(t *testing.T) (*chi.Mux, map[string]bool) {
	t.Helper()
	r := chi.NewRouter()
	mountAddonCore(r, &addonsAPI{})
	routes := map[string]bool{}
	if err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+strings.ReplaceAll(route, "/*/", "/")] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return r, routes
}

// TestAddonCoreRouteCoverage (G8): every route under /api/addon-core/v1
// has a golden answer of TestAddonCoreGoldens (which the addons' adapter
// tests replay through addonkittest.ReplayGolden) or an exemption line
// with its reason; every golden answers a route that exists.
func TestAddonCoreRouteCoverage(t *testing.T) {
	r, routes := addonCoreRoutes(t)
	if len(routes) < 50 {
		t.Fatalf("only %d addon-core routes walked: the walk is broken", len(routes))
	}
	covered := map[string][]string{}
	for name, g := range addonkittest.LoadGoldens(t, addonGoldenDir) {
		u, err := url.Parse(g.Path)
		if err != nil {
			t.Errorf("golden %s: path %q: %v", name, g.Path, err)
			continue
		}
		pattern := r.Find(chi.NewRouteContext(), g.Method, u.EscapedPath())
		if pattern == "" || !routes[g.Method+" "+pattern] {
			t.Errorf("golden %s: %s %s answers no addon-core route", name, g.Method, u.Path)
			continue
		}
		covered[g.Method+" "+pattern] = append(covered[g.Method+" "+pattern], name)
	}
	var missing []string
	for rt := range routes {
		_, exempt := addonCoreExempt[rt]
		switch {
		case len(covered[rt]) == 0 && !exempt:
			missing = append(missing, rt)
		case len(covered[rt]) > 0 && exempt:
			t.Errorf("%s has golden %v and an exemption: drop the exemption", rt, covered[rt])
		}
	}
	sort.Strings(missing)
	for _, rt := range missing {
		t.Errorf("%s has no golden answer (TestAddonCoreGoldens) and no exemption line", rt)
	}
	for rt, why := range addonCoreExempt {
		if !routes[rt] {
			t.Errorf("exemption %q (%s) names no addon-core route", rt, why)
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("exemption %q has no reason", rt)
		}
	}
}
