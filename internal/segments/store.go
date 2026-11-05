package segments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Store keeps segments, what was looked at per file, and the online
// answers (migration 0045).
type Store struct{ DB *sql.DB }

// Of lists the stored segments of a file (every source).
func (s Store) Of(ctx context.Context, fileID int64) ([]Segment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT kind, source, start_ms, end_ms FROM file_segments WHERE file_id=? ORDER BY start_ms`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Segment
	for rows.Next() {
		var g Segment
		if err := rows.Scan(&g.Kind, &g.Source, &g.StartMs, &g.EndMs); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// replace sets a file's segments of one source.
func (s Store) replace(ctx context.Context, fileID int64, source string, segs []Segment) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM file_segments WHERE file_id=? AND source=?`, fileID, source); err != nil {
		return err
	}
	for _, g := range segs {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO file_segments(file_id, kind, source, start_ms, end_ms) VALUES(?,?,?,?,?)`,
			fileID, g.Kind, source, g.StartMs, g.EndMs); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// status is what has been looked at for a file.
type status struct {
	Size, Mtime int64
	ChaptersV   int
	OnlineKey   string
	OnlineAt    int64 // unix seconds
	AnalysisV   int
	// failures in a row and when (unix seconds) the step is due again
	ChaptersFails, OnlineFails, AnalysisFails int
	ChaptersRetry, OnlineRetry, AnalysisRetry int64
}

func (s Store) saveStatus(ctx context.Context, fileID int64, st status) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO segment_status(file_id, size, mtime, chapters_v, online_key, online_at, analysis_v,
		  chapters_fails, chapters_retry, online_fails, online_retry, analysis_fails, analysis_retry)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(file_id) DO UPDATE SET size=excluded.size, mtime=excluded.mtime,
		chapters_v=excluded.chapters_v, online_key=excluded.online_key, online_at=excluded.online_at, analysis_v=excluded.analysis_v,
		chapters_fails=excluded.chapters_fails, chapters_retry=excluded.chapters_retry, online_fails=excluded.online_fails,
		online_retry=excluded.online_retry, analysis_fails=excluded.analysis_fails, analysis_retry=excluded.analysis_retry`,
		fileID, st.Size, st.Mtime, st.ChaptersV, st.OnlineKey, st.OnlineAt, st.AnalysisV,
		st.ChaptersFails, st.ChaptersRetry, st.OnlineFails, st.OnlineRetry, st.AnalysisFails, st.AnalysisRetry)
	return err
}

// forget drops everything known of a file (it was replaced on disk).
func (s Store) forget(ctx context.Context, fileID int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM file_segments WHERE file_id=?`, fileID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM segment_status WHERE file_id=?`, fileID)
	return err
}

// cached is a stored online answer fresher than OnlineTTL.
func (s Store) cached(ctx context.Context, service, key string, now time.Time) (Answer, bool, error) {
	var at, group int64
	var js string
	err := s.DB.QueryRowContext(ctx, `SELECT fetched_at, segments, group_ms FROM segment_lookups WHERE service=? AND lookup=?`, service, key).
		Scan(&at, &js, &group)
	if errors.Is(err, sql.ErrNoRows) {
		return Answer{}, false, nil
	}
	if err != nil {
		return Answer{}, false, err
	}
	if now.Sub(time.Unix(at, 0)) >= OnlineTTL {
		return Answer{}, false, nil
	}
	a := Answer{GroupMs: group}
	if err := json.Unmarshal([]byte(js), &a.Segments); err != nil {
		return Answer{}, false, nil // unreadable: asked again
	}
	return a, true, nil
}

func (s Store) cache(ctx context.Context, service, key string, a Answer, now time.Time) error {
	segs := a.Segments
	if segs == nil {
		segs = []Segment{}
	}
	js, err := json.Marshal(segs)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO segment_lookups(service, lookup, fetched_at, segments, group_ms) VALUES(?,?,?,?,?)
		ON CONFLICT(service, lookup) DO UPDATE SET fetched_at=excluded.fetched_at, segments=excluded.segments, group_ms=excluded.group_ms`,
		service, key, now.Unix(), string(js), a.GroupMs)
	return err
}

// file is a library video file as the queue sees it.
type file struct {
	ID, Item    int64
	Path        string
	Size, Mtime int64
	DurationMs  int64
	Movie       bool
	Season      int64 // the season item (episodes)
	SeasonNo    int
	EpisodeNo   int
	Show        string // the series' (or film's) title, for the log
	TmdbID      int64  // the series' (or film's)
	IMDb        string
	St          status
	HasStatus   bool
}

// unit is the key the queue works by: a season, or one film.
func (f file) unit() int64 {
	if f.Movie {
		return -f.Item
	}
	return f.Season
}

func (f file) lookup() Lookup {
	return Lookup{TmdbID: f.TmdbID, IMDb: f.IMDb, Season: f.SeasonNo, Episode: f.EpisodeNo, Movie: f.Movie, DurationMs: f.DurationMs}
}

// files lists the probed video files of the library's films and episodes
// that are not missing, with their status.
func (s Store) files(ctx context.Context) ([]file, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT f.id, f.item_id, f.path, f.size, f.mtime, COALESCE(f.duration_ms,0),
		  e.kind='movie', COALESCE(sea.id,0), COALESCE(e.season_no,0), COALESCE(e.episode_no,0),
		  COALESCE(top.title,''), COALESCE(top.tmdb_id,0),
		  CASE WHEN json_valid(top.meta_json) THEN COALESCE(json_extract(top.meta_json,'$.imdb_id'),'') ELSE '' END,
		  st.file_id IS NOT NULL, COALESCE(st.size,0), COALESCE(st.mtime,0), COALESCE(st.chapters_v,0),
		  COALESCE(st.online_key,''), COALESCE(st.online_at,0), COALESCE(st.analysis_v,0),
		  COALESCE(st.chapters_fails,0), COALESCE(st.chapters_retry,0), COALESCE(st.online_fails,0),
		  COALESCE(st.online_retry,0), COALESCE(st.analysis_fails,0), COALESCE(st.analysis_retry,0)
		FROM files f JOIN items e ON e.id=f.item_id
		LEFT JOIN items sea ON e.kind='episode' AND sea.id=e.parent_id
		LEFT JOIN items top ON top.id = CASE WHEN e.kind='episode' THEN sea.parent_id ELSE e.id END
		LEFT JOIN segment_status st ON st.file_id=f.id
		WHERE e.kind IN ('movie','episode') AND f.missing_since IS NULL AND e.missing_since IS NULL
		  AND COALESCE(f.duration_ms,0) > 0
		ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []file
	for rows.Next() {
		var f file
		var imdb any
		if err := rows.Scan(&f.ID, &f.Item, &f.Path, &f.Size, &f.Mtime, &f.DurationMs, &f.Movie, &f.Season, &f.SeasonNo, &f.EpisodeNo,
			&f.Show, &f.TmdbID, &imdb, &f.HasStatus, &f.St.Size, &f.St.Mtime, &f.St.ChaptersV, &f.St.OnlineKey, &f.St.OnlineAt, &f.St.AnalysisV,
			&f.St.ChaptersFails, &f.St.ChaptersRetry, &f.St.OnlineFails, &f.St.OnlineRetry, &f.St.AnalysisFails, &f.St.AnalysisRetry); err != nil {
			return nil, err
		}
		if v, ok := imdb.(string); ok {
			f.IMDb = v
		}
		if !f.Movie && f.Season == 0 {
			continue // an episode outside a season: not grouped
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// unprobed counts the library's video files still waiting for their probe
// (a scan probes new files after it kicks the queue).
func (s Store) unprobed(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM files f JOIN items e ON e.id=f.item_id
		WHERE e.kind IN ('movie','episode') AND f.missing_since IS NULL AND COALESCE(f.duration_ms,0)=0`).Scan(&n)
	return n, err
}

// probeOf is a file's cached probe (its audio tracks).
func (s Store) probeOf(ctx context.Context, fileID int64) (string, error) {
	var js sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT probe_json FROM files WHERE id=?`, fileID).Scan(&js)
	return js.String, err
}
