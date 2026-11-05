package ratings

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/metadata/wikidata"
)

const (
	// rtFresh: Rotten Tomatoes scores found are kept this long; a title
	// Wikidata has none for is asked again after rtMissFresh (new releases
	// get theirs within weeks).
	rtFresh     = 30 * 24 * time.Hour
	rtMissFresh = 7 * 24 * time.Hour
	// rtWait bounds a live Wikidata lookup of one request; a slower one
	// goes on in the background and is cached for the next.
	rtWait    = 4 * time.Second
	rtTimeout = 30 * time.Second
	// rtFailRetry: a lookup that failed is not tried again for this long
	// (Wikidata down or slow must not be asked on every page).
	rtFailRetry = 30 * time.Minute
	// rtLookups caps the Wikidata lookups at once: they share its rate
	// limiter with the book search of «Найти и добавить». A title asked
	// for while they are all busy gets its score on a later view.
	rtLookups = 4
)

// RTScores are a title's Rotten Tomatoes scores in percent.
type RTScores struct {
	Tomatometer int `json:"tomatometer,omitempty"` // critics
	Audience    int `json:"audience,omitempty"`    // the Popcornmeter
}

// Ratings are what is known of a title; nil fields are unknown.
type Ratings struct {
	IMDb           *IMDbRating `json:"imdb,omitempty"`
	RottenTomatoes *RTScores   `json:"rottenTomatoes,omitempty"`
}

// RTSource reads Rotten Tomatoes scores by IMDb id (*wikidata.Client).
type RTSource interface {
	RottenTomatoes(ctx context.Context, imdbID string) (wikidata.ReviewScores, error)
}

// Service combines the IMDb dataset with Rotten Tomatoes scores cached in
// the rt_scores table.
type Service struct {
	IMDb *Dataset // nil: no IMDb ratings
	RT   RTSource // nil: no Rotten Tomatoes scores
	DB   *sql.DB
	Log  *slog.Logger
	now  func() time.Time

	mu       sync.Mutex
	inflight map[string]chan struct{}
	failed   map[string]time.Time // id → last failed lookup
}

var imdbID = regexp.MustCompile(`^tt[0-9]{7,10}$`)

// ValidID reports whether s looks like an IMDb title id ("tt0111161").
func ValidID(s string) bool { return imdbID.MatchString(s) }

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Get returns the ratings of an IMDb title id. Nothing here fails a page:
// a source that is off, down or knows nothing leaves its field nil.
func (s *Service) Get(ctx context.Context, id string) Ratings {
	var out Ratings
	if !ValidID(id) {
		return out
	}
	if s.IMDb != nil {
		if r, ok := s.IMDb.Lookup(id); ok {
			out.IMDb = &r
		}
	}
	if s.RT != nil && s.DB != nil {
		if sc, ok := s.rt(ctx, id); ok && (sc.Tomatometer > 0 || sc.Audience > 0) {
			out.RottenTomatoes = &sc
		}
	}
	return out
}

// rt is the cached score, else a live lookup waited for up to rtWait.
func (s *Service) rt(ctx context.Context, id string) (RTScores, bool) {
	var sc RTScores
	var at int64
	err := s.DB.QueryRowContext(ctx, `SELECT tomatometer, audience, fetched_at FROM rt_scores WHERE imdb_id=?`, id).
		Scan(&sc.Tomatometer, &sc.Audience, &at)
	switch {
	case err == nil:
		fresh := rtFresh
		if sc == (RTScores{}) {
			fresh = rtMissFresh
		}
		if s.clock().Sub(time.Unix(at, 0)) < fresh {
			return sc, true
		}
	case !errors.Is(err, sql.ErrNoRows):
		s.log().Warn("ratings: rt cache", "err", err)
		return RTScores{}, false
	}
	stale, haveStale := sc, err == nil
	done := s.fetch(id)
	t := time.NewTimer(rtWait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		return stale, haveStale
	case <-ctx.Done():
		return stale, haveStale
	}
	err = s.DB.QueryRowContext(ctx, `SELECT tomatometer, audience FROM rt_scores WHERE imdb_id=?`, id).Scan(&sc.Tomatometer, &sc.Audience)
	if err != nil {
		return stale, haveStale // the lookup failed: the old score, if any
	}
	return sc, true
}

// closedCh is the answer of a lookup not started (failed lately, or every
// slot busy).
var closedCh = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

// fetch starts (or joins) the lookup of id; the channel closes when the
// score is in rt_scores or the lookup failed (or was not started).
func (s *Service) fetch(id string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.inflight[id]; ok {
		return ch
	}
	if at, ok := s.failed[id]; ok && s.clock().Sub(at) < rtFailRetry {
		return closedCh
	}
	if len(s.inflight) >= rtLookups {
		return closedCh
	}
	if s.inflight == nil {
		s.inflight = map[string]chan struct{}{}
	}
	ch := make(chan struct{})
	s.inflight[id] = ch
	go func() {
		var failed bool
		defer func() {
			s.mu.Lock()
			delete(s.inflight, id)
			if failed {
				if s.failed == nil || len(s.failed) >= 4096 {
					s.failed = map[string]time.Time{}
				}
				s.failed[id] = s.clock()
			} else {
				delete(s.failed, id)
			}
			s.mu.Unlock()
			close(ch)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), rtTimeout)
		defer cancel()
		r, err := s.RT.RottenTomatoes(ctx, id)
		if err != nil {
			failed = true
			s.log().Warn("ratings: wikidata", "imdb", id, "err", err)
			return
		}
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO rt_scores (imdb_id, tomatometer, audience, fetched_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(imdb_id) DO UPDATE SET tomatometer=excluded.tomatometer, audience=excluded.audience, fetched_at=excluded.fetched_at`,
			id, r.Tomatometer, r.Audience, s.clock().Unix()); err != nil {
			s.log().Warn("ratings: rt cache", "err", err)
		}
	}()
	return ch
}
