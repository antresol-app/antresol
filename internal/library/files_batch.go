package library

import (
	"context"
	"slices"

	"github.com/antresol-app/antresol/internal/db"
)

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
	for part := range slices.Chunk(itemIDs, 500) {
		in, args := db.InList(part)
		files, err := r.queryFiles(ctx, `SELECT `+fileCols+` FROM files WHERE item_id IN `+in+` AND missing_since IS NULL ORDER BY path`, args...)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			out[f.ItemID] = append(out[f.ItemID], f)
		}
	}
	return out, nil
}

// ItemsByID is Item of many ids at once: the shown items by id (unknown
// and hidden ids are left out).
func (r *Repo) ItemsByID(ctx context.Context, ids []int64) (map[int64]Item, error) {
	out := make(map[int64]Item, len(ids))
	for part := range slices.Chunk(ids, 500) {
		in, args := db.InList(part)
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
	for part := range slices.Chunk(ids, 500) {
		in, args := db.InList(part)
		files, err := r.queryFiles(ctx, `SELECT `+fileCols+` FROM files WHERE id IN `+in+` AND missing_since IS NULL`, args...)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			out[f.ID] = f
		}
	}
	return out, nil
}
