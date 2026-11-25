package library

import (
	"context"
	"slices"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/release"
)

// Queries of the addons' core API over addon_file_refs (migration 0090):
// a library file an addon brought in carries the addon's ref.

// refFiles is the condition «f is a file of the addon's ref» and its
// arguments.
func refFiles(addonID, ref string) (string, []any) {
	return `f.id IN (SELECT file_id FROM addon_file_refs WHERE addon_id=? AND ref=?)`, []any{addonID, ref}
}

// fileColsNoProbe is fileCols with an empty probe: probe_json is never read.
const fileColsNoProbe = `id, item_id, path, size, mtime, COALESCE(duration_ms,0), ''`

// FilesUnderNoProbe is FilesUnder without the files' probe summaries (a
// whole library root's would be hundreds of MB): ProbeJSON is "".
func (r *Repo) FilesUnderNoProbe(ctx context.Context, dir string) ([]File, error) {
	return r.filesUnder(ctx, dir, fileColsNoProbe)
}

// FilesOfRef lists the shown files that carry the addon's ref, by path.
func (r *Repo) FilesOfRef(ctx context.Context, addonID, ref string) ([]File, error) {
	cond, args := refFiles(addonID, ref)
	files, err := r.queryFiles(ctx, `SELECT `+fileCols+` FROM files f WHERE `+cond+` AND f.missing_since IS NULL ORDER BY f.path`, args...)
	return nonNil(files), err
}

// TracksOfRef returns the visible tracks whose file carries the addon's
// ref, in album and playing order.
func (r *Repo) TracksOfRef(ctx context.Context, addonID, ref string) ([]Track, error) {
	cond, args := refFiles(addonID, ref)
	tracks, err := r.queryTracks(ctx, trackSelect+` WHERE `+cond+` AND i.kind='track' AND i.missing_since IS NULL
	  ORDER BY i.parent_id, t.disc_no, t.track_no, t.start_ms, i.id`, args...)
	return nonNil(tracks), err
}

// ClaimRef makes userID an owner of the titles of the library files that
// carry the addon's ref.
func (r *Repo) ClaimRef(ctx context.Context, addonID, ref string, userID int64) error {
	cond, args := refFiles(addonID, ref)
	return r.claimWhere(ctx, userID, cond, args...)
}

// ClaimFiles makes userID an owner of the titles of these library files;
// ids with no row are skipped.
func (r *Repo) ClaimFiles(ctx context.Context, fileIDs []int64, userID int64) error {
	for part := range slices.Chunk(fileIDs, 500) {
		if err := r.claimWhere(ctx, userID, `f.id IN `+db.In(len(part)), db.Args(part)...); err != nil {
			return err
		}
	}
	return nil
}

// seriesAudioSQL: the audio track titles of each episode file of the
// series (tmdb id ?1), its episodes under the series itself or under its
// seasons; a file without a valid probe lists none.
const seriesAudioSQL = `WITH
	series AS (SELECT id FROM items WHERE kind='series' AND tmdb_id=?1 AND missing_since IS NULL),
	eps AS (
		SELECT e.id, COALESCE(e.season_no, 0) AS season, COALESCE(e.episode_no, 0) AS episode
		FROM series JOIN items e ON e.parent_id=series.id
		WHERE +e.kind='episode' AND e.missing_since IS NULL
		UNION ALL
		SELECT e.id, COALESCE(e.season_no, se.season_no, 0), COALESCE(e.episode_no, 0)
		FROM series JOIN items se ON se.parent_id=series.id JOIN items e ON e.parent_id=se.id
		WHERE +se.kind='season' AND se.missing_since IS NULL AND +e.kind='episode' AND e.missing_since IS NULL),
	fs AS (
		SELECT eps.season, eps.episode, f.id AS file_id,
		  CASE WHEN f.probe_json IS NOT NULL AND f.probe_json<>'' AND json_valid(f.probe_json) THEN f.probe_json END AS probe
		FROM eps JOIN files f ON f.item_id=eps.id
		WHERE f.missing_since IS NULL)
SELECT fs.season, fs.episode, fs.file_id, a.key IS NOT NULL, COALESCE(json_extract(a.value, '$.Title'), '')
FROM fs LEFT JOIN json_each(fs.probe, '$.Audio') a
ORDER BY fs.season, fs.episode, fs.file_id, a.key`

// SeriesAudio lists the audio track titles of the series' episode files
// (by season, episode, file) and the audio track each profile last chose
// in it (the latest first).
func (r *Repo) SeriesAudio(ctx context.Context, tmdbID int64) (addonkit.SeriesAudio, error) {
	out := addonkit.SeriesAudio{Files: []addonkit.EpisodeAudio{}, Profiles: []addonkit.ProfileVoice{}}
	rows, err := r.db.QueryContext(ctx, seriesAudioSQL, tmdbID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	last := int64(-1)
	for rows.Next() {
		var season, episode int
		var fileID int64
		var has bool
		var title string
		if err := rows.Scan(&season, &episode, &fileID, &has, &title); err != nil {
			return out, err
		}
		if fileID != last {
			last = fileID
			out.Files = append(out.Files, addonkit.EpisodeAudio{Season: season, Episode: episode, Titles: []string{}})
		}
		if has {
			e := &out.Files[len(out.Files)-1]
			e.Titles = append(e.Titles, title)
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	prows, err := r.db.QueryContext(ctx, `SELECT st.user_id, st.audio_title, st.updated_at
		FROM series_tracks st JOIN items i ON i.id=st.series_id
		WHERE i.kind='series' AND i.tmdb_id=? AND i.missing_since IS NULL AND st.has_audio=1
		ORDER BY st.updated_at DESC, st.user_id`, tmdbID)
	if err != nil {
		return out, err
	}
	defer prows.Close()
	for prows.Next() {
		var p addonkit.ProfileVoice
		var at string
		if err := prows.Scan(&p.UserID, &p.AudioTitle, &at); err != nil {
			return out, err
		}
		p.UpdatedAt, _ = time.Parse(time.RFC3339, at) // a bad stamp: the zero time
		out.Profiles = append(out.Profiles, p)
	}
	return out, prows.Err()
}

// SearchTitles finds the shown items of kind with a title, original title
// or alias equal to title once normalised (item_titles.norm: case, ё=е,
// punctuation), lowest id first, at most limit (50 when 0 or less).
func (r *Repo) SearchTitles(ctx context.Context, kind, title string, limit int) ([]Item, error) {
	norm := release.Normalize(title)
	if norm == "" {
		return []Item{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	out, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items i WHERE i.kind=? AND i.missing_since IS NULL
	  AND EXISTS (SELECT 1 FROM item_titles n WHERE n.item_id=i.id AND n.norm=?) ORDER BY i.id LIMIT ?`, kind, norm, limit)
	if out == nil {
		out = []Item{}
	}
	return out, err
}

// Aliases are the other titles of the items (item_titles less each one's
// title and original title, compared normalised) by item id, in the
// order the library stored them; every item has a list, [] when none.
func (r *Repo) Aliases(ctx context.Context, items ...Item) (map[int64][]string, error) {
	out := make(map[int64][]string, len(items))
	if len(items) == 0 {
		return out, nil
	}
	own := make(map[int64][2]string, len(items))
	args := make([]any, 0, len(items))
	for _, it := range items {
		if _, ok := out[it.ID]; ok {
			continue
		}
		out[it.ID] = []string{}
		own[it.ID] = [2]string{release.Normalize(it.Title), release.Normalize(it.OriginalTitle)}
		args = append(args, it.ID)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT item_id, title, norm FROM item_titles WHERE item_id IN `+db.In(len(args))+` ORDER BY item_id, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var title, norm string
		if err := rows.Scan(&id, &title, &norm); err != nil {
			return nil, err
		}
		if o := own[id]; norm != o[0] && norm != o[1] {
			out[id] = append(out[id], title)
		}
	}
	return out, rows.Err()
}
