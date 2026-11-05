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
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/natsort"
	"github.com/antresol-app/antresol/internal/release"
)

// ComicInput is one manga/comic volume as the comics scanner sees it: an
// archive or a folder of images (File.Path is then the folder, Size the
// sum of its pages, Mtime the newest page). SeriesDir is the series folder
// ("" for a volume right in the comics root: the series is then keyed by
// the root and the normalised Series). Series is the series name the
// folder or file name gives; Title the volume's display name (see
// comicVolumeTitle). Direction is "rtl" or "ltr"; Rating is canonical RU.
// Cover is "file:<path>" or a name in ArtDir.
type ComicInput struct {
	File                                            FileInput
	SeriesDir, Series                               string
	Volume, Chapter                                 float64
	Title, Writer, Summary, Lang, Direction, Rating string
	Year, Pages                                     int
	Cover                                           string

	infoSeries string // ComicInfo.Series: names the series when this is its first volume
}

// comicMatchKey is items.match_key of a comic series (see model.go).
func comicMatchKey(seriesDir, volumePath, series string) string {
	if seriesDir != "" {
		return filepath.Clean(seriesDir)
	}
	n := release.Normalize(series)
	if n == "" {
		n = strings.ToLower(strings.TrimSpace(series))
	}
	return filepath.Dir(volumePath) + "|" + n
}

// UpsertComicVolume creates or refreshes a volume and, on demand, its
// series, in one transaction. The series is found by match_key, the volume
// by its file. The scanner owns the volume's title and meta_json keys
// (volume, chapter, pages, direction, lang, writer, rating); year and overview
// are only filled when empty, the poster is replaced only if the scanner
// set it. A new series takes this volume's name, cover, direction and
// writer. A volume's own age rating is kept in meta_json.rating, not in
// items.rating: a volume is visible exactly when its series is (the rating
// filter and the API gate follow the series), and the series rating is the
// highest of its volumes — raised here at once when this one is higher,
// settled by finishComicSeries after a scan. It returns the volume id.
func (r *Repo) UpsertComicVolume(ctx context.Context, v ComicInput) (int64, error) {
	id, _, err := r.upsertComicVolume(ctx, v)
	return id, err
}

func (r *Repo) upsertComicVolume(ctx context.Context, v ComicInput) (volID, seriesID int64, err error) {
	if v.File.Path == "" {
		return 0, 0, errors.New("library: comic volume needs a file")
	}
	stem := comicStem(v.File.Path)
	series := strings.TrimSpace(v.Series)
	if series == "" {
		if v.SeriesDir != "" {
			series = filepath.Base(v.SeriesDir)
		} else if series, _, _ = parseComicFile(stem); series == "" {
			series = stem
		}
	}
	title := strings.TrimSpace(v.Title)
	if title == "" {
		title = comicVolumeTitle(v.Volume, v.Chapter, stem)
	}
	rating := NormalizeRating(v.Rating)
	key := comicMatchKey(v.SeriesDir, v.File.Path, series)
	err = r.inTx(ctx, func(tx *sql.Tx) error {
		ts := now()
		var sRating string
		err := tx.QueryRowContext(ctx, `SELECT id, rating FROM items WHERE kind='comic' AND match_key=? ORDER BY id LIMIT 1`, key).
			Scan(&seriesID, &sRating)
		newSeries := errors.Is(err, sql.ErrNoRows)
		if newSeries {
			name := series
			if s := strings.TrimSpace(v.infoSeries); s != "" {
				name = s
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO items(kind,title,sort_title,year,overview,rating,poster,match_key,added_at,updated_at)
			  VALUES('comic',?,?,?,?,?,?,?,?,?)`, name, sortTitle(name), v.Year, v.Summary, rating, nullStr(v.Cover), key, ts, ts)
			if err != nil {
				return err
			}
			if seriesID, err = res.LastInsertId(); err != nil {
				return err
			}
			meta := map[string]any{"direction": nilIfEmpty(v.Direction), "writer": nilIfEmpty(v.Writer), "scan_cover": nilIfEmpty(v.Cover)}
			if err := setAlbumMeta(ctx, tx, seriesID, meta); err != nil {
				return err
			}
			if err := addTitles(ctx, tx, seriesID, name, series, v.Writer); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if higherRating(rating, sRating) {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET rating=? WHERE id=?`, rating, seriesID); err != nil {
				return err
			}
		}

		var scanCover sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT i.id, json_extract(i.meta_json,'$.scan_cover') FROM files f JOIN items i ON i.id=f.item_id
		  WHERE f.path=? AND i.kind='comic_volume'`, v.File.Path).Scan(&volID, &scanCover)
		if errors.Is(err, sql.ErrNoRows) {
			res, err := tx.ExecContext(ctx, `INSERT INTO items(kind,parent_id,title,sort_title,year,overview,poster,added_at,updated_at)
			  VALUES('comic_volume',?,?,?,?,?,?,?,?)`, seriesID, title, sortTitle(title), v.Year, v.Summary, nullStr(v.Cover), ts, ts)
			if err != nil {
				return err
			}
			if volID, err = res.LastInsertId(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET parent_id=?1, title=?2, sort_title=?3, updated_at=?4
			  WHERE id=?5 AND (parent_id IS NOT ?1 OR title IS NOT ?2 OR sort_title IS NOT ?3)`,
				seriesID, title, sortTitle(title), ts, volID); err != nil {
				return err
			}
			if err := fillEmpty(ctx, tx, volID, "", v.Year, v.Summary, "", 0, "", ""); err != nil {
				return err
			}
			if v.Cover != "" {
				if _, err := tx.ExecContext(ctx, `UPDATE items SET poster=?1 WHERE id=?2 AND poster IS NOT ?1 AND (poster IS NULL OR poster=?3)`,
					v.Cover, volID, scanCover.String); err != nil {
					return err
				}
			} else if scanCover.String != "" {
				if _, err := tx.ExecContext(ctx, `UPDATE items SET poster=NULL WHERE id=? AND poster=?`, volID, scanCover.String); err != nil {
					return err
				}
			}
		}
		meta := map[string]any{
			"volume": v.Volume, "chapter": v.Chapter, "pages": v.Pages, "direction": nilIfEmpty(v.Direction),
			"lang": nilIfEmpty(v.Lang), "writer": nilIfEmpty(v.Writer), "ci_series": nilIfEmpty(v.infoSeries), "rating": nilIfEmpty(rating),
			"scan_cover": nilIfEmpty(v.Cover),
		}
		if err := setAlbumMeta(ctx, tx, volID, meta); err != nil {
			return err
		}
		if _, err := upsertFileID(ctx, tx, volID, v.File); err != nil {
			return err
		}
		if err := addTitles(ctx, tx, volID, title); err != nil {
			return err
		}
		return found(ctx, tx, volID, seriesID)
	})
	return volID, seriesID, err
}

// nilIfEmpty is a meta_json value: nil (the key is removed) for "".
func nilIfEmpty(s string) any {
	if s = strings.TrimSpace(s); s != "" {
		return s
	}
	return nil
}

// higherRating reports whether rating a is above b on the RU scale (a
// recognised rating is above none).
func higherRating(a, b string) bool {
	ra, oka := RatingRank(a)
	rb, okb := RatingRank(b)
	return oka && (!okb || ra > rb)
}

// comicVol is a visible volume of a series with its file and numbers.
type comicVol struct {
	item            Item
	path            string
	volume, chapter float64
	meta            map[string]any
}

// comicVolumes returns the visible volumes of a series in reading order:
// by volume, then chapter, then the file name (natural order).
func (r *Repo) comicVolumes(ctx context.Context, seriesID int64) ([]comicVol, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+itemCols+`, f.path FROM items i JOIN files f ON f.item_id=i.id AND f.missing_since IS NULL
	  WHERE i.parent_id=? AND i.kind='comic_volume' AND i.missing_since IS NULL ORDER BY i.id, f.id`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []comicVol
	seen := map[int64]bool{}
	for rows.Next() {
		var v comicVol
		var added, updated string
		it := &v.item
		if err := rows.Scan(&it.ID, &it.Kind, &it.ParentID, &it.Title, &it.OriginalTitle, &it.SortTitle, &it.Year, &it.Overview,
			&it.Rating, &it.TmdbID, &it.SeasonNo, &it.EpisodeNo, &it.Poster, &it.Backdrop, &added, &updated, &it.MetaJSON, &v.path); err != nil {
			return nil, err
		}
		if seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		v.meta = map[string]any{}
		_ = json.Unmarshal([]byte(it.MetaJSON), &v.meta)
		v.volume, _ = v.meta["volume"].(float64)
		v.chapter, _ = v.meta["chapter"].(float64)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.volume != b.volume {
			return a.volume < b.volume
		}
		if a.chapter != b.chapter {
			return a.chapter < b.chapter
		}
		return natsort.Less(filepath.Base(a.path), filepath.Base(b.path))
	})
	return out, nil
}

// finishComicSeries settles a series from its volumes after a scan: the
// title is the first volume's ComicInfo.Series, else name; the poster
// (unless an enricher set it), direction and writer come from the first
// volume; the rating is the highest of the volumes; year and overview are
// filled from the first volume when empty. sig is stored as
// meta_json.scan_sig (the scanner's unchanged-series check).
func (r *Repo) finishComicSeries(ctx context.Context, seriesID int64, name, sig string) error {
	vols, err := r.comicVolumes(ctx, seriesID)
	if err != nil || len(vols) == 0 {
		return err
	}
	first := vols[0]
	str := func(k string) string { s, _ := first.meta[k].(string); return s }
	title := strings.TrimSpace(str("ci_series"))
	if title == "" {
		title = name
	}
	rating, best := "", -1
	for _, v := range vols {
		r, _ := v.meta["rating"].(string)
		if rk, ok := RatingRank(r); ok && rk > best {
			rating, best = ruRatings[rk], rk
		}
	}
	return r.inTx(ctx, func(tx *sql.Tx) error {
		var scanCover sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT json_extract(meta_json,'$.scan_cover') FROM items WHERE id=?`, seriesID).Scan(&scanCover); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE items SET title=?1, sort_title=?2, rating=?3, updated_at=?4
		  WHERE id=?5 AND (title IS NOT ?1 OR sort_title IS NOT ?2 OR rating IS NOT ?3)`,
			title, sortTitle(title), rating, now(), seriesID); err != nil {
			return err
		}
		if p := first.item.Poster; p != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET poster=?1 WHERE id=?2 AND poster IS NOT ?1 AND (poster IS NULL OR poster=?3)`,
				p, seriesID, scanCover.String); err != nil {
				return err
			}
		} else if scanCover.String != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET poster=NULL WHERE id=? AND poster=?`, seriesID, scanCover.String); err != nil {
				return err
			}
		}
		if err := fillEmpty(ctx, tx, seriesID, "", first.item.Year, first.item.Overview, "", 0, "", ""); err != nil {
			return err
		}
		meta := map[string]any{"direction": nilIfEmpty(str("direction")), "writer": nilIfEmpty(str("writer")),
			"scan_cover": nilIfEmpty(first.item.Poster), "scan_sig": nilIfEmpty(sig)}
		if err := setAlbumMeta(ctx, tx, seriesID, meta); err != nil {
			return err
		}
		return addTitles(ctx, tx, seriesID, title, name, str("writer"))
	})
}

// storedComicFile is a visible files row of a visible comic volume.
type storedComicFile struct {
	size, mtime, itemID, seriesID int64
}

// comicFilesUnder returns the volume files stored under root by path.
func (r *Repo) comicFilesUnder(ctx context.Context, root string) (map[string]storedComicFile, error) {
	prefix := filepath.Clean(root) + string(filepath.Separator)
	rows, err := r.db.QueryContext(ctx, `SELECT f.path, f.size, f.mtime, i.id, COALESCE(i.parent_id,0) FROM files f JOIN items i ON i.id=f.item_id
	  WHERE i.kind='comic_volume' AND f.missing_since IS NULL AND i.missing_since IS NULL AND substr(f.path,1,?)=?`,
		utf8.RuneCountInString(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]storedComicFile{}
	for rows.Next() {
		var p string
		var f storedComicFile
		if err := rows.Scan(&p, &f.size, &f.mtime, &f.itemID, &f.seriesID); err != nil {
			return nil, err
		}
		out[p] = f
	}
	return out, rows.Err()
}

// storedComicSeries is a visible series: its id and meta_json.scan_sig.
type storedComicSeries struct {
	id  int64
	sig string
}

// comicSeriesUnder returns the visible series of a comics root by match_key.
func (r *Repo) comicSeriesUnder(ctx context.Context, root string) (map[string]storedComicSeries, error) {
	// keys are "<root>/<folder>" or "<root>|<name>": match with the
	// separator, so ".../manga-old" is not under ".../manga"
	prefix := filepath.Clean(root)
	rows, err := r.db.QueryContext(ctx, `SELECT match_key, id, COALESCE(json_extract(meta_json,'$.scan_sig'),'') FROM items
	  WHERE kind='comic' AND missing_since IS NULL AND match_key IS NOT NULL AND substr(match_key,1,?) IN (?,?) ORDER BY id`,
		utf8.RuneCountInString(prefix)+1, prefix+string(filepath.Separator), prefix+"|")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]storedComicSeries{}
	for rows.Next() {
		var k string
		var s storedComicSeries
		if err := rows.Scan(&k, &s.id, &s.sig); err != nil {
			return nil, err
		}
		if _, dup := out[k]; !dup {
			out[k] = s
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: comic series: %w", err)
	}
	return out, nil
}
