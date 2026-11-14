package myshows

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrFind marks a failed TMDB lookup (not a miss): the show is tried again
// after findRetry, not on every pull.
var ErrFind = errors.New("myshows: TMDB lookup failed")

// findRetry is how long a failed TMDB lookup is not repeated.
const findRetry = 6 * time.Hour

// missRetry is how long a known miss is trusted before asking again.
const missRetry = 30 * 24 * time.Hour

// ShowLookup is the part of Client the Mapper uses.
type ShowLookup interface {
	Show(ctx context.Context, id int64) (ShowInfo, error)
}

// FindFunc looks an IMDb id ("tt…") up on TMDB: tv first, then movie. kind is
// the library kind of the hit; ok false = TMDB does not know it.
type FindFunc func(ctx context.Context, imdb string) (tmdbID int64, kind string, ok bool, err error)

// Mapper resolves MyShows show ids to TMDB titles through IMDb ids, caching
// hits and misses in myshows_map.
type Mapper struct {
	DB     *sql.DB
	Client ShowLookup
	Find   FindFunc
	Now    func() time.Time // default time.Now

	mu     sync.Mutex
	failed map[int64]time.Time // show id → when its TMDB lookup last failed
}

func (m *Mapper) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Resolve maps a show. Cache first (hits forever, misses for 30 days); else
// Show → IMDb → Find, and the outcome is stored (a show MyShows no longer
// knows is a miss). Lookup errors are returned and not stored; a failed TMDB lookup is
// remembered in memory for 6 hours so a TMDB outage is not re-asked every pull.
func (m *Mapper) Resolve(ctx context.Context, showID int64) (tmdbID int64, kind string, ok bool, err error) {
	var cachedAt int64
	err = m.DB.QueryRowContext(ctx, `SELECT tmdb_id, kind, cached_at FROM myshows_map WHERE show_id = ?`, showID).Scan(&tmdbID, &kind, &cachedAt)
	switch {
	case err == nil:
		if tmdbID != 0 {
			return tmdbID, kind, true, nil
		}
		if m.now().Sub(time.Unix(cachedAt, 0)) < missRetry {
			return 0, "", false, nil
		}
	case !errors.Is(err, sql.ErrNoRows):
		return 0, "", false, err
	}
	m.mu.Lock()
	failedAt, wasFailed := m.failed[showID]
	m.mu.Unlock()
	if wasFailed && m.now().Sub(failedAt) < findRetry {
		return 0, "", false, fmt.Errorf("%w (retry later)", ErrFind)
	}
	tmdbID, kind = 0, ""
	info, err := m.Client.Show(ctx, showID)
	if errors.Is(err, ErrNotFound) {
		// a deleted show: a miss like one without an IMDb id
		info, err = ShowInfo{ID: showID}, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	imdb := ""
	if info.IMDbID > 0 {
		imdb = fmt.Sprintf("tt%07d", info.IMDbID)
		id, k, found, ferr := m.Find(ctx, imdb)
		if ferr != nil {
			if ctx.Err() != nil {
				return 0, "", false, ferr
			}
			m.mu.Lock()
			if m.failed == nil {
				m.failed = map[int64]time.Time{}
			}
			m.failed[showID] = m.now()
			m.mu.Unlock()
			return 0, "", false, fmt.Errorf("%w: %v", ErrFind, ferr)
		}
		if found {
			tmdbID, kind = id, k
		}
	}
	if _, err := m.DB.ExecContext(ctx, `INSERT INTO myshows_map(show_id, imdb_id, tmdb_id, kind, cached_at) VALUES(?,?,?,?,?)
		ON CONFLICT(show_id) DO UPDATE SET imdb_id=excluded.imdb_id, tmdb_id=excluded.tmdb_id, kind=excluded.kind, cached_at=excluded.cached_at`,
		showID, imdb, tmdbID, kind, m.now().Unix()); err != nil {
		return 0, "", false, err
	}
	m.mu.Lock()
	delete(m.failed, showID)
	m.mu.Unlock()
	return tmdbID, kind, tmdbID != 0, nil
}
