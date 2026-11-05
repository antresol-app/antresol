package api

import (
	"errors"
	"net/http"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/mediaimport"
	"github.com/go-chi/chi/v5"
)

// mountMediaImport: «Управление → Переезд» (admin only): the media servers'
// import in both editions, and the sources build's own part (migrate_handlers_full.go).
func mountMediaImport(r chi.Router, deps Deps) {
	h := &mediaImportAPI{svc: deps.MediaImport, users: deps.Users, full: newImportFull(deps)}
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/migrate", h.status)
		r.Get("/api/migrate/state", h.state)
		r.Post("/api/migrate/run", h.run)
		r.Get("/api/migrate/reports", h.reports)
		r.Get("/api/migrate/reports/{id}", h.report)
		h.mountFull(r)
	})
}

type mediaImportAPI struct {
	svc   *mediaimport.Service
	users *auth.Store
	full  importFull
}

const notScannedMsg = "Медиатека ещё сканируется — подождите"

// ready is the library state with the reason to show ("" when ready).
func (h *mediaImportAPI) ready() (bool, string) {
	ok, reason := h.svc.Ready()
	switch {
	case ok:
		return true, ""
	case reason == "":
		return false, notScannedMsg
	}
	return false, reason
}

func (h *mediaImportAPI) err(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case r.Context().Err() != nil:
		return // the client went away
	case errors.Is(err, mediaimport.ErrNeedsDryRun):
		writeErr(w, r, http.StatusConflict, "Сначала проверьте, что будет перенесено")
	case errors.Is(err, mediaimport.ErrNotReady):
		_, reason := h.ready()
		writeErr(w, r, http.StatusConflict, reason)
	case errors.Is(err, mediaimport.ErrBusy):
		writeErr(w, r, http.StatusConflict, "Перенос уже идёт")
	case errors.Is(err, mediaimport.ErrUnknownSource):
		writeErr(w, r, http.StatusBadRequest, "Неизвестный источник")
	case errors.Is(err, mediaimport.ErrNoSources):
		writeErr(w, r, http.StatusBadRequest, "Выберите, что перенести")
	case errors.Is(err, mediaimport.ErrUnknownProfile):
		writeErr(w, r, http.StatusBadRequest, "Такого профиля в Antresol нет — обновите страницу")
	case errors.Is(err, mediaimport.ErrNotFound):
		writeErr(w, r, http.StatusNotFound, "Отчёт не найден")
	default:
		if !h.full.err(w, r, err) {
			internalErr(w, r, what, err)
		}
	}
}

type migrateProfile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type migrateStatus struct {
	Sources   []mediaimport.SourceStatus `json:"sources"`
	Leftovers []mediaimport.Line         `json:"leftovers"`
	Users     []mediaimport.UserProposal `json:"users"`
	Profiles  []migrateProfile           `json:"profiles"`
	migrateState
}

// migrateState is the part of the status that changes during a run or a
// library scan; reading it touches no old database, so the page polls it.
// The sources build adds the switch state (migrateStateFull).
type migrateState struct {
	Running      *string `json:"running"`
	LibraryReady struct {
		OK     bool   `json:"ok"`
		Reason string `json:"reason"`
	} `json:"libraryReady"`
	migrateStateFull
}

func (h *mediaImportAPI) readState(w http.ResponseWriter, r *http.Request) (migrateState, bool) {
	var out migrateState
	if id := h.svc.Running(); id != "" {
		out.Running = &id
	}
	if err := h.full.fillState(r.Context(), &out.migrateStateFull); err != nil {
		h.err(w, r, "migrate state", err)
		return out, false
	}
	out.LibraryReady.OK, out.LibraryReady.Reason = h.ready()
	out.LibraryReady.Reason = i18n.Localize(r.Context(), out.LibraryReady.Reason)
	return out, true
}

func (h *mediaImportAPI) state(w http.ResponseWriter, r *http.Request) {
	if out, ok := h.readState(w, r); ok {
		writeJSON(w, http.StatusOK, out)
	}
}

func (h *mediaImportAPI) status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := migrateStatus{Sources: h.svc.Discover(ctx), Leftovers: h.svc.Leftovers(ctx)}
	if out.Leftovers == nil {
		out.Leftovers = []mediaimport.Line{}
	}
	for i := range out.Sources {
		out.Sources[i].Detail = i18n.Localize(ctx, out.Sources[i].Detail)
		out.Sources[i].Error = i18n.Localize(ctx, out.Sources[i].Error)
	}
	out.Leftovers = mediaimport.LocalizeLines(ctx, out.Leftovers)
	var err error
	if out.Users, err = h.svc.Users(ctx); err != nil {
		h.err(w, r, "migrate users", err)
		return
	}
	for i := range out.Users {
		out.Users[i].Error = i18n.Localize(ctx, out.Users[i].Error)
	}
	list, err := h.users.List(ctx)
	if err != nil {
		h.err(w, r, "migrate profiles", err)
		return
	}
	out.Profiles = make([]migrateProfile, 0, len(list))
	for _, u := range list {
		out.Profiles = append(out.Profiles, migrateProfile{ID: u.ID, Name: u.Name})
	}
	st, ok := h.readState(w, r)
	if !ok {
		return
	}
	out.migrateState = st
	writeJSON(w, http.StatusOK, out)
}

func (h *mediaImportAPI) run(w http.ResponseWriter, r *http.Request) {
	var req mediaimport.RunRequest
	if !decode(w, r, &req) {
		return
	}
	req.AdminID = userOf(r).ID
	id, err := h.svc.Start(r.Context(), req)
	if err != nil {
		h.err(w, r, "migrate run", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"reportId": id})
}

// reportSummary is a report in the history: no lines; another build adds
// a number of its own.
type reportSummary struct {
	*mediaimport.Report
	reportSummaryFull
}

func (h *mediaImportAPI) reports(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Reports()
	if err != nil {
		h.err(w, r, "migrate reports", err)
		return
	}
	out := make([]reportSummary, 0, len(list))
	for _, rep := range list {
		sum := reportSummary{Report: rep}
		h.full.fillSummary(&sum)
		out = append(out, sum)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *mediaImportAPI) report(w http.ResponseWriter, r *http.Request) {
	rep, err := h.svc.Report(chi.URLParam(r, "id"))
	if err != nil {
		h.err(w, r, "migrate report", err)
		return
	}
	rep.Localize(r.Context())
	writeJSON(w, http.StatusOK, rep)
}
