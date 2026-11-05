package library

import (
	"context"
	"fmt"
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
	var n, ids, revs int64
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(id),0), COALESCE(SUM(probe_rev),0)
	  FROM files WHERE missing_since IS NULL AND item_id IN (`+showEpisodes+`)`, id).Scan(&n, &ids, &revs)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d/%d/%d", n, ids, revs), nil
}
