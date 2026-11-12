package api

import (
	"context"
	"slices"
	"time"

	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// The TV guide's text (GET /api/channels, …/{id}/guide, …/epg and the IPTV
// guide) in the viewer's language: TMDB's titles, episode names and
// overviews (Deps.Translations) from the cache only, which guideWarmer
// fills ahead for what airs; the stored text when none is cached (no TMDB
// key, TMDB has none or is down). Titles outside the library keep their
// own text.

// programTranslation is the cached TMDB text of it (a movie or a series)
// in the viewer's language, also when that is the metadata language: the
// scan keeps a title from the file's name or a .nfo, often the original
// (English) one, beside text in the metadata language, so the guide always
// takes TMDB's. nil: none cached.
func (h *lib) programTranslation(ctx context.Context, it library.Item) *tmdb.Translation {
	if h == nil || h.Translations == nil {
		return nil
	}
	kind, ok := tmdbKind(it)
	if !ok {
		return nil
	}
	t, ok := h.Translations.Peek(ctx, kind, int(it.TmdbID), i18n.TMDB(i18n.FromContext(ctx)))
	if !ok {
		return nil
	}
	return &t
}

// localizeProgram puts x's title, episode name and overview in the
// viewer's language where TMDB's text is cached (it: x.ArtItem).
func (h *lib) localizeProgram(ctx context.Context, x *channels.Info, it library.Item) {
	t := h.programTranslation(ctx, it)
	if t == nil {
		return
	}
	x.Title = translatedTitle(t, x.Title)
	if x.Kind == library.KindEpisode {
		x.Episode, x.Overview = translatedEpisode(t, library.Item{Title: x.Episode, Overview: x.Overview, SeasonNo: x.Season, EpisodeNo: x.Number})
	} else if t.Overview != "" {
		x.Overview = t.Overview
	}
}

// guideLanguage is the language the IPTV guide of profile uid is written
// in: its interface language (GET /api/me/ui-language), else the default.
func (a *channelsAPI) guideLanguage(ctx context.Context, uid int64) string {
	if a.cards != nil {
		if _, eff, err := a.cards.effectiveUILanguage(ctx, uid); err == nil && eff != "" {
			return eff
		}
	}
	return i18n.Default
}

// warmPause spaces the warmer's TMDB fetches (a var: tests).
var warmPause = time.Second

// guideWarmer fetches ahead, in every interface language, the TMDB text of
// the movies and series airing on the channels (with the seasons of the
// episodes scheduled), so the guide's Peek finds it (channels.WarmFunc).
type guideWarmer struct {
	h   *lib
	svc *channels.Service
}

func (w *guideWarmer) warm(ctx context.Context, items []int64) {
	tr := w.h.Translations
	if tr == nil {
		return
	}
	info, err := w.svc.Describe(ctx, items)
	if err != nil {
		return
	}
	type title struct {
		kind    string
		id      int
		seasons []int
	}
	var order []int64
	titles := map[int64]*title{}
	for _, id := range items { // in airing order: the soonest first
		x, ok := info[id]
		if !ok || x.External {
			continue
		}
		t, seen := titles[x.ArtItem]
		if !seen {
			if it, err := w.h.Library.Item(ctx, x.ArtItem); err == nil {
				if kind, ok := tmdbKind(it); ok {
					t = &title{kind: kind, id: int(it.TmdbID)}
					order = append(order, x.ArtItem)
				}
			}
			titles[x.ArtItem] = t // nil: not a TMDB title
		}
		if t != nil && t.kind == "tv" && x.Season > 0 && !slices.Contains(t.seasons, x.Season) {
			t.seasons = append(t.seasons, x.Season)
		}
	}
	for _, art := range order {
		t := titles[art]
		slices.Sort(t.seasons)
		for _, l := range i18n.Languages {
			tag := i18n.TMDB(l)
			if tr.Has(ctx, t.kind, t.id, tag, t.seasons) {
				continue
			}
			tr.Get(ctx, t.kind, t.id, tag, t.seasons)
			select {
			case <-ctx.Done():
				return
			case <-time.After(warmPause):
			}
		}
	}
}

// localizeChannels names cs in ctx's language (display).
func (a *channelsAPI) localizeChannels(ctx context.Context, cs []channels.Channel) {
	for i := range cs {
		cs[i].Name, cs[i].Category = a.display(ctx, cs[i])
	}
}

// localizeInfo puts the programmes of info (the IPTV guide) in ctx's
// language: their text (localizeProgram) and genres.
func (a *channelsAPI) localizeInfo(ctx context.Context, info map[int64]channels.Info) {
	items := map[int64]*library.Item{} // by ArtItem; nil: not found
	for id, x := range info {
		if x.External {
			continue
		}
		it, seen := items[x.ArtItem]
		if !seen {
			if v, err := a.lib.Item(ctx, x.ArtItem); err == nil {
				it = &v
			}
			items[x.ArtItem] = it
		}
		if it != nil {
			a.cards.localizeProgram(ctx, &x, *it)
		}
		gs := make([]string, 0, len(x.Genres))
		for _, g := range x.Genres {
			gs = append(gs, i18n.Genre(ctx, g))
		}
		x.Genres = gs
		info[id] = x
	}
}
