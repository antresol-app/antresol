package api

import (
	"errors"
	langpkg "github.com/antresol-app/antresol/internal/lang"
	"log"
	"net/http"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/subtitles"
	"github.com/go-chi/chi/v5"
)

// mountSubtitles: the admin's manual OpenSubtitles search for one video
// file and the download of the chosen result (saved like Auto saves).
func mountSubtitles(r chi.Router, h *lib) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/subtitles/{fileId}/search", h.subtitleSearch)
		r.Post("/api/subtitles/{fileId}", h.subtitleDownload)
	})
}

type subtitleResult struct {
	SubtitleID int    `json:"subtitleId"`
	Lang       string `json:"lang"`
	Release    string `json:"release"`
	Downloads  int    `json:"downloads"`
	HashMatch  bool   `json:"hashMatch"`
}

// subtitleFile is the video file of the request, or false with the error written.
func (h *lib) subtitleFile(w http.ResponseWriter, r *http.Request) (library.File, bool) {
	if h.Subtitles == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "Поиск субтитров не настроен: нужен ключ OpenSubtitles")
		return library.File{}, false
	}
	f, it, ok := h.file(w, r)
	if !ok {
		return f, false
	}
	if it.Kind != "movie" && it.Kind != "episode" {
		writeErr(w, r, http.StatusBadRequest, "Субтитры ищутся только для фильмов и серий")
		return f, false
	}
	return f, true
}

const quotaMsg = "Лимит загрузок OpenSubtitles на сегодня исчерпан"

func (h *lib) subtitleErr(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case errors.Is(err, subtitles.ErrQuota):
		// shared with Auto: neither asks again before the reset
		h.Subtitles.PauseUntil(h.Subtitles.QuotaReset(time.Now()))
		writeErr(w, r, http.StatusTooManyRequests, quotaMsg)
	case errors.Is(err, subtitles.ErrAuth):
		log.Printf("api: subtitles %s: %v", what, err)
		writeErr(w, r, http.StatusBadGateway, "OpenSubtitles не принял ключ или пароль. Проверьте настройки сервера")
	case errors.Is(err, subtitles.ErrOffline):
		writeErr(w, r, http.StatusBadGateway, "Не удалось связаться с OpenSubtitles. Попробуйте позже")
	default:
		log.Printf("api: subtitles %s: %v", what, err)
		writeErr(w, r, http.StatusBadGateway, "OpenSubtitles не ответил как ожидалось. Попробуйте позже")
	}
}

func (h *lib) subtitleSearch(w http.ResponseWriter, r *http.Request) {
	f, ok := h.subtitleFile(w, r)
	if !ok {
		return
	}
	lang := r.URL.Query().Get("lang")
	if !langpkg.Known(lang) {
		writeErr(w, r, http.StatusBadRequest, "Неизвестный язык субтитров")
		return
	}
	q, part, total, err := subtitles.QueryFor(r.Context(), h.Library, f, []string{lang})
	if err != nil {
		internalErr(w, r, "subtitles query", err)
		return
	}
	var subs []subtitles.Subtitle
	if q.Identifies() {
		subs, err = h.Subtitles.Search(r.Context(), q)
	}
	if err != nil {
		if r.Context().Err() == nil {
			h.subtitleErr(w, r, "search", err)
		}
		return
	}
	out := []subtitleResult{}
	for _, s := range subtitles.Ranked(subs, lang) {
		out = append(out, subtitleResult{SubtitleID: subtitles.FileFor(s, part, total), Lang: s.Lang, Release: s.Release,
			Downloads: s.Downloads, HashMatch: s.HashMatch})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

func (h *lib) subtitleDownload(w http.ResponseWriter, r *http.Request) {
	f, ok := h.subtitleFile(w, r)
	if !ok {
		return
	}
	var in struct {
		SubtitleID int    `json:"subtitleId"`
		Lang       string `json:"lang"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !langpkg.Known(in.Lang) {
		writeErr(w, r, http.StatusBadRequest, "Неизвестный язык субтитров")
		return
	}
	if in.SubtitleID <= 0 {
		writeErr(w, r, http.StatusBadRequest, "Неверный id")
		return
	}
	if h.Subtitles.QuotaUntil().After(time.Now()) {
		writeErr(w, r, http.StatusTooManyRequests, quotaMsg)
		return
	}
	data, err := h.Subtitles.Download(r.Context(), in.SubtitleID)
	if err != nil && !(errors.Is(err, subtitles.ErrQuota) && len(data) > 0) { // the day's last download still counts
		if r.Context().Err() == nil {
			h.subtitleErr(w, r, "download", err)
		}
		return
	}
	if errors.Is(err, subtitles.ErrQuota) {
		h.Subtitles.PauseUntil(h.Subtitles.QuotaReset(time.Now()))
	}
	if _, err := subtitles.WriteSidecar(f.Path, h.DataDir, f.ID, in.Lang, data); err != nil {
		internalErr(w, r, "subtitles save", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
