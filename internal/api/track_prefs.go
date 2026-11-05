package api

import (
	"context"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/lang"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/go-chi/chi/v5"
)

// Audio and subtitle choices that follow the profile across devices:
//
//   - GET|PUT|DELETE /api/me/playback-prefs: the profile's defaults («Звук
//     и субтитры»); a kid's are the parents' choice, changed through
//     /api/users/{id}/playback-prefs (admin) as «Скрывать прогресс» is.
//   - PUT /api/me/playback-prefs/prefer-cleaned {"on":bool} (admin:
//     /api/users/{id}/…): «Выбирать очищенную озвучку», kept apart from the
//     defaults (switching it keeps a profile on the household's; PUT and
//     DELETE of the defaults leave it).
//   - GET /api/items/{id}/track-prefs: what the player needs before it
//     starts an item: the defaults, the tracks remembered for its series
//     and the subtitle offsets of its files.
//   - PUT /api/items/{id}/track-prefs: a track chosen in an episode, kept
//     for the series (a movie has none: nothing is kept).
//   - PUT /api/files/{fileId}/subtitle-offset: «Субтитры раньше / позже».
func mountTrackPrefs(r chi.Router, h *lib) {
	r.Get("/api/me/playback-prefs", h.myPlaybackPrefs)
	r.Put("/api/me/playback-prefs", h.putMyPlaybackPrefs)
	r.Delete("/api/me/playback-prefs", h.deleteMyPlaybackPrefs)
	r.Put("/api/me/playback-prefs/prefer-cleaned", h.putMyPreferCleaned)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/users/{id}/playback-prefs", h.userPlaybackPrefs(h.playbackPrefsOut))
		r.Put("/api/users/{id}/playback-prefs", h.userPlaybackPrefs(h.savePlaybackPrefs))
		r.Delete("/api/users/{id}/playback-prefs", h.userPlaybackPrefs(h.clearPlaybackPrefs))
		r.Put("/api/users/{id}/playback-prefs/prefer-cleaned", h.userPlaybackPrefs(h.savePreferCleaned))
	})
	r.Get("/api/items/{id}/track-prefs", h.trackPrefs)
	r.Put("/api/items/{id}/track-prefs", h.rememberTracks)
	r.Put("/api/files/{fileId}/subtitle-offset", h.setSubtitleOffset)
}

// playbackPrefs is the answer of /api/me/playback-prefs: the profile's own
// defaults (Custom) or the household's.
type playbackPrefs struct {
	library.PlaybackPrefs
	Custom bool `json:"custom"`
}

// subtitleOffsetMax: «Субтитры раньше / позже» goes no further than ±60 s
// (a track off by more is another release's, not a late one).
const subtitleOffsetMax = 60_000

// subtitleOffsetGrid: offsets are kept in 50 ms steps (the players move by 250).
const subtitleOffsetGrid = 50

// trackTitleMax: a remembered track's title is at most this many characters.
const trackTitleMax = 200

// prefsFor is uid's defaults, the household's «Языки» when it has none.
func (h *lib) prefsFor(ctx context.Context, uid int64) (playbackPrefs, error) {
	cleaned, err := h.Library.PreferCleaned(ctx, uid)
	if err != nil {
		return playbackPrefs{}, err
	}
	p, ok, err := h.Library.PlaybackPrefs(ctx, uid)
	if err != nil {
		return playbackPrefs{}, err
	}
	if ok {
		p.PreferCleaned = cleaned
		return playbackPrefs{PlaybackPrefs: p, Custom: true}, nil
	}
	langs := h.myLanguages(ctx)
	if len(langs) == 0 {
		langs = defaultLanguages
	}
	return playbackPrefs{PlaybackPrefs: library.PlaybackPrefs{
		AudioLangs: slices.Clone(langs), SubMode: "off", SubLangs: slices.Clone(langs),
		SubSize: "m", SubBackground: "translucent", PreferCleaned: cleaned,
	}}, nil
}

func (h *lib) putMyPreferCleaned(w http.ResponseWriter, r *http.Request) {
	if kidLocked(w, r) {
		return
	}
	h.savePreferCleaned(w, r, userOf(r).ID)
}

// savePreferCleaned keeps {"on":bool} and answers with the defaults.
func (h *lib) savePreferCleaned(w http.ResponseWriter, r *http.Request, uid int64) {
	var in struct {
		On *bool `json:"on"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.On == nil {
		writeErr(w, r, http.StatusBadRequest, "Неверные настройки звука и субтитров")
		return
	}
	if err := h.Library.SetPreferCleaned(r.Context(), uid, *in.On); err != nil {
		internalErr(w, r, "prefer cleaned", err)
		return
	}
	h.playbackPrefsOut(w, r, uid)
}

func (h *lib) playbackPrefsOut(w http.ResponseWriter, r *http.Request, uid int64) {
	p, err := h.prefsFor(r.Context(), uid)
	if err != nil {
		internalErr(w, r, "playback prefs", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, p)
}

func (h *lib) myPlaybackPrefs(w http.ResponseWriter, r *http.Request) {
	h.playbackPrefsOut(w, r, userOf(r).ID)
}

// kidLocked answers 403 for a kid changing its own defaults.
func kidLocked(w http.ResponseWriter, r *http.Request) bool {
	if userOf(r).IsKid {
		writeErr(w, r, http.StatusForbidden, "Это может изменить только администратор")
		return true
	}
	return false
}

func (h *lib) putMyPlaybackPrefs(w http.ResponseWriter, r *http.Request) {
	if kidLocked(w, r) {
		return
	}
	h.savePlaybackPrefs(w, r, userOf(r).ID)
}

func (h *lib) deleteMyPlaybackPrefs(w http.ResponseWriter, r *http.Request) {
	if kidLocked(w, r) {
		return
	}
	h.clearPlaybackPrefs(w, r, userOf(r).ID)
}

// validPlaybackPrefs checks p (subLangs may be empty: the audio languages).
func validPlaybackPrefs(p library.PlaybackPrefs) bool {
	return lang.Valid(p.AudioLangs, 1) && (len(p.SubLangs) == 0 || lang.Valid(p.SubLangs, 1)) &&
		slices.Contains(library.SubModes, p.SubMode) && slices.Contains(library.SubSizes, p.SubSize) &&
		slices.Contains(library.SubBackgrounds, p.SubBackground)
}

func (h *lib) savePlaybackPrefs(w http.ResponseWriter, r *http.Request, uid int64) {
	var in library.PlaybackPrefs
	if !decode(w, r, &in) {
		return
	}
	if in.SubLangs == nil {
		in.SubLangs = []string{}
	}
	if !validPlaybackPrefs(in) {
		writeErr(w, r, http.StatusBadRequest, "Неверные настройки звука и субтитров")
		return
	}
	if err := h.Library.SetPlaybackPrefs(r.Context(), uid, in); err != nil {
		internalErr(w, r, "save playback prefs", err)
		return
	}
	h.playbackPrefsOut(w, r, uid)
}

func (h *lib) clearPlaybackPrefs(w http.ResponseWriter, r *http.Request, uid int64) {
	if err := h.Library.ClearPlaybackPrefs(r.Context(), uid); err != nil {
		internalErr(w, r, "clear playback prefs", err)
		return
	}
	h.playbackPrefsOut(w, r, uid)
}

// userPlaybackPrefs runs next for the profile named by {id} (admin routes).
func (h *lib) userPlaybackPrefs(next func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, "Неверный id")
			return
		}
		if h.Users == nil {
			writeErr(w, r, http.StatusNotFound, "Профиль не найден")
			return
		}
		if _, err := h.Users.Get(r.Context(), id); err != nil {
			storeErr(w, r, err)
			return
		}
		next(w, r, id)
	}
}

// seriesOf is the series an episode, a season or a series belongs to (0: none).
func (h *lib) seriesOf(ctx context.Context, it library.Item) (int64, error) {
	switch it.Kind {
	case library.KindSeries:
		return it.ID, nil
	case library.KindSeason:
		return it.ParentID, nil
	case library.KindEpisode:
		season, err := h.Library.Item(ctx, it.ParentID)
		if err != nil {
			return 0, err
		}
		if season.Kind == library.KindSeries {
			return season.ID, nil
		}
		return season.ParentID, nil
	}
	return 0, nil
}

func (h *lib) trackPrefs(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "id")
	if !ok {
		return
	}
	ctx, uid := r.Context(), userOf(r).ID
	prefs, err := h.prefsFor(ctx, uid)
	if err != nil {
		internalErr(w, r, "playback prefs", err)
		return
	}
	out := map[string]any{"prefs": prefs, "series": nil}
	if sid, err := h.seriesOf(ctx, it); err != nil {
		internalErr(w, r, "series of item", err)
		return
	} else if sid > 0 {
		st, ok, err := h.Library.SeriesTracks(ctx, uid, sid)
		if err != nil {
			internalErr(w, r, "series tracks", err)
			return
		}
		if ok {
			out["series"] = st
		}
	}
	files, err := h.Library.FilesOf(ctx, it.ID)
	if err != nil {
		internalErr(w, r, "files of item", err)
		return
	}
	ids := make([]int64, 0, len(files))
	for _, f := range files {
		ids = append(ids, f.ID)
	}
	offs, err := h.Library.SubtitleOffsets(ctx, uid, ids)
	if err != nil {
		internalErr(w, r, "subtitle offsets", err)
		return
	}
	offsets := map[string]int{}
	for id, ms := range offs {
		offsets[strconv.FormatInt(id, 10)] = ms
	}
	out["offsets"] = offsets
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// validChoice: a language tag as the file has it ("rus", "en", "pt-BR",
// "" for none) and a title of at most trackTitleMax characters.
func validChoice(c *library.TrackChoice) bool {
	if c == nil {
		return true
	}
	c.Lang, c.Title = strings.TrimSpace(c.Lang), strings.TrimSpace(c.Title)
	return len(c.Lang) <= 16 && utf8.ValidString(c.Title) && utf8.RuneCountInString(c.Title) <= trackTitleMax &&
		slices.Contains(library.TrackKinds, c.Kind)
}

func (h *lib) rememberTracks(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Audio       *library.TrackChoice `json:"audio"`
		Subtitle    *library.TrackChoice `json:"subtitle"`
		SubtitleOff bool                 `json:"subtitleOff"`
	}
	if !decode(w, r, &in) {
		return
	}
	n := 0
	for _, set := range []bool{in.Audio != nil, in.Subtitle != nil, in.SubtitleOff} {
		if set {
			n++
		}
	}
	if n != 1 || !validChoice(in.Audio) || !validChoice(in.Subtitle) {
		writeErr(w, r, http.StatusBadRequest, "Неверный выбор дорожки")
		return
	}
	ctx, uid := r.Context(), userOf(r).ID
	sid, err := h.seriesOf(ctx, it)
	if err != nil {
		internalErr(w, r, "series of item", err)
		return
	}
	if sid > 0 {
		switch {
		case in.Audio != nil:
			err = h.Library.RememberSeriesAudio(ctx, uid, sid, *in.Audio)
		default:
			err = h.Library.RememberSeriesSubtitle(ctx, uid, sid, in.Subtitle)
		}
		if err != nil {
			internalErr(w, r, "remember tracks", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// clampSubtitleOffset keeps ms within ±subtitleOffsetMax on the 50 ms grid.
func clampSubtitleOffset(ms float64) int {
	v := int(math.Round(ms/subtitleOffsetGrid)) * subtitleOffsetGrid
	return max(-subtitleOffsetMax, min(subtitleOffsetMax, v))
}

func (h *lib) setSubtitleOffset(w http.ResponseWriter, r *http.Request) {
	f, _, ok := h.file(w, r)
	if !ok {
		return
	}
	var in struct {
		OffsetMs *float64 `json:"offsetMs"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.OffsetMs == nil || math.IsNaN(*in.OffsetMs) || math.IsInf(*in.OffsetMs, 0) {
		writeErr(w, r, http.StatusBadRequest, "Не удалось разобрать запрос")
		return
	}
	ms := clampSubtitleOffset(*in.OffsetMs)
	if err := h.Library.SetSubtitleOffset(r.Context(), userOf(r).ID, f.ID, ms); err != nil {
		internalErr(w, r, "subtitle offset", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"offsetMs": ms})
}
