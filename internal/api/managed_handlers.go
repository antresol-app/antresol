package api

// Managed accounts («Отдельная история на этом устройстве»): a device of a
// person writes its history (progress, watched marks, «Продолжить», the
// family view) into a managed account under that person, who stays the
// one signed in. See auth.HistoryID.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/go-chi/chi/v5"
)

const (
	msgNoManaged      = "Нет такой истории"
	msgTooManyManaged = "Слишком много отдельных историй"
	msgNoDevice       = "Устройство не найдено"
	msgManagedName    = "Укажите название"
)

// historyRef is a device's separate history: the managed account it writes
// into.
type historyRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func historyOf(ss auth.Session) *historyRef {
	if ss.HistoryUserID == 0 {
		return nil
	}
	return &historyRef{ss.HistoryUserID, ss.HistoryName}
}

type managedDevice struct {
	ID    int64  `json:"id"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Model string `json:"model"`
}

type managedOwner struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type managedItem struct {
	ID      int64           `json:"id"`
	Name    string          `json:"name"`
	Avatar  string          `json:"avatar"`
	Owner   *managedOwner   `json:"owner,omitempty"` // the admin's view only
	Devices []managedDevice `json:"devices"`
}

type managedAPI struct {
	users    *auth.Store
	sessions *auth.Sessions
	prog     *progress.Store
}

// mountManaged: inside the session guard. A kid profile only reads: its
// switch, its accounts and what becomes of their history are an
// administrator's to change (an admin switches a kid's device in
// «Устройства» and deletes its accounts), so that a kid cannot erase what
// the family view shows of what it watched.
func mountManaged(r chi.Router, users *auth.Store, sessions *auth.Sessions, prog *progress.Store) {
	m := &managedAPI{users, sessions, prog}
	r.With(noKids).Put("/api/me/devices/{id}/history", m.switchDevice)
	r.Get("/api/me/managed", m.listMine)
	r.With(noKids).Post("/api/me/managed", m.create)
	r.With(noKids).Patch("/api/me/managed/{id}", m.rename)
	r.With(noKids).Delete("/api/me/managed/{id}", m.remove(false))
	r.With(noKids).Post("/api/me/managed/{id}/merge", m.merge)
	r.With(auth.RequireAdmin).Get("/api/managed-accounts", m.listAll)
	r.With(auth.RequireAdmin).Delete("/api/managed-accounts/{id}", m.remove(true))
}

func managedErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrNotFound):
		writeErr(w, r, http.StatusNotFound, msgNoManaged)
	case errors.Is(err, auth.ErrTooMany):
		writeErr(w, r, http.StatusConflict, msgTooManyManaged)
	case errors.Is(err, auth.ErrInvalid):
		writeErr(w, r, http.StatusBadRequest, msgManagedName)
	default:
		storeErr(w, r, err)
	}
}

func (m *managedAPI) item(r *http.Request, u auth.User, withOwner bool) (managedItem, error) {
	it := managedItem{ID: u.ID, Name: u.Name, Avatar: u.Avatar, Devices: []managedDevice{}}
	devs, err := m.sessions.DevicesOfManaged(r.Context(), u.ID)
	if err != nil {
		return it, err
	}
	for _, d := range devs {
		it.Devices = append(it.Devices, managedDevice{d.ID, d.Kind, d.Device.Name, d.Device.Model})
	}
	if withOwner {
		o, err := m.users.Get(r.Context(), u.OwnerID)
		if err != nil {
			return it, err
		}
		it.Owner = &managedOwner{o.ID, o.Name}
	}
	return it, nil
}

func (m *managedAPI) items(w http.ResponseWriter, r *http.Request, list []auth.User, withOwner bool) {
	out := make([]managedItem, 0, len(list))
	for _, u := range list {
		it, err := m.item(r, u, withOwner)
		if err != nil {
			internalErr(w, r, "managed accounts", err)
			return
		}
		out = append(out, it)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (m *managedAPI) listMine(w http.ResponseWriter, r *http.Request) {
	list, err := m.users.ManagedOf(r.Context(), userOf(r).ID)
	if err != nil {
		internalErr(w, r, "managed accounts", err)
		return
	}
	m.items(w, r, list, false)
}

func (m *managedAPI) listAll(w http.ResponseWriter, r *http.Request) {
	list, err := m.users.AllManaged(r.Context())
	if err != nil {
		internalErr(w, r, "managed accounts", err)
		return
	}
	m.items(w, r, list, true)
}

func (m *managedAPI) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := m.users.CreateManaged(r.Context(), userOf(r).ID, in.Name)
	if err != nil {
		managedErr(w, r, err)
		return
	}
	it, err := m.item(r, u, false)
	if err != nil {
		internalErr(w, r, "managed create", err)
		return
	}
	writeJSON(w, http.StatusCreated, it)
}

func managedIDParam(r *http.Request) int64 {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id
}

func (m *managedAPI) rename(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := managedIDParam(r)
	if err := m.users.RenameManaged(r.Context(), userOf(r).ID, id, in.Name); err != nil {
		managedErr(w, r, err)
		return
	}
	u, err := m.users.Get(r.Context(), id)
	if err != nil {
		managedErr(w, r, err)
		return
	}
	it, err := m.item(r, u, false)
	if err != nil {
		internalErr(w, r, "managed rename", err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

// remove deletes a managed account with its history; anyOwner: an admin's, of
// any owner.
func (m *managedAPI) remove(anyOwner bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner := userOf(r).ID
		if anyOwner {
			owner = 0
		}
		if err := m.users.DeleteManaged(r.Context(), owner, managedIDParam(r)); err != nil {
			managedErr(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// merge: «Перенести в мой профиль» for a whole account, which is deleted
// after (its devices write the person's history again). The devices are
// switched off first, so that a write from a TV that is playing meanwhile
// lands in the person's history and not in the account that is going; the
// merge and the deletion are one transaction.
func (m *managedAPI) merge(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	id := managedIDParam(r)
	acc, err := m.users.Get(r.Context(), id)
	if err != nil || acc.OwnerID != u.ID {
		writeErr(w, r, http.StatusNotFound, msgNoManaged)
		return
	}
	if m.prog == nil {
		internalErr(w, r, "managed merge", errors.New("no progress store"))
		return
	}
	if err := m.sessions.DetachManaged(r.Context(), id); err != nil {
		internalErr(w, r, "managed merge", err)
		return
	}
	if _, err := m.prog.MergeAndDelete(r.Context(), id, u.ID, false); err != nil && !errors.Is(err, progress.ErrNotFound) {
		internalErr(w, r, "managed merge", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// switchDevice: PUT /api/me/devices/{id|current}/history
// {"separate": true, "accountId"?: n} or {"separate": false, "merge": bool}.
// A person switches their own devices, an admin anyone's (the account is
// then the device's profile's).
func (m *managedAPI) switchDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Separate  bool  `json:"separate"`
		AccountID int64 `json:"accountId"`
		Merge     bool  `json:"merge"`
	}
	if !decode(w, r, &in) {
		return
	}
	u := userOf(r)
	ctx := r.Context()
	var ss auth.Session
	if chi.URLParam(r, "id") == "current" {
		ss, _ = auth.SessionFrom(ctx)
		if _, viaKey := auth.ViaMediaKey(ctx); viaKey || ss.ID == 0 {
			writeErr(w, r, http.StatusNotFound, msgNoDevice)
			return
		}
		fresh, err := m.sessions.SessionByID(ctx, ss.ID)
		if err != nil {
			writeErr(w, r, http.StatusNotFound, msgNoDevice)
			return
		}
		ss = fresh
	} else {
		var err error
		ss, err = m.sessions.SessionByID(ctx, managedIDParam(r))
		if err != nil || (ss.UserID != u.ID && !u.IsAdmin) {
			writeErr(w, r, http.StatusNotFound, msgNoDevice)
			return
		}
	}
	if in.Separate {
		name := ss.Device.Name
		if name == "" {
			name = ss.Device.Model
		}
		if name == "" {
			name = i18n.T(ctx, "Это устройство")
		}
		if _, err := m.sessions.SwitchOn(ctx, ss, in.AccountID, name); err != nil {
			managedErr(w, r, err)
			return
		}
	} else {
		cur := ss.HistoryUserID
		if err := m.sessions.SetHistory(ctx, ss, 0, false); err != nil {
			internalErr(w, r, "device history", err)
			return
		}
		if in.Merge && cur != 0 {
			if m.prog == nil {
				internalErr(w, r, "device history merge", errors.New("no progress store"))
				return
			}
			// the account goes with the merge unless another device writes into it
			if _, err := m.prog.MergeAndDelete(ctx, cur, ss.UserID, true); err != nil && !errors.Is(err, progress.ErrNotFound) {
				internalErr(w, r, "device history merge", err)
				return
			}
		}
	}
	fresh, err := m.sessions.SessionByID(ctx, ss.ID)
	if err != nil {
		internalErr(w, r, "device history", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": fresh.ID, "history": historyOf(fresh)})
}
