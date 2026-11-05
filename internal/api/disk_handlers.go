package api

import (
	"log"
	"net/http"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/disk"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/go-chi/chi/v5"
)

// mountDisk: GET /api/disk (admin) says how full the media disk is, or why
// it is away.
func mountDisk(r chi.Router, usage func() (disk.Usage, error)) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/disk", diskHandler(usage))
	})
}

func diskHandler(usage func() (disk.Usage, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := usage()
		if err != nil {
			log.Printf("api: disk usage: %v", err)
			writeErr(w, r, http.StatusServiceUnavailable, "Не удалось узнать, сколько места на диске")
			return
		}
		u.Offline = i18n.Localize(r.Context(), u.Offline)
		writeJSON(w, http.StatusOK, u)
	}
}
