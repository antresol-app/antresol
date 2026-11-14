package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/auth"
)

// AccountRoutes are the external-accounts routes (e.g. the Shikimori link:
// internal/accounts documents their JSON). A nil func mounts nothing.
type AccountRoutes struct {
	// API routes need a session (401) and turn kid profiles away (403).
	API func(chi.Router)
	// Browser routes are browser navigations (an OAuth callback): they
	// answer with a page or a redirect, so a request without a session, or
	// from a kid profile, reaches them without a user in its context.
	Browser func(chi.Router)
}

// mountAccounts mounts Deps.Accounts. Kid profiles never act through
// them: the API answers 403, the browser routes see no user.
func mountAccounts(r chi.Router, guard *auth.Guard, a AccountRoutes) {
	if a.API != nil {
		r.Group(func(r chi.Router) {
			r.Use(guard.Require)
			r.Use(noKids) // (files_handlers.go)
			a.API(r)
		})
	}
	if a.Browser != nil {
		r.Group(func(r chi.Router) {
			r.Use(optionalAdult(guard))
			a.Browser(r)
		})
	}
}

// optionalAdult runs the session check without answering for it: a
// request it would turn away, or a kid profile's, goes on without a user.
func optionalAdult(guard *auth.Guard) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			passed := false
			guard.Require(http.HandlerFunc(func(_ http.ResponseWriter, checked *http.Request) {
				passed = true
				if u, ok := auth.UserFrom(checked.Context()); ok && !u.IsKid {
					next.ServeHTTP(w, checked)
					return
				}
				next.ServeHTTP(w, r)
			})).ServeHTTP(discardWriter{}, r)
			if !passed {
				next.ServeHTTP(w, r)
			}
		})
	}
}

// discardWriter swallows the session check's own rejection.
type discardWriter struct{}

func (discardWriter) Header() http.Header         { return http.Header{} }
func (discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (discardWriter) WriteHeader(int)             {}
