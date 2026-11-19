package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/channels"
	"github.com/go-chi/chi/v5"
)

// Channels with fetching (channels.Rules.Fetch), the admin's alone:
//
//	GET /api/channels/{id}/fetch             the channel's disk use, budgets and programmes (channels.FetchStatus)
//	PUT /api/channels/fetch/budget           {globalGB}: the disk every channel's fetches may take together (0: the default)
//	GET /api/channels/fetch/search?q=        movies and series to add (TMDB)
//	GET /api/channels/fetch/seasons?tmdbId=  a series' seasons
const msgFetchAdmin = "Каналы с докачкой настраивает администратор"

func mountChannelsFetch(r chi.Router, a *channelsAPI) {
	r.Get("/api/channels/{id}/fetch", a.fetchStatus)
	r.Put("/api/channels/fetch/budget", a.fetchBudget)
	r.Get("/api/channels/fetch/search", a.fetchSearch)
	r.Get("/api/channels/fetch/seasons", a.fetchSeasons)
}

func (a *channelsAPI) fetchAdmin(w http.ResponseWriter, r *http.Request) bool {
	if u := userOf(r); !u.IsAdmin || u.IsKid {
		writeErr(w, r, http.StatusForbidden, msgFetchAdmin)
		return false
	}
	return true
}

func (a *channelsAPI) fetchStatus(w http.ResponseWriter, r *http.Request) {
	if !a.fetchAdmin(w, r) {
		return
	}
	c, ok := a.channelParam(w, r)
	if !ok {
		return
	}
	st, err := a.svc.FetchStatusOf(r.Context(), c)
	if err != nil {
		internalErr(w, r, "channel fetch", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *channelsAPI) fetchBudget(w http.ResponseWriter, r *http.Request) {
	if !a.fetchAdmin(w, r) {
		return
	}
	var b struct {
		GlobalGB float64 `json:"globalGB"`
	}
	if !decode(w, r, &b) {
		return
	}
	if err := a.svc.Store.SetFetchGlobalGB(r.Context(), b.GlobalGB); err != nil {
		a.storeErr(w, r, err)
		return
	}
	a.svc.FetchKick()
	writeJSON(w, http.StatusOK, map[string]float64{"globalGB": a.svc.Store.FetchGlobalGB(r.Context())})
}

func (a *channelsAPI) fetchSearch(w http.ResponseWriter, r *http.Request) {
	if !a.fetchAdmin(w, r) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if a.svc.FetchCatalog == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "TMDB сейчас недоступен. Попробуйте позже")
		return
	}
	if q == "" {
		writeJSON(w, http.StatusOK, []channels.FetchFound{})
		return
	}
	found, err := a.svc.FetchCatalog.Search(liveTMDB(r), q)
	if err != nil {
		if r.Context().Err() == nil {
			log.Printf("api: channel fetch search: %v", err)
		}
		writeErr(w, r, http.StatusServiceUnavailable, "TMDB сейчас недоступен. Попробуйте позже")
		return
	}
	writeJSON(w, http.StatusOK, found)
}

func (a *channelsAPI) fetchSeasons(w http.ResponseWriter, r *http.Request) {
	if !a.fetchAdmin(w, r) {
		return
	}
	id, err := strconv.Atoi(r.URL.Query().Get("tmdbId"))
	if err != nil || id <= 0 || a.svc.FetchCatalog == nil {
		writeErr(w, r, http.StatusBadRequest, "Неизвестный фильм или сериал в составе канала")
		return
	}
	ss, err := a.svc.FetchCatalog.Seasons(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusServiceUnavailable, "TMDB сейчас недоступен. Попробуйте позже")
		return
	}
	if ss == nil {
		ss = []int{}
	}
	writeJSON(w, http.StatusOK, ss)
}
