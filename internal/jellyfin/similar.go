package jellyfin

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// SimilarSource is TMDB's titles around a title (*tmdb.Related, cached;
// the web's «Похожие» row reads the same entries).
type SimilarSource interface {
	// Language is the TMDB tag asked for (only the ids are used here).
	Language() string
	Titles(ctx context.Context, kind string, id int, tag string) (tmdb.RelatedTitles, bool)
}

// similarLimit is /Items/{id}/Similar's default and largest page.
const similarLimit = 50

// similar serves /Items/{id}/Similar: the library's movies or series that
// TMDB recommends or calls similar to the title, in TMDB's order, within
// the profile's rating. Never a title outside the library.
func (s *Server) similar(w http.ResponseWriter, r *http.Request) {
	k, id, ok := ParseID(chi.URLParam(r, "id"))
	if !ok || k != kindItem {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	c := s.conv(r.Context(), userOf(r), fieldsOf(r))
	it, ok, err := c.visibleItem(id)
	if err != nil {
		s.internal(w, r, "similar", err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	limit := qInt(r, "limit", similarLimit)
	if limit <= 0 || limit > similarLimit {
		limit = similarLimit
	}
	out := []BaseItemDto{}
	if items := c.similarItems(it, limit); len(items) > 0 {
		if out, err = c.dtos(items); err != nil {
			s.internal(w, r, "similar", err)
			return
		}
	}
	writeJSON(w, QueryResult[BaseItemDto]{Items: out, TotalRecordCount: len(out)})
}

// similarItems are the visible library items TMDB names around it.
func (c *conv) similarItems(it library.Item, limit int) []library.Item {
	kind := ""
	switch it.Kind {
	case library.KindMovie:
		kind = "movie"
	case library.KindSeries:
		kind = "tv"
	}
	src := c.s.Similar
	if src == nil || kind == "" || it.TmdbID <= 0 {
		return nil
	}
	rel, ok := src.Titles(c.ctx, kind, int(it.TmdbID), src.Language())
	if !ok || len(rel.Similar) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(rel.Similar))
	for _, ref := range rel.Similar {
		ids = append(ids, int64(ref.ID))
	}
	found, err := c.s.Library.ItemIDsByTMDB(c.ctx, it.Kind, ids)
	if err != nil {
		c.s.log().Warn("jellyfin: similar", "item", it.ID, "err", err) // degrade: none
		return nil
	}
	var out []library.Item
	for _, tid := range ids {
		lid, ok := found[tid]
		if !ok || lid == it.ID {
			continue
		}
		m, ok, err := c.visibleItem(lid)
		if err != nil || !ok {
			continue
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out
}
