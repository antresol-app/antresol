package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// F8: trashing a movie or series folder and restoring it (undo or Корзина)
// keeps the item id, so watch progress, watchlist entries and TMDB metadata
// survive; while it is in the trash the item is hidden.
func TestTrashAndRestoreKeepsProgress(t *testing.T) {
	s, mv, sr := newScanner(t)
	ctx := context.Background()
	movie := filepath.Join(mv, "Брат (1997)", "Брат (1997).mkv")
	ep := filepath.Join(sr, "Сёгун", "Season 01", "Сёгун.S01E03.mkv")
	touch(t, movie, "m")
	touch(t, ep, "e")
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	r := s.Repo
	mItem, _, err := r.ItemByPath(ctx, movie)
	if err != nil {
		t.Fatal(err)
	}
	eItem, _, err := r.ItemByPath(ctx, ep)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetMetadata(ctx, mItem.ID, Metadata{Title: "Брат", Overview: "Из TMDB", TmdbID: 20992}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO users(id, name, avatar) VALUES (1, 'Папа', 'a')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{mItem.ID, eItem.ID} {
		if _, err := r.db.Exec(`INSERT INTO progress(user_id, item_id, position_ms, duration_ms, updated_at) VALUES (1, ?, 600000, 5400000, '2026-10-25T00:00:00.000000000Z')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.db.Exec(`INSERT INTO watchlist(user_id, item_id, added_at) VALUES (1, ?, 'x')`, mItem.ID); err != nil {
		t.Fatal(err)
	}

	// Trash: the folders leave the library roots, the scan runs.
	trash := t.TempDir()
	if err := os.Rename(filepath.Dir(movie), filepath.Join(trash, "m")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(sr, "Сёгун"), filepath.Join(trash, "s")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Item(ctx, mItem.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trashed movie still shown: %v", err)
	}
	if items, total, err := r.List(ctx, ListQuery{}); err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("list %v %d %v", items, total, err)
	}
	if res, _ := r.Search(ctx, "Брат", 10); len(res) != 0 {
		t.Fatalf("search finds a trashed movie: %v", res)
	}
	if _, err := r.ItemByTMDB(ctx, KindMovie, 20992); !errors.Is(err, ErrNotFound) {
		t.Fatalf("by tmdb: %v", err)
	}

	// Restore.
	if err := os.Rename(filepath.Join(trash, "m"), filepath.Dir(movie)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(trash, "s"), filepath.Join(sr, "Сёгун")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	m2, _, err := r.ItemByPath(ctx, movie)
	if err != nil || m2.ID != mItem.ID || m2.Overview != "Из TMDB" {
		t.Fatalf("restored movie %+v %v (was id %d)", m2, err, mItem.ID)
	}
	e2, _, err := r.ItemByPath(ctx, ep)
	if err != nil || e2.ID != eItem.ID {
		t.Fatalf("restored episode %+v %v", e2, err)
	}
	if _, err := r.Item(ctx, e2.ParentID); err != nil {
		t.Fatalf("season still hidden: %v", err)
	}
	var progress, watch int
	r.db.QueryRow(`SELECT COUNT(*) FROM progress WHERE user_id=1`).Scan(&progress)
	r.db.QueryRow(`SELECT COUNT(*) FROM watchlist WHERE user_id=1`).Scan(&watch)
	if progress != 2 || watch != 1 {
		t.Fatalf("progress %d watchlist %d after restore", progress, watch)
	}
	if items, total, _ := r.List(ctx, ListQuery{}); total != 2 || len(items) != 2 {
		t.Fatalf("list after restore: %d", total)
	}
}

// Deferred M4 helper: the folder that already holds a series.
func TestSeriesFolder(t *testing.T) {
	s, _, sr := newScanner(t)
	ctx := context.Background()
	touch(t, filepath.Join(sr, "Planet Earth II", "tvshow.nfo"), `<tvshow><title>Planet Earth II</title><tmdbid>68595</tmdbid></tvshow>`)
	touch(t, filepath.Join(sr, "Planet Earth II", "Season 01", "Planet.Earth.II.S01E01.mkv"), "")
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.Repo.SeriesFolder(ctx, sr, 68595)
	if err != nil || got != "Planet Earth II" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := s.Repo.SeriesFolder(ctx, sr, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown series: %v", err)
	}
}
