package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Links of the letters carry a signed token instead of a login:
//
//	v|<user>|<address hash>|<expires>   confirms the address
//	u|<user>|<topic>|<address hash>     turns the topic off
//
// The address hash ties a link to the address it went to: after the
// address changes, old links do nothing.

func addrHash(email string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(h[:6])
}

func (s *Service) sign(payload string) string {
	m := hmac.New(sha256.New, s.Secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

func (s *Service) open(token string) ([]string, error) {
	p, sig, ok := strings.Cut(token, ".")
	if !ok || len(s.Secret) == 0 {
		return nil, ErrBadToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, ErrBadToken
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, ErrBadToken
	}
	m := hmac.New(sha256.New, s.Secret)
	m.Write(payload)
	if !hmac.Equal(got, m.Sum(nil)[:16]) {
		return nil, ErrBadToken
	}
	return strings.Split(string(payload), "|"), nil
}

func (s *Service) verifyToken(uid int64, email string) string {
	exp := s.now().Add(verifyTTL).Unix()
	return s.sign("v|" + strconv.FormatInt(uid, 10) + "|" + addrHash(email) + "|" + strconv.FormatInt(exp, 10))
}

// UnsubscribeToken is the token of the link that turns topic off for uid
// at email.
func (s *Service) UnsubscribeToken(uid int64, topic, email string) string {
	return s.sign("u|" + strconv.FormatInt(uid, 10) + "|" + topic + "|" + addrHash(email))
}

// LinkInfo is what a token says: whose, which topic (unsubscribe) and the
// language of its page.
type LinkInfo struct {
	UserID int64
	Topic  string
	Lang   string
}

// Confirm applies a verification token: the address it went to is the
// profile's confirmed one.
func (s *Service) Confirm(ctx context.Context, token string) (LinkInfo, error) {
	f, err := s.open(token)
	if err != nil || len(f) != 4 || f[0] != "v" {
		return LinkInfo{}, ErrBadToken
	}
	uid, err1 := strconv.ParseInt(f[1], 10, 64)
	exp, err2 := strconv.ParseInt(f[3], 10, 64)
	if err1 != nil || err2 != nil {
		return LinkInfo{}, ErrBadToken
	}
	p, err := s.Prefs(ctx, uid)
	if err != nil {
		return LinkInfo{}, err
	}
	info := LinkInfo{UserID: uid, Lang: p.Language()}
	if s.now().After(time.Unix(exp, 0)) {
		return info, ErrBadToken
	}
	if p.Email == "" || addrHash(p.Email) != f[2] {
		return info, ErrStaleToken
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE notify_prefs SET verified=email WHERE user_id=?`, uid)
	return info, err
}

// CheckUnsubscribe reads an unsubscribe token without applying it (the
// page that asks to confirm).
func (s *Service) CheckUnsubscribe(ctx context.Context, token string) (LinkInfo, error) {
	return s.unsubscribe(ctx, token, false)
}

// Unsubscribe applies an unsubscribe token: its topic is off.
func (s *Service) Unsubscribe(ctx context.Context, token string) (LinkInfo, error) {
	return s.unsubscribe(ctx, token, true)
}

func (s *Service) unsubscribe(ctx context.Context, token string, apply bool) (LinkInfo, error) {
	f, err := s.open(token)
	if err != nil || len(f) != 4 || f[0] != "u" || (f[2] != TopicAll && !slices.Contains(AllTopics, f[2])) {
		return LinkInfo{}, ErrBadToken
	}
	uid, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return LinkInfo{}, ErrBadToken
	}
	p, err := s.Prefs(ctx, uid)
	if err != nil {
		return LinkInfo{}, err
	}
	info := LinkInfo{UserID: uid, Topic: f[2], Lang: p.Language()}
	if p.Email == "" || addrHash(p.Email) != f[3] {
		return info, ErrStaleToken
	}
	if !apply {
		return info, nil
	}
	set := "episodes=0, available=0, requests=0, downloads=0" // TopicAll: the one-click of List-Unsubscribe
	if f[2] == TopicAll {
		for _, t := range AllTopics {
			p.set(t, false)
		}
	} else {
		p.set(f[2], false)
		set = map[string]string{Episodes: "episodes", Available: "available", Requests: "requests", Downloads: "downloads"}[f[2]] + "=0"
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE notify_prefs SET `+set+`, updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE user_id=?`, uid); err != nil {
		return info, err
	}
	return info, s.dropOff(ctx, uid, p)
}
