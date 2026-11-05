package library

import (
	"context"
	"testing"
)

// «Моя медиатека»: a claim lands on the title a library page lists (the
// series of an episode, the album of a track, the comic series of a
// volume), several profiles may own one title, and every list filters by
// its Owner.
func TestOwnersUnitsAndLists(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	user := func(name string) int64 {
		res, err := r.db.ExecContext(ctx, `INSERT INTO users (name, avatar) VALUES (?, 'grad-1')`, name)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	mum, dad := user("Мама"), user("Папа")

	film, err := r.UpsertMovie(ctx, MovieInput{Title: "Дюна", Year: 2021, File: FileInput{Path: "/v/Дюна.mkv", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.UpsertMovie(ctx, MovieInput{Title: "Брат", Year: 1997, File: FileInput{Path: "/v/Брат.mkv", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ep := func(season, n int) int64 {
		id, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Тьма", SeriesYear: 2017, Season: season, Episode: n,
			File: FileInput{Path: "/s/Тьма/" + itoa(season) + "x" + itoa(n) + ".mkv", Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ep(1, 1)
	ep(2, 1)
	album, err := r.UpsertAlbumTracks(ctx, "Кино", []TrackInput{{File: FileInput{Path: "/m/Кино/Ночь/01.flac", Size: 1, Mtime: 1},
		AlbumDir: "/m/Кино/Ночь", Album: "Ночь", AlbumArtist: "Кино", Artist: "Кино", Title: "Ночь", Track: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpsertAlbumTracks(ctx, "Аквариум", []TrackInput{{File: FileInput{Path: "/m/Аквариум/Табу/01.flac", Size: 1, Mtime: 1},
		AlbumDir: "/m/Аквариум/Табу", Album: "Табу", AlbumArtist: "Аквариум", Artist: "Аквариум", Title: "Табу", Track: 1}}); err != nil {
		t.Fatal(err)
	}
	vol, err := r.UpsertComicVolume(ctx, ComicInput{File: FileInput{Path: "/c/Берсерк/v1.cbz", Size: 1, Mtime: 1},
		SeriesDir: "/c/Берсерк", Series: "Берсерк", Volume: 1})
	if err != nil {
		t.Fatal(err)
	}

	// Мама asked for the movie, season 2, the album and the manga; Папа
	// for season 1 of the same series
	if err := r.ClaimPaths(ctx, []string{"/v/Дюна.mkv", "/s/Тьма/2x1.mkv", "/m/Кино/Ночь/01.flac", "/c/Берсерк/v1.cbz", "/nowhere.mkv"}, mum); err != nil {
		t.Fatal(err)
	}
	if err := r.ClaimPaths(ctx, []string{"/s/Тьма/1x1.mkv"}, dad); err != nil {
		t.Fatal(err)
	}
	if err := r.ClaimPaths(ctx, []string{"/v/Брат.mkv"}, 0); err != nil { // nobody asked: no owner
		t.Fatal(err)
	}
	// a repeated claim changes nothing
	if err := r.ClaimPaths(ctx, []string{"/v/Дюна.mkv"}, mum); err != nil {
		t.Fatal(err)
	}

	titles := func(items []Item) map[string]bool {
		out := map[string]bool{}
		for _, it := range items {
			out[it.Title] = true
		}
		return out
	}
	mine, total, err := r.List(ctx, ListQuery{Owner: mum})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(mine); total != 2 || !got["Дюна"] || !got["Тьма"] {
		t.Fatalf("Мама's movies and series: %d %v", total, got)
	}
	dads, _, err := r.List(ctx, ListQuery{Owner: dad})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(dads); len(got) != 1 || !got["Тьма"] {
		t.Fatalf("Папа's: %v", got)
	}
	all, total, err := r.List(ctx, ListQuery{})
	if err != nil || total != 3 {
		t.Fatalf("shared library: %d %v", total, err)
	}
	_ = all
	if owners, err := r.Owners(ctx, ep(2, 1)); err != nil || len(owners) != 2 {
		t.Fatalf("series owners from an episode: %v %v", owners, err)
	}
	if owners, err := r.Owners(ctx, other); err != nil || len(owners) != 0 {
		t.Fatalf("an unclaimed movie: %v %v", owners, err)
	}
	if owners, err := r.Owners(ctx, film); err != nil || len(owners) != 1 || owners[0] != mum {
		t.Fatalf("movie owners: %v %v", owners, err)
	}

	series, err := r.RecentSeriesOf(ctx, 10, "", dad)
	if err != nil || len(series) != 1 {
		t.Fatalf("Папа's new episodes: %v %v", series, err)
	}

	albums, total, err := r.Albums(ctx, AlbumQuery{Owner: mum})
	if err != nil || total != 1 || albums[0].ID != album {
		t.Fatalf("Мама's albums: %d %+v %v", total, albums, err)
	}
	if _, total, _ := r.Albums(ctx, AlbumQuery{}); total != 2 {
		t.Fatalf("every album: %d", total)
	}
	artists, total, err := r.ArtistsOf(ctx, 0, 0, mum)
	if err != nil || total != 1 || artists[0].Title != "Кино" {
		t.Fatalf("Мама's artists: %d %+v %v", total, artists, err)
	}
	if _, total, _ := r.ArtistsOf(ctx, 0, 0, dad); total != 0 {
		t.Fatalf("Папа has no music: %d", total)
	}
	if _, total, _ := r.Artists(ctx, 0, 0); total != 2 {
		t.Fatalf("every artist: %d", total)
	}

	comics, total, err := r.Books(ctx, BookQuery{Kind: KindComic, Owner: mum})
	if err != nil || total != 1 {
		t.Fatalf("Мама's comics: %d %+v %v", total, comics, err)
	}
	if owners, _ := r.Owners(ctx, vol); len(owners) != 1 {
		t.Fatalf("a volume's series is owned: %v", owners)
	}
	if _, total, _ := r.Books(ctx, BookQuery{Kind: KindComic, Owner: dad}); total != 0 {
		t.Fatalf("Папа has no comics: %d", total)
	}

	// ClaimItem: an album an import found in the library already
	if err := r.ClaimItem(ctx, album, dad); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := r.Albums(ctx, AlbumQuery{Owner: dad}); total != 1 {
		t.Fatalf("claimed album: %d", total)
	}
	// a deleted profile's claims go with it
	if _, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, dad); err != nil {
		t.Fatal(err)
	}
	if owners, _ := r.Owners(ctx, album); len(owners) != 1 || owners[0] != mum {
		t.Fatalf("owners after the profile went: %v", owners)
	}
	// a claim for a profile that is gone records nothing
	if err := r.ClaimItem(ctx, other, dad); err != nil {
		t.Fatal(err)
	}
	if owners, _ := r.Owners(ctx, other); len(owners) != 0 {
		t.Fatalf("claim of a gone profile: %v", owners)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}
