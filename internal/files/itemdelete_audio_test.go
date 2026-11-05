package files

import (
	"context"
	"path/filepath"
	"testing"
)

// Only a video's audio tracks: «Книга.ru.mp3» beside an audiobook or a
// book file «Книга.m4b» is someone's media, not a track of it.
func TestDeleteTrackOnlyOfAVideo(t *testing.T) {
	p := &planner{d: &ItemDelete{}, ctx: context.Background(), mine: map[string]bool{"/m/Книга.m4b": true, "/m/Книга.epub": true}}
	for _, q := range []string{"/m/Книга.ru.mp3", "/m/Книга.part2.ac3"} {
		if p.track(q) {
			t.Fatalf("%s counted as a track", q)
		}
	}
}

// The audio tracks the organizer put beside a video («Брат (1997).ru.LostFilm.ac3»)
// are its own: they go with its folder, or with the file alone in a
// shared folder. A soundtrack named otherwise still keeps the folder.
func TestDeleteTakesExternalAudioTracks(t *testing.T) {
	e := newItemEnv(t)
	ctx := context.Background()
	dir := filepath.Join(e.mv, "Брат (1997)")
	f := filepath.Join(dir, "Брат (1997).mkv")
	write(t, f, 4)
	write(t, filepath.Join(dir, "Брат (1997).ru.LostFilm.ac3"), 2)
	e.rescan(t)
	it := e.itemOf(t, f)
	res, err := e.d.Delete(ctx, it.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trashed) != 1 || exists(dir) {
		t.Fatalf("trashed %+v", res.Trashed)
	}

	shared := filepath.Join(e.mv, "Коллекция")
	a, b := filepath.Join(shared, "Брат (1997).mkv"), filepath.Join(shared, "Брат 2 (2000).mkv")
	write(t, a, 4)
	write(t, filepath.Join(shared, "Брат (1997).ru.LostFilm.ac3"), 2)
	write(t, b, 4)
	write(t, filepath.Join(shared, "Брат 2 (2000).ru.mka"), 2)
	e.rescan(t)
	ia := e.itemOf(t, a)
	res, err = e.d.Delete(ctx, ia.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trashed) != 2 || exists(a) || exists(filepath.Join(shared, "Брат (1997).ru.LostFilm.ac3")) {
		t.Fatalf("trashed %+v", res.Trashed)
	}
	if !exists(b) || !exists(filepath.Join(shared, "Брат 2 (2000).ru.mka")) {
		t.Fatal("the other movie's track went")
	}

	// a neighbour whose name starts with this one's keeps its track
	c, d := filepath.Join(shared, "Брат.mkv"), filepath.Join(shared, "Брат.2.mkv")
	write(t, c, 4)
	write(t, d, 4)
	write(t, filepath.Join(shared, "Брат.2.ru.ac3"), 2)
	write(t, filepath.Join(shared, "Брат.ru.ac3"), 2)
	e.rescan(t)
	if _, err := e.d.Delete(ctx, e.itemOf(t, c).ID, 1); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(shared, "Брат.ru.ac3")) || !exists(filepath.Join(shared, "Брат.2.ru.ac3")) {
		t.Fatal("tracks of the neighbour")
	}
}
