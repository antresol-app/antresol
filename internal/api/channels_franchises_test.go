package api

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
)

func TestFranchisesAPI(t *testing.T) {
	e, svc := newChannelsEnv(t)
	var ids []int64
	for _, n := range []string{"Один", "Два", "Три", "Четыре", "Пять"} {
		id, _ := e.movie(n, "", n)
		ids = append(ids, id)
	}
	e.withDuration() // 5 × 90 min: over the franchise minimum with one hidden
	other, _ := e.movie("Чужой", "", "x")
	items, _ := json.Marshal(ids)
	if _, err := e.db.Exec(`INSERT INTO franchises (key, name, source, items_json, updated_at) VALUES ('c10', 'Сага', 'tmdb', ?, 0)`, string(items)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.Generator.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	ann := e.login(`{"name":"Аня"}`)
	if w := e.do("GET", "/api/channels/franchises", "", ann); w.Code != 403 {
		t.Fatalf("not an admin: %d", w.Code)
	}
	w := e.do("GET", "/api/channels/franchises", "", e.admin)
	var list struct {
		Available  bool
		Franchises []franchiseJSON
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || list.Available || len(list.Franchises) != 1 || len(list.Franchises[0].Items) != 5 || list.Franchises[0].ChannelID == 0 ||
		list.Franchises[0].Items[0].Title != "Один" {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	ch := list.Franchises[0].ChannelID
	order := func(o []int64, hidden []int64) string {
		b, _ := json.Marshal(channels.FranchiseManual{Order: o, Hidden: hidden})
		return string(b)
	}
	if w := e.do("PUT", "/api/channels/franchises/c10/order", order([]int64{other}, nil), e.admin); w.Code != 400 {
		t.Fatalf("a title not of the franchise: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/channels/franchises/nope/order", order(nil, nil), e.admin); w.Code != 404 {
		t.Fatalf("no franchise: %d", w.Code)
	}
	w = e.do("PUT", "/api/channels/franchises/c10/order", order([]int64{ids[3], ids[0], ids[1], ids[2], ids[4]}, []int64{ids[1]}), e.admin)
	var fr franchiseJSON
	json.Unmarshal(w.Body.Bytes(), &fr)
	if w.Code != 200 || !fr.Manual || len(fr.Items) != 5 || fr.Items[0].ID != ids[3] || !fr.Items[4].Hidden {
		t.Fatalf("order %d %s", w.Code, w.Body)
	}
	c, err := svc.Store.Get(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, p := range c.Rules.Picks {
		got = append(got, p.ID)
	}
	if want := []int64{ids[3], ids[0], ids[2], ids[4]}; !slices.Equal(got, want) || c.Edited {
		t.Fatalf("channel picks %v, want %v (edited %v)", got, want, c.Edited)
	}
	if w := e.do("DELETE", "/api/channels/franchises/c10/order", "", e.admin); w.Code != 200 || strings.Contains(w.Body.String(), `"manual":true`) {
		t.Fatalf("reset %d %s", w.Code, w.Body)
	}
	c, _ = svc.Store.Get(ctx, ch)
	if len(c.Rules.Picks) != 5 || c.Rules.Picks[0].ID != ids[0] {
		t.Fatalf("after reset %+v", c.Rules.Picks)
	}
	// an order kept for titles that are gone is not a franchise to look at
	if _, err := e.db.Exec(`INSERT INTO franchises (key, name, source, items_json, manual_json, updated_at) VALUES ('c11', 'Ушла', 'tmdb', '[]', '{"order":[1]}', 0)`); err != nil {
		t.Fatal(err)
	}
	w = e.do("GET", "/api/channels/franchises", "", e.admin)
	list.Franchises = nil
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Franchises) != 1 || list.Franchises[0].Key != "c10" {
		t.Fatalf("list with a kept order %d %s", w.Code, w.Body)
	}
	// no TMDB: no recompute
	if w := e.do("POST", "/api/channels/franchises/refresh", "", e.admin); w.Code != 409 {
		t.Fatalf("refresh without TMDB: %d", w.Code)
	}
	// a franchise channel deleted from «Управление каналами» is not «too little to play»: the list says it
	// was deleted, and the generator (a new order, a pass) leaves it until it is given back
	if w := e.do("DELETE", "/api/channels/"+itoa(ch), "", e.admin); w.Code != 204 {
		t.Fatalf("delete the franchise channel: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/channels/franchises/c10/order", order([]int64{ids[1], ids[0], ids[2], ids[3], ids[4]}, nil), e.admin); w.Code != 200 {
		t.Fatalf("order after the delete: %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/channels/franchises", "", e.admin)
	list.Franchises = nil
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Franchises) != 1 || list.Franchises[0].ChannelID != 0 || !list.Franchises[0].ChannelRemoved {
		t.Fatalf("list after the delete %d %s", w.Code, w.Body)
	}
}
