package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/antresol-app/antresol/internal/tracking"
	"github.com/go-chi/chi/v5"
)

// Tracking API limits.
const (
	feedMax         = 50               // items of /api/tracking/feed
	feedDays        = 14               // its default window
	feedMaxDays     = 90               // its widest window
	calendarMaxDays = 42               // to - from of /api/tracking/calendar
	onDemandBudget  = 10 * time.Second // what an untracked title's episodes page and choice routes wait for: its TMDB episode list
	onDemandFresh   = 6 * time.Hour    // an untracked title refreshed this recently is not fetched again
)

// onDemandScanBudget bounds the rest of such a refresh (the addons' scanners:
// indexers that time out, one call to an addon can take 120 s), which runs
// after the response, in the background. A var so a test can shorten it.
var onDemandScanBudget = 5 * time.Minute

// onDemandStaleWait is how long a title read before waits for its refresh
// when the stored list is old: slower than that, the stored list is served and
// the refresh goes on in the background. A var so a test can shorten it.
var onDemandStaleWait = 500 * time.Millisecond

// scanGrace is how long the request that started a background scan waits for
// it, to answer after a scan that has nothing to wait for.
const scanGrace = 100 * time.Millisecond

func mountTracking(r chi.Router, h *lib) {
	r.Get("/api/marks", h.listMarks)
	r.Get("/api/marks/{kind}/{tmdbId}", h.getMark)
	r.Put("/api/marks/{kind}/{tmdbId}", h.putMark)
	r.Delete("/api/marks/{kind}/{tmdbId}", h.deleteMark)
	r.Get("/api/tracking/feed", h.trackingFeed)
	r.Get("/api/tracking/calendar", h.trackingCalendar)
	r.Get("/api/tracking/{kind}/{tmdbId}/episodes", h.trackingEpisodes)
	r.Get("/api/tracking/{kind}/{tmdbId}/choice", h.getChoice)
	r.Put("/api/tracking/{kind}/{tmdbId}/choice", h.putChoice)
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
	Kind      string              `json:"kind"`
	Ref       string              `json:"ref"`
	Provider  string              `json:"provider,omitempty"`
	Quality   string              `json:"quality,omitempty"`
	Labels    []string            `json:"labels"`
	FirstSeen time.Time           `json:"firstSeen"`
	Meta      tracking.SourceMeta `json:"meta"`
}

type feedItemOut struct {
	Title   trackTitleOut `json:"title"`
	Season  int           `json:"season"`
	Episode int           `json:"episode"`
	Name    string        `json:"name"`
	AirDate string        `json:"airDate,omitempty"`
	// Still: the episode's own 16:9 picture (TMDB), "" = none (the card shows the poster).
	Still string `json:"still,omitempty"`
	// FirstSeen: when it became available (null: not yet).
	FirstSeen *time.Time `json:"firstSeen"`
	Kinds     []string   `json:"kinds"`
	Why       *whyOut    `json:"why,omitempty"`
}

// whyOut is why the profile follows a feed item's title (tracking.Why):
// state planned | watching | on_hold, or "watchlist"; origin the list that
// set the mark ("" for the watchlist).
type whyOut struct {
	State  string `json:"state"`
	Origin string `json:"origin,omitempty"`
}

func whyJSON(w tracking.Why) *whyOut {
	if w.State == "" {
		return nil
	}
	return &whyOut{State: w.State, Origin: w.Origin}
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
	if kind != "series" && kind != "movie" {
		writeErr(w, r, http.StatusBadRequest, "Неизвестный раздел")
		return "", 0, false
	}
	id, ok := pathID(r, "tmdbId")
	if !ok {
		writeErr(w, r, http.StatusBadRequest, "Неверный id")
		return "", 0, false
	}
	return kind, id, true
}

// ensureFresh returns the title (created, unmarked, when absent) after a
// refresh of its episode list within onDemandBudget when nobody tracks it and
// it was not refreshed in onDemandFresh (or when force). Only that half of
// the refresh is waited for (tracking.Service.RefreshEpisodes: TMDB and the
// library's own rows): it marks the title refreshed, so the next visit is
// served at once, whatever the addons' scanners do. They run on in the
// background (Pending.Finish, bounded by onDemandScanBudget), one per title.
// Concurrent callers for one title share one refresh (h.refreshes); it runs
// to its budget even when the request that started it goes away. A title
// that was read before (and not forced) is waited for only onDemandStaleWait:
// slower than that its stored list is served and the refresh goes on. A failed
// refresh is logged: the stored state is served.
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
	key := kind + "/" + strconv.FormatInt(tmdbID, 10)
	done := h.refreshes.DoChan(key, func() (any, error) {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), onDemandBudget)
		defer cancel()
		// a flight that ended after this caller's check above refreshed it
		if now, _, err := st.TitleByID(rctx, t.ID); err == nil && !force && now.Refreshed.After(t.Refreshed) {
			return nil, nil
		}
		p, err := h.Tracking.RefreshEpisodes(rctx, t.ID)
		if err != nil {
			log.Printf("tracking: on-demand refresh %s %d: %v", kind, tmdbID, err)
			return nil, nil
		}
		h.finishInBackground(key, p)
		return nil, nil
	})
	var late <-chan time.Time // nil: the first list of a title is waited for
	if !force && !t.Refreshed.IsZero() {
		timer := time.NewTimer(onDemandStaleWait)
		defer timer.Stop()
		late = timer.C
	}
	select {
	case <-done:
	case <-late:
		return t, nil
	case <-ctx.Done():
		return t, ctx.Err()
	}
	t, _, err = st.TitleByID(ctx, t.ID)
	return t, err
}

// finishInBackground runs the rest of an on-demand refresh (the addons'
// scanners, the letters) outside any request, one at a time per title (a
// refresh whose title is still being scanned leaves it to that scan). The
// caller does not wait for a scan: do's short wait only lets a refresh that
// needs no addon (none installed) be over before the answer.
func (h *lib) finishInBackground(key string, p *tracking.Pending) {
	_, _ = h.scans.do(context.Background(), key, scanGrace, onDemandScanBudget, func(ctx context.Context) (struct{}, error) {
		if err := p.Finish(ctx); err != nil {
			log.Printf("tracking: on-demand scan %s: %v", key, err)
		}
		return struct{}{}, nil
	})
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
// rating gate, newest first, at most max items. An item keeps only the
// source kinds the profile may open (sourceKindsFor); one left with none
// is not news for it.
func (h *lib) visibleFeed(ctx context.Context, u auth.User, days, max int) ([]tracking.FeedItem, *trackGate, error) {
	since := time.Now().AddDate(0, 0, -days)
	items, err := h.Tracking.Store.Feed(ctx, u.ID, since, h.Tracking.FeedWatched())
	if err != nil {
		return nil, nil, err
	}
	g := h.gate(u)
	kinds := h.sourceKindsFor(u)
	out := []tracking.FeedItem{}
	for _, it := range items {
		if len(out) == max {
			break
		}
		if it.Kinds = mayOpenKinds(it.Kinds, kinds); len(it.Kinds) == 0 {
			continue
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
		Name: it.Name, AirDate: it.AirDate, Still: tracking.StillURL(it.Still), Kinds: it.Kinds, Why: whyJSON(it.Why)}
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
	kinds := h.sourceKindsFor(u)
	out := []dayOut{}
	for _, d := range days {
		var items []feedItemOut
		for _, it := range d.Items {
			// only the kinds the profile may open; none: not available
			// to it yet
			if it.Kinds = mayOpenKinds(it.Kinds, kinds); len(it.Kinds) == 0 {
				it.FirstSeen = time.Time{}
			}
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

// sourceKindsFor is the source kinds the caller may be shown: a kid has the
// library's alone (a kid's every other play and download route is nonKid);
// an adult has the library's and the scan kinds of the installed addons the
// profile has access to, on or off, on every server (E2: a disabled addon's
// rows stay listed). The core names no other kind: `online` rows are listed
// when an addon declares tracking.scan:online (D12).
func (h *lib) sourceKindsFor(u auth.User) map[string]bool {
	k := map[string]bool{"library": true}
	if u.IsKid {
		return k
	}
	for _, a := range h.AddonHooks.ScanKinds(u) {
		k[a] = true
	}
	return k
}

// mayOpenKinds are the kinds of ks the profile may open (allowed:
// sourceKindsFor), in their order.
func mayOpenKinds(ks []string, allowed map[string]bool) []string {
	out := []string{}
	for _, k := range ks {
		if allowed[k] {
			out = append(out, k)
		}
	}
	return out
}

// choiceKindOK: a source choice's kind is empty, library, or an installed
// addon's scan kind the profile has access to (E2, on every server); a
// kid's is the library's alone.
func (h *lib) choiceKindOK(u auth.User, kind string) bool {
	switch {
	case kind == "" || kind == "library":
		return true
	case u.IsKid:
		return false
	}
	return slices.Contains(h.AddonHooks.ScanKinds(u), kind)
}

// trackingEpisodes serves GET /api/tracking/{kind}/{tmdbId}/episodes, the
// apps' contract:
//
//	{title, episodes: [{season, number, name, airDate,
//	    watched: bool, positionSec?: int, new: bool,
//	    sources: [{kind, ref, provider?, quality?, labels[], firstSeen,
//	               meta: SourceMeta}]}]}
//
// Sources are those the caller may use (sourceKindsFor). meta is
// tracking.SourceMeta (itemId and fileId play a library row; ref of a
// library row is "item:<id>"). watched and positionSec merge the profile's
// WatchStates (watched if any says so, the largest position). new: the
// episode is unwatched, has a source other than the library's, and is news
// by the feed's rule (tracking.IsNews over its earliest source of any kind,
// since the feed's default window, feedDays). name of the title (off the
// library) and of the episodes is in the request's language where TMDB has
// it (trackingTranslation), else the stored one. 404 when the rating gate
// hides the title.
func (h *lib) trackingEpisodes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	kind, tmdbID, ok := titleParams(w, r)
	if !ok {
		return
	}
	t, res, ok := h.gatedTitle(w, r, kind, tmdbID)
	if !ok {
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
	u := userOf(r)
	states, err := tracking.MergeWatchStates(ctx, h.Tracking.WatchStates, auth.HistoryID(ctx), kind, tmdbID)
	if err != nil {
		internalErr(w, r, "tracking watch state", err)
		return
	}
	kinds := h.sourceKindsFor(u)
	since := time.Now().AddDate(0, 0, -feedDays)
	first := map[[2]int]time.Time{} // every kind's, as the feed counts
	for _, s := range srcs {
		k := [2]int{s.Season, s.Episode}
		if f, ok := first[k]; !ok || s.FirstSeen.Before(f) {
			first[k] = s.FirstSeen
		}
	}
	type epOut struct {
		Season      int         `json:"season"`
		Number      int         `json:"number"`
		Name        string      `json:"name"`
		AirDate     string      `json:"airDate"`
		Still       string      `json:"still,omitempty"` // the episode's TMDB picture
		Watched     bool        `json:"watched"`
		PositionSec int         `json:"positionSec,omitempty"`
		New         bool        `json:"new"`
		Sources     []sourceOut `json:"sources"`
	}
	byKey := map[[2]int]*epOut{}
	out := make([]*epOut, 0, len(eps))
	add := func(season, number int, name, airDate string) *epOut {
		st := states[[2]int{season, number}]
		e := &epOut{Season: season, Number: number, Name: name, AirDate: airDate,
			Watched: st.Watched, PositionSec: st.PositionSec, Sources: []sourceOut{}}
		out = append(out, e)
		byKey[[2]int{season, number}] = e
		return e
	}
	tr := h.trackingTranslation(ctx, t, res, eps)
	for _, e := range eps {
		add(e.Season, e.Number, translatedTrackedEpisode(tr, e), e.AirDate).Still = tracking.StillURL(e.Still)
	}
	extra := false
	for _, s := range srcs {
		if !kinds[s.Kind] {
			continue
		}
		e := byKey[[2]int{s.Season, s.Episode}]
		if e == nil {
			if s.Kind != "library" {
				continue
			}
			// a file the episode list lacks (a special, absolute
			// numbering, a list not refreshed yet): still an episode
			e = add(s.Season, s.Episode, h.libraryEpisodeName(ctx, s.Meta.ItemID), "")
			extra = true
		}
		labels := s.Labels
		if labels == nil {
			labels = []string{}
		}
		e.Sources = append(e.Sources, sourceOut{Kind: s.Kind, Ref: s.Ref, Provider: s.Provider, Quality: s.Quality,
			Labels: labels, FirstSeen: s.FirstSeen, Meta: s.Meta})
		if s.Kind != "library" && !e.Watched && tracking.IsNews(kind, e.AirDate, first[[2]int{s.Season, s.Episode}], since) {
			e.New = true
		}
	}
	if extra {
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i], out[j]
			return a.Season < b.Season || a.Season == b.Season && a.Number < b.Number
		})
	}
	title := titleJSON(t, res.item)
	if tr != nil && res.item == nil && tr.Title != "" {
		title.Name = tr.Title // a library title keeps its library name
	}
	writeJSON(w, http.StatusOK, map[string]any{"title": title, "episodes": out})
}

// trackingTranslation is TMDB's text of a tracked title in the viewer's
// language (the request's, as the library pages read it): its name and its
// episodes' names, for the episode list, which is stored once in the
// household's metadata language. nil: serve the stored text: the viewer
// reads that language, nothing in the answer needs a translation (a movie
// of the library), no translator is set, or TMDB has none (down, or no
// text in that language). The text is cached for a month per language; a
// missing one is fetched here within Translations' short wait, so only the
// first visit in a language pays for it.
func (h *lib) trackingTranslation(ctx context.Context, t tracking.Title, res gateResult, eps []tracking.Episode) *tmdb.Translation {
	if h.Translations == nil {
		return nil
	}
	tag := i18n.TMDB(i18n.FromContext(ctx))
	if primaryLang(h.Translations.Language()) == primaryLang(tag) {
		return nil
	}
	kind, seasons := "movie", []int(nil)
	if t.Kind == "series" {
		kind = "tv"
		for _, e := range eps {
			if e.Season > 0 && !slices.Contains(seasons, e.Season) {
				seasons = append(seasons, e.Season)
			}
		}
		slices.Sort(seasons)
	} else if res.item != nil {
		return nil // the movie's name is the library's, and it has no episode names
	}
	tr, ok := h.Translations.Get(ctx, kind, int(t.TMDBID), tag, seasons)
	if !ok {
		return nil
	}
	return &tr
}

// translatedTrackedEpisode is the name of episode e in tr, else the stored one.
func translatedTrackedEpisode(tr *tmdb.Translation, e tracking.Episode) string {
	if tr != nil {
		if x, ok := tr.Episode(e.Season, e.Number); ok && x.Name != "" {
			return x.Name
		}
	}
	return e.Name
}

// libraryEpisodeName is the name of a library episode item ("" when unknown).
func (h *lib) libraryEpisodeName(ctx context.Context, itemID int64) string {
	if itemID <= 0 || h.Library == nil {
		return ""
	}
	it, err := h.Library.Item(ctx, itemID)
	if err != nil {
		return ""
	}
	return it.Title
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
	var lib []library.Item // the library cards in one batch
	for _, it := range items {
		if li := g.seen[it.Title.ID].item; li != nil {
			lib = append(lib, *li)
		}
	}
	ctx = h.withCards(ctx, auth.HistoryID(ctx), lib)
	for _, it := range items {
		if done[it.Title.ID] || len(cards) == shelfSize {
			continue
		}
		done[it.Title.ID] = true
		var c Card
		if li := g.seen[it.Title.ID].item; li != nil {
			c = h.card(ctx, auth.HistoryID(ctx), *li)
		} else {
			// off the library (ID 0): the TMDB title and the episode
			c = Card{Kind: it.Title.Kind, Title: it.Title.Name, Poster: tracking.PosterURL(it.Title.Poster),
				TmdbKind: it.Title.Kind, TmdbID: int(it.Title.TMDBID)}
			if it.Title.Kind == "series" {
				c.Season, c.Episode = it.Season, it.Episode
			}
		}
		if it.Title.Kind == "series" {
			c.Subtitle = episodeLabel(ctx, it.Season, it.Episode)
		}
		c.Why = whyJSON(it.Why)
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

// gatedTitle returns the title (created unmarked when absent; refreshed on
// demand by ensureFresh when a TMDB skeleton is set) and its rating gate
// result. The gate reads
// the title's stored cert, which a refresh fills, so the episodes and the
// choice routes go through here to agree on who may see the title. false: the response
// (404 or 500) is written.
func (h *lib) gatedTitle(w http.ResponseWriter, r *http.Request, kind string, tmdbID int64) (tracking.Title, gateResult, bool) {
	ctx := r.Context()
	var t tracking.Title
	var err error
	if h.Tracking.Skeleton != nil {
		t, err = h.ensureFresh(ctx, kind, tmdbID, "", "", false)
	} else {
		t, err = h.Tracking.Store.EnsureTitle(ctx, kind, tmdbID, "", "")
	}
	if err != nil {
		internalErr(w, r, "tracking title", err)
		return t, gateResult{}, false
	}
	res, err := h.gate(userOf(r)).check(ctx, t)
	if err != nil {
		internalErr(w, r, "tracking title gate", err)
		return t, gateResult{}, false
	}
	if !res.ok {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return t, gateResult{}, false
	}
	return t, res, true
}

// getChoice serves GET /api/tracking/{kind}/{tmdbId}/choice: the profile's
// source choice for the title as {"choice": {kind, provider, voice, lang,
// quality}} (fields omitted when empty), or {"choice": null} when none was
// made. 404 when the rating gate hides the title.
func (h *lib) getChoice(w http.ResponseWriter, r *http.Request) {
	kind, tmdbID, ok := titleParams(w, r)
	if !ok {
		return
	}
	if _, _, ok := h.gatedTitle(w, r, kind, tmdbID); !ok {
		return
	}
	c, found, err := h.Tracking.Store.Choice(r.Context(), userOf(r).ID, kind, tmdbID)
	if err != nil {
		internalErr(w, r, "tracking choice", err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"choice": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"choice": c})
}

// putChoice serves PUT /api/tracking/{kind}/{tmdbId}/choice. The body is a
// Choice {kind, provider, voice, lang, quality}, every field optional; kind
// is empty, library or an addon's scan kind (choiceKindOK; a kid's only
// library), else 400. 204 on success, 404 when the rating gate hides
// the title.
func (h *lib) putChoice(w http.ResponseWriter, r *http.Request) {
	kind, tmdbID, ok := titleParams(w, r)
	if !ok {
		return
	}
	var in tracking.Choice
	if !decode(w, r, &in) {
		return
	}
	if !h.choiceKindOK(userOf(r), in.Kind) {
		writeErr(w, r, http.StatusBadRequest, "Неизвестный тип источника")
		return
	}
	if _, _, ok := h.gatedTitle(w, r, kind, tmdbID); !ok {
		return
	}
	if err := h.Tracking.Store.SetChoice(r.Context(), userOf(r).ID, kind, tmdbID, in, time.Now()); err != nil {
		internalErr(w, r, "tracking choice", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
