// Package music stores per-user music state: playlists, starred items and
// the play history (the library itself is internal/library).
package music

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// ErrNotFound: no such playlist for this user (someone else's included).
var ErrNotFound = errors.New("music: not found")

// Store is the playlists/stars/plays repository.
type Store struct {
	db  *sql.DB
	Now func() time.Time // overridable in tests
}

func New(d *sql.DB) *Store { return &Store{db: d, Now: time.Now} }

// Fixed-width so lexicographic order equals chronological order.
const tsLayout = "2006-01-02T15:04:05.000000000Z"

func ts(t time.Time) string { return t.UTC().Format(tsLayout) }

// maxCovers is how many album covers a playlist shows.
const maxCovers = 4

// Playlist is a user's playlist with its summary. Covers are up to four
// different album cover URLs in track order (library.ImageURL);
// CoverAlbums are those albums' ids (Subsonic cover art ids).
type Playlist struct {
	ID          int64
	Name        string
	TrackCount  int
	DurationMs  int64
	Covers      []string
	CoverAlbums []int64
	UpdatedAt   time.Time
}

// PlaylistPatch changes a playlist: Name renames it, Remove drops items by
// position (0-based, of the current list), Append adds tracks at the end;
// Order, when not nil, replaces the whole list (after the others).
type PlaylistPatch struct {
	Name   *string
	Append []int64
	Remove []int
	Order  []int64
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Playlists returns the user's playlists, most recently changed first.
func (s *Store) Playlists(ctx context.Context, userID int64) ([]Playlist, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM playlists WHERE user_id=? ORDER BY updated_at DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Playlist, 0, len(ids))
	for _, id := range ids {
		p, err := s.summary(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// summary loads a playlist row with its counts and covers (no owner check).
func (s *Store) summary(ctx context.Context, x execer, id int64) (Playlist, error) {
	p := Playlist{ID: id, Covers: []string{}, CoverAlbums: []int64{}}
	var updated string
	err := x.QueryRowContext(ctx, `SELECT name, updated_at FROM playlists WHERE id=?`, id).Scan(&p.Name, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if p.UpdatedAt, err = time.Parse(tsLayout, updated); err != nil {
		return p, fmt.Errorf("music: playlist %d updated_at: %w", id, err)
	}
	// visible tracks only (a missing file's track is hidden, not counted)
	rows, err := x.QueryContext(ctx, `SELECT COALESCE(MAX(COALESCE(t.end_ms, f.duration_ms, 0) - t.start_ms, 0), 0),
	  COALESCE(al.id, 0), COALESCE(al.poster, '')
	  FROM playlist_items pi JOIN items i ON i.id=pi.item_id AND i.missing_since IS NULL
	  JOIN tracks t ON t.item_id=i.id JOIN files f ON f.id=t.file_id
	  LEFT JOIN items al ON al.id=i.parent_id
	  WHERE pi.playlist_id=? ORDER BY pi.idx`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	seen := map[int64]bool{}
	for rows.Next() {
		var dur, album int64
		var poster string
		if err := rows.Scan(&dur, &album, &poster); err != nil {
			return p, err
		}
		p.TrackCount++
		p.DurationMs += dur
		if album != 0 && !seen[album] && len(p.Covers) < maxCovers {
			if u := library.ImageURL(album, "poster", poster); u != "" {
				seen[album] = true
				p.Covers = append(p.Covers, u)
				p.CoverAlbums = append(p.CoverAlbums, album)
			}
		}
	}
	return p, rows.Err()
}

// owned checks that playlist id belongs to userID.
func owned(ctx context.Context, x execer, userID, id int64) error {
	var n int
	if err := x.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlists WHERE id=? AND user_id=?`, id, userID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func items(ctx context.Context, x execer, id int64) ([]int64, error) {
	rows, err := x.QueryContext(ctx, `SELECT item_id FROM playlist_items WHERE playlist_id=? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func setItems(ctx context.Context, x execer, id int64, ids []int64) error {
	if _, err := x.ExecContext(ctx, `DELETE FROM playlist_items WHERE playlist_id=?`, id); err != nil {
		return err
	}
	for i, it := range ids {
		if _, err := x.ExecContext(ctx, `INSERT INTO playlist_items(playlist_id,idx,item_id) VALUES(?,?,?)`, id, i, it); err != nil {
			return err
		}
	}
	return nil
}

// Playlist returns one of the user's playlists with its item ids in order
// (as stored: the caller hides tracks that are gone or not visible).
func (s *Store) Playlist(ctx context.Context, userID, id int64) (Playlist, []int64, error) {
	if err := owned(ctx, s.db, userID, id); err != nil {
		return Playlist{}, nil, err
	}
	p, err := s.summary(ctx, s.db, id)
	if err != nil {
		return p, nil, err
	}
	ids, err := items(ctx, s.db, id)
	return p, ids, err
}

// CreatePlaylist makes a playlist of the given tracks.
func (s *Store) CreatePlaylist(ctx context.Context, userID int64, name string, ids []int64) (Playlist, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := ts(s.Now())
		res, err := tx.ExecContext(ctx, `INSERT INTO playlists(user_id,name,created_at,updated_at) VALUES(?,?,?,?)`, userID, name, now, now)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return setItems(ctx, tx, id, ids)
	})
	if err != nil {
		return Playlist{}, err
	}
	return s.summary(ctx, s.db, id)
}

// UpdatePlaylist applies p to one of the user's playlists. Remove
// positions are stored positions (Playlist's ids index).
func (s *Store) UpdatePlaylist(ctx context.Context, userID, id int64, p PlaylistPatch) error {
	var edit func([]int64) ([]int64, error)
	if len(p.Remove) > 0 || len(p.Append) > 0 || p.Order != nil {
		edit = func(cur []int64) ([]int64, error) {
			drop := map[int]bool{}
			for _, i := range p.Remove {
				drop[i] = true
			}
			next := make([]int64, 0, len(cur)+len(p.Append))
			for i, v := range cur {
				if !drop[i] {
					next = append(next, v)
				}
			}
			next = append(next, p.Append...)
			if p.Order != nil {
				next = p.Order
			}
			return next, nil
		}
	}
	return s.EditPlaylist(ctx, userID, id, p.Name, edit)
}

// EditPlaylist renames one of the user's playlists (name != nil) and/or
// replaces its items by edit(current items), in one transaction, so the
// positions edit works on are the stored ones at that moment.
func (s *Store) EditPlaylist(ctx context.Context, userID, id int64, name *string, edit func(cur []int64) ([]int64, error)) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := owned(ctx, tx, userID, id); err != nil {
			return err
		}
		if name != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE playlists SET name=? WHERE id=?`, *name, id); err != nil {
				return err
			}
		}
		if edit != nil {
			cur, err := items(ctx, tx, id)
			if err != nil {
				return err
			}
			next, err := edit(cur)
			if err != nil {
				return err
			}
			if err := setItems(ctx, tx, id, next); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE playlists SET updated_at=? WHERE id=?`, ts(s.Now()), id)
		return err
	})
}

// DeletePlaylist removes one of the user's playlists.
func (s *Store) DeletePlaylist(ctx context.Context, userID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM playlists WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Star marks an item (track, album, artist) as a favourite; idempotent.
func (s *Store) Star(ctx context.Context, userID, itemID int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO stars(user_id,item_id,starred_at) VALUES(?,?,?)`, userID, itemID, ts(s.Now()))
	return err
}

// StarAt stars an item as of at (an import keeps the service's date added;
// zero: now). An item already starred keeps its date.
func (s *Store) StarAt(ctx context.Context, userID, itemID int64, at time.Time) error {
	return s.starAt(ctx, s.db, userID, itemID, at)
}

// StarAtIn is StarAt inside the caller's transaction on the same database
// (the import marks what it starred in the same transaction).
func (s *Store) StarAtIn(ctx context.Context, tx *sql.Tx, userID, itemID int64, at time.Time) error {
	return s.starAt(ctx, tx, userID, itemID, at)
}

func (s *Store) starAt(ctx context.Context, x execer, userID, itemID int64, at time.Time) error {
	if at.IsZero() {
		at = s.Now()
	}
	_, err := x.ExecContext(ctx, `INSERT OR IGNORE INTO stars(user_id,item_id,starred_at) VALUES(?,?,?)`, userID, itemID, ts(at))
	return err
}

func (s *Store) Unstar(ctx context.Context, userID, itemID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM stars WHERE user_id=? AND item_id=?`, userID, itemID)
	return err
}

// Starred returns the user's starred item ids, most recent first.
func (s *Store) Starred(ctx context.Context, userID int64) ([]int64, error) {
	return s.ids(ctx, `SELECT item_id FROM stars WHERE user_id=? ORDER BY starred_at DESC, rowid DESC`, userID)
}

func (s *Store) IsStarred(ctx context.Context, userID, itemID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stars WHERE user_id=? AND item_id=?`, userID, itemID).Scan(&n)
	return n > 0, err
}

// Played records that the user played a track at at.
func (s *Store) Played(ctx context.Context, userID, trackID int64, at time.Time) error {
	if at.IsZero() {
		at = s.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO plays(user_id,item_id,played_at) VALUES(?,?,?)`, userID, trackID, ts(at))
	return err
}

// PlayedAt records a play the client timed itself (the app's offline
// queue): sending it again records nothing new. A time in the future is
// stored as now; at stays the play's identity (client_at), so a retry
// keeps the first stored time.
func (s *Store) PlayedAt(ctx context.Context, userID, trackID int64, at time.Time) error {
	c := ts(at)
	played := at
	if now := s.Now(); played.After(now) {
		played = now
	}
	// played_at=c also matches timed plays stored before client_at existed
	_, err := s.db.ExecContext(ctx, `INSERT INTO plays(user_id,item_id,played_at,client_at) SELECT ?1,?2,?3,?4
	  WHERE NOT EXISTS (SELECT 1 FROM plays WHERE user_id=?1 AND item_id=?2 AND (client_at=?4 OR played_at=?4))`,
		userID, trackID, ts(played), c)
	return err
}

// RecentAlbums returns the albums of the user's plays, each once, by its
// latest play (newest first).
func (s *Store) RecentAlbums(ctx context.Context, userID int64, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = -1
	}
	return s.ids(ctx, `SELECT t.parent_id FROM plays p JOIN items t ON t.id=p.item_id
	  JOIN items al ON al.id=t.parent_id AND al.kind='album' AND al.missing_since IS NULL
	  WHERE p.user_id=? GROUP BY t.parent_id ORDER BY MAX(p.played_at) DESC, MAX(p.rowid) DESC LIMIT ?`, userID, limit)
}

// TopTracks returns an artist's most played tracks (all users), then the
// rest of their albums' tracks in album order.
func (s *Store) TopTracks(ctx context.Context, artistID int64, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = -1
	}
	return s.ids(ctx, `SELECT i.id FROM items al JOIN items i ON i.parent_id=al.id AND i.kind='track' AND i.missing_since IS NULL
	  JOIN tracks t ON t.item_id=i.id
	  LEFT JOIN (SELECT p.item_id, COUNT(*) n FROM plays p JOIN items pi ON pi.id=p.item_id JOIN items pa ON pa.id=pi.parent_id
	      WHERE pa.parent_id=?1 GROUP BY p.item_id) pc ON pc.item_id=i.id
	  WHERE al.parent_id=?1 AND al.kind='album' AND al.missing_since IS NULL
	  ORDER BY COALESCE(pc.n,0) DESC, al.year DESC, al.sort_title, t.disc_no, t.track_no, i.id
	  LIMIT ?2`, artistID, limit)
}

// PlayCount is how often an item was played: a track itself, an album
// through its tracks.
func (s *Store) PlayCount(ctx context.Context, itemID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plays WHERE item_id=?1)
	  + (SELECT COUNT(*) FROM items t JOIN plays p ON p.item_id=t.id WHERE t.parent_id=?1)`, itemID).Scan(&n)
	return n, err
}

func (s *Store) ids(ctx context.Context, q string, args ...any) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
