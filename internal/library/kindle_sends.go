package library

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MarkSentToKindle records that the profile sent the book to address.
func (r *Repo) MarkSentToKindle(ctx context.Context, userID, itemID int64, address string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO kindle_sends (user_id, item_id, address, sent_at) VALUES (?, ?, ?, ?)
	  ON CONFLICT(user_id, item_id, address) DO UPDATE SET sent_at=excluded.sent_at`, userID, itemID, address, time.Now().Unix())
	return err
}

// SentToKindle reports whether the profile has sent the book to address.
func (r *Repo) SentToKindle(ctx context.Context, userID, itemID int64, address string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM kindle_sends WHERE user_id=? AND item_id=? AND address=?`, userID, itemID, address).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
