package segments

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

const (
	// PlaybackIdle: the analysis reads the disk only after this long
	// without playback (main wires Busy with it).
	PlaybackIdle = 2 * time.Minute

	limitPauseBase = time.Hour      // a 429 (or the daily cap) pauses the lookups
	limitPauseMax  = 24 * time.Hour //
	failPauseBase  = 10 * time.Minute
	failPauseMax   = 6 * time.Hour
	failRun        = 3 // unreachable services in a row pause the lookups
	// retryBase / retryMax: one file's failed step (a chapter read, an
	// online answer not understood, a fingerprint) is retried after
	// retryBase, doubling to retryMax.
	retryBase = time.Hour
	retryMax  = 7 * 24 * time.Hour
	// cmdRunLimit: an ffmpeg run is killed after this much RUNNING time
	// (time held stopped for playback does not count).
	cmdRunLimit   = 10 * time.Minute
	chaptersLimit = time.Minute
	yieldEvery    = 250 * time.Millisecond
	// probeRetry: how soon a pass comes back for files still unprobed
	probeRetry = 2 * time.Minute
	// wantEvery: a play asks for a file never looked at at most this often
	wantEvery = time.Hour
	// FingerprintVersion names the cached fingerprints (bump when the
	// windows or the chromaprint call change).
	FingerprintVersion = 1
)

// Worker finds the segments of the library's films and episodes in the
// background, one season (or film) at a time: chapters, then the online
// databases, then the analysis for what they leave unknown. Only files on
// disk are read. The analysis runs only in full passes (after a scan, the
// first one, every Idle), waits until playback has been quiet for
// PlaybackIdle (Busy) and holds its ffmpeg stopped whenever playback starts
// again. A play of a file never looked at asks only for the cheap steps
// (chapters, online). What is done is stored with versions, failures with
// a retry time (segment_status), so a pass redoes only what is due.
// Fingerprints are cached on disk (CacheDir): a season re-analysed for a
// new episode fingerprints only that episode.
type Worker struct {
	Store  Store
	FFmpeg string
	// FFprobe reads the chapters.
	FFprobe string
	// Online is nil (or has no URLs) when the databases are not asked.
	Online *Online
	// Busy: something plays (an HLS session, a stream read or a progress
	// save in the last PlaybackIdle); nil never.
	Busy func() bool
	Log  *slog.Logger
	Now  func() time.Time
	// Idle: how often a pass runs unprompted (6 h when 0).
	Idle time.Duration
	// CacheDir keeps the fingerprints ("" none).
	CacheDir string

	// Overrides of the media reads (tests).
	Chapters    func(ctx context.Context, path string) ([]media.Chapter, error)
	Fingerprint func(ctx context.Context, path string, audioIdx int, fromSec, durSec float64) ([]uint32, error)
	Black       func(ctx context.Context, path string, fromSec, durSec, minSec float64, keyframesOnly bool) ([]media.BlackRun, error)
	// Chromaprint says whether ffmpeg can fingerprint (nil: asked once).
	Chromaprint *bool

	once     sync.Once
	kick     chan struct{} // a full pass
	light    chan struct{} // the files a play asked for: chapters and online only
	mu       sync.Mutex
	want     map[int64]bool      // files asked for by a play
	asked    map[int64]time.Time // when a play last asked for a file
	cpOnce   sync.Once
	cp       bool
	fails    int
	inLight  bool // a light pass runs inside a full pass' wait
	pause    time.Duration
	resumeAt time.Time
}

func (w *Worker) init() {
	w.once.Do(func() {
		w.kick, w.light = make(chan struct{}, 1), make(chan struct{}, 1)
		w.want, w.asked = map[int64]bool{}, map[int64]time.Time{}
	})
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

func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// Kick asks for a full pass soon (a scan added files); never blocks.
func (w *Worker) Kick() {
	if w == nil {
		return
	}
	w.init()
	signal(w.kick)
}

// ForPlay is what POST /api/play returns for a file: the stored segments
// (Pick), never waiting for a lookup. A file never looked at is asked for
// (chapters and online only, at most once per wantEvery: a file that never
// gets a status does not start a pass on every play or seek).
func (w *Worker) ForPlay(ctx context.Context, fileID, durationMs int64, movie bool) []Play {
	if w == nil {
		return []Play{}
	}
	segs, err := w.Store.Of(ctx, fileID)
	if err != nil {
		w.log().Warn("segments: read", "file", fileID, "err", err)
		return []Play{}
	}
	var seen int
	if err := w.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM segment_status WHERE file_id=?`, fileID).Scan(&seen); err == nil && seen == 0 {
		w.init()
		now := w.now()
		w.mu.Lock()
		fresh := w.asked[fileID].IsZero() || now.Sub(w.asked[fileID]) >= wantEvery
		if fresh {
			w.asked[fileID], w.want[fileID] = now, true
		}
		w.mu.Unlock()
		if fresh {
			signal(w.light)
		}
	}
	return Pick(segs, durationMs, movie)
}

// Run passes over the library until ctx is done: a full pass soon after
// the start (the existing library), after each Kick and every Idle; a
// light pass when a play asks for a file.
func (w *Worker) Run(ctx context.Context) {
	w.init()
	idle := w.Idle
	if idle <= 0 {
		idle = 6 * time.Hour
	}
	wait := time.Minute // the first scan usually kicks before this
	unprobed := 0
	for {
		t := time.NewTimer(wait)
		full := true
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.kick:
			t.Stop()
		case <-w.light:
			t.Stop()
			full = false
		case <-t.C:
		}
		if err := w.pass(ctx, full); err != nil && ctx.Err() == nil {
			w.log().Warn("segments: pass", "err", err)
		}
		if !full {
			continue // the full pass keeps its schedule (restarted: a little later)
		}
		wait = idle
		// files a scan just added are probed after it kicked: back for them
		// soon, while their number moves (a file ffprobe fails on stays)
		if n, err := w.Store.unprobed(ctx); err == nil && n > 0 && n != unprobed {
			wait = probeRetry
			unprobed = n
		} else if err == nil && n == 0 {
			unprobed = 0
		}
	}
}

func (w *Worker) chromaprint(ctx context.Context) bool {
	if w.Chromaprint != nil {
		return *w.Chromaprint
	}
	w.cpOnce.Do(func() {
		w.cp = media.HasMuxer(ctx, w.ffmpeg(), "chromaprint")
		if !w.cp {
			w.log().Warn("segments: ffmpeg has no chromaprint muxer: intros and credits are not analysed")
		}
	})
	return w.cp
}

func (w *Worker) ffmpeg() string {
	if w.FFmpeg != "" {
		return w.FFmpeg
	}
	return "ffmpeg"
}

func (w *Worker) ffprobe() string {
	if w.FFprobe != "" {
		return w.FFprobe
	}
	return "ffprobe"
}

// run holds ffmpeg stopped while playback is on and kills it after
// cmdRunLimit of running time.
func (w *Worker) run(cmd *exec.Cmd) error {
	return media.RunYieldingLimit(cmd, w.busy, yieldEvery, cmdRunLimit)
}

func (w *Worker) chapters(ctx context.Context, path string) ([]media.Chapter, error) {
	ctx, cancel := context.WithTimeout(ctx, chaptersLimit)
	defer cancel()
	if w.Chapters != nil {
		return w.Chapters(ctx, path)
	}
	return media.Chapters(ctx, w.ffprobe(), path)
}

func (w *Worker) fingerprint(ctx context.Context, path string, idx int, from, dur float64) ([]uint32, error) {
	if w.Fingerprint != nil {
		return w.Fingerprint(ctx, path, idx, from, dur)
	}
	return media.Fingerprint(ctx, w.ffmpeg(), path, idx, from, dur, w.run)
}

func (w *Worker) black(ctx context.Context, path string, from, dur, minSec float64, keyOnly bool) ([]Span, error) {
	var runs []media.BlackRun
	var err error
	if w.Black != nil {
		runs, err = w.Black(ctx, path, from, dur, minSec, keyOnly)
	} else {
		runs, err = media.BlackRuns(ctx, w.ffmpeg(), path, from, dur, minSec, keyOnly, w.run)
	}
	out := make([]Span, len(runs))
	for i, r := range runs {
		out[i] = Span{r.Start, r.End}
	}
	return out, err
}

// idle waits until nothing plays; a play's request meanwhile gets its
// light pass (online lookups only: the disk stays untouched while busy).
func (w *Worker) idle(ctx context.Context) error {
	for w.busy() {
		t := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-w.light:
			t.Stop()
			if !w.inLight {
				w.inLight = true
				if err := w.pass(ctx, false); err != nil && ctx.Err() == nil {
					w.log().Warn("segments: light pass", "err", err)
				}
				w.inLight = false
			}
		case <-t.C:
		}
	}
	return ctx.Err()
}

// retryAfter is when a step that failed n times in a row is due again.
func retryAfter(now time.Time, n int) int64 {
	d := retryBase << min(max(n-1, 0), 20)
	if d > retryMax || d <= 0 {
		d = retryMax
	}
	return now.Add(d).Unix()
}

// onlineKey is what the lookups of f would ask ("" nothing to ask).
func (w *Worker) onlineKey(f file) string {
	var parts []string
	for _, s := range []string{SourceTheIntroDB, SourceIntroDB} {
		if w.Online.Enabled(s) {
			if k := f.lookup().key(s); k != "" {
				parts = append(parts, s+"="+k)
			}
		}
	}
	return strings.Join(parts, "|")
}

// due says what f still needs.
type due struct{ changed, chapters, online, analysis bool }

func (d due) cheap() bool { return d.changed || d.chapters || d.online }

func (w *Worker) dueOf(f file, chromaprint bool) due {
	var d due
	now := w.now().Unix()
	d.changed = f.HasStatus && (f.St.Size != f.Size || f.St.Mtime != f.Mtime)
	fresh := !f.HasStatus || d.changed
	d.chapters = fresh || (f.St.ChaptersV < ChaptersVersion && now >= f.St.ChaptersRetry)
	key := w.onlineKey(f)
	stale := key != "" && now-f.St.OnlineAt >= int64(OnlineTTL/time.Second)
	d.online = fresh || ((key != f.St.OnlineKey || stale) && now >= f.St.OnlineRetry)
	d.analysis = (fresh || (f.St.AnalysisV < AnalysisVersion && now >= f.St.AnalysisRetry)) && (f.Movie || chromaprint)
	return d
}

// Pass works through every season and film with something due (a full
// pass: the analysis too), those of files just played first, then the newest.
func (w *Worker) Pass(ctx context.Context) error { return w.pass(ctx, true) }

func (w *Worker) pass(ctx context.Context, full bool) error {
	w.init()
	files, err := w.Store.files(ctx)
	if err != nil {
		return err
	}
	cp := w.chromaprint(ctx)
	units := map[int64][]file{}
	dues := map[int64]due{}
	for _, f := range files {
		dues[f.ID] = w.dueOf(f, cp)
		units[f.unit()] = append(units[f.unit()], f)
	}
	type todo struct {
		key    int64
		wanted bool
		newest int64
	}
	var list []todo
	w.mu.Lock()
	for k, fs := range units {
		t := todo{key: k}
		any := false
		for _, f := range fs {
			d := dues[f.ID]
			any = any || d.cheap() || (full && d.analysis)
			t.wanted = t.wanted || w.want[f.ID]
			t.newest = max(t.newest, f.ID)
		}
		if any && (full || t.wanted) {
			list = append(list, t)
		}
	}
	w.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		if list[i].wanted != list[j].wanted {
			return list[i].wanted
		}
		return list[i].newest > list[j].newest
	})
	for _, t := range list {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.unit(ctx, units[t.key], dues, full); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.log().Warn("segments: unit", "show", units[t.key][0].Show, "err", err)
		}
		w.mu.Lock()
		for _, f := range units[t.key] {
			delete(w.want, f.ID)
		}
		w.mu.Unlock()
	}
	if full {
		w.pruneCache(files)
	}
	return nil
}

// unit does what is due for one season or film; the analysis only in a
// full pass.
func (w *Worker) unit(ctx context.Context, fs []file, dues map[int64]due, full bool) error {
	var present []file
	analysis := false
	for _, f := range fs {
		if _, err := os.Stat(f.Path); err != nil {
			continue // not on disk (an unmounted drive): left for later
		}
		d := dues[f.ID]
		if d.changed {
			if err := w.Store.forget(ctx, f.ID); err != nil {
				return err
			}
			f.St, f.HasStatus = status{}, false
		}
		f.St.Size, f.St.Mtime = f.Size, f.Mtime
		// chapters read the file's header: a full pass waits for quiet, a
		// play's light pass leaves them to it while something plays
		if d.chapters && (full || !w.busy()) {
			if full {
				if err := w.idle(ctx); err != nil {
					return err
				}
			}
			if err := w.chaptersStep(ctx, &f); err != nil {
				return err
			}
		}
		if d.online && !w.now().Before(w.resumeAt) {
			if err := w.online(ctx, &f); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				w.onlineFailed(&f, err)
			}
		}
		f.HasStatus = true
		if err := w.Store.saveStatus(ctx, f.ID, f.St); err != nil {
			return err
		}
		analysis = analysis || d.analysis
		present = append(present, f)
	}
	// a pending or failed lookup does not hold the analysis: a database
	// answer found later wins by priority
	if !full || !analysis || len(present) == 0 {
		return nil
	}
	failed, err := w.analyse(ctx, present)
	if err != nil {
		return err
	}
	for _, f := range present {
		if failed[f.ID] {
			f.St.AnalysisFails++
			f.St.AnalysisRetry = retryAfter(w.now(), f.St.AnalysisFails)
		} else {
			f.St.AnalysisV, f.St.AnalysisFails, f.St.AnalysisRetry = AnalysisVersion, 0, 0
		}
		if err := w.Store.saveStatus(ctx, f.ID, f.St); err != nil {
			return err
		}
	}
	return nil
}

// chaptersStep reads f's chapters; a failure is retried later (the chapter
// rows of an earlier read stay).
func (w *Worker) chaptersStep(ctx context.Context, f *file) error {
	chs, err := w.chapters(ctx, f.Path)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		w.log().Warn("segments: chapters", "file", f.Path, "err", err)
		f.St.ChaptersFails++
		f.St.ChaptersRetry = retryAfter(w.now(), f.St.ChaptersFails)
		return nil
	}
	if err := w.Store.replace(ctx, f.ID, SourceChapters, FromChapters(chs, f.DurationMs)); err != nil {
		return err
	}
	f.St.ChaptersV, f.St.ChaptersFails, f.St.ChaptersRetry = ChaptersVersion, 0, 0
	return nil
}

// onlineFailed: a 429 or an unreachable service pauses the lookups (the
// file is asked again when they resume); a bad answer about this file is
// retried for it alone, with its own backoff.
func (w *Worker) onlineFailed(f *file, err error) {
	now := w.now()
	switch {
	case errors.Is(err, ErrLimited):
		if w.pause < limitPauseBase {
			w.pause = limitPauseBase
		} else if w.pause *= 2; w.pause > limitPauseMax {
			w.pause = limitPauseMax
		}
		w.resumeAt = now.Add(w.pause)
		w.log().Warn("segments: online lookups paused (limit)", "for", w.pause, "err", err)
	case errors.Is(err, ErrNetwork):
		w.fails++
		if w.fails >= failRun {
			if w.pause < failPauseBase {
				w.pause = failPauseBase
			} else if w.pause *= 2; w.pause > failPauseMax {
				w.pause = failPauseMax
			}
			w.resumeAt = now.Add(w.pause)
			w.fails = 0
			w.log().Warn("segments: online databases are not answering, lookups paused", "for", w.pause, "err", err)
		}
	default:
		w.fails = 0
		f.St.OnlineFails++
		f.St.OnlineRetry = retryAfter(now, f.St.OnlineFails)
		w.log().Warn("segments: online lookup", "file", f.Path, "retry", time.Unix(f.St.OnlineRetry, 0), "err", err)
	}
}

// online asks the databases about f (TheIntroDB, then IntroDB for what is
// still missing), from the stored answers when fresh. Nothing is asked
// when the chapters already give everything (an episode's recap included).
func (w *Worker) online(ctx context.Context, f *file) error {
	key := w.onlineKey(*f)
	have, err := w.Store.Of(ctx, f.ID)
	if err != nil {
		return err
	}
	var got []Segment // the chapters' (the other sources are redone here)
	for _, s := range have {
		if s.Source == SourceChapters {
			got = append(got, s)
		}
	}
	for _, service := range []string{SourceTheIntroDB, SourceIntroDB} {
		k := f.lookup().key(service)
		// IntroDB is not asked just for a recap once intro and credits are known
		if !w.Online.Enabled(service) || k == "" || covered(got, f.Movie) || (service == SourceIntroDB && basicCovered(got)) {
			if err := w.Store.replace(ctx, f.ID, service, nil); err != nil {
				return err
			}
			continue
		}
		a, ok, err := w.Store.cached(ctx, service, k, w.now())
		if err != nil {
			return err
		}
		if !ok {
			if service == SourceTheIntroDB {
				a, err = w.Online.AskTheIntroDB(ctx, f.lookup(), f.DurationMs)
			} else {
				a, err = w.Online.AskIntroDB(ctx, f.lookup())
			}
			if err != nil {
				return err
			}
			w.fails, w.pause = 0, 0
			if err := w.Store.cache(ctx, service, k, a, w.now()); err != nil {
				return err
			}
		}
		segs := a.ForFile(f.DurationMs)
		if f.Movie {
			segs = credits(segs)
		}
		if err := w.Store.replace(ctx, f.ID, service, segs); err != nil {
			return err
		}
		got = append(got, segs...)
	}
	f.St.OnlineKey, f.St.OnlineAt, f.St.OnlineFails, f.St.OnlineRetry = key, w.now().Unix(), 0, 0
	return nil
}

func credits(segs []Segment) []Segment {
	var out []Segment
	for _, s := range segs {
		if s.Kind == Credits {
			out = append(out, s)
		}
	}
	return out
}

// audioTrack is the ffprobe index of the track to fingerprint: the
// original language (a dub talks over the theme: 11 s matched on the
// English track of a sitcom, 5 s on its Russian dub), taken to be the
// first track that is neither Russian nor Ukrainian nor a commentary;
// else the first that is not a commentary; -1 none.
func audioTrack(p media.ProbeResult) int {
	dub := map[string]bool{"rus": true, "ru": true, "ukr": true, "uk": true}
	commentary := func(a media.AudioStream) bool {
		t := strings.ToLower(a.Title)
		return strings.Contains(t, "comment") || strings.Contains(t, "коммент")
	}
	for _, a := range p.Audio {
		if !dub[strings.ToLower(a.Lang)] && !commentary(a) {
			return a.Index
		}
	}
	for _, a := range p.Audio {
		if !commentary(a) {
			return a.Index
		}
	}
	if len(p.Audio) > 0 {
		return p.Audio[0].Index
	}
	return -1
}

// analyse runs the analysis over a season (or a film) and stores what it
// finds; failed lists the files that could not be read (retried later).
func (w *Worker) analyse(ctx context.Context, fs []file) (failed map[int64]bool, err error) {
	failed = map[int64]bool{}
	all, basic := true, true
	known := map[int64][]Segment{}
	for _, f := range fs {
		segs, err := w.Store.Of(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		known[f.ID] = segs
		all = all && covered(segs, f.Movie)
		basic = basic && basicCovered(segs)
	}
	if all {
		return failed, nil // chapters and databases gave everything
	}
	if fs[0].Movie {
		for _, f := range fs {
			segs, err := w.Store.Of(ctx, f.ID)
			if err != nil {
				return nil, err
			}
			var out []Segment
			if !covered(segs, true) {
				dur := float64(f.DurationMs) / 1000
				tail := TailSec(dur, true)
				if err := w.idle(ctx); err != nil {
					return nil, err
				}
				bl, err := w.cachedBlack(ctx, f, dur-tail, tail)
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if err != nil {
					w.log().Warn("segments: black frames", "file", f.Path, "err", err)
					failed[f.ID] = true
					continue
				}
				if s, ok := BlackCredits(bl, dur-tail, dur); ok {
					out = append(out, Segment{Kind: Credits, Source: SourceAnalysis, StartMs: ms(s.Start), EndMs: ms(s.End)})
				}
			}
			if err := w.Store.replace(ctx, f.ID, SourceAnalysis, out); err != nil {
				return nil, err
			}
		}
		return failed, nil
	}
	began := w.now()
	items := map[int64]bool{}
	for _, f := range fs {
		items[f.Item] = true
	}
	if basic && !fs[0].Movie {
		// intro and credits known for every episode: only a recap is
		// missing, worth the heads of a season of three or more
		if len(items) < 3 {
			return failed, nil
		}
		return w.recapsOnly(ctx, fs, known, began)
	}
	var eps []Episode
	if len(items) >= 2 { // a single-episode season gets nothing from analysis
		for _, f := range fs {
			e, err := w.episode(ctx, f)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err != nil {
				w.log().Warn("segments: fingerprint", "file", f.Path, "err", err)
				failed[f.ID] = true
				continue
			}
			eps = append(eps, e)
		}
	}
	intros := FindIntros(eps)
	creds := FindCredits(eps)
	stings := FindStings(eps, intros)
	nRecap := 0
	for _, f := range fs {
		if failed[f.ID] {
			continue // its earlier rows stay until it is read
		}
		var segs []Segment
		if s, ok := intros[f.ID]; ok {
			segs = append(segs, Segment{Kind: Intro, Source: SourceAnalysis, StartMs: ms(s.Start), EndMs: ms(s.End)})
			if end, ok := stings[f.ID]; ok {
				if r, ok := w.recap(ctx, eps, f, end, s); ok {
					segs = append(segs, Segment{Kind: Recap, Source: SourceAnalysis, StartMs: ms(r.Start), EndMs: ms(r.End)})
					nRecap++
				}
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
			}
		}
		if s, ok := creds[f.ID]; ok {
			segs = append(segs, Segment{Kind: Credits, Source: SourceAnalysis, StartMs: ms(s.Start), EndMs: ms(s.End)})
		}
		if err := w.Store.replace(ctx, f.ID, SourceAnalysis, segs); err != nil {
			return nil, err
		}
	}
	w.log().Info("segments: season analysed", "show", fs[0].Show, "season", fs[0].SeasonNo, "files", len(fs),
		"intro", len(intros), "credits", len(creds), "recap", nRecap, "failed", len(failed), "took", w.now().Sub(began).Round(time.Second))
	return failed, nil
}

func ms(sec float64) int64 { return int64(sec*1000 + 0.5) }

// recapsOnly looks for recaps in a season whose intros and credits are
// known (chapters, databases): the heads alone, the known intros.
func (w *Worker) recapsOnly(ctx context.Context, fs []file, known map[int64][]Segment, began time.Time) (map[int64]bool, error) {
	failed := map[int64]bool{}
	intros := map[int64]Span{}
	var eps []Episode
	for _, f := range fs {
		e, err := w.head(ctx, f)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			w.log().Warn("segments: fingerprint", "file", f.Path, "err", err)
			failed[f.ID] = true
			continue
		}
		eps = append(eps, e)
		for _, p := range Pick(known[f.ID], f.DurationMs, false) {
			if p.Kind == Intro {
				intros[f.ID] = Span{p.StartSec, p.EndSec}
			}
		}
	}
	stings := FindStings(eps, intros)
	n := 0
	for _, f := range fs {
		if failed[f.ID] {
			continue
		}
		var segs []Segment
		if end, ok := stings[f.ID]; ok {
			if r, ok := w.recap(ctx, eps, f, end, intros[f.ID]); ok {
				segs = append(segs, Segment{Kind: Recap, Source: SourceAnalysis, StartMs: ms(r.Start), EndMs: ms(r.End)})
				n++
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		if err := w.Store.replace(ctx, f.ID, SourceAnalysis, segs); err != nil {
			return nil, err
		}
	}
	w.log().Info("segments: season analysed (recaps only)", "show", fs[0].Show, "season", fs[0].SeasonNo, "files", len(fs),
		"recap", n, "failed", len(failed), "took", w.now().Sub(began).Round(time.Second))
	return failed, nil
}

// head reads one file's head fingerprint (from the cache when there).
func (w *Worker) head(ctx context.Context, f file) (Episode, error) {
	e := Episode{File: f.ID, Item: f.Item, Number: f.EpisodeNo, DurSec: float64(f.DurationMs) / 1000}
	idx, err := w.audioIndex(ctx, f)
	if err != nil {
		return e, err
	}
	e.Head, err = w.cachedFingerprint(ctx, f, fmt.Sprintf("head-%d", idx), idx, 0, HeadSec(e.DurSec))
	return e, err
}

// audioIndex is the track to fingerprint (audioTrack of f's cached probe).
func (w *Worker) audioIndex(ctx context.Context, f file) (int, error) {
	js, err := w.Store.probeOf(ctx, f.ID)
	if err != nil {
		return -1, err
	}
	var p media.ProbeResult
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		return -1, err
	}
	idx := audioTrack(p)
	if idx < 0 {
		return -1, errors.New("no audio track")
	}
	return idx, nil
}

// episode reads what the analysis needs of one file (from the cache when
// there): the fingerprints of its head and tail, and the black runs of the
// tail (keyframes only).
func (w *Worker) episode(ctx context.Context, f file) (Episode, error) {
	e := Episode{File: f.ID, Item: f.Item, Number: f.EpisodeNo, DurSec: float64(f.DurationMs) / 1000}
	idx, err := w.audioIndex(ctx, f)
	if err != nil {
		return e, err
	}
	if e.Head, err = w.cachedFingerprint(ctx, f, fmt.Sprintf("head-%d", idx), idx, 0, HeadSec(e.DurSec)); err != nil {
		return e, err
	}
	tail := TailSec(e.DurSec, false)
	e.TailAt = e.DurSec - tail
	if e.Tail, err = w.cachedFingerprint(ctx, f, fmt.Sprintf("tail-%d", idx), idx, e.TailAt, tail); err != nil {
		return e, err
	}
	if e.Black, err = w.cachedBlack(ctx, f, e.TailAt, tail); err != nil {
		if ctx.Err() != nil {
			return e, ctx.Err()
		}
		w.log().Warn("segments: black frames", "file", f.Path, "err", err)
		e.Black = nil // the credits then come from the audio alone
	}
	return e, nil
}

// recap decodes the picture between the sting and the intro for the black
// run that ends a recap (every frame: a 2 s run falls between keyframes),
// and keeps it only when it holds audio of an earlier episode (a cold open
// is new material).
func (w *Worker) recap(ctx context.Context, eps []Episode, f file, stingEnd float64, intro Span) (Span, bool) {
	if intro.Start-stingEnd < recapBlackSec || !EarlierClip(eps, f.ID, stingEnd, intro.Start) {
		return Span{}, false
	}
	if err := w.idle(ctx); err != nil {
		return Span{}, false
	}
	bl, err := w.black(ctx, f.Path, stingEnd, intro.Start-stingEnd+1, recapBlackSec, false)
	if err != nil {
		if ctx.Err() == nil {
			w.log().Warn("segments: recap black frames", "file", f.Path, "err", err)
		}
		return Span{}, false
	}
	r, ok := RecapAfter(stingEnd, intro, bl)
	if !ok || !EarlierClip(eps, f.ID, stingEnd, r.End) {
		return Span{}, false
	}
	return r, true
}

// ---------- the fingerprint cache ----------

// cacheBase is the name stem of f's cached reads: its path, size and
// mtime (a replaced file is read again) and FingerprintVersion.
func (w *Worker) cacheBase(f file) string {
	if w.CacheDir == "" {
		return ""
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|v%d", f.Path, f.Size, f.Mtime, FingerprintVersion)))
	return hex.EncodeToString(h[:16])
}

func (w *Worker) cachePath(f file, part string) string {
	b := w.cacheBase(f)
	if b == "" {
		return ""
	}
	return filepath.Join(w.CacheDir, b+"."+part)
}

func writeAtomic(p string, b []byte) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, p)
	}
}

func (w *Worker) cachedFingerprint(ctx context.Context, f file, part string, idx int, from, dur float64) ([]uint32, error) {
	p := w.cachePath(f, part)
	if p != "" {
		if b, err := os.ReadFile(p); err == nil && len(b)%4 == 0 {
			out := make([]uint32, len(b)/4)
			for i := range out {
				out[i] = binary.LittleEndian.Uint32(b[i*4:])
			}
			return out, nil
		}
	}
	if err := w.idle(ctx); err != nil {
		return nil, err
	}
	fp, err := w.fingerprint(ctx, f.Path, idx, from, dur)
	if err != nil {
		return nil, err
	}
	if p != "" {
		b := make([]byte, 4*len(fp))
		for i, x := range fp {
			binary.LittleEndian.PutUint32(b[i*4:], x)
		}
		writeAtomic(p, b)
	}
	return fp, nil
}

func (w *Worker) cachedBlack(ctx context.Context, f file, from, dur float64) ([]Span, error) {
	p := w.cachePath(f, fmt.Sprintf("black-%d", int(dur)))
	if p != "" {
		if b, err := os.ReadFile(p); err == nil {
			var out []Span
			if json.Unmarshal(b, &out) == nil {
				return out, nil
			}
		}
	}
	if err := w.idle(ctx); err != nil {
		return nil, err
	}
	bl, err := w.black(ctx, f.Path, from, dur, 1, true)
	if err != nil {
		return nil, err
	}
	if p != "" {
		if b, err := json.Marshal(bl); err == nil {
			writeAtomic(p, b)
		}
	}
	return bl, nil
}

// pruneCache deletes the cached reads of files no longer in the library
// (or since replaced).
func (w *Worker) pruneCache(files []file) {
	if w.CacheDir == "" {
		return
	}
	keep := map[string]bool{}
	for _, f := range files {
		keep[w.cacheBase(f)] = true
	}
	ents, err := os.ReadDir(w.CacheDir)
	if err != nil {
		return
	}
	for _, e := range ents {
		base, _, _ := strings.Cut(e.Name(), ".")
		if !keep[base] {
			os.Remove(filepath.Join(w.CacheDir, e.Name()))
		}
	}
}
