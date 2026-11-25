package library

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// emptyVisible counts the episodes with no file, and the seasons and series
// with no visible child, that a list would still show.
func emptyVisible(t *testing.T, s *Scanner) (empty int) {
	t.Helper()
	if err := s.Repo.db.QueryRow(`SELECT COUNT(*) FROM items i WHERE i.missing_since IS NULL AND i.kind IN ('episode','season','series')
	  AND ((i.kind='episode' AND NOT EXISTS (SELECT 1 FROM files f WHERE f.item_id=i.id))
	    OR (i.kind IN ('season','series') AND NOT EXISTS (SELECT 1 FROM items c WHERE c.parent_id=i.id AND c.missing_since IS NULL)))`).Scan(&empty); err != nil {
		t.Fatal(err)
	}
	return empty
}

// An episode .nfo that renumbers the episode moves its file to another
// episode item: the scan that does it leaves no empty one behind.
func TestRescanThatMovesAFileLeavesNoEmptyEpisode(t *testing.T) {
	s, _, sr := newScanner(t)
	touch(t, filepath.Join(sr, "Show", "Season 1", "Show.S01E01.mkv"), "x")
	ep := filepath.Join(sr, "Show", "Season 1", "Show.S01E02.mkv")
	touch(t, ep, "x")
	scanAllOK(t, s)
	if n := emptyVisible(t, s); n != 0 {
		t.Fatalf("after the first scan: %d empty items", n)
	}
	touch(t, strings.TrimSuffix(ep, ".mkv")+".nfo", `<episodedetails><title>X</title><season>1</season><episode>7</episode></episodedetails>`)
	if st := scanAllOK(t, s); st.Changed != 1 {
		t.Fatalf("renumbering scan: %+v", st)
	}
	if n := emptyVisible(t, s); n != 0 {
		t.Fatalf("empty visible episode/season/series items after the renumbering scan: %d", n)
	}
	// and a scan that finds nothing changed still tidies nothing (the P10 saving)
	if st := scanAllOK(t, s); st.Changed != 0 || st.Removed != 0 {
		t.Fatalf("quiet scan: %+v", st)
	}
}

// A season folder emptied by the same kind of move goes with its episode.
func TestRescanThatMovesTheLastFileOfASeasonLeavesNoEmptySeason(t *testing.T) {
	s, _, sr := newScanner(t)
	ep := filepath.Join(sr, "Show", "Season 2", "Show.S02E01.mkv")
	touch(t, ep, "x")
	touch(t, filepath.Join(sr, "Show", "Season 1", "Show.S01E01.mkv"), "x")
	scanAllOK(t, s)
	touch(t, strings.TrimSuffix(ep, ".mkv")+".nfo", `<episodedetails><title>X</title><season>3</season><episode>1</episode></episodedetails>`)
	scanAllOK(t, s)
	if n := emptyVisible(t, s); n != 0 {
		t.Fatalf("empty visible items after the season moved: %d", n)
	}
}

// A tvshow.nfo that names another series moves every episode to a new
// series: the old one does not stay in the lists empty.
func TestRescanThatMovesASeriesLeavesNoEmptySeries(t *testing.T) {
	ctx := context.Background()
	s, _, sr := newScanner(t)
	touch(t, filepath.Join(sr, "Show", "tvshow.nfo"), `<tvshow><title>Old Name</title></tvshow>`)
	touch(t, filepath.Join(sr, "Show", "Season 1", "Show.S01E01.mkv"), "x")
	if _, err := s.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(sr, "Show", "tvshow.nfo"), `<tvshow><title>Completely Different</title><year>2001</year></tvshow>`)
	if _, err := s.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}
	if n := emptyVisible(t, s); n != 0 {
		t.Fatalf("empty visible items after the tvshow.nfo rename scan: %d", n)
	}
}
