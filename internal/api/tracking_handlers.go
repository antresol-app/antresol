package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/tracking"
	"github.com/go-chi/chi/v5"
)

// Tracking API limits.
const (
	feedMax         = 50               // items of /api/tracking/feed
	feedDays        = 14               // its default window
	feedMaxDays     = 90               // its widest window
	calendarMaxDays = 42               // to - from of /api/tracking/calendar
	onDemandBudget  = 15 * time.Second // an untracked title's refresh on its episodes page
	onDemandFresh   = 6 * time.Hour    // an untracked title refreshed this recently is not fetched again
)

func mountTracking(r chi.Router, h *lib) {
	r.Get("/api/marks", h.listMarks)
	r.Get("/api/marks/{kind}/{tmdbId}", h.getMark)
	r.Put("/api/marks/{kind}/{tmdbId}", h.putMark)
	r.Delete("/api/marks/{kind}/{tmdbId}", h.deleteMark)
	r.Get("/api/tracking/feed", h.trackingFeed)
	r.Get("/api/tracking/calendar", h.trackingCalendar)
	r.Get("/api/tracking/{kind}/{tmdbId}/episodes", h.trackingEpisodes)
}

type trackTitleOut struct {
	Kind   string `json:"kind"` // series | movie
	TmdbID int64  `json:"tmdbId"`
	Name   string `json:"name"`
	Poster string `json:"poster"`
	Status string `json:"status"`
	// ItemID: the title's library item (0: not in the library).
	ItemID int64 `json:"itemId,omitempty"`
}

type markOut struct {
	State     string    `json:"state"`
	Score     *int      `json:"score"`
	Origin    string    `json:"origin"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type sourceOut struct {
	Kind      string    `json:"kind"`
	Ref       string    `json:"ref"`
	Provider  string    `json:"provider,omitempty"`
	Quality   string    `json:"quality,omitempty"`
	Labels    []string  `json:"labels"`
	FirstSeen time.Time `json:"firstSeen"`
}

type feedItemOut struct {
	Title   trackTitleOut `json:"title"`
	Season  int           `json:"season"`
	Episode int           `json:"episode"`
	Name    string        `json:"name"`
	AirDate string        `json:"airDate,omitempty"`
	// FirstSeen: when it became available (null: not yet).
	FirstSeen *time.Time `json:"firstSeen"`
	Kinds     []string   `json:"kinds"`
}

func markJSON(m tracking.Mark) markOut {
	return markOut{State: string(m.State), Score: m.Score, Origin: m.Origin, UpdatedAt: m.UpdatedAt}
}

// trackGate is the rating gate of tracked titles for one request: a title
// in the library passes by the library's rule; one off the library is
// hidden from kids, and from a limited profile unless TMDB's stored
// certification passes its limit. Lookups are kept for the request.
type trackGate struct {
	h    *lib
	u    auth.User
	seen map[int64]gateResult
}

type gateResult struct {
	ok   bool
	item *library.Item
}

func (h *lib) gate(u auth.User) *trackGate {
	return &trackGate{h: h, u: u, seen: map[int64]gateResult{}}
}

// trackedItem is the title's library item (nil: not in the library).
func (h *lib) trackedItem(ctx context.Context, t tracking.Title) (*library.Item, error) {
	kind := library.KindSeries
	if t.Kind == "movie" {
		kind = library.KindMovie
	}
	it, err := h.Library.ItemByTMDB(ctx, kind, t.TMDBID)
	if errors.Is(err, library.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &it, nil
}

func (g *trackGate) check(ctx context.Context, t tracking.Title) (gateResult, error) {
	if r, ok := g.seen[t.ID]; ok && t.ID != 0 {
		return r, nil
	}
	it, err := g.h.trackedItem(ctx, t)
	if err != nil {
		return gateResult{}, err
	}
	var r gateResult
	limit := ratingLimit(g.u)
	switch {
	case it != nil:
		ok, err := g.h.allowed(ctx, g.u, *it)
		if err != nil {
			return gateResult{}, err
		}
		r = gateResult{ok: ok, item: it}
	case limit == "":
		r.ok = true
	case g.u.IsKid || t.Cert == "":
		r.ok = false
	default:
		r.ok = library.RatingAllowed(limit, library.Item{Kind: t.Kind, Rating: t.Cert})
	}
	g.seen[t.ID] = r
	return r, nil
}

// titleJSON is a tracked title as the API shows it: a library title by its
// item's name and artwork.
func titleJSON(t tracking.Title, it *library.Item) trackTitleOut {
	out := trackTitleOut{Kind: t.Kind, TmdbID: t.TMDBID, Name: t.Name, Poster: tracking.PosterURL(t.Poster), Status: t.Status}
	if it != nil {
		out.ItemID, out.Name = it.ID, it.Title
		if p := imageURL(it.ID, "poster", it.Poster); p != "" {
			out.Poster = p
		}
	}
	return out
}

// titleParams reads {kind} and {tmdbId}; writes 400 and returns false when
// they are not a series or movie and a positive id.
func titleParams(w http.ResponseWriter, r *http.Request) (string, int64, bool) {
	kind := chi.URLParam(r, "kind")
	id, err := strconv.ParseInt(chi.URLParam(r, "tmdbId"), 10, 64)
	if kind != "series" && kind != "movie" {
		writeErr(w, r, http.StatusBadRequest, "Неизвестный раздел")
		return "", 0, false
	}
	if err != nil || id <= 0 {
		writeErr(w, r, http.StatusBadRequest, "Неверный id")
		return "", 0, false
	}
	return kind, id, true
}

// ensureFresh returns the title (created, unmarked, when absent) after a
// refresh within onDemandBudget when nobody tracks it and it was not
// refreshed in onDemandFresh (or when force). A failed refresh is logged:
// the stored state is served.
func (h *lib) ensureFresh(ctx context.Context, kind string, tmdbID int64, name, poster string, force bool) (tracking.Title, error) {
	st := h.Tracking.Store
	t, err := st.EnsureTitle(ctx, kind, tmdbID, name, poster)
	if err != nil {
		return t, err
	}
	if !force {
		if !t.Refreshed.IsZero() && time.Since(t.Refreshed) < onDemandFresh {
			return t, nil
		}
		users, err := st.Interested(ctx, t.ID)
		if err != nil {
			return t, err
		}
		if len(users) > 0 && !t.Refreshed.IsZero() {
			return t, nil // the refresher keeps it
		}
	}
	rctx, cancel := context.WithTimeout(ctx, onDemandBudget)
	defer cancel()
	if err := h.Tracking.Refresh(rctx, t.ID); err != nil {
		if ctx.Err() != nil {
			return t, ctx.Err()
		}
		log.Printf("tracking: on-demand refresh %s %d: %v", kind, tmdbID, err)
	}
	t, _, err = st.TitleByID(ctx, t.ID)
	return t, err
}

func (h *lib) putMark(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	kind, tmdbID, ok := titleParams(w, r)
	if !ok {
		return
	}
	var in struct {
		State  string          `json:"state"`
		Score  json.RawMessage `json:"score"` // absent: keep the mark's score; null: clear it
		Name   string          `json:"name"`
		Poster string          `json:"poster"` // TMDB path
	}
	if !decode(w, r, &in) {
		return
	}
	switch tracking.MarkState(in.State) {
	case tracking.Planned, tracking.Watching, tracking.Completed, tracking.OnHold, tracking.Dropped:
	default:
		writeErr(w, r, http.StatusBadRequest, "Неизвестное состояние отметки")
		return
	}
	var score *int
	scoreGiven := in.Score != nil
	if scoreGiven && string(in.Score) != "null" {
		var n int
		if json.Unmarshal(in.Score, &n) != nil || n < 1 || n > 10 {
			writeErr(w, r, http.StatusBadRequest, "Оценка — от 1 до 10")
			return
		}
		score = &n
	}
	t, err := h.Tracking.Store.EnsureTitle(ctx, kind, tmdbID, strings.TrimSpace(in.Name), in.Poster)
	if err != nil {
		internalErr(w, r, "mark title", err)
		return
	}
	g := h.gate(u)
	res, err := g.check(ctx, t)
	if err != nil {
		internalErr(w, r, "mark gate", err)
		return
	}
	// a limited adult's title off the library: its rating comes with a refresh
	if !res.ok && res.item == nil && !u.IsKid && t.Cert == "" && t.Refreshed.IsZero() && h.Tracking.Skeleton != nil {
		if t, err = h.ensureFresh(ctx, kind, tmdbID, "", "", true); err != nil {
			internalErr(w, r, "mark refresh", err)
			return
		}
		delete(g.seen, t.ID)
		if res, err = g.check(ctx, t); err != nil {
			internalErr(w, r, "mark gate", err)
			return
		}
	}
	if !res.ok {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	}
	if !scoreGiven {
		if old, _, err := h.markOf(ctx, u.ID, kind, tmdbID); err != nil {
			internalErr(w, r, "mark", err)
			return
		} else if old != nil {
			score = old.Score
		}
	}
	m := tracking.Mark{UserID: u.ID, TitleID: t.ID, State: tracking.MarkState(in.State), Score: score, UpdatedAt: time.Now().UTC().Truncate(time.Second)}
	if err := h.Tracking.Store.SetMark(ctx, m); err != nil {
		internalErr(w, r, "mark", err)
		return
	}
	switch m.State {
	case tracking.Planned, tracking.Watching, tracking.OnHold:
		h.Tracking.Kick(t.ID)
	}
	if res.item != nil {
		if err := h.syncWatchlist(ctx, u.ID, res.item.ID, m.State == tracking.Planned); err != nil {
			internalErr(w, r, "mark watchlist", err)
			return
		}
	}
	m.Origin = "antresol"
	writeJSON(w, http.StatusOK, map[string]any{"mark": markJSON(m)})
}

// syncWatchlist keeps the profile's «Хочу посмотреть» row of a library
// title the same as its mark: planned ⇒ present, any other state or none ⇒ gone.
func (h *lib) syncWatchlist(ctx context.Context, userID, itemID int64, planned bool) error {
	if planned {
		return h.Progress.AddToWatchlist(ctx, userID, itemID)
	}
	return h.Progress.RemoveFromWatchlist(ctx, userID, itemID)
}

// inWatchlist: the library title of (kind, tmdbID) is on the profile's watchlist.
func (h *lib) inWatchlist(ctx context.Context, userID int64, kind string, tmdbID int64) (bool, error) {
	it, err := h.trackedItem(ctx, tracking.Title{Kind: kind, TMDBID: tmdbID})
	if err != nil || it == nil {
		return false, err
	}
	ids, err := h.Progress.Watchlist(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if id == it.ID {
			return true, nil
		}
	}
	return false, nil
}

// markOf is the profile's mark on (kind, tmdbID), nil when none.
func (h *lib) markOf(ctx context.Context, userID int64, kind string, tmdbID int64) (*tracking.Mark, tracking.Title, error) {
	t, ok, err := h.Tracking.Store.TitleByTMDB(ctx, kind, tmdbID)
	if err != nil || !ok {
		return nil, t, err
	}
	marks, err := h.Tracking.Store.Marks(ctx, userID)
	if err != nil {
		return nil, t, err
	}
	for _, m := range marks {
		if m.TitleID == t.ID {
			return &m, t, nil
		}
	}
	return nil, t, nil
}

func (h *lib) getMark(w http.ResponseWriter, r *http.Request) {
	kind, tmdbID, ok := titleParams(w, r)
	if !ok {
		return
	}
	m, _, err := h.markOf(r.Context(), userOf(r).ID, kind, tmdbID)
	if err != nil {
		internalErr(w, r, "mark", err)
		return
	}
	if m == nil {
		// a library title already on the watchlist reads as «Хочу посмотреть»
		on, err := h.inWatchlist(r.Context(), userOf(r).ID, kind, tmdbID)
		if err != nil {
			internalErr(w, r, "mark", err)
			return
		}
		if on {
			writeJSON(w, http.StatusOK, map[string]any{"mark": markOut{State: string(tracking.Planned), Origin: "antresol"}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mark": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mark": markJSON(*m)})
}

func (h *lib) deleteMark(w http.ResponseWriter, r *http.Request) {
	kind, tmdbID, ok := titleParams(w, r)
	if !ok {
		return
	}
	t, found, err := h.Tracking.Store.TitleByTMDB(r.Context(), kind, tmdbID)
	if err == nil && found {
		err = h.Tracking.Store.DeleteMark(r.Context(), userOf(r).ID, t.ID)
	}
	if err == nil {
		var it *library.Item
		if it, err = h.trackedItem(r.Context(), tracking.Title{Kind: kind, TMDBID: tmdbID}); err == nil && it != nil {
			err = h.syncWatchlist(r.Context(), userOf(r).ID, it.ID, false)
		}
	}
	if err != nil {
		internalErr(w, r, "mark delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *lib) listMarks(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	var states []tracking.MarkState
	if q := r.URL.Query().Get("state"); q != "" {
		for _, s := range strings.Split(q, ",") {
			switch st := tracking.MarkState(strings.TrimSpace(s)); st {
			case tracking.Planned, tracking.Watching, tracking.Completed, tracking.OnHold, tracking.Dropped:
				states = append(states, st)
			default:
				writeErr(w, r, http.StatusBadRequest, "Неизвестное состояние отметки")
				return
			}
		}
	}
	marks, err := h.Tracking.Store.Marks(ctx, u.ID, states...)
	if err != nil {
		internalErr(w, r, "marks", err)
		return
	}
	type itemOut struct {
		Title trackTitleOut `json:"title"`
		Mark  markOut       `json:"mark"`
	}
	g := h.gate(u)
	items := []itemOut{}
	for _, m := range marks {
		t, ok, err := h.Tracking.Store.TitleByID(ctx, m.TitleID)
		if err != nil {
			internalErr(w, r, "marks title", err)
			return
		}
		if !ok {
			continue
		}
		res, err := g.check(ctx, t)
		if err != nil {
			internalErr(w, r, "marks gate", err)
			return
		}
		if res.ok {
			items = append(items, itemOut{titleJSON(t, res.item), markJSON(m)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// visibleFeed is the feed of the profile in the last days, behind its
// rating gate, newest first, at most max items.
func (h *lib) visibleFeed(ctx context.Context, u auth.User, days, max int) ([]tracking.FeedItem, *trackGate, error) {
	since := time.Now().AddDate(0, 0, -days)
	items, err := h.Tracking.Store.Feed(ctx, u.ID, since, h.Tracking.WatchedBy())
	if err != nil {
		return nil, nil, err
	}
	g := h.gate(u)
	out := []tracking.FeedItem{}
	for _, it := range items {
		if len(out) == max {
			break
		}
		res, err := g.check(ctx, it.Title)
		if err != nil {
			return nil, nil, err
		}
		if res.ok {
			out = append(out, it)
		}
	}
	return out, g, nil
}

func feedJSON(it tracking.FeedItem, g *trackGate) feedItemOut {
	out := feedItemOut{Title: titleJSON(it.Title, g.seen[it.Title.ID].item), Season: it.Season, Episode: it.Episode,
		Name: it.Name, AirDate: it.AirDate, Kinds: it.Kinds}
	if out.Kinds == nil {
		out.Kinds = []string{}
	}
	if !it.FirstSeen.IsZero() {
		fs := it.FirstSeen
		out.FirstSeen = &fs
	}
	return out
}

func (h *lib) trackingFeed(w http.ResponseWriter, r *http.Request) {
	days := feedDays
	if s := r.URL.Query().Get("days"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			writeErr(w, r, http.StatusBadRequest, "Неверное число дней")
			return
		}
		days = min(n, feedMaxDays)
	}
	items, g, err := h.visibleFeed(r.Context(), userOf(r), days, feedMax)
	if err != nil {
		internalErr(w, r, "tracking feed", err)
		return
	}
	out := make([]feedItemOut, 0, len(items))
	for _, it := range items {
		out = append(out, feedJSON(it, g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *lib) trackingCalendar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err1 := time.Parse(time.DateOnly, q.Get("from"))
	to, err2 := time.Parse(time.DateOnly, q.Get("to"))
	if err1 != nil || err2 != nil || to.Before(from) || to.Sub(from) > calendarMaxDays*24*time.Hour {
		writeErr(w, r, http.StatusBadRequest, "Неверный диапазон дат")
		return
	}
	ctx, u := r.Context(), userOf(r)
	days, err := h.Tracking.Store.Calendar(ctx, u.ID, from.Format(time.DateOnly), to.Format(time.DateOnly))
	if err != nil {
		internalErr(w, r, "tracking calendar", err)
		return
	}
	type dayOut struct {
		Date  string        `json:"date"`
		Items []feedItemOut `json:"items"`
	}
	g := h.gate(u)
	out := []dayOut{}
	for _, d := range days {
		var items []feedItemOut
		for _, it := range d.Items {
			res, err := g.check(ctx, it.Title)
			if err != nil {
				internalErr(w, r, "tracking calendar gate", err)
				return
			}
			if res.ok {
				items = append(items, feedJSON(it, g))
			}
		}
		if len(items) > 0 {
			out = append(out, dayOut{d.Date, items})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": out})
}

func (h *lib) trackingEpisodes(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	kind, tmdbID, ok := titleParams(w, r)
	if !ok {
		return
	}
	var t tracking.Title
	var err error
	if h.Tracking.Skeleton != nil {
		t, err = h.ensureFresh(ctx, kind, tmdbID, "", "", false)
	} else {
		t, err = h.Tracking.Store.EnsureTitle(ctx, kind, tmdbID, "", "")
	}
	if err != nil {
		internalErr(w, r, "tracking episodes", err)
		return
	}
	g := h.gate(u)
	res, err := g.check(ctx, t)
	if err != nil {
		internalErr(w, r, "tracking episodes gate", err)
		return
	}
	if !res.ok {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	}
	eps, err := h.Tracking.Store.Episodes(ctx, t.ID)
	if err != nil {
		internalErr(w, r, "tracking episodes", err)
		return
	}
	srcs, err := h.Tracking.Store.Sources(ctx, t.ID)
	if err != nil {
		internalErr(w, r, "tracking sources", err)
		return
	}
	bySE := map[[2]int][]sourceOut{}
	for _, s := range srcs {
		labels := s.Labels
		if labels == nil {
			labels = []string{}
		}
		k := [2]int{s.Season, s.Episode}
		bySE[k] = append(bySE[k], sourceOut{Kind: s.Kind, Ref: s.Ref, Provider: s.Provider, Quality: s.Quality, Labels: labels, FirstSeen: s.FirstSeen})
	}
	type epOut struct {
		Season  int         `json:"season"`
		Number  int         `json:"number"`
		Name    string      `json:"name"`
		AirDate string      `json:"airDate"`
		Sources []sourceOut `json:"sources"`
	}
	out := make([]epOut, 0, len(eps))
	for _, e := range eps {
		s := bySE[[2]int{e.Season, e.Number}]
		if s == nil {
			s = []sourceOut{}
		}
		out = append(out, epOut{e.Season, e.Number, e.Name, e.AirDate, s})
	}
	writeJSON(w, http.StatusOK, map[string]any{"title": titleJSON(t, res.item), "episodes": out})
}

// trackedShelf is «Вышло новое»: one card per title of the profile's feed
// (its newest episode), library titles as their library cards.
func (h *lib) trackedShelf(ctx context.Context, u auth.User) ([]Card, error) {
	items, g, err := h.visibleFeed(ctx, u, feedDays, feedMax)
	if err != nil {
		return nil, err
	}
	cards := []Card{}
	done := map[int64]bool{}
	for _, it := range items {
		if done[it.Title.ID] || len(cards) == shelfSize {
			continue
		}
		done[it.Title.ID] = true
		var c Card
		if li := g.seen[it.Title.ID].item; li != nil {
			c = h.card(ctx, u.ID, *li)
		} else {
			// off the library (ID 0, not Online): the TMDB title and the episode
			c = Card{Kind: it.Title.Kind, Title: it.Title.Name, Poster: tracking.PosterURL(it.Title.Poster),
				TmdbKind: it.Title.Kind, TmdbID: int(it.Title.TMDBID)}
			if it.Title.Kind == "series" {
				c.Season, c.Episode = it.Season, it.Episode
			}
		}
		if it.Title.Kind == "series" {
			c.Subtitle = episodeLabel(ctx, it.Season, it.Episode)
		}
		cards = append(cards, c)
	}
	return cards, nil
}

// trackedShelfTitle is the shelf's name in the request's language.
func trackedShelfTitle(ctx context.Context) string { return i18n.T(ctx, "Вышло новое") }

// watchlistToMark is the other half of the sync, for POST/DELETE
// /api/watchlist/{itemId} (the native apps): adding a library movie or
// series with a TMDB id makes the profile's mark planned (its score kept);
// removing it deletes the mark only when it is planned. It writes the
// store directly, never through syncWatchlist, so the two cannot loop.
func (h *lib) watchlistToMark(ctx context.Context, userID int64, it library.Item, add bool) error {
	if h.Tracking == nil || it.TmdbID <= 0 || (it.Kind != library.KindSeries && it.Kind != library.KindMovie) {
		return nil
	}
	kind := "series"
	if it.Kind == library.KindMovie {
		kind = "movie"
	}
	old, t, err := h.markOf(ctx, userID, kind, it.TmdbID)
	if err != nil {
		return err
	}
	st := h.Tracking.Store
	if !add {
		if old != nil && old.State == tracking.Planned {
			return st.DeleteMark(ctx, userID, t.ID)
		}
		return nil
	}
	if old != nil && old.State == tracking.Planned {
		return nil
	}
	if t, err = st.EnsureTitle(ctx, kind, it.TmdbID, it.Title, ""); err != nil {
		return err
	}
	m := tracking.Mark{UserID: userID, TitleID: t.ID, State: tracking.Planned, UpdatedAt: time.Now().UTC().Truncate(time.Second)}
	if old != nil {
		m.Score = old.Score
	}
	if err := st.SetMark(ctx, m); err != nil {
		return err
	}
	h.Tracking.Kick(t.ID)
	return nil
}
