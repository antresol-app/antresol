package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/notify"
	"github.com/go-chi/chi/v5"
)

// LibraryPendingCard is a card of GET /api/library/pending: flat (a Card
// with its progress beside it), unlike addonkit.PendingCard, the nested
// {card, pending} the library.pending hook sends.
type LibraryPendingCard struct {
	Card
	Pending PendingState `json:"pending"`
}

// PendingState is the progress of a LibraryPendingCard, as the addon sent
// it once the core has checked it (addonkit.PendingState).
type PendingState = addonkit.PendingState

// pendingStatuses are the statuses a pending card may have.
var pendingStatuses = map[string]bool{"active": true, "waiting": true, "paused": true, "organizing": true, "error": true}

// pendingKinds are the library kinds a pending card may have (titles, not
// their parts).
var pendingKinds = map[string]bool{library.KindMovie: true, library.KindSeries: true, library.KindAlbum: true,
	library.KindBook: true, library.KindAudiobook: true, library.KindComic: true}

// maxPendingHint caps a pending card's hint, in runes.
const maxPendingHint = 200

// mountPendingCards: GET /api/library/pending, the titles on their way
// into the library, from the addons answering library.pending (none: []).
// maxHeight is the user header's remote quality cap (remoteMaxHeight).
func mountPendingCards(r chi.Router, hooks *addons.Hooks, maxHeight func(*http.Request) int) {
	r.Get("/api/library/pending", func(w http.ResponseWriter, r *http.Request) { libraryPending(w, r, hooks, maxHeight) })
}

// libraryPending asks every addon for its pending cards ({scope}: "mine"
// for ?scope=mine, else "all"; the addon filters by the profile it is
// told) and merges them in addon-id order. A kid's or rating-limited
// profile gets none, and no addon is asked: a title has no rating until
// the library has it. The core checks what comes back: keys are prefixed
// "<addonId>:", error cards reach admins only, a card with an unknown
// status is dropped, a poster that is not an image-cache name and an href
// that is not an app path are emptied.
func libraryPending(w http.ResponseWriter, r *http.Request, hooks *addons.Hooks, maxHeight func(*http.Request) int) {
	u := userOf(r)
	out := []LibraryPendingCard{}
	if hooks == nil || u.IsKid || ratingLimit(u) != "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	scope := "all"
	if scopeOwner(r, u) != 0 {
		scope = auth.ScopeMine
	}
	who := &addonkit.User{ID: u.ID, UID: u.UID, Name: u.Name, IsAdmin: u.IsAdmin, IsKid: u.IsKid, Lang: i18n.FromContext(r.Context()),
		MaxHeight: maxHeight(r)}
	hooks.Each(r.Context(), "library.pending", who, addonkit.PendingQuery{Scope: scope}, func(id string, raw json.RawMessage) {
		var ans addonkit.PendingAnswer
		if err := json.Unmarshal(raw, &ans); err != nil {
			lg := hooks.Log
			if lg == nil {
				lg = slog.Default()
			}
			lg.Warn("addon hook: bad answer", "addon", id, "hook", "library.pending", "err", err)
			return // that addon is left out
		}
		for _, pc := range ans.Cards {
			if c, ok := pendingCardOf(id, pc, u.IsAdmin); ok {
				out = append(out, c)
			}
		}
	})
	writeJSON(w, http.StatusOK, out)
}

// pendingCardOf is addon id's card pc as the profile gets it (ok false:
// dropped).
func pendingCardOf(id string, pc addonkit.PendingCard, admin bool) (LibraryPendingCard, bool) {
	p := pc.Pending
	c := pc.Card
	if p.Key == "" || !pendingStatuses[p.Status] || (p.Status == "error" && !admin) || !pendingKinds[c.Kind] ||
		(c.TmdbKind != "" && c.TmdbKind != library.KindMovie && c.TmdbKind != library.KindSeries) {
		return LibraryPendingCard{}, false
	}
	p.Key = id + ":" + p.Key
	p.Href = notify.AppPath(p.Href)
	p.Hint = pendingHint(p.Hint)
	poster := ""
	if library.ImageCacheName.MatchString(c.Poster) {
		poster = imageURL(0, "poster", c.Poster)
	}
	return LibraryPendingCard{
		Card: Card{ID: c.ID, Kind: c.Kind, Title: c.Title, Subtitle: c.Subtitle, Year: c.Year, Poster: poster,
			TmdbKind: c.TmdbKind, TmdbID: int(c.TmdbID)},
		Pending: p,
	}, true
}

// pendingHint is an addon's hint without control characters, cut to
// maxPendingHint runes.
func pendingHint(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	if r := []rune(s); len(r) > maxPendingHint {
		s = string(r[:maxPendingHint])
	}
	return s
}
