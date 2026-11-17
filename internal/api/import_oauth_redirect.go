package api

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

// serviceOwners tells which addon signs in to a music service: the one
// whose import.services lists it (*addons.Hooks).
type serviceOwners interface {
	ServiceOwner(service string) (addonID string, ok bool)
}

// GET /api/import/oauth/{service}/callback?code&state (or ?error&state):
// the address a service's sign-in sends the browser back to, registered
// in the service's console, so it never changes. The sign-in is the
// addon's: an admin's session is sent on with 302 to
// /api/addons/<id>/x/admin/import/oauth/<service>/callback with the query
// as it came, where the addon checks the state against the admin who
// began it. 404 «Сервис не подключён» when no addon lists the service;
// one that listed it but is slow or in its down period keeps it (the 302
// still goes to it), a stopped or failing one does not. The query carries a one-time code: it is not logged,
// not cached and not passed on as a referrer, and the answer has no body.
func (h *importAPI) oauthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	service := chi.URLParam(r, "service")
	addonID, ok := "", false
	if h.owners != nil {
		addonID, ok = h.owners.ServiceOwner(service)
	}
	if !ok {
		writeErr(w, r, http.StatusNotFound, "Сервис не подключён")
		return
	}
	to := "/api/addons/" + url.PathEscape(addonID) + "/x/admin/import/oauth/" + url.PathEscape(service) + "/callback"
	if r.URL.RawQuery != "" {
		to += "?" + r.URL.RawQuery
	}
	w.Header().Set("Location", to)
	w.WriteHeader(http.StatusFound)
}
