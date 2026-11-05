package api

// «Семья»: the other profiles' progress on an item's page, «Продолжить с
// места …» (another profile's position becomes the caller's own) and the
// Home shelf «Семья смотрит».
//
// Who sees whom: an adult (or admin) sees every other profile, adults and
// kids, except one that hides its progress (users.hide_progress, «Скрывать
// мой прогресс от семьи»); a kid sees nobody's, and a kid's own setting is
// the admin's to change. Everything is read on a page the caller can
// already open (episodes inherit the series rating, comic volumes are
// checked one by one), so the view never names a title the caller may not
// see. Every write goes to the caller's own progress row only.

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
)

// FamilyMember is one other profile's place in an item. ItemID is where
// the position is: the movie, book, audiobook, the episode of a series or
// the volume of a comic. For a series or comic, Done/Total count its
// finished (regular) episodes or volumes, Next says ItemID is the one
// after the last finished (not started: PositionMs 0), and Finished that
// everything is done.
type FamilyMember struct {
	UserID     int64   `json:"userId"`
	Name       string  `json:"name"`
	Avatar     string  `json:"avatar"`
	IsKid      bool    `json:"isKid"`
	ItemID     int64   `json:"itemId"`
	PositionMs int64   `json:"positionMs"`
	DurationMs int64   `json:"durationMs"`
	Fraction   float64 `json:"fraction"` // 0..1 of ItemID
	Finished   bool    `json:"finished"`
	Locator    string  `json:"locator,omitempty"`
	UpdatedAt  string  `json:"updatedAt"`
	Season     int     `json:"season,omitempty"`
	Episode    int     `json:"episode,omitempty"`
	Volume     float64 `json:"volume,omitempty"`
	Title      string  `json:"title,omitempty"` // the episode's or volume's
	Page       int     `json:"page,omitempty"`  // a "page:N" locator: N
	Pages      int     `json:"pages,omitempty"`
	Next       bool    `json:"next,omitempty"`
	Done       int     `json:"done,omitempty"`
	Total      int     `json:"total,omitempty"`
}

// familyKinds are the kinds with a family view (everything with progress
// but music).
var familyKinds = []string{library.KindMovie, library.KindSeries, library.KindSeason, library.KindEpisode,
	library.KindBook, library.KindAudiobook, library.KindComic, library.KindComicVolume}

// adoptKinds hold a position of their own.
var adoptKinds = map[string]bool{library.KindMovie: true, library.KindEpisode: true, library.KindBook: true,
	library.KindAudiobook: true, library.KindComicVolume: true}

// family is whose progress u may see: nobody for a kid; else every other
// profile that does not hide its progress.
func (h *lib) family(ctx context.Context, u auth.User) ([]auth.User, error) {
	if u.IsKid || h.Users == nil {
		return nil, nil
	}
	all, err := h.Users.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []auth.User
	for _, o := range all {
		if o.ID != u.ID && !o.HideProgress {
			out = append(out, o)
		}
	}
	return out, nil
}

func userIDs(us []auth.User) []int64 {
	ids := make([]int64, 0, len(us))
	for _, u := range us {
		ids = append(ids, u.ID)
	}
	return ids
}

// familyProgress: GET /api/items/{id}/family → {"members": [...]}, most
// recent first; empty for a kid and when nobody else has progress there.
func (h *lib) familyProgress(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "id")
	if !ok {
		return
	}
	if !contains(familyKinds, it.Kind) {
		writeErr(w, r, http.StatusBadRequest, "Прогресс для этого элемента не сохраняется")
		return
	}
	ctx := r.Context()
	members, err := h.familyMembers(ctx, userOf(r), it)
	if err != nil {
		internalErr(w, r, "family", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

func contains(ks []string, k string) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

// familyMembers is the family view of it for u (see familyProgress).
func (h *lib) familyMembers(ctx context.Context, u auth.User, it library.Item) ([]FamilyMember, error) {
	out := []FamilyMember{}
	fam, err := h.family(ctx, u)
	if err != nil || len(fam) == 0 {
		return out, err
	}
	switch it.Kind {
	case library.KindSeries, library.KindSeason:
		seriesID := it.ID
		if it.Kind == library.KindSeason {
			seriesID = it.ParentID
		}
		eps, err := h.seriesEpisodes(ctx, seriesID)
		if err != nil {
			return nil, err
		}
		out, err = h.familyOfSet(ctx, fam, eps, func(uid int64, finished map[int64]bool, last library.Item) (int64, bool, error) {
			return h.Progress.NextEpisode(ctx, uid, seriesID)
		}, func(e library.Item) bool { return e.SeasonNo > 0 })
		if err != nil {
			return nil, err
		}
	case library.KindComic:
		vols, err := h.Library.Volumes(ctx, it.ID)
		if err != nil {
			return nil, err
		}
		visible := vols[:0:0]
		for _, v := range vols {
			if ok, err := h.allowed(ctx, u, v); err != nil {
				return nil, err
			} else if ok {
				visible = append(visible, v)
			}
		}
		out, err = h.familyOfSet(ctx, fam, visible, func(_ int64, finished map[int64]bool, last library.Item) (int64, bool, error) {
			// the first unread volume after the last one read
			after := false
			for _, v := range visible {
				if after && !finished[v.ID] {
					return v.ID, true, nil
				}
				after = after || v.ID == last.ID
			}
			return 0, false, nil
		}, func(library.Item) bool { return true })
		if err != nil {
			return nil, err
		}
	default:
		entries, err := h.Progress.Of(ctx, userIDs(fam), []int64{it.ID})
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			o := findUser(fam, e.UserID)
			out = append(out, h.member(o, e, it))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

func findUser(us []auth.User, id int64) auth.User {
	for _, u := range us {
		if u.ID == id {
			return u
		}
	}
	return auth.User{ID: id}
}

// member is o's entry e in the leaf item it.
func (h *lib) member(o auth.User, e progress.Entry, it library.Item) FamilyMember {
	m := FamilyMember{UserID: o.ID, Name: o.Name, Avatar: o.Avatar, IsKid: o.IsKid, ItemID: it.ID,
		PositionMs: e.PositionMs, DurationMs: e.DurationMs, Fraction: ratio(e), Finished: e.Finished, Locator: e.Locator,
		UpdatedAt: e.UpdatedAt.UTC().Format(wireTime)}
	switch it.Kind {
	case library.KindEpisode:
		m.Season, m.Episode, m.Title = it.SeasonNo, it.EpisodeNo, it.Title
	case library.KindComicVolume:
		bm := bookMetaOf(it)
		m.Volume, m.Title, m.Pages = bm.Volume, it.Title, bm.Pages
	}
	if n, ok := strings.CutPrefix(e.Locator, "page:"); ok {
		if p, err := strconv.Atoi(n); err == nil && p > 0 {
			m.Page = p
			if m.Pages == 0 && m.Fraction > 0 && m.Fraction < 1 {
				m.Pages = int(float64(p)/m.Fraction + 0.5) // a PDF: the read share is page / pages
			}
		}
	}
	return m
}

// familyOfSet places each profile in a set of leaves (a series' episodes,
// a comic's volumes, in order): at its newest entry when unfinished, else
// at next(...) (Next), else everything is done (Finished, at the newest).
// counted says which leaves Done/Total count (regular episodes).
func (h *lib) familyOfSet(ctx context.Context, fam []auth.User, leaves []library.Item,
	next func(uid int64, finished map[int64]bool, last library.Item) (int64, bool, error), counted func(library.Item) bool) ([]FamilyMember, error) {
	out := []FamilyMember{}
	if len(leaves) == 0 {
		return out, nil
	}
	byID := make(map[int64]library.Item, len(leaves))
	ids := make([]int64, 0, len(leaves))
	total := 0
	for _, l := range leaves {
		byID[l.ID] = l
		ids = append(ids, l.ID)
		if counted(l) {
			total++
		}
	}
	entries, err := h.Progress.Of(ctx, userIDs(fam), ids)
	if err != nil {
		return nil, err
	}
	byUser := map[int64][]progress.Entry{} // newest first
	var order []int64
	for _, e := range entries {
		if _, ok := byUser[e.UserID]; !ok {
			order = append(order, e.UserID)
		}
		byUser[e.UserID] = append(byUser[e.UserID], e)
	}
	for _, uid := range order {
		es := byUser[uid]
		o := findUser(fam, uid)
		finished := map[int64]bool{}
		own := map[int64]progress.Entry{}
		done := 0
		for _, e := range es {
			own[e.ItemID] = e
			finished[e.ItemID] = e.Finished
			if e.Finished && counted(byID[e.ItemID]) {
				done++
			}
		}
		last := es[0]
		m := h.member(o, last, byID[last.ItemID])
		if last.Finished {
			id, ok, err := next(uid, finished, byID[last.ItemID])
			if err != nil {
				return nil, err
			}
			if l, in := byID[id]; ok && in {
				e, had := own[id]
				if !had {
					e = progress.Entry{UserID: uid, ItemID: id}
				}
				e.UpdatedAt = last.UpdatedAt
				m = h.member(o, e, l)
				m.Next = e.PositionMs == 0
			}
		}
		m.Done, m.Total = done, total
		out = append(out, m)
	}
	return out, nil
}

// seriesEpisodes are a series' episodes in order (seasons, then episodes).
func (h *lib) seriesEpisodes(ctx context.Context, seriesID int64) ([]library.Item, error) {
	seasons, err := h.Library.Children(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	var out []library.Item
	for _, s := range seasons {
		if s.Kind != library.KindSeason {
			continue
		}
		eps, err := h.Library.Children(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		for _, e := range eps {
			if e.Kind == library.KindEpisode {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// adoptProgress: POST /api/progress/{itemId}/adopt {"userId": N},
// «Продолжить с места …»: N's unfinished position in the item becomes the
// caller's own (only the caller's row is written); answers the caller's
// new progress like GET /api/progress/{itemId}. 404 when N is not in the
// caller's family view or has no unfinished position there.
func (h *lib) adoptProgress(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "itemId")
	if !ok {
		return
	}
	if !adoptKinds[it.Kind] {
		writeErr(w, r, http.StatusBadRequest, "Прогресс для этого элемента не сохраняется")
		return
	}
	var in struct {
		UserID int64 `json:"userId"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx, u := r.Context(), userOf(r)
	fam, err := h.family(ctx, u)
	if err != nil {
		internalErr(w, r, "adopt", err)
		return
	}
	if in.UserID == u.ID || !containsID(userIDs(fam), in.UserID) {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	}
	done, err := h.Progress.Adopt(ctx, in.UserID, u.ID, it.ID)
	if err != nil {
		internalErr(w, r, "adopt", err)
		return
	}
	if !done {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	}
	h.getProgress(w, r)
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// familyShelfDays: what someone left longer ago is not «Семья смотрит».
const familyShelfDays = 30

// familyShelf is «Семья смотрит»: movies and series other profiles are in
// the middle of (newest first, one card per title), that u may see and is
// not already watching (skip: the titles of u's «Продолжить»). The card's
// progress and subtitle are the newest viewer's: «Мама · С2 Э5».
func (h *lib) familyShelf(ctx context.Context, r *http.Request, u auth.User, skip map[int64]bool) ([]Card, error) {
	out := []Card{}
	fam, err := h.family(ctx, u)
	if err != nil || len(fam) == 0 {
		return out, err
	}
	since := time.Now().AddDate(0, 0, -familyShelfDays)
	entries, err := h.Progress.StartedBy(ctx, userIDs(fam), []string{library.KindMovie, library.KindEpisode}, since, 10*shelfSize)
	if err != nil {
		return nil, err
	}
	type title struct {
		card  Card
		names []string
	}
	var titles []*title
	byTop := map[int64]*title{}
	for _, e := range entries {
		top := e.ItemID
		if e.SeriesID != 0 {
			top = e.SeriesID
		}
		if skip[top] {
			continue
		}
		name := findUser(fam, e.UserID).Name
		if t := byTop[top]; t != nil {
			if !containsStr(t.names, name) {
				t.names = append(t.names, name)
			}
			continue
		}
		if len(titles) >= shelfSize {
			continue
		}
		it, ok, err := h.visible(ctx, r, top)
		if err != nil {
			return nil, err
		}
		if !ok {
			skip[top] = true
			continue
		}
		c := h.card(ctx, u.ID, it)
		c.Progress = ratio(e)
		code := ""
		if e.SeriesID != 0 {
			if ep, err := h.Library.Item(ctx, e.ItemID); err == nil {
				code = i18n.T(ctx, "С%d Э%d", ep.SeasonNo, ep.EpisodeNo)
			}
		}
		t := &title{card: c, names: []string{name}}
		t.card.Subtitle = code
		byTop[top] = t
		titles = append(titles, t)
	}
	for _, t := range titles {
		who := strings.Join(t.names, ", ")
		if t.card.Subtitle != "" {
			who += " · " + t.card.Subtitle
		}
		t.card.Subtitle = who
		out = append(out, t.card)
	}
	return out, nil
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
