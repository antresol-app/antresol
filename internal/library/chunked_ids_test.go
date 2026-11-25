package library

import (
	"context"
	"slices"
	"testing"
)

// Lookups that take a list of ids or paths cut it into parts of 500. 1203
// movies make three parts; each case below names ids in the first, middle
// and last part, so a part that is skipped, repeated or cut short shows.
func TestIDListsAcrossChunks(t *testing.T) {
	const n = 1203
	r, ctx := newRepo(t), context.Background()
	items := make([]int64, n)
	paths := make([]string, n)
	for i := range items {
		paths[i] = "/v/Фильм " + itoa(i) + ".mkv"
		id, err := r.UpsertMovie(ctx, MovieInput{Title: "Фильм " + itoa(i), Year: 2000, File: FileInput{Path: paths[i], Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		items[i] = id
	}
	// a TMDB id of its own for each, apart from the item id
	if _, err := r.db.ExecContext(ctx, `UPDATE items SET tmdb_id=id+100000 WHERE kind='movie'`); err != nil {
		t.Fatal(err)
	}
	tmdb := make([]int64, n)
	for i, id := range items {
		tmdb[i] = id + 100000
	}
	user := func(name string) int64 {
		res, err := r.db.ExecContext(ctx, `INSERT INTO users (name, avatar) VALUES (?, 'grad-1')`, name)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	probes := []int{0, 1, 499, 500, 501, 999, 1000, n - 1} // each side of every boundary

	t.Run("ClaimPaths", func(t *testing.T) {
		u := user("Мама")
		all := append(slices.Clone(paths), "/v/нет такого.mkv") // a path with no row is skipped
		if err := r.ClaimPaths(ctx, all, u); err != nil {
			t.Fatal(err)
		}
		var owned int
		if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM item_owners WHERE user_id=?`, u).Scan(&owned); err != nil || owned != n {
			t.Fatalf("owns %d titles, want %d (%v)", owned, n, err)
		}
		for _, i := range probes {
			var one int
			if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM item_owners WHERE user_id=? AND item_id=?`, u, items[i]).Scan(&one); err != nil || one != 1 {
				t.Fatalf("movie %d (item %d) not claimed: %d %v", i, items[i], one, err)
			}
		}
		if err := r.ClaimPaths(ctx, nil, u); err != nil {
			t.Fatalf("no paths: %v", err)
		}
	})

	t.Run("SubtitleOffsets", func(t *testing.T) {
		u := user("Папа")
		fileIDs := make([]int64, n)
		for i, p := range paths {
			f, err := r.FileByPath(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			fileIDs[i] = f.ID
		}
		want := map[int64]int{}
		for k, i := range probes[:len(probes)-1] { // the last file keeps none
			want[fileIDs[i]] = (k + 1) * 100
			if err := r.SetSubtitleOffset(ctx, u, fileIDs[i], (k+1)*100); err != nil {
				t.Fatal(err)
			}
		}
		got, err := r.SubtitleOffsets(ctx, u, fileIDs)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("got %d offsets, want %d: %v", len(got), len(want), got)
		}
		for id, ms := range want {
			if got[id] != ms {
				t.Fatalf("file %d: %d ms, want %d", id, got[id], ms)
			}
		}
		if got, err := r.SubtitleOffsets(ctx, u, nil); err != nil || len(got) != 0 {
			t.Fatalf("no ids: %v %v", got, err)
		}
	})

	t.Run("ItemIDsByTMDB", func(t *testing.T) {
		ids := append(slices.Clone(tmdb), 4242, tmdb[3], tmdb[900]) // unknown ids, repeats
		got, err := r.ItemIDsByTMDB(ctx, KindMovie, ids)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != n {
			t.Fatalf("got %d, want %d", len(got), n)
		}
		for _, i := range probes {
			if got[tmdb[i]] != items[i] {
				t.Fatalf("tmdb %d: item %d, want %d", tmdb[i], got[tmdb[i]], items[i])
			}
		}
		if got, err := r.ItemIDsByTMDB(ctx, KindSeries, tmdb); err != nil || len(got) != 0 {
			t.Fatalf("series: %d %v", len(got), err)
		}
	})

	t.Run("EditionsByTMDB", func(t *testing.T) {
		// second editions of the films 10 and 1100, and a missing third one
		extra := func(title string, tmdbID int64, missing bool) int64 {
			id, err := r.UpsertMovie(ctx, MovieInput{Title: title, Year: 2000, File: FileInput{Path: "/v/" + title + ".mkv", Size: 1}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.db.ExecContext(ctx, `UPDATE items SET tmdb_id=? WHERE id=?`, tmdbID, id); err != nil {
				t.Fatal(err)
			}
			if missing {
				if _, err := r.db.ExecContext(ctx, `UPDATE items SET missing_since='2026-01-01' WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
			}
			return id
		}
		ed10 := extra("Фильм 10 (4K)", tmdb[10], false)
		ed1100 := extra("Фильм 1100 (4K)", tmdb[1100], false)
		extra("Фильм 10 (пропал)", tmdb[10], true)
		// the ids come unsorted, with 10 again 600 and 1100 positions on
		ids := slices.Clone(tmdb)
		slices.Reverse(ids)
		ids = append(ids, tmdb[10], 4242, tmdb[1100], tmdb[10])
		got, err := r.EditionsByTMDB(ctx, KindMovie, ids)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != n {
			t.Fatalf("got %d titles, want %d", len(got), n)
		}
		if !slices.Equal(got[tmdb[10]], []int64{items[10], ed10}) {
			t.Fatalf("film 10: %v, want [%d %d] once each, ascending", got[tmdb[10]], items[10], ed10)
		}
		if !slices.Equal(got[tmdb[1100]], []int64{items[1100], ed1100}) {
			t.Fatalf("film 1100: %v, want [%d %d]", got[tmdb[1100]], items[1100], ed1100)
		}
		for _, i := range probes {
			if i == 10 || i == 1100 {
				continue
			}
			if !slices.Equal(got[tmdb[i]], []int64{items[i]}) {
				t.Fatalf("film %d: %v, want [%d]", i, got[tmdb[i]], items[i])
			}
		}
	})
}
