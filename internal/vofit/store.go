package vofit

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"
)

// Stored is a track's stored fit (external_audio_fit).
type Stored struct {
	Path     string
	FileID   int64
	State    string
	OffsetMs float64
	Tempo    float64
	// Current: made of the track and the video as they are now (a pending
	// row always is). A stale one is fitted again; until then the track is
	// not offered.
	Current bool
	// GaveUp: a failed fit retried no more (maxFails).
	GaveUp bool
}

// Offered: the players list the track — only fitted, for the files as
// they are now: one waiting for its fit, of another cut, unmeasured or
// whose fit failed never plays unverified.
func (f Stored) Offered() bool { return f.Current && f.State == StateOK }

// Applies: the offset and tempo are measured (StateOK) for the files as
// they are now.
func (f Stored) Applies() bool { return f.Current && f.State == StateOK }

// Queue records a track of «Найти озвучку…» about to be placed beside the
// library file fileID as pending: the players leave it out until it is
// fitted against the track in origLang (the title's original language,
// ISO 639-1; "" unknown).
func Queue(ctx context.Context, d *sql.DB, path string, fileID int64, origLang string) error {
	_, err := d.ExecContext(ctx, `INSERT INTO external_audio_fit(path, file_id, state, orig_lang, updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET file_id=excluded.file_id, state=excluded.state, orig_lang=excluded.orig_lang, offset_ms=0, tempo=1, measure_json='',
		  track_size=0, track_mtime=0, file_size=0, file_mtime=0, fails=0, retry_at=NULL, updated_at=excluded.updated_at`,
		path, fileID, StatePending, origLang, time.Now().UTC().Format(time.RFC3339))
	return err
}

// current: a row made for the track and the video as they are now (a
// pending one always is: it waits for the track).
func current(state string, trackSize, trackMtime, fileSize, fileMtime, nowSize, nowMtime int64, path string) bool {
	if state == StatePending {
		return true
	}
	if fileSize != nowSize || fileMtime != nowMtime {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.Size() == trackSize && st.ModTime().UnixNano() == trackMtime
}

// ForFile returns the fits of the tracks «Найти озвучку…» placed beside
// the library file fileID, by track path; a stale one (Current false) is
// fitted again, the track hidden meanwhile.
func ForFile(ctx context.Context, d *sql.DB, fileID int64) (map[string]Stored, error) {
	rows, err := d.QueryContext(ctx, `SELECT x.path, x.state, x.offset_ms, x.tempo, x.track_size, x.track_mtime, x.file_size, x.file_mtime, f.size, f.mtime,
		  x.state=? AND x.retry_at IS NULL
		FROM external_audio_fit x JOIN files f ON f.id=x.file_id WHERE x.file_id=?`, StateFailed, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Stored{}
	for rows.Next() {
		f := Stored{FileID: fileID}
		var ts, tm, fs, fm, ns, nm int64
		if err := rows.Scan(&f.Path, &f.State, &f.OffsetMs, &f.Tempo, &ts, &tm, &fs, &fm, &ns, &nm, &f.GaveUp); err != nil {
			return nil, err
		}
		f.Current = current(f.State, ts, tm, fs, fm, ns, nm, f.Path)
		out[f.Path] = f
	}
	return out, rows.Err()
}

// Fitted returns the tracks fitted (StateOK) by library file, those made
// of the track and the video as they are now.
func Fitted(ctx context.Context, d *sql.DB) (map[int64][]Stored, error) {
	rows, err := d.QueryContext(ctx, `SELECT x.path, x.file_id, x.state, x.offset_ms, x.tempo, x.track_size, x.track_mtime, x.file_size, x.file_mtime, f.size, f.mtime
		FROM external_audio_fit x JOIN files f ON f.id=x.file_id WHERE x.state=? ORDER BY x.path`, StateOK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]Stored{}
	for rows.Next() {
		var f Stored
		var ts, tm, fs, fm, ns, nm int64
		if err := rows.Scan(&f.Path, &f.FileID, &f.State, &f.OffsetMs, &f.Tempo, &ts, &tm, &fs, &fm, &ns, &nm); err != nil {
			return nil, err
		}
		if f.Current = current(f.State, ts, tm, fs, fm, ns, nm, f.Path); f.Current {
			out[f.FileID] = append(out[f.FileID], f)
		}
	}
	return out, rows.Err()
}

// Lookup is the fit of the track at path (false: none; a stale one has
// Current false).
func Lookup(ctx context.Context, d *sql.DB, path string) (Stored, bool, error) {
	var fileID int64
	err := d.QueryRowContext(ctx, `SELECT file_id FROM external_audio_fit WHERE path=?`, path).Scan(&fileID)
	if errors.Is(err, sql.ErrNoRows) {
		return Stored{}, false, nil
	}
	if err != nil {
		return Stored{}, false, err
	}
	m, err := ForFile(ctx, d, fileID)
	if err != nil {
		return Stored{}, false, err
	}
	f, ok := m[path]
	return f, ok, nil
}
