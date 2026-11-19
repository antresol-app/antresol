package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
)

type historyOut struct {
	ID      int64 `json:"id"`
	History *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"history"`
}

// tv signs a TV in as the admin (profile 1) and returns its cookie.
func (e *libEnv) tv(id string) *http.Cookie {
	e.t.Helper()
	return &http.Cookie{Name: "vault_session", Value: e.deviceLogin(1, "secret", id)}
}

func (e *libEnv) switchHistory(c *http.Cookie, device, body string) historyOut {
	e.t.Helper()
	return e.switchHistoryAt("/api/me/devices/", c, device, body)
}

// switchHistoryAt: under /api/me/devices/ (the caller's own) or /api/admin/devices/ (any profile's).
func (e *libEnv) switchHistoryAt(base string, c *http.Cookie, device, body string) historyOut {
	e.t.Helper()
	w := e.do("PUT", base+device+"/history", body, c)
	if w.Code != 200 {
		e.t.Fatalf("switch %s: %d %s", body, w.Code, w.Body)
	}
	return decodeJSON[historyOut](e.t, w.Body.Bytes())
}

func (e *libEnv) progressOf(c *http.Cookie, item int64) famProgress {
	e.t.Helper()
	w := e.do("GET", "/api/progress/"+itoa(item), "", c)
	if w.Code != 200 {
		e.t.Fatalf("progress %d %s", w.Code, w.Body)
	}
	return decodeJSON[famProgress](e.t, w.Body.Bytes())
}

func TestSeparateHistoryRoutesHistoryNotChoices(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Film", "", "x")
	other, _ := e.movie("Other", "", "y")
	tv := e.tv("tv-living-01")

	on := e.switchHistory(tv, "current", `{"separate":true}`)
	if on.History == nil || on.History.Name == "" {
		t.Fatalf("on: %+v", on)
	}
	// /api/me names the device's history
	if w := e.do("GET", "/api/me", "", tv); !strings.Contains(w.Body.String(), `"deviceHistory":{"id":`+itoa(on.History.ID)) {
		t.Fatalf("me %s", w.Body)
	}
	if w := e.do("GET", "/api/me", "", e.admin); !strings.Contains(w.Body.String(), `"deviceHistory":null`) {
		t.Fatalf("admin me %s", w.Body)
	}

	// history goes to the TV's account
	e.put("/api/progress/"+itoa(movie), `{"positionMs":60000,"durationMs":6000000}`, tv)
	if w := e.do("POST", "/api/items/"+itoa(other)+"/watched", `{"watched":true}`, tv); w.Code != 204 {
		t.Fatalf("watched %d", w.Code)
	}
	if p := e.progressOf(tv, movie); p.PositionMs != 60000 {
		t.Fatalf("tv progress %+v", p)
	}
	if p := e.progressOf(e.admin, movie); p.PositionMs != 0 || p.UpdatedAt != nil {
		t.Fatalf("person got the TV's progress %+v", p)
	}
	if p := e.progressOf(e.admin, other); p.Finished {
		t.Fatal("person got the TV's watched mark")
	}
	home := func(c *http.Cookie) string {
		w := e.do("GET", "/api/home", "", c)
		if w.Code != 200 {
			t.Fatalf("home %d %s", w.Code, w.Body)
		}
		return w.Body.String()
	}
	if !strings.Contains(home(tv), `"Film"`) {
		t.Fatal("TV «Продолжить» misses its film")
	}
	var h struct {
		Shelves []struct {
			ID    string `json:"id"`
			Items []Card `json:"items"`
		} `json:"shelves"`
	}
	json.Unmarshal([]byte(home(e.admin)), &h)
	for _, s := range h.Shelves {
		if s.ID == "continue" && len(s.Items) != 0 {
			t.Fatalf("person's «Продолжить» has the TV's: %+v", s.Items)
		}
	}
	// the watchlist is the person's, from any device
	if w := e.do("POST", "/api/watchlist/"+itoa(other), "", tv); w.Code != 204 {
		t.Fatalf("watchlist %d", w.Code)
	}
	ids, _ := e.prog.Watchlist(t.Context(), 1)
	if len(ids) != 1 || ids[0] != other {
		t.Fatalf("person's watchlist %v", ids)
	}

	// the person sees the TV in the family view, and the TV sees the person
	e.put("/api/progress/"+itoa(movie), `{"positionMs":120000,"durationMs":6000000}`, e.admin)
	fam := e.familyOf(movie, e.admin)
	if len(fam.Members) != 1 || fam.Members[0].UserID != on.History.ID || fam.Members[0].OwnerName == "" {
		t.Fatalf("person's family %+v", fam)
	}
	fam = e.familyOf(movie, tv)
	if len(fam.Members) != 1 || fam.Members[0].UserID != 1 {
		t.Fatalf("TV's family %+v", fam)
	}

	// the managed account is no profile to sign in to
	if w := e.do("GET", "/api/profiles", "", nil); strings.Contains(w.Body.String(), on.History.Name) {
		t.Fatalf("picker shows it: %s", w.Body)
	}
	if w := e.do("GET", "/api/users", "", e.admin); strings.Contains(w.Body.String(), on.History.Name) {
		t.Fatalf("/api/users shows it: %s", w.Body)
	}
	if w := e.do("POST", "/api/login", `{"userId":`+itoa(on.History.ID)+`}`, nil); w.Code == 200 {
		t.Fatal("signed in to a managed account")
	}

	// off with «Перенести в мой профиль»: the TV's newer mark moves, the
	// person's newer position stays; the account is gone
	off := e.switchHistory(tv, "current", `{"separate":false,"merge":true}`)
	if off.History != nil {
		t.Fatalf("off: %+v", off)
	}
	if p := e.progressOf(e.admin, other); !p.Finished {
		t.Fatal("merged watched mark missing")
	}
	if p := e.progressOf(e.admin, movie); p.PositionMs != 120000 {
		t.Fatalf("person's newer position lost: %+v", p)
	}
	if w := e.do("GET", "/api/me/managed", "", e.admin); strings.Contains(w.Body.String(), on.History.Name) {
		t.Fatalf("merged account still listed: %s", w.Body)
	}
}

func TestSeparateHistoryKeepReusesAccount(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Film", "", "x")
	tv := e.tv("tv-living-02")
	on := e.switchHistory(tv, "current", `{"separate":true}`)
	e.put("/api/progress/"+itoa(movie), `{"positionMs":60000,"durationMs":6000000}`, tv)
	if off := e.switchHistory(tv, "current", `{"separate":false}`); off.History != nil {
		t.Fatalf("off %+v", off)
	}
	if p := e.progressOf(tv, movie); p.PositionMs != 0 {
		t.Fatalf("off still reads the TV's history %+v", p)
	}
	if p := e.progressOf(e.admin, movie); p.PositionMs != 0 {
		t.Fatalf("«Оставить» merged %+v", p)
	}
	again := e.switchHistory(tv, "current", `{"separate":true}`)
	if again.History == nil || again.History.ID != on.History.ID {
		t.Fatalf("again %+v, was %+v", again, on)
	}
	if p := e.progressOf(tv, movie); p.PositionMs != 60000 {
		t.Fatalf("kept history lost %+v", p)
	}
}

func TestManagedAccountsAPI(t *testing.T) {
	e := newLibEnv(t)
	mamaID, mama := e.profile(`{"name":"Mama"}`)
	w := e.do("POST", "/api/me/managed", `{"name":"Гостиная"}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var m struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	json.Unmarshal(w.Body.Bytes(), &m)
	if w := e.do("POST", "/api/me/managed", `{"name":"Гостиная"}`, e.admin); w.Code != 409 {
		t.Fatalf("dup %d", w.Code)
	}
	// another person's account may have the name: names are per person
	if w := e.do("POST", "/api/me/managed", `{"name":"Гостиная"}`, mama); w.Code != 201 {
		t.Fatalf("same name, other person %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/me/managed", `{"name":"  "}`, mama); w.Code != 400 {
		t.Fatalf("empty %d", w.Code)
	}
	// someone else's: not found
	if w := e.do("PATCH", "/api/me/managed/"+itoa(m.ID), `{"name":"x"}`, mama); w.Code != 404 {
		t.Fatalf("rename other %d", w.Code)
	}
	if w := e.do("DELETE", "/api/me/managed/"+itoa(m.ID), "", mama); w.Code != 404 {
		t.Fatalf("delete other %d", w.Code)
	}
	if w := e.do("PATCH", "/api/me/managed/"+itoa(m.ID), `{"name":"Кухня"}`, e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), "Кухня") {
		t.Fatalf("rename %d %s", w.Code, w.Body)
	}
	// a person's own device picks the account by hand
	tv := e.tv("tv-kitchen-01")
	if got := e.switchHistory(tv, "current", `{"separate":true,"accountId":`+itoa(m.ID)+`}`); got.History == nil || got.History.ID != m.ID {
		t.Fatalf("by hand %+v", got)
	}
	// Mama cannot switch the admin's device, nor use his account
	devs := e.do("GET", "/api/me/devices", "", e.admin)
	var list struct {
		Items []struct {
			ID      int64 `json:"id"`
			UserID  int64 `json:"userId"`
			History *struct {
				ID int64 `json:"id"`
			} `json:"history"`
		} `json:"items"`
	}
	json.Unmarshal(devs.Body.Bytes(), &list)
	var tvID int64
	for _, d := range list.Items {
		if d.History != nil && d.History.ID == m.ID {
			tvID = d.ID
		}
	}
	if tvID == 0 {
		t.Fatalf("devices list has no history: %s", devs.Body)
	}
	if w := e.do("PUT", "/api/me/devices/"+itoa(tvID)+"/history", `{"separate":false}`, mama); w.Code != 404 {
		t.Fatalf("other's device %d", w.Code)
	}
	if w := e.do("PUT", "/api/me/devices/current/history", `{"separate":true,"accountId":`+itoa(m.ID)+`}`, mama); w.Code != 404 {
		t.Fatalf("other's account %d", w.Code)
	}
	mine := e.do("GET", "/api/me/managed", "", e.admin)
	if !strings.Contains(mine.Body.String(), `"devices":[{"id":`+itoa(tvID)) {
		t.Fatalf("managed list %s", mine.Body)
	}
	// the admin's view lists everyone's; Mama is no admin
	if w := e.do("GET", "/api/managed-accounts", "", mama); w.Code != 403 {
		t.Fatalf("mama admin view %d", w.Code)
	}
	e.do("POST", "/api/me/managed", `{"name":"Спальня"}`, mama)
	all := e.do("GET", "/api/managed-accounts", "", e.admin)
	if all.Code != 200 || !strings.Contains(all.Body.String(), "Спальня") || !strings.Contains(all.Body.String(), `"owner":{"id":`+itoa(mamaID)) {
		t.Fatalf("admin view %d %s", all.Code, all.Body)
	}
	// deleting sends the device back to the person's history
	if w := e.do("DELETE", "/api/me/managed/"+itoa(m.ID), "", e.admin); w.Code != 204 {
		t.Fatalf("delete %d", w.Code)
	}
	if w := e.do("GET", "/api/me", "", tv); !strings.Contains(w.Body.String(), `"deviceHistory":null`) {
		t.Fatalf("after delete %s", w.Body)
	}
}

// currentDevice is the id of the session c is signed in with.
func (e *libEnv) currentDevice(c *http.Cookie) int64 {
	e.t.Helper()
	w := e.do("GET", "/api/me/devices", "", c)
	var list struct {
		Items []struct {
			ID      int64 `json:"id"`
			Current bool  `json:"current"`
		} `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	for _, d := range list.Items {
		if d.Current {
			return d.ID
		}
	}
	e.t.Fatalf("no current device: %s", w.Body)
	return 0
}

func TestKidDeviceHistoryKeepsKidLimits(t *testing.T) {
	e := newLibEnv(t)
	adult, _ := e.movie("Adult", "18+", "x")
	_, kid := e.profile(`{"name":"Kid","isKid":true,"maxRating":"6+"}`)
	// a kid's device is switched by an administrator
	on := e.switchHistoryAt("/api/admin/devices/", e.admin, itoa(e.currentDevice(kid)), `{"separate":true}`)
	if on.History == nil {
		t.Fatal("kid switch")
	}
	if w := e.do("GET", "/api/items/"+itoa(adult), "", kid); w.Code != 404 && w.Code != 403 {
		t.Fatalf("kid with separate history opened an 18+ title: %d", w.Code)
	}
	u, err := e.store.Get(t.Context(), on.History.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !u.IsKid || u.MaxRating == nil || *u.MaxRating != "6+" {
		t.Fatalf("kid's managed account %+v", u)
	}
}

// A kid cannot change its separate histories (switch, create, rename,
// delete, merge): that would erase what the family view shows of what it
// watched. An administrator can.
func TestKidCannotChangeSeparateHistories(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Film", "6+", "x")
	kidID, kid := e.profile(`{"name":"Kid","isKid":true,"maxRating":"12+"}`)
	dev := itoa(e.currentDevice(kid))

	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/api/me/devices/current/history", `{"separate":true}`},
		{"PUT", "/api/me/devices/" + dev + "/history", `{"separate":true}`},
		{"POST", "/api/me/managed", `{"name":"Детская"}`},
	} {
		if w := e.do(c.method, c.path, c.body, kid); w.Code != 403 {
			t.Fatalf("kid %s %s %s: %d", c.method, c.path, c.body, w.Code)
		}
	}
	if list, _ := e.store.ManagedOf(t.Context(), kidID); len(list) != 0 {
		t.Fatalf("a kid made %+v", list)
	}

	// the administrator switches the kid's device on, from the server's
	// list of devices (not as one of the admin's own); the kid's history goes there
	if w := e.do("PUT", "/api/me/devices/"+dev+"/history", `{"separate":true}`, e.admin); w.Code != 404 {
		t.Fatalf("a kid's device as the admin's own: %d", w.Code)
	}
	on := e.switchHistoryAt("/api/admin/devices/", e.admin, dev, `{"separate":true}`)
	if on.History == nil {
		t.Fatal("admin could not switch a kid's device")
	}
	acc := itoa(on.History.ID)
	e.put("/api/progress/"+itoa(movie), `{"positionMs":60000,"durationMs":6000000}`, kid)
	if w := e.do("GET", "/api/me/managed", "", kid); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":`+acc) {
		t.Fatalf("a kid reads its accounts: %d %s", w.Code, w.Body)
	}
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/api/me/devices/current/history", `{"separate":false,"merge":true}`},
		{"PUT", "/api/me/devices/current/history", `{"separate":false}`},
		{"PATCH", "/api/me/managed/" + acc, `{"name":"Мой"}`},
		{"POST", "/api/me/managed/" + acc + "/merge", ``},
		{"DELETE", "/api/me/managed/" + acc, ``},
	} {
		if w := e.do(c.method, c.path, c.body, kid); w.Code != 403 {
			t.Fatalf("kid %s %s %s: %d", c.method, c.path, c.body, w.Code)
		}
	}
	// nothing changed: the switch is on, the account and its history are there
	if w := e.do("GET", "/api/me", "", kid); !strings.Contains(w.Body.String(), `"deviceHistory":{"id":`+acc) {
		t.Fatalf("the switch changed: %s", w.Body)
	}
	if p := e.progressOf(kid, movie); p.PositionMs != 60000 {
		t.Fatalf("history lost %+v", p)
	}

	// the administrator turns it off with «Перенести»: the kid's own history gets it
	off := e.switchHistoryAt("/api/admin/devices/", e.admin, dev, `{"separate":false,"merge":true}`)
	if off.History != nil {
		t.Fatalf("off: %+v", off)
	}
	if list, _ := e.store.ManagedOf(t.Context(), kidID); len(list) != 0 {
		t.Fatalf("account left after the merge: %+v", list)
	}
	if p := e.progressOf(kid, movie); p.PositionMs != 60000 {
		t.Fatalf("merged history missing %+v", p)
	}
	// and deletes an account with its history
	on = e.switchHistoryAt("/api/admin/devices/", e.admin, dev, `{"separate":true}`)
	e.put("/api/progress/"+itoa(movie), `{"positionMs":90000,"durationMs":6000000}`, kid)
	if w := e.do("DELETE", "/api/managed-accounts/"+itoa(on.History.ID), "", e.admin); w.Code != 204 {
		t.Fatalf("admin delete %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me", "", kid); !strings.Contains(w.Body.String(), `"deviceHistory":null`) {
		t.Fatalf("after delete %s", w.Body)
	}
}

// «Перенести в мой профиль» for a whole account switches its devices off
// before anything moves, and merges and deletes in one go: a write from a
// TV that is still playing lands in the person's history, never in the
// account that is going away.
func TestMergeSwitchesDevicesOffBeforeItMoves(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Film", "", "x")
	tv := e.tv("tv-living-03")
	on := e.switchHistory(tv, "current", `{"separate":true}`)
	e.put("/api/progress/"+itoa(movie), `{"positionMs":60000,"durationMs":6000000}`, tv)

	// what the device mapping says when the account row goes
	for _, q := range []string{
		`CREATE TABLE zz_at_delete(enabled INTEGER)`,
		`CREATE TRIGGER zz_before_delete BEFORE DELETE ON users WHEN OLD.owner_id IS NOT NULL
		 BEGIN INSERT INTO zz_at_delete SELECT enabled FROM device_history WHERE managed_id = OLD.id; END`,
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if w := e.do("POST", "/api/me/managed/"+itoa(on.History.ID)+"/merge", "", e.admin); w.Code != 204 {
		t.Fatalf("merge %d %s", w.Code, w.Body)
	}
	var total, stillOn int
	e.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(enabled), 0) FROM zz_at_delete`).Scan(&total, &stillOn)
	if total != 1 || stillOn != 0 {
		t.Fatalf("the device was still switched on when the account was deleted: %d rows, %d on", total, stillOn)
	}
	if p := e.progressOf(e.admin, movie); p.PositionMs != 60000 {
		t.Fatalf("merged position %+v", p)
	}
	// the TV's next write is the person's
	e.put("/api/progress/"+itoa(movie), `{"positionMs":70000,"durationMs":6000000}`, tv)
	if p := e.progressOf(e.admin, movie); p.PositionMs != 70000 {
		t.Fatalf("a write after the merge did not reach the person: %+v", p)
	}
	if w := e.do("GET", "/api/me", "", tv); !strings.Contains(w.Body.String(), `"deviceHistory":null`) {
		t.Fatalf("tv after merge %s", w.Body)
	}
}

// Two «on» requests at once (a double tap, an app that retries) make one
// account.
func TestSeparateHistoryOnTwiceAtOnce(t *testing.T) {
	e := newLibEnv(t)
	tv := e.tv("tv-living-04")
	const n = 6
	codes := make([]int, n)
	bodies := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := e.do("PUT", "/api/me/devices/current/history", `{"separate":true}`, tv)
			codes[i], bodies[i] = w.Code, w.Body.String()
		}()
	}
	wg.Wait()
	var first historyOut
	for i := range n {
		if codes[i] != 200 {
			t.Fatalf("request %d: %d %s", i, codes[i], bodies[i])
		}
		var got historyOut
		if err := json.Unmarshal([]byte(bodies[i]), &got); err != nil || got.History == nil {
			t.Fatalf("request %d: %s %v", i, bodies[i], err)
		}
		if i == 0 {
			first = got
		} else if got.History.ID != first.History.ID {
			t.Fatalf("two accounts: %d and %d", first.History.ID, got.History.ID)
		}
	}
	if list, _ := e.store.ManagedOf(t.Context(), 1); len(list) != 1 {
		t.Fatalf("accounts %+v", list)
	}
}

// A managed account's name does not take a real profile's name.
func TestManagedNameDoesNotBlockAProfile(t *testing.T) {
	e := newLibEnv(t)
	if w := e.do("POST", "/api/me/managed", `{"name":"Гостиная"}`, e.admin); w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/users", `{"name":"Гостиная"}`, e.admin); w.Code != 201 {
		t.Fatalf("a profile of that name: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/users", `{"name":"Гостиная"}`, e.admin); w.Code != 409 {
		t.Fatalf("a second profile of that name: %d", w.Code)
	}
	if w := e.do("GET", "/api/profiles", "", nil); strings.Count(w.Body.String(), `"Гостиная"`) != 1 {
		t.Fatalf("picker %s", w.Body)
	}
}

// A managed account never signs in: a session row it got some other way is
// no session.
func TestManagedAccountSessionRowIs401(t *testing.T) {
	e := newLibEnv(t)
	acc, err := e.store.CreateManaged(t.Context(), 1, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	tok := "managed-bearer-token"
	if _, err := e.db.Exec(`INSERT INTO sessions(token, user_id, kind, device_id, device_name) VALUES(?,?,?,?,?)`,
		auth.TokenHash(tok), acc.ID, auth.KindNative, "tv-000009", "TV"); err != nil {
		t.Fatal(err)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(tok)...); w.Code != 401 {
		t.Fatalf("/api/me with a managed account's token: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/home", "", nil, bearer(tok)...); w.Code != 401 {
		t.Fatalf("/api/home with a managed account's token: %d", w.Code)
	}
}
