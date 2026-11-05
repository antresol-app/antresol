package playlistimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrNotReview: the song is not waiting for a decision (shown as is).
var ErrNotReview = errors.New("Не найдено")

// AssignGroups gives every matched song of the job without a group the
// group of its release group (one per album and job; artist, album and
// year from the song's match). A finished group that gets a new song goes
// back to waiting: the song still has to be fetched.
func (s *Store) AssignGroups(ctx context.Context, jobID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := assignGroups(ctx, tx, jobID, s.now()); err != nil {
		return err
	}
	return tx.Commit()
}

// ungroupedSQL selects the job's (?1) matched songs that have no group.
const ungroupedSQL = `job_id = ?1 AND state = 'matched' AND group_id IS NULL AND mb_release_group != ''`

func assignGroups(ctx context.Context, tx *sql.Tx, jobID int64, now string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO import_groups(job_id, release_group, artist, album, year, state, updated_at)
		SELECT job_id, mb_release_group, mb_artist, mb_album, mb_year, 'waiting', ?2 FROM import_tracks
		WHERE id IN (SELECT MIN(id) FROM import_tracks WHERE `+ungroupedSQL+` GROUP BY mb_release_group)
		ON CONFLICT(job_id, release_group) DO NOTHING`, jobID, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE import_groups SET state = 'waiting', error = '', updated_at = ?2
		WHERE job_id = ?1 AND state IN ('done', 'have', 'not_found', 'no_format', 'failed')
		AND release_group IN (SELECT mb_release_group FROM import_tracks WHERE `+ungroupedSQL+`)`, jobID, now); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE import_tracks SET group_id = (SELECT g.id FROM import_groups g
		WHERE g.job_id = import_tracks.job_id AND g.release_group = import_tracks.mb_release_group) WHERE `+ungroupedSQL, jobID)
	return err
}

// reviewTrack loads a song in review and its job's status inside tx
// (ErrNotFound; ErrNotReview: not in review, or its job is not running
// or done).
func reviewTrack(ctx context.Context, tx *sql.Tx, trackID int64) (jobID int64, status string, err error) {
	var state string
	err = tx.QueryRowContext(ctx, `SELECT t.job_id, t.state, j.status FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id
		WHERE t.id = ?`, trackID).Scan(&jobID, &state, &status)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, "", ErrNotFound
	case err != nil:
		return 0, "", err
	case state != TrackReview || (status != StatusMatching && status != StatusDownloading && status != StatusDone):
		return 0, "", ErrNotReview
	}
	return jobID, status, nil
}

// Resolve settles a song in review with the album the admin chose
// (d.ReleaseGroup, Album, Artist, Year, Aliases): it is matched and
// grouped at once, and a done job goes back to downloading (ErrBusy when
// another job runs meanwhile).
func (s *Store) Resolve(ctx context.Context, trackID int64, d Decision) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	jobID, status, err := reviewTrack(ctx, tx, trackID)
	if err != nil {
		return err
	}
	now := s.now()
	if status == StatusDone {
		var busy int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_jobs WHERE status IN ('matching', 'downloading')`).Scan(&busy); err != nil {
			return err
		}
		if busy > 0 {
			return ErrBusy
		}
		if err := newerJobOpen(ctx, tx, jobID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE import_jobs SET status = ?, updated_at = ?, finished_at = NULL WHERE id = ? AND status = ?`,
			StatusDownloading, now, jobID, StatusDone); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE import_tracks SET state = ?, reason = '', mb_recording = ?, mb_release_group = ?,
		mb_title = ?, mb_artist = ?, mb_album = ?, mb_year = ? WHERE id = ?`,
		TrackMatched, d.RecordingID, d.ReleaseGroup, d.Title, d.Artist, d.Album, d.Year, trackID); err != nil {
		return err
	}
	aliases := d.Aliases
	if aliases == nil {
		aliases = []string{}
	}
	aj, err := json.Marshal(aliases)
	if err != nil {
		return err
	}
	// the admin's aliases fill a group that has none
	if _, err := tx.ExecContext(ctx, `INSERT INTO import_groups(job_id, release_group, artist, album, year, aliases_json, state, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'waiting', ?)
		ON CONFLICT(job_id, release_group) DO UPDATE SET aliases_json = excluded.aliases_json WHERE aliases_json = '[]'`,
		jobID, d.ReleaseGroup, d.Artist, d.Album, d.Year, string(aj), now); err != nil {
		return err
	}
	if err := assignGroups(ctx, tx, jobID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// Skip leaves a song in review out of the import.
func (s *Store) Skip(ctx context.Context, trackID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, err := reviewTrack(ctx, tx, trackID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE import_tracks SET state = ? WHERE id = ?`, TrackSkipped, trackID); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrNotRetryable: the group is not waiting for «Повторить» (shown as is).
var ErrNotRetryable = errors.New("Не найдено")

// Group is one album of a job to acquire. Songs counts its selected songs.
type Group struct {
	ID, JobID     int64
	ReleaseGroup  string
	Artist, Album string
	Year          int
	Aliases       []string
	AlbumItemID   int64 // the library album its songs are in (0: none yet)
	State         string
	InfoHash      string
	Bytes         int64
	Attempts      int
	Error         string
	UpdatedAt     time.Time
	Songs         int
	// WaitingFor names the download a waiting_download album waits for;
	// WaitDone, WaitAt: its bytes and when they last grew; SkipInfoHash: a
	// download given up on (its own search leaves it out).
	WaitingFor   string
	SkipInfoHash string
	// AnyFormat: «Скачать в другом формате» — its search takes a release
	// of any format.
	AnyFormat bool
	// Dead: its own downloads given up for making no headway (oldest
	// first); its search leaves those releases out. Slow: given up for
	// being too slow; its search takes them only when nothing else holds
	// the songs.
	Dead, Slow []string
	// WaitDone is also a downloading album's bytes when they last grew;
	// Clock its download's running time (Queue.headway).
	WaitDone int64
	WaitAt   time.Time
	Clock    RunClock
}

const groupCols = `g.id, g.job_id, g.release_group, g.artist, g.album, g.year, g.aliases_json, COALESCE(g.album_item_id, 0), g.state,
	g.infohash, g.bytes, g.attempts, g.error, g.updated_at, g.wait_title, g.skip_infohash, g.wait_done, COALESCE(g.wait_at, ''), g.any_format,
	g.dead_json, g.slow_json, g.run_s, g.quiet_s, COALESCE(g.run_base, -1), (SELECT COUNT(*) FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id WHERE t.group_id = g.id AND ` + wantedSQL + `)`

func scanGroup(r scanner) (Group, error) {
	var g Group
	var aliases, updated, WaitAt, dead, slow string
	var runS, quietS int64
	err := r.Scan(&g.ID, &g.JobID, &g.ReleaseGroup, &g.Artist, &g.Album, &g.Year, &aliases, &g.AlbumItemID, &g.State, &g.InfoHash,
		&g.Bytes, &g.Attempts, &g.Error, &updated, &g.WaitingFor, &g.SkipInfoHash, &g.WaitDone, &WaitAt, &g.AnyFormat, &dead, &slow, &runS, &quietS,
		&g.Clock.Base, &g.Songs)
	if err != nil {
		return g, err
	}
	if t := parseTime(sql.NullString{String: WaitAt, Valid: WaitAt != ""}); t != nil {
		g.WaitAt = *t
	}
	_ = json.Unmarshal([]byte(aliases), &g.Aliases) // broken aliases read as none
	_ = json.Unmarshal([]byte(dead), &g.Dead)
	_ = json.Unmarshal([]byte(slow), &g.Slow)
	g.Clock.Run, g.Clock.Quiet, g.Clock.Done = time.Duration(runS)*time.Second, time.Duration(quietS)*time.Second, g.WaitDone
	if t := parseTime(sql.NullString{String: updated, Valid: true}); t != nil {
		g.UpdatedAt = *t
	}
	return g, nil
}

// GroupsWhere returns the albums matching where (ordered by id).
func (s *Store) GroupsWhere(ctx context.Context, where string, args ...any) ([]Group, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+groupCols+` FROM import_groups g WHERE `+where+` ORDER BY g.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Groups returns a job's albums, oldest first.
func (s *Store) Groups(ctx context.Context, jobID int64) ([]Group, error) {
	return s.GroupsWhere(ctx, `g.job_id = ?`, jobID)
}

// Group returns one album (ErrNotFound).
func (s *Store) Group(ctx context.Context, id int64) (Group, error) {
	g, err := scanGroup(s.DB.QueryRowContext(ctx, `SELECT `+groupCols+` FROM import_groups g WHERE g.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	return g, err
}

// GroupTracks returns the selected songs of an album in these states.
func (s *Store) GroupTracks(ctx context.Context, groupID int64, states ...string) ([]Track, error) {
	if len(states) == 0 {
		return nil, nil
	}
	args := []any{groupID}
	for _, st := range states {
		args = append(args, st)
	}
	return s.tracks(ctx, `SELECT `+trackCols+` FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id
		WHERE t.group_id = ? AND t.state IN (?`+strings.Repeat(", ?", len(states)-1)+`) AND `+wantedSQL+` ORDER BY t.id`, args...)
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// InTx runs f in a transaction.
func (s *Store) InTx(ctx context.Context, f func(tx *sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// SetTracks moves the songs ids to state (with item ids from items, and
// reason).
func SetTracks(ctx context.Context, tx *sql.Tx, state, reason string, ids []int64, items map[int64]int64) error {
	for _, id := range ids {
		var item any
		if v, ok := items[id]; ok && v != 0 {
			item = v
		}
		if _, err := tx.ExecContext(ctx, `UPDATE import_tracks SET state = ?, reason = ?, item_id = COALESCE(?, item_id) WHERE id = ?`,
			state, reason, item, id); err != nil {
			return err
		}
	}
	return nil
}

// Settle ends an album in state, or puts it back in the queue when a song
// joined it meanwhile (still matched: a review resolved into it).
func Settle(ctx context.Context, tx *sql.Tx, id int64, state, errText string, at time.Time) error {
	const joined = `EXISTS (SELECT 1 FROM import_tracks t JOIN import_jobs j ON j.id = t.job_id
		WHERE t.group_id = ?1 AND t.state = 'matched' AND ` + wantedSQL + `)`
	_, err := tx.ExecContext(ctx, `UPDATE import_groups SET updated_at = ?3,
		state = CASE WHEN `+joined+` THEN 'waiting' ELSE ?2 END, error = CASE WHEN `+joined+` THEN '' ELSE ?4 END
		WHERE id = ?1`, id, state, stamp(at), errText)
	return err
}

// MarkHave links songs the library has already (track id → library
// track) and remembers the library album; done: the album is complete
// (state have, or waiting again when a song joined it).
func (s *Store) MarkHave(ctx context.Context, groupID int64, have map[int64]int64, album int64, done bool, at time.Time) error {
	return s.InTx(ctx, func(tx *sql.Tx) error {
		ids := make([]int64, 0, len(have))
		for id := range have {
			ids = append(ids, id)
		}
		if err := SetTracks(ctx, tx, TrackHave, "", ids, have); err != nil {
			return err
		}
		if album != 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE import_groups SET album_item_id = ? WHERE id = ?`, album, groupID); err != nil {
				return err
			}
			// the album the import found is the importer's too («Моя
			// медиатека»), as one it downloads is (acquire's organize)
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO item_owners (item_id, user_id)
				SELECT ?, j.user_id FROM import_groups g JOIN import_jobs j ON j.id = g.job_id WHERE g.id = ?`, album, groupID); err != nil {
				return err
			}
		}
		if !done {
			return nil
		}
		return Settle(ctx, tx, groupID, GroupHave, "", at)
	})
}

// RunClock is a downloading album's own download's clock (run_s, quiet_s,
// run_base, wait_done): how long it has run, how long since its bytes
// last grew, its bytes when the clock started (-1: not yet) and then.
type RunClock struct {
	Run, Quiet time.Duration
	Base, Done int64
}

// NotFound ends an album no release holds: its songs ids are not found.
func (s *Store) NotFound(ctx context.Context, groupID int64, ids []int64, msg string, at time.Time) error {
	return s.InTx(ctx, func(tx *sql.Tx) error {
		if err := SetTracks(ctx, tx, TrackNotFound, "", ids, nil); err != nil {
			return err
		}
		return Settle(ctx, tx, groupID, GroupNotFound, msg, at)
	})
}

// RetryGroup is «Повторить» of an album that was not found, failed or
// waits for space: it goes back to the queue with its songs that were not
// found or failed (ErrNotRetryable otherwise). A done job downloads again
// (ErrBusy when another job runs meanwhile).
func (s *Store) RetryGroup(ctx context.Context, id int64) error { return s.retryGroup(ctx, id, false) }

// RetryGroupAnyFormat is «Скачать в другом формате»: RetryGroup whose
// search from now on takes a release of any format.
func (s *Store) RetryGroupAnyFormat(ctx context.Context, id int64) error {
	return s.retryGroup(ctx, id, true)
}

func (s *Store) retryGroup(ctx context.Context, id int64, anyFormat bool) error {
	return s.InTx(ctx, func(tx *sql.Tx) error {
		var jobID int64
		var state, status string
		err := tx.QueryRowContext(ctx, `SELECT g.job_id, g.state, j.status FROM import_groups g JOIN import_jobs j ON j.id = g.job_id
			WHERE g.id = ?`, id).Scan(&jobID, &state, &status)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return err
		case state != GroupNotFound && state != GroupNoFormat && state != GroupFailed && state != GroupWaitingSpace && state != GroupWaitingDownload,
			status != StatusDownloading && status != StatusDone:
			return ErrNotRetryable
		}
		now := s.now()
		if status == StatusDone {
			var busy int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_jobs WHERE status IN ('matching', 'downloading')`).Scan(&busy); err != nil {
				return err
			}
			if busy > 0 {
				return ErrBusy
			}
			if err := newerJobOpen(ctx, tx, jobID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE import_jobs SET status = ?, updated_at = ?, finished_at = NULL WHERE id = ? AND status = ?`,
				StatusDownloading, now, jobID, StatusDone); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE import_tracks SET state = ?, reason = '' WHERE group_id = ? AND state IN (?, ?)`,
			TrackMatched, id, TrackNotFound, TrackFailed); err != nil {
			return err
		}
		// «Повторить» of an album waiting for another download: its own search
		_, err = tx.ExecContext(ctx, `UPDATE import_groups SET state = ?, error = '', updated_at = ?,
			skip_infohash = CASE WHEN state = ? THEN infohash ELSE skip_infohash END, wait_title = '', wait_done = 0, wait_at = NULL,
			any_format = MAX(any_format, ?), dead_json = '[]', slow_json = '[]'
			WHERE id = ?`, GroupWaiting, now, GroupWaitingDownload, anyFormat, id)
		return err
	})
}

// newerJobOpen refuses to reopen a done job (ErrBusy) while another job
// of its profile and service is open: that one owns the service's
// playlists, so the reopened songs would be wanted no more.
func newerJobOpen(ctx context.Context, tx *sql.Tx, jobID int64) error {
	var userID int64
	var service string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, service FROM import_jobs WHERE id = ?`, jobID).Scan(&userID, &service); err != nil {
		return err
	}
	return openJobOf(ctx, tx, userID, service, jobID)
}

// AutoRetries: a group failed this many times is retried automatically.
const AutoRetries = 1
