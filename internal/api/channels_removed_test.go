package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/channels"
)

type manageList struct {
	Channels []channelJSON
	Removed  []removedJSON
}

func (e *libEnv) manage(who *http.Cookie) manageList {
	e.t.Helper()
	w := e.do("GET", "/api/channels?manage=1", "", who)
	var l manageList
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &l) != nil {
		e.t.Fatalf("manage list %d %s", w.Code, w.Body)
	}
	return l
}

func (e *libEnv) userID(name string) int64 {
	e.t.Helper()
	var id int64
	if err := e.db.QueryRow(`SELECT id FROM users WHERE name=?`, name).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// Every kind of channel on /tv/manage can be deleted: a hand-made one and a
// channel with fetching go; a generated one goes too, and the generator does
// not make it again until it is given back from «Удалённые каналы».
func TestChannelsDeleteKinds(t *testing.T) {
	e, svc := newChannelsEnv(t)
	ctx := context.Background()
	svc.Generator.MinRuntime = time.Nanosecond
	for _, n := range []string{"Один", "Два", "Три", "Четыре", "Пять", "Шесть"} {
		e.movie(n, "", n)
	}
	e.withDuration()
	if _, err := svc.Generator.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	hand := e.do("POST", "/api/channels", `{"name":"Мой канал","rules":{}}`, e.admin)
	if hand.Code != 201 {
		t.Fatalf("create %d %s", hand.Code, hand.Body)
	}
	var mine channelJSON
	json.Unmarshal(hand.Body.Bytes(), &mine)
	fetching, err := svc.Store.Create(ctx, channels.Channel{Name: "Марафон", Enabled: true,
		Rules: channels.Rules{Order: channels.OrderOrdered, Fetch: &channels.FetchRules{Titles: []channels.FetchTitle{{Kind: "series", TMDBID: 100, Title: "Друзья"}}}}})
	if err != nil {
		t.Fatal(err)
	}

	l := e.manage(e.admin)
	var auto channelJSON
	for _, c := range l.Channels {
		if c.Auto && c.Name == "Все фильмы" {
			auto = c
		}
	}
	// «Все фильмы» and «Кино 2000-х» (the six films are of one decade), the hand-made one and the one with fetching
	if auto.ID == 0 || len(l.Channels) != 4 || len(l.Removed) != 0 {
		t.Fatalf("before: %+v", l)
	}

	// the hand-made one and the one with fetching go, leaving nothing to give back
	for _, id := range []int64{mine.ID, fetching.ID} {
		if w := e.do("DELETE", "/api/channels/"+itoa(id), "", e.admin); w.Code != 204 {
			t.Fatalf("delete %d: %d %s", id, w.Code, w.Body)
		}
	}
	if l := e.manage(e.admin); len(l.Channels) != 2 || len(l.Removed) != 0 {
		t.Fatalf("after hand-made and fetching: %+v", l)
	}
	if w := e.do("DELETE", "/api/channels/"+itoa(mine.ID), "", e.admin); w.Code != 404 {
		t.Fatalf("delete again: %d", w.Code)
	}

	// the generated one goes and is listed to bring back; the lineup closes up
	if w := e.do("DELETE", "/api/channels/"+itoa(auto.ID), "", e.admin); w.Code != 204 {
		t.Fatalf("delete generated: %d %s", w.Code, w.Body)
	}
	l = e.manage(e.admin)
	if len(l.Channels) != 1 || l.Channels[0].Number != 1 || len(l.Removed) != 1 || l.Removed[0].Name != "Все фильмы" || l.Removed[0].Category != channels.CatGeneral || l.Removed[0].Personal {
		t.Fatalf("after generated: %+v", l)
	}
	// the generator's passes (the refresh, «Обновить автоматические каналы», a franchise's order) leave it
	if w := e.do("POST", "/api/channels/generate", "", e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"created":0`) {
		t.Fatalf("generate: %d %s", w.Code, w.Body)
	}
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if l := e.manage(e.admin); len(l.Channels) != 1 || len(l.Removed) != 1 {
		t.Fatalf("after the generator ran: %+v", l)
	}
	// the viewer's list does not show it, and says nothing of what was deleted
	w := e.do("GET", "/api/channels", "", e.admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"removed"`) || strings.Contains(w.Body.String(), "Все фильмы") {
		t.Fatalf("viewer's list: %d %s", w.Code, w.Body)
	}

	// bring it back: it is made now, as the library is now
	rid := l.Removed[0].ID
	if w := e.do("POST", "/api/channels/removed/"+itoa(rid)+"/restore", "", e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"restored":true`) {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	l = e.manage(e.admin)
	if len(l.Channels) != 2 || len(l.Removed) != 0 {
		t.Fatalf("after restore: %+v", l)
	}
	for _, c := range l.Channels {
		if c.Name == "Все фильмы" && (!c.Enabled || c.Number != 2) { // at the end of the list
			t.Fatalf("restored channel: %+v", c)
		}
	}
	if w := e.do("POST", "/api/channels/removed/"+itoa(rid)+"/restore", "", e.admin); w.Code != 404 {
		t.Fatalf("restore twice: %d", w.Code)
	}
}

// A deleted channel the library has no material for is given back all the
// same: it comes when the library has enough, and the answer says it is not
// there yet.
func TestChannelsRestoreWithoutMaterial(t *testing.T) {
	e, svc := newChannelsEnv(t)
	ctx := context.Background()
	c, err := svc.Store.Create(ctx, channels.Channel{Name: "Комедии", Category: channels.CatGenres, AutoKey: "genre:комедия", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if w := e.do("DELETE", "/api/channels/"+itoa(c.ID), "", e.admin); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	l := e.manage(e.admin)
	if len(l.Removed) != 1 {
		t.Fatalf("removed: %+v", l)
	}
	if w := e.do("POST", "/api/channels/removed/"+itoa(l.Removed[0].ID)+"/restore", "", e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"restored":false`) {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	if l := e.manage(e.admin); len(l.Channels) != 0 || len(l.Removed) != 0 {
		t.Fatalf("after: %+v", l)
	}
}

// A profile deletes and brings back its own generated channel («Моя
// медиатека»); nobody sees or gives back another's, and the shared ones are
// the admin's.
func TestChannelsRemovedPermissions(t *testing.T) {
	e, svc := newChannelsEnv(t)
	ctx := context.Background()
	ann := e.login(`{"name":"Аня"}`)
	annID := e.userID("Аня")
	own, err := svc.Store.Create(ctx, channels.Channel{Name: "Моя медиатека", Category: channels.CatMine, AutoKey: "mine:" + itoa(annID), UserID: annID, Enabled: true, Rules: channels.Rules{LibraryUser: annID}})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := svc.Store.Create(ctx, channels.Channel{Name: "Все фильмы", Category: channels.CatGeneral, AutoKey: "movies", Enabled: true, Rules: channels.Rules{Kinds: []string{"movie"}}})
	if err != nil {
		t.Fatal(err)
	}
	// a profile does not delete a shared channel
	if w := e.do("DELETE", "/api/channels/"+itoa(shared.ID), "", ann); w.Code != 403 {
		t.Fatalf("ann deletes a shared one: %d", w.Code)
	}
	if w := e.do("DELETE", "/api/channels/"+itoa(own.ID), "", ann); w.Code != 204 {
		t.Fatalf("ann deletes her own: %d %s", w.Code, w.Body)
	}
	if w := e.do("DELETE", "/api/channels/"+itoa(shared.ID), "", e.admin); w.Code != 204 {
		t.Fatalf("admin deletes a shared one: %d", w.Code)
	}
	la, ladmin := e.manage(ann), e.manage(e.admin)
	if len(la.Removed) != 1 || la.Removed[0].Name != "Моя медиатека" || !la.Removed[0].Personal {
		t.Fatalf("ann's list: %+v", la)
	}
	if len(ladmin.Removed) != 1 || ladmin.Removed[0].Name != "Все фильмы" {
		t.Fatalf("admin's list shows another profile's channel or misses its own: %+v", ladmin)
	}
	// who gives back what
	if w := e.do("POST", "/api/channels/removed/"+itoa(ladmin.Removed[0].ID)+"/restore", "", ann); w.Code != 403 {
		t.Fatalf("ann gives back a shared one: %d", w.Code)
	}
	if w := e.do("POST", "/api/channels/removed/"+itoa(la.Removed[0].ID)+"/restore", "", e.admin); w.Code != 403 {
		t.Fatalf("admin gives back another's personal one: %d", w.Code)
	}
	if w := e.do("POST", "/api/channels/removed/"+itoa(la.Removed[0].ID)+"/restore", "", ann); w.Code != 200 {
		t.Fatalf("ann gives back her own: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/channels/removed/x/restore", "", e.admin); w.Code != 404 {
		t.Fatalf("a bad id: %d", w.Code)
	}
	if w := e.do("POST", "/api/channels/removed/1/restore", "", nil); w.Code != 401 {
		t.Fatalf("signed out: %d", w.Code)
	}
}

// Deleting an external (IPTV) channel is hiding it in its source's editor:
// the decision is the admin's and outlives a refresh of the playlist, even
// one that loses the entry for a while; showing it again undoes it.
func TestExternalChannelHideOutlivesRefresh(t *testing.T) {
	e := newTVEnv(t)
	ctx := context.Background()
	src := e.addSource("П")
	find := func(name string, filter ...string) memberJSON {
		t.Helper()
		for _, m := range e.channels(src.ID, strings.Join(filter, "")).Channels {
			if m.ProviderName == name {
				return m
			}
		}
		t.Fatalf("no %q (%v)", name, filter)
		return memberJSON{}
	}
	n2 := find("Новости 2")
	if !n2.Shown {
		t.Fatalf("shown: %+v", n2)
	}
	if w := e.do("PATCH", "/api/tv/channels", `{"channelIds":[`+itoa(n2.ChannelID)+`],"shown":false}`, e.admin); w.Code != 200 {
		t.Fatalf("hide: %d %s", w.Code, w.Body)
	}
	refresh := func() {
		t.Helper()
		if _, err := e.tv.RefreshSource(ctx, src.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	if m := find("Новости 2"); m.Shown || m.Why != "manual" {
		t.Fatalf("hidden channel after a refresh: %+v", m)
	}
	// the playlist loses the channel for a refresh, then has it again
	e.p.put("/get.php", strings.Replace(tvPlaylist, "#EXTINF:-1 tvg-id=\"n2\" group-title=\"Новости\",Новости 2\n#EXTVLCOPT:http-user-agent=TestUA/1\nhttp://stream.example.test/live/u1/secret9/2.ts\n", "", 1))
	refresh()
	if m := find("Новости 2", "?state=gone"); m.Present || m.Shown {
		t.Fatalf("gone from the playlist: %+v", m)
	}
	e.p.put("/get.php", tvPlaylist)
	refresh()
	if m := find("Новости 2"); !m.Present || m.Shown || m.Why != "manual" {
		t.Fatalf("back in the playlist: %+v", m)
	}
	// the viewers' lists never get it
	if w := e.do("GET", "/api/channels", "", e.admin); strings.Contains(w.Body.String(), "Новости 2") {
		t.Fatalf("a hidden channel in the viewer's list: %s", w.Body)
	}
	// and it comes back when the admin shows it
	if w := e.do("PATCH", "/api/tv/channels", `{"channelIds":[`+itoa(n2.ChannelID)+`],"shown":true}`, e.admin); w.Code != 200 {
		t.Fatalf("show: %d", w.Code)
	}
	if m := find("Новости 2"); !m.Shown {
		t.Fatalf("shown again: %+v", m)
	}
}
