// Package auth implements profiles, password hashing and sessions.
package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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
	ErrAdminPassCommon = fmt.Errorf("%w: admin password is a common one", ErrInvalid)
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
	// Photo: the address of the profile's photo (PhotoURL; absent: none, the
	// letter on the coloured circle Avatar names stands for it).
	Photo string `json:"photo,omitempty"`
	// PhotoVersion: users.photo_version, 0 without a photo.
	PhotoVersion int64 `json:"-"`
	// UID: the profile's stable key (users.uid), for addons only: an id
	// may be given to a new profile once this one is deleted, a uid never.
	UID string `json:"-"`
	// OwnerID: the person a managed account belongs to (users.owner_id; 0:
	// a person's own profile). A managed account takes its owner's limits
	// (IsKid, MaxRating, LibraryScope, HideProgress) and is never signed in.
	OwnerID int64 `json:"ownerId,omitempty"`
	// OwnerName: the owner's name (ManagedOf, AllManaged and ListAll only).
	OwnerName string `json:"ownerName,omitempty"`

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

// cols: a managed account (owner_id set) reads its limits from its owner,
// so a change of the owner's applies to it at once, and its name from
// managed_name (users.name holds an internal key for it, see NameSQL).
// Every query with cols reads FROM users without an alias.
const cols = `id, CASE WHEN owner_id IS NULL THEN name ELSE COALESCE(managed_name, name) END, avatar, password_hash, is_admin,
	COALESCE((SELECT o.is_kid FROM users o WHERE o.id = users.owner_id), is_kid),
	CASE WHEN owner_id IS NULL THEN max_rating ELSE (SELECT o.max_rating FROM users o WHERE o.id = users.owner_id) END,
	COALESCE((SELECT o.hide_progress FROM users o WHERE o.id = users.owner_id), hide_progress),
	COALESCE((SELECT o.library_scope FROM users o WHERE o.id = users.owner_id), library_scope),
	kindle_email, COALESCE(uid, ''), photo_version, COALESCE(owner_id, 0)`

// NameSQL is the profile's name as an expression over the users table
// (or its alias a). A managed account keeps its name in managed_name, so
// that it never shares the namespace of the people's profiles; its
// users.name is an internal key and is never shown.
func NameSQL(a string) string {
	return `CASE WHEN ` + a + `.owner_id IS NULL THEN ` + a + `.name ELSE COALESCE(` + a + `.managed_name, ` + a + `.name) END`
}

type scanner interface{ Scan(...any) error }

func scan(s scanner) (User, error) {
	var u User
	var admin, kid, hide int
	var mr sql.NullString
	if err := s.Scan(&u.ID, &u.Name, &u.Avatar, &u.hash, &admin, &kid, &mr, &hide, &u.LibraryScope, &u.KindleEmail, &u.UID, &u.PhotoVersion, &u.OwnerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	u.HasPassword = u.hash.Valid && u.hash.String != ""
	u.Photo = PhotoURL(u.ID, u.PhotoVersion)
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

// AdminPasswordMin is the length an administrator's password needs when it
// is chosen through the API. The store itself refuses only passwords under
// minAdminPassLen, so existing accounts and internal callers are unaffected.
const AdminPasswordMin = 10

// commonPasswords: passwords of AdminPasswordMin or more characters that
// every guessing list starts with (lower case).
var commonPasswords = map[string]bool{
	"1234567890": true, "0123456789": true, "9876543210": true, "12345678910": true, "123456789a": true,
	"1q2w3e4r5t": true, "1qaz2wsx3edc": true, "qwertyuiop": true, "asdfghjkl;": true, "qwertyuiopasdf": true,
	"password12": true, "password123": true, "password1234": true, "passw0rd123": true, "iloveyou123": true,
	"administrator": true, "adminadmin": true, "admin12345": true, "welcome123": true, "changeme123": true,
	"qwerty1234": true, "qwerty12345": true, "1234qwerty": true, "zxcvbnm123": true,
}

// CheckAdminPassword is what an administrator's new password must pass:
// ErrAdminPassShort under AdminPasswordMin characters, ErrAdminPassCommon
// for a password on the guessing lists.
func CheckAdminPassword(p string) error {
	if len([]rune(p)) < AdminPasswordMin {
		return ErrAdminPassShort
	}
	if commonPasswords[strings.ToLower(p)] {
		return ErrAdminPassCommon
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
	uid, err := newUID()
	if err != nil {
		return User{}, err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users(name, avatar, password_hash, is_admin, is_kid, max_rating, uid) VALUES(?,?,?,?,?,?,?)`,
		n.Name, n.Avatar, hash, b2i(n.IsAdmin), b2i(n.IsKid), mr, uid)
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

// newUID is a fresh users.uid: 16 random bytes in lower-case hex.
func newUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// List: the people's profiles (managed accounts are left out: they are
// never signed in to; ManagedOf and AllManaged list them).
func (s *Store) List(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM users WHERE owner_id IS NULL ORDER BY id`)
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

// GetPerson is Get for a person's profile only: a managed account is
// ErrNotFound (it has no sign-in and no settings of its own). Whatever
// signs a profile in, or picks one to sign in as, looks it up with this.
func (s *Store) GetPerson(ctx context.Context, id int64) (User, error) {
	u, err := s.Get(ctx, id)
	if err == nil && u.OwnerID != 0 {
		return User{}, ErrNotFound
	}
	return u, err
}

// Verify checks the password. A profile without a password accepts an empty
// password only. Passwords longer than bcrypt's 72 bytes never match (no
// stored password can be that long), so they never reach bcrypt.
func (s *Store) Verify(ctx context.Context, id int64, password string) (User, error) {
	u, err := s.GetPerson(ctx, id)
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
	u, err := s.GetPerson(ctx, id)
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
	if u.OwnerID != 0 { // a managed account is renamed with RenameManaged only
		return ErrNotFound
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
		// The managed accounts' app passwords too: they were made from such
		// a session as well.
		_, err = tx.ExecContext(ctx, `UPDATE users SET app_password=NULL, app_password_created_at=NULL WHERE id=? OR owner_id=?`, id, id)
		if err == nil {
			err = revokeAppTokens(ctx, tx, id) // and the app tokens got with them
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
	// the person's managed accounts first (the owner_id cascade covers it
	// too; explicit as below)
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE owner_id=?`, id); err != nil {
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
	// the photo too: the id may go to a new profile, which must not get this face
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_photos WHERE user_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) NeedsSetup(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n == 0, err
}
