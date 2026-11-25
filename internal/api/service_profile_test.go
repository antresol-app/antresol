package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
)

type nameList []struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Service bool   `json:"service"`
}

func (l nameList) has(name string) bool {
	for _, u := range l {
		if u.Name == name {
			return true
		}
	}
	return false
}

func (e *libEnv) names(path string, c *http.Cookie) nameList {
	e.t.Helper()
	w := e.do("GET", path, "", c)
	if w.Code != 200 {
		e.t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
	}
	var l nameList
	if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l
}

// serviceProfile makes one (as `vault service-profile create` does) and signs it in.
func (e *libEnv) serviceProfile(name string) (auth.User, *http.Cookie) {
	e.t.Helper()
	u, pw, err := e.store.CreateService(context.Background(), name)
	if err != nil {
		e.t.Fatal(err)
	}
	w := e.do("POST", "/api/login", `{"userId":`+itoa(u.ID)+`,"password":"`+pw+`"}`, nil)
	c := session(w)
	if w.Code != 200 || c == nil {
		e.t.Fatalf("the service profile does not sign in: %d %s", w.Code, w.Body)
	}
	return u, c
}

// A service profile is in the sign-in picker for no one and not in the
// administrator's list of people; the administrator finds it in its own
// list, and cannot change or remove it there.
func TestServiceProfileIsHiddenFromThePeopleLists(t *testing.T) {
	e := newLibEnv(t)
	_, mom := e.profile(`{"name":"Мама"}`)
	svc, svcCookie := e.serviceProfile("playback-check")

	for _, c := range []*http.Cookie{nil, mom, e.admin, svcCookie} {
		if l := e.names("/api/profiles", c); l.has("playback-check") || !l.has("Мама") {
			t.Errorf("GET /api/profiles lists %+v", l)
		}
	}
	if l := e.names("/api/users", e.admin); l.has("playback-check") || !l.has("Мама") {
		t.Errorf("GET /api/users lists %+v", l)
	}
	// from outside the home network too (a remote picker)
	e.peer = "203.0.113.7:50000"
	if l := e.names("/api/profiles", nil); l.has("playback-check") {
		t.Errorf("a remote picker lists %+v", l)
	}
	e.peer = ""

	l := e.names("/api/service-profiles", e.admin)
	if len(l) != 1 || l[0].ID != svc.ID || !l[0].Service {
		t.Fatalf("GET /api/service-profiles: %+v", l)
	}
	if w := e.do("GET", "/api/service-profiles", "", mom); w.Code != 403 {
		t.Errorf("a person reads the service profiles: %d", w.Code)
	}
	if w := e.do("GET", "/api/service-profiles", "", nil); w.Code != 401 {
		t.Errorf("no one reads the service profiles: %d", w.Code)
	}
	// not changed, not deleted by accident
	if w := e.do("PATCH", "/api/users/"+itoa(svc.ID), `{"name":"x"}`, e.admin); w.Code != 409 {
		t.Errorf("PATCH: %d %s", w.Code, w.Body)
	}
	if w := e.do("DELETE", "/api/users/"+itoa(svc.ID), "", e.admin); w.Code != 409 {
		t.Errorf("DELETE: %d %s", w.Code, w.Body)
	}
	if l := e.names("/api/service-profiles", e.admin); len(l) != 1 || l[0].Name != "playback-check" {
		t.Errorf("after: %+v", l)
	}
}

// Its sign-in is in no list of devices.
func TestServiceProfileSignInIsInNoDeviceList(t *testing.T) {
	e := newLibEnv(t)
	svc, _ := e.serviceProfile("playback-check")
	w := e.do("GET", "/api/admin/devices", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		Items []struct {
			UserID int64 `json:"userId"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) == 0 {
		t.Fatalf("the list has not even the administrator's: %s", w.Body)
	}
	for _, d := range out.Items {
		if d.UserID == svc.ID {
			t.Errorf("the list has the service profile's device: %s", w.Body)
		}
	}
}

// It is in no family view, however its row would be read.
func TestServiceProfileIsNoFamily(t *testing.T) {
	e := newLibEnv(t)
	e.profile(`{"name":"Мама"}`)
	svc, _ := e.serviceProfile("playback-check")
	ctx := context.Background()
	admin, err := e.store.ByName(ctx, "Папа")
	if err != nil {
		list, _ := e.store.List(ctx)
		admin = list[0]
	}
	fam, err := (&lib{Users: e.store}).family(ctx, admin)
	if err != nil || len(fam) < 2 {
		t.Fatalf("family %+v %v", fam, err)
	}
	for _, m := range fam {
		if m.ID == svc.ID {
			t.Errorf("the family has the service profile: %+v", fam)
		}
	}
}

// It plays like any profile; what it reports leaves no trace, and no
// request fails over it.
func TestServiceProfilePlaysAndLeavesNoHistory(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Брат", "16+", "0123456789")
	track := e.item("track", "Группа крови")
	svc, c := e.serviceProfile("playback-check")

	e.put("/api/progress/"+itoa(movie), `{"positionMs":1000,"durationMs":4000}`, c)
	e.put("/api/progress/"+itoa(movie), `{"positionMs":2000,"durationMs":4000}`, c)
	for _, call := range []struct{ method, path, body string }{
		{"POST", "/api/items/" + itoa(movie) + "/watched", `{"watched":true}`},
		{"POST", "/api/items/" + itoa(movie) + "/watched", `{"watched":false}`},
		{"POST", "/api/watchlist/" + itoa(movie), ""},
		{"POST", "/api/music/plays", `{"trackId":` + itoa(track) + `}`},
	} {
		if w := e.do(call.method, call.path, call.body, c); w.Code != 204 {
			t.Errorf("%s %s: %d %s", call.method, call.path, w.Code, w.Body)
		}
	}
	book := e.item("book", "Мастер")
	if w := e.do("PUT", "/api/annotations/"+itoa(book)+"/h-1", `{"kind":"highlight","locator":"epubcfi(/6/4)","fraction":0.3,"color":"green"}`, c); w.Code != 200 {
		t.Errorf("a highlight: %d %s", w.Code, w.Body)
	}
	if p := e.myProgress(movie, c); p.PositionMs != 0 || p.UpdatedAt != nil {
		t.Errorf("progress was kept: %+v", p)
	}
	w := e.do("GET", "/api/home", "", c)
	var home struct {
		Shelves []shelf `json:"shelves"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &home); err != nil || w.Code != 200 {
		t.Fatalf("home: %d %v %s", w.Code, err, w.Body)
	}
	for _, sh := range home.Shelves {
		if (sh.ID == "continue" || sh.ID == "watchlist") && len(sh.Cards) != 0 {
			t.Errorf("shelf %s of the service profile has %d cards", sh.ID, len(sh.Cards))
		}
	}
	for _, table := range []string{"progress", "progress_cleared", "watchlist", "watch_time", "plays", "annotations"} {
		var n int
		if err := e.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE user_id=?`, svc.ID).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows for the service profile (%v)", table, n, err)
		}
	}
	// the administrator's own writes still land
	e.put("/api/progress/"+itoa(movie), `{"positionMs":1000,"durationMs":4000}`, e.admin)
	if p := e.myProgress(movie, e.admin); p.UpdatedAt == nil {
		t.Errorf("the administrator's progress is gone")
	}
	// and the statistics name no one for it
	w = e.do("GET", "/api/stats", "", e.admin)
	if w.Code == 200 && strings.Contains(w.Body.String(), "playback-check") {
		t.Errorf("statistics name the service profile: %s", w.Body)
	}
}
