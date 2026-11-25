package api

import "testing"

// Every answer carries the three plain hardening headers; a handler that
// sets its own policy (the relay's no-referrer) still wins.
func TestSecurityHeaders(t *testing.T) {
	e := newLibEnv(t)
	for _, p := range []string{"/api/health", "/api/profiles", "/api/me", "/nope"} {
		h := e.do("GET", p, "", e.admin).Header()
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "strict-origin-when-cross-origin" ||
			h.Get("Permissions-Policy") != "camera=(), microphone=(), geolocation=()" {
			t.Errorf("%s: %v", p, h)
		}
	}
}
