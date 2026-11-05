// Package loudness measures the loudness of the library's music for
// «Выравнивание громкости» (ReplayGain): a track without a ReplayGain tag
// is read once with ffmpeg's ebur128 filter, in the background at the
// lowest priority and only while nothing plays; an album's gain comes from
// the loudness of all of its tracks (tags or measured).
package loudness

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

const (
	// runLimit: one track's ffmpeg is killed after this much RUNNING time
	// (held stopped for playback does not count); a CUE image track of an
	// hour decodes in well under a minute.
	runLimit   = 20 * time.Minute
	yieldEvery = 250 * time.Millisecond
	retryBase  = time.Hour
	retryMax   = 7 * 24 * time.Hour
	// minWeightMs: a track of unknown length still counts in its album
	minWeightMs = 1000
	// maxFails: a track that failed this many times is given up on
	// (loud_state 2, unmeasurable): its album's gain comes from the rest
	maxFails = 3
	// startDelay: the first pass waits this long after the server starts
	// (the boot's own scans and the first plays come first)
	startDelay = 5 * time.Minute
	// tagPause: the re-read of tags rests this long between album folders
	tagPause = 200 * time.Millisecond
)

// Result is one measurement: the integrated loudness (nil: silence, the
// gate found nothing to measure) and the largest sample (linear, 1 = full
// scale; 0 for silence).
type Result struct {
	LUFS *float64
	Peak float64
}

// Worker measures the tracks that have no ReplayGain track tag, one at a
// time, the newest first. Each waits until playback has been quiet (Busy)
// and its ffmpeg is held stopped while playback reads the disk
// (media.RunYieldingLimit), at the lowest CPU and I/O priority. A track
// whose measurement fails is retried after an hour, doubling to a week,
// and given up on after maxFails (its album's gain then comes from the
// rest).
//
// A track from before ReplayGain tags were read (rg_scanned 0) first has
// its file's tags read again here, an album folder at a time behind the
// same quiet (ReadTags), not by a forced rescan of the whole library.
type Worker struct {
	DB     *sql.DB
	FFmpeg string
	// Busy: something plays (nil: never).
	Busy func() bool
	Log  *slog.Logger
	Now  func() time.Time
	// Idle: how often a pass runs unprompted (6 h when 0).
	Idle time.Duration
	// Measure overrides the ffmpeg read of a track (tests); endMs nil: to
	// the end of the file.
	Measure func(ctx context.Context, path string, startMs int64, endMs *int64) (Result, error)
	// ReadTags reads a file's ReplayGain tags (nil: library.ReadReplayGain);
	// image: the file is a CUE image (its tags are the whole disc's).
	ReadTags func(path string, image bool) (library.ReplayGain, error)
	// StartDelay: the first pass after Run starts (startDelay when 0).
	StartDelay time.Duration

	once sync.Once
	kick chan struct{}
}

func (w *Worker) init() { w.once.Do(func() { w.kick = make(chan struct{}, 1) }) }

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Worker) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

func (w *Worker) busy() bool { return w.Busy != nil && w.Busy() }

// Kick asks for a pass soon (a scan added tracks); never blocks.
func (w *Worker) Kick() {
	if w == nil {
		return
	}
	w.init()
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// Run passes over the library until ctx is done: StartDelay after the
// start (a Kick before then waits for it too), after each Kick and every
// Idle.
func (w *Worker) Run(ctx context.Context) {
	w.init()
	idle := w.Idle
	if idle <= 0 {
		idle = 6 * time.Hour
	}
	wait := w.StartDelay
	if wait <= 0 {
		wait = startDelay
	}
	first := true
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.kick:
			if first { // the boot's scan: still after the delay
				select {
				case <-ctx.Done():
					t.Stop()
					return
				case <-t.C:
				}
			}
			t.Stop()
		case <-t.C:
		}
		first = false
		wait = idle
		if err := w.Pass(ctx); err != nil && ctx.Err() == nil {
			w.log().Warn("loudness: pass", "err", err)
		}
	}
}

// candidate is a track to measure.
type candidate struct {
	ID, AlbumID int64
	Path        string
	StartMs     int64
	EndMs       *int64
}

func (w *Worker) candidates(ctx context.Context) ([]candidate, error) {
	rows, err := w.DB.QueryContext(ctx, `SELECT t.item_id, COALESCE(i.parent_id,0), f.path, t.start_ms, t.end_ms
		FROM tracks t JOIN items i ON i.id=t.item_id JOIN files f ON f.id=t.file_id
		WHERE t.loud_state=0 AND t.rg_scanned=1 AND t.rg_track_gain IS NULL
		  AND i.missing_since IS NULL AND f.missing_since IS NULL
		  AND (t.loud_retry IS NULL OR t.loud_retry <= ?)
		ORDER BY i.added_at DESC, t.item_id DESC`, w.now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		var end sql.NullInt64
		if err := rows.Scan(&c.ID, &c.AlbumID, &c.Path, &c.StartMs, &end); err != nil {
			return nil, err
		}
		if end.Valid {
			v := end.Int64
			c.EndMs = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Pass works out the gain of every album whose tracks are all known, then
// measures every track still without a loudness, its album's gain again
// after each.
func (w *Worker) Pass(ctx context.Context) error {
	w.init()
	if w.DB == nil {
		return nil
	}
	if err := w.rereadTags(ctx); err != nil {
		return err
	}
	if err := w.RefreshAlbums(ctx, 0); err != nil {
		return err
	}
	list, err := w.candidates(ctx)
	if err != nil {
		return err
	}
	measured := 0
	for _, c := range list {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := os.Stat(c.Path); err != nil {
			continue // not on disk (an unmounted drive): left for later
		}
		if err := w.wait(ctx); err != nil {
			return err
		}
		r, err := w.measure(ctx, c)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, exec.ErrNotFound) {
				w.log().Warn("loudness: no ffmpeg, no measurements", "err", err)
				return nil
			}
			w.log().Warn("loudness: track", "file", c.Path, "start_ms", c.StartMs, "err", err)
			if err := w.failed(ctx, c.ID); err != nil {
				return err
			}
			// given up on at last: the album's gain from the rest
			if err := w.RefreshAlbums(ctx, c.AlbumID); err != nil {
				return err
			}
			continue
		}
		if err := w.store(ctx, c.ID, r); err != nil {
			return err
		}
		measured++
		if err := w.RefreshAlbums(ctx, c.AlbumID); err != nil {
			return err
		}
	}
	if measured > 0 {
		w.log().Info("loudness: tracks measured", "tracks", measured)
	}
	return nil
}

// wait holds until nothing plays.
func (w *Worker) wait(ctx context.Context) error {
	for w.busy() {
		t := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return ctx.Err()
}

func (w *Worker) measure(ctx context.Context, c candidate) (Result, error) {
	if w.Measure != nil {
		return w.Measure(ctx, c.Path, c.StartMs, c.EndMs)
	}
	ff := w.FFmpeg
	if ff == "" {
		ff = "ffmpeg"
	}
	return FFmpegLoudness(ctx, ff, c.Path, c.StartMs, c.EndMs, func(cmd *exec.Cmd) error {
		return media.RunYieldingLimit(cmd, w.busy, yieldEvery, runLimit)
	})
}

func (w *Worker) store(ctx context.Context, id int64, r Result) error {
	var lufs any
	if r.LUFS != nil {
		lufs = round2(*r.LUFS)
	}
	_, err := w.DB.ExecContext(ctx, `UPDATE tracks SET loud_state=1, loud_lufs=?, loud_peak=?, loud_fails=0, loud_retry=NULL WHERE item_id=?`,
		lufs, round6(r.Peak), id)
	return err
}

func (w *Worker) failed(ctx context.Context, id int64) error {
	var fails int
	if err := w.DB.QueryRowContext(ctx, `SELECT loud_fails FROM tracks WHERE item_id=?`, id).Scan(&fails); err != nil {
		return err
	}
	fails++
	if fails >= maxFails {
		_, err := w.DB.ExecContext(ctx, `UPDATE tracks SET loud_state=2, loud_fails=?, loud_retry=NULL WHERE item_id=?`, fails, id)
		return err
	}
	d := retryBase << min(fails-1, 20)
	if d > retryMax || d <= 0 {
		d = retryMax
	}
	_, err := w.DB.ExecContext(ctx, `UPDATE tracks SET loud_fails=?, loud_retry=? WHERE item_id=?`,
		fails, w.now().Add(d).UTC().Format(time.RFC3339), id)
	return err
}

// rereadTags reads the ReplayGain tags of the tracks from before the scanner
// read them (rg_scanned 0), an album folder at a time: each file only while
// nothing plays, a short rest between folders. A file not on disk is left
// for later; one that cannot be read counts as untagged (measured).
func (w *Worker) rereadTags(ctx context.Context) error {
	rows, err := w.DB.QueryContext(ctx, `SELECT t.item_id, COALESCE(i.parent_id,0), f.path, (t.start_ms > 0 OR t.end_ms IS NOT NULL)
		FROM tracks t JOIN items i ON i.id=t.item_id JOIN files f ON f.id=t.file_id
		WHERE t.rg_scanned=0 AND i.missing_since IS NULL AND f.missing_since IS NULL
		ORDER BY i.parent_id, f.path, t.start_ms`)
	if err != nil {
		return err
	}
	type row struct {
		id, album int64
		path      string
		image     bool
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.album, &r.path, &r.image); err != nil {
			rows.Close()
			return err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(list) == 0 {
		return err
	}
	read := w.ReadTags
	if read == nil {
		read = library.ReadReplayGain
	}
	cache := map[string]library.ReplayGain{} // an image's file is read once
	n := 0
	for i, r := range list {
		if i > 0 && list[i-1].album != r.album {
			if err := w.rest(ctx); err != nil {
				return err
			}
		}
		key := r.path
		if r.image {
			key += "|image"
		}
		rg, ok := cache[key]
		if !ok {
			if _, err := os.Stat(r.path); err != nil {
				continue // not on disk: left for later
			}
			if err := w.wait(ctx); err != nil {
				return err
			}
			if rg, err = read(r.path, r.image); err != nil {
				rg = library.ReplayGain{}
			}
			cache[key] = rg
		}
		if _, err := w.DB.ExecContext(ctx, `UPDATE tracks SET rg_track_gain=?, rg_track_peak=?, rg_album_gain=?, rg_album_peak=?, rg_scanned=1
			WHERE item_id=? AND rg_scanned=0`, nullPtr(rg.TrackGain), nullPtr(rg.TrackPeak), nullPtr(rg.AlbumGain), nullPtr(rg.AlbumPeak), r.id); err != nil {
			return err
		}
		n++
	}
	if n > 0 {
		w.log().Info("loudness: ReplayGain tags read", "tracks", n)
	}
	return nil
}

// rest pauses between album folders of the tag re-read.
func (w *Worker) rest(ctx context.Context) error {
	t := time.NewTimer(tagPause)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func nullPtr(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// AlbumTrack is one track of an album as its gain sees it.
type AlbumTrack struct {
	// Known: its loudness is known (a track gain tag, or measured), or it
	// was given up on (LUFS and Peak nil: it counts for nothing)
	Known bool
	// LUFS: its integrated loudness (nil: silence)
	LUFS *float64
	// Peak: its largest sample (nil: unknown)
	Peak       *float64
	DurationMs int64
}

// AlbumGain is the ReplayGain album gain of tracks (dB on
// library.RGReferenceLUFS) and the album's peak, the largest of its
// tracks' (nil when one's is unknown: a client then never boosts): the
// loudness of the album played through is the length-weighted energy mean
// of its tracks' loudness. ok false while a track's is unknown or all are
// silent.
func AlbumGain(tracks []AlbumTrack) (gain float64, peak *float64, ok bool) {
	var energy, weight float64
	peakKnown := true
	for _, t := range tracks {
		if !t.Known {
			return 0, nil, false
		}
		if t.Peak == nil {
			peakKnown = false
		} else if peak == nil || *t.Peak > *peak {
			p := *t.Peak
			peak = &p
		}
		if t.LUFS == nil {
			continue
		}
		wgt := float64(max(t.DurationMs, minWeightMs))
		energy += wgt * math.Pow(10, *t.LUFS/10)
		weight += wgt
	}
	if weight == 0 {
		return 0, nil, false
	}
	if !peakKnown {
		peak = nil
	}
	return round2(library.RGReferenceLUFS - 10*math.Log10(energy/weight)), peak, true
}

// RefreshAlbums writes loud_album_gain/peak of every track of an album
// (albumID 0: of every album) from its tracks' loudness; NULL while one is
// unknown.
func (w *Worker) RefreshAlbums(ctx context.Context, albumID int64) error {
	q := `SELECT COALESCE(i.parent_id,0), t.rg_track_gain, t.rg_track_peak, t.loud_state, t.loud_lufs, t.loud_peak,
		  MAX(COALESCE(t.end_ms, f.duration_ms, 0) - t.start_ms, 0), t.loud_album_gain, t.loud_album_peak
		FROM tracks t JOIN items i ON i.id=t.item_id JOIN files f ON f.id=t.file_id
		WHERE i.kind='track' AND i.missing_since IS NULL AND i.parent_id IS NOT NULL`
	args := []any{}
	if albumID != 0 {
		q += ` AND i.parent_id=?`
		args = append(args, albumID)
	}
	rows, err := w.DB.QueryContext(ctx, q+` ORDER BY i.parent_id`, args...)
	if err != nil {
		return err
	}
	type album struct {
		tracks       []AlbumTrack
		stale        bool // a stored album value differs from the new one
		gains, peaks []sql.NullFloat64
	}
	albums := map[int64]*album{}
	var order []int64
	for rows.Next() {
		var id int64
		var tg, tp, lufs, lp, ag, ap sql.NullFloat64
		var state int
		var dur int64
		if err := rows.Scan(&id, &tg, &tp, &state, &lufs, &lp, &dur, &ag, &ap); err != nil {
			rows.Close()
			return err
		}
		a := albums[id]
		if a == nil {
			a = &album{}
			albums[id] = a
			order = append(order, id)
		}
		at := AlbumTrack{DurationMs: dur}
		switch {
		case tg.Valid:
			l := library.RGReferenceLUFS - tg.Float64
			at.Known, at.LUFS, at.Peak = true, &l, nullable(tp)
		case state == 1:
			at.Known, at.LUFS, at.Peak = true, nullable(lufs), nullable(lp)
		case state == 2: // given up on: counts for nothing, its peak unknown
			at.Known = true
		}
		a.tracks = append(a.tracks, at)
		a.gains = append(a.gains, ag)
		a.peaks = append(a.peaks, ap)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range order {
		a := albums[id]
		gain, peak, ok := AlbumGain(a.tracks)
		var g, p sql.NullFloat64
		if ok {
			g = sql.NullFloat64{Float64: gain, Valid: true}
			if peak != nil {
				p = sql.NullFloat64{Float64: round6(*peak), Valid: true}
			}
		}
		for i := range a.tracks {
			if a.gains[i] != g || a.peaks[i] != p {
				a.stale = true
				break
			}
		}
		if !a.stale {
			continue
		}
		if _, err := w.DB.ExecContext(ctx, `UPDATE tracks SET loud_album_gain=?, loud_album_peak=?
			WHERE item_id IN (SELECT id FROM items WHERE parent_id=? AND kind='track')`, nullArg(g), nullArg(p), id); err != nil {
			return err
		}
	}
	return nil
}

func nullable(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func nullArg(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return v.Float64
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }
