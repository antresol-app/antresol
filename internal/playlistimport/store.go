package playlistimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Job statuses: reading → preview → matching → downloading → done; failed,
// cancelled.
const (
	StatusReading     = "reading"
	StatusPreview     = "preview"
	StatusMatching    = "matching"
	StatusDownloading = "downloading"
	StatusDone        = "done"
	StatusFailed      = "failed"
	StatusCancelled   = "cancelled"
)

// Track states.
const (
	TrackPending     = "pending"
	TrackMatched     = "matched"
	TrackReview      = "review"
	TrackSkipped     = "skipped"
	TrackHave        = "have"
	TrackQueued      = "queued"
	TrackDownloading = "downloading"
	TrackDone        = "done"
	TrackNotFound    = "not_found"
	TrackFailed      = "failed"
)

// Group states.
const (
	GroupWaiting      = "waiting"
	GroupSearching    = "searching"
	GroupWaitingSpace = "waiting_space"
	// GroupWaitingDownload: the album downloads elsewhere (another job's
	// download, or «На сервер»): Group.WaitingFor names it.
	GroupWaitingDownload = "waiting_download"
	GroupDownloading     = "downloading"
	GroupDone            = "done"
	GroupHave            = "have"
	GroupNotFound        = "not_found"
	// GroupNoFormat: releases exist, none in «Формат загрузки»; it waits
	// for «Скачать в другом формате» (Group.AnyFormat).
	GroupNoFormat = "no_format"
	GroupFailed   = "failed"
)

// Store errors (their text is shown as is).
var (
	ErrNotFound = errors.New("Не найдено")
	ErrBusy     = errors.New("Импорт уже идёт")
	ErrStarted  = errors.New("Импорт уже начат")
)

// Store keeps import jobs in the database.
type Store struct {
	DB  *sql.DB
	Now func() time.Time // nil: time.Now
}

// Job is one import run.
type Job struct {
	ID, UserID int64
	Service    string
	Account    string
	Status     string
	Favorites  bool // liked songs become «Избранное»
	Error      string
	Report     Report
	CreatedAt  time.Time
	UpdatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// Active reports whether the job is matching or downloading.
func (j Job) Active() bool { return j.Status == StatusMatching || j.Status == StatusDownloading }

// Report is what the import could not do (report_json).
type Report struct {
	SavedAlbumsMissing int `json:"savedAlbumsMissing"`
	// Unresolved: songs an uploaded file named only by an id (a Takeout
	// playlist video not in its library), left out of the import.
	Unresolved int `json:"unresolved"`
}

// Track is one song of a job.
type Track struct {
	ID, JobID      int64
	Key            string
	Artist, Title  string
	Album          string
	AlbumArtist    string
	ISRC           string
	DurationMs     int64
	AddedAt        *time.Time
	Liked          bool
	State, Reason  string
	MBRecording    string
	MBReleaseGroup string
	MBTitle        string
	MBArtist       string
	MBAlbum        string
	MBYear         int
	GroupID        int64 // 0: none
	ItemID         int64 // 0: none
}

// wantedSQL selects the job's songs to import (t: import_tracks, j: its
// import_jobs row): liked ones when «Избранное» is on, and those of the
// ticked playlists this job read.
const wantedSQL = `(t.liked = 1 AND j.favorites = 1 OR EXISTS (
	SELECT 1 FROM import_playlist_entries e JOIN import_playlists p ON p.id = e.import_playlist_id
	WHERE e.track_id = t.id AND p.job_id = t.job_id AND p.selected = 1))`

func (s *Store) now() string {
	n := time.Now
	if s.Now != nil {
		n = s.Now
	}
	return n().UTC().Format(time.RFC3339)
}

func fmtTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTime(ns sql.NullString) *time.Time {
	if !ns.Valid {
		return nil
	}
	t, err := time.Parse(time.RFC3339, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nextJobIDSQL is a new job's id: past every job's and every id a
// playlist names as its builder (built_job), so a deleted job's id is
// not given again (SQLite reuses the highest id of a table without
// AUTOINCREMENT) and a re-run is never taken for the job that built it.
const nextJobIDSQL = `(SELECT MAX(COALESCE((SELECT MAX(id) FROM import_jobs), 0), COALESCE((SELECT MAX(built_job) FROM import_playlists), 0)) + 1)`

// CreateJob stores a snapshot as a new job in preview.
func (s *Store) CreateJob(ctx context.Context, userID int64, snap Snapshot) (int64, error) {
	if err := snap.Normalize(); err != nil {
		return 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := openJobOf(ctx, tx, userID, snap.Service, 0); err != nil {
		return 0, err
	}
	now := s.now()
	res, err := tx.ExecContext(ctx, `INSERT INTO import_jobs(id, user_id, service, account, status, created_at, updated_at)
		VALUES (`+nextJobIDSQL+`, ?, ?, ?, ?, ?, ?)`, userID, snap.Service, snap.Account, StatusPreview, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := fillJob(ctx, tx, id, userID, snap); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// CreateReadingJob makes an empty job a connector fills later (FillJob).
func (s *Store) CreateReadingJob(ctx context.Context, userID int64, service string) (int64, error) {
	if (&Snapshot{Service: service}).Normalize() != nil {
		return 0, ErrUnknownService
	}
	now := s.now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO import_jobs(id, user_id, service, status, created_at, updated_at)
		VALUES (`+nextJobIDSQL+`, ?, ?, ?, ?, ?)`, userID, service, StatusReading, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FailReading fails every job still reading (at startup: a read does not
// survive a restart) and returns their ids.
func (s *Store) FailReading(ctx context.Context, errText string) ([]int64, error) {
	now := s.now()
	rows, err := s.DB.QueryContext(ctx, `UPDATE import_jobs SET status = ?, error = ?, updated_at = ?, finished_at = ?
		WHERE status = ? RETURNING id`, StatusFailed, errText, now, now, StatusReading)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FillJob stores what a connector read into a reading job, which moves to
// preview.
func (s *Store) FillJob(ctx context.Context, id int64, snap Snapshot) error {
	if err := snap.Normalize(); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var userID int64
	var status string
	switch err := tx.QueryRowContext(ctx, `SELECT user_id, status FROM import_jobs WHERE id = ?`, id).Scan(&userID, &status); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return err
	case status != StatusReading:
		return ErrStarted
	}
	if err := openJobOf(ctx, tx, userID, snap.Service, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE import_jobs SET status = ?, account = ?, updated_at = ? WHERE id = ?`,
		StatusPreview, snap.Account, s.now(), id); err != nil {
		return err
	}
	if err := fillJob(ctx, tx, id, userID, snap); err != nil {
		return err
	}
	return tx.Commit()
}

// OpenJob reports whether the profile has a job of the service in
// preview, matching or downloading (a new snapshot of it would be refused
// with ErrBusy).
func (s *Store) OpenJob(ctx context.Context, userID int64, service string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_jobs WHERE user_id = ? AND service = ?
		AND status IN ('preview', 'matching', 'downloading')`, userID, service).Scan(&n)
	return n > 0, err
}

// openJobOf refuses a new snapshot (ErrBusy) while the profile has a job
// of the same service in preview, matching or downloading (other than
// except): that job owns the service's playlists until it is done,
// cancelled or deleted, and a new snapshot would take them over.
func openJobOf(ctx context.Context, tx *sql.Tx, userID int64, service string, except int64) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_jobs WHERE user_id = ? AND service = ? AND id != ?
		AND status IN ('preview', 'matching', 'downloading')`, userID, service, except).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrBusy
	}
	return nil
}

// fillJob writes a normalized snapshot's songs, playlists (upserted by
// profile, service and playlist id: a re-run updates the same row and
// replaces its entries) and saved albums.
func fillJob(ctx context.Context, tx *sql.Tx, jobID, userID int64, snap Snapshot) error {
	ins, err := tx.PrepareContext(ctx, `INSERT INTO import_tracks(job_id, key, artist, title, album, album_artist, isrc,
		duration_ms, added_at, liked) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	ids := make(map[string]int64, len(snap.Tracks))
	for _, t := range snap.Tracks {
		res, err := ins.ExecContext(ctx, jobID, t.Key, t.Artist, t.Title, t.Album, t.AlbumArtist, t.ISRC, t.DurationMs,
			fmtTime(t.AddedAt), b2i(t.Liked))
		if err != nil {
			return fmt.Errorf("track %q: %w", t.Key, err)
		}
		if ids[t.Key], err = res.LastInsertId(); err != nil {
			return err
		}
	}
	entry, err := tx.PrepareContext(ctx, `INSERT INTO import_playlist_entries(import_playlist_id, position, track_id) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer entry.Close()
	for _, p := range snap.Playlists {
		var pid int64
		// an unreadable playlist starts unticked
		if err := tx.QueryRowContext(ctx, `INSERT INTO import_playlists(user_id, service, external_id, name, readable, job_id, selected)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(user_id, service, external_id) DO UPDATE SET
				name = excluded.name, readable = excluded.readable, job_id = excluded.job_id, selected = excluded.selected
			RETURNING id`, userID, snap.Service, p.ID, p.Name, b2i(p.Readable), jobID, b2i(p.Readable)).Scan(&pid); err != nil {
			return fmt.Errorf("playlist %q: %w", p.ID, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM import_playlist_entries WHERE import_playlist_id = ?`, pid); err != nil {
			return err
		}
		for i, k := range p.Entries {
			if _, err := entry.ExecContext(ctx, pid, i, ids[k]); err != nil {
				return err
			}
		}
	}
	for _, a := range snap.Albums {
		if _, err := tx.ExecContext(ctx, `INSERT INTO import_albums(job_id, key, artist, title, year, added_at) VALUES (?, ?, ?, ?, ?, ?)`,
			jobID, a.Key, a.Artist, a.Title, a.Year, fmtTime(a.AddedAt)); err != nil {
			return err
		}
	}
	return nil
}

const jobCols = `id, user_id, service, account, status, favorites, error, report_json, created_at, updated_at, started_at, finished_at`

type scanner interface{ Scan(...any) error }

func scanJob(r scanner) (Job, error) {
	var j Job
	var fav int
	var report, created, updated string
	var started, finished sql.NullString
	if err := r.Scan(&j.ID, &j.UserID, &j.Service, &j.Account, &j.Status, &fav, &j.Error, &report, &created, &updated,
		&started, &finished); err != nil {
		return j, err
	}
	j.Favorites = fav != 0
	_ = json.Unmarshal([]byte(report), &j.Report) // a broken report reads as empty
	if t := parseTime(sql.NullString{String: created, Valid: true}); t != nil {
		j.CreatedAt = *t
	}
	if t := parseTime(sql.NullString{String: updated, Valid: true}); t != nil {
		j.UpdatedAt = *t
	}
	j.StartedAt, j.FinishedAt = parseTime(started), parseTime(finished)
	return j, nil
}

// Job returns one job (ErrNotFound).
func (s *Store) Job(ctx context.Context, id int64) (Job, error) {
	j, err := scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobCols+` FROM import_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

// Jobs returns a profile's jobs, newest first.
func (s *Store) Jobs(ctx context.Context, userID int64) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+jobCols+` FROM import_jobs WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// jobStatus is the status of a job inside a transaction (ErrNotFound).
func jobStatus(ctx context.Context, tx *sql.Tx, id int64) (string, error) {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM import_jobs WHERE id = ?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return status, err
}

// SetOptions changes what a job in preview imports: «Избранное» on or off
// (nil: unchanged) and ticked playlists (by import playlist id; ids of
// other jobs are ignored). ErrStarted after the preview.
func (s *Store) SetOptions(ctx context.Context, id int64, favorites *bool, playlists map[int64]bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	switch status, err := jobStatus(ctx, tx, id); {
	case err != nil:
		return err
	case status != StatusPreview:
		return ErrStarted
	}
	now := s.now()
	if favorites != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE import_jobs SET favorites = ?, updated_at = ? WHERE id = ?`, b2i(*favorites), now, id); err != nil {
			return err
		}
	}
	for pid, on := range playlists {
		if _, err := tx.ExecContext(ctx, `UPDATE import_playlists SET selected = ? WHERE id = ? AND job_id = ?`, b2i(on), pid, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetStatus moves a job to status with an error text ("" clears it);
// matching/downloading stamp started_at, done/failed/cancelled
// finished_at. A cancelled job stays cancelled (cancel always wins); use
// Advance for a move that depends on the current status.
func (s *Store) SetStatus(ctx context.Context, id int64, status, errText string) error {
	_, err := s.move(ctx, id, "", status, errText)
	return err
}

// Advance moves a job from one status to another only if it is still in
// from (compare-and-set); moved false: it was not (cancelled meanwhile).
func (s *Store) Advance(ctx context.Context, id int64, from, to, errText string) (bool, error) {
	return s.move(ctx, id, from, to, errText)
}

// move is SetStatus (from "") and Advance.
func (s *Store) move(ctx context.Context, id int64, from, to, errText string) (bool, error) {
	now := s.now()
	stamps := ""
	switch to {
	case StatusMatching, StatusDownloading: // a resolved review song re-opens a done job
		stamps = ", started_at = COALESCE(started_at, ?1), finished_at = NULL"
	case StatusDone, StatusFailed, StatusCancelled:
		stamps = ", finished_at = ?1"
	}
	// from "": anything but cancelled (unless cancelling); else exactly from
	res, err := s.DB.ExecContext(ctx, `UPDATE import_jobs SET status = ?2, error = ?3, updated_at = ?1`+stamps+
		` WHERE id = ?4 AND (?5 = '' AND (status != 'cancelled' OR ?2 = 'cancelled') OR status = ?5)`, now, to, errText, id, from)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return true, nil
	}
	if _, err := s.Job(ctx, id); err != nil {
		return false, err // ErrNotFound
	}
	return false, nil
}

// StartJob moves a job from preview to matching. ErrStarted: it is past
// the preview; ErrBusy: another job is matching or downloading.
func (s *Store) StartJob(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	switch status, err := jobStatus(ctx, tx, id); {
	case err != nil:
		return err
	case status != StatusPreview:
		return ErrStarted
	}
	var busy int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_jobs WHERE status IN ('matching', 'downloading')`).Scan(&busy); err != nil {
		return err
	}
	if busy > 0 {
		return ErrBusy
	}
	now := s.now()
	if _, err := tx.ExecContext(ctx, `UPDATE import_jobs SET status = ?, error = '', started_at = ?, updated_at = ? WHERE id = ?`,
		StatusMatching, now, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

// CancelJob stops a job that has not finished: it becomes cancelled and
// its groups not yet handed to the download client are dropped (running
// acquisitions stay on the downloads screen). A finished job is unchanged.
func (s *Store) CancelJob(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	switch status, err := jobStatus(ctx, tx, id); {
	case err != nil:
		return err
	case status == StatusDone || status == StatusFailed || status == StatusCancelled:
		return nil
	}
	now := s.now()
	if _, err := tx.ExecContext(ctx, `UPDATE import_jobs SET status = ?, updated_at = ?, finished_at = ? WHERE id = ?`,
		StatusCancelled, now, now, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM import_groups WHERE job_id = ? AND state IN ('waiting', 'searching', 'waiting_space', 'waiting_download')`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteJob removes a job that is not running (ErrBusy).
func (s *Store) DeleteJob(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	switch status, err := jobStatus(ctx, tx, id); {
	case err != nil:
		return err
	case status == StatusMatching || status == StatusDownloading:
		return ErrBusy
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM import_jobs WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ActiveJob returns the job that is matching or downloading (ok false: none).
func (s *Store) ActiveJob(ctx context.Context) (Job, bool, error) {
	j, err := scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobCols+` FROM import_jobs
		WHERE status IN ('matching', 'downloading') ORDER BY id LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return j, false, nil
	}
	return j, err == nil, err
}

// ResetSearching puts groups whose release search a stop interrupted back
// in the queue; the attempt is not counted.
func (s *Store) ResetSearching(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE import_groups SET state = ?, updated_at = ? WHERE state = ?`,
		GroupWaiting, s.now(), GroupSearching)
	return err
}

const trackCols = `t.id, t.job_id, t.key, t.artist, t.title, t.album, t.album_artist, t.isrc, t.duration_ms, t.added_at, t.liked,
	t.state, t.reason, t.mb_recording, t.mb_release_group, t.mb_title, t.mb_artist, t.mb_album, t.mb_year,
	COALESCE(t.group_id, 0), COALESCE(t.item_id, 0)`

func scanTrack(r scanner) (Track, error) {
	var t Track
	var added sql.NullString
	var liked int
	err := r.Scan(&t.ID, &t.JobID, &t.Key, &t.Artist, &t.Title, &t.Album, &t.AlbumArtist, &t.ISRC, &t.DurationMs, &added, &liked,
		&t.State, &t.Reason, &t.MBRecording, &t.MBReleaseGroup, &t.MBTitle, &t.MBArtist, &t.MBAlbum, &t.MBYear, &t.GroupID, &t.ItemID)
	t.AddedAt, t.Liked = parseTime(added), liked != 0
	return t, err
}

// NextPending returns up to limit selected songs still to match, in
// snapshot order.
func (s *Store) NextPending(ctx context.Context, jobID int64, limit int) ([]Track, error) {
	return s.tracks(ctx, `SELECT `+trackCols+` FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id
		WHERE t.job_id = ? AND t.state = 'pending' AND `+wantedSQL+` ORDER BY t.id LIMIT ?`, jobID, limit)
}

func (s *Store) tracks(ctx context.Context, query string, args ...any) ([]Track, error) {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Track
	for rows.Next() {
		t, err := scanTrack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Track returns one song (ErrNotFound).
func (s *Store) Track(ctx context.Context, id int64) (Track, error) {
	t, err := scanTrack(s.DB.QueryRowContext(ctx, `SELECT `+trackCols+` FROM import_tracks t WHERE t.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// ReviewTracks returns the selected songs waiting for the admin's
// decision, in snapshot order.
func (s *Store) ReviewTracks(ctx context.Context, jobID int64) ([]Track, error) {
	return s.tracks(ctx, `SELECT `+trackCols+` FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id
		WHERE t.job_id = ? AND t.state = 'review' AND `+wantedSQL+` ORDER BY t.id`, jobID)
}

// MatcherVersion numbers the matching rules (match_ver): a song in review
// that older rules decided is matched again once by the current ones
// (Service.rematchStep); a song the admin skipped stays skipped. Raised
// with every matcher change that may match more.
const MatcherVersion = 2

// SetDecision records a match decision for a song.
func (s *Store) SetDecision(ctx context.Context, trackID int64, d Decision) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE import_tracks SET state = ?, reason = ?, mb_recording = ?, mb_release_group = ?,
		mb_title = ?, mb_artist = ?, mb_album = ?, mb_year = ?, match_ver = ? WHERE id = ?`,
		d.State, d.Reason, d.RecordingID, d.ReleaseGroup, d.Title, d.Artist, d.Album, d.Year, MatcherVersion, trackID)
	return err
}

// NextRematch returns up to limit selected songs of the job in review
// that older matching rules decided.
func (s *Store) NextRematch(ctx context.Context, jobID int64, limit int) ([]Track, error) {
	return s.tracks(ctx, `SELECT `+trackCols+` FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id
		WHERE t.job_id = ? AND t.state = ? AND t.match_ver < ? AND `+wantedSQL+` ORDER BY t.id LIMIT ?`,
		jobID, TrackReview, MatcherVersion, limit)
}

// SetRematch records the current rules' decision for a song in review —
// only while it still is (the admin's choice or skip meanwhile wins):
// matched, it joins its album's group (the queue fetches it); else it
// stays in review with its new reason. matched: it was matched.
func (s *Store) SetRematch(ctx context.Context, trackID int64, d Decision) (matched bool, err error) {
	err = s.InTx(ctx, func(tx *sql.Tx) error {
		var jobID int64
		err := tx.QueryRowContext(ctx, `SELECT job_id FROM import_tracks WHERE id = ? AND state = ? AND match_ver < ?`,
			trackID, TrackReview, MatcherVersion).Scan(&jobID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // settled meanwhile
		}
		if err != nil {
			return err
		}
		if d.State != TrackMatched {
			_, err := tx.ExecContext(ctx, `UPDATE import_tracks SET match_ver = ?, reason = CASE WHEN ? != '' THEN ? ELSE reason END
				WHERE id = ? AND state = ?`, MatcherVersion, d.Reason, d.Reason, trackID, TrackReview)
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE import_tracks SET state = ?, reason = '', mb_recording = ?, mb_release_group = ?,
			mb_title = ?, mb_artist = ?, mb_album = ?, mb_year = ?, match_ver = ? WHERE id = ? AND state = ?`,
			TrackMatched, d.RecordingID, d.ReleaseGroup, d.Title, d.Artist, d.Album, d.Year, MatcherVersion, trackID,
			TrackReview); err != nil {
			return err
		}
		matched = true
		return assignGroups(ctx, tx, jobID, s.now())
	})
	return matched, err
}

// Pending counts the selected songs still to match.
func (s *Store) Pending(ctx context.Context, jobID int64) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id
		WHERE t.job_id = ? AND t.state = 'pending' AND `+wantedSQL, jobID).Scan(&n)
	return n, err
}

// Unfinished reports whether a downloading job still has work: a group
// queued, downloading or due for its automatic retry, or a selected song
// matched, queued or downloading. Songs in review do not keep it open.
func (s *Store) Unfinished(ctx context.Context, jobID int64) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM import_groups WHERE job_id = ?1 AND (state IN ('waiting', 'searching', 'waiting_space', 'waiting_download', 'downloading')
			OR state = 'failed' AND attempts <= ?2)) +
		(SELECT COUNT(*) FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id
			WHERE t.job_id = ?1 AND t.state IN ('matched', 'queued', 'downloading') AND `+wantedSQL+`)`, jobID, AutoRetries).Scan(&n)
	return n > 0, err
}

// setBuilt records the Vault playlist an import playlist was rebuilt
// into, by which job and with what fingerprint.
func (s *Store) setBuilt(ctx context.Context, importPlaylistID, playlistID, jobID int64, hash string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE import_playlists SET playlist_id = ?, built_job = ?, built_hash = ? WHERE id = ?`,
		playlistID, jobID, hash, importPlaylistID)
	return err
}

// setAlbumItem links a saved album of a job to its library album.
func (s *Store) setAlbumItem(ctx context.Context, jobID int64, key string, itemID int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE import_albums SET item_id = ? WHERE job_id = ? AND key = ?`, itemID, jobID, key)
	return err
}

// SetReport changes a job's report (report_json) in one transaction.
func (s *Store) SetReport(ctx context.Context, jobID int64, edit func(r *Report)) error {
	return s.InTx(ctx, func(tx *sql.Tx) error {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT report_json FROM import_jobs WHERE id = ?`, jobID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var r Report
		_ = json.Unmarshal([]byte(raw), &r) // a broken report starts empty
		edit(&r)
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE import_jobs SET report_json = ? WHERE id = ?`, string(b), jobID)
		return err
	})
}

// PendingEntry is a song of an imported playlist that has not arrived:
// Position is its place in the service's playlist (0-based), State the
// song's state (its album's searching, waiting_space or waiting_download
// while it waits; WaitingFor names that download).
type PendingEntry struct {
	Position int    `json:"position"`
	Artist   string `json:"artist"`
	Title    string `json:"title"`
	State    string `json:"state"`
	// WaitingFor: the download its album waits for (State waiting_download).
	WaitingFor string `json:"waitingFor,omitempty"`
}

// PlaylistPending returns the songs of one of the profile's Vault
// playlists that its newest import has not brought yet (ErrNotFound: not
// the profile's playlist). A playlist never imported, unticked in that
// import, or whose import is not under way or done has none.
func (s *Store) PlaylistPending(ctx context.Context, userID, playlistID int64) ([]PendingEntry, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlists WHERE id = ? AND user_id = ?`, playlistID, userID).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT e.position, t.artist, t.title,
			CASE WHEN t.state IN ('matched', 'queued') AND g.state IN ('searching', 'waiting_space', 'waiting_download') THEN g.state ELSE t.state END,
			CASE WHEN t.state IN ('matched', 'queued') AND g.state = 'waiting_download' THEN g.wait_title ELSE '' END
		FROM import_playlists p JOIN import_jobs j ON j.id = p.job_id
		JOIN import_playlist_entries e ON e.import_playlist_id = p.id
		JOIN import_tracks t ON t.id = e.track_id LEFT JOIN import_groups g ON g.id = t.group_id
		WHERE p.id = (SELECT id FROM import_playlists WHERE playlist_id = ?1 AND user_id = ?2 ORDER BY job_id DESC LIMIT 1)
			AND p.selected = 1 AND j.status IN ('matching', 'downloading', 'done') AND t.item_id IS NULL
			AND NOT (`+unresolvedSQL+` AND COALESCE(`+prevItemSQL+`, 0) IN (SELECT item_id FROM playlist_items WHERE playlist_id = ?1))
		ORDER BY e.position`, playlistID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingEntry{}
	for rows.Next() {
		var p PendingEntry
		if err := rows.Scan(&p.Position, &p.Artist, &p.Title, &p.State, &p.WaitingFor); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
