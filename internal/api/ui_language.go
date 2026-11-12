package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// The interface language of a profile in the apps:
//   - GET /api/me/ui-language: {"language": the profile's choice ("" none),
//     "effective": what the apps show (the choice, else the first of the
//     profile's audio languages the apps speak, else the household's; ""
//     only when none of them is one)}
//   - PUT /api/me/ui-language {"language": "ru" | "en" | ""}: "" goes back to
//     the profile's language.
func mountUILanguage(r chi.Router, h *lib) {
	r.Get("/api/me/ui-language", h.myUILanguage)
	r.Put("/api/me/ui-language", h.putMyUILanguage)
}

// uiLanguages are the languages the apps' interfaces have.
var uiLanguages = map[string]bool{"ru": true, "en": true}

func (h *lib) chosenUILanguage(ctx context.Context, uid int64) (string, error) {
	var l string
	err := h.DB.QueryRowContext(ctx, `SELECT lang FROM ui_prefs WHERE user_id=?`, uid).Scan(&l)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return l, err
}

// effectiveUILanguage is the language the apps show profile uid in.
func (h *lib) effectiveUILanguage(ctx context.Context, uid int64) (chosen, effective string, err error) {
	chosen, err = h.chosenUILanguage(ctx, uid)
	if err != nil || chosen != "" {
		return chosen, chosen, err
	}
	p, err := h.prefsFor(ctx, uid)
	if err != nil {
		return "", "", err
	}
	for _, l := range p.AudioLangs {
		if l = primaryLang(l); uiLanguages[l] {
			return "", l, nil
		}
	}
	return "", "", nil
}

func (h *lib) myUILanguage(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	chosen, eff, err := h.effectiveUILanguage(r.Context(), u.ID)
	if err != nil {
		internalErr(w, r, "ui language", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"language": chosen, "effective": eff})
}

func (h *lib) putMyUILanguage(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var in struct {
		Language string `json:"language"`
	}
	if !decode(w, r, &in) {
		return
	}
	l := strings.ToLower(strings.TrimSpace(in.Language))
	var err error
	switch {
	case l == "":
		_, err = h.DB.ExecContext(r.Context(), `DELETE FROM ui_prefs WHERE user_id=?`, u.ID)
	case uiLanguages[l]:
		_, err = h.DB.ExecContext(r.Context(), `INSERT INTO ui_prefs (user_id, lang) VALUES (?,?)
		  ON CONFLICT (user_id) DO UPDATE SET lang=excluded.lang, updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')`, u.ID, l)
	default:
		writeErr(w, r, http.StatusBadRequest, "Неизвестный язык интерфейса")
		return
	}
	if err != nil {
		internalErr(w, r, "ui language", err)
		return
	}
	h.myUILanguage(w, r)
}
