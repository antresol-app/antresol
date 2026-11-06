// Package progress stores per-user watch progress and the watchlist, and
// derives "continue watching" and "next episode" from them.
package progress

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// finishedPercent is the share of the duration after which an item counts as watched.
const finishedPercent = 92

// An audiobook counts as heard from this share of it, or this close to its end.
const (
	listenFinishedPercent = 98
	listenFinishedTailMs  = 30_000
)

// Played time. Players report a position every 10 s while playing (web
// useProgress.ts and the audio engine, the app's ProgressReporter), so the
// time played between two reports of an item is their position delta, as
// long as it is no more than the real time between them at the fastest
// speed: a longer jump is a seek and counts only that real time. A
// backward delta (a seek back, a rewatch) counts nothing; the reports after
// it count again. Reports more than sessionGapMs apart belong to two
// sittings (stopped at 0:10 last week, sought to 2:50 today): the jump
// between them counts one report interval at most, like a first report.
// That includes an offline queue's replay (one position per item, so one
// long delta), which reports cannot tell apart from a new sitting. What
// position-only reports cannot tell apart either: a pause followed by a
// seek forward no longer than the pause counts as played.
const (
	reportEveryMs = 10_000
	videoMaxRate  = 2     // the app's speed menu (the web has none)
	listenMaxRate = 3     // audiobook.ts RATES
	creditSlackMs = 5_000 // a late tick, rounding, clocks of two devices
	sessionGapMs  = 3 * reportEveryMs
)

// report is a stored or incoming position with its time.
type report struct {
	posMs int64
	at    time.Time
}

// playedMs is the time played from prev (nil: the item's first report,
// when playback began is unknown: one report interval) to cur.
func playedMs(prev *report, cur report, rate float64) int64 {
	from, elapsed := int64(0), int64(reportEveryMs)
	if prev != nil {
		from, elapsed = prev.posMs, cur.at.Sub(prev.at).Milliseconds()
		if elapsed > sessionGapMs {
			elapsed = reportEveryMs // a new sitting: as a first report
		}
	}
	delta := cur.posMs - from
	if delta <= 0 || elapsed <= 0 {
		return 0
	}
	return min(delta, int64(float64(elapsed)*rate)+creditSlackMs)
}

// ErrNotFound is returned by Get when the user has no progress for the item.
var ErrNotFound = errors.New("progress: not found")

// Entry is one progress row, enriched for rendering a card. SeriesID is set
// for episodes (0 for movies). Synthetic entries (the next unwatched episode)
// have PositionMs 0 unless that episode was partially watched before.
type Entry struct {
	UserID     int64     `json:"user_id"`
	ItemID     int64     `json:"item_id"`
	SeriesID   int64     `json:"series_id,omitempty"`
	PositionMs int64     `json:"position_ms"`
	DurationMs int64     `json:"duration_ms"`
	Finished   bool      `json:"finished"`
	UpdatedAt  time.Time `json:"updated_at"`
	// Locator is the reading position of a book or comic volume (CFI for
	// EPUB/FB2, "page:N" for PDF and comics); PositionMs/DurationMs then
	// hold the read fraction scaled to FractionScale.
	Locator string `json:"locator,omitempty"`
}

// FractionScale is duration_ms of a reading position: position_ms is the
// read fraction times this.
const FractionScale = 1_000_000

// readFinished is the read fraction from which a book counts as read.
const readFinished = 0.98

// Store is the progress/watchlist repository.
type Store struct {
	db  *sql.DB
	Now func() time.Time // overridable in tests
}

func New(d *sql.DB) *Store { return &Store{db: d, Now: time.Now} }

// Fixed-width so lexicographic order equals chronological order.
const tsLayout = "2006-01-02T15:04:05.000000000Z"

func (s *Store) ts() string { return s.Now().UTC().Format(tsLayout) }

func parseTS(v string) (time.Time, error) {
	t, err := time.Parse(tsLayout, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("progress: bad timestamp %q: %w", v, err)
	}
	return t, nil
}

const selectEntry = `SELECT p.user_id, p.item_id, COALESCE(CASE WHEN i.kind='episode' THEN se.parent_id END, 0),
  p.position_ms, p.duration_ms, p.finished, p.updated_at, p.locator
  FROM progress p JOIN items i ON i.id=p.item_id LEFT JOIN items se ON se.id=i.parent_id `

type scanner interface{ Scan(...any) error }

func scanEntry(r scanner) (Entry, error) {
	var e Entry
	var fin int
	var ts string
	err := r.Scan(&e.UserID, &e.ItemID, &e.SeriesID, &e.PositionMs, &e.DurationMs, &fin, &ts, &e.Locator)
	if err != nil {
		return e, err
	}
	e.Finished = fin != 0
	e.UpdatedAt, err = parseTS(ts)
	return e, err
}

// Save records the playback position now; finished is set once position
// reaches 92% of duration.
func (s *Store) Save(ctx context.Context, userID, itemID, posMs, durMs int64) error {
	return s.SaveAt(ctx, userID, itemID, posMs, durMs, time.Time{})
}

// SaveAt records the playback position as of at (zero: now). The write is
// ignored when the stored position is newer than at: an offline queue
// replayed later does not overwrite fresher progress from another device.
// at is clamped to now, so a client clock running ahead cannot block
// later writes. It adds nothing to the played time (imports, sharing):
// a player's report goes through PlayedAt.
func (s *Store) SaveAt(ctx context.Context, userID, itemID, posMs, durMs int64, at time.Time) error {
	return s.saveVideo(ctx, userID, itemID, posMs, durMs, at, 0)
}

// PlayedAt is SaveAt for a position a player reported: it also adds the
// time played since the previous report to watch_time (see playedMs).
func (s *Store) PlayedAt(ctx context.Context, userID, itemID, posMs, durMs int64, at time.Time) error {
	return s.saveVideo(ctx, userID, itemID, posMs, durMs, at, videoMaxRate)
}

func (s *Store) saveVideo(ctx context.Context, userID, itemID, posMs, durMs int64, at time.Time, rate float64) error {
	posMs, durMs = max(posMs, 0), max(durMs, 0)
	fin := 0
	if Watched(posMs, durMs) {
		fin = 1
	}
	return s.upsert(ctx, userID, itemID, posMs, durMs, fin, "", at, rate)
}

// Watched is the video rule of Save: a position counts as watched from
// 92% of a known duration.
func Watched(posMs, durMs int64) bool { return durMs > 0 && posMs*100 >= durMs*finishedPercent }

// SaveListenAt records an audiobook position with the stale-write rule of
// SaveAt. A book of many hours counts as heard only at its real end: from
// 98% of it or within its last 30 seconds (92% would leave an hour unheard).
// Like SaveAt it adds no played time: a player's report goes through
// ListenedAt.
func (s *Store) SaveListenAt(ctx context.Context, userID, itemID, posMs, durMs int64, at time.Time) error {
	return s.saveListen(ctx, userID, itemID, posMs, durMs, at, 0)
}

// ListenedAt is SaveListenAt for a position a player reported, adding the
// time played to watch_time (book time: an hour of the book heard at 2×
// counts an hour, as the position always did).
func (s *Store) ListenedAt(ctx context.Context, userID, itemID, posMs, durMs int64, at time.Time) error {
	return s.saveListen(ctx, userID, itemID, posMs, durMs, at, listenMaxRate)
}

func (s *Store) saveListen(ctx context.Context, userID, itemID, posMs, durMs int64, at time.Time, rate float64) error {
	posMs, durMs = max(posMs, 0), max(durMs, 0)
	fin := 0
	if durMs > 0 && (posMs*100 >= durMs*listenFinishedPercent || durMs-posMs <= listenFinishedTailMs) {
		fin = 1
	}
	return s.upsert(ctx, userID, itemID, posMs, durMs, fin, "", at, rate)
}

// SaveLocator records a reading position (locator) with its read fraction
// (0..1) as of at (zero: now), with the stale-write rule of SaveAt; the
// book counts as read from a fraction of 0.98.
func (s *Store) SaveLocator(ctx context.Context, userID, itemID int64, locator string, fraction float64, at time.Time) error {
	if !(fraction > 0) { // NaN too
		fraction = 0
	}
	fraction = min(fraction, 1)
	fin := 0
	if fraction >= readFinished {
		fin = 1
	}
	return s.upsert(ctx, userID, itemID, int64(math.Round(fraction*FractionScale)), FractionScale, fin, locator, at, 0)
}

// upsertSQL is the stale-write rule: a write older than the stored row, or
// not newer than the item's «unwatched» mark (progress_cleared), changes
// nothing. Arguments: the row (7), then user, item and updated_at again.
const upsertSQL = `INSERT INTO progress(user_id,item_id,position_ms,duration_ms,finished,updated_at,locator)
  SELECT ?,?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM progress_cleared WHERE user_id=? AND item_id=? AND cleared_at>=?)
  ON CONFLICT(user_id,item_id) DO UPDATE SET position_ms=excluded.position_ms, duration_ms=excluded.duration_ms,
  finished=excluded.finished, updated_at=excluded.updated_at, locator=excluded.locator
  WHERE progress.updated_at < excluded.updated_at`

// upsert writes the row with the stale-write rule; rate > 0 also credits
// the time played since the stored report (at most rate × real time).
func (s *Store) upsert(ctx context.Context, userID, itemID, posMs, durMs int64, fin int, locator string, at time.Time, rate float64) error {
	now := s.Now()
	if at.IsZero() || at.After(now) {
		at = now
	}
	ts := at.UTC().Format(tsLayout)
	if rate <= 0 {
		_, err := s.db.ExecContext(ctx, upsertSQL, userID, itemID, posMs, durMs, fin, ts, locator, userID, itemID, ts)
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var prev *report
	var prevPos int64
	var prevTS string
	switch err := tx.QueryRowContext(ctx, `SELECT position_ms, updated_at FROM progress WHERE user_id=? AND item_id=?`, userID, itemID).
		Scan(&prevPos, &prevTS); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		prevAt, err := parseTS(prevTS)
		if err != nil {
			return err
		}
		prev = &report{posMs: prevPos, at: prevAt}
	}
	res, err := tx.ExecContext(ctx, upsertSQL, userID, itemID, posMs, durMs, fin, ts, locator, userID, itemID, ts)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err // a stale write: nothing played since
	}
	if ms := playedMs(prev, report{posMs: posMs, at: at}, rate); ms > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO watch_time(user_id,item_id,day,ms) VALUES(?,?,?,?)
		  ON CONFLICT(user_id,item_id,day) DO UPDATE SET ms=watch_time.ms+excluded.ms`,
			userID, itemID, at.UTC().Format(time.DateOnly), ms); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Get returns the user's progress for an item, or ErrNotFound.
func (s *Store) Get(ctx context.Context, userID, itemID int64) (Entry, error) {
	e, err := scanEntry(s.db.QueryRowContext(ctx, selectEntry+`WHERE p.user_id=? AND p.item_id=?`, userID, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	return e, err
}

// MarkWatched is MarkWatchedAt as of now.
func (s *Store) MarkWatched(ctx context.Context, userID, itemID int64, watched bool) error {
	return s.MarkWatchedAt(ctx, userID, itemID, watched, time.Time{})
}

// MarkWatchedAt marks an item watched (position = duration when known) or
// unwatched as of at (zero or in the future: now). Unwatched deletes the
// progress row and keeps the time as a tombstone (progress_cleared): a
// write that is not newer (an offline queue replayed later, a «finished»
// position too) no longer brings the position or the mark back. Both keep
// the stale-write rule: a mark older than the stored state changes nothing.
func (s *Store) MarkWatchedAt(ctx context.Context, userID, itemID int64, watched bool, at time.Time) error {
	now := s.Now()
	if at.IsZero() || at.After(now) {
		at = now
	}
	ts := at.UTC().Format(tsLayout)
	if watched {
		_, err := s.db.ExecContext(ctx, `INSERT INTO progress(user_id,item_id,position_ms,duration_ms,finished,updated_at)
		  SELECT ?,?,0,0,1,? WHERE NOT EXISTS (SELECT 1 FROM progress_cleared WHERE user_id=? AND item_id=? AND cleared_at>?)
		  ON CONFLICT(user_id,item_id) DO UPDATE SET position_ms=progress.duration_ms, finished=1, updated_at=excluded.updated_at
		  WHERE progress.updated_at <= excluded.updated_at`,
			userID, itemID, ts, userID, itemID, ts)
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM progress WHERE user_id=? AND item_id=? AND updated_at<=?`, userID, itemID, ts); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO progress_cleared(user_id,item_id,cleared_at) VALUES(?,?,?)
	  ON CONFLICT(user_id,item_id) DO UPDATE SET cleared_at=excluded.cleared_at WHERE progress_cleared.cleared_at < excluded.cleared_at`,
		userID, itemID, ts); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearedAt is when the item was last marked unwatched (ok false: never).
func (s *Store) ClearedAt(ctx context.Context, userID, itemID int64) (time.Time, bool, error) {
	var ts string
	err := s.db.QueryRowContext(ctx, `SELECT cleared_at FROM progress_cleared WHERE user_id=? AND item_id=?`, userID, itemID).Scan(&ts)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := parseTS(ts)
	return t, err == nil, err
}

type episode struct {
	id, season, number int64
}

// orderedEpisodes lists a series' episodes: regular seasons first by
// (season, episode), then specials (season 0).
func (s *Store) orderedEpisodes(ctx context.Context, seriesID int64) ([]episode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, COALESCE(e.season_no,0), COALESCE(e.episode_no,0)
	  FROM items e JOIN items se ON se.id=e.parent_id WHERE e.kind='episode' AND se.parent_id=? AND e.missing_since IS NULL`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var eps []episode
	for rows.Next() {
		var e episode
		if err := rows.Scan(&e.id, &e.season, &e.number); err != nil {
			return nil, err
		}
		eps = append(eps, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(eps, func(i, j int) bool {
		a, b := eps[i], eps[j]
		if (a.season == 0) != (b.season == 0) {
			return b.season == 0
		}
		if a.season != b.season {
			return a.season < b.season
		}
		if a.number != b.number {
			return a.number < b.number
		}
		return a.id < b.id
	})
	return eps, nil
}

// NextEpisode picks the episode to watch next in a series: starting from the
// most recently watched one (itself, if unfinished), the first unfinished
// episode after it, wrapping around to earlier unwatched ones. With no
// history it is the first episode. ok is false when everything is watched
// or the series has no episodes.
func (s *Store) NextEpisode(ctx context.Context, userID, seriesID int64) (int64, bool, error) {
	eps, err := s.orderedEpisodes(ctx, seriesID)
	if err != nil || len(eps) == 0 {
		return 0, false, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.item_id, p.finished FROM progress p
	  JOIN items e ON e.id=p.item_id JOIN items se ON se.id=e.parent_id
	  WHERE p.user_id=? AND e.kind='episode' AND se.parent_id=? AND e.missing_since IS NULL ORDER BY p.updated_at DESC, p.rowid DESC`, userID, seriesID)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	finished := map[int64]bool{}
	var last int64
	for rows.Next() {
		var id int64
		var f int
		if err := rows.Scan(&id, &f); err != nil {
			return 0, false, err
		}
		if last == 0 {
			last = id
		}
		finished[id] = f != 0
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	var regular, specials []episode
	for _, e := range eps {
		if e.season == 0 {
			specials = append(specials, e)
		} else {
			regular = append(regular, e)
		}
	}
	start := 0
	for i, e := range regular {
		if e.id == last {
			start = i
			break
		}
	}
	for k := 0; k < len(regular); k++ {
		e := regular[(start+k)%len(regular)]
		if !finished[e.id] {
			return e.id, true, nil
		}
	}
	for _, e := range specials {
		if !finished[e.id] {
			return e.id, true, nil
		}
	}
	return 0, false, nil
}

// SeriesWatched reports whether the user finished every regular episode of
// a series (specials do not count); false for a series with none.
func (s *Store) SeriesWatched(ctx context.Context, userID, seriesID int64) (bool, error) {
	var total, done int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(p.item_id) FROM items e JOIN items se ON se.id=e.parent_id
	  LEFT JOIN progress p ON p.item_id=e.id AND p.user_id=? AND p.finished=1
	  WHERE e.kind='episode' AND se.parent_id=? AND e.missing_since IS NULL AND COALESCE(e.season_no,0)>0`, userID, seriesID).Scan(&total, &done)
	return total > 0 && done == total, err
}

// WatchedOf is, of the movies (or other leaf items) and series ids, the
// ones the user watched: a leaf finished, a series by SeriesWatched's rule.
// One query for each list, whatever their length.
func (s *Store) WatchedOf(ctx context.Context, userID int64, items, series []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(items) > 0 {
		args := []any{userID}
		for _, id := range items {
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT item_id FROM progress WHERE user_id=? AND finished=1 AND item_id IN `+inList(len(items)), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if len(series) > 0 {
		args := []any{userID}
		for _, id := range series {
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT se.parent_id, COUNT(*), COUNT(p.item_id) FROM items e JOIN items se ON se.id=e.parent_id
		  LEFT JOIN progress p ON p.item_id=e.id AND p.user_id=? AND p.finished=1
		  WHERE e.kind='episode' AND se.parent_id IN `+inList(len(series))+` AND e.missing_since IS NULL AND COALESCE(e.season_no,0)>0
		  GROUP BY se.parent_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var total, done int
			if err := rows.Scan(&id, &total, &done); err != nil {
				rows.Close()
				return nil, err
			}
			if total > 0 && done == total {
				out[id] = true
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// InProgress returns the user's unfinished entries of the given kinds
// (e.g. book, comic_volume, audiobook), newest first.
func (s *Store) InProgress(ctx context.Context, userID int64, kinds []string, limit int) ([]Entry, error) {
	return s.entriesOf(ctx, userID, kinds, false, limit)
}

// FinishedOf returns the user's finished entries of the given kinds,
// most recently finished first.
func (s *Store) FinishedOf(ctx context.Context, userID int64, kinds []string, limit int) ([]Entry, error) {
	return s.entriesOf(ctx, userID, kinds, true, limit)
}

func (s *Store) entriesOf(ctx context.Context, userID int64, kinds []string, finished bool, limit int) ([]Entry, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = -1
	}
	args := []any{userID}
	for _, k := range kinds {
		args = append(args, k)
	}
	fin := 0
	if finished {
		fin = 1
	}
	args = append(args, fin, limit)
	rows, err := s.db.QueryContext(ctx, selectEntry+`WHERE p.user_id=? AND i.kind IN (?`+strings.Repeat(",?", len(kinds)-1)+`)
	  AND p.finished=? AND i.missing_since IS NULL ORDER BY p.updated_at DESC, p.rowid DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ContinueWatching (video only) returns unfinished movies and, per series, one episode:
// the last watched one if unfinished, else the next unwatched (position 0
// unless partially watched). Newest first.
func (s *Store) ContinueWatching(ctx context.Context, userID int64, limit int) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, selectEntry+`WHERE p.user_id=? AND i.kind IN ('movie','episode') AND i.missing_since IS NULL ORDER BY p.updated_at DESC, p.rowid DESC`, userID)
	if err != nil {
		return nil, err
	}
	var all []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	byItem := map[int64]Entry{}
	for _, e := range all {
		byItem[e.ItemID] = e
	}
	var out []Entry
	seen := map[int64]bool{}
	for _, e := range all { // newest first
		if limit > 0 && len(out) >= limit {
			break // out is newest first too: the rest would be cut
		}
		if e.SeriesID == 0 {
			if !e.Finished {
				out = append(out, e)
			}
			continue
		}
		if seen[e.SeriesID] {
			continue
		}
		seen[e.SeriesID] = true
		if !e.Finished {
			out = append(out, e)
			continue
		}
		next, ok, err := s.NextEpisode(ctx, userID, e.SeriesID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		n, had := byItem[next]
		if !had {
			n = Entry{UserID: userID, ItemID: next}
		}
		n.SeriesID = e.SeriesID
		n.UpdatedAt = e.UpdatedAt
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// AddToWatchlist is idempotent.
func (s *Store) AddToWatchlist(ctx context.Context, userID, itemID int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO watchlist(user_id,item_id,added_at) VALUES(?,?,?)`, userID, itemID, s.ts())
	return err
}

func (s *Store) RemoveFromWatchlist(ctx context.Context, userID, itemID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM watchlist WHERE user_id=? AND item_id=?`, userID, itemID)
	return err
}

// Watchlist returns item ids, most recently added first.
func (s *Store) Watchlist(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id FROM watchlist WHERE user_id=? ORDER BY added_at DESC, rowid DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// inList is "(?,?,…)" for n values.
func inList(n int) string { return "(?" + strings.Repeat(",?", n-1) + ")" }

// Of returns the users' entries of the items (the family view: other
// profiles' progress on a page), newest first. Missing items are left out.
func (s *Store) Of(ctx context.Context, userIDs, itemIDs []int64) ([]Entry, error) {
	if len(userIDs) == 0 || len(itemIDs) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(userIDs)+len(itemIDs))
	for _, id := range userIDs {
		args = append(args, id)
	}
	for _, id := range itemIDs {
		args = append(args, id)
	}
	return s.list(ctx, selectEntry+`WHERE p.user_id IN `+inList(len(userIDs))+` AND p.item_id IN `+inList(len(itemIDs))+`
	  AND i.missing_since IS NULL ORDER BY p.updated_at DESC, p.rowid DESC`, args...)
}

// StartedBy returns the users' unfinished, started entries of the kinds
// updated since `since` (the Home shelf «Семья смотрит»), newest first.
func (s *Store) StartedBy(ctx context.Context, userIDs []int64, kinds []string, since time.Time, limit int) ([]Entry, error) {
	if len(userIDs) == 0 || len(kinds) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = -1
	}
	args := make([]any, 0, len(userIDs)+len(kinds)+2)
	for _, id := range userIDs {
		args = append(args, id)
	}
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, since.UTC().Format(tsLayout), limit)
	return s.list(ctx, selectEntry+`WHERE p.user_id IN `+inList(len(userIDs))+` AND i.kind IN `+inList(len(kinds))+`
	  AND p.finished=0 AND p.position_ms>0 AND p.updated_at>=? AND i.missing_since IS NULL
	  ORDER BY p.updated_at DESC, p.rowid DESC LIMIT ?`, args...)
}

func (s *Store) list(ctx context.Context, q string, args ...any) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Adopt is «Продолжить с места …»: from's position in an item (a time, or
// a reading locator with its fraction) becomes to's own, as of now. Only
// to's row is written. It adds no played time: the jump is not watching,
// and the next report of to's player counts from here. ok is false when
// from has no unfinished position there (nothing is written).
func (s *Store) Adopt(ctx context.Context, from, to, itemID int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO progress(user_id,item_id,position_ms,duration_ms,finished,updated_at,locator)
	  SELECT ?, item_id, position_ms, duration_ms, 0, ?, locator FROM progress WHERE user_id=? AND item_id=? AND finished=0
	  ON CONFLICT(user_id,item_id) DO UPDATE SET position_ms=excluded.position_ms, duration_ms=excluded.duration_ms,
	  finished=0, updated_at=excluded.updated_at, locator=excluded.locator`, to, s.ts(), from, itemID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
