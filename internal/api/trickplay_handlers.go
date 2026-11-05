package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/trickplay"
	"github.com/go-chi/chi/v5"
)

// Trickplay is the part of *trickplay.Worker the API uses: the scrub
// previews of a file (made in the background) and the play that puts a
// file first in their queue.
type Trickplay interface {
	Index(path string, size, mtime int64) (trickplay.Index, bool)
	Sheet(path string, size, mtime int64, n int) (string, bool)
	Want(fileID int64)
}

// msgNoPreviews: a file's scrub previews are not made yet (or never will be).
const msgNoPreviews = "Превью для перемотки ещё не готовы"

// mountTrickplay: the scrub previews of a library video (visible items
// only, like every file route). They read generated images, never the
// media: not playback for the disk's quiet (cmd/vault/playback.go).
//
//   - GET /api/files/{fileId}/trickplay: the index (trickplay.Index), 404
//     until made (the request queues the file);
//   - GET /api/files/{fileId}/trickplay/{n}.jpg: sheet n; the index's key
//     goes in ?v=, so a sheet is cached for good.
func mountTrickplay(r chi.Router, h *lib) {
	r.Get("/api/files/{fileId}/trickplay", h.trickplayIndex)
	r.Get("/api/files/{fileId}/trickplay/{sheet}", h.trickplaySheet)
}

func (h *lib) trickplayFile(w http.ResponseWriter, r *http.Request) (library.File, bool) {
	f, it, ok := h.file(w, r)
	if !ok {
		return f, false
	}
	if h.Trickplay == nil || (it.Kind != library.KindMovie && it.Kind != library.KindEpisode) {
		writeErr(w, r, http.StatusNotFound, msgNoPreviews)
		return f, false
	}
	return f, true
}

func (h *lib) trickplayIndex(w http.ResponseWriter, r *http.Request) {
	f, ok := h.trickplayFile(w, r)
	if !ok {
		return
	}
	ix, ok := h.Trickplay.Index(f.Path, f.Size, f.Mtime)
	if !ok {
		// asked for: made next (a part of a film in parts, another version,
		// one the budget left out)
		h.Trickplay.Want(f.ID)
		w.Header().Set("Cache-Control", "no-store")
		writeErr(w, r, http.StatusNotFound, msgNoPreviews)
		return
	}
	// made once, but a replaced file gets new ones: asked again each play
	w.Header().Set("Cache-Control", "private, no-cache")
	writeJSON(w, http.StatusOK, ix)
}

func (h *lib) trickplaySheet(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutSuffix(chi.URLParam(r, "sheet"), ".jpg")
	n, err := strconv.Atoi(name)
	if !ok || err != nil || n < 0 {
		writeErr(w, r, http.StatusBadRequest, "Неверный id")
		return
	}
	f, ok := h.trickplayFile(w, r)
	if !ok {
		return
	}
	// a sheet asked for by an older index (?v= of a replaced file's) is
	// not today's: it must not be cached under that URL
	ix, ok := h.Trickplay.Index(f.Path, f.Size, f.Mtime)
	if v := r.URL.Query().Get("v"); ok && v != "" && v != ix.Key {
		ok = false
	}
	var p string
	if ok {
		p, ok = h.Trickplay.Sheet(f.Path, f.Size, f.Mtime, n)
	}
	if !ok {
		writeErr(w, r, http.StatusNotFound, msgNoPreviews)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache") // no key: the file may be replaced
	}
	http.ServeFile(w, r, p)
}
