package library

import (
	"context"
	"testing"
)

// ItemsByTMDB: the top-level items of each kind's TMDB ids; ids the library
// lacks are left out, a kind with none is an empty map.
func TestItemsByTMDB(t *testing.T) {
	r, ctx := newRepo(t), context.Background()
	a, err := r.UpsertMovie(ctx, MovieInput{Title: "Матрица", Year: 1999, File: FileInput{Path: "/v/a.mkv", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.UpsertMovie(ctx, MovieInput{Title: "Брат", Year: 1997, File: FileInput{Path: "/v/b.mkv", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for id, tmdb := range map[int64]int{a: 603, b: 680} {
		if _, err := r.db.Exec(`UPDATE items SET tmdb_id=? WHERE id=?`, tmdb, id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.ItemsByTMDB(ctx, map[string][]int64{KindMovie: {603, 680, 4242}, KindSeries: {603}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[KindMovie]) != 2 || got[KindMovie][603].ID != a || got[KindMovie][680].Title != "Брат" || len(got[KindSeries]) != 0 {
		t.Fatalf("%+v", got)
	}
	if got, err := r.ItemsByTMDB(ctx, nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("none: %v %v", got, err)
	}
}
