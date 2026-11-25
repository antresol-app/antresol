package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Querier is what *sql.DB, *sql.Conn and *sql.Tx share: the part of them the
// helpers here need, so a helper runs inside a transaction as well.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// GetSetting reads the settings row of key; found is false (and the error
// nil) when there is none.
func GetSetting(ctx context.Context, q Querier, key string) (value string, found bool, err error) {
	err = q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// PutSetting stores value in the settings row of key, replacing what it held.
func PutSetting(ctx context.Context, q Querier, key, value string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// DeleteSetting removes the settings row of key (none: nothing happens).
func DeleteSetting(ctx context.Context, q Querier, key string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key)
	return err
}

// GetJSON decodes the settings row of key into v, a pointer. v may hold
// defaults: the fields the stored JSON does not have keep their value, and
// without a row (found false) v is not touched. A row that is not valid
// JSON for v is an error naming the key; v may then be half filled.
func GetJSON(ctx context.Context, q Querier, key string, v any) (found bool, err error) {
	raw, found, err := GetSetting(ctx, q, key)
	if err != nil || !found {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return true, fmt.Errorf("settings %s: %w", key, err)
	}
	return true, nil
}

// PutJSON stores v as JSON in the settings row of key.
func PutJSON(ctx context.Context, q Querier, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return PutSetting(ctx, q, key, string(raw))
}
