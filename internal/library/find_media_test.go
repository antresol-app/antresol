package library

import (
	"context"
	"testing"
)

func TestFindAlbumAndBook(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	albumID, err := r.UpsertAlbumTracks(ctx, "Кино", []TrackInput{{File: FileInput{Path: "/m/Кино/Группа крови/01.flac", Size: 1, Mtime: 1},
		AlbumDir: "/m/Кино/Группа крови", Album: "Группа крови", Title: "Группа крови", Track: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if it, ok, err := r.FindAlbum(ctx, "КИНО", "группа  крови!"); err != nil || !ok || it.ID != albumID {
		t.Fatalf("album: %+v %v %v", it, ok, err)
	}
	for _, c := range [][2]string{{"Алиса", "Группа крови"}, {"Кино", "Звезда по имени Солнце"}, {"", "Группа крови"}} {
		if _, ok, err := r.FindAlbum(ctx, c[0], c[1]); err != nil || ok {
			t.Fatalf("%q: %v %v", c, ok, err)
		}
	}

	bookID, err := r.UpsertBook(ctx, BookInput{Files: []FileInput{{Path: "/b/Булгаков/Мастер и Маргарита.epub", Size: 1, Mtime: 1}},
		Title: "Мастер и Маргарита", Authors: []string{"Булгаков М."}})
	if err != nil {
		t.Fatal(err)
	}
	if it, ok, err := r.FindBook(ctx, KindBook, "Михаил Булгаков", "Мастер и Маргарита"); err != nil || !ok || it.ID != bookID {
		t.Fatalf("book: %+v %v %v", it, ok, err)
	}
	for _, c := range [][3]string{{KindBook, "Лев Толстой", "Мастер и Маргарита"}, {KindAudiobook, "Булгаков", "Мастер и Маргарита"}, {KindBook, "", "Белая гвардия"}} {
		if _, ok, err := r.FindBook(ctx, c[0], c[1], c[2]); err != nil || ok {
			t.Fatalf("%q: %v %v", c, ok, err)
		}
	}
	if _, ok, _ := r.FindBook(ctx, KindBook, "", "Мастер и Маргарита"); !ok {
		t.Fatal("title alone")
	}
}

// FindTitle: a movie or series by its TMDB id, an album or book by creator
// and title; other kinds have no lookup.
func TestFindTitle(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	movie, err := r.UpsertMovie(ctx, MovieInput{Title: "Дюна", Year: 2021, TmdbID: 438631, Rating: "12+", File: FileInput{Path: "/m/dune.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	album, err := r.UpsertAlbumTracks(ctx, "Кино", []TrackInput{{File: FileInput{Path: "/m/Кино/Группа крови/01.flac", Size: 1, Mtime: 1},
		AlbumDir: "/m/Кино/Группа крови", Album: "Группа крови", Title: "Группа крови", Track: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		kind    string
		tmdb    int64
		title   string
		creator string
		want    int64
	}{
		{KindMovie, 438631, "", "", movie},
		{KindSeries, 438631, "", "", 0}, // the TMDB id is of a movie
		{KindMovie, 1, "Дюна", "", 0},
		{"music", 0, "группа крови", "КИНО", album},
		{"music", 0, "группа крови", "Алиса", 0},
		{KindComic, 0, "Берсерк", "", 0},
	} {
		it, ok, err := r.FindTitle(ctx, c.kind, c.tmdb, c.title, c.creator)
		if err != nil || ok != (c.want != 0) || (ok && it.ID != c.want) {
			t.Errorf("%+v: %v %v %v", c, it.ID, ok, err)
		}
		if id, ok := r.FindTitleID(ctx, c.kind, int(c.tmdb), c.title, c.creator); id != c.want || ok != (c.want != 0) {
			t.Errorf("%+v: id %d %v", c, id, ok)
		}
	}
}
