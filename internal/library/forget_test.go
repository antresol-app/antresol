package library

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestDeleteSetAndForgetSeason(t *testing.T) {
	s, _, sr := newScanner(t)
	ctx := context.Background()
	for _, p := range []string{"Show/Season 01/Show.S01E01.mkv", "Show/Season 01/Show.S01E02.mkv", "Show/Season 02/Show.S02E01.mkv"} {
		touch(t, filepath.Join(sr, p), "abc")
	}
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	r := s.Repo
	ep, _, err := r.ItemByPath(ctx, filepath.Join(sr, "Show/Season 01/Show.S01E01.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	season, err := r.Item(ctx, ep.ParentID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.DeleteSet(ctx, season.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != 3 || len(d.Files) != 2 || len(d.Shared) != 0 || d.Size() != 6 {
		t.Fatalf("season set: %+v", d)
	}
	ids := []int64{}
	for _, f := range d.Files {
		ids = append(ids, f.ID)
	}
	if err := r.Forget(ctx, d.Items, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Item(ctx, season.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("season still shown: %v", err)
	}
	if _, err := r.Item(ctx, season.ParentID); err != nil {
		t.Fatalf("series hidden with a season left: %v", err)
	}
	// the whole series now: the last season goes, the series with it
	d, err = r.DeleteSet(ctx, season.ParentID)
	if err != nil || len(d.Items) != 3 || len(d.Files) != 1 {
		t.Fatalf("series set: %+v %v", d, err)
	}
	if err := r.Forget(ctx, d.Items[1:], []int64{d.Files[0].ID}); err != nil { // the series by the cascade
		t.Fatal(err)
	}
	if _, err := r.Item(ctx, season.ParentID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty series still shown: %v", err)
	}
	// a rescan with the files back brings the item back (restore from the trash)
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Item(ctx, season.ID); err != nil {
		t.Fatalf("restored season not back: %v", err)
	}
}

// A file another album's track plays from stays (shared), the album's own go.
func TestDeleteSetSharedFile(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := r.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO items(id, kind, title) VALUES (1,'album','A'), (2,'track','a1'), (3,'track','a2'), (4,'album','B'), (5,'track','b1')`)
	exec(`UPDATE items SET parent_id=1 WHERE id IN (2,3)`)
	exec(`UPDATE items SET parent_id=4 WHERE id=5`)
	exec(`INSERT INTO files(id, item_id, path, size) VALUES (10, 1, '/m/A/image.flac', 100), (11, 3, '/m/A/a2.flac', 5)`)
	exec(`INSERT INTO tracks(item_id, file_id, start_ms) VALUES (2, 10, 0), (3, 11, 0), (5, 10, 60000)`)
	d, err := r.DeleteSet(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != 3 || len(d.Files) != 1 || d.Files[0].ID != 11 || len(d.Shared) != 1 || d.Shared[0].ID != 10 {
		t.Fatalf("set %+v", d)
	}
	if err := r.Forget(ctx, d.Items, []int64{11}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Item(ctx, 5); err != nil {
		t.Fatalf("the other album's track broke: %v", err)
	}
	if _, err := r.FileByID(ctx, 10); err != nil {
		t.Fatalf("shared file forgotten: %v", err)
	}
	if _, err := r.DeleteSet(ctx, 5); !errors.Is(err, ErrNotDeletable) {
		t.Fatalf("a track is deletable: %v", err)
	}
	if _, err := r.DeleteSet(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forgotten album found: %v", err)
	}
}
