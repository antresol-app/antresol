package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/notify"
)

// fakeRequests is a RequestCatalog over a few titles.
type fakeRequests struct {
	rating string        // the age rating of every title ("" = "6+")
	inLib  map[int]int64 // TMDB id → library item
}

func (f fakeRequests) RequestTitle(_ context.Context, kind string, id int) (RequestTitle, error) {
	if id == 404 {
		return RequestTitle{}, errUnknownTitle
	}
	if id == 503 {
		return RequestTitle{}, http.ErrHandlerTimeout
	}
	r := f.rating
	if r == "" {
		r = "6+"
	}
	return RequestTitle{Title: "Матрица", Poster: "https://image.tmdb.org/t/p/w342/m.jpg", Rating: r, Year: 1999}, nil
}
func (f fakeRequests) TitleRating(context.Context, string, int) (string, error) { return f.rating, nil }
func (f fakeRequests) RequestPoster(_, _, p string) string                      { return "" }
func (f fakeRequests) LibraryItemOf(_ context.Context, _ string, id int, _, _ string) (int64, bool) {
	it, ok := f.inLib[id]
	return it, ok
}

func newCoreRequestsEnv(t *testing.T, cat RequestCatalog, opt func(*Deps)) *libEnv {
	old := kidAskInterval
	kidAskInterval = 0
	t.Cleanup(func() { kidAskInterval = old })
	return newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		d.Requests = cat
		d.RequestsOpen = func(context.Context) bool { return true }
		if opt != nil {
			opt(d)
		}
	})
}

type coreRequest struct {
	ID            int64  `json:"id"`
	Status        string `json:"status"`
	Title         string `json:"title"`
	Rating        string `json:"rating"`
	LibraryItemID *int64 `json:"libraryItemId"`
}

func (e *libEnv) askMovie(kid *http.Cookie, id string) coreRequest {
	e.t.Helper()
	w := e.do("POST", "/api/requests", `{"kind":"movie","tmdbId":`+id+`}`, kid)
	if w.Code != 200 {
		e.t.Fatalf("ask %s: %d %s", id, w.Code, w.Body)
	}
	return decodeJSON[coreRequest](e.t, w.Body.Bytes())
}

func (e *libEnv) pending(t *testing.T) int {
	t.Helper()
	w := e.do("GET", "/api/requests/count", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("count: %d %s", w.Code, w.Body)
	}
	return decodeJSON[struct {
		Pending int `json:"pending"`
	}](t, w.Body.Bytes()).Pending
}

// A kid asks, the admin approves without any download, the kid sees it.
func TestKidAskAndAdminApprove(t *testing.T) {
	e := newCoreRequestsEnv(t, fakeRequests{}, nil)
	kid := e.kid("")
	got := e.askMovie(kid, "603")
	if got.Status != "pending" || got.Title != "Матрица" {
		t.Fatalf("ask: %+v", got)
	}
	if n := e.pending(t); n != 1 {
		t.Fatalf("pending %d", n)
	}
	id := itoa(got.ID)
	if w := e.do("POST", "/api/requests/"+id+"/approve", "", kid); w.Code != http.StatusForbidden {
		t.Errorf("kid approves: %d", w.Code)
	}
	w := e.do("POST", "/api/requests/"+id+"/approve", "", e.admin)
	if w.Code != 200 || decodeJSON[coreRequest](t, w.Body.Bytes()).Status != "approved" {
		t.Fatalf("approve: %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/requests/mine", "", kid)
	if mine := decodeJSON[[]coreRequest](t, w.Body.Bytes()); len(mine) != 1 || mine[0].Status != "approved" {
		t.Fatalf("mine: %s", w.Body)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("pending after approval %d", n)
	}
	if w := e.do("POST", "/api/requests/"+id+"/approve", "", e.admin); w.Code != http.StatusConflict {
		t.Errorf("second approve: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/requests/9999/approve", "", e.admin); w.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", w.Code)
	}
	// an approved request can no longer be declined; a declined one can be approved
	if w := e.do("POST", "/api/requests/"+id+"/decline", `{}`, e.admin); w.Code != http.StatusConflict {
		t.Errorf("decline approved: %d", w.Code)
	}
	other := e.askMovie(kid, "604")
	if w := e.do("POST", "/api/requests/"+itoa(other.ID)+"/decline", `{"reason":"потом"}`, e.admin); w.Code != 200 {
		t.Fatalf("decline: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/requests/"+itoa(other.ID)+"/approve", "", e.admin); w.Code != 200 {
		t.Errorf("approve declined: %d %s", w.Code, w.Body)
	}
}

func TestKidAskRatingLimitCore(t *testing.T) {
	e := newCoreRequestsEnv(t, fakeRequests{rating: "18+"}, nil)
	kid := e.kid("12+")
	w := e.do("POST", "/api/requests", `{"kind":"movie","tmdbId":603}`, kid)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"rating"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if e.pending(t) != 0 {
		t.Fatal("stored")
	}
}

// The title lookup failing: 404 for a title TMDB does not know, 503 else.
func TestKidAskTitleErrors(t *testing.T) {
	e := newCoreRequestsEnv(t, fakeRequests{}, nil)
	kid := e.kid("")
	if w := e.do("POST", "/api/requests", `{"kind":"movie","tmdbId":404}`, kid); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "нет в TMDB") {
		t.Errorf("unknown: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/requests", `{"kind":"movie","tmdbId":503}`, kid); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "TMDB сейчас недоступен") {
		t.Errorf("down: %d %s", w.Code, w.Body)
	}
}

func TestApproveQueuesKidLetter(t *testing.T) {
	var svc *notify.Service
	e := newCoreRequestsEnv(t, fakeRequests{}, func(d *Deps) {
		svc = &notify.Service{DB: d.DB, Users: d.Users, Secret: []byte("s"), RequestsOpen: func(context.Context) bool { return true }}
		d.Notify = svc
	})
	kid := e.kid("")
	var kidID int64
	if err := e.db.QueryRow(`SELECT id FROM users WHERE is_kid=1`).Scan(&kidID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetPrefs(context.Background(), auth.User{ID: kidID, IsKid: true}, notify.Prefs{Email: "kid@example.com", Requests: true}); err != nil {
		t.Fatal(err)
	}
	got := e.askMovie(kid, "603")
	if w := e.do("POST", "/api/requests/"+itoa(got.ID)+"/approve", "", e.admin); w.Code != 200 {
		t.Fatalf("approve: %d %s", w.Code, w.Body)
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM notify_events WHERE user_id=? AND topic='requests' AND key=?`, kidID, "req:"+itoa(got.ID)+":approved").Scan(&n)
	if n != 1 {
		t.Fatalf("approval events %d", n)
	}
}

func TestRequestInLibraryDerived(t *testing.T) {
	inLib := map[int]int64{}
	e := newCoreRequestsEnv(t, fakeRequests{inLib: inLib}, nil)
	kid := e.kid("")
	got := e.askMovie(kid, "603")
	inLib[603] = 42 // the scanner found the movie
	w := e.do("GET", "/api/requests/mine", "", kid)
	mine := decodeJSON[[]coreRequest](t, w.Body.Bytes())
	if len(mine) != 1 || mine[0].ID != got.ID || mine[0].Status != "in_library" || mine[0].LibraryItemID == nil || *mine[0].LibraryItemID != 42 {
		t.Fatalf("mine: %s", w.Body)
	}
}

func TestMeRequestsFlags(t *testing.T) {
	type flags struct {
		Requests   bool `json:"requests"`
		CanRequest bool `json:"canRequest"`
	}
	me := func(e *libEnv, c *http.Cookie) flags {
		w := e.do("GET", "/api/me", "", c)
		return decodeJSON[flags](t, w.Body.Bytes())
	}
	closed := newCoreRequestsEnv(t, fakeRequests{}, func(d *Deps) { d.RequestsOpen = nil })
	if got := me(closed, closed.admin); got.Requests || got.CanRequest {
		t.Errorf("closed admin %+v", got)
	}
	if got := me(closed, closed.kid("")); got.Requests || got.CanRequest {
		t.Errorf("closed kid %+v", got)
	}
	open := newCoreRequestsEnv(t, fakeRequests{}, func(d *Deps) { d.RequestsOpen = func(context.Context) bool { return true } })
	if got := me(open, open.admin); !got.Requests || got.CanRequest {
		t.Errorf("admin %+v", got)
	}
	if got := me(open, open.kid("")); got.Requests || !got.CanRequest {
		t.Errorf("kid %+v", got)
	}
	adult := open.login(`{"name":"Мама"}`)
	if got := me(open, adult); got.Requests || got.CanRequest {
		t.Errorf("adult %+v", got)
	}
	// open without a catalog: nothing is offered
	noCat := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		d.RequestsOpen = func(context.Context) bool { return true }
	})
	if got := me(noCat, noCat.admin); got.Requests || got.CanRequest {
		t.Errorf("no catalog %+v", got)
	}
}

// countingCatalog counts the TMDB lookups of a fakeRequests.
type countingCatalog struct {
	fakeRequests
	lookups int
}

func (c *countingCatalog) RequestTitle(ctx context.Context, kind string, id int) (RequestTitle, error) {
	c.lookups++
	return c.fakeRequests.RequestTitle(ctx, kind, id)
}

// Requests not open (RequestsOpen nil or false): ask answers 403 and neither
// calls the catalog nor stores anything.
func TestKidAskClosed(t *testing.T) {
	for name, open := range map[string]func(context.Context) bool{
		"nil":   nil,
		"false": func(context.Context) bool { return false },
	} {
		t.Run(name, func(t *testing.T) {
			cat := &countingCatalog{}
			e := newCoreRequestsEnv(t, cat, func(d *Deps) { d.RequestsOpen = open })
			kid := e.kid("")
			w := e.do("POST", "/api/requests", `{"kind":"movie","tmdbId":603}`, kid)
			if w.Code != http.StatusForbidden {
				t.Fatalf("ask: %d %s", w.Code, w.Body)
			}
			if cat.lookups != 0 {
				t.Errorf("catalog called %d times", cat.lookups)
			}
			var n int
			if err := e.db.QueryRow(`SELECT COUNT(*) FROM kid_requests`).Scan(&n); err != nil || n != 0 {
				t.Errorf("stored %d (%v)", n, err)
			}
		})
	}
}

// A malformed id is answered 404 before any query runs.
func TestApproveBadID(t *testing.T) {
	e := newCoreRequestsEnv(t, fakeRequests{}, nil)
	for _, id := range []string{"abc", "1x", "99999999999999999999"} {
		if w := e.do("POST", "/api/requests/"+id+"/approve", "", e.admin); w.Code != http.StatusNotFound {
			t.Errorf("approve %q: %d %s", id, w.Code, w.Body)
		}
	}
}
