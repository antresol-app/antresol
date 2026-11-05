package library

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// queryPlan returns EXPLAIN QUERY PLAN of q, one detail per line.
func queryPlan(t *testing.T, r *Repo, q string, args ...any) string {
	t.Helper()
	rows, err := r.db.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		b.WriteString(detail + "\n")
	}
	return b.String()
}

// F13: a rescan reads each album folder's own rows through an index, so
// it costs O(albums), not O(albums²).
func TestMusicRescanQueriesUseIndexes(t *testing.T) {
	r := newRepo(t)
	plan := queryPlan(t, r, albumSigsSQL, "/m/Кино - Группа крови")
	if !strings.Contains(plan, "USING INDEX items_match (kind=? AND match_key>? AND match_key<?)") {
		t.Fatalf("album signatures by folder:\n%s", plan)
	}
	plan = queryPlan(t, r, staleTracksSQL(2), 1, 0, 1000)
	// (the lines after the subquery are the FK cascades, run only for rows
	// actually deleted)
	if !strings.HasPrefix(plan, "SEARCH items USING INTEGER PRIMARY KEY (rowid=?)\n") || strings.Contains(plan, "items_tmdb") ||
		!strings.Contains(plan, "SEARCH tracks USING COVERING INDEX tracks_file_start (file_id=?)") {
		t.Fatalf("stale track delete scans:\n%s", plan)
	}
	plan = queryPlan(t, r, folderFilesSQL(3), "/a", "/b", "/c")
	if strings.Contains(plan, "SCAN f") || strings.Contains(plan, "SCAN files") {
		t.Fatalf("folder files scan:\n%s", plan)
	}
}

// The folder range must not catch a sibling folder whose name extends it.
func TestUnchangedFolderOwnAlbumsOnly(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	for _, k := range []struct{ key, sig string }{{"/m/Кино|группа крови", "s1"}, {"/m/Кино 2|x", "other"}, {"/m/Кино/CD1|y", "other"}} {
		if _, err := r.db.Exec(`INSERT INTO items(kind,title,sort_title,match_key,meta_json,added_at,updated_at) VALUES('album','a','a',?,json_object('scan_sig',?),'t','t')`, k.key, k.sig); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.db.QueryContext(ctx, albumSigsSQL, "/m/Кино")
	if err != nil {
		t.Fatal(err)
	}
	var sigs []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		sigs = append(sigs, s)
	}
	rows.Close()
	if len(sigs) != 1 || sigs[0] != "s1" {
		t.Fatalf("albums of /m/Кино: %v", sigs)
	}
}

// F16: a flat dump of more audio files than SQLite binds in one query must
// not fail (and so abort) the music scan.
func TestUnchangedFolderManyFiles(t *testing.T) {
	r := newRepo(t)
	if _, err := r.db.Exec(`INSERT INTO items(kind,title,sort_title,match_key,meta_json,added_at,updated_at) VALUES('album','a','a','/m|a',json_object('scan_sig','sig'),'t','t')`); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 33000)
	for i := range paths {
		paths[i] = fmt.Sprintf("/m/%05d.mp3", i)
	}
	_, _, ok, err := r.unchangedFolder(context.Background(), "/m", "sig", paths)
	if err != nil || ok {
		t.Fatalf("33000 paths: ok=%v err=%v", ok, err)
	}
}
