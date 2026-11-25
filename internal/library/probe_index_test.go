package library

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// planOf is the query plan of q (its detail column, one line per step).
func planOf(t *testing.T, r *Repo, q string, args ...any) string {
	t.Helper()
	rows, err := r.db.Query(`EXPLAIN QUERY PLAN `+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	return strings.Join(out, "\n")
}

// A file with no probe is NULL, never ”: the partial index of the probe
// pass holds exactly those, and the pass's query reads it.
func TestUnprobedFilesAreFoundByTheirIndex(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	id, err := r.UpsertMovie(ctx, MovieInput{Title: "Брат", Year: 1997, File: FileInput{Path: "/m/Брат (1997).mkv", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	fs, _ := r.FilesOf(ctx, id)
	if err := r.SetProbe(ctx, fs[0].ID, "", 0); err != nil { // an empty result is no probe
		t.Fatal(err)
	}
	var isNull bool
	if err := r.db.QueryRow(`SELECT probe_json IS NULL FROM files WHERE id=?`, fs[0].ID).Scan(&isNull); err != nil || !isNull {
		t.Fatalf("probe_json of an empty probe: null=%v err=%v", isNull, err)
	}
	if left, err := r.FilesWithoutProbe(ctx, 10); err != nil || len(left) != 1 {
		t.Fatalf("left %v %v", left, err)
	}
	if err := r.SetProbe(ctx, fs[0].ID, `{"Container":"mkv"}`, 1000); err != nil {
		t.Fatal(err)
	}
	if left, err := r.FilesWithoutProbe(ctx, 10); err != nil || len(left) != 0 {
		t.Fatalf("left %v %v", left, err)
	}
	plan := planOf(t, r, `SELECT `+fileCols+` FROM files WHERE probe_json IS NULL AND missing_since IS NULL AND `+probeExtSQL+`
	  ORDER BY item_id IN (SELECT id FROM items WHERE kind IN ('movie','episode')) DESC, id LIMIT ?`, 10)
	if !strings.Contains(plan, "files_unprobed") {
		t.Fatalf("the probe pass does not read files_unprobed:\n%s", plan)
	}
}

// findSeries looks a series up by the ids of its tvshow.nfo through an
// index, not by reading every series row.
func TestSeriesLookupByNFOIdUsesIndex(t *testing.T) {
	r := newRepo(t)
	for _, k := range []string{"tvdb_id", "imdb_id"} {
		plan := planOf(t, r, `SELECT id FROM items WHERE kind='series' AND json_extract(meta_json,'$.`+k+`')=? ORDER BY id LIMIT 1`, "1")
		if !strings.Contains(plan, "items_series_"+strings.TrimSuffix(k, "_id")) {
			t.Fatalf("%s: no index in the plan:\n%s", k, plan)
		}
	}
	// and the scan finds the series that way: the second file of a show whose
	// nfo names only a tvdb id joins the first one's series
	ctx := context.Background()
	e := EpisodeInput{SeriesTitle: "Сёгун", SeriesExtIDs: map[string]string{"tvdb_id": "392573"}, Season: 1, Episode: 1,
		File: FileInput{Path: filepath.Join("/s", "a", "S01E01.mkv")}}
	a, err := r.UpsertEpisode(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	e2 := e
	e2.SeriesTitle, e2.Episode, e2.File.Path = "Shogun", 2, filepath.Join("/s", "b", "S01E02.mkv")
	b, err := r.UpsertEpisode(ctx, e2)
	if err != nil {
		t.Fatal(err)
	}
	ea, _ := r.Item(ctx, a)
	eb, _ := r.Item(ctx, b)
	sa, _ := r.Item(ctx, mustParent(t, r, ea.ParentID))
	sb, _ := r.Item(ctx, mustParent(t, r, eb.ParentID))
	if sa.ID != sb.ID {
		t.Fatalf("two series for one tvdb id: %d and %d", sa.ID, sb.ID)
	}
}

func mustParent(t *testing.T, r *Repo, id int64) int64 {
	t.Helper()
	it, err := r.Item(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it.ParentID
}
