package jellyfin

import (
	"net/http"
	"strings"

	"github.com/antresol-app/antresol/internal/library"
)

// searchHint is one result of the legacy Emby search.
type searchHint struct {
	ItemId          string `json:"ItemId"`
	Id              string `json:"Id"`
	Name            string `json:"Name"`
	Type            string `json:"Type"`
	ProductionYear  int    `json:"ProductionYear,omitempty"`
	PrimaryImageTag string `json:"PrimaryImageTag,omitempty"`
	RunTimeTicks    int64  `json:"RunTimeTicks,omitempty"`
	MediaType       string `json:"MediaType,omitempty"`
}

type searchHintsResult struct {
	SearchHints      []searchHint `json:"SearchHints"`
	TotalRecordCount int          `json:"TotalRecordCount"`
}

// searchHints serves /Search/Hints (older Emby-style clients): movies and
// series by title (library.Repo.SearchKinds, the web search), within the
// profile's rating limit.
func (s *Server) searchHints(w http.ResponseWriter, r *http.Request) {
	out := searchHintsResult{SearchHints: []searchHint{}}
	term := strings.TrimSpace(qv(r, "searchTerm"))
	if term == "" {
		writeJSON(w, out)
		return
	}
	c := s.conv(r.Context(), userOf(r), nil)
	ks := []string{library.KindMovie, library.KindSeries}
	if include, named := typeSet(r, "includeItemTypes"); named {
		ks = ks[:0]
		for _, k := range []string{library.KindMovie, library.KindSeries} {
			if include[k] {
				ks = append(ks, k)
			}
		}
	}
	limit := qInt(r, "limit", 20)
	if limit <= 0 || limit > searchLimit {
		limit = 20
	}
	items, err := s.Library.SearchKinds(r.Context(), term, ks, limit, c.limit)
	if err == nil {
		items, err = c.keepVisible(items)
	}
	var dtos []BaseItemDto
	if err == nil {
		dtos, err = c.dtos(items)
	}
	if err != nil {
		s.internal(w, r, "search", err)
		return
	}
	for _, d := range dtos {
		out.SearchHints = append(out.SearchHints, searchHint{ItemId: d.Id, Id: d.Id, Name: d.Name, Type: d.Type,
			ProductionYear: d.ProductionYear, PrimaryImageTag: d.ImageTags["Primary"], RunTimeTicks: d.RunTimeTicks, MediaType: d.MediaType})
	}
	out.TotalRecordCount = len(out.SearchHints)
	writeJSON(w, out)
}
