package jellyfin

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/library"
)

// series loads the visible series named by {id}, or answers 404.
func (s *Server) series(w http.ResponseWriter, r *http.Request, c *conv) (library.Item, bool) {
	k, id, ok := ParseID(chi.URLParam(r, "id"))
	var it library.Item
	if ok && k == kindItem {
		var err error
		it, ok, err = c.visibleItem(id)
		if err != nil {
			s.internal(w, r, "series", err)
			return it, false
		}
	}
	if !ok || it.Kind != library.KindSeries {
		w.WriteHeader(http.StatusNotFound)
		return it, false
	}
	return it, true
}

// seasons serves /Shows/{id}/Seasons.
func (s *Server) seasons(w http.ResponseWriter, r *http.Request) {
	c := s.conv(r.Context(), userOf(r), fieldsOf(r))
	sr, ok := s.series(w, r, c)
	if !ok {
		return
	}
	children, err := s.Library.Children(r.Context(), sr.ID)
	if err != nil {
		s.internal(w, r, "seasons", err)
		return
	}
	var out []library.Item
	for _, se := range children {
		if se.Kind == library.KindSeason {
			out = append(out, se)
		}
	}
	s.result(w, r, c, out)
}

// episodes serves /Shows/{id}/Episodes: one season (seasonId, or season
// = its number) or all, in order; startItemId starts the list there.
func (s *Server) episodes(w http.ResponseWriter, r *http.Request) {
	c := s.conv(r.Context(), userOf(r), fieldsOf(r))
	sr, ok := s.series(w, r, c)
	if !ok {
		return
	}
	children, err := s.Library.Children(r.Context(), sr.ID)
	if err != nil {
		s.internal(w, r, "episodes", err)
		return
	}
	_, seasonID, bySeason := ParseID(qv(r, "seasonId"))
	number, byNumber := 0, false
	if v := qv(r, "season"); v != "" {
		number, err = strconv.Atoi(v)
		byNumber = err == nil
	}
	var eps []library.Item
	for _, se := range children {
		if se.Kind != library.KindSeason || bySeason && se.ID != seasonID || byNumber && se.SeasonNo != number {
			continue
		}
		got, err := s.Library.Children(r.Context(), se.ID)
		if err != nil {
			s.internal(w, r, "episodes", err)
			return
		}
		for _, ep := range got {
			if ep.Kind == library.KindEpisode {
				eps = append(eps, ep)
			}
		}
	}
	if _, start, ok := ParseID(qv(r, "startItemId")); ok {
		for i, ep := range eps {
			if ep.ID == start {
				eps = eps[i:]
				break
			}
		}
	}
	s.result(w, r, c, eps)
}

// nextUp serves /Shows/NextUp: the episode to watch next
// (progress.NextEpisode) of each series in «Продолжить», or of seriesId.
func (s *Server) nextUp(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	c := s.conv(r.Context(), u, fieldsOf(r))
	var series []int64
	if v := qv(r, "seriesId"); v != "" {
		if k, id, ok := ParseID(v); ok && k == kindItem {
			series = append(series, id)
		}
	} else {
		entries, err := s.Progress.ContinueWatching(r.Context(), u.ID, 0)
		if err != nil {
			s.internal(w, r, "next up", err)
			return
		}
		seen := map[int64]bool{}
		for _, e := range entries {
			if e.SeriesID != 0 && !seen[e.SeriesID] {
				seen[e.SeriesID] = true
				series = append(series, e.SeriesID)
			}
		}
	}
	var items []library.Item
	for _, id := range series {
		sr, ok, err := c.visibleItem(id)
		if err != nil {
			s.internal(w, r, "next up", err)
			return
		}
		if !ok || sr.Kind != library.KindSeries {
			continue
		}
		next, ok, err := s.Progress.NextEpisode(r.Context(), u.ID, id)
		if err != nil {
			s.internal(w, r, "next up", err)
			return
		}
		if !ok {
			continue
		}
		ep, ok, err := c.visibleItem(next)
		if err != nil {
			s.internal(w, r, "next up", err)
			return
		}
		if ok {
			items = append(items, ep)
		}
	}
	s.result(w, r, c, items)
}
