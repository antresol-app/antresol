package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/auth"
)

// Music plays are history: on a device whose separate history is on, a
// play and «Слушали недавно» are the device's account's; stars stay the
// person's.
func TestSeparateHistoryTakesMusicPlays(t *testing.T) {
	e := newLibEnv(t)
	tr := e.track("Кукушка")
	tv := e.tv("tv-living-01")
	on := e.switchHistory(tv, "current", `{"separate":true}`)
	if w := e.do("POST", "/api/music/plays", `{"trackId":`+itoa(tr)+`}`, tv); w.Code != 204 {
		t.Fatalf("play %d %s", w.Code, w.Body)
	}
	var tvPlays, own int
	e.db.QueryRow(`SELECT COUNT(*) FROM plays WHERE user_id=?`, on.History.ID).Scan(&tvPlays)
	e.db.QueryRow(`SELECT COUNT(*) FROM plays WHERE user_id=1`).Scan(&own)
	if tvPlays != 1 || own != 0 {
		t.Fatalf("plays: tv %d person %d", tvPlays, own)
	}
	recent := func(c *http.Cookie) int {
		w := e.do("GET", "/api/music/albums?sort=recent", "", c)
		if w.Code != 200 {
			t.Fatalf("albums %d %s", w.Code, w.Body)
		}
		var out struct{ Items []Card }
		json.Unmarshal(w.Body.Bytes(), &out)
		return len(out.Items)
	}
	if recent(tv) != 1 || recent(e.admin) != 0 {
		t.Fatalf("recent: tv %d person %d", recent(tv), recent(e.admin))
	}
}

// The managed account's own app password: its owner makes, reads and
// revokes it; a kid only reads; another person's account is 404.
func TestManagedAppPasswordAPI(t *testing.T) {
	e := newLibEnv(t)
	acc, err := e.store.CreateManaged(t.Context(), 1, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/me/managed/" + itoa(acc.ID) + "/app-password"
	if w := e.do("GET", path, "", e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"exists":false`) {
		t.Fatalf("get %d %s", w.Code, w.Body)
	}
	w := e.do("POST", path, "", e.admin)
	var out struct{ Password string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Password) != 19 {
		t.Fatalf("post %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", path, "", e.admin); !strings.Contains(w.Body.String(), `"exists":true`) || strings.Contains(w.Body.String(), out.Password) {
		t.Fatalf("get after %s", w.Body)
	}
	// the person's own app password is another one
	if ok, _, _ := e.store.AppPasswordInfo(t.Context(), 1); ok {
		t.Fatal("the person's own app password was set")
	}
	_, other := e.profile(`{"name":"Мама"}`)
	if w := e.do("POST", path, "", other); w.Code != 404 {
		t.Fatalf("another person: %d", w.Code)
	}
	kidID, kid := e.profile(`{"name":"Kid","isKid":true}`)
	kacc, _ := e.store.CreateManaged(t.Context(), kidID, "Детская")
	kpath := "/api/me/managed/" + itoa(kacc.ID) + "/app-password"
	if w := e.do("GET", kpath, "", kid); w.Code != 200 {
		t.Fatalf("kid reads: %d", w.Code)
	}
	for _, m := range []string{"POST", "DELETE"} {
		if w := e.do(m, kpath, "", kid); w.Code != 403 {
			t.Fatalf("kid %s: %d", m, w.Code)
		}
	}
	if w := e.do("DELETE", path, "", e.admin); w.Code != 204 {
		t.Fatalf("delete %d", w.Code)
	}
	if w := e.do("GET", path, "", e.admin); !strings.Contains(w.Body.String(), `"exists":false`) {
		t.Fatalf("after delete %s", w.Body)
	}
}

// Switching a device off with «Перенести» keeps an account that has an
// app password: the TV's Jellyfin or music app still signs in with it.
func TestMergeKeepsAnAccountWithAnAppPassword(t *testing.T) {
	e := newLibEnv(t)
	tv := e.tv("tv-living-01")
	on := e.switchHistory(tv, "current", `{"separate":true}`)
	if _, err := e.store.SetManagedAppPassword(t.Context(), 1, on.History.ID); err != nil {
		t.Fatal(err)
	}
	e.switchHistory(tv, "current", `{"separate":false,"merge":true}`)
	if list, _ := e.store.ManagedOf(t.Context(), 1); len(list) != 1 {
		t.Fatalf("account gone with its app password: %+v", list)
	}
}

// The addon proxy's X-Antresol-User names the device's separate history
// (history: id, uid, name) next to the profile, which stays the person;
// GET /users lists each person's histories, and the core API takes a
// history's id for progress.
func TestAddonsSeeTheSeparateHistory(t *testing.T) {
	e := newAddonEnv(t)
	withAdultsAddon(t, e)
	tv := &http.Cookie{Name: "vault_session", Value: e.deviceLogin(1, adminPass, "tv-living-01")}
	w := e.do("PUT", "/api/me/devices/current/history", `{"separate":true}`, tv)
	if w.Code != 200 {
		t.Fatalf("switch %d %s", w.Code, w.Body)
	}
	header := func(c *http.Cookie) addonkit.User {
		t.Helper()
		w := e.do("POST", "/api/addons/fam/x/echo", `{}`, c)
		var got struct{ Headers http.Header }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatalf("proxy: %d %s", w.Code, w.Body)
		}
		var u addonkit.User
		if err := json.Unmarshal([]byte(got.Headers.Get("X-Antresol-User")), &u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	u := header(tv)
	if u.ID != 1 || u.History == nil || u.History.ID == 1 || len(u.History.UID) != 32 || u.History.Name == "" {
		t.Fatalf("tv header %+v %+v", u, u.History)
	}
	hu := u.HistoryUser()
	if hu.ID != u.History.ID || hu.UID != u.History.UID || hu.IsAdmin != u.IsAdmin {
		t.Fatalf("history user %+v", hu)
	}
	if own := header(e.admin); own.ID != 1 || own.History != nil {
		t.Fatalf("person's header %+v", own)
	}
	acc, err := auth.NewStore(e.db).Get(t.Context(), u.History.ID)
	if err != nil || acc.OwnerID != 1 || acc.UID != u.History.UID {
		t.Fatalf("account %+v %v", acc, err)
	}
}

// GET /users: a person's separate histories ride on the person (never as
// a profile of their own), and PUT /progress takes a history's id, with
// the person's limits.
func TestAddonCoreUsersListHistories(t *testing.T) {
	f := newProgFix(t)
	store := auth.NewStore(f.db)
	acc, err := store.CreateManaged(t.Context(), f.masha, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	f.fake.mu.Lock()
	pr := f.fake.installed["prog"]
	pr.Manifest.Permissions = append(pr.Manifest.Permissions, "users.read")
	f.fake.installed["prog"] = pr
	f.fake.mu.Unlock()
	var users []addonkit.User
	f.get("tok-prog", "/users", 200, &users)
	if len(users) != 3 {
		t.Fatalf("a history listed as a profile: %+v", users)
	}
	var masha addonkit.User
	for _, u := range users {
		if u.ID == f.masha {
			masha = u
		} else if len(u.Histories) != 0 {
			t.Fatalf("someone else's histories: %+v", u)
		}
	}
	if len(masha.Histories) != 1 || masha.Histories[0].ID != acc.ID || masha.Histories[0].UID != acc.UID || masha.Histories[0].Name != "Гостиная" {
		t.Fatalf("masha %+v", masha)
	}
	all := addonkit.HistoryProfiles(users)
	if len(all) != 4 {
		t.Fatalf("history profiles %+v", all)
	}

	f.put("tok-prog", save(acc.ID, f.movie, 60_000, 6_000_000, time.Time{}, false), 204)
	if p := f.progressOf("tok-prog", acc.ID, idList(f.movie)); p[itoa(f.movie)].PositionMs != 60_000 {
		t.Fatalf("history progress %+v", p)
	}
	if p := f.progressOf("tok-prog", f.masha, idList(f.movie)); len(p) != 0 {
		t.Fatalf("written for the person %+v", p)
	}
	// the person's limit applies to the history
	f.limit("6+", "18+", "")
	f.put("tok-prog", save(acc.ID, f.movie, 90_000, 6_000_000, time.Time{}, false), 404)
}
