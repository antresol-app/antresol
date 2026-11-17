package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/auth"
)

// msgAddonNoUser answers a /watched of a missing or unknown profile.
const msgAddonNoUser = "Нет такого профиля"

// coreWatched is GET /watched?userId&items&series (library.read): of the
// item ids (movies and other leaves) and series ids, the ones the profile
// watched, as progress.Store.WatchedOf says, {"watched": {"<id>": true}};
// at most addonMaxTMDBIDs ids per list.
func (a *addonsAPI) coreWatched(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readLibrary(w, r); !ok {
		return
	}
	if a.users == nil || a.prog == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "Профили недоступны")
		return
	}
	q := r.URL.Query()
	uid, ok := positiveID(q.Get("userId"))
	if !ok {
		writeErr(w, r, http.StatusBadRequest, msgAddonNoUser)
		return
	}
	items, ok1 := watchedIDs(w, r, q.Get("items"))
	if !ok1 {
		return
	}
	series, ok2 := watchedIDs(w, r, q.Get("series"))
	if !ok2 {
		return
	}
	if _, err := a.users.Get(r.Context(), uid); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			writeErr(w, r, http.StatusBadRequest, msgAddonNoUser)
			return
		}
		libFail(w, r, err)
		return
	}
	m, err := a.prog.WatchedOf(r.Context(), uid, items, series)
	if err != nil {
		libFail(w, r, err)
		return
	}
	out := make(map[string]bool, len(m))
	for id := range m {
		out[strconv.FormatInt(id, 10)] = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"watched": out})
}

// watchedIDs parses a comma-separated id list ("" is none); a bad id or
// more than addonMaxTMDBIDs is answered 400 and ok is false.
func watchedIDs(w http.ResponseWriter, r *http.Request, raw string) ([]int64, bool) {
	if raw == "" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	if len(parts) > addonMaxTMDBIDs {
		writeErr(w, r, http.StatusBadRequest, msgAddonTooManyIDs)
		return nil, false
	}
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		n, ok := positiveID(strings.TrimSpace(p))
		if !ok {
			badRequest(w, r)
			return nil, false
		}
		ids = append(ids, n)
	}
	return ids, true
}
