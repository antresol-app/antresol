package library

import (
	"context"
	"strings"
)

// «Моя медиатека»: item_owners holds who brought a title into the library
// (migration 0037). The owned unit is the title a library page lists: a
// movie, a series (whichever season or episode was asked for), an album, a
// book, an audiobook or a comic series. An artist is a profile's when one
// of its albums is.

// unitOf is the SQL of the owned unit of item i (with p its parent, LEFT
// JOINed): an episode's series, a season's series, a track's album, a
// comic volume's series, else the item itself. NULL for an orphan.
const unitOf = `CASE i.kind
  WHEN 'episode' THEN p.parent_id
  WHEN 'season' THEN i.parent_id
  WHEN 'track' THEN i.parent_id
  WHEN 'comic_volume' THEN i.parent_id
  ELSE i.id END`

// ownedBy is the condition «col is a title owner brought» (col: an items
// id column) and its argument.
func ownedBy(col string, owner int64) (string, []any) {
	return col + ` IN (SELECT item_id FROM item_owners WHERE user_id=?)`, []any{owner}
}

// artistOwnedBy is the condition «artist col has an album owner brought».
func artistOwnedBy(col string, owner int64) (string, []any) {
	return `EXISTS (SELECT 1 FROM items oa JOIN item_owners o ON o.item_id=oa.id WHERE oa.parent_id=` + col +
		` AND oa.kind='album' AND oa.missing_since IS NULL AND o.user_id=?)`, []any{owner}
}

// claimWhere records userID as an owner of the units of the files matching
// cond (on f). userID 0 (nobody asked: a scan, a migration) records nothing.
func (r *Repo) claimWhere(ctx context.Context, userID int64, cond string, args ...any) error {
	if userID == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO item_owners (item_id, user_id)
	  SELECT unit, ? FROM (SELECT DISTINCT `+unitOf+` AS unit FROM files f JOIN items i ON i.id=f.item_id
	    LEFT JOIN items p ON p.id=i.parent_id WHERE `+cond+`)
	  WHERE unit IS NOT NULL AND EXISTS (SELECT 1 FROM users WHERE id=?)`, append(append([]any{userID}, args...), userID)...)
	return err
}

// ClaimTorrent makes userID an owner of the titles of the library files
// bound to the download ih (files.torrent_infohash): organize calls it once
// the files are in place.
func (r *Repo) ClaimTorrent(ctx context.Context, ih string, userID int64) error {
	return r.claimWhere(ctx, userID, `f.torrent_infohash=?`, strings.ToLower(ih))
}

// ClaimPaths makes userID an owner of the titles of these library files (a
// direct download's book). Paths the scanner has no row for are skipped.
func (r *Repo) ClaimPaths(ctx context.Context, paths []string, userID int64) error {
	if len(paths) == 0 {
		return nil
	}
	args := make([]any, 0, len(paths))
	for _, p := range paths {
		args = append(args, p)
	}
	return r.claimWhere(ctx, userID, `f.path IN (?`+strings.Repeat(",?", len(paths)-1)+`)`, args...)
}

// ClaimItem makes userID an owner of the title of item id (its unit): an
// import's album found in the library already.
func (r *Repo) ClaimItem(ctx context.Context, id, userID int64) error {
	if userID == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO item_owners (item_id, user_id)
	  SELECT unit, ? FROM (SELECT `+unitOf+` AS unit FROM items i LEFT JOIN items p ON p.id=i.parent_id WHERE i.id=?)
	  WHERE unit IS NOT NULL AND EXISTS (SELECT 1 FROM users WHERE id=?)`, userID, id, userID)
	return err
}

// Owners lists the owners of item id's title (its unit), lowest id first.
func (r *Repo) Owners(ctx context.Context, id int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT o.user_id FROM items i LEFT JOIN items p ON p.id=i.parent_id
	  JOIN item_owners o ON o.item_id=`+unitOf+` WHERE i.id=? ORDER BY o.user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var u int64
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
