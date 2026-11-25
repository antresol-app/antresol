package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
)

// Service profiles: a machine's profile (the post-deploy playback check
// signs in as one), not a person's. It signs in like any profile, with its
// password, but it is in no list of people, writes no history (the
// database drops it, see migration 0119) and is created, given a new
// password and removed only by `vault service-profile`: the people's
// profile API refuses it (ErrService).

// servicePasswordBytes: the random bytes of a service profile's password
// (hex: 40 characters, under bcrypt's 72).
const servicePasswordBytes = 20

// ListService lists the service profiles, oldest first.
func (s *Store) ListService(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM users WHERE service=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateService adds a service profile: an adult, not an administrator,
// with a random password, returned in the clear (the only time).
// ErrExists when the name is taken (by a person's profile too).
func (s *Store) CreateService(ctx context.Context, name string) (User, string, error) {
	pw, err := servicePassword()
	if err != nil {
		return User{}, "", err
	}
	u, err := s.create(ctx, NewUser{Name: name, Password: pw}, false, true)
	if err != nil {
		return User{}, "", err
	}
	return u, pw, nil
}

// ResetServicePassword gives the service profile called name a new random
// password (returned in the clear) and ends its sessions and tokens.
func (s *Store) ResetServicePassword(ctx context.Context, name string) (User, string, error) {
	u, err := s.serviceByName(ctx, name)
	if err != nil {
		return User{}, "", err
	}
	pw, err := servicePassword()
	if err != nil {
		return User{}, "", err
	}
	h, err := hashPassword(pw, false)
	if err != nil {
		return User{}, "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=?, app_password=NULL, app_password_created_at=NULL WHERE id=? AND service=1`, h, u.ID); err != nil {
		return User{}, "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, u.ID); err != nil {
		return User{}, "", err
	}
	if err := revokeAppTokens(ctx, tx, u.ID); err != nil {
		return User{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return User{}, "", err
	}
	return u, pw, nil
}

// DeleteService removes the service profile called name (ErrNotFound: none
// such; a person's profile is never found by it).
func (s *Store) DeleteService(ctx context.Context, name string) error {
	u, err := s.serviceByName(ctx, name)
	if err != nil {
		return err
	}
	return s.remove(ctx, u.ID, true)
}

func (s *Store) serviceByName(ctx context.Context, name string) (User, error) {
	u, err := scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE name=? AND service=1`, strings.TrimSpace(name)))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func servicePassword() (string, error) {
	b := make([]byte, servicePasswordBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
