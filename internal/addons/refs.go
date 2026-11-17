package addons

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/antresol-app/antresol/addonkit"
)

// RefStore keeps the addons' file references and holds in the core
// database (migration 0090). A reference ties a library file (files.id) to
// one addon under a string of the addon's choosing; a file belongs to at
// most one addon, and a file another addon references is never re-bound.
// A hold's key equals its ref: the files an addon imported from a hold
// carry the hold's key as their ref.
type RefStore struct{ DB *sql.DB }

// AddonRef is the reference a library file carries.
type AddonRef struct {
	Addon, Ref string
}

// MaxRefLen is the longest ref (and hold key), in bytes.
const MaxRefLen = 128

var (
	// ErrBadRef: a ref is not 1–128 printable ASCII characters.
	ErrBadRef = errors.New("addons: a ref is 1-128 printable ASCII characters")
	// ErrNoAddon: no addon id given.
	ErrNoAddon = errors.New("addons: no addon id")
)

// ValidRef reports whether s can be a ref or a hold key: 1–128 printable
// ASCII characters (space to tilde).
func ValidRef(s string) bool {
	if s == "" || len(s) > MaxRefLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// Bind gives ref to the library files at paths (files.path), in one
// transaction. A path with no file row is missing; a file another addon
// references is taken and left untouched; a file of this addon under
// another ref moves to ref. The ref's other files keep it. A path given
// twice counts once.
func (s *RefStore) Bind(ctx context.Context, addonID, ref string, paths []string) (addonkit.Bound, error) {
	pairs := make([]addonkit.RefPath, len(paths))
	for i, p := range paths {
		pairs[i] = addonkit.RefPath{Ref: ref, Path: p}
	}
	return s.BindMany(ctx, addonID, pairs)
}

// BindMany is Bind of each pair's path to its own ref, all in one
// transaction (the one-time migration of an addon's files). A path given
// more than once is bound once, to its last ref, and counted once.
func (s *RefStore) BindMany(ctx context.Context, addonID string, pairs []addonkit.RefPath) (addonkit.Bound, error) {
	return s.bind(ctx, addonID, pairs, "")
}

// Replace leaves ref on exactly the addon's files at paths, in one
// transaction: they are bound as by Bind (missing, taken alike), and the
// addon's other files with ref lose it (the files stay). Another addon's
// files, and the addon's other refs, are untouched.
func (s *RefStore) Replace(ctx context.Context, addonID, ref string, paths []string) (addonkit.Bound, error) {
	if !ValidRef(ref) {
		return addonkit.Bound{Missing: []string{}, Taken: []string{}}, ErrBadRef
	}
	pairs := make([]addonkit.RefPath, len(paths))
	for i, p := range paths {
		pairs[i] = addonkit.RefPath{Ref: ref, Path: p}
	}
	return s.bind(ctx, addonID, pairs, ref)
}

// bind binds the pairs; with prune set, the addon's files carrying that
// ref and not bound here lose it, in the same transaction.
func (s *RefStore) bind(ctx context.Context, addonID string, pairs []addonkit.RefPath, prune string) (addonkit.Bound, error) {
	out := addonkit.Bound{Missing: []string{}, Taken: []string{}}
	fail := func(err error) (addonkit.Bound, error) {
		return addonkit.Bound{Missing: []string{}, Taken: []string{}}, err
	}
	if addonID == "" {
		return out, ErrNoAddon
	}
	// a path given twice counts once, under its last ref
	at := map[string]int{}
	uniq := make([]addonkit.RefPath, 0, len(pairs))
	for _, p := range pairs {
		if !ValidRef(p.Ref) {
			return out, ErrBadRef
		}
		if i, ok := at[p.Path]; ok {
			uniq[i].Ref = p.Ref
			continue
		}
		at[p.Path] = len(uniq)
		uniq = append(uniq, p)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	look, err := tx.PrepareContext(ctx, `SELECT f.id, COALESCE(r.addon_id, '') FROM files f
	  LEFT JOIN addon_file_refs r ON r.file_id=f.id WHERE f.path=?`)
	if err != nil {
		return out, err
	}
	defer look.Close()
	// the WHERE keeps another addon's row even if one appeared meanwhile
	put, err := tx.PrepareContext(ctx, `INSERT INTO addon_file_refs(file_id, addon_id, ref) VALUES(?,?,?)
	  ON CONFLICT(file_id) DO UPDATE SET ref=excluded.ref WHERE addon_file_refs.addon_id=excluded.addon_id`)
	if err != nil {
		return out, err
	}
	defer put.Close()
	kept := map[int64]bool{}
	for _, p := range uniq {
		var id int64
		var owner string
		err := look.QueryRowContext(ctx, p.Path).Scan(&id, &owner)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			out.Missing = append(out.Missing, p.Path)
			continue
		case err != nil:
			return fail(err)
		case owner != "" && owner != addonID:
			out.Taken = append(out.Taken, p.Path)
			continue
		}
		res, err := put.ExecContext(ctx, id, addonID, p.Ref)
		if err != nil {
			return fail(err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fail(err)
		} else if n == 0 { // the guard kept another addon's row
			out.Taken = append(out.Taken, p.Path)
			continue
		}
		kept[id] = true
		out.Bound++
	}
	if prune != "" {
		if err := pruneRef(ctx, tx, addonID, prune, kept); err != nil {
			return fail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return out, nil
}

// pruneRef removes ref from the addon's files that are not kept.
func pruneRef(ctx context.Context, tx *sql.Tx, addonID, ref string, kept map[int64]bool) error {
	rows, err := tx.QueryContext(ctx, `SELECT file_id FROM addon_file_refs WHERE addon_id=? AND ref=?`, addonID, ref)
	if err != nil {
		return err
	}
	var drop []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !kept[id] {
			drop = append(drop, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	del, err := tx.PrepareContext(ctx, `DELETE FROM addon_file_refs WHERE file_id=? AND addon_id=? AND ref=?`)
	if err != nil {
		return err
	}
	defer del.Close()
	for _, id := range drop {
		if _, err := del.ExecContext(ctx, id, addonID, ref); err != nil {
			return err
		}
	}
	return nil
}

// Unbind removes ref from the addon's files (the files stay).
func (s *RefStore) Unbind(ctx context.Context, addonID, ref string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM addon_file_refs WHERE addon_id=? AND ref=?`, addonID, ref)
	return err
}

// RefsOf returns the references of those files that have one.
func (s *RefStore) RefsOf(ctx context.Context, fileIDs []int64) (map[int64]AddonRef, error) {
	out := map[int64]AddonRef{}
	for start := 0; start < len(fileIDs); start += 500 {
		part := fileIDs[start:min(start+500, len(fileIDs))]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		rows, err := s.DB.QueryContext(ctx, `SELECT file_id, addon_id, ref FROM addon_file_refs WHERE file_id IN (?`+
			strings.Repeat(",?", len(part)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var r AddonRef
			if err := rows.Scan(&id, &r.Addon, &r.Ref); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = r
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SaveHolds keeps raw (the addon's validated set of holds, JSON) as the
// addon's last set, replacing the one before.
func (s *RefStore) SaveHolds(ctx context.Context, addonID string, raw []byte) error {
	if addonID == "" {
		return ErrNoAddon
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO addon_holds(addon_id, holds_json, updated_at) VALUES(?,?,?)
	  ON CONFLICT(addon_id) DO UPDATE SET holds_json=excluded.holds_json, updated_at=excluded.updated_at`,
		addonID, string(raw), time.Now().Unix())
	return err
}

// LoadHolds returns every addon's last set of holds by addon id; the
// caller ignores those of addons no longer installed.
func (s *RefStore) LoadHolds(ctx context.Context) (map[string][]byte, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT addon_id, holds_json FROM addon_holds`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]byte{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		out[id] = []byte(raw)
	}
	return out, rows.Err()
}

// DropHolds forgets the addon's holds (removing an addon always does).
func (s *RefStore) DropHolds(ctx context.Context, addonID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM addon_holds WHERE addon_id=?`, addonID)
	return err
}

// DropRefs forgets every reference of the addon (removing it with its
// data does); the library files stay.
func (s *RefStore) DropRefs(ctx context.Context, addonID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM addon_file_refs WHERE addon_id=?`, addonID)
	return err
}
