package library

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// FolderFlags are the library folders (absolute) marked «Только для
// взрослых» (Adult) and «Скрыть из общих подборок» (Unlisted). The marks
// live on the items (items.adult, items.unlisted): an item is marked when
// one of its files, or of its children's, lies in a marked folder, and the
// parents of a marked item are marked too (a series with one episode in an
// adults-only folder is an adults-only series: a limited profile sees a
// title whole or not at all, as with its rating).
type FolderFlags struct {
	Adult, Unlisted []string
}

// of: path lies in an adults-only folder, an unlisted one.
func (f FolderFlags) of(path string) (adult, unlisted bool) {
	return underAny(f.Adult, path), underAny(f.Unlisted, path)
}

func underAny(roots []string, p string) bool {
	for _, r := range roots {
		if strings.HasPrefix(p, dirPrefix(r)) {
			return true
		}
	}
	return false
}

// dirPrefix is root, clean, with one trailing separator.
func dirPrefix(root string) string {
	p := filepath.Clean(root)
	if !strings.HasSuffix(p, string(filepath.Separator)) {
		p += string(filepath.Separator)
	}
	return p
}

// pathRange is the [lo, hi) range of paths under root in byte order ('0'
// follows '/'), so the files' path index finds them.
func pathRange(root string) (lo, hi string) {
	lo = dirPrefix(root)
	return lo, lo[:len(lo)-1] + string(rune(filepath.Separator)+1)
}

// RatingOf is the SQL of the age rating the visibility rules judge for the
// items aliased alias: their own, or 18+ for what came from an adults-only
// folder. Every SQL filter by rating reads this, not alias.rating, so an
// adults-only folder needs no filter of its own: a profile with an age
// limit below 18+ (every kid) never sees it, an unlimited one does.
func RatingOf(alias string) string {
	return "(CASE WHEN " + alias + ".adult=1 THEN '" + adultRating + "' ELSE " + alias + ".rating END)"
}

// folderFlags is the repository's copy of the scanner's marks.
type folderFlags struct{ p atomic.Pointer[FolderFlags] }

// SetFolderFlags gives the repository the folders' marks: what is stored in
// a marked folder from now on is marked at once.
func (r *Repo) SetFolderFlags(f FolderFlags) { r.flags.p.Store(&f) }

func (r *Repo) folderFlags() FolderFlags {
	if f := r.flags.p.Load(); f != nil {
		return *f
	}
	return FolderFlags{}
}

// markFile marks item (and its parents) when path lies in a marked folder;
// SyncFolderFlags later takes off what no longer applies.
func (r *Repo) markFile(ctx context.Context, x execer, itemID int64, path string) error {
	adult, unlisted := r.folderFlags().of(path)
	if !adult && !unlisted {
		return nil
	}
	a, u := b2i(adult), b2i(unlisted)
	for i := 0; i < 4 && itemID != 0; i++ {
		var parent int64
		err := x.QueryRowContext(ctx, `UPDATE items SET adult=MAX(adult, ?), unlisted=MAX(unlisted, ?) WHERE id=?
		  RETURNING COALESCE(parent_id, 0)`, a, u, itemID).Scan(&parent)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		itemID = parent
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SyncFolderFlags sets the marks of every item from the folders' (the ones
// SetFolderFlags gave): marked are the items with a file in a marked
// folder, the tracks playing from one, and the parents of marked items;
// every other item loses its mark. Only the rows that change are written.
func (r *Repo) SyncFolderFlags(ctx context.Context) error {
	f := r.folderFlags()
	return r.inTx(ctx, func(tx *sql.Tx) error {
		for _, m := range []struct {
			col   string
			roots []string
		}{{"adult", f.Adult}, {"unlisted", f.Unlisted}} {
			if err := syncFlag(ctx, tx, m.col, m.roots); err != nil {
				return err
			}
		}
		return nil
	})
}

// syncFlag is SyncFolderFlags of one mark (col: a constant).
func syncFlag(ctx context.Context, tx *sql.Tx, col string, roots []string) error {
	want := "temp.want_" + col
	steps := []string{`DROP TABLE IF EXISTS ` + want, `CREATE TEMP TABLE want_` + col + ` (id INTEGER PRIMARY KEY)`}
	for _, q := range steps {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	for _, root := range roots {
		lo, hi := pathRange(root)
		for _, q := range []string{
			`INSERT OR IGNORE INTO ` + want + ` SELECT item_id FROM files WHERE path >= ? AND path < ?`,
			`INSERT OR IGNORE INTO ` + want + ` SELECT t.item_id FROM tracks t JOIN files f ON f.id=t.file_id WHERE f.path >= ? AND f.path < ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, lo, hi); err != nil {
				return err
			}
		}
	}
	// the parents: episode → season → series, track → album → artist,
	// volume → comic
	for i := 0; i < 3; i++ {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+want+` SELECT i.parent_id FROM items i
		  WHERE i.parent_id IS NOT NULL AND i.id IN (SELECT id FROM `+want+`)`); err != nil {
			return err
		}
	}
	in := `(id IN (SELECT id FROM ` + want + `))`
	if _, err := tx.ExecContext(ctx, `UPDATE items SET `+col+`=`+in+` WHERE `+col+` <> `+in); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DROP TABLE `+want)
	return err
}

// ForgetFolder takes a folder out of the library (its files are left
// alone): every file row under it is gone from the library at once, as a
// file deleted from the disk is (hidden now, purged with its progress after
// MissingGrace: a folder added back within it comes back as it was).
func (r *Repo) ForgetFolder(ctx context.Context, root string) (int, error) {
	if filepath.Clean(root) == string(filepath.Separator) || !filepath.IsAbs(root) {
		return 0, nil
	}
	return r.removeMissing(ctx, root, nil)
}

// FolderCount is how many titles of the library have files under root (the
// count «Удалить папку» warns about).
func (r *Repo) FolderCount(ctx context.Context, root string) (int, error) {
	lo, hi := pathRange(root)
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT item_id) FROM files WHERE path >= ? AND path < ? AND missing_since IS NULL`, lo, hi).Scan(&n)
	return n, err
}
