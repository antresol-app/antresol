package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
)

// requestSearcher is a RequestCatalog that also finds movies and series
// for a kid's search (tmdbRequests does).
type requestSearcher interface {
	Search(ctx context.Context, q string) ([]RequestCandidate, error)
}

// requestSearchKinds: ?kind= of GET /api/requests/search ("video": TMDB
// movies and series; the others: the addons' catalogs).
var requestSearchKinds = map[string]bool{"video": true, library.KindMusic: true, library.KindBook: true,
	library.KindAudiobook: true, library.KindComic: true}

// kidCandidate is a search result for a kid: with its kidRequest.
type kidCandidate struct {
	RequestCandidate
	KidRequest *requestState `json:"kidRequest"`
}

// GET /api/requests/search?q=&kind=video|music|book|audiobook|comic
// (kids, while requests are open) → []kidCandidate: what the kid may see
// and ask for, each with its kidRequest. Video comes from TMDB, the other
// kinds from the addons (requests.search).
func (k *kidRequests) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if k.open == nil || !k.open(ctx) {
		writeErr(w, r, http.StatusForbidden, "Просьбы здесь не принимаются")
		return
	}
	u := userOf(r)
	if !u.IsKid {
		writeErr(w, r, http.StatusForbidden, "Попросить могут только детские профили")
		return
	}
	kind, q := r.URL.Query().Get("kind"), strings.TrimSpace(r.URL.Query().Get("q"))
	if !requestSearchKinds[kind] {
		writeErr(w, r, http.StatusBadRequest, "Неизвестный раздел")
		return
	}
	cs := []RequestCandidate{}
	switch {
	case q == "":
	case kind == "video":
		s, ok := k.cat.(requestSearcher)
		if !ok {
			break
		}
		var err error
		if cs, err = s.Search(liveTMDB(r), q); err != nil {
			if ctx.Err() != nil {
				return // the client went away
			}
			log.Printf("api: request search: %v", err)
			writeErr(w, r, http.StatusServiceUnavailable, "TMDB сейчас недоступен. Попробуйте позже")
			return
		}
	default:
		cs = k.addons.Search(ctx, kind, q)
	}
	writeJSON(w, http.StatusOK, k.forKid(ctx, u, cs))
}

// visibleTo drops what a kid may not see: movies and series by kidMaySee,
// their ratings looked up (the rating alone, cached, a few at once; only the
// first kidRatingChecks of them, the rest dropped; a failed lookup drops the
// title too), albums and books the catalog marks explicit.
func (k *kidRequests) visibleTo(ctx context.Context, u auth.User, cs []RequestCandidate) []RequestCandidate {
	keep := make([]bool, len(cs))
	sem := make(chan struct{}, ratingLookups)
	var wg sync.WaitGroup
	videos := 0
	for i, c := range cs {
		if c.Kind != library.KindMovie && c.Kind != library.KindSeries {
			keep[i] = !c.Explicit
			continue
		}
		if videos++; videos > kidRatingChecks {
			continue // not checked: hidden
		}
		if e, ok := k.ratings.get(c.Kind, c.TMDBID); ok {
			keep[i] = kidMaySee(ctx, u, e.rating, e.adult)
			continue
		}
		wg.Add(1)
		go func(i int, c RequestCandidate) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rating, err := k.cat.TitleRating(ctx, c.Kind, c.TMDBID)
			if err != nil {
				return // unknown: hidden
			}
			k.ratings.put(c.Kind, c.TMDBID, rating, false) // adult results never get here (Search drops them)
			keep[i] = kidMaySee(ctx, u, rating, false)
		}(i, c)
	}
	wg.Wait()
	out := make([]RequestCandidate, 0, len(cs))
	for i, c := range cs {
		if keep[i] {
			out = append(out, c)
		}
	}
	return out
}

// forKid is a kid's search results: only what it may see (visibleTo), each
// with its kidRequest.
func (k *kidRequests) forKid(ctx context.Context, u auth.User, cs []RequestCandidate) []kidCandidate {
	cs = k.visibleTo(ctx, u, cs)
	mine := k.mineByKey(ctx, u.ID)
	out := make([]kidCandidate, 0, len(cs))
	for _, c := range cs {
		subtitle := c.Subtitle
		if c.Kind == library.KindComic {
			subtitle = ""
		}
		st := k.titleState(ctx, u, mine, c.Kind, c.TMDBID, c.ExtID, c.Title, subtitle, "")
		if c.Kind != library.KindMovie && c.Kind != library.KindSeries {
			c.Poster = "" // a kid gets placeholder art for albums and books: no outside cover
		}
		c.Aliases = nonNil(c.Aliases)
		out = append(out, kidCandidate{RequestCandidate: c, KidRequest: st})
	}
	return out
}

// titleState is the kidRequest field of a title for the kid (mine:
// mineByKey); blocked says why it may not be asked for.
func (k *kidRequests) titleState(ctx context.Context, u auth.User, mine map[string]KidRequest, kind string, tmdbID int, extID, title, subtitle, blocked string) *requestState {
	key := requestKey(kind, tmdbID, extID, title, subtitle)
	st := &requestState{Key: key, Allowed: blocked == "", Blocked: blocked}
	if x, ok := mine[kind+"|"+key]; ok {
		v := k.view(ctx, x, u)
		st.Request = &v
	}
	return st
}
