package tracking

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// Store persists tracking data in SQLite.
type Store struct {
	DB *sql.DB
	// ExtraTracked returns additional title IDs to treat as tracked
	// (nil = none).
	ExtraTracked func(ctx context.Context) ([]int64, error)
}

const titleCols = `id, kind, tmdb_id, shikimori_id, myshows_id, anidb_id, imdb_id, name, poster, status, cert, next_check, refreshed_at`

func tstr(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func tparse(s sql.NullString) time.Time {
	if !s.Valid {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s.String)
	if err != nil {
		return time.Time{}
	}
	return t
}

func ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

type scanner interface{ Scan(...any) error }

func scanTitle(r scanner) (Title, error) {
	var t Title
	var sh, ms, an sql.NullInt64
	var nc, rf sql.NullString
	if err := r.Scan(&t.ID, &t.Kind, &t.TMDBID, &sh, &ms, &an, &t.IMDbID, &t.Name, &t.Poster, &t.Status, &t.Cert, &nc, &rf); err != nil {
		return Title{}, err
	}
	t.ShikimoriID, t.MyShowsID, t.AniDBID = ptr(sh), ptr(ms), ptr(an)
	t.NextCheck, t.Refreshed = tparse(nc), tparse(rf)
	return t, nil
}

func (s *Store) queryTitles(ctx context.Context, q string, args ...any) ([]Title, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Title
	for rows.Next() {
		t, err := scanTitle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// EnsureTitle returns the title for (kind, tmdbID), creating it when absent.
func (s *Store) EnsureTitle(ctx context.Context, kind string, tmdbID int64, name, poster string) (Title, error) {
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO tracked_titles(kind, tmdb_id, name, poster) VALUES(?,?,?,?)
		 ON CONFLICT(kind, tmdb_id) DO NOTHING`, kind, tmdbID, name, poster); err != nil {
		return Title{}, err
	}
	t, ok, err := s.TitleByTMDB(ctx, kind, tmdbID)
	if err != nil {
		return Title{}, err
	}
	if !ok {
		return Title{}, sql.ErrNoRows
	}
	return t, nil
}

// TitleByTMDB looks a title up by kind and TMDB id.
func (s *Store) TitleByTMDB(ctx context.Context, kind string, tmdbID int64) (Title, bool, error) {
	t, err := scanTitle(s.DB.QueryRowContext(ctx,
		`SELECT `+titleCols+` FROM tracked_titles WHERE kind=? AND tmdb_id=?`, kind, tmdbID))
	if err == sql.ErrNoRows {
		return Title{}, false, nil
	}
	if err != nil {
		return Title{}, false, err
	}
	return t, true, nil
}

// SetMark creates or replaces a profile's mark on a title.
func (s *Store) SetMark(ctx context.Context, m Mark) error {
	origin := m.Origin
	if origin == "" {
		origin = "antresol"
	}
	at := m.UpdatedAt
	if at.IsZero() {
		at = time.Now()
	}
	var score any
	if m.Score != nil {
		score = *m.Score
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO title_marks(user_id, title_id, state, score, origin, updated_at) VALUES(?,?,?,?,?,?)
		 ON CONFLICT(user_id, title_id) DO UPDATE SET state=excluded.state, score=excluded.score,
		   origin=excluded.origin, updated_at=excluded.updated_at`,
		m.UserID, m.TitleID, string(m.State), score, origin, at.UTC().Format(time.RFC3339))
	return err
}

// DeleteMark removes a profile's mark on a title.
func (s *Store) DeleteMark(ctx context.Context, userID, titleID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM title_marks WHERE user_id=? AND title_id=?`, userID, titleID)
	return err
}

// Marks lists a profile's marks, optionally limited to the given states.
func (s *Store) Marks(ctx context.Context, userID int64, states ...MarkState) ([]Mark, error) {
	q := `SELECT user_id, title_id, state, score, origin, updated_at FROM title_marks WHERE user_id=?`
	args := []any{userID}
	if len(states) > 0 {
		q += ` AND state IN (?` + strings.Repeat(",?", len(states)-1) + `)`
		for _, st := range states {
			args = append(args, string(st))
		}
	}
	q += ` ORDER BY updated_at DESC, title_id`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mark
	for rows.Next() {
		var m Mark
		var st, up string
		var sc sql.NullInt64
		if err := rows.Scan(&m.UserID, &m.TitleID, &st, &sc, &m.Origin, &up); err != nil {
			return nil, err
		}
		m.State = MarkState(st)
		if sc.Valid {
			v := int(sc.Int64)
			m.Score = &v
		}
		m.UpdatedAt, _ = time.Parse(time.RFC3339, up)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReplaceEpisodes replaces a title's whole episode list.
func (s *Store) ReplaceEpisodes(ctx context.Context, titleID int64, eps []Episode) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM title_episodes WHERE title_id=?`, titleID); err != nil {
		return err
	}
	for _, e := range eps {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO title_episodes(title_id, season, number, name, air_date) VALUES(?,?,?,?,?)
			 ON CONFLICT(title_id, season, number) DO UPDATE SET name=excluded.name, air_date=excluded.air_date`,
			titleID, e.Season, e.Number, e.Name, e.AirDate); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertSources syncs the sources of one kind for a title: rows not in srcs
// are deleted, known rows get last_seen (and details) refreshed, and the rows
// new for that title+kind are returned.
func (s *Store) UpsertSources(ctx context.Context, titleID int64, kind string, srcs []Source, now time.Time) ([]Source, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	type key struct {
		season, ep int
		ref        string
	}
	existing := map[key]bool{}
	rows, err := tx.QueryContext(ctx,
		`SELECT season, episode, ref FROM episode_sources WHERE title_id=? AND kind=?`, titleID, kind)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.season, &k.ep, &k.ref); err != nil {
			rows.Close()
			return nil, err
		}
		existing[k] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	ts := now.UTC().Format(time.RFC3339)
	keep := map[key]bool{}
	var added []Source
	for _, src := range srcs {
		k := key{src.Season, src.Episode, src.Ref}
		if keep[k] {
			continue
		}
		keep[k] = true
		labels := src.Labels
		if labels == nil {
			labels = []string{}
		}
		lj, _ := json.Marshal(labels)
		if existing[k] {
			if _, err := tx.ExecContext(ctx,
				`UPDATE episode_sources SET provider=?, quality=?, labels_json=?, last_seen=?
				 WHERE title_id=? AND season=? AND episode=? AND kind=? AND ref=?`,
				src.Provider, src.Quality, string(lj), ts, titleID, src.Season, src.Episode, kind, src.Ref); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO episode_sources(title_id, season, episode, kind, ref, provider, quality, labels_json, first_seen, last_seen)
			 VALUES(?,?,?,?,?,?,?,?,?,?)`,
			titleID, src.Season, src.Episode, kind, src.Ref, src.Provider, src.Quality, string(lj), ts, ts); err != nil {
			return nil, err
		}
		n := src
		n.TitleID, n.Kind, n.FirstSeen, n.LastSeen = titleID, kind, now, now
		added = append(added, n)
	}
	for k := range existing {
		if keep[k] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM episode_sources WHERE title_id=? AND season=? AND episode=? AND kind=? AND ref=?`,
			titleID, k.season, k.ep, kind, k.ref); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return added, nil
}

// Sources lists all sources of a title.
func (s *Store) Sources(ctx context.Context, titleID int64) ([]Source, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT title_id, season, episode, kind, ref, provider, quality, labels_json, first_seen, last_seen
		 FROM episode_sources WHERE title_id=? ORDER BY season, episode, kind, ref`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var x Source
		var lj, fs, ls string
		if err := rows.Scan(&x.TitleID, &x.Season, &x.Episode, &x.Kind, &x.Ref, &x.Provider, &x.Quality, &lj, &fs, &ls); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(lj), &x.Labels)
		x.FirstSeen, _ = time.Parse(time.RFC3339, fs)
		x.LastSeen, _ = time.Parse(time.RFC3339, ls)
		out = append(out, x)
	}
	return out, rows.Err()
}

// trackedFilter is the WHERE clause (and args) selecting tracked titles.
func (s *Store) trackedFilter(ctx context.Context) (string, []any, error) {
	where := `id IN (SELECT title_id FROM ` + interest + `)`
	var args []any
	if s.ExtraTracked != nil {
		ids, err := s.ExtraTracked(ctx)
		if err != nil {
			return "", nil, err
		}
		if len(ids) > 0 {
			where += ` OR id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`
			for _, id := range ids {
				args = append(args, id)
			}
		}
	}
	return "(" + where + ")", args, nil
}

// Tracked lists titles with any mark in planned|watching|on_hold or a
// watchlist row of their library item with no mark (see interest), plus
// those reported by ExtraTracked.
func (s *Store) Tracked(ctx context.Context) ([]Title, error) {
	w, args, err := s.trackedFilter(ctx)
	if err != nil {
		return nil, err
	}
	return s.queryTitles(ctx, `SELECT `+titleCols+` FROM tracked_titles WHERE `+w+` ORDER BY id`, args...)
}

// Due lists tracked titles whose next_check has passed (or is unset), oldest
// first.
func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]Title, error) {
	w, args, err := s.trackedFilter(ctx)
	if err != nil {
		return nil, err
	}
	args = append(args, now.UTC().Format(time.RFC3339), limit)
	return s.queryTitles(ctx,
		`SELECT `+titleCols+` FROM tracked_titles
		 WHERE `+w+` AND (next_check IS NULL OR next_check <= ?)
		 ORDER BY next_check IS NOT NULL, next_check, id LIMIT ?`, args...)
}

// SetChecked records that a title was refreshed at now, its airing status
// ("" keeps the stored one) and when to look again.
func (s *Store) SetChecked(ctx context.Context, titleID int64, status string, next, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE tracked_titles SET status=COALESCE(NULLIF(?, ''), status), next_check=?, refreshed_at=? WHERE id=?`,
		status, tstr(next), now.UTC().Format(time.RFC3339), titleID)
	return err
}

// EnsureWatchlistTitles makes the title row of every library movie or
// series with a TMDB id that some profile has on its watchlist (such a
// title reads as planned and is tracked: interest); existing rows stay.
func (s *Store) EnsureWatchlistTitles(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO tracked_titles(kind, tmdb_id, name, poster)
		SELECT i.kind, i.tmdb_id, MIN(i.title), '' FROM watchlist w JOIN items i ON i.id=w.item_id
		WHERE i.kind IN ('movie','series') AND i.tmdb_id > 0
		GROUP BY i.kind, i.tmdb_id
		ON CONFLICT(kind, tmdb_id) DO NOTHING`)
	return err
}

// TitleByID looks a title up by its id.
func (s *Store) TitleByID(ctx context.Context, id int64) (Title, bool, error) {
	t, err := scanTitle(s.DB.QueryRowContext(ctx, `SELECT `+titleCols+` FROM tracked_titles WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return Title{}, false, nil
	}
	if err != nil {
		return Title{}, false, err
	}
	return t, true, nil
}

// Interested lists the profiles following the title, in id order: a mark
// in planned|watching|on_hold, or its library item on their watchlist with
// no mark of their own (see interest).
func (s *Store) Interested(ctx context.Context, titleID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT DISTINCT user_id FROM `+interest+` WHERE title_id=? ORDER BY user_id`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ScannedKinds is the set of source kinds that have scanned the title
// successfully before.
func (s *Store) ScannedKinds(ctx context.Context, titleID int64) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT kind FROM tracked_scans WHERE title_id=?`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// MarkScanned records a successful scan of the title by a source kind.
func (s *Store) MarkScanned(ctx context.Context, titleID int64, kind string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO tracked_scans(title_id, kind) VALUES(?,?) ON CONFLICT DO NOTHING`, titleID, kind)
	return err
}

// SetInfo stores what a refresh learnt of a title: the certification when
// known, the name and poster only where none is stored (a name given when
// the title was marked wins).
func (s *Store) SetInfo(ctx context.Context, titleID int64, info TitleInfo) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE tracked_titles SET cert=COALESCE(NULLIF(?, ''), cert),
		   name=CASE WHEN name='' THEN ? ELSE name END,
		   poster=CASE WHEN poster='' THEN ? ELSE poster END WHERE id=?`,
		info.Cert, info.Name, info.Poster, titleID)
	return err
}

// Episodes lists a title's stored episode list in season and number order.
func (s *Store) Episodes(ctx context.Context, titleID int64) ([]Episode, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT title_id, season, number, name, air_date FROM title_episodes WHERE title_id=? ORDER BY season, number`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Episode
	for rows.Next() {
		var e Episode
		if err := rows.Scan(&e.TitleID, &e.Season, &e.Number, &e.Name, &e.AirDate); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
