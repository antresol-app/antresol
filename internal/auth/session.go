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
	// CookieName is the session cookie over plain HTTP (a home network
	// address cannot carry a Secure cookie) and the only name older
	// servers used; HostCookieName is what an HTTPS request gets: the
	// browser then refuses to let a sibling subdomain plant or replace it
	// (Secure, Path=/, no Domain).
	CookieName     = "vault_session"
	HostCookieName = "__Host-vault_session"
	sessionTTL     = 400 * 24 * time.Hour

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
	if err := requirePerson(ctx, s.db, userID); err != nil {
		return "", err
	}
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions(token, user_id, device_name, device_model) VALUES(?,?,?,?)`, TokenHash(tok), userID, browser, system)
	return tok, err
}

// requirePerson is the guard of everything that signs a profile in or
// keeps it signed in: a managed account has no sign-in and is never the
// identity of a request, so a session, a device token or an app token of
// one is refused (ErrNotFound) when it is made and again when it is
// presented, whichever path got there. A user that does not exist passes:
// the foreign key refuses it.
func requirePerson(ctx context.Context, q dbtx, userID int64) error {
	var owner sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT owner_id FROM users WHERE id=?`, userID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if owner.Valid {
		return ErrNotFound
	}
	return nil
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
	// HistoryUserID: the managed account the device's history goes to
	// («Отдельная история на этом устройстве»; 0: the profile's own), with
	// its name.
	HistoryUserID int64
	HistoryName   string
	HistoryUID    string
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
	if err := requirePerson(ctx, tx, userID); err != nil {
		return "", err
	}
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

// sessionColsOf is sessionCols of the table (or alias) t.
func sessionColsOf(t string) string {
	return t + ".rowid, " + t + ".user_id, " + t + ".kind, " + t + ".device_id, " + t + ".device_name, " + t + ".device_model, " + t + ".app_version, " + t + ".created_at, " + t + ".last_seen"
}

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

// sessionLookupSQL is a session, its profile and the managed account its
// device's history goes to (the three of withHistory's answer: 0, "", "" for
// none) in one statement: the one every authenticated request runs. cols
// has no table prefix, so the history account is read by sub-selects (a
// second users in the join would make its names ambiguous).
var sessionLookupSQL = `SELECT ` + sessionColsOf("s") + `, ` + cols + `,
	COALESCE(d.managed_id, 0),
	COALESCE((SELECT ` + NameSQL("h") + ` FROM users h WHERE h.id = d.managed_id), ''),
	COALESCE((SELECT h.uid FROM users h WHERE h.id = d.managed_id), '')
	FROM sessions s JOIN users ON users.id = s.user_id
	LEFT JOIN device_history d ON d.user_id = s.user_id AND d.enabled = 1 AND d.device_key = ` + deviceKeySQL + `
	WHERE s.token = ?`

// scanSessionUser scans a row of sessionColsOf("s"), then cols, then dest.
func scanSessionUser(row scanner, dest ...any) (Session, User, error) {
	var u User
	ss, err := scanSession(scanFn(func(sessionDest ...any) error {
		var err error
		u, err = scan(scanFn(func(userDest ...any) error {
			return row.Scan(append(append(sessionDest, userDest...), dest...)...)
		}))
		return err
	}))
	return ss, u, err
}

// LookupSession returns the token's profile and session. Sessions unused
// for sessionTTL end; last_seen is updated at most every lastSeenThrottle.
func (s *Sessions) LookupSession(ctx context.Context, token string) (User, Session, error) {
	if token == "" {
		return User{}, Session{}, ErrNotFound
	}
	var h Session // the history's three columns
	ss, u, err := scanSessionUser(s.db.QueryRowContext(ctx, sessionLookupSQL, TokenHash(token)), &h.HistoryUserID, &h.HistoryName, &h.HistoryUID)
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
	if u.OwnerID != 0 { // a managed account is never signed in
		return User{}, Session{}, ErrNotFound
	}
	ss.HistoryUserID, ss.HistoryName, ss.HistoryUID = h.HistoryUserID, h.HistoryName, h.HistoryUID
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		if err := s.withHistory(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// FamilyDevice is a device of any profile, with the profile's name and
// avatar (an admin's «Устройства»).
type FamilyDevice struct {
	Session
	UserName, UserAvatar string
	// UserPhoto: the profile's photo (User.Photo; "": none).
	UserPhoto string
}

// AllDevices: every profile's apps and browsers, the most recent first.
func (s *Sessions) AllDevices(ctx context.Context) ([]FamilyDevice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.rowid, s.user_id, s.kind, s.device_id, s.device_name, s.device_model, s.app_version, s.created_at, s.last_seen, u.name, u.avatar, u.photo_version
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.kind IN (?,?) AND u.service=0 ORDER BY s.last_seen DESC, s.rowid DESC`, KindNative, KindWeb)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FamilyDevice{}
	for rows.Next() {
		var d FamilyDevice
		var photo int64
		ss, err := scanSession(scanFn(func(dest ...any) error {
			return rows.Scan(append(dest, &d.UserName, &d.UserAvatar, &photo)...)
		}))
		if err != nil {
			return nil, err
		}
		d.Session = ss
		d.UserPhoto = PhotoURL(ss.UserID, photo)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		if err := s.withHistory(ctx, &out[i].Session); err != nil {
			return nil, err
		}
	}
	return out, nil
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

// CookieToken is the session token r carries in a cookie: the __Host- one
// when it has one, else the plain one (plain HTTP, and a browser that has
// not been renewed since the prefix came).
func CookieToken(r *http.Request) string {
	for _, name := range [...]string{HostCookieName, CookieName} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// SetCookie writes the session cookie: __Host-prefixed and Secure when n
// says the browser came over HTTPS (Network.Secure), else plain. Over HTTPS
// the plain cookie of an older session is removed as it is replaced.
func SetCookie(w http.ResponseWriter, r *http.Request, n *Network, token string) {
	secure := n.Secure(r)
	name := CookieName
	if secure {
		name = HostCookieName
		if _, err := r.Cookie(CookieName); err == nil {
			dropCookie(w, CookieName, true)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: token, Path: "/",
		MaxAge: int(sessionTTL / time.Second), HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: secure,
	})
}

// ClearCookie ends the browser's session cookie, under both names over HTTPS.
func ClearCookie(w http.ResponseWriter, r *http.Request, n *Network) {
	secure := n.Secure(r)
	dropCookie(w, CookieName, secure)
	if secure {
		dropCookie(w, HostCookieName, true)
	}
}

func dropCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: secure,
	})
}
