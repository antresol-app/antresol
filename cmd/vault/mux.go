package main

import (
	"net/http"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/jellyfin"
)

// buildMux puts the Subsonic API (/rest/), OPDS (/opds) and the Jellyfin
// API next to the web API and the SPA. /rest, /opds and Jellyfin are
// outside the API router's csrf guard on purpose: they have no cookie
// session, every request carries its password or access token, so a
// cross-site request gains nothing; and music apps POST plain forms, which
// csrf would refuse. Jellyfin paths have known first segments in any case
// (jellyfin.IsJellyfinPath), none of which is a web app route, so they
// never shadow the SPA. A nil handler is not mounted.
func buildMux(api, subsonic, opds, jf http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	// Subsonic and OPDS clients rarely send Accept-Language: no header
	// keeps them Russian; one that does gets its language
	if subsonic != nil {
		subsonic = i18n.Middleware(subsonic)
	}
	if opds != nil {
		opds = i18n.Middleware(opds)
	}
	if subsonic != nil {
		mux.Handle("/rest/", subsonic)
	}
	if opds != nil {
		mux.Handle("/opds", opds)
		mux.Handle("/opds/", opds)
	}
	if jf == nil {
		mux.Handle("/", api)
		return mux
	}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if jellyfin.IsJellyfinPath(r.URL.Path) {
			jf.ServeHTTP(w, r)
			return
		}
		api.ServeHTTP(w, r)
	}))
	return mux
}
