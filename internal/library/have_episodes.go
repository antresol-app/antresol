package library

import (
	"context"
	"path/filepath"

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
		info := release.Parse(filepath.Base(path))
		if info.Episode == ep && info.EpisodeEnd > ep && (info.Season == 0 || info.Season == season) {
			for e := ep + 1; e <= info.EpisodeEnd && e < ep+100; e++ {
				have[[2]int{season, e}] = true
			}
		}
	}
	return have, rows.Err()
}
