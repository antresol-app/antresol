package library

import (
	"context"
	"strings"
	"testing"
)

// ShowFiles gathers the files of every episode of a series (or one season)
// in one query, without missing files or episodes; its key moves when a
// file is probed or goes missing.
func TestShowFiles(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	var eps []int64
	for _, e := range []struct{ s, n int }{{1, 1}, {1, 2}, {2, 1}} {
		id, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Сёгун", Season: e.s, Episode: e.n,
			File: FileInput{Path: "/tv/Сёгун/s" + string(rune('0'+e.s)) + "e" + string(rune('0'+e.n)) + ".mkv", Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		eps = append(eps, id)
	}
	if _, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Другой", Season: 1, Episode: 1, File: FileInput{Path: "/tv/x.mkv", Size: 1}}); err != nil {
		t.Fatal(err)
	}
	ep, err := r.Item(ctx, eps[0])
	if err != nil {
		t.Fatal(err)
	}
	season, err := r.Item(ctx, ep.ParentID)
	if err != nil {
		t.Fatal(err)
	}
	series := season.ParentID
	if fs, err := r.ShowFiles(ctx, series); err != nil || len(fs) != 3 {
		t.Fatalf("series: %v %v", fs, err)
	}
	if fs, err := r.ShowFiles(ctx, season.ID); err != nil || len(fs) != 2 {
		t.Fatalf("season: %v %v", fs, err)
	}

	k0, err := r.ShowFilesKey(ctx, series)
	if err != nil {
		t.Fatal(err)
	}
	fs, _ := r.FilesOf(ctx, eps[1])
	if err := r.SetProbe(ctx, fs[0].ID, `{"Container":"mkv"}`, 1); err != nil {
		t.Fatal(err)
	}
	k1, _ := r.ShowFilesKey(ctx, series)
	if k1 == k0 {
		t.Fatal("key must change with a probe")
	}
	// a probe rewritten with other content of the same length moves the key;
	// the same probe written again does not
	if err := r.SetProbe(ctx, fs[0].ID, `{"Container":"mp4"}`, 1); err != nil {
		t.Fatal(err)
	}
	k1b, _ := r.ShowFilesKey(ctx, series)
	if k1b == k1 {
		t.Fatal("key must change when a probe is rewritten with same-length content")
	}
	if err := r.SetProbe(ctx, fs[0].ID, `{"Container":"mp4"}`, 1); err != nil {
		t.Fatal(err)
	}
	if k, _ := r.ShowFilesKey(ctx, series); k != k1b {
		t.Fatalf("an unchanged probe must keep the key: %s != %s", k, k1b)
	}
	k1 = k1b
	if _, err := r.db.Exec(`UPDATE items SET missing_since='2026-01-01' WHERE id=?`, eps[2]); err != nil {
		t.Fatal(err)
	}
	if fs, _ := r.ShowFiles(ctx, series); len(fs) != 2 {
		t.Fatalf("missing episode kept: %v", fs)
	}
	if k2, _ := r.ShowFilesKey(ctx, series); k2 == k1 {
		t.Fatal("key must change when an episode goes missing")
	}
}

// The fingerprint is answered from the covering index: the files table (and
// its probe_json overflow pages) is never read.
func TestShowFilesKeyCoveringIndex(t *testing.T) {
	r := newRepo(t)
	rows, err := r.db.Query(`EXPLAIN QUERY PLAN SELECT COUNT(*), COALESCE(SUM(id),0), COALESCE(SUM(probe_rev),0)
	  FROM files WHERE missing_since IS NULL AND item_id IN (`+showEpisodes+`)`, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "files_item_probe_rev") {
		t.Fatalf("plan does not use files_item_probe_rev: %q", plan)
	}
	for _, d := range plan {
		if strings.Contains(d, "files") && !strings.Contains(d, "COVERING INDEX files_item_probe_rev") {
			t.Fatalf("files read past the covering index: %q", plan)
		}
	}
}

// The episodes of a show are found through the parent index whether or not
// the planner has statistics: without them it walked every episode of the
// library by kind for each series card (a grid of series read the items
// table a hundred times over).
func TestShowEpisodesByParentIndex(t *testing.T) {
	r := newRepo(t)
	if _, err := r.db.Exec(`DROP TABLE IF EXISTS sqlite_stat1`); err != nil {
		t.Fatal(err)
	}
	rows, err := r.db.Query(`EXPLAIN QUERY PLAN `+showEpisodes, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	for _, d := range plan {
		if strings.Contains(d, " e ") || strings.Contains(d, " s ") {
			if !strings.Contains(d, "items_parent") {
				t.Fatalf("items read past the parent index: %q", plan)
			}
		}
	}
}
