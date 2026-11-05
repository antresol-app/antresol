package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// A page of cards built from the batch is the same, card for card, as one
// built item by item: every kind, with progress, probes and parents.
func TestCardBatchSameCards(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	movie, file := e.movie("Брат", "16+", "0123456789abcdef")
	probe, _ := json.Marshal(media.ProbeResult{DurationMs: 1000, Video: &media.VideoStream{Width: 1920, Height: 1080},
		Audio: []media.AudioStream{{Lang: "rus"}, {Lang: "eng"}}, Subs: []media.SubStream{{Lang: "eng"}}})
	if err := e.repo.SetProbe(ctx, file, string(probe), 1000); err != nil {
		t.Fatal(err)
	}
	e.movie("Брат 2", "16+", "fedcba9876543210") // no probe, no progress
	series, eps := e.series("Тьма", "16+", 3)
	book := e.book("Мастер и Маргарита", ".epub")
	audiobook, _ := e.audiobook("Война и мир", 2)
	comicSeries, vols := e.comic("Берсерк", "18+", 2)
	dir := filepath.Join(e.media, "Кино", "Группа крови")
	album, err := e.repo.UpsertAlbumTracks(ctx, "Кино", []library.TrackInput{{File: library.FileInput{Path: filepath.Join(dir, "01.flac"), Size: 9},
		AlbumDir: dir, Album: "Группа крови", AlbumArtist: "Кино", Artist: "Кино", Title: "Группа крови", Track: 1, Codec: "flac", Lossless: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ item, pos int64 }{{movie, 300}, {eps[1], 500}, {book, 1}, {audiobook, 40}, {vols[0].id, 2}} {
		if err := e.prog.Save(ctx, 1, p.item, p.pos, 1000); err != nil {
			t.Fatal(err)
		}
	}
	var ids []int64
	rows, err := e.db.Query(`SELECT id FROM items ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	byID, err := e.repo.ItemsByID(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	var items []library.Item
	kinds := map[string]bool{}
	for _, id := range ids {
		if it, ok := byID[id]; ok {
			items = append(items, it)
			kinds[it.Kind] = true
		}
	}
	for _, k := range []string{library.KindMovie, library.KindSeries, library.KindSeason, library.KindEpisode, library.KindAlbum,
		library.KindArtist, library.KindTrack, library.KindBook, library.KindAudiobook, library.KindComic, library.KindComicVolume} {
		if !kinds[k] {
			t.Fatalf("no %s among %v", k, kinds)
		}
	}
	h := &lib{Deps: Deps{Library: e.repo, Progress: e.prog, DB: e.db}}
	var one []Card
	for _, it := range items {
		one = append(one, h.card(ctx, 1, it))
	}
	a, _ := json.Marshal(one)
	b, _ := json.Marshal(h.cards(ctx, 1, items))
	if string(a) != string(b) {
		t.Fatalf("cards differ\none by one: %s\nbatched:    %s", a, b)
	}
	if one[0].Progress == 0 && len(one[0].Badges) == 0 {
		t.Fatalf("the movie card lost its progress and badges: %+v", one[0])
	}
	_, _, _ = series, comicSeries, album

	// a second page in the same request adds to the first page's batch
	ctx1 := h.withCards(ctx, 1, items[:len(items)/2])
	c, _ := json.Marshal(append(h.cards(ctx1, 1, items[:len(items)/2]), h.cards(ctx1, 1, items[len(items)/2:])...))
	if string(a) != string(c) {
		t.Fatalf("two pages differ from one by one\none by one: %s\npages:      %s", a, c)
	}
	// another profile's cards: its own progress, never the batch's of
	// the first profile
	e.login(`{"name":"Мама"}`)
	mom := profileID(t, e, "Мама")
	if err := e.prog.Save(ctx, mom, movie, 900, 1000); err != nil {
		t.Fatal(err)
	}
	var two []Card
	for _, it := range items {
		two = append(two, h.card(ctx, mom, it))
	}
	d, _ := json.Marshal(two)
	f, _ := json.Marshal(h.cards(ctx1, mom, items))
	if string(d) != string(f) {
		t.Fatalf("profile 2 differs\none by one: %s\nbatched:    %s", d, f)
	}
}

// Home for a kid's profile from the batch: «Продолжить» keeps what the
// rating allows and drops the rest, as one by one; an item gone from the
// library meanwhile is left out, not an error.
func TestHomeBatchKidAndMissing(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	adult, _ := e.movie("Брат", "18+", "0123456789")
	child, _ := e.movie("Бременские", "6+", "0123456789")
	gone, _ := e.movie("Ушедший", "6+", "0123456789")
	_, eps := e.series("Смешарики", "0+", 2)
	kid := e.kid("12+")
	kidID := profileID(t, e, "Вася")
	for _, id := range []int64{adult, child, gone, eps[0]} {
		if err := e.prog.Save(ctx, kidID, id, 100, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.db.Exec(`UPDATE items SET missing_since='2026-11-01T00:00:00Z' WHERE id=?`, gone); err != nil {
		t.Fatal(err)
	}
	w := e.do("GET", "/api/home", "", kid)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var home struct{ Shelves []shelf }
	if err := json.Unmarshal(w.Body.Bytes(), &home); err != nil {
		t.Fatal(err)
	}
	got := map[int64]Card{}
	for _, c := range home.Shelves[0].Cards {
		got[c.ID] = c
	}
	if _, ok := got[adult]; ok {
		t.Fatal("the 18+ film in a 12+ profile's «Продолжить»")
	}
	if _, ok := got[gone]; ok {
		t.Fatal("an item gone from the library in «Продолжить»")
	}
	if c, ok := got[child]; !ok || c.Progress == 0 {
		t.Fatalf("the 6+ film: %+v %v", c, ok)
	}
	if c, ok := got[eps[0]]; !ok || c.SeriesID == 0 || c.Progress == 0 {
		t.Fatalf("the episode: %+v %v", c, ok)
	}
}

// profileID is the id of the profile called name (both editions).
func profileID(t *testing.T, e *libEnv, name string) int64 {
	t.Helper()
	var id int64
	if err := e.db.QueryRow(`SELECT id FROM users WHERE name=?`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
