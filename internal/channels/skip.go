package channels

import (
	"context"
	"database/sql"
	"sort"

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
// pieces of its file with their offsets). A movie, a title outside the
// library, an episode of several files or one whose segments are not
// known yet stays as it is (the next build sees segments found later).
func skipCut(ctx context.Context, tx *sql.Tx, prog Prog) (Prog, error) {
	if prog.ExtraID != 0 || prog.ItemID <= 0 || len(prog.Parts) != 1 {
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
	part := prog.Parts[0]
	rows, err := tx.QueryContext(ctx, `SELECT kind, source, start_ms, end_ms FROM file_segments WHERE file_id=?`, part.FileID)
	if err != nil {
		return prog, err
	}
	var stored []segments.Segment
	for rows.Next() {
		var g segments.Segment
		if err := rows.Scan(&g.Kind, &g.Source, &g.StartMs, &g.EndMs); err != nil {
			rows.Close()
			return prog, err
		}
		stored = append(stored, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return prog, err
	}
	pieces := keptPieces(segments.Pick(stored, part.DurationMs, false), part.DurationMs)
	if pieces == nil {
		return prog, nil
	}
	parts := make([]Part, 0, len(pieces))
	for _, p := range pieces {
		parts = append(parts, Part{FileID: part.FileID, OffsetMs: p.start, DurationMs: p.end - p.start})
	}
	prog.Parts = parts
	return prog, nil
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
