package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
)

// Unit is what a channel picks: a movie or a series.
type Unit struct {
	ID     int64
	Kind   string
	Title  string
	Sort   string
	Year   int
	Rating string
	Genres []string // GenreKey of each
}

// Part is one file of a programme.
type Part struct {
	FileID     int64
	DurationMs int64
}

// Prog is one programme: a movie or an episode, its files in playback
// order.
type Prog struct {
	ItemID int64
	UnitID int64
	Parts  []Part
	// ExtraID: a title outside the library (channel_extras; ItemID is
	// then -ExtraID); 0 for the library's.
	ExtraID int64
}

// Pool is what a channel's rules take: the units (by id) and each unit's
// programmes in order (a movie: one; a series: its episodes, season 0
// left out). Units without a playable programme are not in it.
type Pool struct {
	Units []Unit
	Progs map[int64][]Prog
}

// Empty: nothing to play.
func (p Pool) Empty() bool { return len(p.Units) == 0 }

// Catalog reads the library's movies and series for channels.
type Catalog struct{ DB *sql.DB }

// Units returns every movie and series in the library (not missing), by id.
func (c Catalog) Units(ctx context.Context) ([]Unit, error) {
	rows, err := c.DB.QueryContext(ctx, `SELECT id, kind, title, sort_title, year, rating, meta_json FROM items
	  WHERE kind IN ('movie','series') AND missing_since IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unit
	for rows.Next() {
		var u Unit
		var meta string
		if err := rows.Scan(&u.ID, &u.Kind, &u.Title, &u.Sort, &u.Year, &u.Rating, &meta); err != nil {
			return nil, err
		}
		var m struct {
			Genres []string `json:"genres"`
		}
		_ = json.Unmarshal([]byte(meta), &m)
		for _, g := range m.Genres {
			u.Genres = append(u.Genres, i18n.GenreKey(g))
		}
		if u.Sort == "" {
			u.Sort = strings.ToLower(u.Title)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Owned returns the units profile uid brought (item_owners).
func (c Catalog) Owned(ctx context.Context, uid int64) (map[int64]bool, error) {
	rows, err := c.DB.QueryContext(ctx, `SELECT item_id FROM item_owners WHERE user_id=?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Progs returns the programmes of the units, in order: a movie's files; a
// series' episodes by season and episode (season 0 out), each with its
// files. Files without a known duration or missing are left out.
func (c Catalog) Progs(ctx context.Context, units []Unit) (map[int64][]Prog, error) {
	out := map[int64][]Prog{}
	var movies, series []int64
	for _, u := range units {
		if u.Kind == library.KindSeries {
			series = append(series, u.ID)
		} else {
			movies = append(movies, u.ID)
		}
	}
	add := func(rows *sql.Rows) error {
		defer rows.Close()
		var last *Prog
		for rows.Next() {
			var unit, item, file, dur int64
			if err := rows.Scan(&unit, &item, &file, &dur); err != nil {
				return err
			}
			if last == nil || last.ItemID != item || last.UnitID != unit {
				out[unit] = append(out[unit], Prog{ItemID: item, UnitID: unit})
				ps := out[unit]
				last = &ps[len(ps)-1]
			}
			last.Parts = append(last.Parts, Part{FileID: file, DurationMs: dur})
		}
		return rows.Err()
	}
	for part := range chunks(movies, 500) {
		in, args := inArgs(part)
		rows, err := c.DB.QueryContext(ctx, `SELECT f.item_id, f.item_id, f.id, f.duration_ms FROM files f
		  WHERE f.item_id IN `+in+` AND f.missing_since IS NULL AND f.duration_ms > 0 ORDER BY f.item_id, f.path`, args...)
		if err != nil {
			return nil, err
		}
		if err := add(rows); err != nil {
			return nil, err
		}
	}
	for part := range chunks(series, 200) {
		in, args := inArgs(part)
		rows, err := c.DB.QueryContext(ctx, `SELECT s.id, e.id, f.id, f.duration_ms FROM items s
		  JOIN items se ON se.parent_id=s.id AND se.kind='season'
		  JOIN items e ON e.parent_id=se.id AND e.kind='episode' AND e.missing_since IS NULL
		  JOIN files f ON f.item_id=e.id AND f.missing_since IS NULL AND f.duration_ms > 0
		  WHERE s.id IN `+in+` AND COALESCE(e.season_no, se.season_no, 0) > 0
		  ORDER BY s.id, COALESCE(e.season_no, se.season_no), COALESCE(e.episode_no, 0), e.id, f.path`, args...)
		if err != nil {
			return nil, err
		}
		if err := add(rows); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func chunks(ids []int64, n int) func(func([]int64) bool) {
	return func(yield func([]int64) bool) {
		for len(ids) > 0 {
			k := min(n, len(ids))
			if !yield(ids[:k]) {
				return
			}
			ids = ids[k:]
		}
	}
}

func inArgs(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(?" + strings.Repeat(",?", len(ids)-1) + ")", args
}

// Match filters units by rules (everything but the library owner, which
// Pool applies).
func Match(units []Unit, r Rules) []Unit {
	var out []Unit
	if len(r.Titles) > 0 {
		want := map[int64]bool{}
		for _, id := range r.Titles {
			want[id] = true
		}
		for _, u := range units {
			if want[u.ID] && library.RatingAllowed(r.MaxRating, library.Item{Kind: u.Kind, Rating: u.Rating}) {
				out = append(out, u)
			}
		}
		return out
	}
	var genres map[string]bool
	if len(r.Genres) > 0 {
		genres = map[string]bool{}
		for _, g := range r.Genres {
			genres[i18n.GenreKey(g)] = true
		}
	}
	for _, u := range units {
		if len(r.Kinds) > 0 && !contains(r.Kinds, u.Kind) {
			continue
		}
		if r.YearFrom > 0 && u.Year < r.YearFrom || r.YearTo > 0 && (u.Year == 0 || u.Year > r.YearTo) {
			continue
		}
		if genres != nil && !anyIn(u.Genres, genres) {
			continue
		}
		if !library.RatingAllowed(r.MaxRating, library.Item{Kind: u.Kind, Rating: u.Rating}) {
			continue
		}
		out = append(out, u)
	}
	return out
}

func anyIn(list []string, set map[string]bool) bool {
	for _, x := range list {
		if set[x] {
			return true
		}
	}
	return false
}

// Pool is the channel's pool now.
func (c Catalog) Pool(ctx context.Context, r Rules) (Pool, error) {
	units, err := c.Units(ctx)
	if err != nil {
		return Pool{}, err
	}
	return c.PoolOf(ctx, units, r)
}

// PoolOf is Pool over units already read (the generator reads them once).
func (c Catalog) PoolOf(ctx context.Context, units []Unit, r Rules) (Pool, error) {
	m := Match(units, r)
	if r.LibraryUser != 0 {
		owned, err := c.Owned(ctx, r.LibraryUser)
		if err != nil {
			return Pool{}, err
		}
		k := m[:0:0]
		for _, u := range m {
			if owned[u.ID] {
				k = append(k, u)
			}
		}
		m = k
	}
	progs, err := c.Progs(ctx, m)
	if err != nil {
		return Pool{}, err
	}
	p := Pool{Progs: progs}
	for _, u := range m {
		if len(progs[u.ID]) > 0 {
			p.Units = append(p.Units, u)
		}
	}
	sort.Slice(p.Units, func(i, j int) bool { return p.Units[i].ID < p.Units[j].ID })
	return p, nil
}
