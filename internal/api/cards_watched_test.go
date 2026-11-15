package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

// Card.watched is on every library card: a movie or episode the profile
// finished, a series or season every episode of which it finished.
func TestCardsWatched(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	done, _ := e.movie("Первый фильм", "", "0123456789abcdef")
	e.movie("Второй фильм", "", "fedcba9876543210")
	full, fullEps := e.series("Досмотренный", "", 2)
	half, halfEps := e.series("Начатый", "", 2)
	for _, id := range append([]int64{done, halfEps[0]}, fullEps...) {
		if err := e.prog.MarkWatched(ctx, 1, id, true); err != nil {
			t.Fatal(err)
		}
	}

	var list struct{ Items []Card }
	w := e.do("GET", "/api/items", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, c := range list.Items {
		got[c.ID] = c.Watched
	}
	want := map[int64]bool{done: true, full: true, half: false}
	for id, v := range want {
		if got[id] != v {
			t.Errorf("item %d watched %v, want %v (%s)", id, got[id], v, w.Body)
		}
	}
	if len(got) != 4 {
		t.Fatalf("%d cards: %s", len(got), w.Body)
	}

	// seasons and episodes, batched and one by one alike
	h := &lib{Deps: Deps{Library: e.repo, Progress: e.prog, DB: e.db}}
	ids := []int64{fullEps[0], halfEps[1]}
	for _, ep := range []int64{fullEps[0], halfEps[0]} {
		it, err := e.repo.Item(ctx, ep)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, it.ParentID)
	}
	byID, err := e.repo.ItemsByID(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]library.Item, 0, len(ids))
	for _, id := range ids {
		items = append(items, byID[id])
	}
	batched := h.cards(ctx, 1, items)
	for i, wantW := range []bool{true, false, true, false} {
		single := h.card(ctx, 1, items[i])
		if batched[i].Watched != wantW || single.Watched != wantW {
			t.Errorf("%s %d: batched %v, single %v, want %v", items[i].Kind, items[i].ID, batched[i].Watched, single.Watched, wantW)
		}
	}
	// the batch answered the seasons' question for the whole page
	b := batchOf(h.withCards(ctx, 1, items))
	if b == nil || !b.askedW[ids[2]] || !b.askedW[ids[3]] {
		t.Fatalf("seasons not in the batch: %+v", b)
	}
}

// Series and seasons are asked about in one grouped query per page.
func TestSeasonsWatchedOfOneQuery(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	_, a := e.series("Первый", "", 2)
	_, b := e.series("Второй", "", 1)
	for _, id := range a {
		if err := e.prog.MarkWatched(ctx, 1, id, true); err != nil {
			t.Fatal(err)
		}
	}
	sa, _ := e.repo.Item(ctx, a[0])
	sb, _ := e.repo.Item(ctx, b[0])
	got, err := e.prog.SeasonsWatchedOf(ctx, 1, []int64{sa.ParentID, sb.ParentID, 999999})
	if err != nil {
		t.Fatal(err)
	}
	if !got[sa.ParentID] || got[sb.ParentID] || got[999999] {
		t.Fatalf("%v", got)
	}
}

// Card.thumb of an episode: its own still when it has one, else a frame
// of its video; never on other kinds, never the series' art.
func TestEpisodeThumb(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	series, eps := e.series("Сериал", "", 2)
	still := "0123456789abcdef0123456789abcdef01234567.jpg"
	if _, err := e.db.Exec(`UPDATE items SET poster=? WHERE id=?`, still, eps[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE items SET poster='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jpg' WHERE id=?`, series); err != nil {
		t.Fatal(err)
	}
	w := e.do("GET", "/api/items/"+itoa(series), "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var d ItemDetail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Thumb != "" {
		t.Errorf("series thumb %q", d.Thumb)
	}
	got := d.Seasons[0].Episodes
	if got[0].Thumb != "/api/images/"+still {
		t.Errorf("episode 1 thumb %q", got[0].Thumb)
	}
	if got[1].Thumb != "/api/images/frame/"+itoa(eps[1]) {
		t.Errorf("episode 2 thumb %q", got[1].Thumb)
	}
	// «Продолжить» carries it too
	if err := e.prog.Save(ctx, 1, eps[1], 100, 1000); err != nil {
		t.Fatal(err)
	}
	var home struct {
		Shelves []struct {
			ID    string `json:"id"`
			Cards []Card `json:"cards"`
		} `json:"shelves"`
	}
	w = e.do("GET", "/api/home", "", e.admin)
	if err := json.Unmarshal(w.Body.Bytes(), &home); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range home.Shelves {
		for _, c := range s.Cards {
			if s.ID == "continue" && c.ID == eps[1] {
				found = c.Thumb == "/api/images/frame/"+itoa(eps[1])
			}
		}
	}
	if !found {
		t.Errorf("continue card without its thumb: %s", w.Body)
	}
}
