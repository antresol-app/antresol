package library

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A walk marks missing only the files under its own root: the folder next
// to it with the same name start, and paths with non-ASCII names, are
// told apart by the path range.
func TestRemoveMissingStaysInsideItsRoot(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	in := insItem(t, r, KindMovie, 0, "Брат", "")
	insFile(t, r, in, "/m/movies/Брат (1997)/Брат (1997).mkv")
	insFile(t, r, in, "/m/movies/zzz.mkv")
	beside := insItem(t, r, KindMovie, 0, "Рядом", "")
	insFile(t, r, beside, "/m/movies2/Рядом.mkv")
	before := insItem(t, r, KindMovie, 0, "До", "")
	insFile(t, r, before, "/m/Рядом.mkv")
	n, err := r.removeMissing(ctx, "/m/movies", map[string]bool{})
	if err != nil || n != 2 {
		t.Fatalf("removed %d %v", n, err)
	}
	if got := states(t, r, in, beside, before); got[0] != "missing" || got[1] != "" || got[2] != "" {
		t.Fatalf("states %q", got)
	}
	if n, err := r.removeMissing(ctx, "/m/movies", map[string]bool{}); err != nil || n != 0 {
		t.Fatalf("again: %d %v", n, err)
	}
}

// A scan that finds nothing gone leaves the hiding of items to the scan
// that reads everything again (after a start, a manual one).
func TestFullScanHealsItemsLeftVisible(t *testing.T) {
	s, mv, _ := newScanner(t)
	touch(t, filepath.Join(mv, "Брат (1997)", "Брат (1997).mkv"), "x")
	scanAllOK(t, s)
	// drift: an item whose only file went missing without the scan noticing
	ghost := insItem(t, s.Repo, KindMovie, 0, "Призрак", "")
	insFile(t, s.Repo, ghost, filepath.Join(mv, "Призрак.mkv"))
	if _, err := s.Repo.db.Exec(`UPDATE files SET missing_since='2020-01-01T00:00:00Z' WHERE item_id=?`, ghost); err != nil {
		t.Fatal(err)
	}
	old := MissingGrace
	MissingGrace = 200 * 365 * 24 * time.Hour // never expires here
	defer func() { MissingGrace = old }()
	scanAllOK(t, s)
	if got := states(t, s.Repo, ghost); got[0] != "" {
		t.Fatalf("a quiet scan tidied: %q", got)
	}
	s.Rescan()
	scanAllOK(t, s)
	if got := states(t, s.Repo, ghost); got[0] != "missing" {
		t.Fatalf("the full scan did not tidy: %q", got)
	}
}
