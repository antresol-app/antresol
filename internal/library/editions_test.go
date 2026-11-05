package library

import (
	"context"
	"slices"
	"testing"
)

// EditionsByTMDB: every top-level item of a TMDB id (two editions of a
// film are two items), lowest id first; missing ones and other kinds out.
func TestEditionsByTMDB(t *testing.T) {
	r, ctx := newRepo(t), context.Background()
	var ids []int64
	for _, title := range []string{"Матрица", "Матрица (4K)", "Брат", "Пропал"} {
		id, err := r.UpsertMovie(ctx, MovieInput{Title: title, Year: 1999, File: FileInput{Path: "/v/" + title + ".mkv", Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if _, err := r.db.Exec(`UPDATE items SET tmdb_id=603 WHERE id IN (?,?,?)`, ids[0], ids[1], ids[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE items SET tmdb_id=680 WHERE id=?`, ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE items SET missing_since='2026-01-01' WHERE id=?`, ids[3]); err != nil {
		t.Fatal(err)
	}
	got, err := r.EditionsByTMDB(ctx, KindMovie, []int64{603, 680, 4242})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !slices.Equal(got[603], []int64{ids[0], ids[1]}) || !slices.Equal(got[680], []int64{ids[2]}) {
		t.Fatalf("%v", got)
	}
	if got, err := r.EditionsByTMDB(ctx, KindSeries, []int64{603}); err != nil || len(got) != 0 {
		t.Fatalf("series: %v %v", got, err)
	}
	if got, err := r.EditionsByTMDB(ctx, KindMovie, nil); err != nil || len(got) != 0 {
		t.Fatalf("none: %v %v", got, err)
	}
}
