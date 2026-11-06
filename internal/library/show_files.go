package library

import (
	"context"
	"fmt"
	"strings"
)

// showEpisodes selects the ids of the present episodes of a series or a
// season (?1 is the series' or season's id): an episode hangs under a
// season, a season under its series. Lookups by parent only (a UNION, not
// an OR; "+kind" keeps the kind index out): short of statistics, the
// planner otherwise walked every episode of the library by kind.
const showEpisodes = `SELECT e.id FROM items e WHERE e.parent_id=?1 AND +e.kind='episode' AND e.missing_since IS NULL
  UNION ALL
  SELECT e.id FROM items s CROSS JOIN items e ON e.parent_id=s.id WHERE s.parent_id=?1 AND +s.kind='season' AND s.missing_since IS NULL
    AND +e.kind='episode' AND e.missing_since IS NULL`

// ShowFiles returns the present files of every present episode of a series
// or a season in one query (the stream facts of a show: its badges).
func (r *Repo) ShowFiles(ctx context.Context, id int64) ([]File, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileCols+` FROM files WHERE missing_since IS NULL AND item_id IN (`+showEpisodes+`) ORDER BY path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ShowFilesKey is a cheap fingerprint of ShowFiles: it changes when an
// episode file appears, goes missing or gets, loses or rewrites its probe
// (files.probe_rev, moved by a trigger on every probe_json change), so a
// cache of what the files say is rebuilt only then. It reads only the
// covering index files_item_probe_rev, never the probe_json blobs.
func (r *Repo) ShowFilesKey(ctx context.Context, id int64) (string, error) {
	keys, err := r.ShowFilesKeys(ctx, []int64{id})
	return keys[id], err
}

// ShowFilesKeys is ShowFilesKey of many series or seasons in one query (a
// grid of them).
func (r *Repo) ShowFilesKeys(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
		out[id] = "0/0/0"
	}
	in := "?" + strings.Repeat(",?", len(ids)-1)
	// showEpisodes for many shows: each branch keeps its show's id
	rows, err := r.db.QueryContext(ctx, `SELECT k.show, COUNT(*), SUM(f.id), SUM(f.probe_rev) FROM (
	    SELECT e.parent_id AS show, e.id AS ep FROM items e WHERE e.parent_id IN (`+in+`) AND +e.kind='episode' AND e.missing_since IS NULL
	    UNION ALL
	    SELECT s.parent_id, e.id FROM items s CROSS JOIN items e ON e.parent_id=s.id WHERE s.parent_id IN (`+in+`) AND +s.kind='season'
	      AND s.missing_since IS NULL AND +e.kind='episode' AND e.missing_since IS NULL
	  ) k JOIN files f ON f.item_id=k.ep AND f.missing_since IS NULL GROUP BY k.show`, append(args, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, n, sum, revs int64
		if err := rows.Scan(&id, &n, &sum, &revs); err != nil {
			return nil, err
		}
		out[id] = fmt.Sprintf("%d/%d/%d", n, sum, revs)
	}
	return out, rows.Err()
}
