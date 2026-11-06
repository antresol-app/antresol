package voclean

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/extaudio"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/antresol-app/antresol/internal/release"
	"github.com/antresol-app/antresol/internal/vofit"
)

const (
	retryBase = time.Hour
	retryMax  = 7 * 24 * time.Hour
	// maxFails: a track that failed this many times is given up on.
	maxFails = 5
	// missingKeep: the cleaned tracks of a file missing this long (an
	// unmounted disk comes back sooner) are removed.
	missingKeep = 7 * 24 * time.Hour
	// jobLimit: ffmpeg's running time (not held for playback) for one
	// read of a file (a 2-hour film takes minutes at the lowest priority).
	jobLimit   = 3 * time.Hour
	startDelay = 5 * time.Minute
	// freshFor: a file added this recently is cleaned even while
	// something plays (an episode played right after its download).
	freshFor = 48 * time.Hour
	// busyRetry: a pass that left backlog files for playback to end runs
	// again this soon.
	busyRetry = time.Minute
)

// fileName: DataDir/audio/<file id>-<voice-over stream index>.mka (and its
// temp file while written).
// An external voice-over's (ExternalIndex, negative) is <file id>-e<−index>.mka.
var fileName = regexp.MustCompile(`^(\d+)-(e?\d+)\.mka(\.tmp)?$`)

// outName is the cleaned track's file name of a file's voice-over idx.
func outName(fileID int64, idx int) string {
	if idx < 0 {
		return fmt.Sprintf("%d-e%d.mka", fileID, -idx)
	}
	return fmt.Sprintf("%d-%d.mka", fileID, idx)
}

// errDiskFull ends a pass whose job failed while the disk was full: not
// the tracks' fault, nothing counted.
var errDiskFull = errors.New("voclean: disk full")

// Worker makes the cleaned voice-overs of the library in the background,
// a file at a time (the queue looked at again after each, so a new
// download goes ahead of the backlog): first the files of what a profile
// is watching (its «Продолжить» entry, the episode after it), then the
// newest. Every Russian voice-over (or a track named after a known
// studio) laid over the file's English track of its layout is cleaned,
// the household's favourite studios first, all of a file's from one read.
// The ffmpeg children run at the lowest CPU and I/O priority; files
// added within 48 h and next episodes run even while something plays,
// older ones wait until nothing does and yield the disk to playback. A
// failure is retried after an hour, doubling to a week, and given up on
// after maxFails; a track that is not eligible is never measured again
// while its file stays the same.
type Worker struct {
	DB      *sql.DB
	FFmpeg  string
	FFprobe string
	DataDir string
	// Studios: «Любимые озвучки», best first (nil: none — stream order).
	Studios func(ctx context.Context) ([]string, error)
	// Disk: false while the disk is full or the media disk is away (nil:
	// always fine); a pass then does nothing.
	Disk func() bool
	// Busy: playback needs the media disk (nil: never).
	Busy func() bool
	Log  *slog.Logger
	Now  func() time.Time
	// Idle: how often a pass runs unprompted (6 h when 0); StartDelay: the
	// first pass after Run starts (startDelay when 0).
	Idle, StartDelay time.Duration
	// Eligible, Clean and StartOf override the ffmpeg work (tests).
	Eligible func(ctx context.Context, in Input, pairs []Pair) ([]Measure, error)
	Clean    func(ctx context.Context, in Input, jobs []Job) []error
	StartOf  func(ctx context.Context, path string) (float64, error)
	// ProbeTrack overrides the probe of an external voice-over (tests).
	ProbeTrack func(ctx context.Context, path string) (media.AudioStream, error)

	once sync.Once
	kick chan struct{}
	// heldForPlayback: the last pass left backlog files until playback ends
	heldForPlayback bool
	queues          int // queue reads (tests)
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

func (w *Worker) diskOK() bool { return w.Disk == nil || w.Disk() }

func (w *Worker) busy() bool { return w.Busy != nil && w.Busy() }

func (w *Worker) dir() string { return filepath.Join(w.DataDir, "audio") }

// Kick asks for a pass soon (a scan added files); never blocks.
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
// Idle (a minute while backlog files wait for playback to end).
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
		if err := w.Pass(ctx); err != nil && ctx.Err() == nil {
			w.log().Warn("voclean: pass", "err", err)
		}
		wait = idle
		if w.heldForPlayback {
			wait = busyRetry
		}
	}
}

// track is a voice-over of a file to clean.
type track struct {
	Pair
	studio string
	rank   int // favourite-studio rank (len(Studios): unlisted)
	// extPath: an external voice-over (stage B, fitted by vofit) and the
	// offset of its fit; "" for a stream of the file
	extPath   string
	extOffset float64
}

// job is a file with tracks to clean.
type job struct {
	fileID, itemID int64
	path           string
	size, mtime    int64
	durationMs     int64
	// fresh: added within freshFor, or about to be watched: runs even
	// while something plays
	fresh  bool
	tracks []track
	order  int // the queue's order before the watched ones go first
}

// Pass removes what is stale (files gone, replaced or missing for a week)
// and cleans every track due, a file at a time, looking at the queue again
// after each file. Backlog files are left while something plays
// (heldForPlayback: Run comes back in a minute).
func (w *Worker) Pass(ctx context.Context) error {
	w.init()
	w.heldForPlayback = false
	if w.DB == nil || !w.diskOK() {
		return nil
	}
	if err := os.MkdirAll(w.dir(), 0o755); err != nil {
		return err
	}
	if err := w.collect(ctx); err != nil {
		return err
	}
	// tracks cleaned before the gain bound followed the voice-over's level
	// (no frontGain in their measure) are made again: a voice-over mixed
	// louder than its original kept a third of the English
	if _, err := w.DB.ExecContext(ctx, `UPDATE cleaned_audio SET state='queued', retry_at=NULL
		WHERE state='done' AND json_extract(measure_json, '$.frontGain') IS NULL`); err != nil {
		return err
	}
	tried := map[int64]bool{}
	done := 0
	defer func() {
		if done > 0 {
			w.log().Info("voclean: voice-overs cleaned", "tracks", done)
		}
	}()
	// the queue is walked from pos; files not on disk move pos on without
	// reading it again (only a file run can change it)
	var jobs []job
	pos, held, stale := 0, false, true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !w.diskOK() {
			return nil
		}
		if stale {
			var err error
			if jobs, err = w.queue(ctx); err != nil {
				return err
			}
			pos, held, stale = 0, false, false
		}
		var next *job
		for ; pos < len(jobs) && next == nil; pos++ {
			j := &jobs[pos]
			switch {
			case tried[j.fileID]:
			case !j.fresh && w.busy():
				held = true
			default:
				next = j
			}
		}
		if next == nil {
			w.heldForPlayback = held
			return nil
		}
		tried[next.fileID] = true
		if _, err := os.Stat(next.path); err != nil {
			continue // not on disk (an unmounted drive): left for later
		}
		stale = true
		n, err := w.runFile(ctx, *next)
		done += n
		if errors.Is(err, errDiskFull) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// runFile measures and cleans a file's tracks due, from one read each:
// the number of cleaned tracks made.
func (w *Worker) runFile(ctx context.Context, j job) (int, error) {
	// the external voice-overs are the read's further inputs
	var ext []ExternalTrack
	inputs := map[string]int{}
	for i := range j.tracks {
		t := &j.tracks[i]
		if t.extPath == "" {
			continue
		}
		k, ok := inputs[t.extPath]
		if !ok {
			ext = append(ext, ExternalTrack{Path: t.extPath, OffsetMs: t.extOffset})
			k = len(ext)
			inputs[t.extPath] = k
		}
		t.VoInput = k
	}
	for _, t := range j.tracks {
		if _, err := w.DB.ExecContext(ctx, `INSERT INTO cleaned_audio(file_id, vo_index, orig_index, studio, lang, title, state, file_size, file_mtime, vo_path, updated_at)
			VALUES(?,?,?,?,?,?,'queued',?,?,?,?)
			ON CONFLICT(file_id, vo_index) DO UPDATE SET orig_index=excluded.orig_index, studio=excluded.studio, lang=excluded.lang,
			  title=excluded.title, file_size=excluded.file_size, file_mtime=excluded.file_mtime, vo_path=excluded.vo_path, updated_at=excluded.updated_at`,
			j.fileID, t.Vo.Index, t.Orig.Index, t.studio, t.Vo.Lang, t.Vo.Title, j.size, j.mtime, t.extPath, w.stamp()); err != nil {
			return 0, err
		}
	}
	in := Input{Path: j.path, DurationMs: j.durationMs, Limit: jobLimit, External: ext}
	if !j.fresh {
		in.Busy = w.Busy // the backlog yields the disk to playback
	}
	pairs := make([]Pair, len(j.tracks))
	for i, t := range j.tracks {
		pairs[i] = t.Pair
	}
	ms, err := w.eligible(ctx, in, pairs)
	if err == nil && len(ms) != len(pairs) {
		err = fmt.Errorf("voclean: %d measures for %d tracks", len(ms), len(pairs))
	}
	if errors.Is(err, ErrBadlyInterleaved) {
		return 0, w.notEligible(ctx, j, j.tracks, err)
	}
	if err != nil {
		return 0, w.failed(ctx, j, j.tracks, nil, err)
	}
	var cleanTracks []track
	var cleanMs []Measure
	var jobs []Job
	for i, t := range j.tracks {
		m := ms[i]
		if !m.Eligible {
			w.log().Info("voclean: not eligible", "file", j.path, "track", t.Vo.Index, "why", m.Why)
			if _, err := w.DB.ExecContext(ctx, `UPDATE cleaned_audio SET state='not_eligible', measure_json=?, path='', retry_at=NULL, updated_at=?
				WHERE file_id=? AND vo_index=?`, measureJSON(m), w.stamp(), j.fileID, t.Vo.Index); err != nil {
				return 0, err
			}
			continue
		}
		cleanTracks = append(cleanTracks, t)
		cleanMs = append(cleanMs, m)
		jobs = append(jobs, Job{Pair: t.Pair, LagMs: m.LagMs, GainCap: GainCap(m.FrontGain), Out: filepath.Join(w.dir(), outName(j.fileID, t.Vo.Index))})
	}
	if len(jobs) == 0 {
		return 0, nil
	}
	start := time.Now()
	errs := w.clean(ctx, in, jobs)
	if n := len(errs); n != len(jobs) {
		errs = make([]error, len(jobs))
		for i := range errs {
			errs[i] = fmt.Errorf("voclean: %d results for %d tracks", n, len(jobs))
		}
	}
	var badTracks, failTracks []track
	var badErr error
	var failMs []Measure
	var failCause error
	for i, err := range errs {
		if err == nil {
			continue
		}
		_ = os.Remove(jobs[i].Out + ".tmp")
		_ = os.Remove(jobs[i].Out)
		if errors.Is(err, ErrBadlyInterleaved) {
			badTracks, badErr = append(badTracks, cleanTracks[i]), err
			continue
		}
		failTracks, failMs = append(failTracks, cleanTracks[i]), append(failMs, cleanMs[i])
		if failCause == nil {
			failCause = err
		}
	}
	offset, err := w.startOf(ctx, j.path)
	if err != nil {
		w.log().Warn("voclean: start of the file's timeline", "file", j.path, "err", err)
	}
	made := 0
	for i, t := range cleanTracks {
		if errs[i] != nil {
			continue
		}
		made++
		w.log().Info("voclean: cleaned", "file", j.path, "track", t.Vo.Index, "studio", t.studio,
			"lag_ms", cleanMs[i].LagMs, "took", time.Since(start).Round(time.Second))
		if _, err := w.DB.ExecContext(ctx, `UPDATE cleaned_audio SET state='done', path=?, method=?, measure_json=?, start_ms=?, fails=0, retry_at=NULL, updated_at=?
			WHERE file_id=? AND vo_index=?`, jobs[i].Out, MethodSubtraction, measureJSON(cleanMs[i]), offset, w.stamp(), j.fileID, t.Vo.Index); err != nil {
			return made, err
		}
	}
	if len(badTracks) > 0 {
		if err := w.notEligible(ctx, j, badTracks, badErr); err != nil {
			return made, err
		}
	}
	if len(failTracks) > 0 {
		return made, w.failed(ctx, j, failTracks, failMs, failCause)
	}
	return made, nil
}

// notEligible stores tracks as not eligible for a reason found while
// reading (ErrBadlyInterleaved): not a failure, not retried while the file
// stays the same.
func (w *Worker) notEligible(ctx context.Context, j job, tracks []track, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m := Measure{Why: cause.Error()}
	for _, t := range tracks {
		w.log().Info("voclean: not eligible", "file", j.path, "track", t.Vo.Index, "why", m.Why)
		if _, err := w.DB.ExecContext(ctx, `UPDATE cleaned_audio SET state='not_eligible', measure_json=?, path='', retry_at=NULL, updated_at=?
			WHERE file_id=? AND vo_index=?`, measureJSON(m), w.stamp(), j.fileID, t.Vo.Index); err != nil {
			return err
		}
	}
	return nil
}

// failed counts a failure of tracks: retried after an hour doubling to a
// week, given up on (retry_at NULL) after maxFails. The pass's own end
// (ctx), a missing ffmpeg and a full disk (errDiskFull: the pass ends)
// are not failures of the tracks.
func (w *Worker) failed(ctx context.Context, j job, tracks []track, ms []Measure, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(cause, exec.ErrNotFound) {
		w.log().Warn("voclean: no ffmpeg, nothing cleaned", "err", cause)
		return cause
	}
	if !w.diskOK() {
		w.log().Warn("voclean: disk full, held", "file", j.path, "err", cause)
		return errDiskFull
	}
	for i, t := range tracks {
		var m Measure
		if i < len(ms) {
			m = ms[i]
		}
		var fails int
		if err := w.DB.QueryRowContext(ctx, `SELECT fails FROM cleaned_audio WHERE file_id=? AND vo_index=?`, j.fileID, t.Vo.Index).Scan(&fails); err != nil {
			return err
		}
		fails++
		var retry any
		if fails < maxFails {
			d := retryBase << min(fails-1, 20)
			if d > retryMax || d <= 0 {
				d = retryMax
			}
			retry = w.now().Add(d).UTC().Format(time.RFC3339)
		}
		w.log().Warn("voclean: track failed", "file", j.path, "track", t.Vo.Index, "fails", fails, "err", cause)
		if _, err := w.DB.ExecContext(ctx, `UPDATE cleaned_audio SET state='failed', fails=?, retry_at=?, path='', measure_json=?, updated_at=?
			WHERE file_id=? AND vo_index=?`, fails, retry, measureJSON(m), w.stamp(), j.fileID, t.Vo.Index); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) stamp() string { return w.now().UTC().Format(time.RFC3339) }

func measureJSON(m Measure) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func (w *Worker) eligible(ctx context.Context, in Input, pairs []Pair) ([]Measure, error) {
	if w.Eligible != nil {
		return w.Eligible(ctx, in, pairs)
	}
	return Eligible(ctx, w.ffmpeg(), in, pairs)
}

func (w *Worker) clean(ctx context.Context, in Input, jobs []Job) []error {
	if w.Clean != nil {
		return w.Clean(ctx, in, jobs)
	}
	return Clean(ctx, w.ffmpeg(), in, jobs)
}

func (w *Worker) startOf(ctx context.Context, path string) (float64, error) {
	if w.StartOf != nil {
		return w.StartOf(ctx, path)
	}
	return StartOffsetMs(ctx, w.FFprobe, path)
}

func (w *Worker) ffmpeg() string {
	if w.FFmpeg == "" {
		return "ffmpeg"
	}
	return w.FFmpeg
}

// collect removes the rows and cleaned tracks of files missing for a week
// or replaced (size or mtime changed: measured again), and every file in
// DataDir/audio no done row names (deleted files' tracks, temp files of an
// interrupted job).
func (w *Worker) collect(ctx context.Context) error {
	cutoff := w.now().Add(-missingKeep).UTC().Format(time.RFC3339)
	rows, err := w.DB.QueryContext(ctx, `SELECT c.file_id, c.vo_index, c.path FROM cleaned_audio c
		JOIN files f ON f.id=c.file_id JOIN items i ON i.id=f.item_id
		WHERE c.file_size<>f.size OR c.file_mtime<>f.mtime
		   OR (f.missing_since IS NOT NULL AND f.missing_since<=?)
		   OR (i.missing_since IS NOT NULL AND i.missing_since<=?)`, cutoff, cutoff)
	if err != nil {
		return err
	}
	type stale struct {
		file int64
		vo   int
		path string
	}
	var list []stale
	for rows.Next() {
		var s stale
		if err := rows.Scan(&s.file, &s.vo, &s.path); err != nil {
			rows.Close()
			return err
		}
		list = append(list, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range list {
		if s.path != "" {
			if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
				w.log().Warn("voclean: remove", "path", s.path, "err", err)
			}
		}
		if _, err := w.DB.ExecContext(ctx, `DELETE FROM cleaned_audio WHERE file_id=? AND vo_index=?`, s.file, s.vo); err != nil {
			return err
		}
	}
	keep := map[string]bool{}
	prow, err := w.DB.QueryContext(ctx, `SELECT path FROM cleaned_audio WHERE state='done' AND path<>''`)
	if err != nil {
		return err
	}
	for prow.Next() {
		var p string
		if err := prow.Scan(&p); err != nil {
			prow.Close()
			return err
		}
		keep[filepath.Clean(p)] = true
	}
	prow.Close()
	if err := prow.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(w.dir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !fileName.MatchString(e.Name()) {
			continue // not ours
		}
		p := filepath.Join(w.dir(), e.Name())
		if keep[filepath.Clean(p)] {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			w.log().Warn("voclean: remove", "path", p, "err", err)
		}
	}
	return nil
}

// queue lists the files with tracks due, in the order they are cleaned.
func (w *Worker) queue(ctx context.Context) ([]job, error) {
	w.queues++
	var studios []string
	if w.Studios != nil {
		s, err := w.Studios(ctx)
		if err != nil {
			w.log().Warn("voclean: favourite studios", "err", err)
		}
		studios = s
	}
	type rowState struct {
		state string
		retry sql.NullString
	}
	states := map[[2]int64]rowState{}
	srows, err := w.DB.QueryContext(ctx, `SELECT file_id, vo_index, state, retry_at FROM cleaned_audio`)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var f, v int64
		var s rowState
		if err := srows.Scan(&f, &v, &s.state, &s.retry); err != nil {
			srows.Close()
			return nil, err
		}
		states[[2]int64{f, v}] = s
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return nil, err
	}
	now := w.now().UTC().Format(time.RFC3339)
	due := func(file int64, vo int) bool {
		s, ok := states[[2]int64{file, int64(vo)}]
		switch {
		case !ok || s.state == "queued":
			return true
		case s.state == "failed":
			return s.retry.Valid && s.retry.String <= now
		}
		return false // done, not eligible
	}

	rows, err := w.DB.QueryContext(ctx, `SELECT f.id, f.item_id, f.path, f.size, f.mtime, COALESCE(f.duration_ms,0), f.probe_json, i.added_at
		FROM files f JOIN items i ON i.id=f.item_id
		WHERE i.kind IN ('movie','episode') AND f.probe_json IS NOT NULL AND f.probe_json<>''
		  AND f.missing_since IS NULL AND i.missing_since IS NULL
		ORDER BY i.added_at DESC, f.id`)
	if err != nil {
		return nil, err
	}
	// the external voice-overs fitted to their videos (stage B)
	fits, err := vofit.Fitted(ctx, w.DB)
	if err != nil {
		rows.Close()
		return nil, err
	}
	type fileProbe struct {
		j job
		p media.ProbeResult
	}
	var withExt []fileProbe
	var jobs []job
	for rows.Next() {
		var j job
		j.order = len(jobs) + len(withExt)
		var probe, added string
		if err := rows.Scan(&j.fileID, &j.itemID, &j.path, &j.size, &j.mtime, &j.durationMs, &probe, &added); err != nil {
			rows.Close()
			return nil, err
		}
		if at, err := time.Parse(time.RFC3339, added); err == nil && w.now().Sub(at) < freshFor {
			j.fresh = true
		}
		var p media.ProbeResult
		if json.Unmarshal([]byte(probe), &p) != nil || p.Video == nil {
			continue
		}
		if j.durationMs <= 0 {
			j.durationMs = p.DurationMs
		}
		if len(fits[j.fileID]) > 0 {
			withExt = append(withExt, fileProbe{j, p})
			continue // after the rows: its external tracks are probed
		}
		for _, t := range voiceOvers(p.Audio, studios) {
			if due(j.fileID, t.Vo.Index) {
				j.tracks = append(j.tracks, t)
			}
		}
		if len(j.tracks) > 0 {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, fp := range withExt {
		j := fp.j
		audio, ext := w.externalStreams(ctx, j, fp.p.Audio, fits[j.fileID], due)
		for _, t := range voiceOvers(audio, studios) {
			if t.Vo.Index < 0 {
				t.extPath, t.extOffset = ext[t.Vo.Index].Path, ext[t.Vo.Index].OffsetMs
			} else if !due(j.fileID, t.Vo.Index) {
				continue
			}
			j.tracks = append(j.tracks, t)
		}
		if len(j.tracks) > 0 {
			jobs = append(jobs, j)
		}
	}
	// the newest first, as the rows came
	sort.SliceStable(jobs, func(a, b int) bool { return jobs[a].order < jobs[b].order })
	first, err := w.watching(ctx)
	if err != nil {
		return nil, err
	}
	rank := func(item int64) int {
		if r, ok := first[item]; ok {
			return r
		}
		return len(first)
	}
	for i := range jobs {
		if _, ok := first[jobs[i].itemID]; ok {
			jobs[i].fresh = true // about to be watched
		}
	}
	sort.SliceStable(jobs, func(a, b int) bool { return rank(jobs[a].itemID) < rank(jobs[b].itemID) })
	return jobs, nil
}

// watching ranks the items to clean first: every profile's «Продолжить»
// entries (latest first), each followed by the episode after it.
func (w *Worker) watching(ctx context.Context) (map[int64]int, error) {
	urows, err := w.DB.QueryContext(ctx, `SELECT id FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var users []int64
	for urows.Next() {
		var id int64
		if err := urows.Scan(&id); err != nil {
			urows.Close()
			return nil, err
		}
		users = append(users, id)
	}
	urows.Close()
	if err := urows.Err(); err != nil {
		return nil, err
	}
	store := progress.New(w.DB)
	var entries []progress.Entry
	for _, u := range users {
		e, err := store.ContinueWatching(ctx, u, 0)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e...)
	}
	sort.SliceStable(entries, func(a, b int) bool { return entries[a].UpdatedAt.After(entries[b].UpdatedAt) })
	out := map[int64]int{}
	add := func(id int64) {
		if _, ok := out[id]; !ok {
			out[id] = len(out)
		}
	}
	for _, e := range entries {
		add(e.ItemID)
		if e.SeriesID == 0 {
			continue
		}
		next, ok, err := w.nextEpisode(ctx, e.ItemID)
		if err != nil {
			return nil, err
		}
		if ok {
			add(next)
		}
	}
	return out, nil
}

// nextEpisode: the episode after id in its series (season and episode
// order, specials last).
func (w *Worker) nextEpisode(ctx context.Context, id int64) (int64, bool, error) {
	var series sql.NullInt64
	var season, ep int64
	err := w.DB.QueryRowContext(ctx, `SELECT s.parent_id, COALESCE(e.season_no,0), COALESCE(e.episode_no,0)
		FROM items e JOIN items s ON s.id=e.parent_id WHERE e.id=? AND e.kind='episode'`, id).Scan(&series, &season, &ep)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !series.Valid) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var next int64
	err = w.DB.QueryRowContext(ctx, `SELECT e.id FROM items e JOIN items s ON s.id=e.parent_id
		WHERE s.parent_id=? AND e.kind='episode' AND e.missing_since IS NULL AND COALESCE(e.season_no,0)>0
		  AND (COALESCE(e.season_no,0) > ? OR (COALESCE(e.season_no,0)=? AND COALESCE(e.episode_no,0) > ?))
		ORDER BY e.season_no, e.episode_no, e.id LIMIT 1`, series.Int64, season, season, ep).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return next, err == nil, err
}

// externalStreams adds to a file's streams its fitted external
// voice-overs due (vofit rows of state ok and no tempo — a PAL track is
// not sample-aligned to the original once slowed, so never cleaned by
// subtraction): each as a stream of index ExternalIndex of its path, with
// the language and studio its name gives (extaudio) and its probe's
// codec and layout; only Russian ones or untagged ones named after a studio. ext
// maps those indexes to their fits.
func (w *Worker) externalStreams(ctx context.Context, j job, audio []media.AudioStream, fits []vofit.Stored, due func(int64, int) bool) ([]media.AudioStream, map[int]vofit.Stored) {
	out := slices.Clone(audio)
	ext := map[int]vofit.Stored{}
	var found []extaudio.Track
	for _, f := range fits {
		if f.Tempo != 0 && math.Abs(f.Tempo-1) > 1e-9 {
			continue
		}
		idx := ExternalIndex(f.Path)
		if !due(j.fileID, idx) {
			continue
		}
		if found == nil {
			found = extaudio.Find(j.path)
		}
		k := slices.IndexFunc(found, func(t extaudio.Track) bool { return t.Path == f.Path })
		if k < 0 {
			continue
		}
		t := found[k]
		// a Russian one, or an untagged one named after a studio: an English
		// track beside the video (even titled after a studio) is no
		// voice-over and must not become an original (input 0 has no such
		// stream)
		if !langIs(t.Lang, "ru", "rus") && !(langIs(t.Lang, "", "und") && t.Studio != "") {
			continue
		}
		a, err := w.probeTrack(ctx, f.Path)
		if err != nil {
			w.log().Warn("voclean: probe external voice-over", "track", f.Path, "err", err)
			continue
		}
		a.Index, a.Lang, a.Title, a.Default = idx, t.Lang, t.Title, false
		if a.Title == "" {
			a.Title = t.Studio
		}
		out = append(out, a)
		ext[idx] = f
	}
	return out, ext
}

func (w *Worker) probeTrack(ctx context.Context, path string) (media.AudioStream, error) {
	if w.ProbeTrack != nil {
		return w.ProbeTrack(ctx, path)
	}
	p, err := media.ProbeAudioFile(ctx, w.FFprobe, path)
	if err != nil {
		return media.AudioStream{}, err
	}
	if len(p.Audio) == 0 {
		return media.AudioStream{}, fmt.Errorf("no audio stream")
	}
	return p.Audio[0], nil
}

// voiceOvers picks a file's voice-overs to clean, each with its original:
// each Russian track (or an untagged one titled after a known studio), the
// favourite studios first, then by stream; its original is the first
// English track of its layout, else the first English track (measured:
// not eligible, the layouts differ).
func voiceOvers(audio []media.AudioStream, studios []string) []track {
	var english []media.AudioStream
	for _, a := range audio {
		if langIs(a.Lang, "eng", "en", "english") {
			english = append(english, a)
		}
	}
	if len(english) == 0 {
		return nil
	}
	originalOf := func(vo media.AudioStream) media.AudioStream {
		for _, e := range english {
			if e.Channels == vo.Channels {
				return e
			}
		}
		return english[0]
	}
	keys := make([]string, len(studios))
	for i, s := range studios {
		keys[i] = release.StudioKey(s)
	}
	var out []track
	for _, a := range audio {
		named := release.Studios(a.Title)
		ru := langIs(a.Lang, "rus", "ru", "russian")
		if !ru && !(langIs(a.Lang, "", "und") && len(named) > 0) {
			continue
		}
		t := track{Pair: Pair{Vo: a, Orig: originalOf(a)}, studio: strings.TrimSpace(a.Title), rank: len(keys)}
		if len(named) > 0 {
			t.studio = named[0]
		}
		for _, n := range append(named, a.Title) {
			for r, k := range keys {
				if r < t.rank && (release.StudioKey(n) == k || release.NamesStudio(a.Title, studios[r])) {
					t.rank = r
				}
			}
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].rank < out[b].rank })
	return out
}

func langIs(l string, codes ...string) bool {
	l = strings.ToLower(strings.TrimSpace(l))
	for _, c := range codes {
		if l == c {
			return true
		}
	}
	return false
}
