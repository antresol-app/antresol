package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/mediaimport"
	"github.com/go-chi/chi/v5"
)

// mountMediaImport: «Управление → Переезд» (admin only): the media servers'
// import. The addons' own moves live on their pages.
func mountMediaImport(r chi.Router, deps Deps) {
	h := &mediaImportAPI{svc: deps.MediaImport, users: deps.Users}
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/migrate", h.status)
		r.Get("/api/migrate/state", h.state)
		r.Post("/api/migrate/run", h.run)
		r.Get("/api/migrate/reports", h.reports)
		r.Get("/api/migrate/reports/{id}", h.report)
		r.Post("/api/migrate/sources/{id}/connect", h.connect)
		r.Delete("/api/migrate/sources/{id}/connect", h.forget)
	})
}

type mediaImportAPI struct {
	svc   *mediaimport.Service
	users *auth.Store
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
	case errors.Is(err, mediaimport.ErrUnknownRemote):
		writeErr(w, r, http.StatusBadRequest, "Неизвестный источник")
	default:
		internalErr(w, r, what, err)
	}
}

type migrateProfile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// migrateSource is a source's status and, for Jellyfin, Kavita and
// Audiobookshelf, its connection over the network (never the key or the
// password).
type migrateSource struct {
	mediaimport.SourceStatus
	Connect *mediaimport.RemoteView `json:"connect,omitempty"`
}

type migrateStatus struct {
	Sources   []migrateSource            `json:"sources"`
	Leftovers []mediaimport.Line         `json:"leftovers"`
	Users     []mediaimport.UserProposal `json:"users"`
	Profiles  []migrateProfile           `json:"profiles"`
	migrateState
}

// migrateState is the part of the status that changes during a run or a
// library scan; reading it touches no old database, so the page polls it.
type migrateState struct {
	Running      *string `json:"running"`
	LibraryReady struct {
		OK     bool   `json:"ok"`
		Reason string `json:"reason"`
	} `json:"libraryReady"`
}

func (h *mediaImportAPI) readState(r *http.Request) migrateState {
	var out migrateState
	if id := h.svc.Running(); id != "" {
		out.Running = &id
	}
	out.LibraryReady.OK, out.LibraryReady.Reason = h.ready()
	out.LibraryReady.Reason = i18n.Localize(r.Context(), out.LibraryReady.Reason)
	return out
}

func (h *mediaImportAPI) state(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.readState(r))
}

func (h *mediaImportAPI) status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	found := h.svc.Discover(ctx)
	out := migrateStatus{Sources: make([]migrateSource, len(found)), Leftovers: h.svc.Leftovers(ctx)}
	if out.Leftovers == nil {
		out.Leftovers = []mediaimport.Line{}
	}
	for i, st := range found {
		st.Detail = i18n.Localize(ctx, st.Detail)
		st.Error = i18n.Localize(ctx, st.Error)
		out.Sources[i].SourceStatus = st
		if v, ok := h.svc.Remote(st.ID); ok {
			out.Sources[i].Connect = &v
		}
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
	out.migrateState = h.readState(r)
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

// reportSummary is a report in the history.
type reportSummary struct {
	*mediaimport.Report
}

func (h *mediaImportAPI) reports(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Reports()
	if err != nil {
		h.err(w, r, "migrate reports", err)
		return
	}
	out := make([]reportSummary, 0, len(list))
	for _, rep := range list {
		out = append(out, reportSummary{Report: rep})
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

// connectTimeout bounds «Подключить» (it reads every user's marks once, to
// count them).
const connectTimeout = 3 * time.Minute

// connect is «Подключить» on a Jellyfin, Kavita or Audiobookshelf card:
// the address and the key (Jellyfin: or the login) are checked and kept in
// memory for the migration; the answer is what was found, or the reason in
// {"error"}. The key and the password are never answered or logged.
func (h *mediaImportAPI) connect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL      string `json:"url"`
		APIKey   string `json:"apiKey"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	id := chi.URLParam(r, "id")
	ctx, cancel := context.WithTimeout(r.Context(), connectTimeout)
	defer cancel()
	detail, err := h.svc.Connect(ctx, id, mediaimport.Remote{URL: req.URL, APIKey: req.APIKey, Username: req.Username, Password: req.Password})
	var re *mediaimport.RemoteError
	if errors.As(err, &re) {
		writeErr(w, r, http.StatusBadRequest, re.Msg)
		return
	}
	if err != nil {
		h.err(w, r, "migrate connect", err)
		return
	}
	v, _ := h.svc.Remote(id)
	writeJSON(w, http.StatusOK, map[string]any{"detail": i18n.Localize(r.Context(), detail), "connect": v})
}

// forget drops a kept connection («Забыть»): the database file is used again.
func (h *mediaImportAPI) forget(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Forget(chi.URLParam(r, "id")); err != nil {
		h.err(w, r, "migrate forget", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
