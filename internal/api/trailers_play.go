package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/trailers"
	"github.com/go-chi/chi/v5"
)

// trailerPlayWait: how long POST /api/trailers/{key}/play waits for a fetch
// before it answers 503 (the fetch goes on; the apps ask again; a var for
// tests).
var trailerPlayWait = 15 * time.Second

// trailerRetryAfter is the Retry-After of a 503 of a trailer.
const trailerRetryAfter = "2"

func mountTrailerPlay(r chi.Router, h *lib) {
	r.Post("/api/trailers/{key}/play", h.trailerPlay)
	r.Get("/api/trailers/{file}", h.trailerFile)
	r.Head("/api/trailers/{file}", h.trailerFile)
}

// trailerPath is the play URL of a trailer the server can play.
func trailerPath(key string) string { return "/api/trailers/" + key + "/play" }

// trailerKey checks a trailer key the server listed and the caller may
// see (an item whose trailers listed it is visible to the profile); 404
// otherwise, never saying which.
func (h *lib) trailerKey(w http.ResponseWriter, r *http.Request, key string) bool {
	notFound := func() bool {
		w.Header().Set("Cache-Control", "no-store")
		writeErr(w, r, http.StatusNotFound, "Трейлер не найден")
		return false
	}
	if h.TrailerCache == nil || !trailers.ValidKey(key) {
		return notFound()
	}
	ids := h.TrailerCache.ItemsOf(key)
	if len(ids) == 0 {
		return notFound()
	}
	ctx := r.Context()
	u := userOf(r)
	for _, id := range ids {
		it, err := h.Library.Item(ctx, id)
		if err != nil {
			continue
		}
		if ok, err := h.allowed(ctx, u, it); err == nil && ok {
			return true
		}
	}
	return notFound()
}

// trailerPlay: POST /api/trailers/{key}/play, {"url", "durationMs"} of a
// trailer the server listed, fetched into the cache first when it is not
// there (up to trailerPlayWait). url is a path playable without headers
// (it carries a media key when the session can have one). 503 with
// Retry-After while busy or still fetching, 502 when the fetch failed,
// 404 for a key the server did not list (or without trailer playback).
func (h *lib) trailerPlay(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if !h.trailerKey(w, r, key) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trailerPlayWait)
	defer cancel()
	_, dur, err := h.TrailerCache.Ensure(ctx, key)
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case errors.Is(err, trailers.ErrBusy), errors.Is(err, context.DeadlineExceeded):
		w.Header().Set("Retry-After", trailerRetryAfter)
		writeErr(w, r, http.StatusServiceUnavailable, "Трейлер ещё готовится, попробуйте через пару секунд")
		return
	case errors.Is(err, context.Canceled):
		return // the caller went away
	case err != nil:
		writeErr(w, r, http.StatusBadGateway, "Не удалось загрузить трейлер")
		return
	}
	h.TrailerCache.Played(key)
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

// trailerFile: GET /api/trailers/{key}.mp4, the cached trailer with Range
// (a session or a media key, like /api/stream).
func (h *lib) trailerFile(w http.ResponseWriter, r *http.Request) {
	file := chi.URLParam(r, "file")
	key, ok := strings.CutSuffix(file, ".mp4")
	if !ok {
		writeErr(w, r, http.StatusNotFound, "Трейлер не найден")
		return
	}
	if !h.trailerKey(w, r, key) {
		return
	}
	f, st, err := h.TrailerCache.Open(key)
	if errors.Is(err, trailers.ErrNotCached) {
		w.Header().Set("Cache-Control", "no-store")
		writeErr(w, r, http.StatusNotFound, "Трейлер не найден")
		return
	}
	if err != nil {
		internalErr(w, r, "trailer open", err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, key+".mp4", st.ModTime(), f)
}

// listTrailers notes the keys of the trailers of item it (only they can be
// played), gives each its play URL and prefetches the first one.
func (h *lib) listTrailers(it library.Item, ts []Trailer) {
	if h.TrailerCache == nil || len(ts) == 0 {
		return
	}
	keys := make([]string, 0, len(ts))
	for i := range ts {
		if trailers.ValidKey(ts[i].Key) {
			keys = append(keys, ts[i].Key)
			ts[i].Play = trailerPath(ts[i].Key)
		}
	}
	h.TrailerCache.Listed(it.ID, keys)
	if len(keys) > 0 {
		h.TrailerCache.Prefetch(keys[0])
	}
}
