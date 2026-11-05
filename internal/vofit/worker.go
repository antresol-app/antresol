package vofit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/subtitles"
)

const (
	retryBase = time.Hour
	retryMax  = 7 * 24 * time.Hour
	// maxFails: a track that failed this many times is left unfitted.
	maxFails = 5
	// pendingKeep: a pending row whose track never appeared is dropped
	// after this (the placement failed).
	pendingKeep = time.Hour
	startDelay  = 3 * time.Minute
	// soonRetry: a pass that left work (playback held the backlog, a
	// track not placed yet) runs again this soon.
	soonRetry = time.Minute
)

// Worker fits the tracks «Найти озвучку…» placed beside their videos (the
// rows Queue made; a release's own tracks, stage A, are never measured):
// the pending ones even while something plays (ffmpeg at the lowest
// priority), the failed ones due again once nothing plays. It runs in the
// sources build only (the core build has no stage B).
type Worker struct {
	DB     *sql.DB
	FFmpeg string
	// Busy: playback needs the media disk (nil: never).
	Busy func() bool
	Log  *slog.Logger
	Now  func() time.Time
	// Idle: how often a pass runs unprompted (6 h when 0); StartDelay: the
	// first pass after Run (startDelay when 0).
	Idle, StartDelay time.Duration
	// Fit overrides the measurement (tests).
	Fit func(ctx context.Context, in Input) (Result, error)
	// OrigLang (optional) looks up the original language (ISO 639-1) of
	// the title of a library file, for a row stored without one; "" when
	// still unknown.
	OrigLang func(ctx context.Context, fileID int64) string
	// Fitted (optional) hears of a pass that fitted a track (the cleaned
	// voice-overs are made of fitted ones).
	Fitted func()
	fitted bool

	once sync.Once
	kick chan struct{}
	// again: the last pass left work for soon (soonRetry)
	again bool
}

func (w *Worker) init() {
	w.once.Do(func() { w.kick = make(chan struct{}, 1) })
}

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

// Kick asks for a pass soon (tracks were placed, a scan added files);
// never blocks.
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

// Run passes until ctx is done: StartDelay after the start, after each
// Kick, every Idle (soonRetry while work waits).
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
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.kick:
			t.Stop()
		case <-t.C:
		}
		if err := w.Pass(ctx); err != nil && ctx.Err() == nil {
			w.log().Warn("vofit: pass", "err", err)
		}
		wait = idle
		if w.again {
			wait = soonRetry
		}
	}
}

// job is a track to fit.
type job struct {
	path       string // the track
	fileID     int64
	video      string
	size       int64
	mtime      int64
	durationMs int64
	probe      string
	lang       string // the title's original language ("" unknown)
	pending    bool
}

// Pass fits the pending tracks, then (nothing playing) the due ones, a
// track at a time.
func (w *Worker) Pass(ctx context.Context) error {
	w.init()
	w.again, w.fitted = false, false
	if w.DB == nil {
		return nil
	}
	defer func() {
		if w.fitted && w.Fitted != nil {
			w.Fitted()
		}
	}()
	if err := w.collect(ctx); err != nil {
		return err
	}
	jobs, err := w.due(ctx, true)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if err := w.run(ctx, j); err != nil {
			return err
		}
	}
	if w.busy() {
		w.again = true
		return nil
	}
	jobs, err = w.due(ctx, false)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if w.busy() {
			w.again = true
			return nil
		}
		if err := w.run(ctx, j); err != nil {
			return err
		}
	}
	return nil
}

// collect drops the rows of tracks gone (a pending one once pendingKeep
// passed without its track).
func (w *Worker) collect(ctx context.Context) error {
	rows, err := w.DB.QueryContext(ctx, `SELECT path, state, updated_at FROM external_audio_fit`)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var p, st, at string
		if err := rows.Scan(&p, &st, &at); err != nil {
			rows.Close()
			return err
		}
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if st == StatePending {
			if t, err := time.Parse(time.RFC3339, at); err == nil && w.now().Sub(t) < pendingKeep {
				w.again = true // its link is being made
				continue
			}
		}
		gone = append(gone, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range gone {
		if _, err := w.DB.ExecContext(ctx, `DELETE FROM external_audio_fit WHERE path=?`, p); err != nil {
			return err
		}
	}
	return nil
}

// due lists the rows to fit: the pending ones and those made of another
// track or video than now (now: the track is hidden until then), or the
// failed ones due again.
func (w *Worker) due(ctx context.Context, pending bool) ([]job, error) {
	q := `SELECT x.path, x.file_id, f.path, f.size, f.mtime, COALESCE(f.duration_ms,0), COALESCE(f.probe_json,''), x.state,
		  x.track_size, x.track_mtime, x.file_size, x.file_mtime, x.retry_at, x.orig_lang
		FROM external_audio_fit x JOIN files f ON f.id=x.file_id WHERE f.missing_since IS NULL`
	rows, err := w.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := w.now().UTC().Format(time.RFC3339)
	var out []job
	for rows.Next() {
		var j job
		var st string
		var ts, tm, fs, fm int64
		var retry sql.NullString
		if err := rows.Scan(&j.path, &j.fileID, &j.video, &j.size, &j.mtime, &j.durationMs, &j.probe, &st, &ts, &tm, &fs, &fm, &retry, &j.lang); err != nil {
			return nil, err
		}
		// a stale row hides its track like a pending one: fitted again at once
		stale := st != StatePending && st != StateFailed && !current(st, ts, tm, fs, fm, j.size, j.mtime, j.path)
		j.pending = st == StatePending || stale
		switch {
		case pending:
			if !j.pending {
				continue
			}
		case j.pending:
			continue
		case st == StateFailed:
			if !retry.Valid || retry.String > now {
				continue
			}
		default:
			continue
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// original picks the track a fit measures against: one in the title's
// original language (lang, ISO 639-1; "" unknown: the first that is not
// Russian, English first). None (only a dub, untagged tracks): false —
// a dub shares too little with a voice-over to measure it.
func original(probe, lang string) (media.AudioStream, bool) {
	var p media.ProbeResult
	if json.Unmarshal([]byte(probe), &p) != nil {
		return media.AudioStream{}, false
	}
	code := func(l string) string {
		if c := subtitles.NormLang(l); c != "" {
			return c
		}
		return strings.ToLower(strings.TrimSpace(l))
	}
	if want := code(lang); want != "" {
		for _, a := range p.Audio {
			if code(a.Lang) == want {
				return a, true
			}
		}
		return media.AudioStream{}, false
	}
	for _, a := range p.Audio {
		if code(a.Lang) == "en" {
			return a, true
		}
	}
	for _, a := range p.Audio {
		if c := code(a.Lang); c != "" && c != "und" && c != "ru" {
			return a, true
		}
	}
	return media.AudioStream{}, false
}

// run fits one track and stores the result.
func (w *Worker) run(ctx context.Context, j job) error {
	st, err := os.Stat(j.path)
	if err != nil {
		w.again = w.again || j.pending // not linked yet
		return nil
	}
	if j.probe == "" {
		w.again = w.again || j.pending // the video is not probed yet
		return nil
	}
	// a language stored empty (no TMDB answer at download) is asked again
	if j.lang == "" && w.OrigLang != nil {
		if l := w.OrigLang(ctx, j.fileID); l != "" {
			j.lang = l
			if _, err := w.DB.ExecContext(ctx, `UPDATE external_audio_fit SET orig_lang=? WHERE path=?`, l, j.path); err != nil {
				return err
			}
		}
	}
	orig, ok := original(j.probe, j.lang)
	var res Result
	if !ok {
		res = Result{State: StateUnmeasured, Tempo: 1, Why: "the video has no track in the title's original language to measure against"}
	} else {
		in := Input{Video: j.video, OrigIndex: orig.Index, DurationMs: j.durationMs, Track: j.path}
		if !j.pending {
			in.Busy = w.Busy
		}
		res, err = w.fit(ctx, in)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return w.failed(ctx, j, st, err)
		}
		// measured against a stand-in for the original (its language
		// unknown: perhaps a dub): no match says nothing of the cut
		if j.lang == "" && res.State == StateMisfit {
			res.State = StateUnmeasured
			res.Why = "the title's original language is unknown, the " + orig.Lang + " track did not match: " + res.Why
		}
	}
	w.fitted = w.fitted || res.State == StateOK
	m, _ := json.Marshal(res)
	w.log().Info("vofit: fitted", "track", j.path, "state", res.State, "offset_ms", res.OffsetMs, "tempo", res.Tempo, "why", res.Why)
	_, err = w.DB.ExecContext(ctx, `INSERT INTO external_audio_fit(path, file_id, state, offset_ms, tempo, measure_json, track_size, track_mtime, file_size, file_mtime, fails, retry_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,0,NULL,?)
		ON CONFLICT(path) DO UPDATE SET file_id=excluded.file_id, state=excluded.state, offset_ms=excluded.offset_ms, tempo=excluded.tempo,
		  measure_json=excluded.measure_json, track_size=excluded.track_size, track_mtime=excluded.track_mtime, file_size=excluded.file_size,
		  file_mtime=excluded.file_mtime, fails=0, retry_at=NULL, updated_at=excluded.updated_at`,
		j.path, j.fileID, res.State, res.OffsetMs, res.Tempo, string(m), st.Size(), st.ModTime().UnixNano(), j.size, j.mtime, w.stamp())
	return err
}

// failed counts a failure: retried after an hour, doubling to a week;
// after maxFails left failed for good. A missing ffmpeg ends the pass.
func (w *Worker) failed(ctx context.Context, j job, st os.FileInfo, cause error) error {
	if errors.Is(cause, exec.ErrNotFound) {
		return cause
	}
	var fails int
	_ = w.DB.QueryRowContext(ctx, `SELECT fails FROM external_audio_fit WHERE path=?`, j.path).Scan(&fails)
	fails++
	var retry any
	if fails < maxFails {
		d := min(retryBase<<min(fails-1, 20), retryMax)
		retry = w.now().Add(d).UTC().Format(time.RFC3339)
	}
	w.log().Warn("vofit: fit failed", "track", j.path, "fails", fails, "err", cause)
	m, _ := json.Marshal(Result{State: StateFailed, Why: cause.Error()})
	_, err := w.DB.ExecContext(ctx, `INSERT INTO external_audio_fit(path, file_id, state, measure_json, track_size, track_mtime, file_size, file_mtime, fails, retry_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET state=excluded.state, measure_json=excluded.measure_json, track_size=excluded.track_size,
		  track_mtime=excluded.track_mtime, file_size=excluded.file_size, file_mtime=excluded.file_mtime, fails=excluded.fails,
		  retry_at=excluded.retry_at, updated_at=excluded.updated_at`,
		j.path, j.fileID, StateFailed, string(m), st.Size(), st.ModTime().UnixNano(), j.size, j.mtime, fails, retry, w.stamp())
	return err
}

func (w *Worker) fit(ctx context.Context, in Input) (Result, error) {
	if w.Fit != nil {
		return w.Fit(ctx, in)
	}
	ff := w.FFmpeg
	if ff == "" {
		ff = "ffmpeg"
	}
	return Fit(ctx, ff, in)
}

func (w *Worker) stamp() string { return w.now().UTC().Format(time.RFC3339) }
