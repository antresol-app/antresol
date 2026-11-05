package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
)

// LoadOrCreateSetting returns the settings value of key, first storing n
// random bytes (hex) there when it has none. Concurrent callers and
// restarts all get the one stored value.
func LoadOrCreateSetting(ctx context.Context, db *sql.DB, key string, n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING`, key, hex.EncodeToString(b)); err != nil {
		return "", err
	}
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return v, err
}
