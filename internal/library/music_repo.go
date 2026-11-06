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

	"github.com/antresol-app/antresol/internal/natsort"
	"github.com/antresol-app/antresol/internal/release"
)

// VariousArtists is the album artist of a compilation; "Various Artists"
// is its search alias.
const (
	VariousArtists      = "Разные исполнители"
	variousArtistsAlias = "Various Artists"
	unknownArtist       = "Неизвестный исполнитель"
)

// isVarious reports whether an artist name means "various artists".
func isVarious(name string) bool {
	switch release.Normalize(name) {
	case "various artists", "various", "va", "v a", "разные исполнители", "сборник":
		return true
	}
	return false
}

// TrackInput is one track of an album as the music scanner sees it. File
// is a plain track's own file, or the album's CUE image shared by several
// tracks (StartMs/EndMs within it; EndMs nil = to the end of the file).
// Cover is the album cover: "file:<path>" or a name in ArtDir.
type TrackInput struct {
	File                                        FileInput
	AlbumDir, Album, AlbumArtist, Artist, Title string
	Genre                                       string
	Year, Disc, Track                           int
	StartMs                                     int64
	EndMs                                       *int64
	Codec                                       string
	Lossless, Compilation                       bool
	MBReleaseGroupID, MBTrackID                 string
	Lyrics, Cover                               string
	// ReplayGain: the file's tags (an image's tags are the whole disc's:
	// only its album gain applies to its tracks)
	ReplayGain ReplayGain
}

// Track is a track item with its album, artist and position in its file.
// DurationMs is end - start (the file's probed duration for the last track
// of an image or a plain track), 0 when unknown.
type Track struct {
	Item
	AlbumID, ArtistID, FileID int64
	Artist, Album             string
	Disc, No                  int
	StartMs                   int64
	EndMs                     *int64
	DurationMs                int64
	Codec                     string
	Lossless                  bool
	Lyrics                    string
	// Gain: the levelling clients apply (tags, else the server's measure)
	Gain Loudness
}

// albumMatchKey is items.match_key of an album (see model.go).
func albumMatchKey(dir, album string) string { return dir + "|" + release.Normalize(album) }

// upsertArtist finds an artist by match_key (release.Normalize(name)) or
// creates it, and returns its id.
func upsertArtist(ctx context.Context, tx *sql.Tx, name string) (int64, error) {
	key := release.Normalize(name)
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE kind='artist' AND match_key=? ORDER BY id LIMIT 1`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		ts := now()
		res, err := tx.ExecContext(ctx, `INSERT INTO items(kind,title,sort_title,match_key,added_at,updated_at) VALUES('artist',?,?,?,?,?)`,
			name, sortTitle(name), key, ts, ts)
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}
	titles := []string{name}
	if name == VariousArtists {
		titles = append(titles, variousArtistsAlias)
	}
	return id, addTitles(ctx, tx, id, titles...)
}

// setAlbumMeta writes the scanner's keys into the album's meta_json and
// keeps the others (enrichers' data). scan_cover remembers the poster the
// scanner set, so a later scan may replace it but never an enricher's;
// scan_sig is the album folder's signature (see Scanner.folderSig). A nil
// value removes the key. A corrupt meta_json is an error, not overwritten.
func setAlbumMeta(ctx context.Context, x execer, id int64, set map[string]any) error {
	var raw string
	if err := x.QueryRowContext(ctx, `SELECT meta_json FROM items WHERE id=?`, id).Scan(&raw); err != nil {
		return err
	}
	m := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return fmt.Errorf("library: item %d meta_json: %w", id, err)
		}
	}
	for k, v := range set {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if string(b) == raw {
		return nil
	}
	_, err = x.ExecContext(ctx, `UPDATE items SET meta_json=? WHERE id=?`, string(b), id)
	return err
}

// UpsertAlbumTracks creates or refreshes an album of albumArtist with its
// tracks, in one transaction: the artist and the album are found by
// match_key, each track by its file and start (tracks_file_start). An
// image's tracks rows that are no longer in the sheet are deleted.
func (r *Repo) UpsertAlbumTracks(ctx context.Context, albumArtist string, tracks []TrackInput) (albumID int64, err error) {
	if len(tracks) == 0 {
		return 0, errors.New("library: album without tracks")
	}
	first := tracks[0]
	albumArtist = strings.TrimSpace(albumArtist)
	if albumArtist == "" {
		albumArtist = strings.TrimSpace(first.AlbumArtist)
	}
	if albumArtist == "" {
		albumArtist = unknownArtist
	}
	if isVarious(albumArtist) {
		albumArtist = VariousArtists
	}
	title := strings.TrimSpace(first.Album)
	if title == "" {
		title = filepath.Base(first.AlbumDir)
	}
	years := map[int]int{}
	year, cover, mbrg := 0, "", ""
	var genres []string
	seenGenre := map[string]bool{}
	compilation := albumArtist == VariousArtists
	for _, t := range tracks {
		if t.File.Path == "" {
			return 0, errors.New("library: track needs a file path")
		}
		if plausibleYear(t.Year) != 0 {
			years[t.Year]++
			if years[t.Year] > years[year] || (years[t.Year] == years[year] && t.Year < year) {
				year = t.Year
			}
		}
		if g := strings.TrimSpace(t.Genre); g != "" && !seenGenre[strings.ToLower(g)] {
			seenGenre[strings.ToLower(g)] = true
			genres = append(genres, g)
		}
		if cover == "" {
			cover = t.Cover
		}
		if mbrg == "" {
			mbrg = t.MBReleaseGroupID
		}
		compilation = compilation || t.Compilation
	}
	err = r.inTx(ctx, func(tx *sql.Tx) error {
		artistID, err := upsertArtist(ctx, tx, albumArtist)
		if err != nil {
			return err
		}
		key := albumMatchKey(first.AlbumDir, title)
		var scanCover sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT id, json_extract(meta_json,'$.scan_cover') FROM items WHERE kind='album' AND match_key=? ORDER BY id LIMIT 1`, key).Scan(&albumID, &scanCover)
		ts := now()
		if errors.Is(err, sql.ErrNoRows) {
			res, err := tx.ExecContext(ctx, `INSERT INTO items(kind,parent_id,title,sort_title,year,poster,match_key,added_at,updated_at) VALUES('album',?,?,?,?,?,?,?,?)`,
				artistID, title, sortTitle(title), year, nullStr(cover), key, ts, ts)
			if err != nil {
				return err
			}
			if albumID, err = res.LastInsertId(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET parent_id=?1 WHERE id=?2 AND parent_id IS NOT ?1`, artistID, albumID); err != nil {
				return err
			}
			if err := fillEmpty(ctx, tx, albumID, "", year, "", "", 0, "", ""); err != nil {
				return err
			}
			// a garbage year stored earlier is replaced (or cleared)
			if _, err := tx.ExecContext(ctx, `UPDATE items SET year=?1 WHERE id=?2 AND year<>?1 AND (year<?3 OR year>?4)`,
				year, albumID, minYear, maxYear); err != nil {
				return err
			}
			// the scanner replaces only a poster it set itself (or none)
			if cover != "" {
				if _, err := tx.ExecContext(ctx, `UPDATE items SET poster=?1 WHERE id=?2 AND poster IS NOT ?1 AND (poster IS NULL OR poster=?3)`,
					cover, albumID, scanCover.String); err != nil {
					return err
				}
			} else if scanCover.String != "" {
				// the cover the scanner set is gone (cover.jpg deleted)
				if _, err := tx.ExecContext(ctx, `UPDATE items SET poster=NULL WHERE id=? AND poster=?`, albumID, scanCover.String); err != nil {
					return err
				}
			}
		}
		meta := map[string]any{"compilation": compilation}
		if len(genres) > 0 {
			meta["genres"] = genres
		}
		if mbrg != "" {
			meta["mb_release_group"] = mbrg
		}
		if cover != "" {
			meta["scan_cover"] = cover
		} else {
			meta["scan_cover"] = nil
		}
		if err := setAlbumMeta(ctx, tx, albumID, meta); err != nil {
			return err
		}
		if err := addTitles(ctx, tx, albumID, title); err != nil {
			return err
		}

		// a file several tracks play from is a CUE image (owned by the album)
		uses := map[string]int{}
		for _, t := range tracks {
			uses[t.File.Path]++
		}
		imageIDs := map[string]int64{}
		starts := map[int64][]any{} // file id -> starts written in this call
		for _, t := range tracks {
			if err := upsertTrack(ctx, tx, albumID, albumArtist, t, uses[t.File.Path] > 1, imageIDs, starts); err != nil {
				return err
			}
		}
		// tracks of a file that are not in the input any more (an edited
		// sheet, an image now split): their items go, with their rows
		for fid, st := range starts {
			if _, err := tx.ExecContext(ctx, staleTracksSQL(len(st)), append([]any{fid}, st...)...); err != nil {
				return err
			}
		}
		return found(ctx, tx, albumID, artistID)
	})
	return albumID, err
}

// staleTracksSQL deletes the track items of a file (?1) whose start is not
// among n given ones. The rows are found through tracks_file_start and
// deleted by rowid: «+kind» keeps the planner off items_tmdb(kind), which
// would scan every track item once per file.
func staleTracksSQL(n int) string {
	return `DELETE FROM items WHERE id IN (SELECT item_id FROM tracks WHERE file_id=? AND start_ms NOT IN (?` +
		strings.Repeat(",?", n-1) + `)) AND +kind='track'`
}

// upsertTrack writes one track: its item, its files row (its own, or the
// album's for an image) and its tracks row.
func upsertTrack(ctx context.Context, tx *sql.Tx, albumID int64, albumArtist string, t TrackInput, image bool,
	imageIDs map[string]int64, starts map[int64][]any) error {
	title := strings.TrimSpace(t.Title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(t.File.Path), filepath.Ext(t.File.Path))
	}
	var itemID int64
	err := tx.QueryRowContext(ctx, `SELECT t.item_id FROM tracks t JOIN files f ON f.id=t.file_id WHERE f.path=? AND t.start_ms=?`,
		t.File.Path, t.StartMs).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) && !image {
		err = tx.QueryRowContext(ctx, `SELECT f.item_id FROM files f JOIN items i ON i.id=f.item_id WHERE f.path=? AND i.kind='track'`,
			t.File.Path).Scan(&itemID)
	}
	ts := now()
	if errors.Is(err, sql.ErrNoRows) {
		res, err := tx.ExecContext(ctx, `INSERT INTO items(kind,parent_id,title,sort_title,year,added_at,updated_at) VALUES('track',?,?,?,?,?,?)`,
			albumID, title, sortTitle(title), plausibleYear(t.Year), ts, ts)
		if err != nil {
			return err
		}
		if itemID, err = res.LastInsertId(); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if _, err := tx.ExecContext(ctx, `UPDATE items SET parent_id=?1, title=?2, sort_title=?3, year=?4, updated_at=?5
	    WHERE id=?6 AND (parent_id IS NOT ?1 OR title<>?2 OR year<>?4)`, albumID, title, sortTitle(title), plausibleYear(t.Year), ts, itemID); err != nil {
		return err
	}

	var fileID int64
	if image {
		id, ok := imageIDs[t.File.Path]
		if !ok {
			if id, err = upsertFileID(ctx, tx, albumID, t.File); err != nil {
				return err
			}
			imageIDs[t.File.Path] = id
		}
		fileID = id
	} else if fileID, err = upsertFileID(ctx, tx, itemID, t.File); err != nil {
		return err
	}
	// the probe (ffprobe codec_name) knows the codec better than the tag
	// reader (ALAC in .m4a); upsertFileID kept it while the file is unchanged
	var probe sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT probe_json FROM files WHERE id=?`, fileID).Scan(&probe); err != nil {
		return err
	}
	if c := probeCodec(probe.String); c != "" {
		t.Codec, t.Lossless = c, losslessCodecs[c]
	}
	// one tracks row per item: drop a row of this item at another position
	if _, err := tx.ExecContext(ctx, `DELETE FROM tracks WHERE item_id=? AND NOT (file_id=? AND start_ms=?)`, itemID, fileID, t.StartMs); err != nil {
		return err
	}
	artist := strings.TrimSpace(t.Artist)
	if artist == "" {
		artist = albumArtist
	}
	disc := t.Disc
	if disc <= 0 {
		disc = 1
	}
	lossless := 0
	if t.Lossless {
		lossless = 1
	}
	var end any
	if t.EndMs != nil {
		end = *t.EndMs
	}
	rg := t.ReplayGain
	if _, err := tx.ExecContext(ctx, `INSERT INTO tracks(item_id,file_id,artist,disc_no,track_no,start_ms,end_ms,codec,lossless,mbid,lyrics,
	    rg_track_gain,rg_track_peak,rg_album_gain,rg_album_peak,rg_scanned)
	  VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)
	  ON CONFLICT(file_id,start_ms) DO UPDATE SET item_id=excluded.item_id, artist=excluded.artist, disc_no=excluded.disc_no,
	    track_no=excluded.track_no, end_ms=excluded.end_ms, codec=excluded.codec, lossless=excluded.lossless,
	    mbid=excluded.mbid, lyrics=excluded.lyrics, rg_track_gain=excluded.rg_track_gain, rg_track_peak=excluded.rg_track_peak,
	    rg_album_gain=excluded.rg_album_gain, rg_album_peak=excluded.rg_album_peak, rg_scanned=1,
	    loud_state=CASE WHEN tracks.end_ms IS NOT excluded.end_ms THEN 0 ELSE tracks.loud_state END
	  WHERE tracks.item_id IS NOT excluded.item_id OR tracks.artist IS NOT excluded.artist OR tracks.disc_no IS NOT excluded.disc_no
	    OR tracks.track_no IS NOT excluded.track_no OR tracks.end_ms IS NOT excluded.end_ms OR tracks.codec IS NOT excluded.codec
	    OR tracks.lossless IS NOT excluded.lossless OR tracks.mbid IS NOT excluded.mbid OR tracks.lyrics IS NOT excluded.lyrics
	    OR tracks.rg_track_gain IS NOT excluded.rg_track_gain OR tracks.rg_track_peak IS NOT excluded.rg_track_peak
	    OR tracks.rg_album_gain IS NOT excluded.rg_album_gain OR tracks.rg_album_peak IS NOT excluded.rg_album_peak
	    OR tracks.rg_scanned=0`,
		itemID, fileID, artist, disc, t.Track, t.StartMs, end, t.Codec, lossless, t.MBTrackID, t.Lyrics,
		nullFloat(rg.TrackGain), nullFloat(rg.TrackPeak), nullFloat(rg.AlbumGain), nullFloat(rg.AlbumPeak)); err != nil {
		return err
	}
	starts[fileID] = append(starts[fileID], t.StartMs)
	if err := found(ctx, tx, itemID); err != nil {
		return err
	}
	return addTitles(ctx, tx, itemID, title)
}

// probeCodec returns the codec of the first audio stream of a cached probe
// (media.ProbeResult JSON, or raw ffprobe output), in the scanner's names;
// "" when unknown or not more precise than the extension (PCM in WAV/AIFF).
func probeCodec(probeJSON string) string {
	if strings.TrimSpace(probeJSON) == "" {
		return ""
	}
	var p struct {
		Audio []struct {
			Codec string
		}
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal([]byte(probeJSON), &p); err != nil {
		return ""
	}
	name := ""
	if len(p.Audio) > 0 {
		name = p.Audio[0].Codec
	} else {
		for _, s := range p.Streams {
			if s.CodecType == "audio" {
				name = s.CodecName
				break
			}
		}
	}
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case name == "" || strings.HasPrefix(name, "pcm_") || strings.HasPrefix(name, "dsd_"):
		return ""
	case name == "wavpack":
		return "wv"
	case strings.HasPrefix(name, "mp3"):
		return "mp3"
	}
	return name
}

// applyProbeCodec copies a file's probed codec onto its tracks rows.
func applyProbeCodec(ctx context.Context, x execer, fileID int64, probeJSON string) error {
	c := probeCodec(probeJSON)
	if c == "" {
		return nil
	}
	lossless := 0
	if losslessCodecs[c] {
		lossless = 1
	}
	_, err := x.ExecContext(ctx, `UPDATE tracks SET codec=?1, lossless=?2 WHERE file_id=?3 AND (codec<>?1 OR lossless<>?2)`, c, lossless, fileID)
	return err
}

// albumSigsSQL lists the scan signatures of the visible albums of one
// folder: their match_key starts with "<dir>|" (albumMatchKey), a range
// items_match(kind, match_key) serves, so each folder reads only its own
// albums. '}' is the byte after '|'.
const albumSigsSQL = `SELECT COALESCE(json_extract(meta_json,'$.scan_sig'),'') FROM items
  WHERE kind='album' AND match_key >= ?1 || '|' AND match_key < ?1 || '}' AND missing_since IS NULL`

// folderFilesSQL counts the visible files among n paths and their visible
// tracks (files.path is unique: one index lookup per path).
func folderFilesSQL(n int) string {
	return `SELECT COUNT(DISTINCT f.id), COUNT(*) FROM files f JOIN tracks t ON t.file_id=f.id JOIN items i ON i.id=t.item_id
	  WHERE f.path IN (?` + strings.Repeat(",?", n-1) + `) AND f.missing_since IS NULL AND i.missing_since IS NULL`
}

// folderPathsBatch bounds the bound parameters of one query, far below
// SQLite's 32766: a flat dump of audio files must not fail the scan.
const folderPathsBatch = 500

// unchangedFolder reports whether the albums found in an album folder were
// written from a folder with this signature, and every one of its audio
// files is a visible files row with tracks: the rescan then skips reading
// its tags and covers. It returns the counts for ScanStats.
func (r *Repo) unchangedFolder(ctx context.Context, dir, sig string, paths []string) (albums, tracks int, ok bool, err error) {
	if len(paths) == 0 {
		return 0, 0, false, nil
	}
	rows, err := r.db.QueryContext(ctx, albumSigsSQL, dir)
	if err != nil {
		return 0, 0, false, err
	}
	stale := false
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return 0, 0, false, err
		}
		albums++
		stale = stale || s != sig
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, false, err
	}
	if albums == 0 || stale {
		return 0, 0, false, nil
	}
	files := 0
	for start := 0; start < len(paths); start += folderPathsBatch {
		batch := paths[start:min(start+folderPathsBatch, len(paths))]
		args := make([]any, len(batch))
		for i, p := range batch {
			args[i] = p
		}
		var nf, nt int // batches hold distinct paths: the counts add up
		if err := r.db.QueryRowContext(ctx, folderFilesSQL(len(batch)), args...).Scan(&nf, &nt); err != nil {
			return 0, 0, false, err
		}
		files += nf
		tracks += nt
	}
	if files != len(paths) {
		return 0, 0, false, nil
	}
	return albums, tracks, true, nil
}

// setFolderSig records the album folder's signature on its albums.
func (r *Repo) setFolderSig(ctx context.Context, albumIDs []int64, sig string) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		for _, id := range albumIDs {
			if err := setAlbumMeta(ctx, tx, id, map[string]any{"scan_sig": sig}); err != nil {
				return err
			}
		}
		return nil
	})
}

const trackSelect = `SELECT ` + itemCols + `, COALESCE(al.id,0), COALESCE(al.parent_id,0), t.file_id, t.artist, COALESCE(al.title,''),
  t.disc_no, t.track_no, t.start_ms, t.end_ms, COALESCE(MAX(COALESCE(t.end_ms, f.duration_ms) - t.start_ms, 0), 0),
  t.codec, t.lossless, t.lyrics,
  COALESCE(t.rg_track_gain, ` + rgReference + ` - t.loud_lufs), CASE WHEN t.rg_track_gain IS NOT NULL THEN t.rg_track_peak ELSE t.loud_peak END,
  COALESCE(t.rg_album_gain, t.loud_album_gain), CASE WHEN t.rg_album_gain IS NOT NULL THEN t.rg_album_peak ELSE t.loud_album_peak END
  FROM items i JOIN tracks t ON t.item_id=i.id JOIN files f ON f.id=t.file_id LEFT JOIN items al ON al.id=i.parent_id`

func scanTrack(s scanner) (Track, error) {
	var tr Track
	var added, updated string
	var end sql.NullInt64
	var lossless int
	var gt, pt, ga, pa sql.NullFloat64
	err := s.Scan(&tr.ID, &tr.Kind, &tr.ParentID, &tr.Title, &tr.OriginalTitle, &tr.SortTitle, &tr.Year, &tr.Overview,
		&tr.Rating, &tr.TmdbID, &tr.SeasonNo, &tr.EpisodeNo, &tr.Poster, &tr.Backdrop, &added, &updated, &tr.MetaJSON,
		&tr.AlbumID, &tr.ArtistID, &tr.FileID, &tr.Artist, &tr.Album, &tr.Disc, &tr.No, &tr.StartMs, &end, &tr.DurationMs,
		&tr.Codec, &lossless, &tr.Lyrics, &gt, &pt, &ga, &pa)
	if err != nil {
		return tr, err
	}
	tr.Gain = Loudness{TrackGain: floatPtr(gt), TrackPeak: floatPtr(pt), AlbumGain: floatPtr(ga), AlbumPeak: floatPtr(pa)}
	if end.Valid {
		v := end.Int64
		tr.EndMs = &v
	}
	tr.Lossless = lossless != 0
	if tr.AddedAt, err = time.Parse(time.RFC3339, added); err != nil {
		return tr, fmt.Errorf("library: item %d added_at: %w", tr.ID, err)
	}
	if tr.UpdatedAt, err = time.Parse(time.RFC3339, updated); err != nil {
		return tr, fmt.Errorf("library: item %d updated_at: %w", tr.ID, err)
	}
	return tr, nil
}

// TrackByID returns one track.
func (r *Repo) TrackByID(ctx context.Context, id int64) (Track, error) {
	tr, err := scanTrack(r.db.QueryRowContext(ctx, trackSelect+` WHERE i.id=? AND i.kind='track' AND i.missing_since IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return tr, ErrNotFound
	}
	return tr, err
}

// AlbumTracks returns the tracks of an album in playing order: disc,
// number, start within the file, then the title in natural order.
func (r *Repo) AlbumTracks(ctx context.Context, albumID int64) ([]Track, error) {
	rows, err := r.db.QueryContext(ctx, trackSelect+` WHERE i.parent_id=? AND i.kind='track' AND i.missing_since IS NULL
	  ORDER BY t.disc_no, t.track_no, t.start_ms, i.id`, albumID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Track
	for rows.Next() {
		tr, err := scanTrack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Disc != b.Disc {
			return a.Disc < b.Disc
		}
		if a.No != b.No {
			return a.No < b.No
		}
		if a.StartMs != b.StartMs {
			return a.StartMs < b.StartMs
		}
		return natsort.Less(a.Title, b.Title)
	})
	return out, nil
}

// AlbumQuery filters Albums. Sort: added (default, newest first), title,
// year (newest first), artist, random, frequent / recent (by UserID's
// plays; only played albums). Starred keeps UserID's starred albums, Genre
// albums with that genre (case-insensitive), FromYear/ToYear a year range
// (both inclusive; From > To lists newest first, as Subsonic does) and,
// with no Sort, orders by year. MaxRating filters by the rating of the
// album's root (its artist), the rule of Repo.Visible. Owner keeps the
// albums that profile brought (item_owners; 0 = all).
type AlbumQuery struct {
	ArtistID         int64
	Sort             string
	UserID           int64
	Starred          bool
	Genre            string
	FromYear, ToYear int
	Limit, Offset    int
	MaxRating        string
	Owner            int64
}

// Album is an album item with its artist and track summary.
type Album struct {
	Item
	ArtistID   int64
	Artist     string
	TrackCount int
	DurationMs int64
	Lossless   bool // every track is lossless
}

// ArtistRow is an artist with its album count; Cover is the URL of its
// newest album's cover (ImageURL with the album's id), "" without one.
type ArtistRow struct {
	Item
	AlbumCount int
	Cover      string
}

const albumCols = itemCols + `, COALESCE(ar.id,0), COALESCE(ar.title,''),
  (SELECT COUNT(*) FROM items ti WHERE ti.parent_id=i.id AND ti.kind='track' AND ti.missing_since IS NULL),
  (SELECT COALESCE(SUM(MAX(COALESCE(t.end_ms, f.duration_ms, 0) - t.start_ms, 0)),0) FROM items ti JOIN tracks t ON t.item_id=ti.id
     JOIN files f ON f.id=t.file_id WHERE ti.parent_id=i.id AND ti.kind='track' AND ti.missing_since IS NULL),
  (SELECT COALESCE(MIN(t.lossless),0) FROM items ti JOIN tracks t ON t.item_id=ti.id WHERE ti.parent_id=i.id AND ti.kind='track' AND ti.missing_since IS NULL)`

func scanAlbum(s scanner) (Album, error) {
	var a Album
	var added, updated string
	var lossless int
	it := &a.Item
	err := s.Scan(&it.ID, &it.Kind, &it.ParentID, &it.Title, &it.OriginalTitle, &it.SortTitle, &it.Year, &it.Overview,
		&it.Rating, &it.TmdbID, &it.SeasonNo, &it.EpisodeNo, &it.Poster, &it.Backdrop, &added, &updated, &it.MetaJSON,
		&a.ArtistID, &a.Artist, &a.TrackCount, &a.DurationMs, &lossless)
	if err != nil {
		return a, err
	}
	a.Lossless = lossless != 0
	if it.AddedAt, err = time.Parse(time.RFC3339, added); err != nil {
		return a, fmt.Errorf("library: item %d added_at: %w", it.ID, err)
	}
	if it.UpdatedAt, err = time.Parse(time.RFC3339, updated); err != nil {
		return a, fmt.Errorf("library: item %d updated_at: %w", it.ID, err)
	}
	return a, nil
}

func (r *Repo) queryAlbums(ctx context.Context, q string, args ...any) ([]Album, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Album{}
	for rows.Next() {
		a, err := scanAlbum(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Albums returns a page of albums and the total count for the filter.
func (r *Repo) Albums(ctx context.Context, q AlbumQuery) ([]Album, int, error) {
	from := ` FROM items i LEFT JOIN items ar ON ar.id=i.parent_id`
	var joinArgs, args []any
	if q.Sort == "frequent" || q.Sort == "recent" {
		from += ` JOIN (SELECT t.parent_id album, COUNT(*) n, MAX(p.played_at) last FROM plays p JOIN items t ON t.id=p.item_id
		  WHERE p.user_id=? GROUP BY 1) pl ON pl.album=i.id`
		joinArgs = append(joinArgs, q.UserID)
	}
	where := []string{"i.kind='album'", "i.missing_since IS NULL"}
	if q.ArtistID != 0 {
		where = append(where, "i.parent_id=?")
		args = append(args, q.ArtistID)
	}
	if q.Starred {
		where = append(where, "EXISTS (SELECT 1 FROM stars WHERE user_id=? AND item_id=i.id)")
		args = append(args, q.UserID)
	}
	if g := strings.TrimSpace(q.Genre); g != "" {
		// NOCASE folds ASCII only: the stored spellings that equal g in any
		// case («рок» for «Рок») are looked up first
		forms, err := r.genreForms(ctx, g)
		if err != nil {
			return nil, 0, err
		}
		where = append(where, "json_valid(i.meta_json) AND EXISTS (SELECT 1 FROM json_each(i.meta_json,'$.genres') WHERE value COLLATE NOCASE IN (?"+strings.Repeat(",?", len(forms)-1)+"))")
		for _, f := range forms {
			args = append(args, f)
		}
	}
	years := q.FromYear != 0 || q.ToYear != 0
	if years {
		lo, hi := q.FromYear, q.ToYear
		if hi == 0 {
			hi = 9999
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		where = append(where, "i.year BETWEEN ? AND ?")
		args = append(args, lo, hi)
	}
	if q.MaxRating != "" {
		// the album's root (its artist) decides, as Repo.Visible does
		c, a := MusicRootAllowed("(CASE WHEN ar.id IS NOT NULL THEN ar.rating ELSE i.rating END)", q.MaxRating)
		where = append(where, c)
		args = append(args, a...)
	}
	if q.Owner != 0 {
		c, a := ownedBy("i.id", q.Owner)
		where = append(where, c)
		args = append(args, a...)
	}
	w := " WHERE " + strings.Join(where, " AND ")
	all := append(append([]any{}, joinArgs...), args...)
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from+w, all...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "i.added_at DESC, i.id DESC"
	switch q.Sort {
	case "title":
		order = "i.sort_title, i.id"
	case "artist":
		order = "COALESCE(ar.sort_title,''), i.year, i.sort_title, i.id"
	case "year":
		order = "i.year DESC, i.sort_title, i.id"
	case "random":
		order = "random()"
	case "frequent":
		order = "pl.n DESC, pl.last DESC, i.id"
	case "recent":
		order = "pl.last DESC, i.id"
	case "":
		if years {
			order = "i.year, i.sort_title, i.id"
			if q.ToYear != 0 && q.FromYear > q.ToYear {
				order = "i.year DESC, i.sort_title, i.id"
			}
		}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = -1
	}
	all = append(all, limit, max(q.Offset, 0))
	out, err := r.queryAlbums(ctx, `SELECT `+albumCols+from+w+` ORDER BY `+order+` LIMIT ? OFFSET ?`, all...)
	return out, total, err
}

// AlbumByID returns one visible album with its summary.
func (r *Repo) AlbumByID(ctx context.Context, id int64) (Album, error) {
	out, err := r.AlbumsByID(ctx, []int64{id})
	if err != nil {
		return Album{}, err
	}
	a, ok := out[id]
	if !ok {
		return Album{}, ErrNotFound
	}
	return a, nil
}

// AlbumsByID is AlbumByID of many ids by id (unknown and missing ones are
// left out).
func (r *Repo) AlbumsByID(ctx context.Context, ids []int64) (map[int64]Album, error) {
	out := make(map[int64]Album, len(ids))
	for part := range chunks(ids, 500) {
		in, args := inArgs(part)
		as, err := r.queryAlbums(ctx, `SELECT `+albumCols+` FROM items i LEFT JOIN items ar ON ar.id=i.parent_id
		  WHERE i.id IN `+in+` AND i.kind='album' AND i.missing_since IS NULL`, args...)
		if err != nil {
			return nil, err
		}
		for _, a := range as {
			out[a.ID] = a
		}
	}
	return out, nil
}

// AlbumDir is the album's folder (from its match_key), "" when unknown.
func (r *Repo) AlbumDir(ctx context.Context, albumID int64) (string, error) {
	var key sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT match_key FROM items WHERE id=? AND kind='album'`, albumID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if i := strings.LastIndex(key.String, "|"); i > 0 {
		return key.String[:i], nil
	}
	return "", nil
}

// Tracks returns the visible tracks with these ids, in the order of ids
// (repeats kept; unknown ids skipped).
func (r *Repo) Tracks(ctx context.Context, ids []int64) ([]Track, error) {
	if len(ids) == 0 {
		return []Track{}, nil
	}
	by := map[int64]Track{}
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		part := ids[start:min(start+chunk, len(ids))]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		rows, err := r.db.QueryContext(ctx, trackSelect+` WHERE i.id IN (?`+strings.Repeat(",?", len(part)-1)+`) AND i.kind='track' AND i.missing_since IS NULL`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			tr, err := scanTrack(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			by[tr.ID] = tr
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]Track, 0, len(ids))
	for _, id := range ids {
		if tr, ok := by[id]; ok {
			out = append(out, tr)
		}
	}
	return out, nil
}

// Songs returns a page of all visible tracks by title and the total.
func (r *Repo) Songs(ctx context.Context, limit, offset int) ([]Track, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items i JOIN tracks t ON t.item_id=i.id WHERE i.kind='track' AND i.missing_since IS NULL`).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = -1
	}
	rows, err := r.db.QueryContext(ctx, trackSelect+` WHERE i.kind='track' AND i.missing_since IS NULL ORDER BY i.sort_title, i.id LIMIT ? OFFSET ?`, limit, max(offset, 0))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Track{}
	for rows.Next() {
		tr, err := scanTrack(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, tr)
	}
	return out, total, rows.Err()
}

// Artists returns a page of the artists that have visible albums, by
// name, and the total.
func (r *Repo) Artists(ctx context.Context, limit, offset int) ([]ArtistRow, int, error) {
	return r.ArtistsOf(ctx, limit, offset, 0)
}

// ArtistsOf is Artists with an album owner brought (item_owners; 0 = all).
func (r *Repo) ArtistsOf(ctx context.Context, limit, offset int, owner int64) ([]ArtistRow, int, error) {
	has, args := `EXISTS (SELECT 1 FROM items al WHERE al.parent_id=i.id AND al.kind='album' AND al.missing_since IS NULL)`, []any(nil)
	if owner != 0 {
		has, args = artistOwnedBy("i.id", owner)
	}
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items i WHERE i.kind='artist' AND i.missing_since IS NULL AND `+has, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = -1
	}
	items, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items i WHERE i.kind='artist' AND i.missing_since IS NULL AND `+has+`
	  ORDER BY i.sort_title, i.id LIMIT ? OFFSET ?`, append(args, limit, max(offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	out, err := r.artistRows(ctx, items)
	return out, total, err
}

// artistRows is artistRow of a page of artists in two queries, not two
// per artist.
func (r *Repo) artistRows(ctx context.Context, items []Item) ([]ArtistRow, error) {
	out := make([]ArtistRow, len(items))
	at := make(map[int64]int, len(items))
	ids := make([]int64, len(items))
	for i, it := range items {
		out[i] = ArtistRow{Item: it, Cover: ImageURL(0, "poster", "")}
		at[it.ID], ids[i] = i, it.ID
	}
	for part := range chunks(ids, 500) {
		in, args := inArgs(part)
		rows, err := r.db.QueryContext(ctx, `SELECT parent_id, COUNT(*) FROM items WHERE parent_id IN `+in+` AND kind='album' AND missing_since IS NULL
		  GROUP BY parent_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				rows.Close()
				return nil, err
			}
			out[at[id]].AlbumCount = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		// the cover: the newest album's with one (artistRow's ORDER BY … LIMIT 1)
		rows, err = r.db.QueryContext(ctx, `SELECT parent_id, id, poster FROM (SELECT parent_id, id, poster,
		    ROW_NUMBER() OVER (PARTITION BY parent_id ORDER BY year DESC, added_at DESC, id DESC) AS n
		  FROM items WHERE parent_id IN `+in+` AND kind='album' AND missing_since IS NULL AND COALESCE(poster,'')<>'') WHERE n=1`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var pid, id int64
			var poster string
			if err := rows.Scan(&pid, &id, &poster); err != nil {
				rows.Close()
				return nil, err
			}
			out[at[pid]].Cover = ImageURL(id, "poster", poster)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ArtistInfo returns the album count and cover of an artist item.
func (r *Repo) ArtistInfo(ctx context.Context, it Item) (ArtistRow, error) {
	return r.artistRow(ctx, it)
}

func (r *Repo) artistRow(ctx context.Context, it Item) (ArtistRow, error) {
	row := ArtistRow{Item: it}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE parent_id=? AND kind='album' AND missing_since IS NULL`, it.ID).Scan(&row.AlbumCount); err != nil {
		return row, err
	}
	var id int64
	var poster string
	err := r.db.QueryRowContext(ctx, `SELECT id, poster FROM items WHERE parent_id=? AND kind='album' AND missing_since IS NULL
	  AND COALESCE(poster,'')<>'' ORDER BY year DESC, added_at DESC, id DESC LIMIT 1`, it.ID).Scan(&id, &poster)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return row, err
	}
	row.Cover = ImageURL(id, "poster", poster)
	return row, nil
}

// AppearsOn returns the albums of other album artists (compilations) that
// have tracks by artistName (case-insensitive), newest first.
func (r *Repo) AppearsOn(ctx context.Context, artistName string) ([]Album, error) {
	name := strings.TrimSpace(artistName)
	if name == "" {
		return []Album{}, nil
	}
	return r.queryAlbums(ctx, appearsOnSQL, name)
}

// appearsOnSQL starts from the artist's tracks (tracks_artist_nocase) and
// reads only their albums, not every album with a probe of its tracks.
const appearsOnSQL = `SELECT ` + albumCols + ` FROM items i LEFT JOIN items ar ON ar.id=i.parent_id
  WHERE i.id IN (SELECT ti.parent_id FROM tracks t JOIN items ti ON ti.id=t.item_id
      WHERE t.artist = ?1 COLLATE NOCASE AND ti.kind='track' AND ti.missing_since IS NULL)
    AND i.kind='album' AND i.missing_since IS NULL AND COALESCE(ar.title,'') <> ?1 COLLATE NOCASE
  ORDER BY i.year DESC, i.sort_title, i.id`

// genreForms returns g and every stored album genre equal to it ignoring
// case (Unicode).
func (r *Repo) genreForms(ctx context.Context, g string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT j.value FROM items i, json_each(i.meta_json,'$.genres') j
	  WHERE i.kind='album' AND json_valid(i.meta_json) AND j.type='text'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{g}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if v != g && strings.EqualFold(v, g) {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

// FindAlbum finds a visible album of this artist by its title (both
// compared normalized: case, ё, punctuation); ok is false when there is
// none. «Найти и добавить» marks catalog albums already in the library.
func (r *Repo) FindAlbum(ctx context.Context, artist, album string) (Item, bool, error) {
	a, t := release.Normalize(artist), release.Normalize(album)
	if a == "" || t == "" {
		return Item{}, false, nil
	}
	var id int64
	err := r.db.QueryRowContext(ctx, `SELECT i.id FROM items i JOIN items ar ON ar.id=i.parent_id
	  WHERE i.kind='album' AND i.missing_since IS NULL AND ar.kind='artist' AND ar.match_key=?
	    AND (i.sort_title=? OR EXISTS (SELECT 1 FROM item_titles n WHERE n.item_id=i.id AND n.norm=?))
	  ORDER BY i.id LIMIT 1`, a, t, t).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	return found1(r.Item(ctx, id))
}

// found1 turns an Item lookup into the (item, ok, err) form: a missing
// item is not an error.
func found1(it Item, err error) (Item, bool, error) {
	if errors.Is(err, ErrNotFound) {
		return Item{}, false, nil
	}
	return it, err == nil, err
}

// FindAlbumByMBID returns the album (not missing) of a MusicBrainz release
// group: the id the enricher matched (meta mb_id) or the one the files'
// tags name (mb_release_group). ok false: none.
func (r *Repo) FindAlbumByMBID(ctx context.Context, rgid string) (Item, bool, error) {
	rgid = strings.TrimSpace(rgid)
	if rgid == "" {
		return Item{}, false, nil
	}
	var id int64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM items WHERE kind='album' AND missing_since IS NULL
	  AND (json_extract(meta_json,'$.mb_id')=?1 OR json_extract(meta_json,'$.mb_release_group')=?1)
	  ORDER BY id LIMIT 1`, rgid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	return found1(r.Item(ctx, id))
}

// AlbumFolderTracks returns the visible tracks of every album item in the
// folder of albumID (one folder may hold several items when its files'
// album tags differ), in album and playing order; none for an unknown
// album.
func (r *Repo) AlbumFolderTracks(ctx context.Context, albumID int64) ([]Track, error) {
	dir, err := r.AlbumDir(ctx, albumID)
	if errors.Is(err, ErrNotFound) || err == nil && dir == "" {
		return []Track{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, trackSelect+` WHERE i.kind='track' AND i.missing_since IS NULL
	  AND al.kind='album' AND al.match_key >= ?1 || '|' AND al.match_key < ?1 || '}'
	  ORDER BY al.id, t.disc_no, t.track_no, t.start_ms, i.id`, dir)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Track{}
	for rows.Next() {
		tr, err := scanTrack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// albumTitlesIn returns the titles of the album items in folder dir
// (missing ones too: a rescan brings them back), oldest first. Their match
// keys are "<dir>|…": the range ['<dir>|', '<dir>}') uses items_match
// ('}' follows '|').
func (r *Repo) albumTitlesIn(ctx context.Context, dir string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT title FROM items WHERE kind='album'
	  AND match_key >= ?1 || '|' AND match_key < ?1 || '}' ORDER BY id`, dir)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
