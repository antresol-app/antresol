package library

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/release"
)

// HaveEpisodes is the set of {season, episode} of a series (by TMDB id,
// every top-level series item with it) that have a file in the library.
// A file holding several episodes ("S01E01-E02", "S01E01E02") is one
// episode item; its whole range counts. It is the pairs of EpisodeItems,
// the one query both answer from.
func (r *Repo) HaveEpisodes(ctx context.Context, tmdbID int64) (map[[2]int]bool, error) {
	refs, err := r.EpisodeItems(ctx, tmdbID)
	if err != nil {
		return nil, err
	}
	have := make(map[[2]int]bool, len(refs))
	for _, e := range refs {
		have[[2]int{e.Season, e.Episode}] = true
	}
	return have, nil
}

// EpisodeRef is one {season, episode} of a series that has a file, with the
// episode item that holds it and the top-level series item it belongs to.
type EpisodeRef struct {
	Season, Episode int
	ItemID          int64 // the episode item with the file
	SeriesID        int64 // the series item above it (the root that carries its rating)
}

// EpisodeItems is the pairs of HaveEpisodes with the item of each: per {season,
// episode} of a series (by TMDB id) with a file, the episode item holding
// it, in season and episode order. A multi-episode file serves its whole
// range, as in HaveEpisodes, with the item that holds the file; an episode
// item of its own wins over a range that also covers its pair. Several
// top-level series with the TMDB id share the answer: a pair goes to the
// lowest series id, then the lowest item id, among equals. That is decided
// here, before any visibility rule: a caller that hides the series of a ref
// (SeriesID) loses the pair, it is not handed to the next series that holds
// it.
func (r *Repo) EpisodeItems(ctx context.Context, tmdbID int64) ([]EpisodeRef, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT s.id, se.season_no, ep.episode_no, ep.id, f.path FROM items s
		JOIN items se ON se.parent_id=s.id AND se.kind='season' AND se.missing_since IS NULL
		JOIN items ep ON ep.parent_id=se.id AND ep.kind='episode' AND ep.missing_since IS NULL
		JOIN files f ON f.item_id=ep.id AND f.missing_since IS NULL
		WHERE s.kind='series' AND s.parent_id IS NULL AND s.tmdb_id=? AND s.missing_since IS NULL
		  AND se.season_no IS NOT NULL AND ep.episode_no IS NOT NULL ORDER BY s.id, ep.id, f.id`, tmdbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type pick struct {
		ref    EpisodeRef
		ranged bool // covered by a range file, not an episode item of its own
	}
	best := map[[2]int]pick{}
	put := func(ref EpisodeRef, ranged bool) {
		k := [2]int{ref.Season, ref.Episode}
		// rows come in (series, item) order: the first of a rank is the lowest
		if old, ok := best[k]; !ok || (old.ranged && !ranged) {
			best[k] = pick{ref, ranged}
		}
	}
	for rows.Next() {
		var ref EpisodeRef
		var path string
		if err := rows.Scan(&ref.SeriesID, &ref.Season, &ref.Episode, &ref.ItemID, &path); err != nil {
			return nil, err
		}
		put(ref, false)
		for _, e := range rangeAfter(path, ref.Season, ref.Episode) {
			covered := ref
			covered.Episode = e
			put(covered, true)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]EpisodeRef, 0, len(best))
	for _, p := range best {
		out = append(out, p.ref)
	}
	slices.SortFunc(out, func(a, b EpisodeRef) int {
		return cmp.Or(cmp.Compare(a.Season, b.Season), cmp.Compare(a.Episode, b.Episode))
	})
	return out, nil
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
