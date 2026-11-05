package api

import (
	"context"
	"strings"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// Translator is TMDB's text of library titles in the viewer's language
// (tmdb.Translations): the scan stores titles, overviews, cast and
// episodes in the metadata language only.
type Translator interface {
	// Language is the TMDB tag of the stored text (the metadata language).
	Language() string
	// Peek is the cached text, never asking TMDB (cards).
	Peek(ctx context.Context, kind string, id int, tag string) (tmdb.Translation, bool)
	// Get waits a short while for TMDB when the cache lacks the text or
	// the seasons (item pages); false: none, the stored text stays.
	Get(ctx context.Context, kind string, id int, tag string, seasons []int) (tmdb.Translation, bool)
}

// translationTarget is the TMDB kind and the language a title's text is
// wanted in: ok false when the item is not a TMDB title or its stored text
// already is in the viewer's language.
func (h *lib) translationTarget(ctx context.Context, it library.Item, m itemMeta) (kind, tag string, ok bool) {
	if h.Translations == nil || it.TmdbID <= 0 {
		return "", "", false
	}
	switch it.Kind {
	case library.KindMovie:
		kind = "movie"
	case library.KindSeries:
		kind = "tv"
	default:
		return "", "", false
	}
	tag = i18n.TMDB(i18n.FromContext(ctx))
	stored := m.TmdbLang
	if stored == "" {
		stored = h.Translations.Language()
	}
	if primaryLang(stored) == primaryLang(tag) {
		return "", "", false
	}
	return kind, tag, true
}

func primaryLang(tag string) string {
	p, _, _ := strings.Cut(strings.ToLower(tag), "-")
	return p
}

// translation is a movie's or series' text in the viewer's language for
// its page (the seasons' episodes too), fetched now if need be; nil: show
// the stored text.
func (h *lib) translation(ctx context.Context, it library.Item, seasons []int) *tmdb.Translation {
	kind, tag, ok := h.translationTarget(ctx, it, metaOf(it))
	if !ok {
		return nil
	}
	t, ok := h.Translations.Get(ctx, kind, int(it.TmdbID), tag, seasons)
	if !ok {
		return nil
	}
	return &t
}

// cachedTranslation is translation for cards: only what is cached.
func (h *lib) cachedTranslation(ctx context.Context, it library.Item, m itemMeta) *tmdb.Translation {
	kind, tag, ok := h.translationTarget(ctx, it, m)
	if !ok {
		return nil
	}
	t, ok := h.Translations.Peek(ctx, kind, int(it.TmdbID), tag)
	if !ok {
		return nil
	}
	return &t
}

// translatedTitle is the title in t, else stored.
func translatedTitle(t *tmdb.Translation, stored string) string {
	if t != nil && t.Title != "" {
		return t.Title
	}
	return stored
}

// translatedEpisode is an episode's name and overview in t, else the
// stored ones.
func translatedEpisode(t *tmdb.Translation, ep library.Item) (title, overview string) {
	title, overview = ep.Title, ep.Overview
	if t == nil {
		return
	}
	if e, ok := t.Episode(ep.SeasonNo, ep.EpisodeNo); ok {
		if e.Name != "" {
			title = e.Name
		}
		if e.Overview != "" {
			overview = e.Overview
		}
	}
	return
}

// pageTranslation is translation for the page of it: a movie, a series
// (with its seasons), or a season or episode of a series (with its own
// season); nil: none.
func (h *lib) pageTranslation(ctx context.Context, it library.Item, seasons []library.Item) *tmdb.Translation {
	if h.Translations == nil {
		return nil
	}
	root := it
	var nums []int
	switch it.Kind {
	case library.KindMovie:
	case library.KindSeries:
		for _, s := range seasons {
			nums = append(nums, s.SeasonNo)
		}
	case library.KindSeason, library.KindEpisode:
		s, err := h.root(ctx, it)
		if err != nil || s.Kind != library.KindSeries {
			return nil
		}
		root, nums = s, []int{it.SeasonNo}
	default:
		return nil
	}
	return h.translation(ctx, root, nums)
}
