package api

import (
	"encoding/json"
	"testing"
)

// TestAuthGuardLanguage: the session guard's own JSON errors follow the
// request language.
func TestAuthGuardLanguage(t *testing.T) {
	e := newSettingsEnv(t)
	for _, c := range []struct{ lang, want string }{
		{"", "Нужно войти"},
		{"en", "Sign in required"},
	} {
		var hdr []string
		if c.lang != "" {
			hdr = []string{"Accept-Language", c.lang}
		}
		w := e.do("GET", "/api/settings", "", nil, hdr...)
		var out struct{ Error string }
		json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != 401 || out.Error != c.want {
			t.Errorf("Accept-Language %q: %d %q, want 401 %q", c.lang, w.Code, out.Error, c.want)
		}
	}
}
