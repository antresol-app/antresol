package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/antresol-app/antresol/internal/i18n"
)

// jsonErr writes {"error": msg} with msg (a Russian catalog key) in the
// request language.
func jsonErr(w http.ResponseWriter, r *http.Request, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": i18n.Localize(r.Context(), msg)})
}

type ctxKey struct{}

func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

// WithUser returns ctx carrying u (used by Require and tests).
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// Require checks sessions with a default Guard.
// Test helper: production code uses one configured Guard.
func Require(s *Sessions) func(http.Handler) http.Handler { return (&Guard{Sessions: s}).Require }

// RequireAdmin must run after Require.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFrom(r.Context())
		if !ok {
			jsonErr(w, r, http.StatusUnauthorized, "Нужно войти")
			return
		}
		if !u.IsAdmin {
			jsonErr(w, r, http.StatusForbidden, "Недостаточно прав")
			return
		}
		next.ServeHTTP(w, r)
	})
}
