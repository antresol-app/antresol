package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/go-chi/chi/v5"
)

// The core API's watch progress (progress): an addon that plays titles
// itself reads and writes a profile's position in a library movie or
// episode, as the players' own reports do (progress.Store).

const (
	msgAddonProgressFuture = "Время позиции опережает часы сервера больше чем на 5 минут"

	addonProgressBody = 64 << 10
	// addonProgressAhead is how far ahead of the server's clock a write's
	// time may run (another device's clock); the store keeps such a time as
	// now.
	addonProgressAhead = 5 * time.Minute
)

func mountAddonCoreProgress(r chi.Router, a *addonsAPI) {
	r.Get("/progress", a.coreProgress)
	r.Put("/progress", a.coreSaveProgress)
}

// progressCaller: the calling addon has the progress permission (403
// first) and the server has profiles, progress and a library (503).
func (a *addonsAPI) progressCaller(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := a.callingAddon(w, r, "progress"); !ok {
		return false
	}
	if a.users == nil || a.prog == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "Профили недоступны")
		return false
	}
	if a.lib == nil {
		writeErr(w, r, http.StatusServiceUnavailable, addons.ErrNoLibrary.Error())
		return false
	}
	return true
}

// progressUser is the profile id names: an adult one. A missing or unknown
// id is 400 «Нет такого профиля», a kid 400 «Детскому профилю это
// недоступно»; ok is false then, the answer is written.
func (a *addonsAPI) progressUser(w http.ResponseWriter, r *http.Request, id int64) (auth.User, bool) {
	if id <= 0 {
		writeErr(w, r, http.StatusBadRequest, msgAddonNoUser)
		return auth.User{}, false
	}
	u, err := a.users.Get(r.Context(), id)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		writeErr(w, r, http.StatusBadRequest, msgAddonNoUser)
		return u, false
	case err != nil:
		libFail(w, r, err)
		return u, false
	case u.IsKid:
		writeErr(w, r, http.StatusBadRequest, msgAddonKidProfile)
		return u, false
	}
	return u, true
}

// progressItem: what profile u may do with item id.
type progressItem int

const (
	itemOpen     progressItem = iota // a movie or an episode u may see
	itemHidden                       // there is no such item, or it is over u's rating limit
	itemNotVideo                     // u may see it, but it is no movie or episode
)

// progressAccess classifies item id for profile u by the one visibility
// rule (library.Repo.Visible): an item over the limit reads as one that
// does not exist, whatever its kind.
func (a *addonsAPI) progressAccess(r *http.Request, u auth.User, id int64) (progressItem, error) {
	it, err := a.lib.Item(r.Context(), id)
	if errors.Is(err, library.ErrNotFound) {
		return itemHidden, nil
	}
	if err != nil {
		return itemHidden, err
	}
	visible, err := a.lib.Visible(r.Context(), u.RatingLimit(), it)
	switch {
	case err != nil:
		return itemHidden, err
	case !visible:
		return itemHidden, nil
	case it.Kind != library.KindMovie && it.Kind != library.KindEpisode:
		return itemNotVideo, nil
	}
	return itemOpen, nil
}

// coreProgress is GET /progress?userId&items (progress): of the item ids
// (at most 500), the profile's progress in the movies and episodes it may
// see, {"progress": {"<id>": ProgressEntry}}. An id with no progress, or
// that the profile may not see (over its rating limit, or no movie or
// episode), is left out: the answer says nothing of what was watched there.
func (a *addonsAPI) coreProgress(w http.ResponseWriter, r *http.Request) {
	if !a.progressCaller(w, r) {
		return
	}
	q := r.URL.Query()
	uid, _ := positiveID(q.Get("userId"))
	items, ok := watchedIDs(w, r, q.Get("items"))
	if !ok {
		return
	}
	u, ok := a.progressUser(w, r, uid)
	if !ok {
		return
	}
	out := make(map[string]addonkit.ProgressEntry, len(items))
	for _, id := range items {
		key := strconv.FormatInt(id, 10)
		if _, seen := out[key]; seen {
			continue
		}
		e, err := a.prog.Get(r.Context(), u.ID, id)
		if errors.Is(err, progress.ErrNotFound) {
			continue
		}
		if err != nil {
			libFail(w, r, err)
			return
		}
		access, err := a.progressAccess(r, u, id)
		if err != nil {
			libFail(w, r, err)
			return
		}
		if access == itemOpen {
			out[key] = addonkit.ProgressEntry{PositionMs: e.PositionMs, DurationMs: e.DurationMs, Watched: e.Finished, UpdatedAt: e.UpdatedAt}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"progress": out})
}

// coreSaveProgress is PUT /progress {userId, itemId, positionMs,
// durationMs, at, played} → 204 (progress): the profile's position in a
// movie or an episode it may see, with the store's rules: a position
// older than the one kept is ignored, one from 92% of the duration is
// watched; played also adds the time played to the profile's watch time.
// A time more than 5 minutes ahead of the server's clock is 400 (one a
// little ahead is kept as now), no time means now.
func (a *addonsAPI) coreSaveProgress(w http.ResponseWriter, r *http.Request) {
	if !a.progressCaller(w, r) {
		return
	}
	var in addonkit.ProgressSave
	if !decodeMax(w, r, &in, addonProgressBody) {
		return
	}
	if in.UserID <= 0 {
		writeErr(w, r, http.StatusBadRequest, msgAddonNoUser)
		return
	}
	if in.ItemID <= 0 || in.PositionMs < 0 || in.DurationMs < 0 {
		badRequest(w, r)
		return
	}
	if in.At.After(time.Now().Add(addonProgressAhead)) {
		writeErr(w, r, http.StatusBadRequest, msgAddonProgressFuture)
		return
	}
	u, ok := a.progressUser(w, r, in.UserID)
	if !ok {
		return
	}
	access, err := a.progressAccess(r, u, in.ItemID)
	switch {
	case err != nil:
		libFail(w, r, err)
		return
	case access == itemHidden:
		writeErr(w, r, http.StatusNotFound, msgAddonNoItem)
		return
	case access == itemNotVideo:
		writeErr(w, r, http.StatusBadRequest, msgAddonNotVideoItem)
		return
	}
	if in.Played {
		err = a.prog.PlayedAt(r.Context(), u.ID, in.ItemID, in.PositionMs, in.DurationMs, in.At)
	} else {
		err = a.prog.SaveAt(r.Context(), u.ID, in.ItemID, in.PositionMs, in.DurationMs, in.At)
	}
	if err != nil { // a stale write is not an error: it is ignored
		libFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
