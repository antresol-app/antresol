package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/release"
)

// ErrNotFound is returned when an item or file does not exist.
var ErrNotFound = errors.New("library: not found")

// Repo stores the library in SQLite.
type Repo struct{ db *sql.DB }

// NewRepo wraps an opened database (see db.Open).
func NewRepo(d *sql.DB) *Repo { return &Repo{db: d} }

// All item queries alias the table as "i".
const itemCols = `i.id, i.kind, COALESCE(i.parent_id,0), i.title, i.original_title, i.sort_title, i.year, i.overview, i.rating,
  COALESCE(i.tmdb_id,0), COALESCE(i.season_no,0), COALESCE(i.episode_no,0), COALESCE(i.poster,''), COALESCE(i.backdrop,''),
  i.added_at, i.updated_at, i.meta_json`

const fileCols = `id, item_id, path, size, mtime, COALESCE(duration_ms,0), COALESCE(probe_json,''), COALESCE(torrent_infohash,'')`

type scanner interface{ Scan(dest ...any) error }

func scanItem(s scanner) (Item, error) {
	var it Item
	var added, updated string
	err := s.Scan(&it.ID, &it.Kind, &it.ParentID, &it.Title, &it.OriginalTitle, &it.SortTitle, &it.Year, &it.Overview,
		&it.Rating, &it.TmdbID, &it.SeasonNo, &it.EpisodeNo, &it.Poster, &it.Backdrop, &added, &updated, &it.MetaJSON)
	if err != nil {
		return it, err
	}
	if it.AddedAt, err = time.Parse(time.RFC3339, added); err != nil {
		return it, fmt.Errorf("library: item %d added_at: %w", it.ID, err)
	}
	if it.UpdatedAt, err = time.Parse(time.RFC3339, updated); err != nil {
		return it, fmt.Errorf("library: item %d updated_at: %w", it.ID, err)
	}
	return it, nil
}

func scanFile(s scanner) (File, error) {
	var f File
	err := s.Scan(&f.ID, &f.ItemID, &f.Path, &f.Size, &f.Mtime, &f.DurationMs, &f.ProbeJSON, &f.TorrentInfohash)
	return f, err
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func sortTitle(t string) string { return release.Normalize(t) }

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

func (r *Repo) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func addTitles(ctx context.Context, x execer, id int64, titles ...string) error {
	for _, t := range titles {
		n := release.Normalize(t)
		if n == "" {
			continue
		}
		if _, err := x.ExecContext(ctx, `INSERT OR IGNORE INTO item_titles(item_id,title,norm) VALUES(?,?,?)`, id, strings.TrimSpace(t), n); err != nil {
			return err
		}
	}
	return nil
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// upsertFile adds or updates a file row. A file replaced in place (same
// path, other size or mtime) loses its cached probe: its streams and
// duration are probed again.
func upsertFile(ctx context.Context, x execer, itemID int64, f FileInput) error {
	_, err := upsertFileID(ctx, x, itemID, f)
	return err
}

// upsertFileID is upsertFile returning the id of the files row (new or
// existing), e.g. for tracks.file_id and chapters.file_id.
func upsertFileID(ctx context.Context, x execer, itemID int64, f FileInput) (int64, error) {
	var id int64
	err := x.QueryRowContext(ctx, `INSERT INTO files(item_id,path,size,mtime,torrent_infohash) VALUES(?,?,?,?,?)
	  ON CONFLICT(path) DO UPDATE SET item_id=excluded.item_id, size=excluded.size, mtime=excluded.mtime,
	  torrent_infohash=COALESCE(excluded.torrent_infohash, files.torrent_infohash), missing_since=NULL,
	  probe_json  = CASE WHEN files.size IS NOT excluded.size OR files.mtime IS NOT excluded.mtime THEN NULL ELSE files.probe_json END,
	  duration_ms = CASE WHEN files.size IS NOT excluded.size OR files.mtime IS NOT excluded.mtime THEN NULL ELSE files.duration_ms END
	  RETURNING id`,
		itemID, f.Path, f.Size, f.Mtime, nullStr(f.Infohash)).Scan(&id)
	return id, err
}

// fillEmpty updates only fields that are still empty on the stored item, so
// that a rescan never overwrites metadata enriched from TMDB.
func fillEmpty(ctx context.Context, x execer, id int64, orig string, year int, overview, rating string, tmdb int64, poster, backdrop string) error {
	_, err := x.ExecContext(ctx, `UPDATE items SET
	  original_title = CASE WHEN original_title='' THEN ?1 ELSE original_title END,
	  year           = CASE WHEN year=0 THEN ?2 ELSE year END,
	  overview       = CASE WHEN overview='' THEN ?3 ELSE overview END,
	  rating         = CASE WHEN rating='' THEN ?4 ELSE rating END,
	  tmdb_id        = COALESCE(tmdb_id, ?5),
	  poster         = COALESCE(poster, ?6),
	  backdrop       = COALESCE(backdrop, ?7)
	  WHERE id=?8 AND (
	    (original_title='' AND ?1<>'') OR (year=0 AND ?2<>0) OR (overview='' AND ?3<>'') OR (rating='' AND ?4<>'') OR
	    (tmdb_id IS NULL AND ?5 IS NOT NULL) OR (poster IS NULL AND ?6 IS NOT NULL) OR (backdrop IS NULL AND ?7 IS NOT NULL))`,
		orig, year, overview, NormalizeRating(rating), nullInt(tmdb), nullStr(poster), nullStr(backdrop), id)
	return err
}

// mergeMeta adds the keys of ids (and "genres", when given) that meta_json
// does not have yet; stored values (TMDB's, an earlier scan's) win.
func mergeMeta(ctx context.Context, x execer, id int64, ids map[string]string, genres ...string) error {
	if len(ids) == 0 && len(genres) == 0 {
		return nil
	}
	var raw string
	if err := x.QueryRowContext(ctx, `SELECT meta_json FROM items WHERE id=?`, id).Scan(&raw); err != nil {
		return err
	}
	m := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &m)
	changed := false
	for k, v := range ids {
		if _, ok := m[k]; !ok && v != "" {
			m[k] = v
			changed = true
		}
	}
	if _, ok := m["genres"]; !ok && len(genres) > 0 {
		m["genres"] = genres
		changed = true
	}
	if !changed {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = x.ExecContext(ctx, `UPDATE items SET meta_json=? WHERE id=?`, string(b), id)
	return err
}

// findMovie locates an existing movie for m: by any known file path, then by
// tmdb id, then by a same-titled movie living in the same folder (a
// quality upgrade by a media manager: the file was renamed, the item must
// stay).
func findMovie(ctx context.Context, tx *sql.Tx, m MovieInput, files []FileInput) (int64, error) {
	var id int64
	for _, f := range files {
		err := tx.QueryRowContext(ctx, `SELECT item_id FROM files WHERE path=?`, f.Path).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	if m.TmdbID != 0 {
		err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE kind='movie' AND tmdb_id=? ORDER BY id LIMIT 1`, m.TmdbID).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	var norms []any
	seen := map[string]bool{}
	for _, t := range append([]string{m.Title, m.OriginalTitle}, m.AltTitles...) {
		if n := release.Normalize(t); n != "" && !seen[n] {
			seen[n] = true
			norms = append(norms, n)
		}
	}
	if len(norms) == 0 {
		return 0, sql.ErrNoRows
	}
	dir := filepath.Dir(files[0].Path) + string(filepath.Separator)
	// Only the main title and original title identify a movie; aliases are too loose.
	main := []any{release.Normalize(m.Title)}
	if o := release.Normalize(m.OriginalTitle); o != "" {
		main = append(main, o)
	}
	args := []any{utf8.RuneCountInString(dir), dir, utf8.RuneCountInString(dir), m.Year, m.Year}
	args = append(args, main...)
	err := tx.QueryRowContext(ctx, `SELECT i.id FROM items i JOIN files f ON f.item_id=i.id JOIN item_titles t ON t.item_id=i.id
	  WHERE i.kind='movie' AND substr(f.path,1,?)=? AND instr(substr(f.path,?+1), '/')=0
	    AND (i.year=0 OR ?=0 OR i.year=?) AND t.norm IN (?`+strings.Repeat(",?", len(main)-1)+`) ORDER BY i.id LIMIT 1`, args...).Scan(&id)
	return id, err
}

// UpsertMovie creates or refreshes a movie. Files (or File) are its parts;
// the item is found by file path, tmdb id, or same title in the same folder.
// Stored non-empty metadata is never overwritten; use SetMetadata for that.
func (r *Repo) UpsertMovie(ctx context.Context, m MovieInput) (int64, error) {
	files := m.Files
	if len(files) == 0 && m.File.Path != "" {
		files = []FileInput{m.File}
	}
	if len(files) == 0 || files[0].Path == "" {
		return 0, errors.New("library: movie needs a file path")
	}
	var out int64
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		id, err := findMovie(ctx, tx, m, files)
		if errors.Is(err, sql.ErrNoRows) {
			title := strings.TrimSpace(m.Title)
			if title == "" {
				title = strings.TrimSuffix(filepath.Base(files[0].Path), filepath.Ext(files[0].Path))
			}
			ts := now()
			res, err := tx.ExecContext(ctx, `INSERT INTO items(kind,title,original_title,sort_title,year,overview,rating,tmdb_id,poster,backdrop,added_at,updated_at)
			  VALUES('movie',?,?,?,?,?,?,?,?,?,?,?)`, title, m.OriginalTitle, sortTitle(title), m.Year, m.Overview,
				NormalizeRating(m.Rating), nullInt(m.TmdbID), nullStr(m.Poster), nullStr(m.Backdrop), ts, ts)
			if err != nil {
				return err
			}
			if id, err = res.LastInsertId(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if err := fillEmpty(ctx, tx, id, m.OriginalTitle, m.Year, m.Overview, m.Rating, m.TmdbID, m.Poster, m.Backdrop); err != nil {
			return err
		}
		for _, f := range files {
			if err := upsertFile(ctx, tx, id, f); err != nil {
				return err
			}
		}
		if err := found(ctx, tx, id); err != nil {
			return err
		}
		if err := mergeMeta(ctx, tx, id, m.ExtIDs, m.Genres...); err != nil {
			return err
		}
		out = id
		return addTitles(ctx, tx, id, append([]string{m.Title, m.OriginalTitle}, m.AltTitles...)...)
	})
	return out, err
}

func findSeries(ctx context.Context, tx *sql.Tx, e EpisodeInput) (int64, error) {
	var id int64
	if e.SeriesTmdbID != 0 {
		err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE kind='series' AND tmdb_id=? ORDER BY id LIMIT 1`, e.SeriesTmdbID).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	for _, k := range []string{"tvdb_id", "imdb_id"} { // fixed keys only: safe to inline in the JSON path
		if v := e.SeriesExtIDs[k]; v != "" {
			err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE kind='series' AND json_extract(meta_json,'$.`+k+`')=? ORDER BY id LIMIT 1`, v).Scan(&id)
			if err == nil {
				return id, nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return 0, err
			}
		}
	}
	var norms []any
	seen := map[string]bool{}
	for _, t := range append([]string{e.SeriesTitle, e.SeriesOriginal}, e.SeriesAlt...) {
		if n := release.Normalize(t); n != "" && !seen[n] {
			seen[n] = true
			norms = append(norms, n)
		}
	}
	if len(norms) == 0 {
		return 0, sql.ErrNoRows
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT i.id, i.year, COALESCE(i.tmdb_id,0) FROM item_titles t JOIN items i ON i.id=t.item_id
	  WHERE i.kind='series' AND t.norm IN (?`+strings.Repeat(",?", len(norms)-1)+`) ORDER BY i.id`, norms...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, tmdb int64
		var year int
		if err := rows.Scan(&id, &year, &tmdb); err != nil {
			return 0, err
		}
		if e.SeriesYear != 0 && year != 0 && year != e.SeriesYear {
			continue // Doctor Who (1963) vs (2005)
		}
		if e.SeriesTmdbID != 0 && tmdb != 0 && tmdb != e.SeriesTmdbID {
			continue
		}
		return id, nil
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return 0, sql.ErrNoRows
}

func seasonTitle(n int) string {
	if n == 0 {
		return i18n.TL(i18n.RU, "Спецвыпуски")
	}
	return i18n.TL(i18n.RU, "Сезон %d", n)
}

func episodeTitle(n int) string { return i18n.TL(i18n.RU, "Серия %d", n) }

// UpsertEpisode creates or refreshes an episode, creating its series and
// season on demand. Several files may belong to the same episode.
func (r *Repo) UpsertEpisode(ctx context.Context, e EpisodeInput) (int64, error) {
	if e.File.Path == "" {
		return 0, errors.New("library: episode needs a file path")
	}
	if strings.TrimSpace(e.SeriesTitle) == "" {
		return 0, errors.New("library: episode needs a series title")
	}
	var epID int64
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		ts := now()
		seriesID, err := findSeries(ctx, tx, e)
		if errors.Is(err, sql.ErrNoRows) {
			title := strings.TrimSpace(e.SeriesTitle)
			res, err := tx.ExecContext(ctx, `INSERT INTO items(kind,title,original_title,sort_title,year,overview,rating,tmdb_id,poster,backdrop,added_at,updated_at)
			  VALUES('series',?,?,?,?,?,?,?,?,?,?,?)`, title, e.SeriesOriginal, sortTitle(title), e.SeriesYear, e.SeriesOverview, NormalizeRating(e.SeriesRating),
				nullInt(e.SeriesTmdbID), nullStr(e.SeriesPoster), nullStr(e.SeriesBackdrop), ts, ts)
			if err != nil {
				return err
			}
			if seriesID, err = res.LastInsertId(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if err := fillEmpty(ctx, tx, seriesID, e.SeriesOriginal, e.SeriesYear, e.SeriesOverview, e.SeriesRating, e.SeriesTmdbID, e.SeriesPoster, e.SeriesBackdrop); err != nil {
			return err
		}
		if err := addTitles(ctx, tx, seriesID, append([]string{e.SeriesTitle, e.SeriesOriginal}, e.SeriesAlt...)...); err != nil {
			return err
		}
		if err := mergeMeta(ctx, tx, seriesID, e.SeriesExtIDs, e.SeriesGenres...); err != nil {
			return err
		}

		var seasonID int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM items WHERE kind='season' AND parent_id=? AND season_no=?`, seriesID, e.Season).Scan(&seasonID)
		if errors.Is(err, sql.ErrNoRows) {
			st := seasonTitle(e.Season)
			res, err := tx.ExecContext(ctx, `INSERT INTO items(kind,parent_id,title,sort_title,season_no,added_at,updated_at) VALUES('season',?,?,?,?,?,?)`,
				seriesID, st, sortTitle(st), e.Season, ts, ts)
			if err != nil {
				return err
			}
			if seasonID, err = res.LastInsertId(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		err = tx.QueryRowContext(ctx, `SELECT id FROM items WHERE kind='episode' AND parent_id=? AND episode_no=?`, seasonID, e.Episode).Scan(&epID)
		if errors.Is(err, sql.ErrNoRows) {
			title := strings.TrimSpace(e.Title)
			if title == "" {
				title = episodeTitle(e.Episode)
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO items(kind,parent_id,title,sort_title,season_no,episode_no,overview,added_at,updated_at) VALUES('episode',?,?,?,?,?,?,?,?)`,
				seasonID, title, sortTitle(title), e.Season, e.Episode, e.Overview, ts, ts)
			if err != nil {
				return err
			}
			if epID, err = res.LastInsertId(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if t := strings.TrimSpace(e.Title); t != "" {
			// replace only the auto-generated placeholder title
			if _, err := tx.ExecContext(ctx, `UPDATE items SET title=?, sort_title=? WHERE id=? AND title=?`, t, sortTitle(t), epID, episodeTitle(e.Episode)); err != nil {
				return err
			}
		}
		if e.Overview != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET overview=? WHERE id=? AND overview=''`, e.Overview, epID); err != nil {
				return err
			}
		}
		if err := upsertFile(ctx, tx, epID, e.File); err != nil {
			return err
		}
		return found(ctx, tx, epID, seasonID, seriesID)
	})
	return epID, err
}

// found clears the missing mark of items whose file is on disk again (a
// restore from the trash, an undo, a disk mounted again): they come back
// with their id, so progress, watchlist and metadata are kept.
func found(ctx context.Context, x execer, ids ...int64) error {
	for _, id := range ids {
		if _, err := x.ExecContext(ctx, `UPDATE items SET missing_since=NULL WHERE id=? AND missing_since IS NOT NULL`, id); err != nil {
			return err
		}
	}
	return nil
}

// Item returns one item.
func (r *Repo) Item(ctx context.Context, id int64) (Item, error) {
	it, err := scanItem(r.db.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.id=? AND i.missing_since IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

func (r *Repo) queryItems(ctx context.Context, q string, args ...any) ([]Item, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Children returns seasons of a series or episodes of a season, in order.
func (r *Repo) Children(ctx context.Context, id int64) ([]Item, error) {
	return r.queryItems(ctx, `SELECT `+itemCols+` FROM items i WHERE i.parent_id=? AND i.missing_since IS NULL ORDER BY COALESCE(i.season_no,0), COALESCE(i.episode_no,0), i.sort_title, i.id`, id)
}

// allowedRatings returns the SQL condition (on items aliased "i") for a
// maximum rating: a rating within the limit; music and books without a
// rating (they carry none: IsUnratedKind); a comic volume only by its
// series (unrated or within the limit; a series rating is the maximum of
// its volumes), never by its own rating.
func allowedRatings(max string) (string, []any) {
	rank, ok := RatingRank(max)
	if !ok {
		return "0", nil // unknown limit: show nothing
	}
	allowed := make([]any, 0, rank+1)
	ph := make([]string, 0, rank+1)
	for i := 0; i <= rank; i++ {
		allowed = append(allowed, ruRatings[i])
		ph = append(ph, "?")
	}
	in := strings.Join(ph, ",")
	args := append(append([]any{}, allowed...), allowed...)
	// a comic volume never passes on its own rating: only its series counts
	return `((i.kind<>'comic_volume' AND i.rating IN (` + in + `)) OR (i.rating='' AND i.kind IN ('artist','album','track','book','audiobook','comic'))` +
		` OR (i.kind='comic_volume' AND COALESCE((SELECT p.rating FROM items p WHERE p.id=i.parent_id),'') IN ('',` + in + `)))`, args
}

// List returns a page of items and the total count for the filter.
func (r *Repo) List(ctx context.Context, q ListQuery) ([]Item, int, error) {
	where := []string{"i.missing_since IS NULL"}
	var args []any
	if q.Kind != "" {
		where = append(where, "i.kind=?")
		args = append(args, q.Kind)
	} else {
		where = append(where, "i.kind IN ('movie','series')")
	}
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
		order = "i.sort_title ASC, i.id ASC"
	case "year":
		order = "i.year DESC, i.sort_title ASC, i.id ASC"
	}
	limit := q.Limit
	if limit <= 0 {
		limit = -1
	}
	args = append(args, limit, max(q.Offset, 0))
	items, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items i`+w+` ORDER BY `+order+` LIMIT ? OFFSET ?`, args...)
	return items, total, err
}

// Search finds movies and series (only: no seasons, episodes, music or
// books) by title, original title, any alias (item_titles: ё=е,
// case-insensitive, substring) or full text (FTS5, prefix). SearchKinds
// searches other kinds.
func (r *Repo) Search(ctx context.Context, text string, limit int) ([]Item, error) {
	return r.SearchRated(ctx, text, limit, "")
}

// SearchRated is Search restricted to items within maxRating ("" = no limit).
func (r *Repo) SearchRated(ctx context.Context, text string, limit int, maxRating string) ([]Item, error) {
	return r.SearchKinds(ctx, text, []string{KindMovie, KindSeries}, limit, maxRating)
}

// SearchKinds is Search over the given item kinds (e.g. album, artist),
// restricted to maxRating ("" = no limit). No kinds finds nothing.
func (r *Repo) SearchKinds(ctx context.Context, text string, kinds []string, limit int, maxRating string) ([]Item, error) {
	rate, rateArgs := "1", []any(nil)
	if maxRating != "" {
		rate, rateArgs = allowedRatings(maxRating)
	}
	return r.searchWith(ctx, text, kinds, limit, "", rate, rateArgs)
}

// searchWith is SearchKinds with a custom rating condition rate (on "i",
// after the extra joins).
func (r *Repo) searchWith(ctx context.Context, text string, kinds []string, limit int, joins, rate string, rateArgs []any) ([]Item, error) {
	norm := release.Normalize(text)
	if norm == "" || len(kinds) == 0 {
		return nil, nil
	}
	kindIn := "i.kind IN (?" + strings.Repeat(",?", len(kinds)-1) + ")"
	kindArgs := make([]any, len(kinds))
	for i, k := range kinds {
		kindArgs[i] = k
	}
	if limit <= 0 {
		limit = 50
	}
	args := append([]any{}, kindArgs...)
	args = append(args, "%"+norm+"%")
	args = append(args, rateArgs...)
	args = append(args, norm, norm+"%", limit)
	out, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items i JOIN item_titles t ON t.item_id=i.id`+joins+`
	  WHERE `+kindIn+` AND i.missing_since IS NULL AND t.norm LIKE ? AND `+rate+`
	  GROUP BY i.id ORDER BY MAX(t.norm=?) DESC, MAX(t.norm LIKE ?) DESC, i.sort_title, i.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	if len(out) >= limit {
		return out, nil
	}
	seen := map[int64]bool{}
	for _, it := range out {
		seen[it.ID] = true
	}
	var toks []string
	for _, f := range strings.FieldsFunc(norm, func(c rune) bool { return !unicode.IsLetter(c) && !unicode.IsDigit(c) }) {
		toks = append(toks, `"`+f+`"*`)
	}
	fargs := []any{strings.Join(toks, " ")}
	fargs = append(fargs, kindArgs...)
	fargs = append(fargs, rateArgs...)
	fargs = append(fargs, limit)
	ftsRows, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items_fts f JOIN items i ON i.id=f.rowid`+joins+`
	  WHERE items_fts MATCH ? AND `+kindIn+` AND i.missing_since IS NULL AND `+rate+` ORDER BY f.rank LIMIT ?`, fargs...)
	if err != nil {
		return nil, err
	}
	for _, it := range ftsRows {
		if !seen[it.ID] && len(out) < limit {
			out = append(out, it)
		}
	}
	return out, nil
}

// FilesOf lists the files of an item.
func (r *Repo) FilesOf(ctx context.Context, itemID int64) ([]File, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileCols+` FROM files WHERE item_id=? AND missing_since IS NULL ORDER BY path`, itemID)
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

// FileByID returns one file.
func (r *Repo) FileByID(ctx context.Context, id int64) (File, error) {
	f, err := scanFile(r.db.QueryRowContext(ctx, `SELECT `+fileCols+` FROM files WHERE id=? AND missing_since IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// MissingGrace is how long items whose files disappeared stay (hidden)
// before they are deleted with their progress and watchlist entries.
var MissingGrace = 30 * 24 * time.Hour

// RemoveMissingFiles marks file rows under root that are not in seen as
// missing, hides items left with nothing on disk (leaves by their files,
// tracks by their tracks.file_id, parents by their children), and purges
// what has been missing for MissingGrace.
func (r *Repo) RemoveMissingFiles(ctx context.Context, root string, seen map[string]bool) error {
	_, err := r.removeMissing(ctx, root, seen)
	return err
}

// leafKinds own their files rows; parentKinds exist through their children
// (in this order: a season goes before its series, an album before its
// artist). Constants only: safe to inline in SQL.
const leafKinds = `('movie','episode','book','audiobook','comic_volume')`

var parentKinds = []string{KindSeason, KindSeries, KindAlbum, KindArtist, KindComic}

func (r *Repo) removeMissing(ctx context.Context, root string, seen map[string]bool) (int, error) {
	prefix := filepath.Clean(root)
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, path FROM files WHERE missing_since IS NULL`)
	if err != nil {
		return 0, err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return 0, err
		}
		if strings.HasPrefix(p, prefix) && !seen[p] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	ts := now()
	cutoff := time.Now().UTC().Add(-MissingGrace).Format(time.RFC3339)
	err = r.inTx(ctx, func(tx *sql.Tx) error {
		for _, id := range gone {
			if _, err := tx.ExecContext(ctx, `UPDATE files SET missing_since=? WHERE id=?`, ts, id); err != nil {
				return err
			}
		}
		if err := hideEmpty(ctx, tx, ts); err != nil {
			return err
		}
		// purge what has been missing long enough (progress and watchlist go with it)
		if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE missing_since IS NOT NULL AND missing_since < ?`, cutoff); err != nil {
			return err
		}
		// (the files cascade took their tracks and chapters rows)
		purge := []string{
			`DELETE FROM items WHERE kind='track' AND NOT EXISTS (SELECT 1 FROM tracks t WHERE t.item_id=items.id)`,
			`DELETE FROM items WHERE kind IN ` + leafKinds + ` AND NOT EXISTS (SELECT 1 FROM files WHERE item_id=items.id)`,
		}
		for _, k := range parentKinds {
			purge = append(purge, `DELETE FROM items WHERE kind='`+k+`' AND NOT EXISTS (SELECT 1 FROM items c WHERE c.parent_id=items.id)`)
		}
		for _, q := range purge {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	return len(gone), err
}

// hideEmpty hides what has nothing on disk any more: leaves by their own
// files, tracks by the file they play from (their own or the album's CUE
// image), then parents by their children.
func hideEmpty(ctx context.Context, tx *sql.Tx, ts string) error {
	hide := []string{
		`UPDATE items SET missing_since=?1 WHERE missing_since IS NULL AND kind IN ` + leafKinds + `
		   AND NOT EXISTS (SELECT 1 FROM files WHERE item_id=items.id AND missing_since IS NULL)`,
		`UPDATE items SET missing_since=?1 WHERE missing_since IS NULL AND kind='track'
		   AND NOT EXISTS (SELECT 1 FROM tracks t JOIN files f ON f.id=t.file_id WHERE t.item_id=items.id AND f.missing_since IS NULL)`,
	}
	for _, k := range parentKinds {
		hide = append(hide, `UPDATE items SET missing_since=?1 WHERE missing_since IS NULL AND kind='`+k+`'
		   AND NOT EXISTS (SELECT 1 FROM items c WHERE c.parent_id=items.id AND c.missing_since IS NULL)`)
	}
	for _, q := range hide {
		if _, err := tx.ExecContext(ctx, q, ts); err != nil {
			return err
		}
	}
	return nil
}

// SeriesFolder returns the folder directly under root that holds the
// episodes of the series with this TMDB id (e.g. "Planet Earth II"), or
// ErrNotFound.
func (r *Repo) SeriesFolder(ctx context.Context, root string, tmdbID int64) (string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT f.path FROM files f JOIN items e ON e.id=f.item_id
	  JOIN items s ON s.id=e.parent_id JOIN items se ON se.id=s.parent_id
	  WHERE se.kind='series' AND se.tmdb_id=? AND se.missing_since IS NULL AND f.missing_since IS NULL ORDER BY f.id LIMIT 50`, tmdbID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	root = filepath.Clean(root)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return "", err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if i := strings.IndexRune(rel, filepath.Separator); i > 0 {
			return rel[:i], nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", ErrNotFound
}

// SetMetadata overwrites the non-empty fields of m on item id (TMDB enrichment).
func (r *Repo) SetMetadata(ctx context.Context, id int64, m Metadata) error {
	return r.inTx(ctx, func(tx *sql.Tx) error { return setMetadata(ctx, tx, id, m) })
}

func setMetadata(ctx context.Context, tx *sql.Tx, id int64, m Metadata) error {
	res, err := tx.ExecContext(ctx, `UPDATE items SET
	  title          = CASE WHEN ?1<>'' THEN ?1 ELSE title END,
	  sort_title     = CASE WHEN ?1<>'' THEN ?9 ELSE sort_title END,
	  original_title = CASE WHEN ?2<>'' THEN ?2 ELSE original_title END,
	  year           = CASE WHEN ?3<>0 THEN ?3 ELSE year END,
	  overview       = CASE WHEN ?4<>'' THEN ?4 ELSE overview END,
	  rating         = CASE WHEN ?5<>'' THEN ?5 ELSE rating END,
	  tmdb_id        = COALESCE(?6, tmdb_id),
	  poster         = COALESCE(?7, poster),
	  backdrop       = COALESCE(?8, backdrop),
	  meta_json      = CASE WHEN ?10<>'' THEN ?10 ELSE meta_json END,
	  updated_at     = ?11
	  WHERE id=?12`, m.Title, m.OriginalTitle, m.Year, m.Overview, NormalizeRating(m.Rating), nullInt(m.TmdbID),
		nullStr(m.Poster), nullStr(m.Backdrop), sortTitle(m.Title), m.MetaJSON, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return addTitles(ctx, tx, id, m.Title, m.OriginalTitle)
}

// AddTitles registers alias titles (e.g. English names) for search.
func (r *Repo) AddTitles(ctx context.Context, id int64, titles []string) error {
	return r.inTx(ctx, func(tx *sql.Tx) error { return addTitles(ctx, tx, id, titles...) })
}

// RecentSeries returns series ordered by their newest episode, newest first,
// restricted to maxRating ("" = no limit).
func (r *Repo) RecentSeries(ctx context.Context, limit int, maxRating string) ([]Item, error) {
	return r.RecentSeriesOf(ctx, limit, maxRating, 0)
}

// RecentSeriesOf is RecentSeries of the series owner brought (item_owners;
// 0 = every series).
func (r *Repo) RecentSeriesOf(ctx context.Context, limit int, maxRating string, owner int64) ([]Item, error) {
	rate, args := "1", []any(nil)
	if maxRating != "" {
		rate, args = allowedRatings(maxRating)
	}
	if owner != 0 {
		c, a := ownedBy("i.id", owner)
		rate += " AND " + c
		args = append(args, a...)
	}
	if limit <= 0 {
		limit = -1
	}
	args = append(args, limit)
	return r.queryItems(ctx, `SELECT `+itemCols+` FROM items i JOIN items s ON s.parent_id=i.id JOIN items e ON e.parent_id=s.id
	  WHERE i.kind='series' AND i.missing_since IS NULL AND s.missing_since IS NULL AND e.missing_since IS NULL AND `+rate+` GROUP BY i.id ORDER BY MAX(e.added_at) DESC, i.id DESC LIMIT ?`, args...)
}

// SetProbe caches the ffprobe result (JSON) and duration of a file, and
// copies the audio codec onto the file's tracks rows.
func (r *Repo) SetProbe(ctx context.Context, fileID int64, probeJSON string, durationMs int64) error {
	res, err := r.db.ExecContext(ctx, `UPDATE files SET probe_json=?, duration_ms=COALESCE(?, duration_ms) WHERE id=?`,
		probeJSON, nullInt(durationMs), fileID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	// a music file's tracks take the probed codec (ALAC in .m4a is lossless)
	return applyProbeCodec(ctx, r.db, fileID, probeJSON)
}

// probeExtSQL matches the paths ffprobe can inspect (videoExt ∪ audioExt;
// LIKE is ASCII case-insensitive): books, archives and image folders are
// never probed.
var probeExtSQL = func() string {
	var exts []string
	for _, m := range []map[string]bool{videoExt, audioExt} {
		for e := range m {
			exts = append(exts, e)
		}
	}
	sort.Strings(exts)
	conds := make([]string, len(exts))
	for i, e := range exts {
		conds[i] = "path LIKE '%" + e + "'" // fixed extensions: no quotes or wildcards
	}
	return "(" + strings.Join(conds, " OR ") + ")"
}()

// FilesWithoutProbe lists up to limit video and audio files whose probe
// result is not cached yet, those of movies and episodes first (a new
// movie needs its probe for subtitles and playback; a big music import
// must not hold it back).
func (r *Repo) FilesWithoutProbe(ctx context.Context, limit int) ([]File, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileCols+` FROM files WHERE (probe_json IS NULL OR probe_json='') AND missing_since IS NULL AND `+probeExtSQL+`
	  ORDER BY item_id IN (SELECT id FROM items WHERE kind IN ('movie','episode')) DESC, id LIMIT ?`, limit)
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

// VideoFiles pages through the probed files of movies and episodes, the
// newest first: ids below beforeID (0 = from the newest), up to limit.
func (r *Repo) VideoFiles(ctx context.Context, beforeID int64, limit int) ([]File, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileCols+` FROM files
		WHERE probe_json IS NOT NULL AND probe_json<>'' AND missing_since IS NULL
		  AND item_id IN (SELECT id FROM items WHERE kind IN ('movie','episode'))
		  AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, beforeID, beforeID, limit)
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

// FilesUnder lists files whose path is below dir (any depth).
func (r *Repo) FilesUnder(ctx context.Context, dir string) ([]File, error) {
	prefix := filepath.Clean(dir)
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	// [prefix, prefix with the separator bumped) is exactly "starts with prefix"
	// under SQLite's bytewise BINARY collation, and uses the path index.
	upper := prefix[:len(prefix)-1] + string(rune(filepath.Separator)+1)
	rows, err := r.db.QueryContext(ctx, `SELECT `+fileCols+` FROM files WHERE path >= ? AND path < ? AND missing_since IS NULL ORDER BY path`, prefix, upper)
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

// FileByPath returns the file stored under an absolute path.
func (r *Repo) FileByPath(ctx context.Context, path string) (File, error) {
	f, err := scanFile(r.db.QueryRowContext(ctx, `SELECT `+fileCols+` FROM files WHERE path=? AND missing_since IS NULL`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// ItemByPath returns the file stored under path and the item it belongs to.
func (r *Repo) ItemByPath(ctx context.Context, path string) (Item, File, error) {
	f, err := r.FileByPath(ctx, path)
	if err != nil {
		return Item{}, f, err
	}
	it, err := r.Item(ctx, f.ItemID)
	return it, f, err
}

// ItemIDsByTMDB maps TMDB ids of one kind (movie or series) to the ids
// of their top-level items, as ItemByTMDB would, in one query; ids not in
// the library are left out.
func (r *Repo) ItemIDsByTMDB(ctx context.Context, kind string, tmdbIDs []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(tmdbIDs) == 0 {
		return out, nil
	}
	args := []any{kind}
	for _, id := range tmdbIDs {
		args = append(args, id)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT tmdb_id, MIN(id) FROM items WHERE kind=? AND tmdb_id IN (?`+strings.Repeat(",?", len(tmdbIDs)-1)+`)
	  AND parent_id IS NULL AND missing_since IS NULL GROUP BY tmdb_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tmdb, id int64
		if err := rows.Scan(&tmdb, &id); err != nil {
			return nil, err
		}
		out[tmdb] = id
	}
	return out, rows.Err()
}

// EditionsByTMDB maps TMDB ids of one kind (movie or series) to all of
// their top-level items (editions of one film are items of their own),
// lowest id first, in one query; ids not in the library are left out.
func (r *Repo) EditionsByTMDB(ctx context.Context, kind string, tmdbIDs []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	if len(tmdbIDs) == 0 {
		return out, nil
	}
	args := []any{kind}
	for _, id := range tmdbIDs {
		args = append(args, id)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT tmdb_id, id FROM items WHERE kind=? AND tmdb_id IN (?`+strings.Repeat(",?", len(tmdbIDs)-1)+`)
	  AND parent_id IS NULL AND missing_since IS NULL ORDER BY tmdb_id, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tmdb, id int64
		if err := rows.Scan(&tmdb, &id); err != nil {
			return nil, err
		}
		out[tmdb] = append(out[tmdb], id)
	}
	return out, rows.Err()
}

// ItemsByIMDb returns the movies and series with this IMDb id (meta_json
// imdb_id, from TMDB or a .nfo).
func (r *Repo) ItemsByIMDb(ctx context.Context, imdbID string) ([]Item, error) {
	return r.queryItems(ctx, `SELECT `+itemCols+` FROM items i WHERE i.kind IN ('movie','series') AND i.parent_id IS NULL
	  AND i.missing_since IS NULL AND json_valid(i.meta_json) AND json_extract(i.meta_json,'$.imdb_id')=? ORDER BY i.id`, imdbID)
}

// ItemByTMDB returns the top-level item (movie or series) with this TMDB id.
func (r *Repo) ItemByTMDB(ctx context.Context, kind string, tmdbID int64) (Item, error) {
	items, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items i WHERE i.kind=? AND i.tmdb_id=? AND i.parent_id IS NULL AND i.missing_since IS NULL ORDER BY i.id LIMIT 1`, kind, tmdbID)
	if err != nil {
		return Item{}, err
	}
	if len(items) == 0 {
		return Item{}, ErrNotFound
	}
	return items[0], nil
}

// FindTitle finds the library item of a title somebody named: a movie or
// series by its TMDB id, an album ("music") by artist and title, a book or
// audiobook by author and title (creator is the artist or the author). ok
// is false when the library has none, and for other kinds (a manga has no
// lookup); err is a lookup that failed.
func (r *Repo) FindTitle(ctx context.Context, kind string, tmdbID int64, title, creator string) (Item, bool, error) {
	switch kind {
	case KindMovie, KindSeries:
		it, err := r.ItemByTMDB(ctx, kind, tmdbID)
		if errors.Is(err, ErrNotFound) {
			return Item{}, false, nil
		}
		return it, err == nil, err
	case KindMusic:
		return r.FindAlbum(ctx, creator, title)
	case KindBook, KindAudiobook:
		return r.FindBook(ctx, kind, creator, title)
	}
	return Item{}, false, nil
}

// FindTitleID is FindTitle's item id, false for a failed lookup too (only
// a hint). It is the notify.LibraryFinder of the kids' requests.
func (r *Repo) FindTitleID(ctx context.Context, kind string, tmdbID int, title, creator string) (int64, bool) {
	it, ok, err := r.FindTitle(ctx, kind, int64(tmdbID), title, creator)
	if err != nil || !ok {
		return 0, false
	}
	return it.ID, true
}
