package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

// newFlowEnv is a server with no addon at all: movies and series from a fake
// TMDB, the library's own repo, and «Запросы детей» as the settings switch
// it (Deps.RequestsOpen is RequestsSwitch.Open, ApplySettings applies it),
// as the server wires them.
func newFlowEnv(t *testing.T) *libEnv {
	old := kidAskInterval
	kidAskInterval = 0
	t.Cleanup(func() { kidAskInterval = old })
	tm := newTitleTMDB(t, "6", false)
	sw := &RequestsSwitch{}
	return newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB, d.Titles = db, tm.client
		d.Requests = TMDBRequests(tm.client, d.Library)
		d.RequestsOpen, d.ApplySettings = sw.Open, sw.Apply
	})
}

func (e *libEnv) setKidRequests(on string) {
	e.t.Helper()
	if w := e.do("PUT", "/api/settings", `{"kidRequests":`+on+`}`, e.admin); w.Code != 200 {
		e.t.Fatalf("settings: %d %s", w.Code, w.Body)
	}
}

type meRequestFlags struct {
	CanRequest bool `json:"canRequest"`
	Requests   bool `json:"requests"`
}

func (e *libEnv) meRequests(c *http.Cookie) meRequestFlags {
	e.t.Helper()
	w := e.do("GET", "/api/me", "", c)
	if w.Code != 200 {
		e.t.Fatalf("me: %d %s", w.Code, w.Body)
	}
	return decodeJSON[meRequestFlags](e.t, w.Body.Bytes())
}

// Kids' requests need no addon: a kid finds a title the library lacks, opens
// its page, asks; the admin approves with nothing to download; the request
// turns «in_library» by itself once the library has the TMDB title.
func TestKidRequestsWithoutAddon(t *testing.T) {
	e := newFlowEnv(t)
	kid := e.kid("")
	if got := e.meRequests(kid); !got.CanRequest || got.Requests {
		t.Fatalf("kid %+v", got)
	}
	if got := e.meRequests(e.admin); got.CanRequest || !got.Requests {
		t.Fatalf("admin %+v", got)
	}
	// the search: TMDB's movies, each with its request state
	w := e.do("GET", "/api/requests/search?kind=video&q=дюна", "", kid)
	var hits []struct {
		TMDBID     int           `json:"tmdbId"`
		KidRequest *requestState `json:"kidRequest"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &hits) != nil || len(hits) != 1 || hits[0].TMDBID != 7 ||
		hits[0].KidRequest == nil || !hits[0].KidRequest.Allowed || hits[0].KidRequest.Request != nil {
		t.Fatalf("search: %d %s", w.Code, w.Body)
	}
	// the page of a title outside the library
	page := e.title(kid, "/api/title/movie/7")
	if page.InLibrary || page.KidRequest == nil || page.KidRequest.Request != nil {
		t.Fatalf("title: %+v", page)
	}
	asked := e.askMovie(kid, "7")
	if asked.Status != "pending" || asked.Title != "Дюна" {
		t.Fatalf("ask: %+v", asked)
	}
	if got := e.title(kid, "/api/title/movie/7"); got.KidRequest == nil || got.KidRequest.Request == nil || got.KidRequest.Request.ID != asked.ID {
		t.Fatalf("title after asking: %+v", got)
	}
	// the admin approves: nothing is downloaded, the request waits for the library
	if w := e.do("POST", "/api/requests/"+itoa(asked.ID)+"/approve", "", e.admin); w.Code != 200 ||
		decodeJSON[coreRequest](t, w.Body.Bytes()).Status != "approved" {
		t.Fatalf("approve: %d %s", w.Code, w.Body)
	}
	mine := func() coreRequest {
		w := e.do("GET", "/api/requests/mine", "", kid)
		got := decodeJSON[[]coreRequest](t, w.Body.Bytes())
		if w.Code != 200 || len(got) != 1 {
			t.Fatalf("mine: %d %s", w.Code, w.Body)
		}
		return got[0]
	}
	if got := mine(); got.Status != "approved" || got.LibraryItemID != nil {
		t.Fatalf("mine before the library has it: %+v", got)
	}
	// the title arrives (a scan: matched by its TMDB id)
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "Dune", TmdbID: 7,
		File: library.FileInput{Path: "/m/Dune (2024)/Dune.mkv", Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if got := mine(); got.Status != "in_library" || got.LibraryItemID == nil || *got.LibraryItemID != id {
		t.Fatalf("mine after the scan: %+v", got)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("pending %d", n)
	}
}

// «Запросы детей» is on by default and the admin turns it off and on
// (PUT /api/settings): off closes everything the kid sees, nothing else.
func TestKidRequestsSetting(t *testing.T) {
	e := newFlowEnv(t)
	kid := e.kid("")
	w := e.do("GET", "/api/settings", "", e.admin)
	if got := decodeJSON[struct {
		KidRequests *bool `json:"kidRequests"`
	}](t, w.Body.Bytes()); got.KidRequests == nil || !*got.KidRequests {
		t.Fatalf("default: %s", w.Body)
	}
	asked := e.askMovie(kid, "7")

	e.setKidRequests("false")
	if got := e.meRequests(kid); got.CanRequest {
		t.Errorf("kid while off: %+v", got)
	}
	// the admin keeps «Запросы»: the list, its count and the switch
	if got := e.meRequests(e.admin); !got.Requests {
		t.Errorf("admin while off: %+v", got)
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"ask":    e.do("POST", "/api/requests", `{"kind":"movie","tmdbId":8}`, kid),
		"search": e.do("GET", "/api/requests/search?kind=video&q=дюна", "", kid),
		"title":  e.do("GET", "/api/title/movie/7", "", kid),
	} {
		if w.Code != http.StatusForbidden {
			t.Errorf("%s while off: %d %s", name, w.Code, w.Body)
		}
	}
	if w := e.do("GET", "/api/requests", "", e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":`+itoa(asked.ID)) {
		t.Errorf("admin list while off: %d %s", w.Code, w.Body)
	}
	// a PUT that leaves the field out keeps it off; the library still plays
	if w := e.do("PUT", "/api/settings", `{"remoteQuality":"720"}`, e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"kidRequests":false`) {
		t.Fatalf("other setting: %d %s", w.Code, w.Body)
	}
	if got := e.meRequests(kid); got.CanRequest {
		t.Errorf("kid after another setting: %+v", got)
	}

	e.setKidRequests("true")
	if got := e.meRequests(kid); !got.CanRequest {
		t.Errorf("kid after turning it on: %+v", got)
	}
	if w := e.do("GET", "/api/title/movie/7", "", kid); w.Code != 200 {
		t.Errorf("title after turning it on: %d %s", w.Code, w.Body)
	}
}

// RequestsSwitch: open until the settings say off.
func TestRequestsSwitch(t *testing.T) {
	var s RequestsSwitch
	if !s.Open(context.Background()) {
		t.Fatal("a new switch is closed")
	}
	s.Apply(AppSettings{})
	if s.Open(context.Background()) {
		t.Fatal("open with the setting off")
	}
	s.Apply(AppSettings{KidRequests: true})
	if !s.Open(context.Background()) {
		t.Fatal("closed with the setting on")
	}
}
