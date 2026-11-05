package library

import (
	"context"
	"strings"
)

// inArgs is "(?,?…)" and the arguments of ids.
func inArgs(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(?" + strings.Repeat(",?", len(ids)-1) + ")", args
}

// FilePaths lists the path of every file the library has, those it marks
// missing too (the path index alone, never the rows): what a delete of
// download data checks it never takes.
func (r *Repo) FilePaths(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT path FROM files ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// FilesOfItems is FilesOf of many items at once (a page of cards): each
// item's present files, by path; items without any are left out.
func (r *Repo) FilesOfItems(ctx context.Context, itemIDs []int64) (map[int64][]File, error) {
	out := map[int64][]File{}
	for part := range chunks(itemIDs, 500) {
		in, args := inArgs(part)
		rows, err := r.db.QueryContext(ctx, `SELECT `+fileCols+` FROM files WHERE item_id IN `+in+` AND missing_since IS NULL ORDER BY path`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			f, err := scanFile(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[f.ItemID] = append(out[f.ItemID], f)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ItemsByID is Item of many ids at once: the shown items by id (unknown
// and hidden ids are left out).
func (r *Repo) ItemsByID(ctx context.Context, ids []int64) (map[int64]Item, error) {
	out := make(map[int64]Item, len(ids))
	for part := range chunks(ids, 500) {
		in, args := inArgs(part)
		items, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items i WHERE i.id IN `+in+` AND i.missing_since IS NULL`, args...)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			out[it.ID] = it
		}
	}
	return out, nil
}

// FilesByID returns the present files with these ids by id (unknown and
// missing ids are left out). Subsonic lists need the size and path of
// hundreds of tracks at once.
func (r *Repo) FilesByID(ctx context.Context, ids []int64) (map[int64]File, error) {
	out := make(map[int64]File, len(ids))
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		part := ids[start:min(start+chunk, len(ids))]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		rows, err := r.db.QueryContext(ctx, `SELECT `+fileCols+` FROM files WHERE id IN (?`+strings.Repeat(",?", len(part)-1)+`) AND missing_since IS NULL`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			f, err := scanFile(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[f.ID] = f
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
