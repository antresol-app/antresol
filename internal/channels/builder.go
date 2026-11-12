package channels

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"sort"
	"time"
)

// Defaults of Builder.
const (
	DefaultHorizon = 48 * time.Hour
	DefaultKeep    = 7 * 24 * time.Hour
)

// Entry is a row of a channel's schedule.
type Entry struct {
	Seq     int64
	Start   time.Time
	End     time.Time
	ItemID  int64
	FileID  int64
	UnitID  int64
	Channel int64
	// ExtraID: a title outside the library (channel_extras); 0: a file.
	ExtraID int64
}

// Builder makes and extends channel schedules.
type Builder struct {
	Store   *Store
	Catalog Catalog
	// Horizon: how far ahead schedules are built (default 48 h); Keep: how
	// long a finished programme stays (default 7 days).
	Horizon, Keep time.Duration
	// Extras: titles outside the library for channels with Rules.Extras
	// (nil: none).
	Extras Extras
}

func (b *Builder) horizon() time.Duration {
	if b.Horizon > 0 {
		return b.Horizon
	}
	return DefaultHorizon
}

func (b *Builder) keep() time.Duration {
	if b.Keep > 0 {
		return b.Keep
	}
	return DefaultKeep
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMs(v int64) time.Time { return time.UnixMilli(v) }

// history is what the builder needs to know of a channel's past.
type history struct {
	lastSeq int64
	lastEnd int64 // 0: no rows
	recent  []int64
	cursor  map[int64]int64 // unit → last item (unit 0: last item of all)
}

func (b *Builder) history(ctx context.Context, tx *sql.Tx, id int64) (history, error) {
	h := history{cursor: map[int64]int64{}}
	rows, err := tx.QueryContext(ctx, `SELECT unit_id, item_id FROM channel_cursor WHERE channel_id=?`, id)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var u, it int64
		if err := rows.Scan(&u, &it); err != nil {
			rows.Close()
			return h, err
		}
		h.cursor[u] = it
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT seq, end_ms, unit_id, item_id FROM channel_playout WHERE channel_id=? ORDER BY seq`, id)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	var lastItem int64 = -1
	for rows.Next() {
		var seq, end, unit, item int64
		if err := rows.Scan(&seq, &end, &unit, &item); err != nil {
			return h, err
		}
		h.lastSeq, h.lastEnd = seq, end
		h.cursor[unit], h.cursor[0] = item, item
		if item != lastItem { // a programme's parts are one pick
			h.recent = append(h.recent, unit)
		}
		lastItem = item
	}
	return h, rows.Err()
}

// Build extends channel c's schedule to now + Horizon and forgets what
// ended more than Keep ago. A channel with nothing to play keeps what it
// has.
func (b *Builder) Build(ctx context.Context, c Channel) error {
	now := b.Store.now()
	pool, err := b.Catalog.Pool(ctx, c.Rules)
	if err != nil {
		return err
	}
	if pool, err = b.withExtras(ctx, c, pool); err != nil {
		return err
	}
	return b.build(ctx, c, pool, now)
}

func (b *Builder) build(ctx context.Context, c Channel, pool Pool, now time.Time) error {
	tx, err := b.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := b.trim(ctx, tx, c.ID, now); err != nil {
		return err
	}
	h, err := b.history(ctx, tx, c.ID)
	if err != nil {
		return err
	}
	start := h.lastEnd
	if start < ms(now) {
		start = ms(now) // a new channel, or the server was off: from now on
	}
	until := ms(now.Add(b.horizon()))
	if start >= until || pool.Empty() {
		return tx.Commit()
	}
	ins, err := tx.PrepareContext(ctx, `INSERT INTO channel_playout (channel_id, seq, start_ms, end_ms, item_id, file_id, unit_id, extra_id) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	p := newPicker(c, pool, h)
	if p.busy, err = b.elsewhere(ctx, tx, c.ID, start, until); err != nil {
		return err
	}
	seq := h.lastSeq
	for start < until {
		prog := p.next(seq+1, start)
		for _, part := range prog.Parts {
			seq++
			end := start + part.DurationMs
			var extra any
			if prog.ExtraID != 0 {
				extra = prog.ExtraID
			}
			if _, err := ins.ExecContext(ctx, c.ID, seq, start, end, prog.ItemID, part.FileID, prog.UnitID, extra); err != nil {
				return err
			}
			start = end
		}
	}
	return tx.Commit()
}

// trim forgets programmes that ended more than Keep ago, keeping each
// unit's last item in channel_cursor.
func (b *Builder) trim(ctx context.Context, tx *sql.Tx, id int64, now time.Time) error {
	cut := ms(now.Add(-b.keep()))
	if _, err := tx.ExecContext(ctx, `INSERT INTO channel_cursor (channel_id, unit_id, item_id)
	  SELECT channel_id, unit_id, item_id FROM channel_playout p WHERE channel_id=? AND end_ms < ?
	    AND seq = (SELECT max(seq) FROM channel_playout q WHERE q.channel_id=p.channel_id AND q.unit_id=p.unit_id AND q.end_ms < ?)
	  ON CONFLICT (channel_id, unit_id) DO UPDATE SET item_id=excluded.item_id`, id, cut, cut); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO channel_cursor (channel_id, unit_id, item_id)
	  SELECT channel_id, 0, item_id FROM channel_playout WHERE channel_id=? AND end_ms < ? ORDER BY seq DESC LIMIT 1
	  ON CONFLICT (channel_id, unit_id) DO UPDATE SET item_id=excluded.item_id`, id, cut); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM channel_playout WHERE channel_id=? AND end_ms < ?`, id, cut)
	return err
}

// airing is a stretch of a title on another channel's schedule.
type airing struct{ start, end int64 }

// elsewhere is what the other enabled channels air in [from, to), by title:
// a title with several genres must not be on two channels at once.
func (b *Builder) elsewhere(ctx context.Context, tx *sql.Tx, id, from, to int64) (map[int64][]airing, error) {
	// by title (a movie, an episode's series; a title outside the library:
	// its unit), so a channel of picks and a genre channel see each other
	rows, err := tx.QueryContext(ctx, `SELECT CASE WHEN p.extra_id IS NOT NULL OR i.id IS NULL THEN p.unit_id ELSE `+titleOf+` END, p.start_ms, p.end_ms
	  FROM channel_playout p JOIN channels c ON c.id=p.channel_id
	  LEFT JOIN items i ON i.id=p.item_id
	  LEFT JOIN items se ON se.id=i.parent_id AND i.kind='episode'
	  LEFT JOIN items sr ON sr.id=se.parent_id AND se.kind='season'
	  WHERE p.channel_id<>? AND c.enabled=1 AND p.end_ms > ? AND p.start_ms < ?`, id, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]airing{}
	for rows.Next() {
		var u int64
		var a airing
		if err := rows.Scan(&u, &a.start, &a.end); err != nil {
			return nil, err
		}
		out[u] = append(out[u], a)
	}
	return out, rows.Err()
}

// picker chooses programmes for one build.
type picker struct {
	c      Channel
	pool   Pool
	h      history
	flat   []Prog // ordered: every programme in order
	window int
	busy   map[int64][]airing // titles on air elsewhere
}

// clashes: unit u's programme pr starting at start would overlap its
// title on another channel.
func (p *picker) clashes(u int64, pr Prog, start int64) bool {
	if pr.TitleID != 0 {
		u = pr.TitleID
	}
	var dur int64
	for _, part := range pr.Parts {
		dur += part.DurationMs
	}
	for _, a := range p.busy[u] {
		if a.start < start+dur && start < a.end {
			return true
		}
	}
	return false
}

// nextOf is the programme unit u plays next.
func (p *picker) nextOf(u int64) Prog {
	progs := p.pool.Progs[u]
	return progs[(indexOf(progs, p.h.cursor[u])+1)%len(progs)]
}

func newPicker(c Channel, pool Pool, h history) *picker {
	p := &picker{c: c, pool: pool, h: h}
	if c.Rules.Order == OrderOrdered {
		us := append([]Unit(nil), pool.Units...)
		sort.SliceStable(us, func(i, j int) bool {
			if len(c.Rules.Picks) > 0 { // picks play in the order they were picked (the pool's)
				return false
			}
			if us[i].Year != us[j].Year {
				return us[i].Year < us[j].Year
			}
			if us[i].Sort != us[j].Sort {
				return us[i].Sort < us[j].Sort
			}
			return us[i].ID < us[j].ID
		})
		for _, u := range us {
			p.flat = append(p.flat, pool.Progs[u.ID]...)
		}
	}
	// no unit comes back before two thirds of the others have played
	p.window = min(len(pool.Units)*2/3, 500)
	return p
}

func (p *picker) next(seq, start int64) Prog {
	if p.c.Rules.Order == OrderOrdered { // a marathon keeps its order
		var unit int64
		if n := len(p.h.recent); n > 0 {
			unit = p.h.recent[n-1]
		}
		i := positionOf(p.flat, unit, p.h.cursor[0]) + 1
		pr := p.flat[i%len(p.flat)]
		p.played(pr)
		return pr
	}
	recent := map[int64]bool{}
	for i := len(p.h.recent) - 1; i >= 0 && len(recent) < p.window; i-- {
		recent[p.h.recent[i]] = true
	}
	// a title outside the library only after one of the library's: never
	// two in a row, so at most half of the programmes
	units := p.pool.Units
	if n := len(p.h.recent); n == 0 || p.h.recent[n-1] < 0 {
		units = libraryUnits(units)
	}
	// best: not played lately and not on another channel then; else not
	// on another channel; else not played lately; else anything
	var fresh, free, idle []Unit
	for _, u := range units {
		clash := p.clashes(u.ID, p.nextOf(u.ID), start)
		if !clash {
			free = append(free, u)
		}
		if !recent[u.ID] {
			idle = append(idle, u)
			if !clash {
				fresh = append(fresh, u)
			}
		}
	}
	cand := fresh
	for _, c := range [][]Unit{free, idle, units} {
		if len(cand) > 0 {
			break
		}
		cand = c
	}
	rng := rand.New(rand.NewPCG(uint64(p.c.Seed), uint64(seq)))
	u := cand[rng.IntN(len(cand))]
	pr := p.nextOf(u.ID)
	p.played(pr)
	return pr
}

// libraryUnits are the library's units of us (not the extras).
func libraryUnits(us []Unit) []Unit {
	out := us[:0:0]
	for _, u := range us {
		if u.ID > 0 {
			out = append(out, u)
		}
	}
	return out
}

func (p *picker) played(pr Prog) {
	p.h.cursor[pr.UnitID], p.h.cursor[0] = pr.ItemID, pr.ItemID
	p.h.recent = append(p.h.recent, pr.UnitID)
}

// positionOf is where the last programme (item of unit) is in an ordered
// channel's programmes: the same item may be in two picks (a folder and
// an episode of it), the unit tells which; -1: not there.
func positionOf(flat []Prog, unit, item int64) int {
	for i, pr := range flat {
		if pr.ItemID == item && pr.UnitID == unit {
			return i
		}
	}
	return indexOf(flat, item)
}

func indexOf(progs []Prog, item int64) int {
	for i, pr := range progs {
		if pr.ItemID == item {
			return i
		}
	}
	return -1
}

// CutMissing drops the schedule from the first programme still to finish
// whose file is gone from the library (missing or deleted); the next Build
// fills it again. Titles outside the library have no file: they are not
// checked here (a failed one is cut when it does not play: ExtraFailed).
func (s *Store) CutMissing(ctx context.Context, id int64) (bool, error) {
	var seq sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT min(p.seq) FROM channel_playout p LEFT JOIN files f ON f.id=p.file_id AND f.missing_since IS NULL
	  WHERE p.channel_id=? AND p.end_ms > ? AND f.id IS NULL AND p.extra_id IS NULL`, id, ms(s.now())).Scan(&seq)
	if err != nil || !seq.Valid {
		return false, err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM channel_playout WHERE channel_id=? AND seq >= ?`, id, seq.Int64)
	return err == nil, err
}

// ErrNothing: the channel has nothing scheduled at that time.
var ErrNothing = errors.New("channels: nothing scheduled")

const entryCols = `seq, start_ms, end_ms, item_id, file_id, unit_id, channel_id, COALESCE(extra_id, 0)`

func scanEntry(r rowScanner) (Entry, error) {
	var e Entry
	var st, en int64
	err := r.Scan(&e.Seq, &st, &en, &e.ItemID, &e.FileID, &e.UnitID, &e.Channel, &e.ExtraID)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNothing
	}
	e.Start, e.End = fromMs(st), fromMs(en)
	return e, err
}

// At returns the entry on air at t, else the first one after t.
func (s *Store) At(ctx context.Context, id int64, t time.Time) (Entry, error) {
	return scanEntry(s.DB.QueryRowContext(ctx, `SELECT `+entryCols+` FROM channel_playout WHERE channel_id=? AND end_ms > ? ORDER BY seq LIMIT 1`, id, ms(t)))
}

// Range returns the entries that overlap [from, to), in order.
func (s *Store) Range(ctx context.Context, id int64, from, to time.Time) ([]Entry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+entryCols+` FROM channel_playout WHERE channel_id=? AND end_ms > ? AND start_ms < ? ORDER BY seq`, id, ms(from), ms(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
