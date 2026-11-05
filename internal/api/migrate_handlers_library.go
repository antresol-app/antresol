//go:build !sources

package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// importFull: the core build imports from the media servers only.
type importFull struct{}

func newImportFull(Deps) importFull { return importFull{} }

type migrateStateFull struct{}
type reportSummaryFull struct{}

func (importFull) fillState(context.Context, *migrateStateFull) error { return nil }
func (importFull) fillSummary(*reportSummary)                         {}
func (importFull) err(http.ResponseWriter, *http.Request, error) bool { return false }
func (*mediaImportAPI) mountFull(chi.Router)                          {}
