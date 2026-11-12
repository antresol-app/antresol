package iptv

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/antresol-app/antresol/internal/auth"
)

// Sealer encrypts a source's secrets (playlist URLs, Xtream passwords)
// with the server's key (AES-256-GCM, a random nonce per value).
type Sealer struct{ key []byte }

// NewSealer loads (or makes, once) the server's key, settings
// "iptv_secret_key".
func NewSealer(ctx context.Context, db *sql.DB) (*Sealer, error) {
	v, err := auth.LoadOrCreateSetting(ctx, db, "iptv_secret_key", 32)
	if err != nil {
		return nil, fmt.Errorf("iptv key: %w", err)
	}
	key, err := hex.DecodeString(v)
	if err != nil || len(key) != 32 {
		return nil, errors.New("iptv key: bad iptv_secret_key setting")
	}
	return &Sealer{key: key}, nil
}

func (s *Sealer) aead() (cipher.AEAD, error) {
	b, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// Seal encrypts plain ("" seals to nil).
func (s *Sealer) Seal(plain string) ([]byte, error) {
	if plain == "" {
		return nil, nil
	}
	g, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, []byte(plain), nil), nil
}

// ErrSealed: a sealed value does not open with the server's key.
var ErrSealed = errors.New("iptv: sealed value does not open")

// Open decrypts what Seal made (nil opens to "").
func (s *Sealer) Open(b []byte) (string, error) {
	if len(b) == 0 {
		return "", nil
	}
	g, err := s.aead()
	if err != nil {
		return "", err
	}
	if len(b) < g.NonceSize() {
		return "", ErrSealed
	}
	p, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], nil)
	if err != nil {
		return "", ErrSealed
	}
	return string(p), nil
}

// Mask shows only a URL's scheme and host: "http://example.test:8080/…***"
// (no user info, path or query). What does not parse is "***".
func Mask(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "***"
	}
	return u.Scheme + "://" + u.Host + "/…***"
}

// credKeys are query parameters that carry credentials.
var credKeys = map[string]bool{"username": true, "user": true, "login": true, "password": true, "pass": true, "token": true, "key": true}

// xtreamPaths are the Xtream stream paths /<kind>/<user>/<pass>/<id>.
var xtreamPaths = map[string]bool{"live": true, "movie": true, "series": true, "timeshift": true}

// StripCreds removes what may be credentials from a URL: the user info,
// the query parameters username|user|login|password|pass|token|key, and
// the user and password segments of an Xtream path (/live/<u>/<p>/<id> →
// /live/<id>). It is the URL part of a member's Ident and what logs and
// errors show. What does not parse is "<invalid url>".
func StripCreds(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "<invalid url>"
	}
	u.User = nil
	u.Fragment, u.RawFragment = "", ""
	segs := strings.Split(u.Path, "/")
	// "", kind, user, pass, id...
	if len(segs) >= 5 && segs[0] == "" && xtreamPaths[strings.ToLower(segs[1])] {
		segs = append(segs[:2], segs[4:]...)
		u.Path = strings.Join(segs, "/")
		u.RawPath = ""
	}
	if u.RawQuery != "" {
		var keep []string
		for _, kv := range strings.Split(u.RawQuery, "&") {
			k := kv
			if i := strings.IndexByte(kv, '='); i >= 0 {
				k = kv[:i]
			}
			if uk, err := url.QueryUnescape(k); err == nil {
				k = uk
			}
			if kv == "" || credKeys[strings.ToLower(k)] {
				continue
			}
			keep = append(keep, kv)
		}
		u.RawQuery = strings.Join(keep, "&")
		u.ForceQuery = false
	}
	return u.String()
}
