package api

import (
	"context"
	"net/http"
	"sort"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// trailerFrameSrc is the origin of the official embedded player the web
// app plays a trailer in when the server does not (appCSP's frame-src).
const trailerFrameSrc = "https://www.youtube-nocookie.com"

// TrailerSource is TMDB's videos of a title (*tmdb.Related, cached a day).
type TrailerSource interface {
	Videos(ctx context.Context, kind string, id int) ([]tmdb.Video, bool)
}

// Trailer is one of GET /api/items/{id}/trailers: a YouTube trailer or
// teaser. With play the server plays it (POST play, trailers_play.go);
// without, the apps open it in the YouTube app, or url.
type Trailer struct {
	Name     string `json:"name"`
	Site     string `json:"site"`
	Key      string `json:"key"`
	Type     string `json:"type"` // Trailer | Teaser
	Lang     string `json:"lang"`
	Official bool   `json:"official"`
	URL      string `json:"url"`
	Play     string `json:"play,omitempty"`
}

// trailers: GET /api/items/{id}/trailers, {"trailers": [...]} of a movie
// or series (a season's or episode's are its series'): the viewer's
// language first, then English, then the rest; official ones first;
// trailers before teasers. An empty list, never an error, without a TMDB
// id, a key or the network.
func (h *lib) trailers(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	out := []Trailer{}
	if it.Kind == library.KindSeason || it.Kind == library.KindEpisode {
		if s, err := h.root(ctx, it); err == nil {
			it = s
		}
	}
	kind := tmdbKindOf(it.Kind)
	if h.Trailers != nil && kind != "" && it.TmdbID > 0 {
		if vs, ok := h.Trailers.Videos(ctx, kind, int(it.TmdbID)); ok {
			out = orderTrailers(vs, i18n.FromContext(ctx))
			h.listTrailers(ctx, it, out)
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"trailers": out})
}

// orderTrailers keeps the YouTube trailers and teasers of vs in the order
// of GET /api/items/{id}/trailers for a viewer of language lang.
func orderTrailers(vs []tmdb.Video, lang string) []Trailer {
	langRank := func(l string) int {
		switch l {
		case lang:
			return 0
		case "en":
			return 1
		}
		return 2
	}
	b2i := func(b bool) int {
		if b {
			return 0
		}
		return 1
	}
	typeRank := func(t string) int {
		if t == "Trailer" {
			return 0
		}
		return 1
	}
	kept := []tmdb.Video{}
	for _, v := range vs {
		if v.Site == "YouTube" && v.Key != "" && (v.Type == "Trailer" || v.Type == "Teaser") {
			kept = append(kept, v)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if x, y := langRank(a.Lang), langRank(b.Lang); x != y {
			return x < y
		}
		if x, y := b2i(a.Official), b2i(b.Official); x != y {
			return x < y
		}
		return typeRank(a.Type) < typeRank(b.Type)
	})
	out := make([]Trailer, 0, len(kept))
	for _, v := range kept {
		out = append(out, Trailer{Name: v.Name, Site: v.Site, Key: v.Key, Type: v.Type, Lang: v.Lang, Official: v.Official,
			URL: trailerURL(v.Key)})
	}
	return out
}

// trailerURL is the page of the trailer key: the apps open it, and a
// trailer provider fetches it.
func trailerURL(key string) string { return "https://www.youtube.com/watch?v=" + key }
