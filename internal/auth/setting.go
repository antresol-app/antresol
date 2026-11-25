package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"

	"github.com/antresol-app/antresol/internal/db"
)

// LoadOrCreateSetting returns the settings value of key, first storing n
// random bytes (hex) there when it has none. Concurrent callers and
// restarts all get the one stored value.
func LoadOrCreateSetting(ctx context.Context, conn *sql.DB, key string, n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING`, key, hex.EncodeToString(b)); err != nil {
		return "", err
	}
	v, _, err := db.GetSetting(ctx, conn, key)
	return v, err
}
