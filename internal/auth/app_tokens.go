package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Device describes the client app a token was issued to (from the
// Jellyfin MediaBrowser header). ID is the app's own device id.
type Device struct {
	ID, Name, Client, Version string
}

// AppTokens are the access tokens of third-party protocols that log in
// once and then send a token (Jellyfin: AuthenticateByName with the app
// password → AccessToken). A token is worth as much as the app password
// it was got with, so changing or removing the app password or the login
// password, or deleting the profile, ends all of the profile's tokens
// (the Store does it in the same transaction). Like web sessions, a token
// unused for sessionTTL expires.
type AppTokens struct {
	seen touches // last_seen writes, off the request
	db   *sql.DB
	Now  func() time.Time
}

func NewAppTokens(db *sql.DB) *AppTokens { return &AppTokens{db: db, Now: time.Now} }

func (t *AppTokens) now() time.Time {
	if t.Now != nil {
		return t.Now().UTC()
	}
	return time.Now().UTC()
}

// Issue mints a token (16 random bytes, 32 hex) for userID. A previous
// token of the same proto, profile and non-empty device id is replaced:
// an app that logs in again on the same device does not pile up tokens.
func (t *AppTokens) Issue(ctx context.Context, userID int64, proto string, d Device) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	now := t.now().Format(time.RFC3339)
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if d.ID != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_tokens WHERE proto=? AND user_id=? AND device_id=?`, proto, userID, d.ID); err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO app_tokens(token, user_id, proto, device_id, device, client, version, created_at, last_seen) VALUES(?,?,?,?,?,?,?,?,?)`,
		tok, userID, proto, d.ID, d.Name, d.Client, d.Version, now, now); err != nil {
		return "", err
	}
	return tok, tx.Commit()
}

// Lookup returns the token's profile (current state) and device;
// ErrNotFound for an unknown, revoked or expired token. last_seen is
// written at most once per lastSeenThrottle.
func (t *AppTokens) Lookup(ctx context.Context, token string) (User, Device, error) {
	if token == "" {
		return User{}, Device{}, ErrNotFound
	}
	var uid int64
	var seen string
	var d Device
	err := t.db.QueryRowContext(ctx, `SELECT user_id, device_id, device, client, version, last_seen FROM app_tokens WHERE token=?`, token).
		Scan(&uid, &d.ID, &d.Name, &d.Client, &d.Version, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, Device{}, ErrNotFound
	}
	if err != nil {
		return User{}, Device{}, err
	}
	last, err := time.Parse(time.RFC3339, seen)
	if err != nil {
		return User{}, Device{}, fmt.Errorf("auth: bad app token last_seen %q: %w", seen, err)
	}
	now := t.now()
	age := now.Sub(last)
	if age > sessionTTL {
		if err := t.Revoke(ctx, token); err != nil {
			return User{}, Device{}, err
		}
		return User{}, Device{}, ErrNotFound
	}
	u, err := scan(t.db.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id=?`, uid))
	if err != nil {
		return User{}, Device{}, err
	}
	if age > lastSeenThrottle {
		t.seen.do(token, func(ctx context.Context) error {
			_, err := t.db.ExecContext(ctx, `UPDATE app_tokens SET last_seen=? WHERE token=?`, now.Format(time.RFC3339), token)
			return err
		})
	}
	return u, d, nil
}

// Revoke ends one token (logout); an unknown token is not an error.
func (t *AppTokens) Revoke(ctx context.Context, token string) error {
	_, err := t.db.ExecContext(ctx, `DELETE FROM app_tokens WHERE token=?`, token)
	return err
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// revokeAppTokens ends every app token of the profile (inside the caller's
// transaction).
func revokeAppTokens(ctx context.Context, ex execer, userID int64) error {
	_, err := ex.ExecContext(ctx, `DELETE FROM app_tokens WHERE user_id=?`, userID)
	return err
}

// Current returns the profile's token of proto and device id (the newest),
// "" when it has none. Tokens a profile shows itself again (its IPTV link)
// are read back with it.
func (t *AppTokens) Current(ctx context.Context, userID int64, proto, deviceID string) (string, error) {
	var tok string
	err := t.db.QueryRowContext(ctx, `SELECT token FROM app_tokens WHERE user_id=? AND proto=? AND device_id=? ORDER BY created_at DESC LIMIT 1`,
		userID, proto, deviceID).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return tok, err
}

// LookupProto is Lookup for a token of proto only: a token of another
// protocol is ErrNotFound.
func (t *AppTokens) LookupProto(ctx context.Context, token, proto string) (User, error) {
	var p string
	err := t.db.QueryRowContext(ctx, `SELECT proto FROM app_tokens WHERE token=?`, token).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && p != proto) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u, _, err := t.Lookup(ctx, token)
	return u, err
}
