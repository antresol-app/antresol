package addonkit

import (
	"encoding/json"
	"net/http"
)

// UserHeader carries the acting profile on proxied requests and on hooks
// run for a profile (JSON, non-ASCII escaped).
const UserHeader = "X-Antresol-User"

// UserFrom is the profile in the request's X-Antresol-User; ok is false
// when the header is missing or broken.
func UserFrom(r *http.Request) (*User, bool) {
	h := r.Header.Get(UserHeader)
	if h == "" {
		return nil, false
	}
	var u User
	if err := json.Unmarshal([]byte(h), &u); err != nil {
		return nil, false
	}
	return &u, true
}

// RequireAdmin lets only admins through to next; anyone else gets 403
// {"error": "Недостаточно прав"}. The server already refuses x/admin/… to
// non-admins; this is defence in depth for an addon's /api/admin/….
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := UserFrom(r); !ok || !u.IsAdmin || u.IsKid {
			WriteError(w, http.StatusForbidden, "Недостаточно прав")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WriteJSON answers status with v as JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteError answers status with {"error": msg}, the contract's error
// shape.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}
