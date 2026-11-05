package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/screentime"
	"github.com/go-chi/chi/v5"
)

// «Экранное время» (internal/screentime): the kid's own GET
// /api/me/screen-time, the admin's /api/users/{id}/screen-time (the report,
// PUT the limits) and POST …/bonus. Enforcement sits in the playback
// handlers: POST /api/play, /api/hls/* (sessions started before the limit
// stop at it), /api/stream (a long direct read ends at it), the audio
// streams when audio is limited; they count the playback too, with the
// progress saves.

// ScreenTimeCode is the "code" of a refusal ({"error", "code", "reason"}).
const ScreenTimeCode = "screen_time"

func mountScreenTime(r chi.Router, h *lib) {
	r.Get("/api/me/screen-time", h.myScreenTime)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/screen-time/limited", h.limitedKids)
		r.Get("/api/users/{id}/screen-time", h.userScreenTime)
		r.Put("/api/users/{id}/screen-time", h.putScreenTime)
		r.Post("/api/users/{id}/screen-time/bonus", h.screenTimeBonus)
	})
}

// writeScreenTime answers a refusal: 403 {"error": message, "code":
// "screen_time", "reason": "limit"|"hours"}.
func writeScreenTime(w http.ResponseWriter, ref *screentime.Refusal) {
	writeJSON(w, http.StatusForbidden, map[string]string{"error": ref.Message, "code": ScreenTimeCode, "reason": ref.Reason})
}

// screenAllowed checks the request's profile may play an item of kind now
// (false: the refusal is written). Kinds never limited always pass.
func (h *lib) screenAllowed(w http.ResponseWriter, r *http.Request, kind string) bool {
	c, ok := screentime.CategoryOf(kind)
	if !ok {
		return true
	}
	if ref := h.ScreenTime.Allow(r.Context(), userOf(r), c); ref != nil {
		writeScreenTime(w, ref)
		return false
	}
	return true
}

// downloadOff is the refusal of a download for a kid with a limit.
const downloadOff = "Скачивание выключено родителем"

// copyOff is the refusal of a device copy for a kid with a video limit.
const copyOff = "Скачивание на устройство выключено родителем"

// screenDownloadAllowed: a kid profile whose rules limit the item's
// category (video for films and episodes, audio for music and
// audiobooks) does not download it (a file saved is a file played without
// a clock), as device copies (device_copy_handlers.go); false: the
// refusal is written (403 code screen_time, reason download).
func (h *lib) screenDownloadAllowed(w http.ResponseWriter, r *http.Request, kind string) bool {
	if !h.screenRestricts(r, kind) {
		return true
	}
	writeScreenTime(w, &screentime.Refusal{Reason: "download", Message: i18n.T(r.Context(), downloadOff)})
	return false
}

// screenRestricts: the request's kid profile has a rule over the
// category of kind (nothing of it downloads or goes to the device).
func (h *lib) screenRestricts(r *http.Request, kind string) bool {
	c, ok := screentime.CategoryOf(kind)
	return ok && h.ScreenTime.Restricted(r.Context(), userOf(r), c)
}

// screenOpened marks a title opened for playback: its player's progress
// reports count from now on (screentime.Service.Opened).
func (h *lib) screenOpened(r *http.Request, it library.Item) {
	if c, ok := screentime.CategoryOf(it.Kind); ok {
		h.ScreenTime.Opened(r.Context(), userOf(r), c, itemStream(it.ID), it.ID)
	}
}

// audioMeter is audioserve's Meter for a kid's stream of it (key: the
// title, or the music queue for a track): what is sent counts, and the
// stream ends at the limit.
func (h *lib) audioMeter(r *http.Request, it library.Item) func(http.ResponseWriter, float64) (http.ResponseWriter, func()) {
	c, ok := screentime.CategoryOf(it.Kind)
	if !ok || h.ScreenTime == nil || !userOf(r).IsKid {
		return nil
	}
	key := itemStream(it.ID)
	if it.Kind == library.KindTrack {
		key = screentime.MusicStream
	}
	return func(w http.ResponseWriter, bps float64) (http.ResponseWriter, func()) {
		m := h.ScreenTime.Meter(r.Context(), w, userOf(r), screentime.Signal{Category: c, Key: key, ItemID: it.ID}, bps)
		if m == nil {
			return nil, nil
		}
		return m, m.Done
	}
}

// itemStream is the stream key of a library title.
func itemStream(id int64) string { return "item:" + strconv.FormatInt(id, 10) }

// screenSignal counts a read of an item's media (Report false) or a
// player's report of it.
func (h *lib) screenSignal(r *http.Request, it library.Item, report bool) {
	c, ok := screentime.CategoryOf(it.Kind)
	if !ok || h.ScreenTime == nil {
		return
	}
	h.ScreenTime.Signal(r.Context(), userOf(r), screentime.Signal{Category: c, Key: itemStream(it.ID), ItemID: it.ID, Report: report})
}

// screenReadEvery: how often a direct stream counts and checks the
// profile's time while it is read.
const screenReadEvery = 5 * time.Second

// screenReader is a direct stream's file for a limited profile: while the
// player reads it, it counts the reading (a media read, at most every
// screenReadEvery) and checks the time; once the time is over the read
// fails and the response ends there (a player that keeps one range
// request open for the whole film stops at the limit too).
type screenReader struct {
	io.ReadSeeker
	check func() bool // false: stop
	every time.Duration
	mu    sync.Mutex
	last  time.Time
	done  bool
}

var errScreenTime = errors.New("api: screen time is over")

func (s *screenReader) Read(p []byte) (int, error) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return 0, errScreenTime
	}
	if now := time.Now(); now.Sub(s.last) >= s.every {
		s.last = now
		if !s.check() {
			s.done = true
			s.mu.Unlock()
			return 0, errScreenTime
		}
	}
	s.mu.Unlock()
	return s.ReadSeeker.Read(p)
}

// screenWrap is the wrapper serveFile gives the file of a video or
// audiobook stream of a kid profile (nil: nothing to watch).
func (h *lib) screenWrap(r *http.Request, it library.Item) func(io.ReadSeeker) io.ReadSeeker {
	c, ok := screentime.CategoryOf(it.Kind)
	u := userOf(r)
	if !ok || h.ScreenTime == nil || !u.IsKid {
		return nil
	}
	ctx := context.WithoutCancel(r.Context())
	return func(rs io.ReadSeeker) io.ReadSeeker {
		return &screenReader{ReadSeeker: rs, every: screenReadEvery, check: func() bool {
			key := itemStream(it.ID)
			if it.Kind == library.KindTrack {
				key = screentime.MusicStream
			}
			h.ScreenTime.Signal(ctx, u, screentime.Signal{Category: c, Key: key, ItemID: it.ID})
			return h.ScreenTime.Allow(ctx, u, c) == nil
		}}
	}
}

// myScreenTime is GET /api/me/screen-time: the profile's time today
// (screentime.Status; a profile that is not a kid: limited false).
func (h *lib) myScreenTime(w http.ResponseWriter, r *http.Request) {
	st, err := h.ScreenTime.Status(r.Context(), userOf(r))
	if err != nil {
		internalErr(w, r, "screen time", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// screenTitle is a title of the report: a library item's name (an
// episode: its series).
type screenTitle struct {
	screentime.TitleUsage
	Title  string `json:"title"`
	Kind   string `json:"kind,omitempty"`
	Poster string `json:"poster,omitempty"`
}

type screenReport struct {
	screentime.Report
	Titles []screenTitle `json:"titles"`
	// Passwordless are the other profiles the kid can switch to without a
	// password and play on unlimited (adults, kids without rules): the
	// sheet asks for passwords (admin-only data).
	Passwordless []string `json:"passwordless"`
}

// passwordlessBut are the names of the profiles other than kid that open
// without a password and are not limited (adults, kids without rules).
func (h *lib) passwordlessBut(ctx context.Context, kid int64) ([]string, error) {
	us, err := h.Users.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, u := range us {
		if u.ID == kid || u.HasPassword || (u.IsKid && h.ScreenTime.Restricted(ctx, u, "")) {
			continue
		}
		out = append(out, u.Name)
	}
	return out, nil
}

// limitedKids is GET /api/screen-time/limited (admin): the kid profiles
// with some rule, and the profiles they could switch to without a
// password ({"userIds": [...], "passwordless": [...]}; «Профили» warns).
func (h *lib) limitedKids(w http.ResponseWriter, r *http.Request) {
	if h.Users == nil {
		writeJSON(w, http.StatusOK, map[string]any{"userIds": []int64{}, "passwordless": []string{}})
		return
	}
	us, err := h.Users.List(r.Context())
	if err != nil {
		internalErr(w, r, "users", err)
		return
	}
	ids := []int64{}
	for _, u := range us {
		if u.IsKid && h.ScreenTime.Restricted(r.Context(), u, "") {
			ids = append(ids, u.ID)
		}
	}
	type entry struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	open := []entry{}
	for _, u := range us {
		if !u.HasPassword && !(u.IsKid && h.ScreenTime.Restricted(r.Context(), u, "")) {
			open = append(open, entry{u.ID, u.Name})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"userIds": ids, "passwordless": open})
}

// kidParam is the kid profile of {id} (false: the error is written).
func (h *lib) kidParam(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return auth.User{}, false
	}
	if h.Users == nil || h.ScreenTime == nil {
		writeErr(w, r, http.StatusNotFound, "Профиль не найден")
		return auth.User{}, false
	}
	u, err := h.Users.Get(r.Context(), id)
	if errors.Is(err, auth.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Профиль не найден")
		return u, false
	}
	if err != nil {
		internalErr(w, r, "user", err)
		return u, false
	}
	if !u.IsKid {
		writeErr(w, r, http.StatusBadRequest, "Экранное время есть только у детских профилей")
		return u, false
	}
	return u, true
}

func (h *lib) writeScreenReport(w http.ResponseWriter, r *http.Request, u auth.User) {
	rep, err := h.ScreenTime.Report(r.Context(), u)
	if err != nil {
		internalErr(w, r, "screen time report", err)
		return
	}
	out := screenReport{Report: rep, Titles: []screenTitle{}}
	if out.Passwordless, err = h.passwordlessBut(r.Context(), u.ID); err != nil {
		internalErr(w, r, "users", err)
		return
	}
	// an episode's (and a season's) time is its series'
	byRoot := map[int64]int{}
	for _, t := range rep.Titles {
		st := screenTitle{TitleUsage: t}
		if t.ItemID > 0 {
			if it, err := h.Library.Item(r.Context(), t.ItemID); err == nil {
				for it.ParentID > 0 && (it.Kind == library.KindEpisode || it.Kind == library.KindSeason) {
					p, err := h.Library.Item(r.Context(), it.ParentID)
					if err != nil {
						break
					}
					it = p
				}
				st.ItemID, st.Title, st.Kind, st.Poster = it.ID, it.Title, it.Kind, imageURL(it.ID, "poster", it.Poster)
			}
		}
		if i, ok := byRoot[st.ItemID]; ok && st.ItemID > 0 && out.Titles[i].Category == st.Category {
			out.Titles[i].Seconds += st.Seconds
			continue
		}
		byRoot[st.ItemID] = len(out.Titles)
		out.Titles = append(out.Titles, st)
	}
	// most first, once episodes are added up under their series
	sort.SliceStable(out.Titles, func(i, j int) bool { return out.Titles[i].Seconds > out.Titles[j].Seconds })
	writeJSON(w, http.StatusOK, out)
}

// userScreenTime is GET /api/users/{id}/screen-time (admin).
func (h *lib) userScreenTime(w http.ResponseWriter, r *http.Request) {
	if u, ok := h.kidParam(w, r); ok {
		h.writeScreenReport(w, r, u)
	}
}

// putScreenTime is PUT /api/users/{id}/screen-time (admin): the limits
// (screentime.Limits); answers the report.
func (h *lib) putScreenTime(w http.ResponseWriter, r *http.Request) {
	u, ok := h.kidParam(w, r)
	if !ok {
		return
	}
	var l screentime.Limits
	if !decode(w, r, &l) {
		return
	}
	if err := h.ScreenTime.SetLimits(r.Context(), u.ID, l); errors.Is(err, screentime.ErrInvalid) {
		writeErr(w, r, http.StatusBadRequest, "Неверные ограничения времени")
		return
	} else if err != nil {
		internalErr(w, r, "screen time limits", err)
		return
	}
	h.writeScreenReport(w, r, u)
}

// screenTimeBonus is POST /api/users/{id}/screen-time/bonus (admin)
// {"minutes": 30}: more time today; answers the report.
func (h *lib) screenTimeBonus(w http.ResponseWriter, r *http.Request) {
	u, ok := h.kidParam(w, r)
	if !ok {
		return
	}
	var in struct {
		Minutes int `json:"minutes"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := h.ScreenTime.AddBonus(r.Context(), u.ID, in.Minutes); errors.Is(err, screentime.ErrInvalid) {
		writeErr(w, r, http.StatusBadRequest, "Неверный бонус")
		return
	} else if err != nil {
		internalErr(w, r, "screen time bonus", err)
		return
	}
	h.writeScreenReport(w, r, u)
}
