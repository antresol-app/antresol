package channels

import (
	"context"
	"database/sql"
	"sort"
)

// Channel numbers are one list for the household (owner, 2026-11-18): the
// channels it sees — Antresol's own (shared, then personal), then the
// external ones in source and playlist order — are 1…N with no gaps. A
// number an admin set (number_manual) stays where it is and the automatic
// ones step round it. A hidden channel (disabled; an external one none of
// whose entries is shown) has no place in 1…N: it keeps a number after N,
// in the same order, because channels.number is NOT NULL UNIQUE and every
// client (web, the apps, the IPTV export) reads a number on every row it
// gets; viewers never get a hidden row. Renumber runs whenever what is
// seen can change: a channel created, edited or removed, the generator's
// sync, the IPTV sync.

// Renumber numbers every channel by the rule above.
func (s *Store) Renumber(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := RenumberTx(ctx, tx, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}

type numRow struct {
	id, number   int64
	kind         string
	personal     bool
	enabled      bool
	manual       int64 // the admin's number, 0: automatic
	place        int64 // the order key within its block (the current number, or a hint)
	src, srcID   int64 // external: the first entry's source position and id
	pos          int64 // external: and its playlist position
	hasEntry     bool
	final        int64
	finalManual  bool
	storedManual bool
}

// RenumberTx renumbers inside tx. manual: the admin's number per channel id
// where the caller knows it better than the stored number_manual (the IPTV
// sync, from its entries' decisions; 0: automatic). place: an order key
// per channel id instead of its current number (a channel a manual number
// pushed out keeps the place it had).
func RenumberTx(ctx context.Context, tx *sql.Tx, manual map[int64]int, place map[int64]int64) error {
	rows, err := loadNumRows(ctx, tx, manual, place)
	if err != nil {
		return err
	}
	assignNumbers(rows)
	return writeNumbers(ctx, tx, rows)
}

// loadNumRows reads every channel with what decides its number.
func loadNumRows(ctx context.Context, tx *sql.Tx, manual map[int64]int, place map[int64]int64) ([]*numRow, error) {
	q, err := tx.QueryContext(ctx, `SELECT id, number, kind, user_id IS NOT NULL, enabled, number_manual FROM channels`)
	if err != nil {
		return nil, err
	}
	var rows []*numRow
	byID := map[int64]*numRow{}
	for q.Next() {
		r := &numRow{}
		if err := q.Scan(&r.id, &r.number, &r.kind, &r.personal, &r.enabled, &r.storedManual); err != nil {
			q.Close()
			return nil, err
		}
		r.place = r.number
		if p, ok := place[r.id]; ok {
			r.place = p
		}
		if n, ok := manual[r.id]; ok {
			r.manual = int64(n)
		} else if r.storedManual {
			r.manual = r.number
		}
		rows = append(rows, r)
		byID[r.id] = r
	}
	q.Close()
	if err := q.Err(); err != nil {
		return nil, err
	}
	// an external channel's place: its first entry by source position, then
	// playlist position (present entries before gone ones)
	e, err := tx.QueryContext(ctx, `SELECT m.channel_id, s.position, s.id, m.position, m.present FROM iptv_members m
		JOIN iptv_sources s ON s.id = m.source_id WHERE m.channel_id IS NOT NULL
		ORDER BY m.present DESC, s.position, s.id, m.position`)
	if err != nil {
		return nil, err
	}
	defer e.Close()
	for e.Next() {
		var ch, sp, sid, mp int64
		var present bool
		if err := e.Scan(&ch, &sp, &sid, &mp, &present); err != nil {
			return nil, err
		}
		if r := byID[ch]; r != nil && !r.hasEntry {
			r.src, r.srcID, r.pos, r.hasEntry = sp, sid, mp, true
		}
	}
	return rows, e.Err()
}

// assignNumbers sets every row's final number: the admin's first (the
// first channel by id to ask for a number gets it), the others 1…N round
// them in the order of the list above.
func assignNumbers(rows []*numRow) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	taken := map[int64]bool{}
	for _, r := range rows {
		if r.manual > 0 && !taken[r.manual] {
			taken[r.manual] = true
			r.final, r.finalManual = r.manual, true
		}
	}
	block := func(r *numRow) int {
		switch {
		case r.kind == KindExternal:
			return 2
		case r.personal:
			return 1
		}
		return 0
	}
	auto := make([]*numRow, 0, len(rows))
	for _, r := range rows {
		if !r.finalManual {
			auto = append(auto, r)
		}
	}
	sort.SliceStable(auto, func(i, j int) bool {
		a, b := auto[i], auto[j]
		if a.enabled != b.enabled {
			return a.enabled // the seen ones are 1…N, the hidden ones after
		}
		if ba, bb := block(a), block(b); ba != bb {
			return ba < bb
		}
		if a.kind == KindExternal && a.hasEntry != b.hasEntry {
			return a.hasEntry
		}
		if a.kind == KindExternal && a.hasEntry {
			if a.src != b.src {
				return a.src < b.src
			}
			if a.srcID != b.srcID {
				return a.srcID < b.srcID
			}
			if a.pos != b.pos {
				return a.pos < b.pos
			}
		}
		if a.place != b.place {
			return a.place < b.place
		}
		return a.id < b.id
	})
	next := int64(1)
	for _, r := range auto {
		for taken[next] {
			next++
		}
		r.final = next
		taken[next] = true
	}
}

// writeNumbers stores the numbers that changed. Numbers are UNIQUE: the
// changed rows step aside first (-id is free).
func writeNumbers(ctx context.Context, tx *sql.Tx, rows []*numRow) error {
	var changed []*numRow
	for _, r := range rows {
		if r.final != r.number || r.finalManual != r.storedManual {
			changed = append(changed, r)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	aside, err := tx.PrepareContext(ctx, `UPDATE channels SET number=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer aside.Close()
	for _, r := range changed {
		if r.final != r.number {
			if _, err := aside.ExecContext(ctx, -r.id, r.id); err != nil {
				return err
			}
		}
	}
	set, err := tx.PrepareContext(ctx, `UPDATE channels SET number=?, number_manual=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer set.Close()
	for _, r := range changed {
		if _, err := set.ExecContext(ctx, r.final, r.finalManual, r.id); err != nil {
			return err
		}
	}
	return nil
}

// makeRoom moves an automatic channel off number n (to -id, Renumber gives
// it a number again) so a manual n can be written; false: an admin's
// number holds n.
func makeRoom(ctx context.Context, tx *sql.Tx, n int, self int64) (place map[int64]int64, ok bool, err error) {
	var id int64
	var manual bool
	err = tx.QueryRowContext(ctx, `SELECT id, number_manual FROM channels WHERE number=? AND id<>?`, n, self).Scan(&id, &manual)
	if err == sql.ErrNoRows {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if manual {
		return nil, false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channels SET number=? WHERE id=?`, -id, id); err != nil {
		return nil, false, err
	}
	return map[int64]int64{id: int64(n)}, true, nil
}
