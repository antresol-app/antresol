package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/release"
)

// HaveEpisodes is the set of {season, episode} of a series (by TMDB id,
// every top-level series item with it) that have a file in the library.
// A file holding several episodes ("S01E01-E02", "S01E01E02") is one
// episode item; its whole range counts.
func (r *Repo) HaveEpisodes(ctx context.Context, tmdbID int64) (map[[2]int]bool, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT se.season_no, ep.episode_no, f.path FROM items s
		JOIN items se ON se.parent_id=s.id AND se.kind='season' AND se.missing_since IS NULL
		JOIN items ep ON ep.parent_id=se.id AND ep.kind='episode' AND ep.missing_since IS NULL
		JOIN files f ON f.item_id=ep.id AND f.missing_since IS NULL
		WHERE s.kind='series' AND s.parent_id IS NULL AND s.tmdb_id=? AND s.missing_since IS NULL
		  AND se.season_no IS NOT NULL AND ep.episode_no IS NOT NULL`, tmdbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[[2]int]bool{}
	for rows.Next() {
		var season, ep int
		var path string
		if err := rows.Scan(&season, &ep, &path); err != nil {
			return nil, err
		}
		have[[2]int{season, ep}] = true
		for _, e := range rangeAfter(path, season, ep) {
			have[[2]int{season, e}] = true
		}
	}
	return have, rows.Err()
}

// rangeAfter lists the episodes after ep that the file named by path also
// holds ("S01E01-E02" holds 2 after 1).
func rangeAfter(path string, season, ep int) []int {
	info := release.Parse(filepath.Base(path))
	if info.Episode != ep || info.EpisodeEnd <= ep || (info.Season != 0 && info.Season != season) {
		return nil
	}
	var out []int
	for e := ep + 1; e <= info.EpisodeEnd && e < ep+100; e++ {
		out = append(out, e)
	}
	return out
}

// EpisodeFile is the best file of one episode: its episode item, the file
// and its resolution class (ResolutionOf; 0 unknown).
type EpisodeFile struct {
	ItemID, FileID int64
	Resolution     int
}

// ResolutionOf is the file's video resolution class from its cached probe:
// 2160, 1080, 720 or 480 by the longer edge (a wide 1920x800 film is
// 1080), 0 when the file has no probed video.
func ResolutionOf(f File) int {
	if f.ProbeJSON == "" {
		return 0
	}
	var p media.ProbeResult
	if json.Unmarshal([]byte(f.ProbeJSON), &p) != nil || p.Video == nil {
		return 0
	}
	long, short := max(p.Video.Width, p.Video.Height), min(p.Video.Width, p.Video.Height)
	switch {
	case p.Video.Height <= 0:
		return 0
	case long >= 3200 || short >= 2000:
		return 2160
	case long >= 1800 || short >= 1000:
		return 1080
	case long >= 1200 || short >= 700:
		return 720
	}
	return 480
}

// EpisodeFiles is, per {season, episode} of a series (by TMDB id) with a
// file, the episode item and its best file (highest resolution, then the
// lowest file id). A multi-episode file serves its whole range, like
// HaveEpisodes.
func (r *Repo) EpisodeFiles(ctx context.Context, tmdbID int64) (map[[2]int]EpisodeFile, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT se.season_no, ep.episode_no, ep.id, f.id, f.path, f.probe_json FROM items s
		JOIN items se ON se.parent_id=s.id AND se.kind='season' AND se.missing_since IS NULL
		JOIN items ep ON ep.parent_id=se.id AND ep.kind='episode' AND ep.missing_since IS NULL
		JOIN files f ON f.item_id=ep.id AND f.missing_since IS NULL
		WHERE s.kind='series' AND s.parent_id IS NULL AND s.tmdb_id=? AND s.missing_since IS NULL
		  AND se.season_no IS NOT NULL AND ep.episode_no IS NOT NULL ORDER BY f.id`, tmdbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]int]EpisodeFile{}
	put := func(k [2]int, c EpisodeFile) {
		if old, ok := out[k]; !ok || c.Resolution > old.Resolution {
			out[k] = c
		}
	}
	for rows.Next() {
		var season, ep int
		var c EpisodeFile
		var path string
		var probe sql.NullString
		if err := rows.Scan(&season, &ep, &c.ItemID, &c.FileID, &path, &probe); err != nil {
			return nil, err
		}
		c.Resolution = ResolutionOf(File{ProbeJSON: probe.String})
		put([2]int{season, ep}, c)
		for _, e := range rangeAfter(path, season, ep) {
			put([2]int{season, e}, c)
		}
	}
	return out, rows.Err()
}
