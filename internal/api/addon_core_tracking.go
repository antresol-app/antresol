package api

import (
	"net/http"
	"strings"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/tracking"
	"github.com/go-chi/chi/v5"
)

// The core API's tracked titles (tracking): an addon makes or finds the
// title of a series or movie, asks for its refresh, and reads its episode
// list and the sources of every kind. The answers carry no ids of the
// list services the core syncs with.

const (
	msgAddonNoTracking = "Отслеживание недоступно"
	msgAddonNotTracked = "Тайтл не отслеживается"
	msgAddonBadKind    = "Неизвестный раздел"
	msgAddonBadTitleID = "Неверный id"
	addonTrackingBody  = 64 << 10
	trackingKindSeries = "series"
	trackingKindMovie  = "movie"
)

func mountAddonCoreTracking(r chi.Router, a *addonsAPI) {
	r.Post("/tracking/titles", a.coreEnsureTitle)
	r.Get("/tracking/titles", a.coreTitleByTMDB)
	r.Get("/tracking/titles/{id}", a.coreTitle)
	r.Post("/tracking/titles/{id}/kick", a.coreKickTitle)
	r.Get("/tracking/titles/{id}/episodes", a.coreTitleEpisodes)
	r.Get("/tracking/titles/{id}/sources", a.coreTitleSources)
}

// trackingCaller: the calling addon has the tracking permission (403
// first) and the server keeps tracked titles (503).
func (a *addonsAPI) trackingCaller(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := a.callingAddon(w, r, "tracking"); !ok {
		return false
	}
	if a.trackStore == nil {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonNoTracking)
		return false
	}
	return true
}

func trackingKindOK(kind string) bool { return kind == trackingKindSeries || kind == trackingKindMovie }

func trackingTitleOut(t tracking.Title) addonkit.TrackingTitle {
	return addonkit.TrackingTitle{ID: t.ID, Kind: t.Kind, TMDBID: t.TMDBID, IMDbID: t.IMDbID, Name: t.Name,
		Poster: t.Poster, Status: t.Status, Cert: t.Cert}
}

// coreEnsureTitle: POST {kind, tmdbId, name, poster} → the title, made
// when absent (name and poster are kept only then).
func (a *addonsAPI) coreEnsureTitle(w http.ResponseWriter, r *http.Request) {
	if !a.trackingCaller(w, r) {
		return
	}
	var in struct {
		Kind   string `json:"kind"`
		TMDBID int64  `json:"tmdbId"`
		Name   string `json:"name"`
		Poster string `json:"poster"`
	}
	if !decodeMax(w, r, &in, addonTrackingBody) {
		return
	}
	if !trackingKindOK(in.Kind) {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadKind)
		return
	}
	if in.TMDBID <= 0 {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadTitleID)
		return
	}
	t, err := a.trackStore.EnsureTitle(r.Context(), in.Kind, in.TMDBID, strings.TrimSpace(in.Name), strings.TrimSpace(in.Poster))
	if err != nil {
		internalErr(w, r, "addon tracking ensure", err)
		return
	}
	writeJSON(w, http.StatusOK, trackingTitleOut(t))
}

// coreTitleByTMDB: GET ?kind&tmdbId → the title, or 404.
func (a *addonsAPI) coreTitleByTMDB(w http.ResponseWriter, r *http.Request) {
	if !a.trackingCaller(w, r) {
		return
	}
	kind := r.URL.Query().Get("kind")
	if !trackingKindOK(kind) {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadKind)
		return
	}
	tmdbID, ok := positiveID(r.URL.Query().Get("tmdbId"))
	if !ok {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadTitleID)
		return
	}
	t, found, err := a.trackStore.TitleByTMDB(r.Context(), kind, tmdbID)
	if err != nil {
		internalErr(w, r, "addon tracking title", err)
		return
	}
	if !found {
		writeErr(w, r, http.StatusNotFound, msgAddonNotTracked)
		return
	}
	writeJSON(w, http.StatusOK, trackingTitleOut(t))
}

// trackedTitle is the title of the path's {id}: 400 for a malformed id,
// 404 for an unknown one.
func (a *addonsAPI) trackedTitle(w http.ResponseWriter, r *http.Request) (tracking.Title, bool) {
	if !a.trackingCaller(w, r) {
		return tracking.Title{}, false
	}
	id, ok := positiveID(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadTitleID)
		return tracking.Title{}, false
	}
	t, found, err := a.trackStore.TitleByID(r.Context(), id)
	if err != nil {
		internalErr(w, r, "addon tracking title", err)
		return tracking.Title{}, false
	}
	if !found {
		writeErr(w, r, http.StatusNotFound, msgAddonNotTracked)
		return tracking.Title{}, false
	}
	return t, true
}

func (a *addonsAPI) coreTitle(w http.ResponseWriter, r *http.Request) {
	if t, ok := a.trackedTitle(w, r); ok {
		writeJSON(w, http.StatusOK, trackingTitleOut(t))
	}
}

// coreKickTitle: POST → 204; the tracking service refreshes the title
// soon (its episode list and every kind's sources).
func (a *addonsAPI) coreKickTitle(w http.ResponseWriter, r *http.Request) {
	t, ok := a.trackedTitle(w, r)
	if !ok {
		return
	}
	if a.trackKick != nil {
		a.trackKick(t.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// coreTitleEpisodes: GET → [Episode] by season and number.
func (a *addonsAPI) coreTitleEpisodes(w http.ResponseWriter, r *http.Request) {
	t, ok := a.trackedTitle(w, r)
	if !ok {
		return
	}
	eps, err := a.trackStore.Episodes(r.Context(), t.ID)
	if err != nil {
		internalErr(w, r, "addon tracking episodes", err)
		return
	}
	out := make([]addonkit.TrackingEpisode, 0, len(eps))
	for _, e := range eps {
		out = append(out, addonkit.TrackingEpisode{TitleID: e.TitleID, Season: e.Season, Number: e.Number,
			Name: e.Name, AirDate: e.AirDate})
	}
	writeJSON(w, http.StatusOK, out)
}

// coreTitleSources: GET → [Source] of every kind, by season, episode,
// kind and ref.
func (a *addonsAPI) coreTitleSources(w http.ResponseWriter, r *http.Request) {
	t, ok := a.trackedTitle(w, r)
	if !ok {
		return
	}
	srcs, err := a.trackStore.Sources(r.Context(), t.ID)
	if err != nil {
		internalErr(w, r, "addon tracking sources", err)
		return
	}
	out := make([]addonkit.TrackingSource, 0, len(srcs))
	for _, s := range srcs {
		m := s.Meta
		out = append(out, addonkit.TrackingSource{TitleID: s.TitleID, Season: s.Season, Episode: s.Episode, Kind: s.Kind,
			Ref: s.Ref, Provider: s.Provider, Quality: s.Quality, Labels: nonNilStrings(s.Labels),
			Meta: addonkit.TrackingSourceMeta{ItemID: m.ItemID, FileID: m.FileID, SourceID: m.SourceID, Provider: m.Provider,
				Voice: m.Voice, Lang: m.Lang, Audio: m.Audio, Studios: m.Studios, Subs: m.Subs, FileIndex: m.FileIndex},
			FirstSeen: s.FirstSeen, LastSeen: s.LastSeen})
	}
	writeJSON(w, http.StatusOK, out)
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
