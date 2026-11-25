package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/extsvc"
	"github.com/go-chi/chi/v5"
)

// externalKey stores «Внешние сервисы» in the settings table: its own row,
// apart from app_settings, so a settings screen open in another tab never
// writes an old choice back.
const externalKey = "external_services"

// externalStored is the row: each service chosen or not (absent: never
// chosen, its default applies) and whether the admin closed the notice
// that the services can be chosen.
type externalStored struct {
	Services        map[extsvc.ID]bool `json:"services,omitempty"`
	NoticeDismissed bool               `json:"noticeDismissed,omitempty"`
}

func (s externalStored) choice() extsvc.Choice {
	c := extsvc.Choice{}
	for id, on := range s.Services {
		if extsvc.Known(id) {
			v := on
			c[id] = &v
		}
	}
	return c
}

func loadExternal(ctx context.Context, conn *sql.DB) (externalStored, error) {
	var out externalStored
	if _, err := db.GetJSON(ctx, conn, externalKey, &out); err != nil {
		return externalStored{}, err
	}
	return out, nil
}

func saveExternal(ctx context.Context, conn *sql.DB, s externalStored) error {
	return db.PutJSON(ctx, conn, externalKey, s)
}

// LoadExternalChoice is the stored choice of «Внешние сервисы» (main
// applies it to the switches at startup).
func LoadExternalChoice(ctx context.Context, conn *sql.DB) (extsvc.Choice, error) {
	s, err := loadExternal(ctx, conn)
	return s.choice(), err
}

// externalService is one row of «Внешние сервисы».
type externalService struct {
	ID extsvc.ID `json:"id"`
	// On: the service is asked now; Default: what it is when nobody chose.
	On      bool `json:"on"`
	Default bool `json:"default"`
	// Locked: the server's environment sets it (its VAULT_* variable), so
	// the switch cannot change it.
	Locked bool `json:"locked"`
}

// externalView is GET/PUT /api/settings/external.
type externalView struct {
	Services []externalService `json:"services"`
	// Chosen: the admin has chosen (in the settings or the wizard).
	Chosen bool `json:"chosen"`
	// Notice: show «Состояние»'s one-time notice that the services can be
	// chosen (never chosen, never closed).
	Notice bool `json:"notice"`
}

func mountExternal(r chi.Router, deps Deps) {
	if deps.External == nil {
		return
	}
	a := &externalAPI{db: deps.DB, sw: deps.External, changed: deps.ExternalChanged}
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/settings/external", a.get)
		r.Put("/api/settings/external", a.put)
	})
}

type externalAPI struct {
	db      *sql.DB
	sw      *extsvc.Switches
	changed func(extsvc.ID, bool)
}

func (a *externalAPI) view(s externalStored) externalView {
	v := externalView{Services: []externalService{}, Chosen: len(s.choice()) > 0}
	v.Notice = !v.Chosen && !s.NoticeDismissed
	for _, id := range extsvc.All {
		_, locked := a.sw.Env(id)
		v.Services = append(v.Services, externalService{ID: id, On: a.sw.On(id), Default: extsvc.Default(id), Locked: locked})
	}
	return v
}

// GET /api/settings/external (admin).
func (a *externalAPI) get(w http.ResponseWriter, r *http.Request) {
	s, err := loadExternal(r.Context(), a.db)
	if err != nil {
		internalErr(w, r, "external services", err)
		return
	}
	writeJSON(w, http.StatusOK, a.view(s))
}

// PUT /api/settings/external {services?: {id: bool}, dismissNotice?: bool}
// (admin): the services given are chosen (the others keep theirs) and
// apply at once; a locked one is stored but the environment still decides.
func (a *externalAPI) put(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Services      map[extsvc.ID]bool `json:"services"`
		DismissNotice bool               `json:"dismissNotice"`
	}
	if !decode(w, r, &body) {
		return
	}
	for id := range body.Services {
		if !extsvc.Known(id) {
			writeErr(w, r, http.StatusBadRequest, "Неизвестный внешний сервис")
			return
		}
	}
	ctx := r.Context()
	s, err := loadExternal(ctx, a.db)
	if err != nil {
		internalErr(w, r, "external services", err)
		return
	}
	before := map[extsvc.ID]bool{}
	for _, id := range extsvc.All {
		before[id] = a.sw.On(id)
	}
	if len(body.Services) > 0 && s.Services == nil {
		s.Services = map[extsvc.ID]bool{}
	}
	for id, on := range body.Services {
		s.Services[id] = on
	}
	if body.DismissNotice {
		s.NoticeDismissed = true
	}
	if err := saveExternal(ctx, a.db, s); err != nil {
		internalErr(w, r, "external services save", err)
		return
	}
	a.sw.Apply(s.choice())
	for _, id := range extsvc.All {
		if on := a.sw.On(id); on != before[id] && a.changed != nil {
			a.changed(id, on)
		}
	}
	writeJSON(w, http.StatusOK, a.view(s))
}
