package api

import (
	"context"
	"time"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// genreWait bounds the wait for TMDB's genre list.
const genreWait = 3 * time.Second

// GenreNamer is TMDB's genre lists (tmdb.Client.GenreNames): id → name for
// "movie" or "tv" in the lookup language of ctx.
type GenreNamer interface {
	GenreNames(ctx context.Context, kind string) (map[int]string, error)
}

// genreNames are an item's genres in the request's language: by TMDB id
// from TMDB's list in that language when the item has ids and the list is
// at hand, else the stored names (i18n.Genre maps TMDB's ru/en ones).
func (h *lib) genreNames(ctx context.Context, kind string, m itemMeta) []string {
	if h.Genres != nil && len(m.GenreIDs) > 0 && (kind == library.KindMovie || kind == library.KindSeries) {
		list := "movie"
		if kind == library.KindSeries {
			list = "tv"
		}
		// bounded: TMDB unreachable must not hold the page (the stored
		// names below serve then)
		gctx, cancel := context.WithTimeout(ctx, genreWait)
		names, _ := h.Genres.GenreNames(tmdb.WithLanguage(gctx, i18n.TMDB(i18n.FromContext(ctx))), list)
		cancel()
		out := make([]string, 0, len(m.GenreIDs))
		for _, id := range m.GenreIDs {
			if n, ok := names[id]; ok {
				out = append(out, n)
			}
		}
		if len(out) == len(m.GenreIDs) {
			return out
		}
	}
	if m.Genres == nil {
		return []string{}
	}
	return i18n.Genres(ctx, m.Genres)
}

func firstN(s []string, n int) []string { return s[:min(n, len(s))] }
