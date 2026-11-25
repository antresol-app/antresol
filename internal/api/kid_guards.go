package api

import "net/http"

// Refusals of a kid profile (and of a non-admin on an admin's channel).
const (
	msgAdminOnly        = "Это может изменить только администратор"
	msgChannelAdminOnly = "Этот канал может менять только администратор"
)

// forbidKids is middleware that answers 403 msg to a kid profile. It runs
// after auth.Require.
func forbidKids(msg string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if userOf(r).IsKid {
				writeErr(w, r, http.StatusForbidden, msg)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// noKids: some routes are not available to kid profiles.
var noKids = forbidKids("Недостаточно прав")

// addonsNoKids: kids get no addon, with the reason.
var addonsNoKids = forbidKids(msgAddonKid)

// kidLocked answers 403 for a kid changing what only an admin may change
// for it (its own defaults, photo, «Скрывать мой прогресс»).
func kidLocked(w http.ResponseWriter, r *http.Request) bool {
	if userOf(r).IsKid {
		writeErr(w, r, http.StatusForbidden, msgAdminOnly)
		return true
	}
	return false
}
