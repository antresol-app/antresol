package channels

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Deleted generated channels (channel_removed). The generator makes a
// channel again whenever the library has enough for its key, so deleting
// one is remembered by that key and the generator skips it until the owner
// gives it back.

// ErrRemoved: a generated channel's key was deleted by its owner; Create
// does not make it again (the generator skips it).
var ErrRemoved = errors.New("channels: generated channel deleted")

// Removed is a deleted generated channel: what the «Удалённые каналы» list
// shows and what the generator keys it by.
type Removed struct {
	ID       int64  `json:"id"`
	AutoKey  string `json:"autoKey"`
	Name     string `json:"name"`
	Category string `json:"category"`
	// Edited: the name was typed by hand (Display shows it as typed).
	Edited bool `json:"-"`
	// UserID: 0 = a shared channel, else the profile whose it was.
	UserID    int64     `json:"userId,omitempty"`
	RemovedAt time.Time `json:"-"`
}

// Channel is rm as a channel row, for Display.
func (rm Removed) Channel() Channel {
	return Channel{Name: rm.Name, Category: rm.Category, AutoKey: rm.AutoKey, Edited: rm.Edited, UserID: rm.UserID}
}

// CanRestore: an admin gives back shared channels and its own, a profile its
// own (the manage list shows no more than that).
func CanRestore(rm Removed, v Viewer) bool {
	return CanEdit(rm.Channel(), v) && (rm.UserID == 0 || rm.UserID == v.ID)
}

// Removed lists the deleted generated channels, the latest deleted first.
func (s *Store) Removed(ctx context.Context) ([]Removed, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, auto_key, name, category, edited, COALESCE(user_id,0), removed_at FROM channel_removed ORDER BY removed_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Removed
	for rows.Next() {
		var r Removed
		var at string
		if err := rows.Scan(&r.ID, &r.AutoKey, &r.Name, &r.Category, &r.Edited, &r.UserID, &at); err != nil {
			return nil, err
		}
		r.RemovedAt, _ = time.Parse(time.RFC3339, at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RemovedKeys is the set of deleted generated channels' keys.
func (s *Store) RemovedKeys(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT auto_key FROM channel_removed`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// GetRemoved returns one deleted generated channel.
func (s *Store) GetRemoved(ctx context.Context, id int64) (Removed, error) {
	var r Removed
	var at string
	err := s.DB.QueryRowContext(ctx, `SELECT id, auto_key, name, category, edited, COALESCE(user_id,0), removed_at FROM channel_removed WHERE id=?`, id).
		Scan(&r.ID, &r.AutoKey, &r.Name, &r.Category, &r.Edited, &r.UserID, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.RemovedAt, _ = time.Parse(time.RFC3339, at)
	return r, nil
}

// Restore lets the generator make a deleted channel again: its key is
// forgotten, nothing is created here (Generator.Sync makes it from the
// library as it is now, when the library has enough for it).
func (s *Store) Restore(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM channel_removed WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// remove records c's key as deleted, inside tx.
func (s *Store) remove(ctx context.Context, tx *sql.Tx, c Channel) error {
	var user any
	if c.UserID != 0 {
		user = c.UserID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO channel_removed (auto_key, name, category, edited, user_id, removed_at) VALUES (?,?,?,?,?,?)
	  ON CONFLICT (auto_key) DO UPDATE SET name=excluded.name, category=excluded.category, edited=excluded.edited, user_id=excluded.user_id, removed_at=excluded.removed_at`,
		c.AutoKey, c.Name, c.Category, c.Edited, user, s.now().UTC().Format(time.RFC3339))
	return err
}
