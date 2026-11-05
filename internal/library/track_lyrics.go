package library

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Stored lyrics statuses (track_lyrics.status).
const (
	LyricsFound = "found" // Synced and/or Plain hold the words
	LyricsNone  = "none"  // the service has none (asked again after NextTry)
	LyricsError = "error" // the service did not answer (retried with backoff)
)

// LRCSynced matches text with at least one [mm:ss(.xx)] line.
var LRCSynced = regexp.MustCompile(`(?m)^\s*\[\d{1,3}:\d{2}([.:]\d{1,3})?\]`)

// StoredLyrics is a track's words fetched online and kept (track_lyrics).
type StoredLyrics struct {
	Status        string
	Synced, Plain string
	Source        string // "lrclib"
	FetchedAt     time.Time
	NextTry       time.Time
	Attempts      int // failures in a row (status error)
	// OffsetMs: added to every line time of the synced words (positive:
	// later); set by SetLyricsOffset, never by SaveTrackLyrics.
	OffsetMs int
}

// MaxLyricsOffsetMs bounds a track's lyrics offset either way.
const MaxLyricsOffsetMs = 10000

// ClampLyricsOffset keeps ms within ±MaxLyricsOffsetMs.
func ClampLyricsOffset(ms int) int { return max(-MaxLyricsOffsetMs, min(MaxLyricsOffsetMs, ms)) }

// Fresh says the stored answer stands at now: found, or none/error whose
// next try is still ahead.
func (s StoredLyrics) Fresh(now time.Time) bool {
	return s.Status == LyricsFound || now.Before(s.NextTry)
}

// TrackLyrics returns the stored online words of a track (ok false: none
// stored).
func (r *Repo) TrackLyrics(ctx context.Context, itemID int64) (StoredLyrics, bool, error) {
	var s StoredLyrics
	var fetched, next string
	err := r.db.QueryRowContext(ctx, `SELECT status, synced, plain, source, fetched_at, next_try_at, attempts, offset_ms
	  FROM track_lyrics WHERE item_id=?`, itemID).Scan(&s.Status, &s.Synced, &s.Plain, &s.Source, &fetched, &next, &s.Attempts, &s.OffsetMs)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	s.FetchedAt, _ = time.Parse(time.RFC3339, fetched)
	s.NextTry, _ = time.Parse(time.RFC3339, next)
	return s, true, nil
}

// SaveTrackLyrics stores (replaces) a track's online words or the attempt;
// the offset (s.OffsetMs) is left as it is. A track that has gone meanwhile
// is not an error.
func (r *Repo) SaveTrackLyrics(ctx context.Context, itemID int64, s StoredLyrics) error {
	if s.Source == "" {
		s.Source = "lrclib"
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO track_lyrics(item_id,status,synced,plain,source,fetched_at,next_try_at,attempts)
	  SELECT ?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM tracks WHERE item_id=?)
	  ON CONFLICT(item_id) DO UPDATE SET status=excluded.status, synced=excluded.synced, plain=excluded.plain,
	    source=excluded.source, fetched_at=excluded.fetched_at, next_try_at=excluded.next_try_at, attempts=excluded.attempts`,
		itemID, s.Status, strings.TrimSpace(s.Synced), strings.TrimSpace(s.Plain), s.Source,
		s.FetchedAt.UTC().Format(time.RFC3339), s.NextTry.UTC().Format(time.RFC3339), s.Attempts, itemID)
	return err
}

// SetLyricsOffset sets a track's lyrics offset (clamped to
// ±MaxLyricsOffsetMs) and returns what was stored. A track with no row yet
// (its file has synced words, or nothing was asked) gets one that reads as
// «not asked yet»: status error, attempts 0, due at once. ErrNotFound: no
// such track.
func (r *Repo) SetLyricsOffset(ctx context.Context, itemID int64, ms int) (int, error) {
	ms = ClampLyricsOffset(ms)
	epoch := time.Unix(0, 0).UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx, `INSERT INTO track_lyrics(item_id,status,source,fetched_at,next_try_at,attempts,offset_ms)
	  SELECT ?,?,'lrclib',?,?,0,? WHERE EXISTS(SELECT 1 FROM tracks WHERE item_id=?)
	  ON CONFLICT(item_id) DO UPDATE SET offset_ms=excluded.offset_ms`,
		itemID, LyricsError, epoch, epoch, ms, itemID)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return 0, err
	} else if n == 0 {
		return 0, ErrNotFound
	}
	return ms, nil
}

// TrackLyricsStatus returns the stored status of each of ids that has one.
func (r *Repo) TrackLyricsStatus(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	for len(ids) > 0 {
		n := min(len(ids), 500)
		args := make([]any, n)
		for i, id := range ids[:n] {
			args[i] = id
		}
		rows, err := r.db.QueryContext(ctx, `SELECT item_id, status FROM track_lyrics WHERE item_id IN (?`+
			strings.Repeat(",?", n-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var st string
			if err := rows.Scan(&id, &st); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = st
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		ids = ids[n:]
	}
	return out, nil
}

// LyricsCandidates returns up to limit tracks whose words should be looked
// up online at now: the file has no synced words, and nothing is stored or
// the stored answer is due again. Never-asked tracks come first, newest
// first (an album just added before the backlog), then the due retries.
// The SQL filter on synced file words is loose (a line starting with
// «[digit…:dd»); callers check LRCSynced.
func (r *Repo) LyricsCandidates(ctx context.Context, now time.Time, limit int) ([]Track, error) {
	rows, err := r.db.QueryContext(ctx, trackSelect+` LEFT JOIN track_lyrics l ON l.item_id=i.id
	  WHERE i.kind='track' AND i.missing_since IS NULL AND TRIM(i.title)<>''
	    AND t.lyrics NOT GLOB '*[[][0-9]*:[0-9][0-9]*'
	    AND (l.item_id IS NULL OR l.next_try_at<=?)
	  ORDER BY l.item_id IS NOT NULL, CASE WHEN l.item_id IS NULL THEN i.id END DESC, l.next_try_at
	  LIMIT ?`, now.UTC().Format(time.RFC3339), limit)
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
	return out, rows.Err()
}

// EffectiveLyrics picks the words to show for tr: synced words from the
// file first, then synced words stored from the service, then the file's
// plain words, then the stored plain ones. source is "file" or the stored
// source; "" when there are none.
func EffectiveLyrics(tr Track, s StoredLyrics, stored bool) (text, source string) {
	local := strings.TrimSpace(tr.Lyrics)
	if LRCSynced.MatchString(local) {
		return local, "file"
	}
	if stored && s.Status == LyricsFound && s.Synced != "" {
		return s.Synced, s.Source
	}
	if local != "" {
		return local, "file"
	}
	if stored && s.Status == LyricsFound && s.Plain != "" {
		return s.Plain, s.Source
	}
	return "", ""
}
