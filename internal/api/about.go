package api

import (
	"encoding/json"
	"net/http"

	antresol "github.com/antresol-app/antresol"
	"github.com/antresol-app/antresol/internal/version"
	"github.com/go-chi/chi/v5"
)

// aboutInfo is GET /api/about: what runs here and where its source is
// (AGPL §13). sourceUrl is left out when the build offers no link.
type aboutInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	License   string `json:"license"`
	SourceURL string `json:"sourceUrl,omitempty"`
}

// mountAbout serves the source offer and the licence texts. Public: no
// session, so the sign-in screens can show the link too.
func mountAbout(r chi.Router) {
	info := aboutInfo{Version: version.Version, Commit: version.BuildCommit(), License: antresol.SPDX, SourceURL: version.SourceLink()}
	r.Get("/api/about", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		json.NewEncoder(w).Encode(info)
	})
	text := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(body))
		}
	}
	r.Get("/api/about/license", text(antresol.License))
	r.Get("/api/about/third-party", text(antresol.ThirdPartyLicenses))
}
