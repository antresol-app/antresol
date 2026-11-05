package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	CookieName = "vault_session"
	sessionTTL = 400 * 24 * time.Hour

	lastSeenThrottle = 5 * time.Minute
)

type Sessions struct {
	db   *sql.DB
	seen touches // last_seen writes, off the request
}

func NewSessions(db *sql.DB) *Sessions { return &Sessions{db: db} }

func (s *Sessions) Create(ctx context.Context, userID int64) (string, error) {
	return s.CreateBrowser(ctx, userID, "", "")
}

// CreateBrowser is a browser's cookie session with what «Устройства» calls
// it: the browser («Safari») and its system («iPhone»), "" when unknown.
func (s *Sessions) CreateBrowser(ctx context.Context, userID int64, browser, system string) (string, error) {
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions(token, user_id, device_name, device_model) VALUES(?,?,?,?)`, TokenHash(tok), userID, browser, system)
	return tok, err
}

// Session kinds: a browser's cookie session, or a native app's device token.
const (
	KindWeb    = "web"
	KindNative = "native"
)

// DeviceInfo describes the device a native token was issued to.
type DeviceInfo struct {
	ID, Name, Model, Version string
}

// Session is one row of sessions; ID is its rowid.
type Session struct {
	ID                  int64
	UserID              int64
	Kind                string
	Device              DeviceInfo
	CreatedAt, LastSeen time.Time
}

// TokenHash is what sessions.token holds for a session token: the hex
// sha256 of it, so a copy of the database signs nobody in.
func TokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateDevice issues a native token for the device; an earlier token of the
// same device for this profile ends (signing in again replaces it).
func (s *Sessions) CreateDevice(ctx context.Context, userID int64, d DeviceInfo) (string, error) {
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND kind=? AND device_id=?`, userID, KindNative, d.ID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(token, user_id, kind, device_id, device_name, device_model, app_version) VALUES(?,?,?,?,?,?,?)`,
		TokenHash(tok), userID, KindNative, d.ID, d.Name, d.Model, d.Version); err != nil {
		return "", err
	}
	return tok, tx.Commit()
}

func (s *Sessions) Lookup(ctx context.Context, token string) (User, error) {
	u, _, err := s.LookupSession(ctx, token)
	return u, err
}

const sessionCols = `rowid, user_id, kind, device_id, device_name, device_model, app_version, created_at, last_seen`

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var ss Session
	var created, seen string
	if err := row.Scan(&ss.ID, &ss.UserID, &ss.Kind, &ss.Device.ID, &ss.Device.Name, &ss.Device.Model, &ss.Device.Version, &created, &seen); err != nil {
		return Session{}, err
	}
	var err error
	if ss.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
		return Session{}, fmt.Errorf("auth: bad created_at %q: %w", created, err)
	}
	if ss.LastSeen, err = time.Parse(time.RFC3339, seen); err != nil {
		return Session{}, fmt.Errorf("auth: bad last_seen %q: %w", seen, err)
	}
	return ss, nil
}

// LookupSession returns the token's profile and session. Sessions unused
// for sessionTTL end; last_seen is updated at most every lastSeenThrottle.
func (s *Sessions) LookupSession(ctx context.Context, token string) (User, Session, error) {
	if token == "" {
		return User{}, Session{}, ErrNotFound
	}
	ss, err := scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE token=?`, TokenHash(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, Session{}, ErrNotFound
	}
	if err != nil {
		return User{}, Session{}, err
	}
	age := time.Since(ss.LastSeen)
	if age > sessionTTL {
		if err := s.Revoke(ctx, token); err != nil {
			return User{}, Session{}, err
		}
		return User{}, Session{}, ErrNotFound
	}
	u, err := scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id=?`, ss.UserID))
	if err != nil {
		return User{}, Session{}, err
	}
	if age > lastSeenThrottle {
		h := TokenHash(token)
		s.seen.do(h, func(ctx context.Context) error {
			_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE token=?`, h)
			return err
		})
		ss.LastSeen = time.Now().UTC().Truncate(time.Second)
	}
	return u, ss, nil
}

// Devices lists the profile's sessions, the native apps' and the
// browsers', most recently used first.
func (s *Sessions) Devices(ctx context.Context, userID int64) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE user_id=? AND kind IN (?,?) ORDER BY last_seen DESC, rowid DESC`, userID, KindNative, KindWeb)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		ss, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// FamilyDevice is a device of any profile, with the profile's name and
// avatar (an admin's «Устройства»).
type FamilyDevice struct {
	Session
	UserName, UserAvatar string
}

// AllDevices: every profile's apps and browsers, the most recent first.
func (s *Sessions) AllDevices(ctx context.Context) ([]FamilyDevice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.rowid, s.user_id, s.kind, s.device_id, s.device_name, s.device_model, s.app_version, s.created_at, s.last_seen, u.name, u.avatar
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.kind IN (?,?) ORDER BY s.last_seen DESC, s.rowid DESC`, KindNative, KindWeb)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FamilyDevice{}
	for rows.Next() {
		var d FamilyDevice
		ss, err := scanSession(scanFn(func(dest ...any) error {
			return rows.Scan(append(dest, &d.UserName, &d.UserAvatar)...)
		}))
		if err != nil {
			return nil, err
		}
		d.Session = ss
		out = append(out, d)
	}
	return out, rows.Err()
}

type scanFn func(dest ...any) error

func (f scanFn) Scan(dest ...any) error { return f(dest...) }

// RevokeAnyDevice ends a session of any profile (an admin's «Выйти на
// устройстве»); ErrNotFound when there is none.
func (s *Sessions) RevokeAnyDevice(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE rowid=? AND kind IN (?,?)`, id, KindNative, KindWeb)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeDevice ends one of the profile's sessions (an app's or a
// browser's) by rowid, and with it the media keys it asked for;
// ErrNotFound for other profiles' rows.
func (s *Sessions) RevokeDevice(ctx context.Context, userID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE rowid=? AND user_id=? AND kind IN (?,?)`, id, userID, KindNative, KindWeb)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Sessions) Revoke(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token=?`, TokenHash(token))
	return err
}

// RevokeUser ends every session of userID except keep (the caller's own;
// "" ends all of them).
func (s *Sessions) RevokeUser(ctx context.Context, userID int64, keep string) error {
	if keep == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND token<>?`, userID, TokenHash(keep))
	return err
}

func secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// SetCookie writes the session cookie.
func SetCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/",
		MaxAge: int(sessionTTL / time.Second), HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: secure(r),
	})
}

func ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: secure(r),
	})
}
