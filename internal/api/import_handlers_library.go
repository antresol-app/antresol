//go:build !sources

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// playlistFull: the core build imports from files only: no sign-ins,
// no downloads.
type playlistFull struct{}

func newPlaylistFull(Deps) playlistFull { return playlistFull{} }

func (playlistFull) reader() backgroundReader                           { return nil }
func (playlistFull) err(http.ResponseWriter, *http.Request, error) bool { return false }
func (playlistFull) catalogErr(w http.ResponseWriter, r *http.Request, err error) {
	internalErr(w, r, "import search", err)
}

func (*importAPI) mountFull(chi.Router) {}
