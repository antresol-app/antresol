package channels

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/antresol-app/antresol/internal/segments"
)

// Skipping openings and credits on a channel (Rules.SkipSegments): an
// episode plays without its recap and intro, and ends where its credits
// start. The rules are the web player's (web/src/player/skipSegments.ts):
// segments.Pick's segments (nothing under 3 s), and credits followed by
// more than postCreditsMs of the file (a scene after them) are kept.
const (
	// postCreditsMs: credits ending this long before the file does are
	// followed by a scene; they stay.
	postCreditsMs = 20_000
	// adjacentMs: a recap and an intro this close are one cut.
	adjacentMs = 2_000
	// minPieceMs: a shorter piece left between cuts is dropped.
	minPieceMs = 2_000
	// minKeptPct: a cut leaving less of the episode than this (percent)
	// trusts the segments too much: the episode plays whole.
	minKeptPct = 50
)

// recutLead: Recut keeps a programme starting this soon (the live
// encoder runs a few segments ahead of the clock).
const recutLead = time.Minute

// span is a stretch of a file, in ms.
type span struct{ start, end int64 }

// keptPieces is what of a file of durMs plays once segs (segments.Pick's,
// in seconds) are cut: [0, recap/intro) + [intro end, credits start). nil:
// the whole file (nothing to cut, or a cut not to trust).
func keptPieces(segs []segments.Play, durMs int64) []span {
	var cuts []span
	end := durMs
	for _, s := range segs {
		sp := span{int64(s.StartSec * 1000), int64(s.EndSec * 1000)}
		switch s.Kind {
		case segments.Recap, segments.Intro:
			cuts = append(cuts, sp)
		case segments.Credits:
			if durMs-sp.end <= postCreditsMs && sp.start < end {
				end = sp.start
			}
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].start < cuts[j].start })
	// a recap right before the intro (or overlapping it) is one cut
	var merged []span
	for _, c := range cuts {
		if n := len(merged); n > 0 && c.start <= merged[n-1].end+adjacentMs {
			merged[n-1].end = max(merged[n-1].end, c.end)
			continue
		}
		merged = append(merged, c)
	}
	var out []span
	var kept int64
	at := int64(0)
	for _, c := range append(merged, span{end, durMs}) {
		if c.start > end {
			c.start = end
		}
		if c.start-at >= minPieceMs {
			out = append(out, span{at, c.start})
			kept += c.start - at
		}
		at = max(at, c.end)
		if at >= end {
			break
		}
	}
	if len(out) == 0 || kept*100 < durMs*minKeptPct || (len(out) == 1 && out[0] == span{0, durMs}) {
		return nil
	}
	return out
}

// skipCut is prog with its episode's openings and credits cut (Parts:
// pieces of its files with their offsets). An episode in several files
// (parts) is cut file by file, each by its own segments, as the player
// skips them in each file it plays. A movie, a title outside the library
// or an episode whose segments are not known yet stays as it is (Recut
// redoes the schedule not yet on air when segments are found later).
func skipCut(ctx context.Context, tx *sql.Tx, prog Prog) (Prog, error) {
	if prog.ExtraID != 0 || prog.ItemID <= 0 || len(prog.Parts) == 0 {
		return prog, nil
	}
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM items WHERE id=?`, prog.ItemID).Scan(&kind); err != nil {
		if err == sql.ErrNoRows {
			return prog, nil
		}
		return prog, err
	}
	if kind != "episode" {
		return prog, nil
	}
	parts := make([]Part, 0, len(prog.Parts))
	cut := false
	for _, part := range prog.Parts {
		stored, err := fileSegments(ctx, tx, part.FileID)
		if err != nil {
			return prog, err
		}
		pieces := keptPieces(segments.Pick(stored, part.DurationMs, false), part.DurationMs)
		if pieces == nil {
			parts = append(parts, part)
			continue
		}
		cut = true
		for _, p := range pieces {
			parts = append(parts, Part{FileID: part.FileID, OffsetMs: p.start, DurationMs: p.end - p.start})
		}
	}
	if cut {
		prog.Parts = parts
	}
	return prog, nil
}

// fileSegments are the stored segments of a file (every source).
func fileSegments(ctx context.Context, tx *sql.Tx, fileID int64) ([]segments.Segment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT kind, source, start_ms, end_ms FROM file_segments WHERE file_id=?`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stored []segments.Segment
	for rows.Next() {
		var g segments.Segment
		if err := rows.Scan(&g.Kind, &g.Source, &g.StartMs, &g.EndMs); err != nil {
			return nil, err
		}
		stored = append(stored, g)
	}
	return stored, rows.Err()
}

// SameProgramme: entry next continues the programme that began with entry
// first and has reached entry prev (the next file of a movie in parts, the
// next piece of an episode with its intro cut), not a new programme of the
// same item (a repeat starts with its first file again, at its first
// offset).
func SameProgramme(first, prev, next Entry) bool {
	if next.ItemID != first.ItemID || !next.Start.Equal(prev.End) {
		return false
	}
	if first.FileID == 0 || next.FileID != first.FileID {
		return true
	}
	return next.FileID == prev.FileID && next.OffsetMs > prev.OffsetMs
}

// Recut makes channel c's schedule again from its first programme not on
// air yet that the files' segments (found, changed or dropped since the
// schedule was built) would cut otherwise than its rows do: segment
// analysis runs after a scan, long after the next 48 h were built with
// those episodes whole. What is on air keeps playing as it was (its later
// pieces too, as CutAfterCurrent keeps them). The rows from there are
// dropped and the build numbers the new ones on from the last kept, as
// every rebuild does (the apps' guides take the newer page). A channel
// without Rules.SkipSegments has nothing to redo. Reports whether it cut.
func (b *Builder) Recut(ctx context.Context, c Channel, files []int64) (bool, error) {
	if !c.Rules.SkipSegments || c.External() || len(files) == 0 {
		return false, nil
	}
	changed := make(map[int64]bool, len(files))
	for _, f := range files {
		changed[f] = true
	}
	// most changes are of titles this channel does not air soon: no pool
	// to build for them
	var found bool
	for part := range chunks(files, 500) {
		in, args := inArgs(part)
		var one int
		err := b.Store.DB.QueryRowContext(ctx, `SELECT 1 FROM channel_playout WHERE channel_id=? AND end_ms > ? AND extra_id IS NULL AND file_id IN `+in+` LIMIT 1`,
			append([]any{c.ID, ms(b.Store.now())}, args...)...).Scan(&one)
		if err == nil {
			found = true
			break
		}
		if err != sql.ErrNoRows {
			return false, err
		}
	}
	if !found {
		return false, nil
	}
	cut := false
	err := b.buildWith(ctx, c, func(tx *sql.Tx, now time.Time) error {
		seq, err := recutFrom(ctx, tx, c.ID, changed, now)
		if err != nil || seq == 0 {
			return err
		}
		cut = true
		_, err = tx.ExecContext(ctx, `DELETE FROM channel_playout WHERE channel_id=? AND seq >= ?`, c.ID, seq)
		return err
	})
	return cut && err == nil, err
}

// recutFrom is the first seq of the first programme starting after now
// with a file of changed whose rows skipCut would make otherwise (0: none).
func recutFrom(ctx context.Context, tx *sql.Tx, id int64, changed map[int64]bool, now time.Time) (int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+entryCols+` FROM channel_playout WHERE channel_id=? AND end_ms > ? ORDER BY seq`, id, ms(now))
	if err != nil {
		return 0, err
	}
	var progs [][]Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		if n := len(progs); n > 0 {
			p := progs[n-1]
			if SameProgramme(p[0], p[len(p)-1], e) {
				progs[n-1] = append(p, e)
				continue
			}
		}
		progs = append(progs, []Entry{e})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for i, p := range progs {
		if i == 0 && !p[0].Start.After(now) {
			continue // on air: it plays to its end as it began
		}
		if p[0].Start.Before(now.Add(recutLead)) {
			continue // the encoder may already be into it
		}
		if p[0].ExtraID != 0 || !touches(p, changed) {
			continue
		}
		prog, ok, err := wholeProg(ctx, tx, p)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue // a file went: CutMissing's
		}
		if prog, err = skipCut(ctx, tx, prog); err != nil {
			return 0, err
		}
		if !sameParts(p, prog.Parts) {
			return p[0].Seq, nil
		}
	}
	return 0, nil
}

func touches(p []Entry, changed map[int64]bool) bool {
	for _, e := range p {
		if changed[e.FileID] {
			return true
		}
	}
	return false
}

// wholeProg is the programme of rows p before any cut: each of its files
// once, whole (false: a file is missing or has no duration).
func wholeProg(ctx context.Context, tx *sql.Tx, p []Entry) (Prog, bool, error) {
	prog := Prog{ItemID: p[0].ItemID, UnitID: p[0].UnitID}
	for i, e := range p {
		if i > 0 && e.FileID == p[i-1].FileID {
			continue
		}
		var dur int64
		err := tx.QueryRowContext(ctx, `SELECT COALESCE(duration_ms,0) FROM files WHERE id=? AND missing_since IS NULL`, e.FileID).Scan(&dur)
		if err == sql.ErrNoRows || (err == nil && dur <= 0) {
			return prog, false, nil
		}
		if err != nil {
			return prog, false, err
		}
		prog.Parts = append(prog.Parts, Part{FileID: e.FileID, DurationMs: dur})
	}
	return prog, true, nil
}

// sameParts: rows p are exactly parts, piece by piece.
func sameParts(p []Entry, parts []Part) bool {
	if len(p) != len(parts) {
		return false
	}
	for i, e := range p {
		if e.FileID != parts[i].FileID || e.OffsetMs != parts[i].OffsetMs || e.End.Sub(e.Start).Milliseconds() != parts[i].DurationMs {
			return false
		}
	}
	return true
}
