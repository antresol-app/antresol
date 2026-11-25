package library

import (
	"context"
	"slices"
	"testing"
)

// One file several tracks play from (a CUE image). An empty keep-list drops
// nothing («NOT IN ()» is true of every row: it would take them all); a
// list drops the tracks whose start is not in it.
func TestDropStaleTracks(t *testing.T) {
	r, ctx := newRepo(t), context.Background()
	var in []TrackInput
	for i, start := range []int64{0, 60000, 120000} {
		in = append(in, TrackInput{File: FileInput{Path: "/m/Кино/Ночь/image.flac", Size: 1, Mtime: 1},
			AlbumDir: "/m/Кино/Ночь", Album: "Ночь", Title: "Трек " + itoa(i+1), Track: i + 1, StartMs: start})
	}
	if _, err := r.UpsertAlbumTracks(ctx, "Кино", in); err != nil {
		t.Fatal(err)
	}
	var fileID int64
	if err := r.db.QueryRowContext(ctx, `SELECT id FROM files WHERE path='/m/Кино/Ночь/image.flac'`).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	starts := func() []int64 {
		rows, err := r.db.QueryContext(ctx, `SELECT t.start_ms FROM tracks t JOIN items i ON i.id=t.item_id
		  WHERE t.file_id=? AND i.kind='track' ORDER BY t.start_ms`, fileID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var s int64
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}
	if got := starts(); !slices.Equal(got, []int64{0, 60000, 120000}) {
		t.Fatalf("setup: %v", got)
	}

	if err := dropStaleTracks(ctx, r.db, fileID, nil); err != nil {
		t.Fatal(err)
	}
	if got := starts(); !slices.Equal(got, []int64{0, 60000, 120000}) {
		t.Fatalf("an empty keep-list dropped tracks: %v", got)
	}
	if err := dropStaleTracks(ctx, r.db, fileID, []any{int64(0), int64(120000)}); err != nil {
		t.Fatal(err)
	}
	if got := starts(); !slices.Equal(got, []int64{0, 120000}) {
		t.Fatalf("after keeping 0 and 120000: %v", got)
	}
	var items int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE kind='track'`).Scan(&items); err != nil || items != 2 {
		t.Fatalf("track items %d %v (their rows go with them)", items, err)
	}
}
