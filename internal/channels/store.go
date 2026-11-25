// Package channels makes live TV channels of the library («ТВ-каналы»):
// channels and their rules (this file), the pool of titles a channel's
// rules take (pool.go), the materialised schedule (builder.go), the
// generated channels (generate.go), the live HLS encoder shared by every
// viewer of a channel (live.go) and the M3U playlist, XMLTV guide and
// logos IPTV players read (m3u.go, xmltv.go, logo.go).
package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// Orders of a channel.
const (
	// OrderShuffle picks a random title each time (no quick repeats); a
	// series plays its next episode in order.
	OrderShuffle = "shuffle"
	// OrderOrdered plays every title in order (series by season and
	// episode), round and round.
	OrderOrdered = "ordered"
)

// Rules are what a channel plays (channels.rules_json).
type Rules struct {
	// Kinds: library.KindMovie and/or library.KindSeries (empty: both).
	Kinds []string `json:"kinds,omitempty"`
	// Genres: any of them (stored names, either language).
	Genres []string `json:"genres,omitempty"`
	// NotGenres: none of them (stored names, either language).
	NotGenres []string `json:"notGenres,omitempty"`
	// YearFrom, YearTo bound the title's year (0: open).
	YearFrom int `json:"yearFrom,omitempty"`
	YearTo   int `json:"yearTo,omitempty"`
	// Countries: made in any of them (ISO 3166-1, «SU» the Soviet Union).
	Countries []string `json:"countries,omitempty"`
	// Languages: the title's original language is any of them (ISO 639-1).
	Languages []string `json:"languages,omitempty"`
	// Keywords: themes, TMDB's keywords (English, lower case); any of them.
	Keywords []string `json:"keywords,omitempty"`
	// Picks: what the channel plays, in this order (titles, single
	// movies or episodes, library folders); when set they are the whole
	// pool and the filters above are ignored.
	Picks []Pick `json:"picks,omitempty"`
	// Titles: movies and series by item id (round 1). Still read: a
	// channel's titles become picks of kind PickTitle when it is read or
	// written, so nothing below the store sees them.
	Titles []int64 `json:"titles,omitempty"`
	// LibraryUser: only the titles that profile brought (item_owners);
	// 0: the whole library.
	LibraryUser int64 `json:"libraryUser,omitempty"`
	// MaxRating: the channel's age limit ("" = none). A limited profile
	// sees the channel only when it is within the profile's limit.
	MaxRating string `json:"maxRating,omitempty"`
	// Order: OrderShuffle (default) or OrderOrdered.
	Order string `json:"order,omitempty"`
	// AudioLang: the audio language the channel prefers (ISO 639, "" =
	// Russian, then the file's default track).
	AudioLang string `json:"audioLang,omitempty"`
	// Extras: also titles of these genres and years the library does not
	// have, from outside it (Service.Extras; off by default, and nothing
	// without an implementation). See ExtrasOn.
	Extras bool `json:"extras,omitempty"`
	// SkipSegments: an episode plays without its recap and intro and ends
	// where its credits start (skip.go); generated series channels have it
	// on. Movies always play whole.
	SkipSegments bool `json:"skipSegments,omitempty"`
	// Fetch: the channel plays these titles in order, the ones not on
	// disk fetched by an addon before they air and removed after
	// (fetch.go); when set it is the whole pool (picks, filters and
	// extras are ignored) and the order is OrderOrdered. Admins only.
	Fetch *FetchRules `json:"fetch,omitempty"`
}

// Channel is a row of channels.
type Channel struct {
	ID       int64  `json:"id"`
	Number   int    `json:"number"`
	Name     string `json:"name"`
	Category string `json:"category"`
	// UserID: 0 = shared, else the profile whose personal channel it is.
	UserID  int64  `json:"userId,omitempty"`
	Rules   Rules  `json:"rules"`
	AutoKey string `json:"autoKey,omitempty"`
	Enabled bool   `json:"enabled"`
	Edited  bool   `json:"edited"`
	Seed    int64  `json:"-"`
	// LogoVersion: the uploaded logo's version (0: none, the generated
	// tile stands for the channel).
	LogoVersion int64 `json:"-"`
	// LogoAuto: how the logo came (LogoAutoNone, LogoAutoFound,
	// LogoAutoRemoved; logo_auto.go).
	LogoAuto int `json:"-"`
	// Kind: KindAntresol (made of the library) or KindExternal (a channel
	// of an external IPTV playlist, internal/iptv: never scheduled or
	// encoded here, seen through the viewer's set only).
	Kind string `json:"-"`
	// NumberManual: an admin set the number (automatic numbering of
	// external channels leaves it).
	NumberManual bool `json:"-"`
	// Why: what decided an external channel's visibility (an i18n key).
	Why string `json:"-"`
}

// Kinds of a channel row.
const (
	KindAntresol = "antresol"
	KindExternal = "external"
)

// External: c is a channel of an external playlist.
func (c Channel) External() bool { return c.Kind == KindExternal }

var (
	// ErrNotFound: no such channel.
	ErrNotFound = errors.New("channels: not found")
	// ErrInvalid: a channel the store refuses (its message says why, in Russian).
	ErrInvalid = errors.New("channels: invalid")
	// ErrNumberTaken: another channel has this number.
	ErrNumberTaken = errors.New("channels: number taken")
)

// Invalid is a refused channel with its reason (Russian, for the UI).
type Invalid struct{ Msg string }

func (e *Invalid) Error() string        { return e.Msg }
func (e *Invalid) Is(target error) bool { return target == ErrInvalid }

// Store keeps channels and their schedules in the database.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

const channelCols = `id, number, name, category, COALESCE(user_id,0), rules_json, COALESCE(auto_key,''), enabled, edited, seed, logo_version, kind, number_manual, why, logo_auto`

type rowScanner interface{ Scan(...any) error }

// dbtx is what a *sql.DB and a *sql.Tx both are.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// setExtrasAt records when channel id's titles outside the library were
// asked for last (0: asked again at the next build).
func setExtrasAt(ctx context.Context, q dbtx, id, at int64) error {
	_, err := q.ExecContext(ctx, `UPDATE channels SET extras_at_ms=? WHERE id=?`, at, id)
	return err
}

// cutFrom drops channel ch's schedule from entry seq on (the builder
// fills it again).
func cutFrom(ctx context.Context, q dbtx, ch, seq int64) error {
	_, err := q.ExecContext(ctx, `DELETE FROM channel_playout WHERE channel_id=? AND seq >= ?`, ch, seq)
	return err
}

func scanChannel(r rowScanner) (Channel, error) {
	var c Channel
	var rules string
	err := r.Scan(&c.ID, &c.Number, &c.Name, &c.Category, &c.UserID, &rules, &c.AutoKey, &c.Enabled, &c.Edited, &c.Seed, &c.LogoVersion, &c.Kind, &c.NumberManual, &c.Why, &c.LogoAuto)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(rules), &c.Rules); err != nil {
		return c, fmt.Errorf("channels: rules of %d: %w", c.ID, err)
	}
	liftTitles(&c.Rules)
	return c, nil
}

// Get returns one channel.
func (s *Store) Get(ctx context.Context, id int64) (Channel, error) {
	return scanChannel(s.DB.QueryRowContext(ctx, `SELECT `+channelCols+` FROM channels WHERE id=?`, id))
}

// All returns every channel (enabled or not), by number.
func (s *Store) All(ctx context.Context) ([]Channel, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+channelCols+` FROM channels ORDER BY number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Viewer is who asks: the profile, its age limit ("" = none) and whether
// it is an admin.
type Viewer struct {
	ID        int64
	MaxRating string
	Admin     bool
}

// VisibleTo reports whether v sees c in its channel list: a shared channel
// or v's own, enabled, and within v's age limit (a limited profile sees
// only channels limited to its limit or lower). An external channel is
// never visible this way: its viewers come from internal/iptv's sets.
func VisibleTo(c Channel, v Viewer) bool {
	if c.External() || !c.Enabled || (c.UserID != 0 && c.UserID != v.ID) {
		return false
	}
	return WithinLimit(c.Rules.MaxRating, v.MaxRating)
}

// WithinLimit: a channel limited to channelMax may be shown to a profile
// limited to viewerMax ("" = unlimited on either side).
func WithinLimit(channelMax, viewerMax string) bool {
	if viewerMax == "" {
		return true
	}
	if channelMax == "" {
		return false
	}
	c, ok1 := library.RatingRank(channelMax)
	v, ok2 := library.RatingRank(viewerMax)
	return ok1 && ok2 && c <= v
}

// Visible returns the channels v sees, by number.
func (s *Store) Visible(ctx context.Context, v Viewer) ([]Channel, error) {
	all, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, c := range all {
		if VisibleTo(c, v) {
			out = append(out, c)
		}
	}
	return out, nil
}

// CanEdit: an admin edits shared channels and anybody's; a profile edits
// its own personal channels.
func CanEdit(c Channel, v Viewer) bool {
	return v.Admin || (c.UserID != 0 && c.UserID == v.ID)
}

// normalize checks and cleans a channel before it is written.
func normalize(c *Channel) error {
	c.Name = strings.TrimSpace(c.Name)
	c.Category = strings.TrimSpace(c.Category)
	if c.Name == "" {
		return &Invalid{"Укажите название канала"}
	}
	if len([]rune(c.Name)) > 80 || len([]rune(c.Category)) > 80 {
		return &Invalid{"Слишком длинное название"}
	}
	if c.Number < 0 || c.Number > 99999 {
		return &Invalid{"Номер канала — от 1 до 99999"}
	}
	r := &c.Rules
	kinds := r.Kinds[:0:0]
	for _, k := range r.Kinds {
		if k != library.KindMovie && k != library.KindSeries {
			return &Invalid{"Канал показывает только фильмы и сериалы"}
		}
		if !contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	r.Kinds = kinds
	if r.Order == "" {
		r.Order = OrderShuffle
	}
	if r.Order != OrderShuffle && r.Order != OrderOrdered {
		return &Invalid{"Неизвестный порядок показа"}
	}
	if r.YearFrom != 0 && r.YearTo != 0 && r.YearFrom > r.YearTo {
		r.YearFrom, r.YearTo = r.YearTo, r.YearFrom
	}
	if r.MaxRating != "" {
		r.MaxRating = library.NormalizeRating(r.MaxRating)
		if _, ok := library.RatingRank(r.MaxRating); !ok {
			return &Invalid{"Неизвестное возрастное ограничение"}
		}
	}
	r.Genres, r.NotGenres = dedupe(r.Genres), dedupe(r.NotGenres)
	r.Countries = cleanList(r.Countries, strings.ToUpper, 40)
	r.Languages = cleanList(r.Languages, strings.ToLower, 40)
	r.Keywords = cleanList(r.Keywords, strings.ToLower, 40)
	if err := normalizePicks(r); err != nil {
		return err
	}
	if err := normalizeFetch(r); err != nil {
		return err
	}
	if c.UserID != 0 {
		// a personal channel plays its profile's library or the whole one
		if r.LibraryUser != 0 && r.LibraryUser != c.UserID {
			return &Invalid{"Личный канал может брать только вашу медиатеку"}
		}
	}
	return nil
}

// cleanList: trimmed, normalised, once each, at most max of them (nil when none).
func cleanList(list []string, norm func(string) string, max int) []string {
	var out []string
	for _, x := range list {
		if x = norm(strings.TrimSpace(x)); x != "" && len(x) <= 60 && !contains(out, x) && len(out) < max {
			out = append(out, x)
		}
	}
	return out
}

// nullIf0 is v for an SQL argument, NULL when it is the zero value.
func nullIf0[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

// clipRunes is s cut to n runes, tail after a cut.
func clipRunes(s string, n int, tail string) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + tail
	}
	return s
}

func contains[T comparable](s []T, v T) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// NextNumber is the first free number from min up.
func (s *Store) NextNumber(ctx context.Context, min int) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT number FROM channels WHERE number >= ? ORDER BY number`, min)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := min
	for rows.Next() {
		var x int
		if err := rows.Scan(&x); err != nil {
			return 0, err
		}
		if x == n {
			n++
		} else if x > n {
			break
		}
	}
	return n, rows.Err()
}

// ManualFrom is where a new channel's number is looked for: channels are
// one list 1…N, a new one takes the first free number.
const ManualFrom = 1

// Create adds a channel (Number 0: automatic; NumberManual: the admin's
// number, an automatic channel holding it steps aside) and returns it with
// its id and its number after Renumber. A generated channel the owner
// deleted is refused with ErrRemoved.
func (s *Store) Create(ctx context.Context, c Channel) (Channel, error) {
	if err := normalize(&c); err != nil {
		return c, err
	}
	if c.Number == 0 {
		c.NumberManual = false
		n, err := s.NextNumber(ctx, ManualFrom)
		if err != nil {
			return c, err
		}
		c.Number = n
	}
	if c.Seed == 0 {
		c.Seed = rand.Int64N(1<<53) + 1
	}
	rules, _ := json.Marshal(c.Rules)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	if c.AutoKey != "" {
		// checked here, in the writing transaction: a delete that lands
		// while the generator works is not undone by it
		var one int
		switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM channel_removed WHERE auto_key=?`, c.AutoKey).Scan(&one); {
		case err == nil:
			return c, ErrRemoved
		case !errors.Is(err, sql.ErrNoRows):
			return c, err
		}
	}
	var place map[int64]int64
	if c.NumberManual {
		p, ok, err := makeRoom(ctx, tx, c.Number, 0)
		if err != nil {
			return c, err
		}
		if !ok {
			return c, ErrNumberTaken
		}
		place = p
	}
	now := s.now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, `INSERT INTO channels (number, name, category, user_id, rules_json, auto_key, enabled, edited, seed, created_at, updated_at, number_manual)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, c.Number, c.Name, c.Category, nullIf0(c.UserID), string(rules), nullIf0(c.AutoKey), c.Enabled, c.Edited, c.Seed, now, now, c.NumberManual)
	if err != nil {
		if isUnique(err, "number") {
			return c, ErrNumberTaken
		}
		return c, err
	}
	c.ID, _ = res.LastInsertId()
	if err := s.renumbered(ctx, tx, &c, place); err != nil {
		return c, err
	}
	return c, tx.Commit()
}

// renumbered renumbers in tx and reads c's number back.
func (s *Store) renumbered(ctx context.Context, tx *sql.Tx, c *Channel, place map[int64]int64) error {
	if err := RenumberTx(ctx, tx, nil, place); err != nil {
		return err
	}
	return tx.QueryRowContext(ctx, `SELECT number, number_manual FROM channels WHERE id=?`, c.ID).Scan(&c.Number, &c.NumberManual)
}

func isUnique(err error, col string) bool {
	m := err.Error()
	return strings.Contains(m, "UNIQUE") && strings.Contains(m, col)
}

// Update writes c (by id). A changed rule set drops the schedule after the
// programme on air (the builder makes it again).
func (s *Store) Update(ctx context.Context, c Channel) (Channel, error) {
	old, err := s.Get(ctx, c.ID)
	if err != nil {
		return c, err
	}
	if err := normalize(&c); err != nil {
		return c, err
	}
	if c.Number == 0 {
		c.Number = old.Number
	}
	// a number other than the one it has is the admin's: kept from now on
	c.NumberManual = old.NumberManual || c.Number != old.Number
	c.AutoKey, c.Seed = old.AutoKey, old.Seed
	rules, _ := json.Marshal(c.Rules)
	oldRules, _ := json.Marshal(old.Rules)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	var place map[int64]int64
	if c.Number != old.Number {
		p, ok, err := makeRoom(ctx, tx, c.Number, c.ID)
		if err != nil {
			return c, err
		}
		if !ok {
			return c, ErrNumberTaken
		}
		place = p
	}
	_, err = tx.ExecContext(ctx, `UPDATE channels SET number=?, name=?, category=?, user_id=?, rules_json=?, enabled=?, edited=?, updated_at=?, number_manual=? WHERE id=?`,
		c.Number, c.Name, c.Category, nullIf0(c.UserID), string(rules), c.Enabled, c.Edited, s.now().UTC().Format(time.RFC3339), c.NumberManual, c.ID)
	if err != nil {
		if isUnique(err, "number") {
			return c, ErrNumberTaken
		}
		return c, err
	}
	if err := s.renumbered(ctx, tx, &c, place); err != nil {
		return c, err
	}
	if string(rules) != string(oldRules) {
		// other rules, other titles from outside the library: saved or
		// cut together, or a retry would see the rules as they are
		if err := setExtrasAt(ctx, tx, c.ID, 0); err != nil {
			return c, err
		}
		if err := cutAfterCurrent(ctx, tx, s.now(), c.ID); err != nil {
			return c, err
		}
	}
	return c, tx.Commit()
}

// Delete removes a channel with its schedule, cursors, favourites, last-channel
// marks and extras (foreign keys), and numbers the rest again. A generated
// channel's key is remembered (channel_removed) in the same transaction, so
// the generator does not make it again until it is given back (Restore).
// What a channel with fetching had fetched is let go by the fetch pass
// (Service.FetchKick), which reads channel_fetch: no foreign key keeps it.
func (s *Store) Delete(ctx context.Context, id int64) error {
	c, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if c.AutoKey != "" && !c.External() { // an external row's key is the IPTV sync's
		if err := s.remove(ctx, tx, c); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE id=?`, id); err != nil {
		return err
	}
	if err := RenumberTx(ctx, tx, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}

// Drop deletes the generated channel id without remembering its key: the
// generator makes it again once the library has enough for it.
func (s *Store) Drop(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := RenumberTx(ctx, tx, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}

// SetLogo records channel id's logo version (0: none) and how it came
// (LogoAuto…).
func (s *Store) SetLogo(ctx context.Context, id, version int64, auto int) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE channels SET logo_version=?, logo_auto=? WHERE id=?`, version, auto, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CutAfterCurrent drops the schedule after the programme on air (its own
// later parts stay: a movie's next file, an episode's piece after its
// cut intro).
func (s *Store) CutAfterCurrent(ctx context.Context, id int64) error {
	return cutAfterCurrent(ctx, s.DB, s.now(), id)
}

func cutAfterCurrent(ctx context.Context, q dbtx, now time.Time, id int64) error {
	from := ms(now)
	cur, err := entryAt(ctx, q, id, now)
	switch {
	case errors.Is(err, ErrNothing):
		return nil
	case err != nil:
		return err
	}
	if !cur.Start.After(now) {
		rows, err := q.QueryContext(ctx, `SELECT `+entryCols+` FROM channel_playout WHERE channel_id=? AND seq > ? ORDER BY seq LIMIT 16`, id, cur.Seq)
		if err != nil {
			return err
		}
		first, prev := cur, cur
		for rows.Next() {
			e, err := scanEntry(rows)
			if err != nil {
				rows.Close()
				return err
			}
			if !SameProgramme(first, prev, e) {
				break
			}
			prev = e
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		from = max(from, ms(prev.End)-1) // rows starting at prev's end or later go
	}
	_, err = q.ExecContext(ctx, `DELETE FROM channel_playout WHERE channel_id=? AND start_ms > ?`, id, from)
	return err
}
