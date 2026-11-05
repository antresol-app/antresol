package playlistimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/music"
	"github.com/antresol-app/antresol/internal/release"
)

// Rebuild writes what a job brought into the profile's music: every
// ticked playlist becomes (or updates) a Vault playlist of the songs that
// arrived, in the service's order; liked songs are starred with the
// service's date when «Избранное» is on, and so are the saved albums the
// library has (the others are counted in the report). It is idempotent:
// it runs after every album and once more when the job ends. A job that
// is not downloading or done (cancelled meanwhile) is left as it is.
//
// Each song and album is starred once per job: an unstar made during the
// import stays. A playlist the user changed or deleted after this job
// built it is the user's for the rest of the job; the next job of the
// service (a re-run) updates the same Vault playlist again — its name
// follows the service, its songs the service's order — or recreates it.
func Rebuild(ctx context.Context, st *Store, m *music.Store, jobID int64) error {
	j, err := st.Job(ctx, jobID)
	if err != nil || !rebuildable(j.Status) {
		return err
	}
	live := func() (bool, error) {
		j, err := st.Job(ctx, jobID)
		if err != nil {
			return false, err
		}
		return rebuildable(j.Status), nil
	}
	pls, err := st.rebuildPlaylists(ctx, jobID)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range pls {
		if ok, err := live(); !ok || err != nil {
			return errors.Join(append(errs, err)...)
		}
		// one playlist that fails does not hold up the others
		if err := rebuildPlaylist(ctx, st, m, j, p); err != nil {
			errs = append(errs, fmt.Errorf("playlist %d: %w", p.id, err))
		}
	}
	if err := linkAlbums(ctx, st, j); err != nil {
		errs = append(errs, err)
	}
	if j.Favorites {
		if err := applyStars(ctx, st, m, j); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func rebuildable(status string) bool { return status == StatusDownloading || status == StatusDone }

type rebuildPlaylistRow struct {
	id, playlist int64 // playlist 0: none (never built, or deleted)
	name         string
	builtJob     int64  // the job whose rebuild last wrote it (0: none)
	builtHash    string // the fingerprint it wrote
}

// fingerprint identifies a playlist's name and items as a rebuild wrote
// them.
func fingerprint(name string, ids []int64) string {
	h := sha256.New()
	h.Write([]byte(name))
	for _, id := range ids {
		h.Write([]byte("\n" + strconv.FormatInt(id, 10)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// errTakenOver: the user changed the playlist while the rebuild worked.
var errTakenOver = errors.New("musicimport: playlist changed by the user")

// rebuildPlaylist writes an import playlist into its Vault playlist. The
// first rebuild of a job applies the service's name and order (creating
// the playlist when there is none); later rebuilds of the same job add
// what arrived only while the playlist is still as the last one wrote it.
func rebuildPlaylist(ctx context.Context, st *Store, m *music.Store, j Job, p rebuildPlaylistRow) error {
	entries, err := st.playlistEntries(ctx, p.id)
	if err != nil {
		return err
	}
	var cur music.Playlist
	var curIDs []int64
	curErr := music.ErrNotFound
	if p.playlist != 0 {
		cur, curIDs, curErr = m.Playlist(ctx, j.UserID, p.playlist)
		if curErr != nil && !errors.Is(curErr, music.ErrNotFound) {
			return curErr
		}
	}
	ids := keptItems(entries, curIDs)
	want := fingerprint(p.name, ids)
	if p.builtJob == j.ID {
		switch {
		case p.playlist == 0:
			return nil // deleted by the user during this job
		case curErr != nil:
			return nil
		case fingerprint(cur.Name, curIDs) != p.builtHash:
			return nil // changed by the user: theirs until the next job
		case p.builtHash == want:
			return nil // nothing new: no write, the playlist keeps its place
		}
		name := p.name
		err = m.EditPlaylist(ctx, j.UserID, p.playlist, &name, func(now []int64) ([]int64, error) {
			if !slices.Equal(now, curIDs) {
				return nil, errTakenOver
			}
			return ids, nil
		})
		if errors.Is(err, errTakenOver) || errors.Is(err, music.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return st.setBuilt(ctx, p.id, p.playlist, j.ID, want)
	}
	// the job's first build: the service's name and order
	if curErr == nil {
		if cur.Name == p.name && slices.Equal(curIDs, ids) {
			return st.setBuilt(ctx, p.id, p.playlist, j.ID, want)
		}
		name := p.name
		err = m.EditPlaylist(ctx, j.UserID, p.playlist, &name, func(now []int64) ([]int64, error) {
			if !slices.Equal(now, curIDs) {
				return nil, errTakenOver // changed meanwhile: built from it on the next run
			}
			return ids, nil
		})
		switch {
		case err == nil:
			return st.setBuilt(ctx, p.id, p.playlist, j.ID, want)
		case errors.Is(err, errTakenOver):
			return nil
		case !errors.Is(err, music.ErrNotFound):
			return err
		}
	}
	created, err := m.CreatePlaylist(ctx, j.UserID, p.name, ids)
	if err != nil {
		return err
	}
	return st.setBuilt(ctx, p.id, created.ID, j.ID, want)
}

// playlistEntry is a song of an import playlist: item is the library item
// it arrived as (0: none yet); prev, for a song not yet resolved (neither
// linked nor skipped or not found), the item an earlier job of the
// service linked it to (0: none).
type playlistEntry struct{ item, prev int64 }

// keptItems is what a rebuild writes: the songs that arrived, in the
// service's order, and in their places the earlier entries of songs not
// yet resolved that the playlist still holds (cur) — a re-run never
// shrinks the playlist while it works, nor when it is cancelled.
func keptItems(entries []playlistEntry, cur []int64) []int64 {
	left := map[int64]int{}
	for _, id := range cur {
		left[id]++
	}
	out := []int64{}
	for _, e := range entries {
		switch {
		case e.item != 0:
			out = append(out, e.item)
		case e.prev != 0 && left[e.prev] > 0:
			left[e.prev]--
			out = append(out, e.prev)
		}
	}
	return out
}

// linkAlbums links the job's saved albums to the library (by the release
// group a song of the job matched, else by artist and title) and reports
// those it has not.
func linkAlbums(ctx context.Context, st *Store, j Job) error {
	albums, err := st.savedAlbums(ctx, j.ID)
	if err != nil {
		return err
	}
	if len(albums) == 0 {
		return nil
	}
	groups, err := st.matchedAlbums(ctx, j.ID)
	if err != nil {
		return err
	}
	lib := library.NewRepo(st.DB)
	missing := 0
	for _, a := range albums {
		if a.item != 0 {
			continue
		}
		item, err := findSavedAlbum(ctx, lib, groups, a)
		if err != nil {
			return err
		}
		if item == 0 {
			missing++
			continue
		}
		if err := st.setAlbumItem(ctx, j.ID, a.key, item); err != nil {
			return err
		}
	}
	return st.SetReport(ctx, j.ID, func(r *Report) { r.SavedAlbumsMissing = missing })
}

// applyStars stars the job's liked songs that arrived and its saved
// albums the library has, each once: what it stars is marked in the same
// transaction (with the job's status checked there), so a star the user
// removes later is never given back by this job.
func applyStars(ctx context.Context, st *Store, m *music.Store, j Job) error {
	return st.InTx(ctx, func(tx *sql.Tx) error {
		switch status, err := jobStatus(ctx, tx, j.ID); {
		case err != nil:
			return err
		case !rebuildable(status):
			return nil
		}
		for _, q := range []struct{ pick, mark string }{
			{`SELECT item_id, added_at FROM import_tracks WHERE job_id = ? AND liked = 1 AND item_id IS NOT NULL AND starred = 0 ORDER BY id`,
				`UPDATE import_tracks SET starred = 1 WHERE job_id = ? AND liked = 1 AND item_id IS NOT NULL AND starred = 0`},
			{`SELECT item_id, added_at FROM import_albums WHERE job_id = ? AND item_id IS NOT NULL AND starred = 0 ORDER BY rowid`,
				`UPDATE import_albums SET starred = 1 WHERE job_id = ? AND item_id IS NOT NULL AND starred = 0`},
		} {
			stars, err := starsToApply(ctx, tx, q.pick, j.ID)
			if err != nil {
				return err
			}
			for _, s := range stars {
				if err := m.StarAtIn(ctx, tx, j.UserID, s.item, s.at); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, q.mark, j.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

type starItem struct {
	item int64
	at   time.Time // zero: unknown (now)
}

func starsToApply(ctx context.Context, tx *sql.Tx, q string, jobID int64) ([]starItem, error) {
	rows, err := tx.QueryContext(ctx, q, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []starItem
	for rows.Next() {
		var s starItem
		var at sql.NullString
		if err := rows.Scan(&s.item, &at); err != nil {
			return nil, err
		}
		if t := parseTime(at); t != nil {
			s.at = *t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// albumKey compares album names across the service, MusicBrainz and the
// library (editions such as «(Remastered)» do not count).
func albumKey(artist, title string) string {
	return release.Normalize(artist) + "|" + release.Normalize(release.StripEditions(title))
}

func findSavedAlbum(ctx context.Context, lib *library.Repo, groups map[string][]string, a savedAlbum) (int64, error) {
	for _, rg := range groups[albumKey(a.artist, a.title)] {
		it, ok, err := lib.FindAlbumByMBID(ctx, rg)
		if err != nil {
			return 0, err
		}
		if ok {
			return it.ID, nil
		}
	}
	it, ok, err := lib.FindAlbum(ctx, a.artist, a.title)
	if err != nil || !ok {
		return 0, err
	}
	return it.ID, nil
}

// rebuildPlaylists returns the job's ticked playlists.
func (s *Store) rebuildPlaylists(ctx context.Context, jobID int64) ([]rebuildPlaylistRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, COALESCE(playlist_id, 0), name, COALESCE(built_job, 0), built_hash FROM import_playlists
		WHERE job_id = ? AND selected = 1 ORDER BY id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rebuildPlaylistRow
	for rows.Next() {
		var p rebuildPlaylistRow
		if err := rows.Scan(&p.id, &p.playlist, &p.name, &p.builtJob, &p.builtHash); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// unresolvedSQL: the song t has not arrived and may still (not skipped,
// not found).
const unresolvedSQL = `t.item_id IS NULL AND t.state NOT IN ('skipped', 'not_found')`

// prevItemSQL is the library item an earlier job of t's profile and
// service linked the same song (t.key) to; j is t's job.
const prevItemSQL = `(SELECT t2.item_id FROM import_tracks t2 JOIN import_jobs j2 ON j2.id = t2.job_id
	WHERE j2.user_id = j.user_id AND j2.service = j.service AND t2.key = t.key AND t2.job_id != t.job_id
	AND t2.item_id IS NOT NULL ORDER BY t2.job_id DESC LIMIT 1)`

// playlistEntries returns an import playlist's songs in the service's
// order (a song listed twice stays twice).
func (s *Store) playlistEntries(ctx context.Context, importPlaylistID int64) ([]playlistEntry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT COALESCE(t.item_id, 0), CASE WHEN `+unresolvedSQL+` THEN COALESCE(`+prevItemSQL+`, 0) ELSE 0 END
		FROM import_playlist_entries e JOIN import_tracks t ON t.id = e.track_id JOIN import_jobs j ON j.id = t.job_id
		WHERE e.import_playlist_id = ? ORDER BY e.position`, importPlaylistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []playlistEntry{}
	for rows.Next() {
		var e playlistEntry
		if err := rows.Scan(&e.item, &e.prev); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type savedAlbum struct {
	key, artist, title string
	item               int64 // 0: not linked yet
	at                 time.Time
}

func (s *Store) savedAlbums(ctx context.Context, jobID int64) ([]savedAlbum, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT key, artist, title, COALESCE(item_id, 0), added_at FROM import_albums
		WHERE job_id = ? ORDER BY rowid`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []savedAlbum
	for rows.Next() {
		var a savedAlbum
		var at sql.NullString
		if err := rows.Scan(&a.key, &a.artist, &a.title, &a.item, &at); err != nil {
			return nil, err
		}
		if t := parseTime(at); t != nil {
			a.at = *t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// matchedAlbums maps an album (albumKey of the service's and of
// MusicBrainz's names) to the release groups the job's songs matched.
func (s *Store) matchedAlbums(ctx context.Context, jobID int64) (map[string][]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT artist, album, album_artist, mb_artist, mb_album, mb_release_group
		FROM import_tracks WHERE job_id = ? AND mb_release_group != ''`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	add := func(k, rg string) {
		for _, v := range out[k] {
			if v == rg {
				return
			}
		}
		out[k] = append(out[k], rg)
	}
	for rows.Next() {
		var artist, album, albumArtist, mbArtist, mbAlbum, rg string
		if err := rows.Scan(&artist, &album, &albumArtist, &mbArtist, &mbAlbum, &rg); err != nil {
			return nil, err
		}
		for _, a := range []string{artist, albumArtist, mbArtist} {
			for _, t := range []string{album, mbAlbum} {
				if a != "" && t != "" {
					add(albumKey(a, t), rg)
				}
			}
		}
	}
	return out, rows.Err()
}
