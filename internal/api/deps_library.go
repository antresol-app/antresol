//go:build !sources

package api

import (
	"context"
	"net/http"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/go-chi/chi/v5"
)

// libraryEdition: me.libraryOnly is always true.
const libraryEdition = true

// fullDeps: the core build serves its library only; the sources build's
// other features are not compiled in.
type fullDeps struct{}

func mountFull(chi.Router, Deps, *auth.Guard, *lib) {}

// fullUploadPath: the core build has no multipart uploads beyond fileUpload's.
func fullUploadPath(string) bool { return false }

type meCaps struct{}

// meExtra: /api/me has no capability fields beyond libraryOnly.
type meExtra struct{}

func newMeCaps(Deps) meCaps                          { return meCaps{} }
func (meCaps) of(context.Context, auth.User) meExtra { return meExtra{} }

type statsFull struct{}

func newStatsFull(Deps) statsFull { return statsFull{} }

// statsOverviewFull: /api/stats has the library's blocks only.
type statsOverviewFull struct{}

func (s *stats) fillOverview(*statsOverviewFull) {}
func fileOpConflict(*http.Request, error) string { return "Что-то пошло не так" }
