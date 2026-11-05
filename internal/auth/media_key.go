package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LoadOrCreateSecret returns the 32-byte secret kept (hex) in settings
// under key, made on first use.
func LoadOrCreateSecret(ctx context.Context, db *sql.DB, key string) ([]byte, error) {
	v, err := LoadOrCreateSetting(ctx, db, key, 32)
	if err != nil {
		return nil, err
	}
	b, err := hex.DecodeString(v)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("auth: bad secret in settings %q", key)
	}
	return b, nil
}

// MediaKeys are short-lived keys for media URLs (?mk=) that the native
// app hands to AVPlayer and AirPlay, which cannot send its Bearer token,
// and that the web player hands to an AirPlay receiver, which fetches the
// stream without the browser's cookie. A key names a session (rowid) and
// its profile, and its MAC covers the hash of that session's token: when
// the session ends, or its rowid is reused by another row, the key stops
// working.
type MediaKeys struct {
	secret   []byte
	sessions *Sessions
	Now      func() time.Time // overridable in tests
}

func NewMediaKeys(secret []byte, s *Sessions) *MediaKeys {
	return &MediaKeys{secret: secret, sessions: s, Now: time.Now}
}

// mediaKeyMACLen is how many base64url characters of the MAC a key keeps
// (22 characters = 132 bits).
const mediaKeyMACLen = 22

// mac covers the session's token hash (TokenHash, what sessions.token
// holds), so keys minted before migration 0020 stored the hash stay valid.
func (m *MediaKeys) mac(sid, uid, exp int64, tokenHash string) string {
	h := hmac.New(sha256.New, m.secret)
	fmt.Fprintf(h, "v1|%d|%d|%d|%s", sid, uid, exp, tokenHash)
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))[:mediaKeyMACLen]
}

// Issue makes a key for the session sess (whose token is token) valid for
// ttl: v1.<sid>.<uid>.<expUnix>.<mac>.
func (m *MediaKeys) Issue(sess Session, token string, ttl time.Duration) (string, time.Time) {
	exp := m.Now().Add(ttl).Truncate(time.Second)
	e := exp.Unix()
	key := fmt.Sprintf("v1.%d.%d.%d.%s", sess.ID, sess.UserID, e, m.mac(sess.ID, sess.UserID, e, TokenHash(token)))
	return key, exp
}

// Verify returns the profile and session of a key; ErrNotFound when the
// key is malformed, forged, expired, or its session ended.
func (m *MediaKeys) Verify(ctx context.Context, key string) (User, Session, error) {
	parts := strings.Split(key, ".")
	if len(parts) != 5 || parts[0] != "v1" || len(parts[4]) != mediaKeyMACLen {
		return User{}, Session{}, ErrNotFound
	}
	var n [3]int64
	for i := range n {
		v, err := strconv.ParseInt(parts[i+1], 10, 64)
		if err != nil || v <= 0 || strconv.FormatInt(v, 10) != parts[i+1] {
			return User{}, Session{}, ErrNotFound
		}
		n[i] = v
	}
	sid, uid, exp := n[0], n[1], n[2]
	now := m.Now()
	if now.Unix() >= exp {
		return User{}, Session{}, ErrNotFound
	}
	var tokenHash string
	ss, err := scanSessionToken(m.sessions.db.QueryRowContext(ctx, `SELECT token, `+sessionCols+` FROM sessions WHERE rowid=?`, sid), &tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, Session{}, ErrNotFound
	}
	if err != nil {
		return User{}, Session{}, err
	}
	if !hmac.Equal([]byte(m.mac(sid, uid, exp, tokenHash)), []byte(parts[4])) ||
		ss.UserID != uid || (ss.Kind != KindNative && ss.Kind != KindWeb) || now.Sub(ss.LastSeen) > sessionTTL {
		return User{}, Session{}, ErrNotFound
	}
	u, err := scan(m.sessions.db.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id=?`, uid))
	if err != nil {
		return User{}, Session{}, err
	}
	return u, ss, nil
}

// scanSessionToken scans a "token, sessionCols" row.
func scanSessionToken(row scanner, token *string) (Session, error) {
	return scanSession(scanFunc(func(dest ...any) error {
		return row.Scan(append([]any{token}, dest...)...)
	}))
}

type scanFunc func(...any) error

func (f scanFunc) Scan(dest ...any) error { return f(dest...) }
