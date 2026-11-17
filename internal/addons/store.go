package addons

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound: no addon with that id (or token).
var ErrNotFound = errors.New("addon not found")

// userError is an error whose text is a Russian user message (translated
// through internal/i18n).
type userError string

func (e userError) Error() string { return string(e) }

// UserMessage is the user message err carries, if it is one; other
// errors (storage, file system) are not for the user's eyes.
func UserMessage(err error) (string, bool) {
	var ue userError
	if errors.As(err, &ue) {
		return string(ue), true
	}
	return "", false
}

// Installed is one installed addon.
type Installed struct {
	ID, Source, Version, PrevVersion string
	Manifest                         Manifest
	Enabled                          bool
	InstalledAt, UpdatedAt           time.Time
}

// Store keeps installed addons in the core database.
type Store struct{ DB *sql.DB }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

const cols = `id, source, version, prev_version, manifest, enabled, installed_at, updated_at`

type scanner interface{ Scan(...any) error }

func scan(r scanner) (Installed, error) {
	var in Installed
	var prev sql.NullString
	var manifest string
	var enabled int
	var inst, upd int64
	if err := r.Scan(&in.ID, &in.Source, &in.Version, &prev, &manifest, &enabled, &inst, &upd); err != nil {
		return Installed{}, err
	}
	if err := json.Unmarshal([]byte(manifest), &in.Manifest); err != nil {
		return Installed{}, err
	}
	in.PrevVersion = prev.String
	in.Enabled = enabled != 0
	in.InstalledAt = time.Unix(inst, 0).UTC()
	in.UpdatedAt = time.Unix(upd, 0).UTC()
	return in, nil
}

// List returns all addons ordered by id.
func (s *Store) List(ctx context.Context) ([]Installed, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+cols+` FROM addons ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Installed
	for rows.Next() {
		in, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// Get returns one addon, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Installed, error) {
	in, err := scan(s.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM addons WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Installed{}, ErrNotFound
	}
	return in, err
}

// Put inserts or updates an addon and stores sha256(token). On an existing
// id the original installed_at is kept.
func (s *Store) Put(ctx context.Context, in Installed, token string) error {
	m, err := json.Marshal(in.Manifest)
	if err != nil {
		return err
	}
	var prev any
	if in.PrevVersion != "" {
		prev = in.PrevVersion
	}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO addons (id, source, version, prev_version, manifest, enabled, token_hash, installed_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  source = excluded.source, version = excluded.version, prev_version = excluded.prev_version,
  manifest = excluded.manifest, enabled = excluded.enabled, token_hash = excluded.token_hash,
  updated_at = excluded.updated_at`,
		in.ID, in.Source, in.Version, prev, string(m), enabled, hashToken(token),
		in.InstalledAt.Unix(), in.UpdatedAt.Unix())
	return err
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetEnabled switches an addon on or off.
func (s *Store) SetEnabled(ctx context.Context, id string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	return affected(s.DB.ExecContext(ctx, `UPDATE addons SET enabled = ? WHERE id = ?`, v, id))
}

// SetToken replaces the addon's token (stores its hash).
func (s *Store) SetToken(ctx context.Context, id, token string) error {
	return affected(s.DB.ExecContext(ctx, `UPDATE addons SET token_hash = ? WHERE id = ?`, hashToken(token), id))
}

// Delete removes an addon.
func (s *Store) Delete(ctx context.Context, id string) error {
	return affected(s.DB.ExecContext(ctx, `DELETE FROM addons WHERE id = ?`, id))
}

// IDForToken returns the id of the addon owning the token, or ErrNotFound.
func (s *Store) IDForToken(ctx context.Context, token string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM addons WHERE token_hash = ?`, hashToken(token)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// Save updates an existing addon's record and leaves its token hash
// alone (the running process keeps its token); ErrNotFound if none.
func (s *Store) Save(ctx context.Context, in Installed) error {
	m, err := json.Marshal(in.Manifest)
	if err != nil {
		return err
	}
	var prev any
	if in.PrevVersion != "" {
		prev = in.PrevVersion
	}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	return affected(s.DB.ExecContext(ctx, `
UPDATE addons SET source = ?, version = ?, prev_version = ?, manifest = ?, enabled = ?, updated_at = ?
WHERE id = ?`, in.Source, in.Version, prev, string(m), enabled, in.UpdatedAt.Unix(), in.ID))
}

// ClearPrevVersion forgets the previous version (its files are gone).
func (s *Store) ClearPrevVersion(ctx context.Context, id string) error {
	return affected(s.DB.ExecContext(ctx, `UPDATE addons SET prev_version = NULL WHERE id = ?`, id))
}
