package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
)

// Channels with fetching («С докачкой»): a channel whose Rules.Fetch is
// set plays its titles (movies, series or one season of a series) in
// order, a marathon or a curated list, including the programmes not on
// disk. Those are fetched by an addon (Service.Fetcher, the channels.fetch
// hook) in time before they air: the lead time comes from the download's
// size and the speed seen so far. A programme that would not be on disk in
// time is not scheduled: the slot takes the next available programme in
// the channel's order, and a fetch that turns late cuts the schedule there
// and builds it again (the programme on air stays). After airing, plus the
// channel's grace period, what was fetched for the channel goes again
// (fetch_pass.go) unless someone in the household has progress on it, has
// it on a watchlist or a mark, the addon keeps it, or another schedule
// still has it. What the library had before is never removed.
//
// This file: the rules, the parts the pass needs and the channel's pool.

// FetchRules are a channel's fetching rules (Rules.Fetch).
type FetchRules struct {
	// Titles: what the channel plays, in this order.
	Titles []FetchTitle `json:"titles"`
	// BudgetGB bounds the disk what is fetched for the channel takes
	// (0: DefaultFetchChannelGB).
	BudgetGB float64 `json:"budgetGB,omitempty"`
	// GraceHours: how long after airing a fetched programme stays (0:
	// DefaultFetchGraceHours).
	GraceHours int `json:"graceHours,omitempty"`
}

// FetchTitle is a title of a channel with fetching: a TMDB movie, or a
// series (every season but the specials, or Season alone).
type FetchTitle struct {
	Kind   string `json:"kind"` // library.KindMovie | library.KindSeries
	TMDBID int    `json:"tmdbId"`
	Season int    `json:"season,omitempty"`
	// Title and Year: what the editor showed (the guide takes TMDB's).
	Title string `json:"title,omitempty"`
	Year  int    `json:"year,omitempty"`
}

// Defaults and bounds of fetching (owner's to tune; see the report).
const (
	DefaultFetchChannelGB  = 50.0
	DefaultFetchGlobalGB   = 200.0
	DefaultFetchGraceHours = 24
	maxFetchTitles         = 200
	maxFetchGB             = 100000.0
	maxFetchGraceHours     = 24 * 30
)

// FetchOn: r is a channel with fetching.
func FetchOn(r Rules) bool { return r.Fetch != nil && len(r.Fetch.Titles) > 0 }

// normalizeFetch checks and cleans r.Fetch: titles once each, bounds kept;
// a channel with fetching plays in order and takes nothing else.
func normalizeFetch(r *Rules) error {
	f := r.Fetch
	if f == nil {
		return nil
	}
	var out []FetchTitle
	for _, t := range f.Titles {
		if (t.Kind != library.KindMovie && t.Kind != library.KindSeries) || t.TMDBID <= 0 || t.Season < 0 {
			return &Invalid{"Неизвестный фильм или сериал в составе канала"}
		}
		if t.Kind == library.KindMovie {
			t.Season = 0
		}
		t.Title = clipRunes(t.Title, 200, "")
		dup := false
		for _, o := range out {
			if o.Kind == t.Kind && o.TMDBID == t.TMDBID && o.Season == t.Season {
				dup = true
			}
		}
		if !dup {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		r.Fetch = nil
		return nil
	}
	if len(out) > maxFetchTitles {
		return &Invalid{"Слишком много элементов в составе канала"}
	}
	if f.BudgetGB < 0 || f.BudgetGB > maxFetchGB {
		return &Invalid{"Недопустимый объём диска для канала"}
	}
	if f.GraceHours < 0 || f.GraceHours > maxFetchGraceHours {
		return &Invalid{"Недопустимый срок хранения после эфира"}
	}
	f.Titles = out
	r.Order = OrderOrdered
	r.Picks, r.Titles, r.Extras = nil, nil, false
	return nil
}

func (f *FetchRules) budget() int64 {
	gb := DefaultFetchChannelGB
	if f != nil && f.BudgetGB > 0 {
		gb = f.BudgetGB
	}
	return int64(gb * (1 << 30))
}

func (f *FetchRules) grace() time.Duration {
	h := DefaultFetchGraceHours
	if f != nil && f.GraceHours > 0 {
		h = f.GraceHours
	}
	return time.Duration(h) * time.Hour
}

// FetchProgramme is one programme of a FetchTitle (FetchCatalog).
type FetchProgramme struct {
	Kind            string // library.KindMovie | library.KindEpisode
	Season, Episode int
	Title           string // the movie's or the series' title
	EpisodeTitle    string
	Year            int
	Overview        string
	Rating          string
	Poster          string // a TMDB image path
	RuntimeMs       int64
}

// FetchFound is a title a search of FetchCatalog found.
type FetchFound struct {
	Kind   string `json:"kind"` // movie | series
	TMDBID int    `json:"tmdbId"`
	Title  string `json:"title"`
	Year   int    `json:"year,omitempty"`
	Poster string `json:"poster,omitempty"` // a full URL
}

// FetchCatalog names the programmes of a channel's titles (TMDB).
type FetchCatalog interface {
	Programmes(ctx context.Context, t FetchTitle) ([]FetchProgramme, error)
	Search(ctx context.Context, q string) ([]FetchFound, error)
	// Seasons are a series' seasons (specials left out).
	Seasons(ctx context.Context, tmdbID int) ([]int, error)
}

// FetchWant is a programme the core wants on disk.
type FetchWant struct {
	Key     string
	Kind    string // movie | episode
	TMDBID  int    // the movie's or the series'
	Season  int
	Episode int
	Title   string
	Year    int
}

// States of a fetch the addon reports (FetchState.State).
const (
	FetchSearching   = "searching"
	FetchDownloading = "downloading"
	FetchDone        = "done"
	FetchFailed      = "failed"
)

// FetchState is the addon's state of a wanted programme.
type FetchState struct {
	Key   string
	State string
	// Size and Done in bytes (0: not known yet); Rate: the download's
	// speed now, bytes a second.
	Size, Done, Rate int64
	// Existing: another download of it ran already (not the channel's).
	Existing bool
	// Keep: the addon keeps the title (followed, pinned): never removed.
	Keep  bool
	Error string
}

// Fetcher gets programmes on disk for channels: an addon over the
// channels.fetch hook (internal/addons), never called with the build lock
// held for long.
type Fetcher interface {
	Ready(ctx context.Context) bool
	// Fetch makes sure each of want is being fetched and says how each
	// is; drop are keys no channel needs any more (an unfinished
	// download goes, a finished one is forgotten).
	Fetch(ctx context.Context, want []FetchWant, drop []string) ([]FetchState, error)
}

// Row states of channel_fetch.
const (
	fetchWanted    = "wanted"
	fetchRequested = "requested"
	fetchReady     = "ready"
	fetchLibrary   = "library"
	fetchKept      = "kept"
	fetchRemoved   = "removed"
	fetchFailed    = "failed"
)

// fetchItemBase: a schedule row of a programme not on disk has item_id
// fetchItem(its channel_fetch id), below every title outside the library
// (-channel_extras id).
const fetchItemBase = int64(1) << 50

func fetchItem(id int64) int64 { return -fetchItemBase - id }

// isFetchItem: an item id of fetchItem.
func isFetchItem(id int64) bool { return id < -fetchItemBase }

type fetchRow struct {
	ID, Channel     int64
	Pos             int
	Kind            string
	TMDBID          int
	Season, Episode int
	Title, EpTitle  string
	Year            int
	Overview        string
	Rating          string
	Poster          string
	RuntimeMs       int64
	State           string
	Existing        bool
	ItemID          int64
	Files           []int64
	Size, Done      int64
	RequestedMs     int64
	ReadyMs         int64
	AiredMs         int64
	FailedUntilMs   int64
	Error           string
}

// fetchKey names a programme for the addon (the same on every channel).
func fetchKey(kind string, tmdb, season, episode int) string {
	return kind + ":" + strconv.Itoa(tmdb) + ":" + strconv.Itoa(season) + ":" + strconv.Itoa(episode)
}

func (r fetchRow) key() string { return fetchKey(r.Kind, r.TMDBID, r.Season, r.Episode) }

// bytes is the row's size: the download's, else estimated from its runtime.
func (r fetchRow) bytes() int64 {
	if r.Size > 0 {
		return r.Size
	}
	return estimateBytes(r.RuntimeMs)
}

const fetchCols = `id, channel_id, pos, kind, tmdb_id, season, episode, title, episode_title, year, overview, rating, poster, runtime_ms,
  state, existing, item_id, files_json, size_bytes, done_bytes, requested_ms, ready_ms, aired_ms, failed_until_ms, error`

func scanFetch(r rowScanner) (fetchRow, error) {
	var x fetchRow
	var files string
	err := r.Scan(&x.ID, &x.Channel, &x.Pos, &x.Kind, &x.TMDBID, &x.Season, &x.Episode, &x.Title, &x.EpTitle, &x.Year, &x.Overview, &x.Rating,
		&x.Poster, &x.RuntimeMs, &x.State, &x.Existing, &x.ItemID, &files, &x.Size, &x.Done, &x.RequestedMs, &x.ReadyMs, &x.AiredMs,
		&x.FailedUntilMs, &x.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNothing
	}
	_ = json.Unmarshal([]byte(files), &x.Files)
	return x, err
}

// unqueueFetch takes all of channel ch's fetch rows out of its order
// (pos -1: not a title of the channel now).
func unqueueFetch(ctx context.Context, q dbtx, ch int64) error {
	_, err := q.ExecContext(ctx, `UPDATE channel_fetch SET pos=-1 WHERE channel_id=?`, ch)
	return err
}

func fetchRows(ctx context.Context, q dbtx, where string, args ...any) ([]fetchRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+fetchCols+` FROM channel_fetch WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fetchRow
	for rows.Next() {
		x, err := scanFetch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// libProg is a programme of a channel with fetching that is on disk.
type libProg struct {
	ItemID  int64
	TitleID int64
	Rating  string
	Parts   []Part
	Files   []int64 // every file of the item, playable or not yet probed
}

// onDisk finds rows' programmes in the library: by key, the item whose
// files are probed (a file still being probed is not on disk yet).
func onDisk(ctx context.Context, conn *sql.DB, rows []fetchRow) (map[string]libProg, error) {
	out := map[string]libProg{}
	var movies, series []int64
	seen := map[string]bool{}
	for _, r := range rows {
		k := r.Kind + strconv.Itoa(r.TMDBID)
		if seen[k] {
			continue
		}
		seen[k] = true
		if r.Kind == library.KindMovie {
			movies = append(movies, int64(r.TMDBID))
		} else {
			series = append(series, int64(r.TMDBID))
		}
	}
	scan := func(q string, ids []int64, key func(tmdb int64, s, e int) string) error {
		for part := range slices.Chunk(ids, 400) {
			in, args := db.InList(part)
			rs, err := conn.QueryContext(ctx, fmt.Sprintf(q, in), args...)
			if err != nil {
				return err
			}
			for rs.Next() {
				var tmdb, item, title, file, dur int64
				var s, e int
				var rating string
				if err := rs.Scan(&tmdb, &s, &e, &item, &title, &rating, &file, &dur); err != nil {
					rs.Close()
					return err
				}
				k := key(tmdb, s, e)
				p, has := out[k]
				if has && p.ItemID != item {
					continue // two copies: the first one's
				}
				p.ItemID, p.TitleID, p.Rating = item, title, rating
				p.Files = append(p.Files, file)
				if dur > 0 {
					p.Parts = append(p.Parts, Part{FileID: file, DurationMs: dur})
				}
				out[k] = p
			}
			err = rs.Err()
			rs.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	if err := scan(`SELECT i.tmdb_id, 0, 0, i.id, i.id, `+library.RatingOf("i")+`, f.id, COALESCE(f.duration_ms, 0) FROM items i
	  JOIN files f ON f.item_id=i.id AND f.missing_since IS NULL
	  WHERE i.kind='movie' AND i.parent_id IS NULL AND i.missing_since IS NULL AND i.tmdb_id IN %s ORDER BY i.id, f.path`, movies,
		func(t int64, _, _ int) string { return fetchKey(library.KindMovie, int(t), 0, 0) }); err != nil {
		return nil, err
	}
	if err := scan(`SELECT s.tmdb_id, COALESCE(e.season_no, se.season_no), e.episode_no, e.id, s.id, `+library.RatingOf("s")+`, f.id, COALESCE(f.duration_ms, 0) FROM items s
	  JOIN items se ON se.parent_id=s.id AND se.kind='season' AND se.missing_since IS NULL
	  JOIN items e ON e.parent_id=se.id AND e.kind='episode' AND e.missing_since IS NULL AND e.episode_no IS NOT NULL
	  JOIN files f ON f.item_id=e.id AND f.missing_since IS NULL
	  WHERE s.kind='series' AND s.parent_id IS NULL AND s.missing_since IS NULL AND s.tmdb_id IN %s ORDER BY s.id, e.id, f.path`, series,
		func(t int64, s, e int) string { return fetchKey(library.KindEpisode, int(t), s, e) }); err != nil {
		return nil, err
	}
	// an item whose files are not all probed yet is not on disk: its
	// duration, and so its slot, is not known
	for k, p := range out {
		if len(p.Parts) != len(p.Files) {
			p.Parts = nil
			out[k] = p
		}
	}
	return out, nil
}

// fetchUnit is the unit of a channel's title (one per movie or series).
func fetchUnit(kind string, tmdb int) int64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "fetch:%s:%d", kind, tmdb)
	return -int64(h.Sum64()&(1<<48-1)) - 1
}

// fetchPool is a channel with fetching's pool: every programme of its
// rows in order, on disk or not, and Fits, which lets a programme not on
// disk into a slot only when it will be there in time and the disk
// budgets have room (fetchPlan).
func (b *Builder) fetchPool(ctx context.Context, c Channel) (Pool, error) {
	rows, err := fetchRows(ctx, b.Store.DB, `channel_id=? AND pos >= 0 ORDER BY pos`, c.ID)
	if err != nil {
		return Pool{}, err
	}
	disk, err := onDisk(ctx, b.Store.DB, rows)
	if err != nil {
		return Pool{}, err
	}
	plan, err := newFetchPlan(ctx, b.Store, c)
	if err != nil {
		return Pool{}, err
	}
	pool := Pool{Progs: map[int64][]Prog{}}
	byFetch := map[int64]fetchRow{}
	for _, r := range rows {
		unit := fetchUnit(r.Kind, r.TMDBID)
		if r.Kind == library.KindEpisode {
			unit = fetchUnit(library.KindSeries, r.TMDBID)
		}
		var pr Prog
		if p, ok := disk[r.key()]; ok && len(p.Parts) > 0 {
			if !library.RatingAllowed(c.Rules.MaxRating, library.Item{Kind: library.KindMovie, Rating: p.Rating}) {
				continue
			}
			pr = Prog{ItemID: p.ItemID, UnitID: unit, TitleID: p.TitleID, Parts: p.Parts}
		} else {
			if !library.RatingAllowed(c.Rules.MaxRating, library.Item{Kind: library.KindMovie, Rating: r.Rating}) || r.RuntimeMs <= 0 {
				continue
			}
			pr = Prog{ItemID: fetchItem(r.ID), UnitID: unit, FetchID: r.ID, Parts: []Part{{DurationMs: r.RuntimeMs}}}
			byFetch[r.ID] = r
		}
		if pool.Progs[unit] == nil {
			pool.Units = append(pool.Units, Unit{ID: unit, Kind: r.Kind, Title: r.Title, Sort: r.Title, Year: r.Year})
		}
		pool.Progs[unit] = append(pool.Progs[unit], pr)
		pool.Flat = append(pool.Flat, pr)
	}
	pool.Fits = func(pr Prog, start int64) bool {
		if pr.FetchID == 0 {
			return true
		}
		return plan.fits(byFetch[pr.FetchID], start)
	}
	return pool, nil
}
