package api

import (
	"errors"
	"net/http"

	"github.com/antresol-app/antresol/internal/files"
	"github.com/go-chi/chi/v5"
)

// mountStatsDisk: «Медиадиск» of «Статистика» opens a category into what it
// is made of (admin; the caller's group requires it).
func mountStatsDisk(r chi.Router, s *stats) {
	r.Get("/api/stats/disk/{category}", s.diskBreakdown)
}

// diskBreakdown lists a disk category's entries, largest first. They come
// with the cached usage report; «other» is walked in the background and
// answers pending until it is done (the page polls).
func (s *stats) diskBreakdown(w http.ResponseWriter, r *http.Request) {
	if s.files == nil {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	}
	b, err := s.files.Usage.Breakdown(r.Context(), chi.URLParam(r, "category"))
	if errors.Is(err, files.ErrUnknownRoot) {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	}
	if err != nil {
		internalErr(w, r, "disk breakdown", err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}
