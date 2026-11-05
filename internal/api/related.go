package api

import (
	"context"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/go-chi/chi/v5"
)

// RelatedSource is TMDB's titles around a title and its collection, in a
// language (*tmdb.Related, cached).
type RelatedSource interface {
	Titles(ctx context.Context, kind string, id int, tag string) (tmdb.RelatedTitles, bool)
	Collection(ctx context.Context, id int, tag string) (tmdb.Collection, bool)
	ImageURL(size, p string) string
}

// The «Похожие» row: the library's titles first (up to maxSimilarLibrary),
// then a few that are not in it (the sources build, for profiles that may
// open a TMDB title page).
const (
	maxSimilarLibrary  = 20
	maxSimilarDiscover = 8
)

// RelatedRows are GET /api/items/{id}/related: the «Похожие» and
// «Коллекция» rows of a movie's or series' page. Similar is [] and
// Collection null when there is nothing to show (no TMDB key, not a TMDB
// title, TMDB unreachable, nothing the profile may see).
type RelatedRows struct {
	Similar    []Card         `json:"similar"`
	Collection *CollectionRow `json:"collection"`
}

// CollectionRow is a movie's TMDB collection (a franchise) with its parts
// in release order: the library's cards (watched set for a profile that
// finished one) and, in the sources build, cards of the parts that are not
// in the library (id 0, onServer false, tmdbKind/tmdbId: their title
// page). Images are URLs.
type CollectionRow struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Overview string `json:"overview,omitempty"`
	Poster   string `json:"poster,omitempty"`
	Backdrop string `json:"backdrop,omitempty"`
	Parts    []Card `json:"parts"`
}

func mountRelated(r chi.Router, h *lib) {
	r.Get("/api/items/{id}/related", h.related)
	r.Get("/api/items/{id}/collection", h.collection)
}

// tmdbKindOf is the TMDB kind of a library movie or series ("" = other).
func tmdbKindOf(kind string) string {
	switch kind {
	case library.KindMovie:
		return "movie"
	case library.KindSeries:
		return "tv"
	}
	return ""
}

// libraryKindOf is the library kind of a TMDB kind.
func libraryKindOf(tmdbKind string) string {
	if tmdbKind == "tv" {
		return library.KindSeries
	}
	return library.KindMovie
}

// relatedOf is TMDB's titles around it in the viewer's language (false:
// none: not a TMDB movie or series, no TMDB, nothing known).
func (h *lib) relatedOf(ctx context.Context, it library.Item) (tmdb.RelatedTitles, string, bool) {
	kind := tmdbKindOf(it.Kind)
	if h.Related == nil || kind == "" || it.TmdbID <= 0 {
		return tmdb.RelatedTitles{}, "", false
	}
	tag := i18n.TMDB(i18n.FromContext(ctx))
	rel, ok := h.Related.Titles(ctx, kind, int(it.TmdbID), tag)
	return rel, tag, ok
}

func (h *lib) related(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "id")
	if !ok {
		return
	}
	ctx, u := r.Context(), userOf(r)
	out := RelatedRows{Similar: []Card{}}
	if rel, tag, ok := h.relatedOf(ctx, it); ok {
		// the collection's lookup (its TMDB fetch was started along with
		// the titles') runs while the similar cards are made
		var wg sync.WaitGroup
		if rel.Collection != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out.Collection = h.collectionRow(ctx, u, it, *rel.Collection, tag)
			}()
		}
		out.Similar = h.similarCards(ctx, u, it, rel.Similar)
		wg.Wait()
	}
	writeJSON(w, http.StatusOK, out)
}

// collection: GET /api/items/{id}/collection, the collection page of a
// movie (404 when it has none the profile may see).
func (h *lib) collection(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "id")
	if !ok {
		return
	}
	ctx, u := r.Context(), userOf(r)
	if rel, tag, ok := h.relatedOf(ctx, it); ok && rel.Collection != nil {
		if c := h.collectionRow(ctx, u, it, *rel.Collection, tag); c != nil {
			writeJSON(w, http.StatusOK, c)
			return
		}
	}
	writeErr(w, r, http.StatusNotFound, "Не найдено")
}

// matches are the library's items of some TMDB refs.
type matches struct {
	// items: the card of a ref, its first edition the profile may see
	// (never the page's item itself)
	items map[int]library.Item
	// inLib: the refs the library has at all, seen or not
	inLib map[int]bool
	// editions: of a card's item id, every edition of its title the
	// profile may see (the page's item too): watched when one is
	editions map[int64][]int64
}

// libraryMatches finds the library's items of refs for the profile.
func (h *lib) libraryMatches(ctx context.Context, u auth.User, it library.Item, refs []tmdb.TitleRef) matches {
	m := matches{items: map[int]library.Item{}, inLib: map[int]bool{}, editions: map[int64][]int64{}}
	byKind := map[string][]int64{}
	for _, ref := range refs {
		k := libraryKindOf(ref.Kind)
		byKind[k] = append(byKind[k], int64(ref.ID))
	}
	for kind, ids := range byKind {
		found, err := h.Library.EditionsByTMDB(ctx, kind, ids)
		if err != nil {
			log.Printf("api: related %d: %v", it.ID, err) // degrade: no library cards
			continue
		}
		for tmdbID, eds := range found {
			m.inLib[int(tmdbID)] = true
			var seen []int64
			var first *library.Item
			for _, id := range eds {
				e := it
				if id != it.ID {
					if e, err = h.Library.Item(ctx, id); err != nil {
						continue
					}
					if ok, err := h.allowed(ctx, u, e); err != nil || !ok {
						continue
					}
				}
				seen = append(seen, id)
				if first == nil && id != it.ID {
					first = &e
				}
			}
			if slices.Contains(seen, it.ID) {
				m.editions[it.ID] = seen
			}
			if first != nil {
				m.items[int(tmdbID)] = *first
				m.editions[first.ID] = seen
			}
		}
	}
	return m
}

// discoverCard is the card of a TMDB title that is not in the library.
func (h *lib) discoverCard(ref tmdb.TitleRef) Card {
	k := libraryKindOf(ref.Kind)
	return Card{Kind: k, Title: ref.Title, Year: ref.Year, TmdbKind: k, TmdbID: ref.ID,
		Poster: h.Related.ImageURL("w342", ref.Poster), Backdrop: h.Related.ImageURL("w780", ref.Backdrop)}
}

// markWatched sets Watched on the library cards the profile finished, in
// any of the title's editions.
func (h *lib) markWatched(ctx context.Context, uid int64, cards []Card, editions map[int64][]int64) {
	var leaves, series []int64
	of := func(c Card) []int64 {
		if eds := editions[c.ID]; len(eds) > 0 {
			return eds
		}
		return []int64{c.ID}
	}
	for _, c := range cards {
		switch {
		case c.ID <= 0:
		case c.Kind == library.KindSeries:
			series = append(series, of(c)...)
		default:
			leaves = append(leaves, of(c)...)
		}
	}
	if len(leaves)+len(series) == 0 {
		return
	}
	done, err := h.Progress.WatchedOf(ctx, uid, leaves, series)
	if err != nil {
		log.Printf("api: related watched: %v", err) // degrade: no marks
		return
	}
	for i := range cards {
		if cards[i].ID > 0 {
			cards[i].Watched = slices.ContainsFunc(of(cards[i]), func(id int64) bool { return done[id] })
		}
	}
}

// similarCards: the library's titles in TMDB's order, then (when the
// profile may open TMDB titles) a few that are not in the library.
func (h *lib) similarCards(ctx context.Context, u auth.User, it library.Item, refs []tmdb.TitleRef) []Card {
	m := h.libraryMatches(ctx, u, it, refs)
	discover := h.mayDiscover(u)
	out, more := []Card{}, []Card{}
	for _, ref := range refs {
		if e, ok := m.items[ref.ID]; ok {
			if len(out) < maxSimilarLibrary {
				out = append(out, h.card(ctx, u.ID, e))
			}
			continue
		}
		// a title the library has but the profile may not see is not offered either
		if m.inLib[ref.ID] || !discover || len(more) >= maxSimilarDiscover {
			continue
		}
		more = append(more, h.discoverCard(ref))
	}
	h.markWatched(ctx, u.ID, out, m.editions)
	return append(out, more...)
}

// collectionRow is the collection with the parts the profile may see; nil
// when that leaves nothing but it.
func (h *lib) collectionRow(ctx context.Context, u auth.User, it library.Item, ref tmdb.CollectionRef, tag string) *CollectionRow {
	col, ok := h.Related.Collection(ctx, ref.ID, tag)
	if !ok || len(col.Parts) == 0 {
		return nil
	}
	m := h.libraryMatches(ctx, u, it, col.Parts)
	discover := h.mayDiscover(u)
	row := &CollectionRow{ID: col.ID, Name: col.Name, Overview: col.Overview, Parts: []Card{},
		Poster: h.Related.ImageURL("w342", col.Poster), Backdrop: h.Related.ImageURL("w1280", col.Backdrop)}
	if row.Name == "" {
		row.Name = ref.Name
	}
	row.Name = shortCollectionName(row.Name)
	others := 0
	for _, p := range col.Parts {
		var c Card
		switch e, ok := m.items[p.ID]; {
		case p.ID == int(it.TmdbID):
			c = h.card(ctx, u.ID, it)
		case ok:
			c = h.card(ctx, u.ID, e)
		case m.inLib[p.ID] || !discover:
			continue // in the library but not for this profile, or nothing to open
		default:
			c = h.discoverCard(p)
		}
		// the year says the order better than the genres
		c.Subtitle = ""
		row.Parts = append(row.Parts, c)
		if p.ID != int(it.TmdbID) {
			others++
		}
	}
	if others == 0 {
		return nil
	}
	h.markWatched(ctx, u.ID, row.Parts, m.editions)
	return row
}

// collectionWords are how TMDB marks a collection in its name («Матрица
// (Коллекция)», «The Matrix Collection»); the pages say it themselves
// («Коллекция «Матрица»»).
var collectionWords = []string{"(Коллекция)", "(коллекция)", "- Коллекция", "— Коллекция", "Collection", "collection"}

// shortCollectionName is a collection's name without that word.
func shortCollectionName(s string) string {
	s = strings.TrimSpace(s)
	for _, w := range collectionWords {
		if t, ok := strings.CutSuffix(s, w); ok && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	if t, ok := strings.CutPrefix(s, "Коллекция"); ok && strings.HasPrefix(t, " ") {
		if t = strings.Trim(t, " «»\""); t != "" {
			return t
		}
	}
	return s
}
