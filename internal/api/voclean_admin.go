package api

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/voclean"
)

// vocleanBackfillKey stores «Обработать всю медиатеку» in the settings
// table ("1" on; no row or anything else: off).
const vocleanBackfillKey = "voclean_backfill"

// VocleanAdmin is the part of *voclean.Worker the admin's «Очищенные
// озвучки» uses.
type VocleanAdmin interface {
	Status(ctx context.Context) (voclean.Status, error)
	Recheck(ctx context.Context) (int, error)
	Kick()
}

// LoadVocleanBackfill is «Обработать всю медиатеку» (voclean.Worker.Backfill):
// off when unset or unreadable.
func LoadVocleanBackfill(ctx context.Context, db *sql.DB) bool {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, vocleanBackfillKey).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
		log.Printf("api: %s: %v", vocleanBackfillKey, err)
	}
	return v == "1"
}

type vocleanAPI struct {
	db *sql.DB
	w  VocleanAdmin
}

// vocleanView is GET /api/admin/voclean: the tracks by state and whether
// the whole library is processed now.
type vocleanView struct {
	voclean.Status
	Backfill bool `json:"backfill"`
}

// mountVoclean: «Очищенные озвучки» in the admin's server settings.
//
//   - GET /api/admin/voclean: vocleanView;
//   - POST /api/admin/voclean/backfill {on, recheck}: «Обработать всю
//     медиатеку» on or off; recheck: «Перепроверить неподходящие» (the not
//     eligible and given-up tracks measured again). Answers vocleanView.
func mountVoclean(r chi.Router, deps Deps) {
	if deps.Voclean == nil || deps.DB == nil {
		return
	}
	v := &vocleanAPI{db: deps.DB, w: deps.Voclean}
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/admin/voclean", v.get)
		r.Post("/api/admin/voclean/backfill", v.backfill)
	})
}

func (v *vocleanAPI) view(ctx context.Context) (vocleanView, error) {
	st, err := v.w.Status(ctx)
	return vocleanView{Status: st, Backfill: LoadVocleanBackfill(ctx, v.db)}, err
}

func (v *vocleanAPI) get(w http.ResponseWriter, r *http.Request) {
	out, err := v.view(r.Context())
	if err != nil {
		internalErr(w, r, "voclean status", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (v *vocleanAPI) backfill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On      bool `json:"on"`
		Recheck bool `json:"recheck"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx := r.Context()
	val := "0"
	if body.On {
		val = "1"
	}
	if _, err := v.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, vocleanBackfillKey, val); err != nil {
		internalErr(w, r, "voclean backfill", err)
		return
	}
	if body.Recheck {
		if _, err := v.w.Recheck(ctx); err != nil {
			internalErr(w, r, "voclean recheck", err)
			return
		}
	}
	v.w.Kick() // the queue read again with the new setting
	v.get(w, r)
}
