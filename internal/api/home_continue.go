package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"slices"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/notify"
)

// maxAddonContinue is how many of one addon's cards, its newest, the core
// looks at: a shelf's worth with room for the ones the gate drops.
const maxAddonContinue = 2 * shelfSize

// addonContinueAsync starts addonContinue on a goroutine of its own, so
// that Home reads its own shelves meanwhile; the channel gives its cards
// once (none at once when Home did not ask).
func (h *lib) addonContinueAsync(r *http.Request, u auth.User) <-chan []datedCard {
	out := make(chan []datedCard, 1)
	if !h.wantsAddonContinue(r, u) {
		out <- nil
		return out
	}
	go func() { out <- h.addonContinue(r.Context(), u, remoteMaxHeight(&h.Deps, r)) }()
	return out
}

// wantsAddonContinue: only a client that shows addon cards asks for them
// (/api/home?addons=1), and never for a kid.
func (h *lib) wantsAddonContinue(r *http.Request, u auth.User) bool {
	return r.URL.Query().Get("addons") == "1" && h.AddonHooks != nil && !u.IsKid
}

// addonContinue is the addons' part of «Продолжить»: home.continue (body
// {}, the profile in the user header) on every addon the profile has
// access to (Manifest.OpenTo, as for the bundles), in parallel under the
// hook's hard 500 ms; a down, late or failing addon is left out. The core
// does not trust an addon's card: it re-checks the rating with its own
// item's when the library has the title, namespaces the key, keeps only an
// image-cache poster and an app-path href.
func (h *lib) addonContinue(ctx context.Context, u auth.User, maxHeight int) []datedCard {
	who := &addonkit.User{ID: u.ID, UID: u.UID, Name: u.Name, IsAdmin: u.IsAdmin, IsKid: u.IsKid, Lang: i18n.FromContext(ctx),
		MaxRating: ratingLimit(u), MaxHeight: maxHeight}
	type sent struct {
		id string
		c  addonkit.ContinueCard
	}
	var cards []sent
	h.AddonHooks.EachFor(ctx, "home.continue", who, func(in addons.Installed) bool { return in.Manifest.OpenTo(u.IsAdmin) }, nil,
		func(id string, raw json.RawMessage) {
			var ans addonkit.ContinueAnswer
			if err := json.Unmarshal(raw, &ans); err != nil {
				log.Printf("api: addon %s home.continue: %v", id, err) // that addon is left out
				return
			}
			var own []addonkit.ContinueCard
			for _, c := range ans.Cards {
				if c.Key != "" && (c.Kind == library.KindMovie || c.Kind == library.KindSeries) {
					own = append(own, c)
				}
			}
			slices.SortStableFunc(own, func(a, b addonkit.ContinueCard) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
			for _, c := range own[:min(len(own), maxAddonContinue)] {
				cards = append(cards, sent{id, c}) // EachFor calls one addon at a time
			}
		})
	if len(cards) == 0 {
		return nil
	}
	limit := ratingLimit(u)
	var rated map[string]map[int64]library.Item // kind → TMDB id → the library's item
	if limit != "" {
		byKind := map[string][]int64{}
		for _, s := range cards {
			if s.c.TMDBID > 0 {
				byKind[s.c.Kind] = append(byKind[s.c.Kind], s.c.TMDBID)
			}
		}
		var err error
		if rated, err = h.itemsByTMDB(ctx, byKind); err != nil {
			log.Printf("api: home.continue ratings: %v", err) // the gate cannot be applied: no addon cards
			return nil
		}
	}
	var out []datedCard
	for _, s := range cards {
		c := s.c
		if limit != "" {
			it, ok := rated[c.Kind][c.TMDBID]
			if !ok || c.TMDBID <= 0 {
				it = library.Item{Kind: c.Kind, Rating: c.Rating}
			}
			if !library.RatingAllowed(limit, it) {
				continue
			}
		}
		card := Card{Kind: c.Kind, Title: c.Title, Subtitle: c.Subtitle, Year: c.Year, Progress: min(max(c.Progress, 0), 1),
			TmdbKind: c.Kind, Season: max(c.Season, 0), Episode: max(c.Episode, 0), ResumeSec: max(c.ResumeSec, 0),
			AddonID: s.id, Key: "addon:" + s.id + ":" + c.Key, Href: notify.AppPath(c.Href)}
		if c.TMDBID > 0 {
			card.TmdbID = int(c.TMDBID)
		}
		if library.ImageCacheName.MatchString(c.Poster) {
			card.Poster = imageURL(0, "poster", c.Poster)
		}
		out = append(out, datedCard{c.UpdatedAt, card})
	}
	return out
}

// itemsByTMDB finds the library's top-level items of these TMDB ids, per
// kind (movie, series), in one query per kind and one for the items.
func (h *lib) itemsByTMDB(ctx context.Context, byKind map[string][]int64) (map[string]map[int64]library.Item, error) {
	out := map[string]map[int64]library.Item{}
	idOf := map[string]map[int64]int64{}
	var ids []int64
	for kind, tmdb := range byKind {
		m, err := h.Library.ItemIDsByTMDB(ctx, kind, tmdb)
		if err != nil {
			return nil, err
		}
		idOf[kind] = m
		for _, id := range m {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	items, err := h.Library.ItemsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	for kind, m := range idOf {
		out[kind] = map[int64]library.Item{}
		for tmdb, id := range m {
			if it, ok := items[id]; ok {
				out[kind][tmdb] = it
			}
		}
	}
	return out, nil
}
