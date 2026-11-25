package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/go-chi/chi/v5"
)

// The core API's inputs (play): an addon registers an external video it
// serves on its socket, for one profile to play; the core relays it.

const (
	msgAddonPlayOff       = "Проигрывание недоступно"
	msgAddonInputTooBig   = "Описание видео больше 64 КБ"
	msgAddonKidProfile    = "Детскому профилю это недоступно"
	msgAddonNotForProfile = "Профилю это расширение недоступно"
	msgAddonNoItem        = "Элемент медиатеки не найден"
	msgAddonNotVideoItem  = "Это не фильм и не серия"
	msgAddonNoInput       = "Видео не найдено"

	addonInputBody = 64 << 10
)

func mountAddonCorePlay(r chi.Router, a *addonsAPI) {
	r.Post("/play/inputs", a.coreAddInput)
	r.Delete("/play/inputs/{id}", a.coreDropInput)
}

// coreAddInput: POST InputRequest → 201 InputCreated. The profile must be
// an adult one the addon is open to (admins always; others only while its
// access is "adults"); itemId, if set, a movie or episode that profile
// can see.
func (a *addonsAPI) coreAddInput(w http.ResponseWriter, r *http.Request) {
	in, ok := a.callingAddon(w, r, "play")
	if !ok {
		return
	}
	if a.inputs == nil {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonPlayOff)
		return
	}
	var req addonkit.InputRequest
	bodyDeadline(w)
	r.Body = http.MaxBytesReader(w, r.Body, addonInputBody)
	dec := json.NewDecoder(r.Body)
	err := dec.Decode(&req)
	if err == nil {
		// one JSON value: anything after it but white space is not JSON
		if err = dec.Decode(&struct{}{}); err == io.EOF {
			err = nil
		} else if err == nil {
			err = errors.New("trailing data")
		}
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, r, http.StatusRequestEntityTooLarge, msgAddonInputTooBig)
			return
		}
		badRequest(w, r)
		return
	}
	if err := addons.ValidateInput(req); err != nil {
		addonErr(w, r, err)
		return
	}
	if a.users == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "Профили недоступны")
		return
	}
	u, err := a.users.Get(r.Context(), req.UserID)
	if errors.Is(err, auth.ErrNotFound) {
		writeErr(w, r, http.StatusBadRequest, msgAddonNoProfile)
		return
	}
	if err != nil {
		internalErr(w, r, "addon input user", err)
		return
	}
	if u.IsKid {
		writeErr(w, r, http.StatusBadRequest, msgAddonKidProfile)
		return
	}
	if !u.IsAdmin && !in.Manifest.ForAdults() {
		writeErr(w, r, http.StatusBadRequest, msgAddonNotForProfile)
		return
	}
	if req.ItemID != 0 && !a.inputItem(w, r, u, req.ItemID) {
		return
	}
	added, err := a.inputs.Add(in.ID, req)
	switch {
	case errors.Is(err, addons.ErrInputStarting):
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonStarting)
		return
	case errors.Is(err, addons.ErrInputNotRunning):
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonNotRunning)
		return
	case err != nil:
		addonErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, addonkit.InputCreated{ID: added.ID, ExpiresAt: added.LastUsed.Add(addons.InputTTL)})
}

// inputItem checks an input's itemId: a movie or an episode the profile
// may see (library.Repo.Visible, the rule of (*lib).item); else 400.
func (a *addonsAPI) inputItem(w http.ResponseWriter, r *http.Request, u auth.User, id int64) bool {
	if a.lib == nil {
		writeErr(w, r, http.StatusServiceUnavailable, addons.ErrNoLibrary.Error())
		return false
	}
	it, err := a.lib.Item(r.Context(), id)
	if errors.Is(err, library.ErrNotFound) {
		writeErr(w, r, http.StatusBadRequest, msgAddonNoItem)
		return false
	}
	if err != nil {
		internalErr(w, r, "addon input item", err)
		return false
	}
	visible, err := a.lib.Visible(r.Context(), u.RatingLimit(), it)
	if err != nil {
		internalErr(w, r, "addon input item access", err)
		return false
	}
	if !visible {
		writeErr(w, r, http.StatusBadRequest, msgAddonNoItem) // do not reveal hidden items
		return false
	}
	if it.Kind != library.KindMovie && it.Kind != library.KindEpisode {
		writeErr(w, r, http.StatusBadRequest, msgAddonNotVideoItem)
		return false
	}
	return true
}

// coreDropInput: DELETE /play/inputs/{id} → 204; another addon's or an
// unknown id is 404.
func (a *addonsAPI) coreDropInput(w http.ResponseWriter, r *http.Request) {
	in, ok := a.callingAddon(w, r, "play")
	if !ok {
		return
	}
	if a.inputs == nil {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonPlayOff)
		return
	}
	if !a.inputs.Drop(in.ID, chi.URLParam(r, "id")) {
		writeErr(w, r, http.StatusNotFound, msgAddonNoInput)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
