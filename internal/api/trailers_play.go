package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/trailers"
	"github.com/go-chi/chi/v5"
)

// trailerPlayWait: how long POST /api/trailers/{key}/play waits for a
// trailer to be ready before it answers 503 (the fetch goes on; the apps
// ask again; a var for tests).
var trailerPlayWait = 15 * time.Second

// trailerRetryAfter is the Retry-After of a 503 of a trailer.
const trailerRetryAfter = "2"

func mountTrailerPlay(r chi.Router, h *lib) {
	r.Post("/api/trailers/{key}/play", h.trailerPlay)
	r.Get("/api/trailers/{file}", h.trailerFile)
	r.Head("/api/trailers/{file}", h.trailerFile)
}

// trailerKeysFile keeps the listed keys across restarts ("" without a
// data directory: memory only).
func trailerKeysFile(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "trailers", "listed.json")
}

// trailerPath is the play URL of a trailer the server can play.
func trailerPath(key string) string { return "/api/trailers/" + key + "/play" }

// trailerProvider is who plays trailers now: an addon that declares
// trailers.fetch, else the build's own player; nil: none.
func (h *lib) trailerProvider(ctx context.Context) trailers.Provider {
	if h.TrailerAddons != nil && h.TrailerAddons.Available(ctx) {
		return h.TrailerAddons
	}
	if h.TrailerLocal != nil && h.TrailerLocal.Available(ctx) {
		return h.TrailerLocal
	}
	return nil
}

// trailerKey checks a trailer key the server listed and the caller may
// see (an item whose trailers listed it is visible to the profile): the
// provider to play it with, or false after a 404 that never says which
// check failed (no provider included).
func (h *lib) trailerKey(w http.ResponseWriter, r *http.Request, key string) (trailers.Provider, bool) {
	notFound := func() (trailers.Provider, bool) {
		trailerNotFound(w, r)
		return nil, false
	}
	if h.trailerKeys == nil || !trailers.ValidKey(key) {
		return notFound()
	}
	ids := h.trailerKeys.ItemsOf(key)
	if len(ids) == 0 {
		return notFound()
	}
	ctx := r.Context()
	u := userOf(r)
	visible := false
	for _, id := range ids {
		it, err := h.Library.Item(ctx, id)
		if err != nil {
			continue
		}
		if ok, err := h.allowed(ctx, u, it); err == nil && ok {
			visible = true
			break
		}
	}
	if !visible {
		return notFound()
	}
	p := h.trailerProvider(ctx)
	if p == nil {
		return notFound()
	}
	return p, true
}

func trailerNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeErr(w, r, http.StatusNotFound, "Трейлер не найден")
}

// trailerPlay: POST /api/trailers/{key}/play, {"url", "durationMs"} of a
// trailer the server listed, got ready by the provider first (up to
// trailerPlayWait). url is a path playable without headers (it carries a
// media key when the session can have one). 503 with Retry-After while
// still fetching, 502 when the fetch failed or was refused (its text),
// 404 for a key the server did not list, one the profile may not see, or
// without a provider.
func (h *lib) trailerPlay(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	p, ok := h.trailerKey(w, r, key)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trailerPlayWait)
	defer cancel()
	dur, err := p.Play(ctx, key, trailerURL(key))
	w.Header().Set("Cache-Control", "no-store")
	var ref *trailers.Refusal
	switch {
	case errors.Is(err, trailers.ErrPending):
		w.Header().Set("Retry-After", trailerRetryAfter)
		writeErr(w, r, http.StatusServiceUnavailable, "Трейлер ещё готовится, попробуйте через пару секунд")
		return
	case r.Context().Err() != nil:
		return // the caller went away
	case errors.Is(err, trailers.ErrNotFound):
		trailerNotFound(w, r)
		return
	case errors.As(err, &ref):
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": ref.Message})
		return
	case err != nil:
		writeErr(w, r, http.StatusBadGateway, "Не удалось загрузить трейлер")
		return
	}
	u := "/api/trailers/" + key + ".mp4"
	if mk := h.trailerMediaKey(r); mk != "" {
		u += "?mk=" + url.QueryEscape(mk)
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": u, "durationMs": dur})
}

// trailerMediaKey is a media key of the caller's session ("" without
// media keys or a session token).
func (h *lib) trailerMediaKey(r *http.Request) string {
	if h.MediaKeys == nil {
		return ""
	}
	sess, ok := auth.SessionFrom(r.Context())
	tok := auth.TokenFrom(r.Context())
	if !ok || tok == "" {
		return ""
	}
	ttl := mediaKeyTTL
	if sess.Kind == auth.KindWeb {
		ttl = webMediaKeyTTL
	}
	key, _ := h.MediaKeys.Issue(sess, tok, ttl)
	return key
}

// trailerFile: GET|HEAD /api/trailers/{key}.mp4, the trailer with Range
// from its provider (a session or a media key, like /api/stream).
func (h *lib) trailerFile(w http.ResponseWriter, r *http.Request) {
	key, ok := strings.CutSuffix(chi.URLParam(r, "file"), ".mp4")
	if !ok {
		trailerNotFound(w, r)
		return
	}
	p, ok := h.trailerKey(w, r, key)
	if !ok {
		return
	}
	err := p.Serve(w, r, key, trailerURL(key))
	switch {
	case err == nil:
	case errors.Is(err, trailers.ErrNotFound):
		trailerNotFound(w, r)
	case r.Context().Err() != nil:
	default:
		log.Printf("api: trailer %s: %v", key, err)
		w.Header().Set("Cache-Control", "no-store")
		writeErr(w, r, http.StatusBadGateway, "Не удалось загрузить трейлер")
	}
}

// listTrailers notes the keys of the trailers of item it (only they can be
// played) and, while a provider plays trailers, gives each its play URL
// and prefetches the first one.
func (h *lib) listTrailers(ctx context.Context, it library.Item, ts []Trailer) {
	if h.trailerKeys == nil || len(ts) == 0 {
		return
	}
	keys := make([]string, 0, len(ts))
	for i := range ts {
		if trailers.ValidKey(ts[i].Key) {
			keys = append(keys, ts[i].Key)
		}
	}
	h.trailerKeys.Add(it.ID, keys)
	p := h.trailerProvider(ctx)
	if p == nil || len(keys) == 0 {
		return
	}
	for i := range ts {
		if trailers.ValidKey(ts[i].Key) {
			ts[i].Play = trailerPath(ts[i].Key)
		}
	}
	p.Prefetch(keys[0], trailerURL(keys[0]))
}
