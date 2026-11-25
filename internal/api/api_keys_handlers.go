package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/go-chi/chi/v5"
)

// mountAPIKeys: «Ключи API и ИИ», the signed-in profile's personal API
// keys (auth.APIKeys). POST returns the new key in the clear, once. Kids
// have none. An API key itself never reaches these routes (the guard
// refuses /api/me/api-keys to keys). publicURL: VAULT_PUBLIC_URL, for the
// snippets the page shows ("": the page uses its own address).
func mountAPIKeys(r chi.Router, keys *auth.APIKeys, publicURL string) {
	adultOnly := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if u := userOf(r); u.IsKid || u.OwnerID != 0 {
				writeErr(w, r, http.StatusForbidden, "У детского профиля нет ключей API")
				return
			}
			next(w, r)
		}
	}
	r.Get("/api/me/api-keys", adultOnly(func(w http.ResponseWriter, r *http.Request) {
		u := userOf(r)
		list, err := keys.List(r.Context(), u.ID)
		if err != nil {
			internalErr(w, r, "api keys", err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"keys":      list,
			"canAdmin":  u.IsAdmin,
			"publicUrl": publicURL,
			"mcpPath":   auth.MCPPath,
		})
	}))
	r.Post("/api/me/api-keys", adultOnly(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name  string `json:"name"`
			Scope string `json:"scope"`
		}
		if !decode(w, r, &in) {
			return
		}
		key, info, err := keys.Create(r.Context(), userOf(r), in.Name, in.Scope)
		switch {
		case errors.Is(err, auth.ErrInvalid):
			writeErr(w, r, http.StatusBadRequest, "Назовите ключ и выберите, что ему можно")
			return
		case errors.Is(err, auth.ErrKidKey):
			writeErr(w, r, http.StatusForbidden, "У детского профиля нет ключей API")
			return
		case errors.Is(err, auth.ErrAdminScope):
			writeErr(w, r, http.StatusForbidden, "Ключ с администрированием может создать только администратор")
			return
		case errors.Is(err, auth.ErrTooManyKeys):
			writeErr(w, r, http.StatusConflict, "Ключей слишком много: отзовите ненужные")
			return
		case err != nil:
			internalErr(w, r, "api key create", err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "info": info})
	}))
	r.Delete("/api/me/api-keys/{id}", adultOnly(func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r, "id")
		if !ok {
			writeErr(w, r, http.StatusNotFound, "Ключ не найден")
			return
		}
		if err := keys.Revoke(r.Context(), userOf(r).ID, id); errors.Is(err, auth.ErrNotFound) {
			writeErr(w, r, http.StatusNotFound, "Ключ не найден")
			return
		} else if err != nil {
			internalErr(w, r, "api key revoke", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	r.Get("/api/me/api-keys/log", adultOnly(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		list, err := keys.Events(r.Context(), userOf(r).ID, n)
		if err != nil {
			internalErr(w, r, "api key log", err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"events": list})
	}))
}
