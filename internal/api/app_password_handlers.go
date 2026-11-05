package api

import (
	"net/http"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/go-chi/chi/v5"
)

// mountAppPassword: the signed-in profile's password for music apps
// (Subsonic) and readers (OPDS). Any profile, kids included, manages its
// own. POST returns the new password in the clear, once (the user types it
// into the app); GET only says whether there is one.
func mountAppPassword(r chi.Router, users *auth.Store) {
	// GET never returns the password: it is shown once, by POST. Only
	// whether one exists and when it was made.
	r.Get("/api/me/app-password", func(w http.ResponseWriter, r *http.Request) {
		ok, at, err := users.AppPasswordInfo(r.Context(), userOf(r).ID)
		if err != nil {
			storeErr(w, r, err)
			return
		}
		out := struct {
			Exists    bool    `json:"exists"`
			CreatedAt *string `json:"createdAt"`
		}{Exists: ok}
		if ok && !at.IsZero() {
			s := at.UTC().Format(time.RFC3339)
			out.CreatedAt = &s
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
	})
	r.Post("/api/me/app-password", func(w http.ResponseWriter, r *http.Request) {
		p, err := users.SetAppPassword(r.Context(), userOf(r).ID)
		if err != nil {
			storeErr(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"password": p})
	})
	r.Delete("/api/me/app-password", func(w http.ResponseWriter, r *http.Request) {
		if err := users.ClearAppPassword(r.Context(), userOf(r).ID); err != nil {
			storeErr(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
