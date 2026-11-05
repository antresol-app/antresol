package library

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// DeleteSet is what deleting an item from the library covers.
type DeleteSet struct {
	Item Item
	// Items: the item and everything shown under it (seasons and
	// episodes, tracks, volumes).
	Items []int64
	// Files go with the item: the files its items own or their tracks play
	// from, unless an item outside the set uses the file too.
	Files []File
	// Shared: files of the set an item outside it uses as well (another
	// album's tracks playing from the same CUE image); they stay.
	Shared []File
}

// Size is the total size of the files that go.
func (d DeleteSet) Size() int64 {
	var n int64
	for _, f := range d.Files {
		n += f.Size
	}
	return n
}

// deletableKinds: what the library deletes as a whole (a track or an
// artist is not: they go with their album).
var deletableKinds = map[string]bool{
	KindMovie: true, KindSeries: true, KindSeason: true, KindEpisode: true, KindAlbum: true,
	KindBook: true, KindAudiobook: true, KindComic: true, KindComicVolume: true,
}

// ErrNotDeletable: the item is of a kind that is not deleted on its own.
var ErrNotDeletable = errors.New("library: this kind of item is not deleted on its own")

// subtreeCTE selects the shown item ?1 and its shown descendants as sub(id).
const subtreeCTE = `WITH RECURSIVE sub(id) AS (
  SELECT id FROM items WHERE id=?1 AND missing_since IS NULL
  UNION SELECT c.id FROM items c JOIN sub ON c.parent_id=sub.id WHERE c.missing_since IS NULL)`

// DeleteSet returns what deleting the item would take away (ErrNotFound
// for a hidden or unknown item, ErrNotDeletable for a track or an artist).
func (r *Repo) DeleteSet(ctx context.Context, id int64) (DeleteSet, error) {
	var d DeleteSet
	it, err := r.Item(ctx, id)
	if err != nil {
		return d, err
	}
	d.Item = it
	if !deletableKinds[it.Kind] {
		return d, ErrNotDeletable
	}
	rows, err := r.db.QueryContext(ctx, subtreeCTE+` SELECT id FROM sub ORDER BY id`, id)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var x int64
		if err := rows.Scan(&x); err != nil {
			rows.Close()
			return d, err
		}
		d.Items = append(d.Items, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}
	// shared = owned by an item outside the set, or played by a shown
	// track outside it
	rows, err = r.db.QueryContext(ctx, subtreeCTE+` SELECT `+fileCols+`,
	    (files.item_id NOT IN (SELECT id FROM sub) OR EXISTS (SELECT 1 FROM tracks t JOIN items ti ON ti.id=t.item_id
	       WHERE t.file_id=files.id AND ti.missing_since IS NULL AND t.item_id NOT IN (SELECT id FROM sub)))
	  FROM files WHERE missing_since IS NULL
	    AND (item_id IN (SELECT id FROM sub) OR id IN (SELECT t.file_id FROM tracks t WHERE t.item_id IN (SELECT id FROM sub)))
	  ORDER BY path`, id)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var f File
		var shared bool
		if err := rows.Scan(&f.ID, &f.ItemID, &f.Path, &f.Size, &f.Mtime, &f.DurationMs, &f.ProbeJSON, &f.TorrentInfohash, &shared); err != nil {
			return d, err
		}
		if shared {
			d.Shared = append(d.Shared, f)
		} else {
			d.Files = append(d.Files, f)
		}
	}
	return d, rows.Err()
}

// FilesOfLink lists the shown files made from the download ih
// (files.torrent_infohash, any case).
func (r *Repo) FilesOfLink(ctx context.Context, ih string) ([]File, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileCols+` FROM files WHERE lower(torrent_infohash)=lower(?) AND missing_since IS NULL ORDER BY path`, ih)
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

// Forget hides items and files at once, as a scan does when their files
// have gone (missing_since: a restore from the trash and the next scan
// bring them back with their progress; MissingGrace later they are
// purged), then hides the parents left with nothing.
func (r *Repo) Forget(ctx context.Context, items, files []int64) error {
	ts := now()
	return r.inTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []struct {
			table string
			ids   []int64
		}{{"files", files}, {"items", items}} {
			for part := range chunks(q.ids, 500) {
				args := []any{ts}
				for _, id := range part {
					args = append(args, id)
				}
				if _, err := tx.ExecContext(ctx, `UPDATE `+q.table+` SET missing_since=? WHERE missing_since IS NULL AND id IN (?`+
					strings.Repeat(",?", len(part)-1)+`)`, args...); err != nil {
					return err
				}
			}
		}
		return hideEmpty(ctx, tx, ts)
	})
}

// chunks yields ids in parts of at most n.
func chunks(ids []int64, n int) func(func([]int64) bool) {
	return func(yield func([]int64) bool) {
		for len(ids) > 0 {
			k := min(n, len(ids))
			if !yield(ids[:k]) {
				return
			}
			ids = ids[k:]
		}
	}
}
