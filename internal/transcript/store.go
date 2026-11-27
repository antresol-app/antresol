package transcript

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The states of a stored transcript.
const (
	// StatePending: being made (or waiting its turn).
	StatePending = "pending"
	// StateReady: complete.
	StateReady = "ready"
	// StateFailed: the last try failed (tried again later, or given up on).
	StateFailed = "failed"
)

// File is a book's file as a transcript sees it.
type File struct {
	ID, Size, Mtime int64
	DurationMs      int64
}

// Signature names a set of files and what each held: a transcript is good
// while its book's is the same. The order of the files does not count.
func Signature(files []File) string {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b File) int { return cmp.Compare(a.ID, b.ID) })
	parts := make([]string, len(sorted))
	for i, f := range sorted {
		parts[i] = strconv.FormatInt(f.ID, 10) + ":" + strconv.FormatInt(f.Size, 10) + ":" + strconv.FormatInt(f.Mtime, 10)
	}
	return strings.Join(parts, ";")
}

// partOf: every file of sig is one of the stored signature's, unchanged
// (some of them may be away).
func partOf(sig, stored string) bool {
	have := map[string]bool{}
	for _, p := range strings.Split(stored, ";") {
		have[p] = true
	}
	for _, p := range strings.Split(sig, ";") {
		if p != "" && !have[p] {
			return false
		}
	}
	return true
}

// Stored is a book's transcript as stored.
type Stored struct {
	// Signature of the files it was made from.
	Signature string
	State     string
	// Language is the ISO 639-1 code of the speech ("" unknown).
	Language string
	// Chapters: automatic chapters were made.
	Chapters bool
}

// Get is the stored transcript of a book; false: none.
func Get(ctx context.Context, db *sql.DB, itemID int64) (Stored, bool, error) {
	var s Stored
	err := db.QueryRowContext(ctx, `SELECT signature, state, language, chapters_done FROM transcripts WHERE item_id=?`, itemID).
		Scan(&s.Signature, &s.State, &s.Language, &s.Chapters)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	return s, err == nil, err
}

// FileSegment is a segment with the file it is in (times within the file).
type FileSegment struct {
	FileID int64
	Segment
}

// Segments are a book's stored segments.
func Segments(ctx context.Context, db *sql.DB, itemID int64) ([]FileSegment, error) {
	rows, err := db.QueryContext(ctx, `SELECT file_id, segments FROM transcript_pieces WHERE item_id=? ORDER BY file_id, start_ms`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileSegment
	for rows.Next() {
		var file int64
		var raw []byte
		if err := rows.Scan(&file, &raw); err != nil {
			return nil, err
		}
		var segs []Segment
		if err := json.Unmarshal(raw, &segs); err != nil {
			return nil, err
		}
		for _, s := range segs {
			out = append(out, FileSegment{file, s})
		}
	}
	return out, rows.Err()
}

// onTimeline places segments on the book's timeline by their files' starts
// (offsets), in order; a file without one is left out.
func onTimeline(segs []FileSegment, offsets map[int64]int64) []timed {
	out := make([]timed, 0, len(segs))
	for _, s := range segs {
		off, ok := offsets[s.FileID]
		if !ok {
			continue
		}
		out = append(out, timed{Segment{off + s.StartMs, off + s.EndMs, s.Text}, s.FileID, s.StartMs})
	}
	slices.SortStableFunc(out, func(a, b timed) int { return cmp.Compare(a.StartMs, b.StartMs) })
	return out
}

// Absolute is a book's transcript in milliseconds from the start of the
// book: offsets are where each file starts in it.
func Absolute(segs []FileSegment, offsets map[int64]int64) []Segment {
	t := onTimeline(segs, offsets)
	out := make([]Segment, len(t))
	for i := range t {
		out[i] = t[i].Segment
	}
	return out
}

// Chapter is an automatic chapter: StartMs is within its file.
type Chapter struct {
	Idx     int
	Title   string
	FileID  int64
	StartMs int64
}

// AutoChapters are a book's automatic chapters, in order.
func AutoChapters(ctx context.Context, db *sql.DB, itemID int64) ([]Chapter, error) {
	rows, err := db.QueryContext(ctx, `SELECT idx, title, file_id, start_ms FROM auto_chapters WHERE item_id=? ORDER BY idx`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chapter
	for rows.Next() {
		var c Chapter
		if err := rows.Scan(&c.Idx, &c.Title, &c.FileID, &c.StartMs); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func saveChapters(ctx context.Context, db *sql.DB, itemID int64, chs []Chapter, at time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM auto_chapters WHERE item_id=?`, itemID); err != nil {
		return err
	}
	for i, c := range chs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO auto_chapters(item_id, idx, title, file_id, start_ms) VALUES(?,?,?,?,?)`,
			itemID, i, c.Title, c.FileID, c.StartMs); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE transcripts SET chapters_done=1, chapters_fails=0, chapters_retry=NULL, chapters_error='', updated_at=?
		WHERE item_id=?`, stamp(at), itemID); err != nil {
		return err
	}
	return tx.Commit()
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
