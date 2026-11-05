package notify

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// State keys of notify_state.
const (
	// stateAdded: the newest added_at looked at, and the items of the last
	// freshWindow before it that were looked at (addedMark).
	stateAdded        = "added"
	stateWatchMissing = "watch_missing" // watchlist titles missing from disk: [[user, item], …]
)

// freshWindow: items added this long before the mark are looked at again
// (a scan's write that committed after a later one's), unless the mark
// lists them already. Ids are not trusted alone: one can come back for a
// new row after a delete, so a row is named by its id and its added_at.
const freshWindow = 15 * time.Minute

// addedMark is the value of stateAdded.
type addedMark struct {
	At   string   `json:"at"`   // RFC 3339 UTC, as items.added_at
	Seen []string `json:"seen"` // "<id>@<added_at>"
}

func (s *Service) state(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM notify_state WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (s *Service) setState(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO notify_state(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// fresh is a newly added episode or movie.
type fresh struct {
	id int64
	at string
}

func (f fresh) name() string { return strconv.FormatInt(f.id, 10) + "@" + f.at }

// freshItems are the episodes and movies added since the mark that were
// not looked at yet, and the mark to keep after them. first: there was no
// mark (the library as it is now is no news).
func (s *Service) freshItems(ctx context.Context) (out []fresh, next addedMark, first bool, err error) {
	v, ok, err := s.state(ctx, stateAdded)
	if err != nil {
		return nil, next, false, err
	}
	var mark addedMark
	if ok {
		if json.Unmarshal([]byte(v), &mark) != nil {
			ok = false
		}
	}
	since := ""
	if ok && mark.At != "" {
		if t, perr := time.Parse(time.RFC3339, mark.At); perr == nil {
			since = t.Add(-freshWindow).UTC().Format(time.RFC3339)
		}
	}
	if !ok {
		// the library's own clock (added_at is the wall time of its writes)
		var newest string
		if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(added_at), strftime('%Y-%m-%dT%H:%M:%SZ','now')) FROM items`).Scan(&newest); err != nil {
			return nil, next, false, err
		}
		t, _ := time.Parse(time.RFC3339, newest)
		since = t.Add(-freshWindow).UTC().Format(time.RFC3339)
		mark = addedMark{At: newest}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, added_at FROM items WHERE added_at >= ? AND kind IN ('episode', 'movie')
		AND missing_since IS NULL ORDER BY added_at, id`, since)
	if err != nil {
		return nil, next, false, err
	}
	seen := map[string]bool{}
	for _, n := range mark.Seen {
		seen[n] = true
	}
	var all []fresh
	for rows.Next() {
		var f fresh
		if err := rows.Scan(&f.id, &f.at); err != nil {
			rows.Close()
			return nil, next, false, err
		}
		all = append(all, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, next, false, err
	}
	next = addedMark{At: mark.At}
	for _, f := range all {
		if f.at > next.At {
			next.At = f.at
		}
		if ok && !seen[f.name()] {
			out = append(out, f)
		}
	}
	// the window before the new mark: what was looked at in it
	t, _ := time.Parse(time.RFC3339, next.At)
	keep := t.Add(-freshWindow).UTC().Format(time.RFC3339)
	for _, f := range all {
		if f.at >= keep {
			next.Seen = append(next.Seen, f.name())
		}
	}
	return out, next, !ok, nil
}

// Collect queues what the library gained since the last call: new episodes
// for the profiles that watch their series, titles the profiles wanted,
// then runs the Collectors. The first call only marks where the library
// is (what it has already is no news). It runs after each scan.
func (s *Service) Collect(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.collectMu.Lock()
	defer s.collectMu.Unlock()
	items, mark, first, err := s.freshItems(ctx)
	if err != nil {
		return err
	}
	if len(items) > 0 {
		if err := s.collectEpisodes(ctx, items); err != nil {
			return err
		}
		if err := s.collectTitles(ctx, items); err != nil {
			return err
		}
	}
	b, _ := json.Marshal(mark)
	if err := s.setState(ctx, stateAdded, string(b)); err != nil {
		return err
	}
	if err := s.collectReturned(ctx, !first); err != nil {
		return err
	}
	var errs []error
	for _, c := range s.Collectors {
		if err := c(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// visibleTo reports whether uid may see a title rated rating (kids' limits).
func (s *Service) visibleTo(ctx context.Context, uid int64, kind, rating string) bool {
	if s.Users == nil {
		return true
	}
	u, err := s.Users.Get(ctx, uid)
	if err != nil {
		return false
	}
	return library.RatingAllowed(u.RatingLimit(), library.Item{Kind: kind, Rating: rating})
}

// idList is "?,?,…" for the ids of items and the ids as arguments.
func idList(items []fresh) (string, []any) {
	args := make([]any, len(items))
	for i, f := range items {
		args[i] = f.id
	}
	return "?" + strings.Repeat(",?", len(items)-1), args
}

// batches splits items into parts small enough for one query.
func batches(items []fresh) [][]fresh {
	const n = 400
	var out [][]fresh
	for len(items) > n {
		out = append(out, items[:n])
		items = items[n:]
	}
	if len(items) > 0 {
		out = append(out, items)
	}
	return out
}

type newEpisode struct {
	id, seriesID     int64
	season, episode  int
	seriesTitle      string
	seriesTmdb       int64
	rating, metaJSON string
	seriesYear       int
	addedAt          string
	title, path      string // the episode's title, its first file
}

// collectEpisodes: the new episodes among items.
func (s *Service) collectEpisodes(ctx context.Context, items []fresh) error {
	want, err := s.wanting(ctx, Episodes)
	if err != nil || len(want) == 0 {
		return err
	}
	var eps []newEpisode
	newIDs := map[int64]bool{}
	for _, part := range batches(items) {
		in, args := idList(part)
		rows, err := s.DB.QueryContext(ctx, `SELECT e.id, COALESCE(e.season_no, 0), COALESCE(e.episode_no, 0),
			sr.id, sr.title, sr.year, COALESCE(sr.tmdb_id, 0), sr.rating, sr.meta_json, e.added_at, e.title,
			COALESCE((SELECT MIN(f.path) FROM files f WHERE f.item_id = e.id), '')
			FROM items e
			JOIN items p ON p.id = e.parent_id
			JOIN items sr ON sr.id = CASE WHEN p.kind = 'series' THEN p.id ELSE p.parent_id END
			WHERE e.kind = 'episode' AND e.id IN (`+in+`) AND e.missing_since IS NULL AND sr.kind = 'series'
			ORDER BY e.added_at, e.id`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var e newEpisode
			if err := rows.Scan(&e.id, &e.season, &e.episode, &e.seriesID, &e.seriesTitle, &e.seriesYear, &e.seriesTmdb, &e.rating, &e.metaJSON, &e.addedAt, &e.title, &e.path); err != nil {
				rows.Close()
				return err
			}
			eps = append(eps, e)
			newIDs[e.id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	watchers := map[int64][]int64{} // series → profiles
	for _, e := range eps {
		users, seen := watchers[e.seriesID]
		if !seen {
			users, err = s.seriesWatchers(ctx, e.seriesID, e.seriesTmdb, newIDs)
			if err != nil {
				return err
			}
			watchers[e.seriesID] = users
		}
		for _, uid := range users {
			if _, ok := want[uid]; !ok || !s.visibleTo(ctx, uid, library.KindSeries, e.rating) {
				continue
			}
			// a row made again (restored, rescanned after a move) of what
			// the profile was told about is no news: by its numbers, its file
			key, path := e.key(), pathKey(e.path)
			if old, err := s.seenAny(ctx, uid, 0, key, path); err != nil || old {
				if err != nil {
					return err
				}
				continue
			}
			ok, err := s.insert(ctx, Event{UserID: uid, Topic: Episodes, Key: key,
				ItemID: e.seriesID, GroupID: e.seriesID, Data: Data{Title: e.seriesTitle, Year: e.seriesYear, Kind: library.KindSeries,
					Season: e.season, Episode: e.episode, EpisodeTitle: e.title, Poster: posterOf(e.metaJSON)}})
			if err != nil {
				return err
			}
			if ok {
				if err := s.markSeen(ctx, uid, key, path); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// key names an episode by its series and numbers (a TMDB series by its
// id: a series row made again keeps it), one without numbers by its row.
func (e newEpisode) key() string {
	series := "i" + strconv.FormatInt(e.seriesID, 10)
	if e.seriesTmdb > 0 {
		series = "t" + strconv.FormatInt(e.seriesTmdb, 10)
	}
	if e.episode <= 0 {
		return "ep:" + series + ":" + strconv.FormatInt(e.id, 10) + "@" + e.addedAt
	}
	return "ep:" + series + ":S" + strconv.Itoa(e.season) + "E" + strconv.Itoa(e.episode)
}

// pathKey names a file ("" without one).
func pathKey(p string) string {
	if p == "" {
		return ""
	}
	h := sha256.Sum256([]byte(p))
	return "path:" + hex.EncodeToString(h[:12])
}

// seenAny reports whether uid was told of any of keys ("" skipped), since
// since (Unix seconds; 0: ever).
func (s *Service) seenAny(ctx context.Context, uid, since int64, keys ...string) (bool, error) {
	for _, k := range keys {
		if k == "" {
			continue
		}
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notify_seen WHERE user_id=? AND key=? AND at >= ?`, uid, k, since).Scan(&n); err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) markSeen(ctx context.Context, uid int64, keys ...string) error {
	for _, k := range keys {
		if k == "" {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO notify_seen(user_id, key, at) VALUES(?,?,?)
			ON CONFLICT(user_id, key) DO UPDATE SET at=excluded.at`, uid, k, s.now().Unix()); err != nil {
			return err
		}
	}
	return nil
}

// seriesWatchers: the profiles with progress in a series (on its episodes
// other than the new ones), with it in «Хочу посмотреть», or interested by
// an InterestFunc.
func (s *Service) seriesWatchers(ctx context.Context, seriesID, tmdbID int64, newIDs map[int64]bool) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id, item_id FROM progress WHERE item_id = ?
			OR item_id IN (SELECT id FROM items WHERE parent_id = ?)
			OR item_id IN (SELECT e.id FROM items e JOIN items se ON se.id = e.parent_id WHERE se.parent_id = ?)
		UNION SELECT user_id, 0 FROM watchlist WHERE item_id = ?`, seriesID, seriesID, seriesID, seriesID)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	var out []int64
	for rows.Next() {
		var id, item int64
		if err := rows.Scan(&id, &item); err != nil {
			rows.Close()
			return nil, err
		}
		if !seen[id] && !newIDs[item] {
			seen[id] = true
			out = append(out, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	more, err := s.interested(ctx, library.KindSeries, tmdbID)
	if err != nil {
		return nil, err
	}
	for _, id := range more {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func (s *Service) interested(ctx context.Context, kind string, tmdbID int64) ([]int64, error) {
	if tmdbID <= 0 {
		return nil, nil
	}
	var out []int64
	for _, f := range s.Interest {
		ids, err := f(ctx, kind, tmdbID)
		if err != nil {
			return nil, err
		}
		out = append(out, ids...)
	}
	return out, nil
}

// collectTitles: the new movies among items that a profile wanted before
// the library had them (an InterestFunc).
func (s *Service) collectTitles(ctx context.Context, items []fresh) error {
	if len(s.Interest) == 0 {
		return nil
	}
	want, err := s.wanting(ctx, Available)
	if err != nil || len(want) == 0 {
		return err
	}
	type title struct {
		id, tmdb                   int64
		kind, name, rating, mj, at string
		year                       int
	}
	var ts []title
	for _, part := range batches(items) {
		in, args := idList(part)
		rows, err := s.DB.QueryContext(ctx, `SELECT id, kind, title, year, COALESCE(tmdb_id, 0), rating, meta_json, added_at FROM items
			WHERE id IN (`+in+`) AND kind = 'movie' AND missing_since IS NULL AND COALESCE(tmdb_id, 0) > 0 ORDER BY added_at, id`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var t title
			if err := rows.Scan(&t.id, &t.kind, &t.name, &t.year, &t.tmdb, &t.rating, &t.mj, &t.at); err != nil {
				rows.Close()
				return err
			}
			ts = append(ts, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	for _, t := range ts {
		users, err := s.interested(ctx, t.kind, t.tmdb)
		if err != nil {
			return err
		}
		for _, uid := range users {
			if _, ok := want[uid]; !ok || !s.visibleTo(ctx, uid, t.kind, t.rating) {
				continue
			}
			if _, err := s.insert(ctx, Event{UserID: uid, Topic: Available, Key: "new:" + strconv.FormatInt(t.id, 10) + "@" + t.at, ItemID: t.id,
				Data: Data{Title: t.name, Year: t.year, Kind: t.kind, Poster: posterOf(t.mj)}}); err != nil {
				return err
			}
		}
	}
	return nil
}

// collectReturned: «Хочу посмотреть» titles whose files were missing (a
// disk away, a folder moved) and play again. marked: the state existed
// before this call (the first call only records what is missing).
func (s *Service) collectReturned(ctx context.Context, marked bool) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT w.user_id, w.item_id FROM watchlist w JOIN items i ON i.id = w.item_id
		WHERE i.missing_since IS NOT NULL ORDER BY w.user_id, w.item_id LIMIT 5000`)
	if err != nil {
		return err
	}
	now := [][2]int64{}
	isNow := map[[2]int64]bool{}
	for rows.Next() {
		var p [2]int64
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			rows.Close()
			return err
		}
		now = append(now, p)
		isNow[p] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	v, _, err := s.state(ctx, stateWatchMissing)
	if err != nil {
		return err
	}
	var before [][2]int64
	_ = json.Unmarshal([]byte(v), &before)
	if marked && len(before) > 0 {
		want, err := s.wanting(ctx, Available)
		if err != nil {
			return err
		}
		for _, p := range before {
			if isNow[p] {
				continue
			}
			if _, ok := want[p[0]]; !ok {
				continue
			}
			var kind, title, rating, mj string
			var year int
			err := s.DB.QueryRowContext(ctx, `SELECT i.kind, i.title, i.year, i.rating, i.meta_json FROM items i JOIN watchlist w ON w.item_id = i.id AND w.user_id = ?
				WHERE i.id = ? AND i.missing_since IS NULL`, p[0], p[1]).Scan(&kind, &title, &year, &rating, &mj)
			if errors.Is(err, sql.ErrNoRows) {
				continue // gone for good, or off the list
			}
			if err != nil {
				return err
			}
			if !s.visibleTo(ctx, p[0], kind, rating) {
				continue
			}
			// a title that comes and goes (a flaky disk) is news once a month
			seenKey := "back:" + strconv.FormatInt(p[1], 10)
			if again, err := s.seenAny(ctx, p[0], s.now().Add(-returnGap).Unix(), seenKey); err != nil || again {
				if err != nil {
					return err
				}
				continue
			}
			key := seenKey + ":" + strconv.FormatInt(s.now().Unix()/86400, 10)
			ok, err := s.insert(ctx, Event{UserID: p[0], Topic: Available, Key: key, ItemID: p[1],
				Data: Data{Title: title, Year: year, Kind: kind, Poster: posterOf(mj)}})
			if err != nil {
				return err
			}
			if ok {
				if err := s.markSeen(ctx, p[0], seenKey); err != nil {
					return err
				}
			}
		}
	}
	b, _ := json.Marshal(now)
	return s.setState(ctx, stateWatchMissing, string(b))
}
