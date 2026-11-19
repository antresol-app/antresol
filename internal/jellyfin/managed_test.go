package jellyfin

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/antresol-app/antresol/internal/progress"
)

// A Jellyfin app signed in with a managed account's app password (a TV's
// Infuse with «Гостиная») is the person: the same user id, the same
// limits; what it plays and marks is the account's history, and what it
// shows as watched or to resume is read from there. The person's own
// token keeps the person's history.
func TestManagedAppPasswordKeepsTheTVsHistory(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	tv, err := e.users.CreateManaged(ctx, e.anya.ID, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	pw, err := e.users.SetManagedAppPassword(ctx, e.anya.ID, tv.ID)
	if err != nil {
		t.Fatal(err)
	}
	hdr := infuseHdr(e.loginTV("Аня", pw))
	// the login answers the person, not the account
	if w := e.do("GET", "/Users/Me", hdr, nil); w.Code != 200 {
		t.Fatalf("me %d", w.Code)
	} else {
		var u UserDto
		decode(t, w, &u)
		if u.Id != UserID(e.anya.ID) {
			t.Fatalf("me %+v", u)
		}
	}

	u, it := UserID(e.anya.ID), ItemID(e.interstellar)
	if w := e.do("POST", "/Users/"+u+"/PlayingItems/"+it+"/Progress?positionTicks=6000000000", hdr, nil); w.Code != 204 {
		t.Fatalf("progress %d", w.Code)
	}
	if en, err := e.prog.Get(ctx, tv.ID, e.interstellar); err != nil || en.PositionMs != 600_000 {
		t.Fatalf("account's progress %+v %v", en, err)
	}
	if _, err := e.prog.Get(ctx, e.anya.ID, e.interstellar); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("written for the person: %v", err)
	}
	// the TV resumes its own position; the person's phone does not see it
	var ud UserItemDataDto
	decode(t, e.do("GET", "/UserItems/"+it+"/UserData", hdr, nil), &ud)
	if ud.PlaybackPositionTicks != 6_000_000_000 {
		t.Fatalf("tv user data %+v", ud)
	}
	if !has(e.itemsHdr(hdr, "/UserItems/Resume").Items, it) {
		t.Fatal("not on the TV's Resume shelf")
	}
	if ud := e.userData(e.anyaTok, e.interstellar); ud.PlaybackPositionTicks != 0 {
		t.Fatalf("person's user data %+v", ud)
	}
	if has(e.items(e.anyaTok, "/UserItems/Resume").Items, it) {
		t.Fatal("on the person's Resume shelf")
	}

	// a watched mark of a series goes to the account's episodes
	q := "?userId=" + url.QueryEscape(u)
	if w := e.do("POST", "/UserPlayedItems/"+ItemID(e.series)+q, hdr, nil); w.Code != 200 {
		t.Fatalf("played %d", w.Code)
	}
	for _, ep := range e.eps {
		if en, err := e.prog.Get(ctx, tv.ID, ep); err != nil || !en.Finished {
			t.Fatalf("episode %d: %+v %v", ep, en, err)
		}
		if _, err := e.prog.Get(ctx, e.anya.ID, ep); !errors.Is(err, progress.ErrNotFound) {
			t.Fatalf("episode %d marked for the person: %v", ep, err)
		}
	}
	// the watchlist is a choice: it stays the person's
	if w := e.do("POST", "/UserFavoriteItems/"+it+q, hdr, nil); w.Code != 200 {
		t.Fatalf("favourite %d", w.Code)
	}
	if ids, _ := e.prog.Watchlist(ctx, e.anya.ID); len(ids) != 1 || ids[0] != e.interstellar {
		t.Fatalf("watchlist %v", ids)
	}
	// the account deleted: the token ends with it
	if err := e.users.DeleteManaged(ctx, e.anya.ID, tv.ID); err != nil {
		t.Fatal(err)
	}
	if w := e.do("GET", "/Users/Me", hdr, nil); w.Code != 401 {
		t.Fatalf("token of a deleted account: %d", w.Code)
	}
}

// A kid's managed account keeps the kid's limits: the TV token of «Миша»
// sees no film over 6+ and records nothing for it.
func TestManagedAppPasswordOfAKidKeepsTheLimits(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	tv, _ := e.users.CreateManaged(ctx, e.misha.ID, "Детская")
	pw, err := e.users.SetManagedAppPassword(ctx, e.misha.ID, tv.ID)
	if err != nil {
		t.Fatal(err)
	}
	tok := e.login("Миша", pw)
	hdr := swiftfinHdr(tok)
	e.do("POST", "/Sessions/Playing/Stopped", hdr, map[string]any{"ItemId": ItemID(e.deadpool), "PositionTicks": 600_000_000})
	if _, err := e.prog.Get(ctx, tv.ID, e.deadpool); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("saved for a hidden film: %v", err)
	}
	if w := e.get(tok, "/Items/"+ItemID(e.deadpool)); w.Code != 404 {
		t.Fatalf("hidden film %d", w.Code)
	}
	e.do("POST", "/Sessions/Playing/Stopped", hdr, map[string]any{"ItemId": ItemID(e.cartoon), "PositionTicks": 600_000_000})
	if _, err := e.prog.Get(ctx, tv.ID, e.cartoon); err != nil {
		t.Fatalf("the cartoon not in the account's history: %v", err)
	}
}

// loginTV signs name in from the TV's Infuse (a device of its own: a login
// from the same device id replaces that device's token).
func (e *libEnv) loginTV(name, pw string) string {
	e.t.Helper()
	w := e.do("POST", "/Users/AuthenticateByName", infuseHdr(""), map[string]string{"Username": name, "Pw": pw})
	if w.Code != 200 {
		e.t.Fatalf("login %s: %d %s", name, w.Code, w.Body)
	}
	var out struct{ AccessToken string }
	decode(e.t, w, &out)
	return out.AccessToken
}

func (e *libEnv) itemsHdr(hdr map[string]string, path string) itemsPage {
	e.t.Helper()
	w := e.do("GET", path, hdr, nil)
	if w.Code != 200 {
		e.t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	var p itemsPage
	decode(e.t, w, &p)
	return p
}
