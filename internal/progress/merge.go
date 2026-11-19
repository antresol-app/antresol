package progress

import (
	"context"
	"database/sql"
)

// MergeInto is «Перенести в мой профиль»: the history of from (a device's
// managed account) moves into to (its person). A position or mark of from
// wins where it is newer than to's row and to's «снято» tombstone; a newer
// tombstone of from clears to's older row; played time adds up; music
// plays move. from keeps nothing of it. No OnWatchedChange: a merge posts
// nothing to linked accounts.
func (s *Store) MergeInto(ctx context.Context, from, to int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := mergeInto(ctx, tx, from, to); err != nil {
		return err
	}
	return tx.Commit()
}

// MergeAndDelete is MergeInto and then the deletion of the managed
// account from (of the person to; ErrNotFound for any other, and nothing
// moves) in the same transaction: a write that lands in the account
// before it is gone is merged, none lands in it after. keepInUse keeps
// the account (deleted false) while a device still writes into it, or an
// app may (the account has an app password: a TV's Jellyfin or Subsonic
// player signs in with it, and deleting the account would sign it out). The
// caller switches the devices off first, so that their next write is the
// person's.
func (s *Store) MergeAndDelete(ctx context.Context, from, to int64, keepInUse bool) (deleted bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND owner_id=?`, from, to).Scan(&n); err != nil {
		return false, err
	}
	if n == 0 {
		return false, ErrNotFound
	}
	if err := mergeInto(ctx, tx, from, to); err != nil {
		return false, err
	}
	var using int
	if keepInUse {
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM device_history WHERE managed_id=?1 AND enabled=1)
		  + (SELECT COUNT(*) FROM users WHERE id=?1 AND app_password IS NOT NULL AND app_password<>'')`, from).Scan(&using); err != nil {
			return false, err
		}
	}
	if using == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=? AND owner_id=?`, from, to); err != nil {
			return false, err
		}
		deleted = true
	}
	return deleted, tx.Commit()
}

func mergeInto(ctx context.Context, tx *sql.Tx, from, to int64) error {
	steps := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO progress(user_id,item_id,position_ms,duration_ms,finished,updated_at,locator)
		  SELECT ?, f.item_id, f.position_ms, f.duration_ms, f.finished, f.updated_at, f.locator FROM progress f
		  WHERE f.user_id=? AND NOT EXISTS (SELECT 1 FROM progress_cleared c WHERE c.user_id=? AND c.item_id=f.item_id AND c.cleared_at>=f.updated_at)
		  ON CONFLICT(user_id,item_id) DO UPDATE SET position_ms=excluded.position_ms, duration_ms=excluded.duration_ms,
		  finished=excluded.finished, updated_at=excluded.updated_at, locator=excluded.locator
		  WHERE progress.updated_at < excluded.updated_at`, []any{to, from, to}},
		{`DELETE FROM progress WHERE user_id=? AND EXISTS (SELECT 1 FROM progress_cleared c
		  WHERE c.user_id=? AND c.item_id=progress.item_id AND c.cleared_at>=progress.updated_at)`, []any{to, from}},
		{`INSERT INTO progress_cleared(user_id,item_id,cleared_at) SELECT ?, item_id, cleared_at FROM progress_cleared WHERE user_id=? AND true
		  ON CONFLICT(user_id,item_id) DO UPDATE SET cleared_at=excluded.cleared_at WHERE progress_cleared.cleared_at < excluded.cleared_at`, []any{to, from}},
		{`INSERT INTO watch_time(user_id,item_id,day,ms) SELECT ?, item_id, day, ms FROM watch_time WHERE user_id=? AND true
		  ON CONFLICT(user_id,item_id,day) DO UPDATE SET ms=watch_time.ms+excluded.ms`, []any{to, from}},
		{`UPDATE plays SET user_id=? WHERE user_id=?`, []any{to, from}},
		{`DELETE FROM progress WHERE user_id=?`, []any{from}},
		{`DELETE FROM progress_cleared WHERE user_id=?`, []any{from}},
		{`DELETE FROM watch_time WHERE user_id=?`, []any{from}},
	}
	for _, st := range steps {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			return err
		}
	}
	return nil
}
