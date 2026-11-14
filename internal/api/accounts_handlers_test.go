package api

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/auth"
)

// TestKidCannotLink: the accounts API needs a session, and a kid profile
// gets 403 on every route; the browser routes (the OAuth callback) are
// reached by a kid or without a session with no user in the context.
func TestKidCannotLink(t *testing.T) {
	reached := 0
	var browserUser []bool
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.Accounts = AccountRoutes{
			API: func(r chi.Router) {
				h := func(w http.ResponseWriter, _ *http.Request) { reached++; w.WriteHeader(http.StatusNoContent) }
				r.Get("/api/accounts", h)
				r.Put("/api/accounts/shikimori", h)
				r.Post("/api/accounts/shikimori/write", h)
			},
			Browser: func(r chi.Router) {
				r.Get("/api/accounts/shikimori/callback", func(w http.ResponseWriter, r *http.Request) {
					_, ok := auth.UserFrom(r.Context())
					browserUser = append(browserUser, ok)
					http.Redirect(w, r, "/settings/profile", http.StatusFound)
				})
			},
		}
	})
	kid := e.kid("")
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/accounts", ""},
		{"PUT", "/api/accounts/shikimori", `{"nickname":"tester"}`},
		{"POST", "/api/accounts/shikimori/write", `{"enable":true}`},
	} {
		if w := e.do(c.method, c.path, c.body, kid); w.Code != http.StatusForbidden {
			t.Errorf("kid %s %s: %d", c.method, c.path, w.Code)
		}
		if w := e.do(c.method, c.path, c.body, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("no session %s %s: %d", c.method, c.path, w.Code)
		}
	}
	if reached != 0 {
		t.Fatalf("handlers reached %d times", reached)
	}
	if w := e.do("GET", "/api/accounts", "", e.admin); w.Code != http.StatusNoContent || reached != 1 {
		t.Fatalf("admin: %d (reached %d)", w.Code, reached)
	}
	const cb = "/api/accounts/shikimori/callback?code=c&state=s"
	for _, c := range []*http.Cookie{kid, nil, e.admin} {
		if w := e.do("GET", cb, "", c); w.Code != http.StatusFound {
			t.Fatalf("callback: %d %s", w.Code, w.Body)
		}
	}
	if len(browserUser) != 3 || browserUser[0] || browserUser[1] || !browserUser[2] {
		t.Fatalf("browser route users %v (kid, none, admin)", browserUser)
	}
}
