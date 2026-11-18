package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/version"
	"github.com/go-chi/chi/v5"
)

// coreUpdate is GET /api/updates (admin), the core's row of «Управление →
// Обновления»: the version that runs here and the latest public release.
// The server cannot update itself: in a container the image is replaced
// from outside (docker compose pull), and the binary has no rights over its
// own container. So the page shows the steps, never a button (the addons'
// rows update through /api/addons/{id}/update as «Расширения» does).
type coreUpdate struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	// Latest is the latest release's tag ("v0.9.0"); "" when the check
	// failed or this server has none (Checked says which).
	Latest     string `json:"latest"`
	ReleaseURL string `json:"releaseUrl,omitempty"`
	Checked    bool   `json:"checked"`
	// Newer: Latest is a newer version than Version (never for a "dev" build).
	Newer bool `json:"newer"`
	// Docker: the server runs in a container (the steps are Compose's).
	Docker bool `json:"docker"`
}

// inDocker reports whether the process runs in a Docker container.
var inDocker = func() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

func mountUpdates(r chi.Router, latest func(context.Context) (tag, page string, err error)) {
	r.With(auth.RequireAdmin).Get("/api/updates", func(w http.ResponseWriter, r *http.Request) {
		out := coreUpdate{Version: version.Version, Commit: version.BuildCommit(), Docker: inDocker()}
		if latest != nil {
			if tag, page, err := latest(r.Context()); err == nil {
				out.Latest, out.ReleaseURL, out.Checked = tag, page, true
				out.Newer = addons.VersionLess(out.Version, tag)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(out)
	})
}
