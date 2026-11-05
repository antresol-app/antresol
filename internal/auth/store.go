// Package auth implements profiles, password hashing and sessions.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNotFound        = errors.New("auth: not found")
	ErrExists          = errors.New("auth: name already taken")
	ErrInvalid         = errors.New("auth: invalid")
	ErrBadPassword     = errors.New("auth: wrong password")
	ErrSetupDone       = errors.New("auth: setup already done")
	ErrLastAdmin       = errors.New("auth: last admin")
	bcryptCost         = 10
	avatarCount        = 8
	maxNameLen         = 64
	minPasswordLen     = 4
	minAdminPassLen    = 6
	maxPasswordLen     = 72 // bcrypt uses only the first 72 bytes
	ErrPasswordTooLong = fmt.Errorf("%w: password too long", ErrInvalid)
	ErrPasswordShort   = fmt.Errorf("%w: password too short", ErrInvalid)
	ErrAdminPassShort  = fmt.Errorf("%w: admin password too short", ErrInvalid)
	errAdminNoPass     = fmt.Errorf("%w: admin must have a password", ErrInvalid)
	errKidAdmin        = fmt.Errorf("%w: kid profile cannot be admin", ErrInvalid)
)

type User struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Avatar      string  `json:"avatar"`
	HasPassword bool    `json:"hasPassword"`
	IsAdmin     bool    `json:"isAdmin"`
	IsKid       bool    `json:"isKid"`
	MaxRating   *string `json:"maxRating"`
	// HideProgress: the profile's progress is left out of the family view
	// («Скрывать мой прогресс от семьи»).
	HideProgress bool `json:"hideProgress"`
	// LibraryScope: the library the profile's pages show, ScopeAll or
	// ScopeMine («Моя медиатека»; never for a kid). A preference only:
	// the API filters by an explicit ?scope=mine.
	LibraryScope string `json:"libraryScope"`
	// KindleEmail: the profile's Send to Kindle address, set by an admin.
	// Only the profile and admins see it (never /api/profiles).
	KindleEmail string `json:"kindleEmail"`

	hash sql.NullString
}

type NewUser struct {
	Name      string
	Avatar    string // empty: assigned round-robin
	Password  string // empty: no password
	IsAdmin   bool
	IsKid     bool
	MaxRating string
}

// UserPatch: nil fields are left unchanged. Password "" removes the password,
// MaxRating "" removes the limit.
type UserPatch struct {
	Name      *string
	Avatar    *string
	Password  *string
	IsAdmin   *bool
	IsKid     *bool
	MaxRating *string
	// HideProgress is not one of the admin's /api/users fields only: a
	// profile sets its own (PATCH /api/me).
	HideProgress *bool
	// KindleEmail "" removes the address.
	KindleEmail *string
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

const cols = `id, name, avatar, password_hash, is_admin, is_kid, max_rating, hide_progress, library_scope, kindle_email`

type scanner interface{ Scan(...any) error }

func scan(s scanner) (User, error) {
	var u User
	var admin, kid, hide int
	var mr sql.NullString
	if err := s.Scan(&u.ID, &u.Name, &u.Avatar, &u.hash, &admin, &kid, &mr, &hide, &u.LibraryScope, &u.KindleEmail); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	u.HasPassword = u.hash.Valid && u.hash.String != ""
	u.IsAdmin, u.IsKid, u.HideProgress = admin != 0, kid != 0, hide != 0
	if mr.Valid {
		u.MaxRating = &mr.String
	}
	if u.IsKid || u.LibraryScope != ScopeMine {
		u.LibraryScope = ScopeAll
	}
	return u, nil
}

// Library scopes (User.LibraryScope).
const (
	ScopeAll  = "all"
	ScopeMine = "mine"
)

// SetLibraryScope saves the library the profile's pages show. A kid's
// stays ScopeAll (ErrInvalid), as does any other value.
func (s *Store) SetLibraryScope(ctx context.Context, id int64, scope string) error {
	if scope != ScopeAll && scope != ScopeMine {
		return fmt.Errorf("%w: library scope", ErrInvalid)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET library_scope=? WHERE id=? AND (is_kid=0 OR ?='all')`, scope, id, scope)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var kid int
		if err := s.db.QueryRowContext(ctx, `SELECT is_kid FROM users WHERE id=?`, id).Scan(&kid); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		return fmt.Errorf("%w: a kid profile sees the shared library", ErrInvalid)
	}
	return nil
}

func validate(name string, admin, kid, hasPass bool) error {
	if name == "" || len(name) > maxNameLen {
		return fmt.Errorf("%w: name", ErrInvalid)
	}
	if admin && kid {
		return errKidAdmin
	}
	if admin && !hasPass {
		return errAdminNoPass
	}
	return nil
}

// hashPassword checks the length (admins need a longer one) and hashes p.
func hashPassword(p string, admin bool) (string, error) {
	if admin && len([]rune(p)) < minAdminPassLen {
		return "", ErrAdminPassShort
	}
	if len([]rune(p)) < minPasswordLen {
		return "", ErrPasswordShort
	}
	if len(p) > maxPasswordLen {
		return "", ErrPasswordTooLong
	}
	b, err := bcrypt.GenerateFromPassword([]byte(p), bcryptCost)
	return string(b), err
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// CreateUser adds a profile.
func (s *Store) CreateUser(ctx context.Context, n NewUser) (User, error) {
	return s.create(ctx, n, false)
}

// CreateFirstAdmin atomically creates the first user; ErrSetupDone if any exists.
func (s *Store) CreateFirstAdmin(ctx context.Context, name, password string) (User, error) {
	return s.create(ctx, NewUser{Name: name, Password: password, IsAdmin: true}, true)
}

func (s *Store) create(ctx context.Context, n NewUser, first bool) (User, error) {
	n.Name = strings.TrimSpace(n.Name)
	if err := validate(n.Name, n.IsAdmin, n.IsKid, n.Password != ""); err != nil {
		return User{}, err
	}
	var hash any
	if n.Password != "" {
		h, err := hashPassword(n.Password, n.IsAdmin)
		if err != nil {
			return User{}, err
		}
		hash = h
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return User{}, err
	}
	if first && count > 0 {
		return User{}, ErrSetupDone
	}
	if n.Avatar == "" {
		n.Avatar = fmt.Sprintf("grad-%d", count%avatarCount+1)
	}
	var mr any
	if n.MaxRating != "" {
		mr = n.MaxRating
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users(name, avatar, password_hash, is_admin, is_kid, max_rating) VALUES(?,?,?,?,?,?)`,
		n.Name, n.Avatar, hash, b2i(n.IsAdmin), b2i(n.IsKid), mr)
	if err != nil {
		if isUnique(err) {
			return User{}, ErrExists
		}
		return User{}, err
	}
	id, _ := res.LastInsertId()
	u, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id=?`, id))
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) List(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM users ORDER BY id`)
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

func (s *Store) Get(ctx context.Context, id int64) (User, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id=?`, id))
}

// Verify checks the password. A profile without a password accepts an empty
// password only. Passwords longer than bcrypt's 72 bytes never match (no
// stored password can be that long), so they never reach bcrypt.
func (s *Store) Verify(ctx context.Context, id int64, password string) (User, error) {
	u, err := s.Get(ctx, id)
	if err != nil {
		return User{}, err
	}
	if !u.HasPassword {
		if password != "" {
			return User{}, ErrBadPassword
		}
		return u, nil
	}
	if len(password) > maxPasswordLen {
		return User{}, ErrBadPassword
	}
	if bcrypt.CompareHashAndPassword([]byte(u.hash.String), []byte(password)) != nil {
		return User{}, ErrBadPassword
	}
	return u, nil
}

// VerifyPasswordless returns the user only if it has no password.
func (s *Store) VerifyPasswordless(ctx context.Context, id int64) (User, error) {
	u, err := s.Get(ctx, id)
	if err != nil {
		return User{}, err
	}
	if u.HasPassword {
		return User{}, ErrBadPassword
	}
	return u, nil
}

func (s *Store) Update(ctx context.Context, id int64, p UserPatch) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	u, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id=?`, id))
	if err != nil {
		return err
	}
	if p.Name != nil {
		u.Name = strings.TrimSpace(*p.Name)
	}
	if p.Avatar != nil {
		if *p.Avatar == "" {
			return fmt.Errorf("%w: avatar", ErrInvalid)
		}
		u.Avatar = *p.Avatar
	}
	if p.IsAdmin != nil {
		u.IsAdmin = *p.IsAdmin
	}
	if p.Password != nil {
		if *p.Password == "" {
			u.hash = sql.NullString{}
		} else {
			h, err := hashPassword(*p.Password, u.IsAdmin)
			if err != nil {
				return err
			}
			u.hash = sql.NullString{String: h, Valid: true}
		}
		u.HasPassword = u.hash.Valid
	}
	if p.IsKid != nil {
		u.IsKid = *p.IsKid
	}
	if p.MaxRating != nil {
		if *p.MaxRating == "" {
			u.MaxRating = nil
		} else {
			u.MaxRating = p.MaxRating
		}
	}
	if p.HideProgress != nil {
		u.HideProgress = *p.HideProgress
	}
	if p.KindleEmail != nil {
		u.KindleEmail = *p.KindleEmail
	}
	if err := validate(u.Name, u.IsAdmin, u.IsKid, u.HasPassword); err != nil {
		return err
	}
	if !u.IsAdmin {
		if err := lastAdminCheck(ctx, tx, id); err != nil {
			return err
		}
	}
	var hash, mr any
	if u.hash.Valid {
		hash = u.hash.String
	}
	if u.MaxRating != nil {
		mr = *u.MaxRating
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE users SET name=?, avatar=?, password_hash=?, is_admin=?, is_kid=?, max_rating=?, hide_progress=?, kindle_email=? WHERE id=?`,
		u.Name, u.Avatar, hash, b2i(u.IsAdmin), b2i(u.IsKid), mr, b2i(u.HideProgress), u.KindleEmail, id)
	if err == nil && p.Password != nil {
		// A new, changed or removed login password also ends the app
		// password: one minted from a session the password change is meant
		// to lock out must not keep /rest and /opds access.
		_, err = tx.ExecContext(ctx, `UPDATE users SET app_password=NULL, app_password_created_at=NULL WHERE id=?`, id)
		if err == nil {
			err = revokeAppTokens(ctx, tx, id) // and the app tokens got with it
		}
	}
	if err != nil {
		if isUnique(err) {
			return ErrExists
		}
		return err
	}
	return tx.Commit()
}

// lastAdminCheck fails if id is currently the only admin.
func lastAdminCheck(ctx context.Context, tx *sql.Tx, id int64) error {
	var isAdmin, others int
	if err := tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id=?`, id).Scan(&isAdmin); err != nil {
		return err
	}
	if isAdmin == 0 {
		return nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_admin=1 AND id<>?`, id).Scan(&others); err != nil {
		return err
	}
	if others == 0 {
		return ErrLastAdmin
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lastAdminCheck(ctx, tx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	// ON DELETE CASCADE covers it too; explicit so it never depends on the
	// foreign_keys pragma.
	if err := revokeAppTokens(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) NeedsSetup(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n == 0, err
}
