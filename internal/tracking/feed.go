package tracking

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"
)

// FeedItem is one episode (a movie: season and episode 0) of a profile's
// tracked title. FirstSeen is when its first source appeared (zero: none
// yet); Kinds are the kinds of its sources (library, torrent, ...).
type FeedItem struct {
	Title           Title
	Season, Episode int
	Name            string
	AirDate         string
	FirstSeen       time.Time
	Kinds           []string
}

// WatchedFunc says which episodes of a title a profile has watched
// ([season, episode]; a movie is [0, 0]).
type WatchedFunc func(ctx context.Context, userID int64, t Title) (map[[2]int]bool, error)

// CalendarDay is one air date of a profile's tracked titles; an item whose
// FirstSeen is zero is not available yet.
type CalendarDay struct {
	Date  string
	Items []FeedItem
}

// liveStates are the marks that put a title in a profile's feed.
const liveStates = `('planned','watching','on_hold')`

// interest is the (user_id, title_id) pairs of the profiles following a
// title: a live mark, or a library movie or series on the profile's
// watchlist with no mark of its own (it reads as planned).
const interest = `(SELECT user_id, title_id FROM title_marks WHERE state IN ` + liveStates + `
	UNION
	SELECT w.user_id, tt.id FROM watchlist w JOIN items i ON i.id=w.item_id
	  JOIN tracked_titles tt ON tt.kind=i.kind AND tt.tmdb_id=i.tmdb_id
	  WHERE i.kind IN ('movie','series') AND i.tmdb_id > 0
	    AND NOT EXISTS (SELECT 1 FROM title_marks x WHERE x.user_id=w.user_id AND x.title_id=tt.id))`

// prefixedTitleCols is titleCols of the table aliased t.
var prefixedTitleCols = "t." + strings.ReplaceAll(titleCols, ", ", ", t.")

func scanFeedItem(r scanner) (FeedItem, error) {
	var it FeedItem
	var sh, ms, an sql.NullInt64
	var nc, rf, fs, kinds sql.NullString
	t := &it.Title
	if err := r.Scan(&t.ID, &t.Kind, &t.TMDBID, &sh, &ms, &an, &t.IMDbID, &t.Name, &t.Poster, &t.Status, &t.Cert, &nc, &rf,
		&it.Season, &it.Episode, &it.Name, &it.AirDate, &fs, &kinds); err != nil {
		return FeedItem{}, err
	}
	t.ShikimoriID, t.MyShowsID, t.AniDBID = ptr(sh), ptr(ms), ptr(an)
	t.NextCheck, t.Refreshed = tparse(nc), tparse(rf)
	it.FirstSeen = tparse(fs)
	it.Kinds = []string{}
	if kinds.Valid && kinds.String != "" {
		it.Kinds = strings.Split(kinds.String, ",")
		slices.Sort(it.Kinds)
	}
	return it, nil
}

// Feed lists the episodes of the profile's tracked titles (marks planned,
// watching, on hold; a watchlisted library title without a mark) whose first source appeared at or after since, newest
// first, leaving out what watched (nil: nothing) says the profile has
// watched. An episode of a series that aired more than mailMaxAge before
// since is old news (its source is a scanner's baseline) and is left out.
func (s *Store) Feed(ctx context.Context, userID int64, since time.Time, watched WatchedFunc) ([]FeedItem, error) {
	old := since.Add(-mailMaxAge).UTC().Format(time.DateOnly)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+prefixedTitleCols+`, src.season, src.episode,
		  COALESCE(e.name,''), COALESCE(e.air_date,''), src.first, src.kinds
		FROM `+interest+` m JOIN tracked_titles t ON t.id=m.title_id
		JOIN (SELECT title_id, season, episode, MIN(first_seen) AS first, GROUP_CONCAT(DISTINCT kind) AS kinds
		      FROM episode_sources GROUP BY title_id, season, episode) src ON src.title_id=t.id
		LEFT JOIN title_episodes e ON e.title_id=t.id AND e.season=src.season AND e.number=src.episode
		WHERE m.user_id=? AND src.first >= ?
		  AND NOT (t.kind='series' AND COALESCE(e.air_date,'') <> '' AND e.air_date < ?)
		ORDER BY src.first DESC, t.id, src.season DESC, src.episode DESC`,
		userID, since.UTC().Format(time.RFC3339), old)
	if err != nil {
		return nil, err
	}
	var all []FeedItem
	for rows.Next() {
		it, err := scanFeedItem(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if watched == nil {
		return all, nil
	}
	seen := map[int64]map[[2]int]bool{}
	out := all[:0]
	for _, it := range all {
		w, ok := seen[it.Title.ID]
		if !ok {
			if w, err = watched(ctx, userID, it.Title); err != nil {
				return nil, err
			}
			seen[it.Title.ID] = w
		}
		if !w[[2]int{it.Season, it.Episode}] {
			out = append(out, it)
		}
	}
	return out, nil
}

// Calendar lists, by air date from..to (YYYY-MM-DD, both included), the
// episodes of the profile's tracked titles; only days with episodes.
func (s *Store) Calendar(ctx context.Context, userID int64, from, to string) ([]CalendarDay, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+prefixedTitleCols+`, e.season, e.number, e.name, e.air_date,
		  (SELECT MIN(first_seen) FROM episode_sources x WHERE x.title_id=e.title_id AND x.season=e.season AND x.episode=e.number),
		  (SELECT GROUP_CONCAT(DISTINCT kind) FROM episode_sources x WHERE x.title_id=e.title_id AND x.season=e.season AND x.episode=e.number)
		FROM `+interest+` m JOIN tracked_titles t ON t.id=m.title_id
		JOIN title_episodes e ON e.title_id=t.id
		WHERE m.user_id=? AND e.air_date >= ? AND e.air_date <= ?
		ORDER BY e.air_date, t.id, e.season, e.number`, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarDay
	for rows.Next() {
		it, err := scanFeedItem(rows)
		if err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].Date != it.AirDate {
			out = append(out, CalendarDay{Date: it.AirDate})
		}
		out[len(out)-1].Items = append(out[len(out)-1].Items, it)
	}
	return out, rows.Err()
}

// WatchedBy is the union of the service's Watched functions (nil: none).
func (s *Service) WatchedBy() WatchedFunc {
	if len(s.Watched) == 0 {
		return nil
	}
	return func(ctx context.Context, userID int64, t Title) (map[[2]int]bool, error) {
		out := map[[2]int]bool{}
		for _, w := range s.Watched {
			m, err := w(ctx, userID, t)
			if err != nil {
				return nil, err
			}
			for k, v := range m {
				if v {
					out[k] = true
				}
			}
		}
		return out, nil
	}
}
