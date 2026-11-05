package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/release"
)

// BookInput is one e-book as the book scanner sees it: all its formats
// (files of one folder with the same parsed title, best format first) and
// the merged metadata. Cover is "file:<path>" or a name in ArtDir.
type BookInput struct {
	Files                              []FileInput
	Title                              string
	Authors                            []string
	Series                             string
	SeriesIndex                        float64
	Lang, Description, ISBN, Publisher string
	Year                               int
	Cover                              string

	sig string // meta_json.scan_sig: what the scan read (see bookSig)
}

// bookMatchKey is items.match_key of a book (see model.go), computed from
// the path of one of its files.
func bookMatchKey(path string) string {
	_, title := parseBookFile(filepath.Base(path))
	return filepath.Dir(path) + "|" + release.Normalize(title)
}

// UpsertBook creates or refreshes a book with its files, in one
// transaction. The book is found by match_key, else by one of its files.
// The scanner owns the title and its meta_json keys (authors, series,
// series_index, lang, isbn, publisher); year and overview are only filled
// when empty, and the poster is replaced only if the scanner set it.
func (r *Repo) UpsertBook(ctx context.Context, b BookInput) (int64, error) {
	if len(b.Files) == 0 || b.Files[0].Path == "" {
		return 0, errors.New("library: book needs a file")
	}
	title := strings.TrimSpace(b.Title)
	if title == "" {
		_, title = parseBookFile(filepath.Base(b.Files[0].Path))
	}
	key := bookMatchKey(b.Files[0].Path)
	var id int64
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		var scanCover sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT id, json_extract(meta_json,'$.scan_cover') FROM items WHERE kind='book' AND match_key=? ORDER BY id LIMIT 1`,
			key).Scan(&id, &scanCover)
		if errors.Is(err, sql.ErrNoRows) {
			for _, f := range b.Files {
				err = tx.QueryRowContext(ctx, `SELECT i.id, json_extract(i.meta_json,'$.scan_cover') FROM files f JOIN items i ON i.id=f.item_id
				  WHERE f.path=? AND i.kind='book'`, f.Path).Scan(&id, &scanCover)
				if !errors.Is(err, sql.ErrNoRows) {
					break
				}
			}
		}
		ts := now()
		if errors.Is(err, sql.ErrNoRows) {
			res, err := tx.ExecContext(ctx, `INSERT INTO items(kind,title,sort_title,year,overview,poster,match_key,added_at,updated_at)
			  VALUES('book',?,?,?,?,?,?,?,?)`, title, sortTitle(title), b.Year, b.Description, nullStr(b.Cover), key, ts, ts)
			if err != nil {
				return err
			}
			if id, err = res.LastInsertId(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET title=?1, sort_title=?2, match_key=?3, updated_at=?4
			  WHERE id=?5 AND (title IS NOT ?1 OR sort_title IS NOT ?2 OR match_key IS NOT ?3)`,
				title, sortTitle(title), key, ts, id); err != nil {
				return err
			}
			if err := fillEmpty(ctx, tx, id, "", b.Year, b.Description, "", 0, "", ""); err != nil {
				return err
			}
			if b.Cover != "" {
				if _, err := tx.ExecContext(ctx, `UPDATE items SET poster=?1 WHERE id=?2 AND poster IS NOT ?1 AND (poster IS NULL OR poster=?3)`,
					b.Cover, id, scanCover.String); err != nil {
					return err
				}
			} else if scanCover.String != "" {
				// the cover the scanner set is gone (X.jpg deleted)
				if _, err := tx.ExecContext(ctx, `UPDATE items SET poster=NULL WHERE id=? AND poster=?`, id, scanCover.String); err != nil {
					return err
				}
			}
		}
		var authors []string
		for _, a := range b.Authors {
			if a = strings.TrimSpace(a); a != "" {
				authors = append(authors, a)
			}
		}
		// keys the scanner no longer finds are removed (nil), others' kept
		meta := map[string]any{"authors": nil, "series_index": nil}
		if len(authors) > 0 {
			meta["authors"] = authors
		}
		for k, v := range map[string]string{"series": b.Series, "lang": b.Lang, "isbn": b.ISBN, "publisher": b.Publisher,
			"scan_cover": b.Cover, "scan_sig": b.sig} {
			if v = strings.TrimSpace(v); v != "" {
				meta[k] = v
			} else {
				meta[k] = nil
			}
		}
		if strings.TrimSpace(b.Series) != "" && b.SeriesIndex != 0 {
			meta["series_index"] = b.SeriesIndex
		}
		if err := setAlbumMeta(ctx, tx, id, meta); err != nil {
			return err
		}
		for _, f := range b.Files {
			if err := upsertFile(ctx, tx, id, f); err != nil {
				return err
			}
		}
		if err := addTitles(ctx, tx, id, append([]string{title, b.Series}, authors...)...); err != nil {
			return err
		}
		return found(ctx, tx, id)
	})
	return id, err
}

// storedBookFile is a visible files row of a visible book.
type storedBookFile struct {
	size, mtime, itemID int64
	sig                 string // the book's meta_json.scan_sig
}

// bookFilesUnder returns the book files stored under root by path, for the
// scanner's unchanged-book check.
func (r *Repo) bookFilesUnder(ctx context.Context, root string) (map[string]storedBookFile, error) {
	prefix := filepath.Clean(root) + string(filepath.Separator)
	rows, err := r.db.QueryContext(ctx, `SELECT f.path, f.size, f.mtime, f.item_id, COALESCE(json_extract(i.meta_json,'$.scan_sig'),'')
	  FROM files f JOIN items i ON i.id=f.item_id
	  WHERE i.kind='book' AND f.missing_since IS NULL AND i.missing_since IS NULL AND substr(f.path,1,?)=?`,
		utf8.RuneCountInString(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]storedBookFile{}
	for rows.Next() {
		var p string
		var f storedBookFile
		if err := rows.Scan(&p, &f.size, &f.mtime, &f.itemID, &f.sig); err != nil {
			return nil, err
		}
		out[p] = f
	}
	return out, rows.Err()
}

// AudiobookInput is one audiobook as the audiobook scanner sees it. Dir is
// the book folder ("" for a single file in an author folder or the root:
// the book is then keyed by that file). Files are in playback order and
// every chapter's FileIndex points into them. Cover is "file:<path>" or a
// name in ArtDir.
type AudiobookInput struct {
	Dir                string
	Files              []FileInput
	Title              string
	Authors            []string
	Narrator, Series   string
	SeriesIndex        float64
	Year               int
	Description, Cover string
	Chapters           []ChapterInput

	sig    string // meta_json.scan_sig: what the scan read (see audiobookSig)
	probed string // meta_json.chapters_probed: the files the chapter probe read without error
}

// Chapter is a stored audiobook chapter; StartMs/EndMs are positions within
// its file (EndMs nil = to the end of the file).
type Chapter struct {
	Idx     int    `json:"idx"`
	Title   string `json:"title"`
	FileID  int64  `json:"fileId"`
	StartMs int64  `json:"startMs"`
	EndMs   *int64 `json:"endMs,omitempty"`
}

// audiobookMatchKey is items.match_key of an audiobook (see model.go).
func audiobookMatchKey(a AudiobookInput) string {
	if a.Dir != "" {
		return a.Dir
	}
	return a.Files[0].Path
}

// UpsertAudiobook creates or refreshes an audiobook in one transaction:
// found by match_key, else by one of its files. All files are attached to
// it, meta_json.files_order lists their ids in playback order and the
// chapters are replaced as a whole. The scanner owns the title and its
// meta_json keys (authors, narrator, series, series_index); year and
// overview are only filled when empty, the poster is replaced only if the
// scanner set it.
func (r *Repo) UpsertAudiobook(ctx context.Context, a AudiobookInput) (int64, error) {
	if len(a.Files) == 0 || a.Files[0].Path == "" {
		return 0, errors.New("library: audiobook needs a file")
	}
	for _, c := range a.Chapters {
		if c.FileIndex < 0 || c.FileIndex >= len(a.Files) {
			return 0, fmt.Errorf("library: chapter %q points at file %d of %d", c.Title, c.FileIndex, len(a.Files))
		}
	}
	title := strings.TrimSpace(a.Title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(a.Files[0].Path), filepath.Ext(a.Files[0].Path))
	}
	key := audiobookMatchKey(a)
	var id int64
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		var scanCover sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT id, json_extract(meta_json,'$.scan_cover') FROM items WHERE kind='audiobook' AND match_key=? ORDER BY id LIMIT 1`,
			key).Scan(&id, &scanCover)
		if errors.Is(err, sql.ErrNoRows) {
			for _, f := range a.Files {
				err = tx.QueryRowContext(ctx, `SELECT i.id, json_extract(i.meta_json,'$.scan_cover') FROM files f JOIN items i ON i.id=f.item_id
				  WHERE f.path=? AND i.kind='audiobook'`, f.Path).Scan(&id, &scanCover)
				if !errors.Is(err, sql.ErrNoRows) {
					break
				}
			}
		}
		ts := now()
		if errors.Is(err, sql.ErrNoRows) {
			res, err := tx.ExecContext(ctx, `INSERT INTO items(kind,title,sort_title,year,overview,poster,match_key,added_at,updated_at)
			  VALUES('audiobook',?,?,?,?,?,?,?,?)`, title, sortTitle(title), a.Year, a.Description, nullStr(a.Cover), key, ts, ts)
			if err != nil {
				return err
			}
			if id, err = res.LastInsertId(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET title=?1, sort_title=?2, match_key=?3, updated_at=?4
			  WHERE id=?5 AND (title IS NOT ?1 OR sort_title IS NOT ?2 OR match_key IS NOT ?3)`,
				title, sortTitle(title), key, ts, id); err != nil {
				return err
			}
			if err := fillEmpty(ctx, tx, id, "", a.Year, a.Description, "", 0, "", ""); err != nil {
				return err
			}
			if a.Cover != "" {
				if _, err := tx.ExecContext(ctx, `UPDATE items SET poster=?1 WHERE id=?2 AND poster IS NOT ?1 AND (poster IS NULL OR poster=?3)`,
					a.Cover, id, scanCover.String); err != nil {
					return err
				}
			} else if scanCover.String != "" {
				// the cover the scanner set is gone
				if _, err := tx.ExecContext(ctx, `UPDATE items SET poster=NULL WHERE id=? AND poster=?`, id, scanCover.String); err != nil {
					return err
				}
			}
		}
		ids := make([]int64, len(a.Files))
		for i, f := range a.Files {
			fid, err := upsertFileID(ctx, tx, id, f)
			if err != nil {
				return err
			}
			ids[i] = fid
		}
		var authors []string
		for _, s := range a.Authors {
			if s = strings.TrimSpace(s); s != "" {
				authors = append(authors, s)
			}
		}
		// keys the scanner no longer finds are removed (nil), others' kept
		meta := map[string]any{"authors": nil, "narrator": nil, "series": nil, "series_index": nil, "files_order": ids}
		if len(authors) > 0 {
			meta["authors"] = authors
		}
		narrator, series := strings.TrimSpace(a.Narrator), strings.TrimSpace(a.Series)
		if narrator != "" {
			meta["narrator"] = narrator
		}
		if series != "" {
			meta["series"] = series
			if a.SeriesIndex != 0 {
				meta["series_index"] = a.SeriesIndex
			}
		}
		for k, v := range map[string]string{"scan_cover": a.Cover, "scan_sig": a.sig, "chapters_probed": a.probed} {
			if v != "" {
				meta[k] = v
			} else {
				meta[k] = nil
			}
		}
		if err := setAlbumMeta(ctx, tx, id, meta); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM chapters WHERE item_id=?`, id); err != nil {
			return err
		}
		for i, c := range a.Chapters {
			if _, err := tx.ExecContext(ctx, `INSERT INTO chapters(item_id,idx,title,file_id,start_ms,end_ms) VALUES(?,?,?,?,?,?)`,
				id, i, c.Title, ids[c.FileIndex], c.StartMs, c.EndMs); err != nil {
				return err
			}
		}
		if err := addTitles(ctx, tx, id, append([]string{title, series, narrator}, authors...)...); err != nil {
			return err
		}
		return found(ctx, tx, id)
	})
	return id, err
}

// Chapters returns the chapters of an audiobook in order.
func (r *Repo) Chapters(ctx context.Context, audiobookID int64) ([]Chapter, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT idx, title, file_id, start_ms, end_ms FROM chapters WHERE item_id=? ORDER BY idx`, audiobookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chapter
	for rows.Next() {
		var c Chapter
		var end sql.NullInt64
		if err := rows.Scan(&c.Idx, &c.Title, &c.FileID, &c.StartMs, &end); err != nil {
			return nil, err
		}
		if end.Valid {
			v := end.Int64
			c.EndMs = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// storedAudiobook is what a rescan compares with: the visible audiobook
// with a match_key, its scan signature, chapter-probe marker and visible
// files (path → files.id).
type storedAudiobook struct {
	id          int64
	sig, probed string
	files       map[string]int64
}

func (r *Repo) storedAudiobook(ctx context.Context, key string) (storedAudiobook, bool, error) {
	var a storedAudiobook
	err := r.db.QueryRowContext(ctx, `SELECT id, COALESCE(json_extract(meta_json,'$.scan_sig'),''), COALESCE(json_extract(meta_json,'$.chapters_probed'),'')
	  FROM items WHERE kind='audiobook' AND match_key=? AND missing_since IS NULL ORDER BY id LIMIT 1`, key).Scan(&a.id, &a.sig, &a.probed)
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil
	}
	if err != nil {
		return a, false, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT path, id FROM files WHERE item_id=? AND missing_since IS NULL`, a.id)
	if err != nil {
		return a, false, err
	}
	defer rows.Close()
	a.files = map[string]int64{}
	for rows.Next() {
		var p string
		var id int64
		if err := rows.Scan(&p, &id); err != nil {
			return a, false, err
		}
		a.files[p] = id
	}
	return a, true, rows.Err()
}

// storedChapters returns an audiobook's chapters with FileIndex set by the
// position of their file's path in paths; ok is false when a chapter's file
// is not among them.
func (r *Repo) storedChapters(ctx context.Context, id int64, paths []string) (chs []ChapterInput, ok bool, err error) {
	pos := make(map[string]int, len(paths))
	for i, p := range paths {
		pos[p] = i
	}
	rows, err := r.db.QueryContext(ctx, `SELECT c.title, c.start_ms, c.end_ms, f.path FROM chapters c JOIN files f ON f.id=c.file_id
	  WHERE c.item_id=? ORDER BY c.idx`, id)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ChapterInput
		var end sql.NullInt64
		var p string
		if err := rows.Scan(&c.Title, &c.StartMs, &end, &p); err != nil {
			return nil, false, err
		}
		i, found := pos[p]
		if !found {
			return nil, false, nil
		}
		c.FileIndex = i
		if end.Valid {
			v := end.Int64
			c.EndMs = &v
		}
		chs = append(chs, c)
	}
	return chs, len(chs) > 0, rows.Err()
}

// BookQuery filters Books. Kind is book, audiobook or comic; Sort: added
// (default, newest first), title, author (first author, a comic's writer).
// MaxRating and Owner as in ListQuery.
type BookQuery struct {
	Kind          string
	Sort          string
	Limit, Offset int
	MaxRating     string
	Owner         int64
}

// Books returns a page of books, audiobooks or comic series and the total.
func (r *Repo) Books(ctx context.Context, q BookQuery) ([]Item, int, error) {
	if q.Kind != KindBook && q.Kind != KindAudiobook && q.Kind != KindComic {
		return nil, 0, fmt.Errorf("library: books of kind %q", q.Kind)
	}
	where := []string{"i.kind=?", "i.missing_since IS NULL"}
	args := []any{q.Kind}
	if q.MaxRating != "" {
		c, a := allowedRatings(q.MaxRating)
		where = append(where, c)
		args = append(args, a...)
	}
	if q.Owner != 0 {
		c, a := ownedBy("i.id", q.Owner)
		where = append(where, c)
		args = append(args, a...)
	}
	w := " WHERE " + strings.Join(where, " AND ")
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items i`+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "i.added_at DESC, i.id DESC"
	switch q.Sort {
	case "title":
		order = "i.sort_title, i.id"
	case "author":
		order = "COALESCE(json_extract(i.meta_json,'$.authors[0]'), json_extract(i.meta_json,'$.writer'), '') COLLATE NOCASE, i.sort_title, i.id"
	}
	limit := q.Limit
	if limit <= 0 {
		limit = -1
	}
	args = append(args, limit, max(q.Offset, 0))
	items, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items i`+w+` ORDER BY `+order+` LIMIT ? OFFSET ?`, args...)
	if items == nil {
		items = []Item{}
	}
	return items, total, err
}

// Volumes returns the visible volumes of a comic series in reading order
// (volume, chapter, file name in natural order).
func (r *Repo) Volumes(ctx context.Context, comicID int64) ([]Item, error) {
	vols, err := r.comicVolumes(ctx, comicID)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(vols))
	for _, v := range vols {
		out = append(out, v.item)
	}
	return out, nil
}

// VolumeCount is the number of visible volumes of a comic series.
func (r *Repo) VolumeCount(ctx context.Context, comicID int64) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE parent_id=? AND kind='comic_volume' AND missing_since IS NULL`, comicID).Scan(&n)
	return n, err
}

// FindBook finds a visible book or audiobook (kind) by its title and, when
// author is given, an author sharing a name word of 3+ letters with it
// ("Михаил Булгаков" = "Булгаков М."); ok is false when there is none.
func (r *Repo) FindBook(ctx context.Context, kind, author, title string) (Item, bool, error) {
	t := release.Normalize(title)
	if t == "" || (kind != KindBook && kind != KindAudiobook) {
		return Item{}, false, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT i.id, COALESCE(json_extract(i.meta_json,'$.authors'),'[]') FROM items i
	  WHERE i.kind=? AND i.missing_since IS NULL
	    AND (i.sort_title=? OR EXISTS (SELECT 1 FROM item_titles n WHERE n.item_id=i.id AND n.norm=?))
	  ORDER BY i.id LIMIT 50`, kind, t, t)
	if err != nil {
		return Item{}, false, err
	}
	var match int64
	for rows.Next() && match == 0 {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return Item{}, false, err
		}
		var authors []string
		json.Unmarshal([]byte(raw), &authors) // not a list: no author known
		if sameAuthor(author, authors) {
			match = id
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || match == 0 {
		return Item{}, false, err
	}
	return found1(r.Item(ctx, match))
}

// sameAuthor: no author to compare on either side, or a shared name word
// of 3+ letters.
func sameAuthor(author string, authors []string) bool {
	want := map[string]bool{}
	for _, w := range strings.Fields(release.Normalize(author)) {
		if utf8.RuneCountInString(w) >= 3 {
			want[w] = true
		}
	}
	if len(want) == 0 || len(authors) == 0 {
		return true
	}
	for _, a := range authors {
		for _, w := range strings.Fields(release.Normalize(a)) {
			if want[w] {
				return true
			}
		}
	}
	return false
}
