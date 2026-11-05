package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/notify"
	"github.com/go-chi/chi/v5"
)

// kidRequests is «Попросить»: a kid profile, which gets nothing from outside
// the library, asks for a title; an admin («Управление → Просьбы») approves
// it (another build has another way too: approveFor, from the
// acquire handler) or declines it. «Уже в медиатеке» is not stored: the
// library is asked whenever a request is shown. Table kid_requests (0052).
type kidRequests struct {
	db  *sql.DB
	cat RequestCatalog // the titles to ask for: TMDB, covers, the library
	bus *events.Bus    // nil: no events
	// notify tells the kid by mail that a request was approved (nil: no letters)
	notify *notify.Service
	// ratings: the age ratings of TMDB titles, for what a kid may see
	ratings ratingCache
	// open says whether requests are open on this server (Deps.RequestsOpen);
	// nil or false: ask answers 403 and touches nothing
	open func(context.Context) bool

	mu      sync.Mutex
	lastAsk map[int64]time.Time // a kid's last new request (mayAsk)
}

// Request statuses: the stored ones, and statusInLibrary, which is derived.
const (
	statusPending   = "pending"
	statusApproved  = "approved"
	statusDeclined  = "declined"
	statusInLibrary = "in_library"
)

// requestDecidedWindow: how long a decided request stays in the admin's
// list (pending ones stay until decided).
const requestDecidedWindow = 30 * 24 * time.Hour

// KidRequest is a request as the kid and the admins see it.
type KidRequest struct {
	ID int64 `json:"id"`
	// Key names the title ("tmdb:603", "ext:<MusicBrainz id>",
	// "name:берсерк"): one request per kid and key.
	Key      string   `json:"key"`
	Kind     string   `json:"kind"`
	TMDBID   int      `json:"tmdbId"`
	ExtID    string   `json:"extId"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"` // the artist or the author
	Year     int      `json:"year"`
	Poster   string   `json:"poster"`
	Aliases  []string `json:"aliases"`
	Rating   string   `json:"rating"`
	// Status: pending | approved | declined, or in_library once the
	// library has the title (LibraryItemID: its page).
	Status        string `json:"status"`
	Reason        string `json:"reason"` // why it was declined ("" = not said)
	LibraryItemID *int64 `json:"libraryItemId"`
	CreatedAt     string `json:"createdAt"`
	DecidedAt     string `json:"decidedAt,omitempty"`
	// User: who asked (the admins' list only).
	User *requestUser `json:"user,omitempty"`
	// Hidden: a kid's own request of a title above its limit now (the limit
	// was lowered): its title and cover are left out.
	Hidden bool `json:"hidden,omitempty"`
}

type requestUser struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

// requestState is the kidRequest field of a kid's search result and title
// page: the title's key, whether it may be asked for (Blocked: why not, in
// the kid's language) and the kid's request of it (null: none yet). A movie
// or series of the search results is Allowed until asked for: its age
// rating is known on its page (and checked when asked for).
type requestState struct {
	Key     string      `json:"key"`
	Allowed bool        `json:"allowed"`
	Blocked string      `json:"blocked,omitempty"`
	Request *KidRequest `json:"request"`
}

func mountKidRequests(r chi.Router, k *kidRequests) {
	r.Get("/api/requests/mine", k.mine)
	r.Post("/api/requests", k.ask)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/requests", k.list)
		r.Get("/api/requests/count", k.count)
		r.Post("/api/requests/{id}/approve", k.approve)
		r.Post("/api/requests/{id}/decline", k.decline)
	})
}

// foldTitle is a title as a key: case, «ё» and spacing ignored.
func foldTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == 'ё' {
			return 'е'
		}
		return r
	}, strings.ToLower(s))
	return strings.Join(strings.Fields(s), " ")
}

// requestKey names a title: its TMDB id (movies, series), its catalog id
// (albums, books), else its folded artist or author and title.
func requestKey(kind string, tmdbID int, extID, title, subtitle string) string {
	switch {
	case kind == library.KindMovie || kind == library.KindSeries:
		return "tmdb:" + strconv.Itoa(tmdbID)
	case strings.TrimSpace(extID) != "":
		return "ext:" + strings.TrimSpace(extID)
	default:
		return nameKey(kind, title, subtitle)
	}
}

// nameKey names an album, book or manga by its folded artist or author
// and title ("" for movies and series): what an admin's download without
// the catalog id is matched by.
func nameKey(kind, title, subtitle string) string {
	switch {
	case kind == library.KindMovie || kind == library.KindSeries:
		return ""
	case kind != library.KindComic && strings.TrimSpace(subtitle) != "":
		return "name:" + foldTitle(subtitle) + " — " + foldTitle(title)
	default:
		return "name:" + foldTitle(title)
	}
}

var requestKinds = map[string]bool{
	library.KindMovie: true, library.KindSeries: true, library.KindMusic: true,
	library.KindBook: true, library.KindAudiobook: true, library.KindComic: true,
}

// ratingBlock says why a kid may not ask for a video of this age rating
// ("" = it may): above the profile's limit, or no rating at all (a kid's
// library hides such video too).
func ratingBlock(ctx context.Context, u auth.User, rating string) string {
	limit := ratingLimit(u)
	if limit == "" {
		return ""
	}
	max, ok := library.RatingRank(limit)
	r, rok := library.RatingRank(rating)
	switch {
	case !ok || !rok:
		return i18n.T(ctx, "Возраст не указан, поэтому попросить нельзя")
	case r > max:
		return i18n.T(ctx, "Это %s, а в твоём профиле можно до %s", library.NormalizeRating(rating), library.NormalizeRating(limit))
	}
	return ""
}

// POST /api/requests {kind, tmdbId | extId, title, subtitle, year, poster,
// aliases} (kids) → KidRequest: the kid's request of the title, made if it
// has none (asking again changes nothing, a declined one included). A movie
// or series is taken from TMDB (its rating checked: 403 {code: "rating"});
// an album or book as the catalog result the client showed.
func (k *kidRequests) ask(w http.ResponseWriter, r *http.Request) {
	if k.open == nil || !k.open(r.Context()) {
		writeErr(w, r, http.StatusForbidden, "Просьбы здесь не принимаются")
		return
	}
	u := userOf(r)
	if !u.IsKid {
		writeErr(w, r, http.StatusForbidden, "Попросить могут только детские профили")
		return
	}
	var in struct {
		Kind     string   `json:"kind"`
		TMDBID   int      `json:"tmdbId"`
		ExtID    string   `json:"extId"`
		Title    string   `json:"title"`
		Subtitle string   `json:"subtitle"`
		Year     int      `json:"year"`
		Poster   string   `json:"poster"`
		Aliases  []string `json:"aliases"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !requestKinds[in.Kind] {
		writeErr(w, r, http.StatusBadRequest, "Неизвестный раздел")
		return
	}
	row := KidRequest{Kind: in.Kind}
	video := in.Kind == library.KindMovie || in.Kind == library.KindSeries
	if video {
		if in.TMDBID <= 0 {
			writeErr(w, r, http.StatusBadRequest, "Неизвестный фильм или сериал")
			return
		}
		row.TMDBID = in.TMDBID
	} else {
		row.ExtID, row.Title, row.Subtitle = clip(in.ExtID, 200), clip(in.Title, 300), clip(in.Subtitle, 300)
		if in.Kind == library.KindComic {
			row.Subtitle = ""
		}
		if row.Title == "" {
			writeErr(w, r, http.StatusBadRequest, "Не указано название")
			return
		}
		if in.Year > 0 && in.Year < 10000 {
			row.Year = in.Year
		}
		// a cover the admins' browsers will load: a catalog's or the server's own only
		row.Poster = k.cat.RequestPoster(row.Kind, row.ExtID, in.Poster)
		for _, a := range in.Aliases {
			if a = clip(a, 300); a != "" && len(row.Aliases) < 10 {
				row.Aliases = append(row.Aliases, a)
			}
		}
	}
	row.Key = requestKey(row.Kind, row.TMDBID, row.ExtID, row.Title, row.Subtitle)
	ctx := r.Context()
	// asked already: that request, before any catalog lookup or limit
	if got, err := k.query(ctx, `r.user_id=? AND r.kind=? AND r.title_key=?`, u.ID, row.Kind, row.Key); err != nil {
		internalErr(w, r, "ask", err)
		return
	} else if len(got) == 1 {
		writeJSON(w, http.StatusOK, k.view(ctx, got[0], u))
		return
	}
	if !k.mayAsk(u.ID) {
		writeErr(w, r, http.StatusTooManyRequests, "Подожди пару секунд и попроси ещё раз")
		return
	}
	var waiting int
	if err := k.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kid_requests WHERE user_id=? AND status=?`, u.ID, statusPending).Scan(&waiting); err != nil {
		internalErr(w, r, "ask", err)
		return
	}
	if waiting >= maxPendingPerKid {
		writeErr(w, r, http.StatusTooManyRequests, "Уже много просьб ждут ответа. Подожди, пока родители ответят")
		return
	}
	if video {
		t, err := k.cat.RequestTitle(liveTMDB(r), in.Kind, in.TMDBID)
		if err != nil {
			requestTitleErr(w, r, err)
			return
		}
		k.ratings.put(in.Kind, in.TMDBID, t.Rating, t.Adult)
		why := ratingBlock(r.Context(), u, t.Rating)
		if why == "" && t.Adult {
			why = i18n.T(r.Context(), "Недоступно для этого профиля")
		}
		if why != "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": why, "code": "rating"})
			return
		}
		row.Title, row.Year, row.Poster, row.Rating = t.Title, t.Year, t.Poster, t.Rating
	}
	aliases, _ := json.Marshal(nonNil(row.Aliases))
	res, err := k.db.ExecContext(ctx, `INSERT INTO kid_requests (user_id, kind, title_key, name_key, tmdb_id, ext_id, title, subtitle, year, poster, aliases_json, rating)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT (user_id, kind, title_key) DO NOTHING`,
		u.ID, row.Kind, row.Key, nameKey(row.Kind, row.Title, row.Subtitle), row.TMDBID, row.ExtID, row.Title, row.Subtitle, row.Year, row.Poster, string(aliases), row.Rating)
	if err != nil {
		internalErr(w, r, "ask", err)
		return
	}
	got, err := k.query(ctx, `r.user_id=? AND r.kind=? AND r.title_key=?`, u.ID, row.Kind, row.Key)
	if err != nil || len(got) != 1 {
		internalErr(w, r, "ask", errors.Join(err, errors.New("request not stored")))
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		k.publish(got[0].ID, u.ID)
	}
	writeJSON(w, http.StatusOK, k.view(ctx, got[0], u))
}

// maxPendingPerKid: a kid's requests waiting for an answer, at most (the
// admins' list stays readable however often a kid taps).
const maxPendingPerKid = 20

// kidAskInterval: a kid makes a new request at most this often.
var kidAskInterval = 2 * time.Second

// mayAsk says whether the kid may make a new request now (and counts it).
func (k *kidRequests) mayAsk(uid int64) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := time.Now()
	if last, ok := k.lastAsk[uid]; ok && now.Sub(last) < kidAskInterval {
		return false
	}
	if k.lastAsk == nil || len(k.lastAsk) > 1000 {
		k.lastAsk = map[int64]time.Time{}
	}
	k.lastAsk[uid] = now
	return true
}

// GET /api/requests/mine → the profile's requests, newest first.
func (k *kidRequests) mine(w http.ResponseWriter, r *http.Request) {
	got, err := k.query(r.Context(), `r.user_id=?`, userOf(r).ID)
	if err != nil {
		internalErr(w, r, "my requests", err)
		return
	}
	out := make([]KidRequest, 0, len(got))
	for _, x := range got {
		out = append(out, k.view(r.Context(), x, userOf(r)))
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /api/requests (admin) → the pending requests, then those decided in
// the last 30 days, each with who asked; newest first.
func (k *kidRequests) list(w http.ResponseWriter, r *http.Request) {
	since := time.Now().UTC().Add(-requestDecidedWindow).Format(time.RFC3339)
	got, err := k.query(r.Context(), `r.status=? OR r.decided_at >= ?`, statusPending, since)
	if err != nil {
		internalErr(w, r, "requests", err)
		return
	}
	pending, decided := []KidRequest{}, []KidRequest{}
	for _, x := range got {
		v := k.view(r.Context(), x, userOf(r))
		if v.Status == statusPending {
			pending = append(pending, v)
		} else {
			decided = append(decided, v)
		}
	}
	writeJSON(w, http.StatusOK, append(pending, decided...))
}

// GET /api/requests/count (admin) → {pending}: the requests waiting for an
// answer (the badge of «Просьбы»), not counting titles the library has.
func (k *kidRequests) count(w http.ResponseWriter, r *http.Request) {
	got, err := k.query(r.Context(), `r.status=?`, statusPending)
	if err != nil {
		internalErr(w, r, "request count", err)
		return
	}
	n := 0
	for _, x := range got {
		if k.view(r.Context(), x, userOf(r)).Status == statusPending {
			n++
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"pending": n})
}

// POST /api/requests/{id}/decline {reason} (admin) → KidRequest; 409 when
// it was answered already.
func (k *kidRequests) decline(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	got, qerr := k.query(ctx, `r.id=?`, id)
	if err != nil || qerr != nil || len(got) != 1 {
		if qerr != nil {
			internalErr(w, r, "decline", qerr)
			return
		}
		writeErr(w, r, http.StatusNotFound, "Просьба не найдена")
		return
	}
	res, err := k.db.ExecContext(ctx, `UPDATE kid_requests SET status=?, reason=?, decided_at=strftime('%Y-%m-%dT%H:%M:%SZ','now'), decided_by=?
		WHERE id=? AND status=?`, statusDeclined, clip(in.Reason, 200), userOf(r).ID, id, statusPending)
	if err != nil {
		internalErr(w, r, "decline", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, r, http.StatusConflict, "На эту просьбу уже ответили")
		return
	}
	if got, err = k.query(ctx, `r.id=?`, id); err != nil || len(got) != 1 {
		internalErr(w, r, "decline", errors.Join(err, errors.New("request gone")))
		return
	}
	k.publish(id, got[0].User.ID)
	writeJSON(w, http.StatusOK, k.view(ctx, got[0], userOf(r)))
}

// POST /api/requests/{id}/approve (admin) → KidRequest: a pending or
// declined request approved without a download (the admin will see to it);
// 404 when there is none, 409 when it was approved already. The kid is told
// by mail when it asked for that.
func (k *kidRequests) approve(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Просьба не найдена")
		return
	}
	ctx := r.Context()
	got, qerr := k.query(ctx, `r.id=?`, id)
	if qerr != nil {
		internalErr(w, r, "approve", qerr)
		return
	}
	if len(got) != 1 {
		writeErr(w, r, http.StatusNotFound, "Просьба не найдена")
		return
	}
	approved, err := k.approveWhere(ctx, userOf(r).ID, `id=?`, id)
	if err != nil {
		internalErr(w, r, "approve", err)
		return
	}
	if len(approved) == 0 {
		writeErr(w, r, http.StatusConflict, "На эту просьбу уже ответили")
		return
	}
	if got, err = k.query(ctx, `r.id=?`, id); err != nil || len(got) != 1 {
		internalErr(w, r, "approve", errors.Join(err, errors.New("request gone")))
		return
	}
	writeJSON(w, http.StatusOK, k.view(ctx, got[0], userOf(r)))
}

// approveWhere approves the pending and declined requests (the admin
// changed their mind) matching where, tells the kids and returns what it
// approved.
func (k *kidRequests) approveWhere(ctx context.Context, adminID int64, where string, args ...any) ([]notify.Request, error) {
	rows, err := k.db.QueryContext(ctx, `UPDATE kid_requests SET status=?, reason='', decided_at=strftime('%Y-%m-%dT%H:%M:%SZ','now'), decided_by=?
		WHERE (`+where+`) AND status IN (?,?) RETURNING id, user_id, kind, title, year, poster`,
		append([]any{statusApproved, adminID}, append(args, statusPending, statusDeclined)...)...)
	if err != nil {
		return nil, err
	}
	var approved []notify.Request
	for rows.Next() {
		var x notify.Request
		if rows.Scan(&x.ID, &x.UserID, &x.Kind, &x.Title, &x.Year, &x.Poster) == nil {
			k.publish(x.ID, x.UserID)
			approved = append(approved, x)
		}
	}
	rows.Close()
	for _, x := range approved {
		if err := k.notify.RequestApproved(ctx, x); err != nil {
			log.Printf("api: notify approval of request %d: %v", x.ID, err)
		}
	}
	return approved, rows.Err()
}

func (k *kidRequests) publish(id, userID int64) {
	if k.bus != nil {
		k.bus.Publish(events.Event{Type: events.RequestChanged, Data: map[string]int64{"id": id, "userId": userID}})
	}
}

// query reads requests (with who asked) matching where, newest first.
func (k *kidRequests) query(ctx context.Context, where string, args ...any) ([]KidRequest, error) {
	rows, err := k.db.QueryContext(ctx, `SELECT r.id, r.title_key, r.kind, r.tmdb_id, r.ext_id, r.title, r.subtitle, r.year, r.poster,
		r.aliases_json, r.rating, r.status, r.reason, r.created_at, COALESCE(r.decided_at, ''), u.id, u.name, u.avatar
		FROM kid_requests r JOIN users u ON u.id = r.user_id WHERE `+where+` ORDER BY r.created_at DESC, r.id DESC LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KidRequest
	for rows.Next() {
		var x KidRequest
		var aliases string
		u := &requestUser{}
		if err := rows.Scan(&x.ID, &x.Key, &x.Kind, &x.TMDBID, &x.ExtID, &x.Title, &x.Subtitle, &x.Year, &x.Poster,
			&aliases, &x.Rating, &x.Status, &x.Reason, &x.CreatedAt, &x.DecidedAt, &u.ID, &u.Name, &u.Avatar); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(aliases), &x.Aliases)
		x.Aliases = nonNil(x.Aliases)
		x.User = u
		out = append(out, x)
	}
	return out, rows.Err()
}

// view is a request as viewer sees it: in_library once the library has the
// title; the asker only for the admins. A kid sees no cover of an album or
// book (placeholder art), and of a movie or series above its limit now (a
// lowered limit) neither title nor cover: Hidden.
func (k *kidRequests) view(ctx context.Context, x KidRequest, viewer auth.User) KidRequest {
	if id, ok := k.cat.LibraryItemOf(ctx, x.Kind, x.TMDBID, x.Title, x.Subtitle); ok {
		x.Status, x.LibraryItemID = statusInLibrary, &id
	}
	if !viewer.IsAdmin {
		x.User = nil
	}
	if viewer.IsKid {
		if x.Kind != library.KindMovie && x.Kind != library.KindSeries {
			x.Poster = ""
		} else if !kidMaySee(ctx, viewer, x.Rating, false) {
			x.Hidden = true
			x.Title, x.Poster, x.Subtitle, x.Aliases, x.LibraryItemID = "", "", "", []string{}, nil
		}
	}
	return x
}

// mineByKey: the kid's requests by kind and key (empty on a failed read:
// only hints).
func (k *kidRequests) mineByKey(ctx context.Context, uid int64) map[string]KidRequest {
	got, err := k.query(ctx, `r.user_id=?`, uid)
	if err != nil {
		log.Printf("api: kid requests: %v", err)
	}
	m := make(map[string]KidRequest, len(got))
	for _, x := range got {
		m[x.Kind+"|"+x.Key] = x
	}
	return m
}

// ratingCache keeps the age ratings of TMDB titles for kids' searches (a
// search result has none: each is a details lookup). Failed lookups are
// not kept.
type ratingCache struct {
	mu sync.Mutex
	m  map[string]ratingEntry
}

type ratingEntry struct {
	rating string
	adult  bool
	at     time.Time
}

const (
	ratingTTL      = 24 * time.Hour
	ratingCacheMax = 4096
	ratingLookups  = 3 // at once, per search
	// kidRatingChecks: the movies and series of a kid's search whose rating
	// is looked up (TMDB lists the best matches first)
	kidRatingChecks = 15
)

func ratingCacheKey(kind string, id int) string { return kind + ":" + strconv.Itoa(id) }

func (c *ratingCache) get(kind string, id int) (ratingEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[ratingCacheKey(kind, id)]
	if !ok || time.Since(e.at) > ratingTTL {
		return ratingEntry{}, false
	}
	return e, true
}

func (c *ratingCache) put(kind string, id int, rating string, adult bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= ratingCacheMax {
		c.m = map[string]ratingEntry{}
	}
	c.m[ratingCacheKey(kind, id)] = ratingEntry{rating: rating, adult: adult, at: time.Now()}
}

// kidMaySee says whether a kid profile may see a movie or series at all
// (its title, poster, overview): not adult, and a known rating within the
// profile's limit — unrated video is hidden, as in a kid's library.
func kidMaySee(ctx context.Context, u auth.User, rating string, adult bool) bool {
	return !adult && ratingBlock(ctx, u, rating) == ""
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
