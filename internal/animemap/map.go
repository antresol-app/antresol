// Package animemap maps MyAnimeList ids (equal to Shikimori ids) to TMDB
// title/season/episode-offset, from a public mapping dataset fetched at
// runtime, with a TMDB search fallback for titles the dataset lacks.
package animemap

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/antresol-app/antresol/internal/db"
)

const (
	keyETag      = "anime_map.etag"
	keyModified  = "anime_map.last_modified"
	keyFetchedAt = "anime_map.fetched_at"
	maxAge       = 7 * 24 * time.Hour
	searchTTL    = 30 * 24 * time.Hour
)

// maxBody caps the dataset download (a variable so tests can shrink it).
var maxBody int64 = 64 << 20

var errTooLarge = errors.New("animemap: dataset exceeds size limit")

type cappedReader struct {
	r io.Reader
	n int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n -= int64(n)
	if c.n < 0 {
		return n, errTooLarge
	}
	return n, err
}

// Entry is one resolved mapping. Kind is "series" or "movie".
type Entry struct {
	MALID   int64
	Kind    string
	TMDBID  int64
	Season  int
	Offset  int
	AniDBID int64
}

// Title is what Resolve needs to know about an anime (decoupled from any
// particular account provider package).
type Title struct {
	MALID   int64
	Name    string
	Russian string
	Kind    string // tv, movie, ona, ova, special, ...
	AiredOn string // YYYY-MM-DD, possibly partial or empty
}

// Candidate is one TMDB search result.
type Candidate struct {
	TMDBID           int64
	Name             string
	OriginalName     string
	Year             int
	Animation        bool // has the Animation genre
	OriginalLanguage string
}

// Searcher lists TMDB candidates by title and (optional, 0 = any) year.
type Searcher interface {
	SearchTV(ctx context.Context, query string, year int) ([]Candidate, error)
	SearchMovie(ctx context.Context, query string, year int) ([]Candidate, error)
}

type Map struct {
	DB     *sql.DB
	URL    string
	HTTP   *http.Client
	Search Searcher
	Now    func() time.Time
	Log    *slog.Logger
}

func (m *Map) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Map) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

func (m *Map) getSetting(ctx context.Context, key string) string {
	v, _, _ := db.GetSetting(ctx, m.DB, key)
	return v
}

// Run refreshes at start when the data is older than a week, then weekly,
// until ctx ends. Failures are logged and retried at the next tick.
func (m *Map) Run(ctx context.Context) {
	if at, err := time.Parse(time.RFC3339, m.getSetting(ctx, keyFetchedAt)); err != nil || m.now().Sub(at) > maxAge {
		m.refreshLogged(ctx)
	}
	t := time.NewTicker(maxAge)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refreshLogged(ctx)
		}
	}
}

func (m *Map) refreshLogged(ctx context.Context) {
	if err := m.Refresh(ctx); err != nil && ctx.Err() == nil {
		m.log().Warn("anime map refresh failed", "err", err)
	}
}

type rawEntry struct {
	MAL     json.RawMessage `json:"mal_id"`
	AniDB   json.RawMessage `json:"anidb_id"`
	TMDB    json.RawMessage `json:"themoviedb_id"`
	Type    string          `json:"type"`
	Season  json.RawMessage `json:"season"`
	Episode json.RawMessage `json:"episode_offset"`
}

// lenientInt reads a number, a numeric string, or the first element of an
// array of those; anything else is 0.
func lenientInt(raw json.RawMessage) int64 {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0
	}
	switch raw[0] {
	case '[':
		var a []json.RawMessage
		if json.Unmarshal(raw, &a) != nil || len(a) == 0 {
			return 0
		}
		return lenientInt(a[0])
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return 0
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		return n
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return 0
	}
	return int64(f)
}

// lenientField reads o[key]; ok=false when the object or key is absent.
func lenientField(raw json.RawMessage, key string) (int64, bool) {
	var o map[string]json.RawMessage
	if json.Unmarshal(raw, &o) != nil {
		return 0, false
	}
	v, ok := o[key]
	if !ok || string(bytes.TrimSpace(v)) == "null" {
		return 0, false
	}
	return lenientInt(v), true
}

// tmdbID reads themoviedb_id: the published dataset keys it by TMDB kind
// ({"tv": n} or {"movie": [n, …]}, the first movie taken), which decides
// the kind; a bare id takes its kind from the entry's type.
func tmdbID(raw json.RawMessage, typ string) (int64, string) {
	if raw = bytes.TrimSpace(raw); len(raw) > 0 && raw[0] == '{' {
		if id, ok := lenientField(raw, "tv"); ok && id > 0 {
			return id, "series"
		}
		if id, ok := lenientField(raw, "movie"); ok && id > 0 {
			return id, "movie"
		}
		return 0, ""
	}
	return lenientInt(raw), kindOf(typ)
}

func kindOf(t string) string {
	if strings.EqualFold(t, "movie") {
		return "movie"
	}
	return "series"
}

// Refresh downloads the dataset (conditionally, body capped), parses it, then
// upserts it into anime_map in one transaction and deletes dataset rows absent
// from it. A dataset parsing to no entries, or to under half the dataset rows
// held now, is refused. On any error the table is untouched. Cached search rows survive,
// except where the dataset now has the same MAL id (the dataset row wins).
func (m *Map) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	if v := m.getSetting(ctx, keyETag); v != "" {
		req.Header.Set("If-None-Match", v)
	}
	if v := m.getSetting(ctx, keyModified); v != "" {
		req.Header.Set("If-Modified-Since", v)
	}
	hc := m.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	stamp := m.now().UTC().Format(time.RFC3339)
	if resp.StatusCode == http.StatusNotModified {
		return db.PutSetting(ctx, m.DB, keyFetchedAt, stamp)
	}
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("animemap: dataset status %d", resp.StatusCode)
	}

	// Download and parse first: the write lock is only held for the update.
	entries, err := parse(&cappedReader{r: resp.Body, n: maxBody})
	if err != nil {
		return err
	}
	cached := m.now().Unix()
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// a dataset that collapsed (format drift, truncated publish) would turn
	// the map into search guesses: keep the old rows and retry next run
	var held int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM anime_map WHERE source='dataset'`).Scan(&held); err != nil {
		return err
	}
	if len(entries) == 0 || 2*len(entries) < held {
		return fmt.Errorf("animemap: dataset refused: %d entries parsed, %d held", len(entries), held)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO anime_map(mal_id, kind, tmdb_id, tmdb_season, tmdb_offset, anidb_id, source, cached_at) VALUES(?,?,?,?,?,?,'dataset',?)
		ON CONFLICT(mal_id) DO UPDATE SET kind=excluded.kind, tmdb_id=excluded.tmdb_id, tmdb_season=excluded.tmdb_season, tmdb_offset=excluded.tmdb_offset, anidb_id=excluded.anidb_id, source='dataset', cached_at=excluded.cached_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	seen := make(map[int64]bool, len(entries))
	for _, e := range entries {
		seen[e.MALID] = true
		if _, err := stmt.ExecContext(ctx, e.MALID, e.Kind, e.TMDBID, e.Season, e.Offset, e.AniDBID, cached); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT mal_id FROM anime_map WHERE source='dataset'`)
	if err != nil {
		return err
	}
	var stale []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !seen[id] {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if _, err := tx.ExecContext(ctx, `DELETE FROM anime_map WHERE mal_id=? AND source='dataset'`, id); err != nil {
			return err
		}
	}
	if err := db.PutSetting(ctx, tx, keyETag, resp.Header.Get("ETag")); err != nil {
		return err
	}
	if err := db.PutSetting(ctx, tx, keyModified, resp.Header.Get("Last-Modified")); err != nil {
		return err
	}
	if err := db.PutSetting(ctx, tx, keyFetchedAt, stamp); err != nil {
		return err
	}
	return tx.Commit()
}

// parse streams the dataset array. Entries without a MAL or TMDB id are
// dropped; a missing season.tmdb means season 1.
func parse(r io.Reader) ([]Entry, error) {
	dec := json.NewDecoder(r)
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, errors.New("animemap: dataset is not a JSON array")
	}
	var out []Entry
	for dec.More() {
		var e rawEntry
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("animemap: parse: %w", err)
		}
		mal := lenientInt(e.MAL)
		tmdb, kind := tmdbID(e.TMDB, e.Type)
		if mal <= 0 || tmdb <= 0 {
			continue
		}
		season, ok := lenientField(e.Season, "tmdb")
		if !ok {
			season = 1
		}
		off, _ := lenientField(e.Episode, "tmdb")
		out = append(out, Entry{MALID: mal, Kind: kind, TMDBID: tmdb, Season: int(season), Offset: int(off), AniDBID: lenientInt(e.AniDB)})
	}
	if _, err := dec.Token(); err != nil { // closing ']' (a truncated body fails here)
		return nil, fmt.Errorf("animemap: parse: %w", err)
	}
	return out, nil
}

var sequelRe = regexp.MustCompile(`(?i)(\s([2-9]|1\d)\s*$|season\s*([2-9]|1\d)\s*$|\b\d+(st|nd|rd|th)\s+season\s*$|\s(ii|iii|iv|v|vi|vii|viii|ix|x)\s*$|\b\d+\s*сезон|(второй|третий|четв[её]ртый|пятый)\s+сезон)`)

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// pick returns the best acceptable candidate: animation only, and the title
// matches the query (normalised) ; originals in
// ja/zh/ko are preferred. No acceptable candidate means no match.
func pick(cs []Candidate, query string, year int) (int64, bool) {
	nq := normalize(query)
	var fallback int64
	for _, c := range cs {
		if !c.Animation || c.TMDBID <= 0 {
			continue
		}
		if !(normalize(c.Name) == nq || normalize(c.OriginalName) == nq || (year != 0 && c.Year == year)) {
			continue
		}
		switch c.OriginalLanguage {
		case "ja", "zh", "ko":
			return c.TMDBID, true
		}
		if fallback == 0 {
			fallback = c.TMDBID
		}
	}
	return fallback, fallback != 0
}

// Resolve finds the mapping for a title: the dataset (by MAL id), else a TMDB
// search by Name then Russian with the aired year. Search candidates must be
// animation and match the query by name or the year; a hit is cached for 30
// days as season 1, so titles that look like sequels (" 2", "Season 2", "II",
// "2 сезон", ...) are never searched. ok=false means no mapping (callers
// count these); a miss is never a guess.
func (m *Map) Resolve(ctx context.Context, a Title) (Entry, bool, error) {
	var e Entry
	var source string
	var cachedAt int64
	err := m.DB.QueryRowContext(ctx, `SELECT mal_id, kind, tmdb_id, tmdb_season, tmdb_offset, anidb_id, source, cached_at FROM anime_map WHERE mal_id=?`, a.MALID).
		Scan(&e.MALID, &e.Kind, &e.TMDBID, &e.Season, &e.Offset, &e.AniDBID, &source, &cachedAt)
	if err == nil && (source != "search" || m.now().Sub(time.Unix(cachedAt, 0)) < searchTTL) {
		return e, true, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, err
	}
	if m.Search == nil {
		return Entry{}, false, nil
	}
	// A search cannot tell which season a title is: only a first-season
	// (non-sequel) title is accepted as season 1; sequels are a miss.
	if sequelRe.MatchString(a.Name) || sequelRe.MatchString(a.Russian) {
		return Entry{}, false, nil
	}
	kind := kindOf(a.Kind)
	year := 0
	if len(a.AiredOn) >= 4 {
		year, _ = strconv.Atoi(a.AiredOn[:4])
	}
	var lastErr error
	for _, q := range []string{a.Name, a.Russian} {
		if strings.TrimSpace(q) == "" {
			continue
		}
		var cs []Candidate
		var serr error
		if kind == "movie" {
			cs, serr = m.Search.SearchMovie(ctx, q, year)
		} else {
			cs, serr = m.Search.SearchTV(ctx, q, year)
		}
		if serr != nil {
			lastErr = serr
			continue
		}
		id, ok := pick(cs, q, year)
		if !ok {
			continue
		}
		e = Entry{MALID: a.MALID, Kind: kind, TMDBID: id, Season: 1}
		if _, err := m.DB.ExecContext(ctx, `INSERT INTO anime_map(mal_id, kind, tmdb_id, tmdb_season, tmdb_offset, anidb_id, source, cached_at) VALUES(?,?,?,1,0,0,'search',?)
			ON CONFLICT(mal_id) DO UPDATE SET kind=excluded.kind, tmdb_id=excluded.tmdb_id, tmdb_season=1, tmdb_offset=0, anidb_id=0, source='search', cached_at=excluded.cached_at
			WHERE anime_map.source='search'`, e.MALID, e.Kind, e.TMDBID, m.now().Unix()); err != nil {
			return Entry{}, false, err
		}
		return e, true, nil
	}
	return Entry{}, false, lastErr
}

// HasTMDB reports whether any anime maps to the TMDB title (kind: series |
// movie): the title is an anime the account lists can hold.
func (m *Map) HasTMDB(ctx context.Context, kind string, tmdbID int64) (bool, error) {
	var one int
	err := m.DB.QueryRowContext(ctx, `SELECT 1 FROM anime_map WHERE tmdb_id=? AND kind=? LIMIT 1`, tmdbID, kind).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// FirstAnime is the anime that stands for a TMDB title (kind: series |
// movie) when nothing else says which: for a series the one mapped to its
// season 1 from the first episode (offset 0), for a movie any. Dataset
// rows come before cached search hits, then the lowest MAL id.
func (m *Map) FirstAnime(ctx context.Context, kind string, tmdbID int64) (Entry, bool, error) {
	q := `SELECT mal_id, kind, tmdb_id, tmdb_season, tmdb_offset, anidb_id FROM anime_map WHERE tmdb_id=? AND kind=?`
	if kind == "series" {
		q += ` AND tmdb_season=1 AND tmdb_offset=0`
	}
	q += ` ORDER BY source='dataset' DESC, mal_id LIMIT 1`
	var e Entry
	err := m.DB.QueryRowContext(ctx, q, tmdbID, kind).Scan(&e.MALID, &e.Kind, &e.TMDBID, &e.Season, &e.Offset, &e.AniDBID)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	return e, err == nil, err
}
