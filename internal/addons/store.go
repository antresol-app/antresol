package addons

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
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

// row is what Put and Save write of in: its manifest as JSON, prev_version
// (NULL when none) and enabled.
func (in Installed) row() (manifest string, prev any, enabled int, err error) {
	m, err := json.Marshal(in.Manifest)
	if in.PrevVersion != "" {
		prev = in.PrevVersion
	}
	return string(m), prev, b2i(in.Enabled), err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Store keeps installed addons in the core database.
//
// Reads come from a snapshot of the whole table, decoded once and dropped
// by every write through a Store (the only writer of the addons table):
// the addon hot path (a token, a manifest) is a map lookup, not a query and
// a JSON decode. The snapshot is shared by every Store over the same
// database handle. The manifests it hands out are shared: a reader never
// changes one.
type Store struct{ DB *sql.DB }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

const cols = `id, source, version, prev_version, manifest, enabled, installed_at, updated_at`

type scanner interface{ Scan(...any) error }

// snapRow is a row of the addons table. err: its manifest does not decode
// (Get of it fails, so does List, as a query of it did).
type snapRow struct {
	in   Installed
	hash string // of its token
	err  error
}

// scanRow reads a row of `SELECT cols, token_hash`; an error is the row not
// being readable at all.
func scanRow(r scanner) (snapRow, error) {
	var row snapRow
	var prev sql.NullString
	var manifest string
	var enabled int
	var inst, upd int64
	in := &row.in
	if err := r.Scan(&in.ID, &in.Source, &in.Version, &prev, &manifest, &enabled, &inst, &upd, &row.hash); err != nil {
		return snapRow{}, err
	}
	in.PrevVersion = prev.String
	in.Enabled = enabled != 0
	in.InstalledAt = time.Unix(inst, 0).UTC()
	in.UpdatedAt = time.Unix(upd, 0).UTC()
	row.err = json.Unmarshal([]byte(manifest), &in.Manifest)
	return row, nil
}

// storeSnap is the addons table as read at one moment.
type storeSnap struct {
	rows   []snapRow // by id
	byID   map[string]int
	byHash map[string]string // token hash → id
	err    error             // the first row's decode error: List's
}

// snapState is the snapshot of one database and the writes it has seen.
type snapState struct {
	mu   sync.Mutex // gen, and the replacing of snap
	gen  uint64     // writes seen: a snapshot read before one is not kept
	snap atomic.Pointer[storeSnap]
}

var snapStates sync.Map // *sql.DB → *snapState

func (s *Store) state() *snapState {
	if v, ok := snapStates.Load(s.DB); ok {
		return v.(*snapState)
	}
	v, _ := snapStates.LoadOrStore(s.DB, new(snapState))
	return v.(*snapState)
}

// changed drops the snapshot after a write (made or tried). The write is
// done before it, so a snapshot read afterwards has it; one read before it
// carries an older generation and is never kept.
func (s *Store) changed() {
	st := s.state()
	st.mu.Lock()
	st.gen++
	st.snap.Store(nil)
	st.mu.Unlock()
}

func (s *Store) snapshot(ctx context.Context) (*storeSnap, error) {
	st := s.state()
	if sn := st.snap.Load(); sn != nil {
		return sn, nil
	}
	st.mu.Lock()
	gen := st.gen
	st.mu.Unlock()
	sn, err := s.readAll(ctx)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	if st.gen == gen {
		st.snap.Store(sn)
	}
	st.mu.Unlock()
	return sn, nil
}

func (s *Store) readAll(ctx context.Context) (*storeSnap, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+cols+`, token_hash FROM addons ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sn := &storeSnap{byID: map[string]int{}, byHash: map[string]string{}}
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		sn.byID[row.in.ID] = len(sn.rows)
		sn.byHash[row.hash] = row.in.ID
		sn.rows = append(sn.rows, row)
		if sn.err == nil {
			sn.err = row.err
		}
	}
	return sn, rows.Err()
}

// List returns all addons ordered by id.
func (s *Store) List(ctx context.Context) ([]Installed, error) {
	sn, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if sn.err != nil {
		return nil, sn.err
	}
	var out []Installed
	for _, r := range sn.rows {
		out = append(out, r.in)
	}
	return out, nil
}

// Get returns one addon, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Installed, error) {
	sn, err := s.snapshot(ctx)
	if err != nil {
		return Installed{}, err
	}
	i, ok := sn.byID[id]
	switch {
	case !ok:
		return Installed{}, ErrNotFound
	case sn.rows[i].err != nil:
		return Installed{}, sn.rows[i].err
	}
	return sn.rows[i].in, nil
}

// Put inserts or updates an addon and stores sha256(token). On an existing
// id the original installed_at is kept.
func (s *Store) Put(ctx context.Context, in Installed, token string) error {
	m, prev, enabled, err := in.row()
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, `
INSERT INTO addons (id, source, version, prev_version, manifest, enabled, token_hash, installed_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  source = excluded.source, version = excluded.version, prev_version = excluded.prev_version,
  manifest = excluded.manifest, enabled = excluded.enabled, token_hash = excluded.token_hash,
  updated_at = excluded.updated_at`,
		in.ID, in.Source, in.Version, prev, m, enabled, hashToken(token),
		in.InstalledAt.Unix(), in.UpdatedAt.Unix())
	return err
}

// exec runs a write and drops the snapshot, whether it worked or not.
func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := s.DB.ExecContext(ctx, query, args...)
	s.changed()
	return res, err
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
	return affected(s.exec(ctx, `UPDATE addons SET enabled = ? WHERE id = ?`, b2i(on), id))
}

// SetToken replaces the addon's token (stores its hash).
func (s *Store) SetToken(ctx context.Context, id, token string) error {
	return affected(s.exec(ctx, `UPDATE addons SET token_hash = ? WHERE id = ?`, hashToken(token), id))
}

// Delete removes an addon.
func (s *Store) Delete(ctx context.Context, id string) error {
	return affected(s.exec(ctx, `DELETE FROM addons WHERE id = ?`, id))
}

// IDForToken returns the id of the addon owning the token, or ErrNotFound.
func (s *Store) IDForToken(ctx context.Context, token string) (string, error) {
	sn, err := s.snapshot(ctx)
	if err != nil {
		return "", err
	}
	id, ok := sn.byHash[hashToken(token)]
	if !ok {
		return "", ErrNotFound
	}
	return id, nil
}

// Save updates an existing addon's record and leaves its token hash
// alone (the running process keeps its token); ErrNotFound if none.
func (s *Store) Save(ctx context.Context, in Installed) error {
	m, prev, enabled, err := in.row()
	if err != nil {
		return err
	}
	return affected(s.exec(ctx, `
UPDATE addons SET source = ?, version = ?, prev_version = ?, manifest = ?, enabled = ?, updated_at = ?
WHERE id = ?`, in.Source, in.Version, prev, m, enabled, in.UpdatedAt.Unix(), in.ID))
}

// ClearPrevVersion forgets the previous version (its files are gone).
func (s *Store) ClearPrevVersion(ctx context.Context, id string) error {
	return affected(s.exec(ctx, `UPDATE addons SET prev_version = NULL WHERE id = ?`, id))
}
