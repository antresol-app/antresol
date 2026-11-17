package playlistimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"
)

// The download queue's operations on an import's albums, for a queue
// outside the core (an addon with the imports permission, through the
// core API's /imports routes). Each is one transaction. A key names the
// download an album waits for or downloads (import_groups.infohash; a
// skipped one, skip_infohash). The songs an operation names are those of
// its album: an id of another album's song is left alone.

// MsgError is the reason of a song a finished download did not bring
// (and the error of an album whose search went wrong).
const MsgError = "Ошибка"

// clock is the store's time (Now, else the wall clock).
func (s *Store) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Clock is the store's time: what the queue's operations stamp.
func (s *Store) Clock() time.Time { return s.clock() }

// ActiveGroups counts the albums being searched or downloaded for a
// running job. A cancelled or failed job's downloads go on (they are
// library content) but no longer hold the import's slots.
func (s *Store) ActiveGroups(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_groups g JOIN import_jobs j ON j.id = g.job_id
		WHERE g.state IN ('searching', 'downloading') AND j.status = 'downloading'`).Scan(&n)
	return n, err
}

// JobIDs are the ids of the jobs in status, oldest first.
func (s *Store) JobIDs(ctx context.Context, status string) ([]int64, error) {
	return s.ids(ctx, `SELECT id FROM import_jobs WHERE status = ? ORDER BY id`, status)
}

// ReadingJobs are the ids of the profile's jobs still reading, oldest
// first.
func (s *Store) ReadingJobs(ctx context.Context, userID int64) ([]int64, error) {
	return s.ids(ctx, `SELECT id FROM import_jobs WHERE user_id = ? AND status = ? ORDER BY id`, userID, StatusReading)
}

func (s *Store) ids(ctx context.Context, q string, args ...any) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// StillDownloading reports whether the job is still downloading (not
// cancelled meanwhile); ErrNotFound for none.
func (s *Store) StillDownloading(ctx context.Context, jobID int64) (bool, error) {
	j, err := s.Job(ctx, jobID)
	if err != nil {
		return false, err
	}
	return j.Status == StatusDownloading, nil
}

// QueueCandidates returns the job's albums the queue may start, oldest
// first: waiting, waiting for space or for a download, and failed ones
// due for their automatic retry (failed once, at or before retryBefore).
func (s *Store) QueueCandidates(ctx context.Context, jobID int64, retryBefore time.Time) ([]Group, error) {
	return s.GroupsWhere(ctx, `g.job_id = ? AND (g.state IN ('waiting', 'waiting_space', 'waiting_download')
		OR g.state = 'failed' AND g.attempts <= ? AND g.updated_at <= ?)`, jobID, AutoRetries, stamp(retryBefore))
}

// JobGroups returns the job's albums in state ("": all), oldest first.
func (s *Store) JobGroups(ctx context.Context, jobID int64, state string) ([]Group, error) {
	if state == "" {
		return s.Groups(ctx, jobID)
	}
	return s.GroupsWhere(ctx, `g.job_id = ? AND g.state = ?`, jobID, state)
}

// MoveGroup moves an album from one state to another (compare-and-set:
// false when it was not in from — another claimer took it, a cancel
// dropped it, a restart put its search back).
func (s *Store) MoveGroup(ctx context.Context, id int64, from, to string, at time.Time) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE import_groups SET state = ?, updated_at = ? WHERE id = ? AND state = ?`, to, stamp(at), id, from)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// WaitFor records the download key an album waits for (another job's, or
// one the admin started; named name, done bytes so far): the album is
// waiting_download. Its progress clock (wait_at) restarts with another
// download or more bytes. An album the queue is not holding (searching,
// downloading or settled) is left alone.
func (s *Store) WaitFor(ctx context.Context, id int64, key, name string, done int64, at time.Time) error {
	return s.InTx(ctx, func(tx *sql.Tx) error {
		g, err := scanGroup(tx.QueryRowContext(ctx, `SELECT `+groupCols+` FROM import_groups g WHERE g.id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		since := g.WaitAt
		if g.State != GroupWaitingDownload || g.InfoHash != key || done > g.WaitDone || since.IsZero() {
			since = at
		}
		_, err = tx.ExecContext(ctx, `UPDATE import_groups SET state = ?, infohash = ?, wait_title = ?, wait_done = ?, wait_at = ?,
			updated_at = CASE WHEN state = ? THEN updated_at ELSE ? END
			WHERE id = ? AND state IN ('waiting', 'waiting_space', 'waiting_download', 'failed')`,
			GroupWaitingDownload, key, name, max(done, g.WaitDone), stamp(since), GroupWaitingDownload, stamp(at), id)
		return err
	})
}

// GiveUpWait stops a waiting_download album's wait (the download is
// paused, failed, removed or stalled, or it ended): it waits for its own
// search, which leaves key out (skipKey).
func (s *Store) GiveUpWait(ctx context.Context, id int64, key string, at time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE import_groups SET state = ?, skip_infohash = ?, wait_title = '', wait_done = 0, wait_at = NULL,
		updated_at = ? WHERE id = ? AND state = ?`, GroupWaiting, key, stamp(at), id, GroupWaitingDownload)
	return err
}

// StartGroup records a started download (compare-and-set from searching:
// false when the album is no longer searching — cancelled, or a core
// restart put the search back in the queue; the caller removes what it
// began). The album downloads key (bytes); its songs downloading are on
// the way, missing ones are not in the release.
func (s *Store) StartGroup(ctx context.Context, id int64, key string, bytes int64, downloading, missing []int64, at time.Time) (bool, error) {
	moved := false
	err := s.InTx(ctx, func(tx *sql.Tx) error {
		// the progress clock (wait_done, wait_at) starts with the download
		res, err := tx.ExecContext(ctx, `UPDATE import_groups SET state = ?, infohash = ?, bytes = ?, error = '', updated_at = ?,
			wait_title = '', wait_done = 0, wait_at = ?, run_s = 0, quiet_s = 0, run_base = 0 WHERE id = ? AND state = ?`,
			GroupDownloading, key, bytes, stamp(at), stamp(at), id, GroupSearching)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		moved = true
		if err := setGroupTracks(ctx, tx, id, TrackDownloading, "", downloading, nil); err != nil {
			return err
		}
		return setGroupTracks(ctx, tx, id, TrackNotFound, "", missing, nil)
	})
	return moved, err
}

// SaveClock stores the clock of the album's download key (only while it
// downloads that one).
func (s *Store) SaveClock(ctx context.Context, id int64, key string, c RunClock) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE import_groups SET run_s = ?, quiet_s = ?, run_base = ?, wait_done = ?
		WHERE id = ? AND state = ? AND infohash = ?`,
		int64(c.Run/time.Second), int64(c.Quiet/time.Second), c.Base, c.Done, id, GroupDownloading, key)
	return err
}

// MarkGivenUp lists the album's download key as given up — dead
// (dead_json: never searched again) or slow (slow_json: only when nothing
// else holds the songs) — before it is removed, so that a removal whose
// requeue fails is not taken for an admin's removal later. Each key is
// listed once.
func (s *Store) MarkGivenUp(ctx context.Context, id int64, key string, slow bool) error {
	col := "dead_json"
	if slow {
		col = "slow_json"
	}
	return s.InTx(ctx, func(tx *sql.Tx) error {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT `+col+` FROM import_groups WHERE id = ? AND state = ? AND infohash = ?`,
			id, GroupDownloading, key).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var list []string
		_ = json.Unmarshal([]byte(raw), &list)
		if slices.Contains(list, key) {
			return nil
		}
		b, err := json.Marshal(append(list, key))
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE import_groups SET `+col+` = ? WHERE id = ?`, string(b), id)
		return err
	})
}

// Requeue puts a downloading album back in the queue: its download key
// went (given up, or replaced by another download of the album). Its
// songs on the way and those the release lacked are matched again. When
// key is the maxDead-th dead download it fails instead with msg (only
// «Повторить» is left, which forgets them). An album no longer
// downloading key is left alone.
func (s *Store) Requeue(ctx context.Context, id int64, key, msg string, maxDead int, at time.Time) error {
	return s.InTx(ctx, func(tx *sql.Tx) error {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT dead_json FROM import_groups WHERE id = ? AND state = ? AND infohash = ?`,
			id, GroupDownloading, key).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // it ended or went meanwhile
		}
		if err != nil {
			return err
		}
		var dead []string
		_ = json.Unmarshal([]byte(raw), &dead)
		if _, err := tx.ExecContext(ctx, `UPDATE import_groups SET state = ?, infohash = '', bytes = 0, wait_title = '',
			wait_done = 0, wait_at = NULL, run_s = 0, quiet_s = 0, run_base = NULL, updated_at = ? WHERE id = ?`,
			GroupWaiting, stamp(at), id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE import_tracks SET state = ?, reason = '' WHERE group_id = ? AND state IN (?, ?)`,
			TrackMatched, id, TrackDownloading, TrackNotFound); err != nil {
			return err
		}
		if !slices.Contains(dead, key) || len(dead) < maxDead {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE import_tracks SET state = ? WHERE group_id = ? AND state = ?`,
			TrackFailed, id, TrackMatched); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE import_groups SET state = ?, error = ?, attempts = MAX(attempts, ?) WHERE id = ?`,
			GroupFailed, msg, AutoRetries+1, id)
		return err
	})
}

// NoFormat settles an album whose releases are all of another format: its
// songs ids are not found until «Скачать в другом формате».
func (s *Store) NoFormat(ctx context.Context, id int64, ids []int64, msg string, at time.Time) error {
	return s.InTx(ctx, func(tx *sql.Tx) error {
		if err := setGroupTracks(ctx, tx, id, TrackNotFound, "", ids, nil); err != nil {
			return err
		}
		return Settle(ctx, tx, id, GroupNoFormat, msg, at)
	})
}

// FailGroup ends an album with an error: its songs ids and any that
// joined it fail with it. retry: a failed search, retried automatically
// once (attempts + 1); else (a download that went wrong) only «Повторить»
// is left.
func (s *Store) FailGroup(ctx context.Context, id int64, ids []int64, msg string, retry bool, at time.Time) error {
	return s.InTx(ctx, func(tx *sql.Tx) error {
		if err := setGroupTracks(ctx, tx, id, TrackFailed, "", ids, nil); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE import_tracks SET state = ? WHERE group_id = ? AND state = ?`,
			TrackFailed, id, TrackMatched); err != nil {
			return err
		}
		attempts := `attempts + 1`
		if !retry {
			attempts = `MAX(attempts, ` + strconv.Itoa(AutoRetries+1) + `)`
		}
		_, err := tx.ExecContext(ctx, `UPDATE import_groups SET state = ?, error = ?, attempts = `+attempts+`, updated_at = ? WHERE id = ?`,
			GroupFailed, msg, stamp(at), id)
		return err
	})
}

// FinishGroup ends a download that was placed in the library: the songs
// done are linked to their library tracks (song id → library track), the
// songs failed were not among the placed files (reason MsgError); album
// (0: unknown) is the library album.
func (s *Store) FinishGroup(ctx context.Context, id int64, done map[int64]int64, failed []int64, album int64, at time.Time) error {
	return s.InTx(ctx, func(tx *sql.Tx) error {
		ids := make([]int64, 0, len(done))
		for t := range done {
			ids = append(ids, t)
		}
		slices.Sort(ids)
		if err := setGroupTracks(ctx, tx, id, TrackDone, "", ids, done); err != nil {
			return err
		}
		if err := setGroupTracks(ctx, tx, id, TrackFailed, MsgError, failed, nil); err != nil {
			return err
		}
		if album != 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE import_groups SET album_item_id = ? WHERE id = ?`, album, id); err != nil {
				return err
			}
		}
		return Settle(ctx, tx, id, GroupDone, "", at)
	})
}

// setGroupTracks is SetTracks for the songs of the album groupID only.
func setGroupTracks(ctx context.Context, tx *sql.Tx, groupID int64, state, reason string, ids []int64, items map[int64]int64) error {
	for _, id := range ids {
		var item any
		if v, ok := items[id]; ok && v != 0 {
			item = v
		}
		if _, err := tx.ExecContext(ctx, `UPDATE import_tracks SET state = ?, reason = ?, item_id = COALESCE(?, item_id)
			WHERE id = ? AND group_id = ?`, state, reason, item, id, groupID); err != nil {
			return err
		}
	}
	return nil
}

// QueueGroupStates are the states the queue moves an album between
// (MoveGroup's from and to).
var QueueGroupStates = []string{GroupWaiting, GroupSearching, GroupWaitingSpace, GroupWaitingDownload, GroupFailed}

// ValidMove: the queue may move an album from one of its states to
// waiting, searching or waiting_space.
func ValidMove(from, to string) bool {
	return from != to && slices.Contains(QueueGroupStates, from) &&
		(to == GroupWaiting || to == GroupSearching || to == GroupWaitingSpace)
}

// ValidStatus: status is a job status.
func ValidStatus(status string) bool {
	switch status {
	case StatusReading, StatusPreview, StatusMatching, StatusDownloading, StatusDone, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// ValidTrackState: state is a song state.
func ValidTrackState(state string) bool {
	switch state {
	case TrackPending, TrackMatched, TrackReview, TrackSkipped, TrackHave, TrackQueued, TrackDownloading, TrackDone, TrackNotFound,
		TrackFailed:
		return true
	}
	return false
}

// ValidGroupState: state is an album state.
func ValidGroupState(state string) bool {
	switch state {
	case GroupWaiting, GroupSearching, GroupWaitingSpace, GroupWaitingDownload, GroupDownloading, GroupDone, GroupHave, GroupNotFound,
		GroupNoFormat, GroupFailed:
		return true
	}
	return false
}

// markHaveFrom is MarkHave of an album still in state from
// (compare-and-set, checked first in the transaction: another claimer's
// move meanwhile wins and nothing is linked). ok: it was.
func (s *Store) markHaveFrom(ctx context.Context, groupID int64, from string, have map[int64]int64, album int64, done bool, at time.Time) (bool, error) {
	ok := false
	err := s.InTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE import_groups SET updated_at = updated_at WHERE id = ? AND state = ?`, groupID, from)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		ok = true
		return markHave(ctx, tx, groupID, have, album, done, at)
	})
	return ok, err
}
