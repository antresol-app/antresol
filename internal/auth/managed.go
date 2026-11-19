package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Managed accounts («Отдельная история на этом устройстве»): a profile row
// with owner_id set, a history of its own under a person. It is never
// signed in to and never the identity of a request: a device whose switch
// is on stays signed in as the person, and only its history (progress,
// watched marks, «Продолжить») goes to the managed account
// (Session.HistoryUserID, HistoryID).

// maxManaged is how many managed accounts one person may have.
const maxManaged = 20

// ErrTooMany: the person has maxManaged managed accounts already.
var ErrTooMany = errors.New("auth: too many managed accounts")

// CreateManaged adds a managed account named name under the person
// ownerID (ErrNotFound when ownerID is not a person's profile; ErrExists
// when the person has an account of that name already).
func (s *Store) CreateManaged(ctx context.Context, ownerID int64, name string) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	u, err := createManaged(ctx, tx, ownerID, name)
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}

// createManaged is CreateManaged inside the caller's transaction.
func createManaged(ctx context.Context, tx *sql.Tx, ownerID int64, name string) (User, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxNameLen {
		return User{}, fmt.Errorf("%w: name", ErrInvalid)
	}
	var avatar string
	var owner sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT avatar, owner_id FROM users WHERE id=?`, ownerID).Scan(&avatar, &owner)
	if errors.Is(err, sql.ErrNoRows) || owner.Valid {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE owner_id=?`, ownerID).Scan(&n); err != nil {
		return User{}, err
	}
	if n >= maxManaged {
		return User{}, ErrTooMany
	}
	uid, err := newUID()
	if err != nil {
		return User{}, err
	}
	// users.name is unique across the table: the account's own name lives
	// in managed_name (unique per owner), users.name is a key of its own
	res, err := tx.ExecContext(ctx, `INSERT INTO users(name, managed_name, avatar, owner_id, uid) VALUES(?,?,?,?,?)`,
		managedKeyPrefix+uid, name, avatar, ownerID, uid)
	if isUnique(err) {
		return User{}, ErrExists
	}
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id=?`, id))
}

// managedKeyPrefix starts users.name of a managed account, which is
// never shown (its name is managed_name).
const managedKeyPrefix = "managed:"

// CreateManagedNamed is CreateManaged with the first free name of base,
// «base», «base 2», «base 3»… (a device's name, which the person's
// account of another device may have taken already).
func (s *Store) CreateManagedNamed(ctx context.Context, ownerID int64, base string) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	u, err := createManagedNamed(ctx, tx, ownerID, base)
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}

func createManagedNamed(ctx context.Context, tx *sql.Tx, ownerID int64, base string) (User, error) {
	base = strings.TrimSpace(base)
	if r := []rune(base); len(r) > maxNameLen-4 {
		base = strings.TrimSpace(string(r[:maxNameLen-4]))
	}
	for i := 1; i <= 100; i++ {
		name := base
		if i > 1 {
			name = base + " " + strconv.Itoa(i)
		}
		u, err := createManaged(ctx, tx, ownerID, name)
		if !errors.Is(err, ErrExists) {
			return u, err
		}
	}
	return User{}, ErrExists
}

// managedWhere: the managed account id of ownerID, any owner's when
// ownerID is 0 (an admin).
const managedWhere = `id=? AND owner_id IS NOT NULL AND (?=0 OR owner_id=?)`

// RenameManaged renames the managed account id of ownerID (0: any
// owner's); ErrNotFound for anything else.
func (s *Store) RenameManaged(ctx context.Context, ownerID, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxNameLen {
		return fmt.Errorf("%w: name", ErrInvalid)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET managed_name=? WHERE `+managedWhere, name, id, ownerID, ownerID)
	if isUnique(err) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteManaged deletes the managed account id of ownerID (0: any
// owner's) and with it its history; the devices that wrote into it write
// the person's history again.
func (s *Store) DeleteManaged(ctx context.Context, ownerID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE `+managedWhere, id, ownerID, ownerID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ManagedOf lists the person's managed accounts, oldest first.
func (s *Store) ManagedOf(ctx context.Context, ownerID int64) ([]User, error) {
	return s.listWhere(ctx, `owner_id=?`, ownerID)
}

// AllManaged lists every person's managed accounts (an admin's view).
func (s *Store) AllManaged(ctx context.Context) ([]User, error) {
	return s.listWhere(ctx, `owner_id IS NOT NULL`)
}

// ListAll: the people's profiles and the managed accounts (the family view
// shows a device's history next to the people).
func (s *Store) ListAll(ctx context.Context) ([]User, error) {
	return s.listWhere(ctx, `1`)
}

func (s *Store) listWhere(ctx context.Context, where string, args ...any) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM users WHERE `+where+` ORDER BY id`, args...)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	names := map[int64]string{}
	for i, u := range out {
		if u.OwnerID == 0 {
			continue
		}
		n, ok := names[u.OwnerID]
		if !ok {
			if err := s.db.QueryRowContext(ctx, `SELECT name FROM users WHERE id=?`, u.OwnerID).Scan(&n); err != nil {
				return nil, err
			}
			names[u.OwnerID] = n
		}
		out[i].OwnerName = n
	}
	return out, nil
}

// IsManaged: id is a managed account (false for a person and for no row).
func (s *Store) IsManaged(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND owner_id IS NOT NULL`, id).Scan(&n)
	return n > 0, err
}

// deviceKey is the session's key in device_history: a native app's
// install id (it outlives a new sign-in), else the session itself.
func deviceKey(ss Session) string {
	if ss.Kind == KindNative && ss.Device.ID != "" {
		return "app:" + ss.Device.ID
	}
	return "s:" + strconv.FormatInt(ss.ID, 10)
}

// withHistory fills ss.HistoryUserID and HistoryName from device_history.
func (s *Sessions) withHistory(ctx context.Context, ss *Session) error {
	err := s.db.QueryRowContext(ctx, `SELECT d.managed_id, `+NameSQL("u")+` FROM device_history d JOIN users u ON u.id = d.managed_id
		WHERE d.user_id=? AND d.device_key=? AND d.enabled=1`, ss.UserID, deviceKey(*ss)).Scan(&ss.HistoryUserID, &ss.HistoryName)
	if errors.Is(err, sql.ErrNoRows) {
		ss.HistoryUserID, ss.HistoryName = 0, ""
		return nil
	}
	return err
}

// SetHistory turns the device's separate history on (managedID: a managed
// account of the session's profile, else ErrNotFound) or off (on false:
// the account is remembered for LastHistory).
func (s *Sessions) SetHistory(ctx context.Context, ss Session, managedID int64, on bool) error {
	key := deviceKey(ss)
	if !on {
		_, err := s.db.ExecContext(ctx, `UPDATE device_history SET enabled=0 WHERE user_id=? AND device_key=?`, ss.UserID, key)
		return err
	}
	return setHistoryOn(ctx, s.db, ss, key, managedID)
}

// dbtx is what *sql.DB and *sql.Tx share.
type dbtx interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func setHistoryOn(ctx context.Context, q dbtx, ss Session, key string, managedID int64) error {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND owner_id=?`, managedID, ss.UserID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	_, err := q.ExecContext(ctx, `INSERT INTO device_history(user_id, device_key, managed_id, enabled) VALUES(?,?,?,1)
		ON CONFLICT(user_id, device_key) DO UPDATE SET managed_id=excluded.managed_id, enabled=1`, ss.UserID, key, managedID)
	return err
}

// SwitchOn turns the device's separate history on and returns the account
// it writes into: managedID (a managed account of the session's profile),
// or when 0 the account the device had (switched off with «Оставить»),
// else a new one named after base («Apple TV», «Apple TV 2»…). Looking for
// the account, creating it and switching on is one transaction: two
// requests at once (a double tap, an app that retries) end with one
// account, not two.
func (s *Sessions) SwitchOn(ctx context.Context, ss Session, managedID int64, base string) (int64, error) {
	key := deviceKey(ss)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if managedID == 0 {
		err := tx.QueryRowContext(ctx, `SELECT managed_id FROM device_history WHERE user_id=? AND device_key=?`, ss.UserID, key).Scan(&managedID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	if managedID == 0 {
		u, err := createManagedNamed(ctx, tx, ss.UserID, base)
		if err != nil {
			return 0, err
		}
		managedID = u.ID
	}
	if err := setHistoryOn(ctx, tx, ss, key, managedID); err != nil {
		return 0, err
	}
	return managedID, tx.Commit()
}

// DetachManaged switches off every device that writes into the managed
// account (they remember it, as after «Оставить»): from the next request
// they write the person's history again.
func (s *Sessions) DetachManaged(ctx context.Context, managedID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE device_history SET enabled=0 WHERE managed_id=?`, managedID)
	return err
}

// LastHistory: the managed account the device writes into or wrote into
// before it was switched off (0: none).
func (s *Sessions) LastHistory(ctx context.Context, ss Session) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT managed_id FROM device_history WHERE user_id=? AND device_key=?`, ss.UserID, deviceKey(ss)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// DevicesOfManaged: the sessions whose history goes to managedID now.
func (s *Sessions) DevicesOfManaged(ctx context.Context, managedID int64) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionColsOf("sessions")+` FROM device_history d JOIN sessions
		ON sessions.user_id = d.user_id AND d.device_key = CASE WHEN sessions.kind='native' AND sessions.device_id<>''
			THEN 'app:' || sessions.device_id ELSE 's:' || sessions.rowid END
		WHERE d.managed_id=? AND d.enabled=1 AND sessions.kind IN (?,?) ORDER BY sessions.last_seen DESC, sessions.rowid DESC`, managedID, KindNative, KindWeb)
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

// SessionByID is one app's or browser's session by its rowid.
func (s *Sessions) SessionByID(ctx context.Context, id int64) (Session, error) {
	ss, err := scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE rowid=? AND kind IN (?,?)`, id, KindNative, KindWeb))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	return ss, s.withHistory(ctx, &ss)
}

// HistoryID is the profile the request's history goes to: the device's
// managed account when its separate history is on, else the caller.
func HistoryID(ctx context.Context) int64 {
	if ss, ok := SessionFrom(ctx); ok && ss.HistoryUserID != 0 {
		return ss.HistoryUserID
	}
	u, _ := UserFrom(ctx)
	return u.ID
}

// WithSession returns ctx carrying ss (used by Guard.Require and tests).
func WithSession(ctx context.Context, ss Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, ss)
}
