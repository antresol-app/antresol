package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// appPasswordAlphabet: lowercase Latin letters and digits (easy to type on
// a phone).
const appPasswordAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789" // gitleaks:allow

// newAppPassword returns xxxx-xxxx-xxxx-xxxx from crypto/rand.
func newAppPassword() (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for i := 0; i < 16; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		for {
			if _, err := rand.Read(buf); err != nil {
				return "", err
			}
			// 252 = 7*36: reject the tail so every symbol is equally likely
			if buf[0] < 252 {
				b.WriteByte(appPasswordAlphabet[int(buf[0])%len(appPasswordAlphabet)])
				break
			}
		}
	}
	return b.String(), nil
}

// SetAppPassword generates a new app password for the profile (the old one
// stops working, and so do the app tokens got with it) and returns it.
func (s *Store) SetAppPassword(ctx context.Context, id int64) (string, error) {
	p, err := newAppPassword()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET app_password=?, app_password_created_at=? WHERE id=?`, p, now, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	// tokens got with the old app password (Jellyfin) end with it
	if err := revokeAppTokens(ctx, tx, id); err != nil {
		return "", err
	}
	return p, tx.Commit()
}

// AppPassword returns the profile's app password; ok is false when none is set.
func (s *Store) AppPassword(ctx context.Context, id int64) (string, bool, error) {
	var p sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT app_password FROM users WHERE id=?`, id).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, err
	}
	return p.String, p.Valid && p.String != "", nil
}

// AppPasswordInfo tells whether the profile has an app password and when it
// was made (zero when unknown or none), without the password itself: what
// the web UI may see after creation.
func (s *Store) AppPasswordInfo(ctx context.Context, id int64) (bool, time.Time, error) {
	var p, at sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT app_password, app_password_created_at FROM users WHERE id=?`, id).Scan(&p, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return false, time.Time{}, ErrNotFound
	}
	if err != nil {
		return false, time.Time{}, err
	}
	if !p.Valid || p.String == "" {
		return false, time.Time{}, nil
	}
	var created time.Time
	if at.Valid {
		created, _ = time.Parse(time.RFC3339, at.String)
	}
	return true, created, nil
}

// ClearAppPassword removes the profile's app password and ends the app
// tokens got with it.
func (s *Store) ClearAppPassword(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET app_password=NULL, app_password_created_at=NULL WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := revokeAppTokens(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ByName finds a profile by name, ignoring case (Unicode: SQLite NOCASE
// does not fold Cyrillic) and surrounding spaces. Names are unique only
// byte for byte, so «Саша» and «саша» can both exist: more than one match
// is ErrNotFound (a Subsonic login cannot tell them apart).
func (s *Store) ByName(ctx context.Context, name string) (User, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return User{}, ErrNotFound
	}
	list, err := s.List(ctx)
	if err != nil {
		return User{}, err
	}
	var found []User
	for _, u := range list {
		if strings.EqualFold(strings.TrimSpace(u.Name), name) {
			found = append(found, u)
		}
	}
	if len(found) != 1 {
		return User{}, ErrNotFound
	}
	return found[0], nil
}
