package progress

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Annotation kinds.
const (
	KindBookmark  = "bookmark"
	KindHighlight = "highlight"
	KindNote      = "note"
)

// Colors are the four presets of a highlight or a note; the first is the default.
var Colors = []string{"yellow", "green", "blue", "pink"}

// Limits of an annotation's texts (in characters). A longer excerpt is cut
// (a queued offline write must not be refused for it); a longer note is refused.
const (
	MaxExcerpt = 1000
	MaxNote    = 10_000
	maxLabel   = 200
	maxLocator = 4096
)

// maxPerItem caps a profile's annotations in one item (tombstones not counted).
var maxPerItem = 5000

// ErrBadAnnotation is wrapped by every validation error of PutAnnotation;
// errors.As to *AnnotationError gives the (Russian, catalog) message.
var ErrBadAnnotation = errors.New("progress: bad annotation")

// AnnotationError says what is wrong with an annotation (a catalog key).
type AnnotationError struct{ Msg string }

func (e *AnnotationError) Error() string { return e.Msg }
func (e *AnnotationError) Unwrap() error { return ErrBadAnnotation }

func badAnnotation(msg string) error { return &AnnotationError{Msg: msg} }

// Annotation is a bookmark, highlight or note of a profile in an item. See
// migration 0060 for the fields.
//
// In a write, Rev is the revision the client made the change on (0 for a
// new one) and UpdatedAt the device's time of the change, a hint only. As
// stored, UpdatedAt is the server's time of the last accepted write and Rev
// its revision (the profile's sequence).
type Annotation struct {
	ID        string
	ItemID    int64
	Kind      string
	Locator   string
	Fraction  float64
	Label     string
	Excerpt   string
	Note      string
	Color     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Rev       int64
	Deleted   bool
}

var annotationID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// normalize checks a and fills the defaults (the color of a highlight, none for a bookmark).
func (a *Annotation) normalize() error {
	if !annotationID.MatchString(a.ID) {
		return badAnnotation("Неверный идентификатор заметки")
	}
	switch a.Kind {
	case KindBookmark:
		a.Color = ""
	case KindHighlight, KindNote:
		if a.Color == "" {
			a.Color = Colors[0]
		}
		ok := false
		for _, c := range Colors {
			ok = ok || c == a.Color
		}
		if !ok {
			return badAnnotation("Неверный цвет выделения")
		}
	default:
		return badAnnotation("Неверный вид заметки")
	}
	a.Locator = strings.TrimSpace(a.Locator)
	if a.Locator == "" {
		return badAnnotation("Не передана позиция")
	}
	if len(a.Locator) > maxLocator {
		return badAnnotation("Слишком длинная позиция")
	}
	if utf8.RuneCountInString(a.Note) > MaxNote {
		return badAnnotation("Слишком длинная заметка")
	}
	if !(a.Fraction > 0) || math.IsInf(a.Fraction, 0) { // NaN too
		a.Fraction = 0
	}
	a.Fraction = min(a.Fraction, 1)
	a.Excerpt = cut(a.Excerpt, MaxExcerpt)
	a.Label = cut(strings.TrimSpace(a.Label), maxLabel)
	return nil
}

const selectAnnotation = `SELECT id, item_id, kind, locator, fraction, label, excerpt, note, color, created_at, updated_at, rev, deleted, client_at FROM annotations `

// scanAnnotation reads a row and its client_at (the device time of its last accepted write).
func scanAnnotation(r scanner) (Annotation, string, error) {
	var a Annotation
	var created, updated, clientAt string
	var del int
	if err := r.Scan(&a.ID, &a.ItemID, &a.Kind, &a.Locator, &a.Fraction, &a.Label, &a.Excerpt, &a.Note, &a.Color, &created, &updated, &a.Rev, &del, &clientAt); err != nil {
		return a, "", err
	}
	a.Deleted = del != 0
	var err error
	if a.CreatedAt, err = parseTS(created); err != nil {
		return a, "", err
	}
	a.UpdatedAt, err = parseTS(updated)
	return a, clientAt, err
}

// Annotations are the profile's annotations in the item, tombstones
// included (a client drops its copies of those), in reading order.
func (s *Store) Annotations(ctx context.Context, userID, itemID int64) ([]Annotation, error) {
	rows, err := s.db.QueryContext(ctx, selectAnnotation+`WHERE user_id=? AND item_id=? ORDER BY fraction, created_at, id`, userID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Annotation{}
	for rows.Next() {
		a, _, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) annotation(ctx context.Context, q rowQueryer, userID, itemID int64, id string) (Annotation, string, error) {
	return scanAnnotation(q.QueryRowContext(ctx, selectAnnotation+`WHERE user_id=? AND item_id=? AND id=?`, userID, itemID, id))
}

// applies says whether a write made on revision a.Rev (device time
// clientAt) replaces the stored row cur (device time curAt). On the stored
// revision (or a newer one) it does. On an older one, another device wrote
// meanwhile: a delete still wins, a tombstone always stays, and over
// another edit the later device time wins (a hint, the only use of device
// clocks).
func applies(a Annotation, clientAt string, cur Annotation, curAt string) bool {
	switch {
	case cur.Deleted:
		return a.Deleted && a.Rev >= cur.Rev // nothing brings a deleted one back
	case a.Rev >= cur.Rev, a.Deleted:
		return true
	default:
		return clientAt != "" && clientAt > curAt
	}
}

// PutAnnotation creates, changes or (a.Deleted) deletes an annotation and
// returns the row as stored, which the client adopts. Order is the
// server's: an accepted write gets the server's time and the profile's next
// revision, never the device's clock (see applies for a write made on an
// older revision). A delete clears the texts.
func (s *Store) PutAnnotation(ctx context.Context, userID, itemID int64, a Annotation) (Annotation, error) {
	if err := a.normalize(); err != nil {
		return Annotation{}, err
	}
	return s.putAnnotation(ctx, userID, itemID, a)
}

func (s *Store) putAnnotation(ctx context.Context, userID, itemID int64, a Annotation) (Annotation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Annotation{}, err
	}
	defer tx.Rollback()
	now := s.Now().UTC()
	clientAt := ""
	if !a.UpdatedAt.IsZero() {
		clientAt = a.UpdatedAt.UTC().Format(tsLayout)
	}
	cur, curAt, err := s.annotation(ctx, tx, userID, itemID, a.ID)
	switch {
	case err == nil:
		if !applies(a, clientAt, cur, curAt) {
			return cur, tx.Commit()
		}
		a.CreatedAt = cur.CreatedAt
	case errors.Is(err, sql.ErrNoRows):
		a.CreatedAt = now
		if !a.Deleted {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM annotations WHERE user_id=? AND item_id=? AND deleted=0`, userID, itemID).Scan(&n); err != nil {
				return Annotation{}, err
			}
			if n >= maxPerItem {
				return Annotation{}, badAnnotation("Слишком много заметок в этой книге")
			}
		}
	default:
		return Annotation{}, err
	}
	del := 0
	if a.Deleted {
		del = 1
		a.Excerpt, a.Note, a.Label = "", "", ""
	}
	var rev int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rev),0)+1 FROM annotations WHERE user_id=?`, userID).Scan(&rev); err != nil {
		return Annotation{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO annotations(user_id,item_id,id,kind,locator,fraction,label,excerpt,note,color,created_at,updated_at,client_at,rev,deleted)
  VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
  ON CONFLICT(user_id,item_id,id) DO UPDATE SET kind=excluded.kind, locator=excluded.locator, fraction=excluded.fraction,
  label=excluded.label, excerpt=excluded.excerpt, note=excluded.note, color=excluded.color, updated_at=excluded.updated_at,
  client_at=excluded.client_at, rev=excluded.rev, deleted=excluded.deleted`,
		userID, itemID, a.ID, a.Kind, a.Locator, a.Fraction, a.Label, a.Excerpt, a.Note, a.Color,
		a.CreatedAt.Format(tsLayout), now.Format(tsLayout), clientAt, rev, del)
	if err != nil {
		return Annotation{}, err
	}
	got, _, err := s.annotation(ctx, tx, userID, itemID, a.ID)
	if err != nil {
		return Annotation{}, err
	}
	return got, tx.Commit()
}

// DeleteAnnotation leaves a tombstone of the annotation (a delete always
// wins). One that is not there (made and deleted on a device that was
// offline) gets a tombstone too, so its late create cannot land.
func (s *Store) DeleteAnnotation(ctx context.Context, userID, itemID int64, id string) (Annotation, error) {
	if !annotationID.MatchString(id) {
		return Annotation{}, badAnnotation("Неверный идентификатор заметки")
	}
	a, _, err := s.annotation(ctx, s.db, userID, itemID, id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		a = Annotation{ID: id, Kind: KindBookmark, Locator: "-"}
	case err != nil:
		return Annotation{}, err
	}
	a.Deleted, a.UpdatedAt = true, time.Time{}
	return s.putAnnotation(ctx, userID, itemID, a)
}
