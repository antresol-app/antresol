package library

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestListMaxRating(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	for _, c := range []struct{ t, rating string }{{"A", "0+"}, {"B", "G"}, {"C", "12+"}, {"D", "PG-13"}, {"E", "18+"}, {"F", "R"}, {"G", ""}} {
		if _, err := r.UpsertMovie(ctx, MovieInput{Title: c.t, Rating: c.rating, File: FileInput{Path: "/x/" + c.t + ".mkv"}}); err != nil {
			t.Fatal(err)
		}
	}
	items, total, err := r.List(ctx, ListQuery{Kind: "movie", Sort: "title", MaxRating: "12+"})
	if err != nil || total != 4 || len(items) != 4 {
		t.Fatalf("12+: %v %d %v", titles(items), total, err)
	}
	items, _, _ = r.List(ctx, ListQuery{Kind: "movie", Sort: "title", MaxRating: "PG"}) // = 6+
	if len(items) != 2 {
		t.Fatalf("PG: %v", titles(items))
	}
	items, total, _ = r.List(ctx, ListQuery{Kind: "movie", Sort: "title"})
	if total != 7 {
		t.Fatalf("all: %d", total)
	}
	items, total, _ = r.List(ctx, ListQuery{Kind: "movie", Sort: "title", Limit: 3, Offset: 3})
	if len(items) != 3 || total != 7 {
		t.Fatalf("page: %v %d", titles(items), total)
	}
	// NoTotal: the same page, no count
	same, total, _ := r.List(ctx, ListQuery{Kind: "movie", Sort: "title", Limit: 3, Offset: 3, NoTotal: true})
	if !reflect.DeepEqual(same, items) || total != 0 {
		t.Fatalf("no total: %v %d", titles(same), total)
	}
}

func TestSearchRated(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	r.UpsertMovie(ctx, MovieInput{Title: "Шрек", Rating: "6+", File: FileInput{Path: "/a.mkv"}})
	r.UpsertMovie(ctx, MovieInput{Title: "Шрам", Rating: "18+", File: FileInput{Path: "/b.mkv"}})
	r.UpsertMovie(ctx, MovieInput{Title: "Шрифт", File: FileInput{Path: "/c.mkv"}})
	if got, _ := r.Search(ctx, "шр", 10); len(got) != 3 {
		t.Fatalf("%v", titles(got))
	}
	if got, _ := r.SearchRated(ctx, "шр", 10, "12+"); len(got) != 1 || got[0].Title != "Шрек" {
		t.Fatalf("%v", titles(got))
	}
	if got, _ := r.Search(ctx, `"`, 10); len(got) != 0 {
		t.Fatal("quote query")
	}
}

func TestUpsertEpisodeAndRemove(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	id, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "S", Season: 1, Episode: 1, File: FileInput{Path: "/r/s/1.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	id2, _ := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "S", Season: 1, Episode: 1, File: FileInput{Path: "/r/s/1.mkv", Size: 5}})
	if id != id2 {
		t.Fatal("not idempotent")
	}
	f, err := r.FilesOf(ctx, id)
	if err != nil || len(f) != 1 || f[0].Size != 5 {
		t.Fatalf("%v %v", f, err)
	}
	if g, err := r.FileByID(ctx, f[0].ID); err != nil || g.Path != "/r/s/1.mkv" {
		t.Fatalf("%v %v", g, err)
	}
	// a root with a similar prefix must not be touched
	r.UpsertMovie(ctx, MovieInput{Title: "M", File: FileInput{Path: "/r/s2/m.mkv"}})
	if err := r.RemoveMissingFiles(ctx, "/r/s", map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	var n int
	r.db.QueryRow(`SELECT COUNT(*) FROM items WHERE missing_since IS NULL`).Scan(&n)
	if n != 1 {
		t.Fatalf("items left %d", n)
	}
}

func titles(items []Item) []string {
	var s []string
	for _, i := range items {
		s = append(s, i.Title)
	}
	return s
}

func TestFilesUnderAndByPath(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	id, err := r.UpsertMovie(ctx, MovieInput{Title: "Дюна", Year: 2024, Files: []FileInput{
		{Path: "/m/movies/Дюна (2024)/a.mkv"}, {Path: "/m/movies/Дюна (2024)/sub/b.mkv"}, {Path: "/m/movies/Дюна (2024)0/c.mkv"}, {Path: "/m/moviesX/d.mkv"}}})
	if err != nil {
		t.Fatal(err)
	}
	fs, err := r.FilesUnder(ctx, "/m/movies/Дюна (2024)")
	if err != nil || len(fs) != 2 {
		t.Fatalf("%+v %v", fs, err)
	}
	if fs, _ := r.FilesUnder(ctx, "/m/movies/"); len(fs) != 3 {
		t.Fatalf("movies: %+v", fs)
	}
	it, f, err := r.ItemByPath(ctx, "/m/movies/Дюна (2024)/a.mkv")
	if err != nil || it.ID != id || f.ItemID != id {
		t.Fatalf("%+v %+v %v", it, f, err)
	}
	if _, _, err := r.ItemByPath(ctx, "/nope"); err != ErrNotFound {
		t.Fatalf("%v", err)
	}
}

func TestVideoFilesNewestFirst(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	var ids []int64
	for _, p := range []string{"/m/a.mkv", "/m/b.mkv", "/m/c.mkv", "/m/noprobe.mkv"} {
		id, err := r.UpsertMovie(ctx, MovieInput{Title: p, File: FileInput{Path: p}})
		if err != nil {
			t.Fatal(err)
		}
		fs, _ := r.FilesOf(ctx, id)
		ids = append(ids, fs[0].ID)
	}
	ep, _ := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "S", Season: 1, Episode: 1, File: FileInput{Path: "/s/1.mkv"}})
	efs, _ := r.FilesOf(ctx, ep)
	trackPath := "/music/a/01.flac"
	if _, err := r.UpsertAlbumTracks(ctx, "A", []TrackInput{{File: FileInput{Path: trackPath}, AlbumDir: "/music/a", Album: "X", AlbumArtist: "A", Artist: "A", Title: "t", Track: 1}}); err != nil {
		t.Fatal(err)
	}
	track, _ := r.FileByPath(ctx, trackPath)
	for _, id := range []int64{ids[0], ids[1], ids[2], efs[0].ID, track.ID} {
		if err := r.SetProbe(ctx, id, `{"Container":"x"}`, 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.db.Exec(`UPDATE files SET missing_since='2026-01-01' WHERE id=?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	got, err := r.VideoFiles(ctx, 0, 2)
	if err != nil || len(got) != 2 || got[0].ID != efs[0].ID || got[1].ID != ids[2] {
		t.Fatalf("page 1: %+v %v", got, err)
	}
	got, err = r.VideoFiles(ctx, got[1].ID, 2)
	if err != nil || len(got) != 1 || got[0].ID != ids[0] {
		t.Fatalf("page 2: %+v %v", got, err)
	}
}

// A page of films and series together is the same slice of the whole
// list, whatever the sort, rating limit and owner.
func TestListBothKindsPage(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	ratings := []string{"0+", "12+", "16+", "18+", ""}
	for i := range 40 {
		var err error
		if i%3 == 0 {
			_, err = r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: fmt.Sprintf("Сериал %02d", i), SeriesRating: ratings[i%5], SeriesYear: 1990 + i%7,
				Season: 1, Episode: 1, File: FileInput{Path: fmt.Sprintf("/tv/%d.mkv", i)}})
		} else {
			_, err = r.UpsertMovie(ctx, MovieInput{Title: fmt.Sprintf("Фильм %02d", i), Rating: ratings[i%5], Year: 1990 + i%7,
				File: FileInput{Path: fmt.Sprintf("/m/%d.mkv", i)}})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	// ties on added_at (whole hours), one missing
	if _, err := r.db.Exec(`UPDATE items SET added_at=strftime('%Y-%m-%dT%H:%M:%SZ','2026-01-01','+'||(id%9)||' hours')`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE items SET missing_since='2026-02-01T00:00:00Z' WHERE title='Фильм 05'`); err != nil {
		t.Fatal(err)
	}
	// RecentSeriesOf answers as its query did before it grouped ids only
	for _, rating := range []string{"", "12+"} {
		for _, limit := range []int{0, 3, 20} {
			got, err := r.RecentSeriesOf(ctx, limit, rating, 0)
			if err != nil {
				t.Fatal(err)
			}
			rate, args := "1", []any(nil)
			if rating != "" {
				rate, args = allowedRatings(rating)
			}
			l := limit
			if l <= 0 {
				l = -1
			}
			want, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items i JOIN items s ON s.parent_id=i.id JOIN items e ON e.parent_id=s.id
			  WHERE i.kind='series' AND i.missing_since IS NULL AND s.missing_since IS NULL AND e.missing_since IS NULL AND `+rate+
				` GROUP BY i.id ORDER BY MAX(e.added_at) DESC, i.id DESC LIMIT ?`, append(args, l)...)
			if err != nil || len(want) == 0 || !reflect.DeepEqual(got, want) {
				t.Fatalf("recent series %q %d:\n got %v\nwant %v %v", rating, limit, titles(got), titles(want), err)
			}
		}
	}
	for _, sort := range []string{"", "added", "title", "year"} {
		for _, rating := range []string{"", "12+"} {
			all, total, err := r.List(ctx, ListQuery{Sort: sort, MaxRating: rating})
			if err != nil || len(all) != total {
				t.Fatalf("all: %d of %d %v", len(all), total, err)
			}
			for _, pg := range []struct{ limit, offset int }{{5, 0}, {7, 3}, {50, 0}, {10, 25}, {3, 100}} {
				got, n, err := r.List(ctx, ListQuery{Sort: sort, MaxRating: rating, Limit: pg.limit, Offset: pg.offset})
				if err != nil || n != total {
					t.Fatal(err, n, total)
				}
				want := all[min(pg.offset, len(all)):min(pg.offset+pg.limit, len(all))]
				if !reflect.DeepEqual(titles(got), titles(want)) {
					t.Fatalf("sort %q rating %q page %+v:\n got %v\nwant %v", sort, rating, pg, titles(got), titles(want))
				}
			}
		}
	}
}
