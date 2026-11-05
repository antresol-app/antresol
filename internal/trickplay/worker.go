package trickplay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	// frameRunLimit: one frame's ffmpeg is killed after this much RUNNING
	// time (held stopped for playback does not count).
	frameRunLimit = 2 * time.Minute
	// linearRunLimit bounds the one-pass read of a file without an index.
	linearRunLimit = 20 * time.Minute
	yieldEvery     = 250 * time.Millisecond
	retryBase      = time.Hour
	retryMax       = 7 * 24 * time.Hour
	// pruneEvery: previews of files gone from the library are swept this
	// often, between files of a long pass too
	pruneEvery = time.Hour
	// wantTTL: a play's request is forgotten after this (the file may be
	// one the queue never takes)
	wantTTL = 24 * time.Hour
	// viewedEvery: a view marks the previews as used at most this often
	viewedEvery = time.Hour
	// DefaultBudget is «Место для превью» when unset.
	DefaultBudget = 10 << 30
)

// Worker makes the scrub previews of the library's films and episodes in
// the background, one file at a time: those someone is watching first
// (a play or a scrub here, then a started and unfinished progress), then
// the newest. Every frame waits until playback has been quiet (Busy) and
// its ffmpeg is held stopped while playback reads the disk
// (media.RunYieldingLimit), at the lowest CPU and I/O priority. Files
// shorter than MinDuration get none; the parts of a film in parts and the
// other versions of a title only when asked for (Want). A file whose
// previews fail is retried after an hour, doubling to a week.
//
// The previews keep within a budget: the background stops there, and a
// file asked for makes room by deleting the least recently viewed. Those
// of files no longer in the library (or replaced) are deleted; turned off,
// all of them.
type Worker struct {
	DB *sql.DB
	// Dir keeps the previews (Store); "" makes none.
	Dir    string
	FFmpeg string
	// Busy: something plays (nil: never).
	Busy func() bool
	// Settings is «Превью при перемотке» and «Место для превью» (bytes);
	// nil: on, DefaultBudget. Read before each file.
	Settings func(ctx context.Context) (on bool, budget int64)
	// KeyIndex reads a file's keyframe index (media.KeyIndexCache.Get):
	// a file with none (Matroska without Cues) is read in one pass instead
	// of a seek per frame. nil: every file is seekable.
	KeyIndex func(path string) error
	Log      *slog.Logger
	Now      func() time.Time
	// Idle: how often a pass runs unprompted (6 h when 0).
	Idle time.Duration
	// Frame overrides the ffmpeg read of one frame and Linear the one-pass
	// read (tests).
	Frame  func(ctx context.Context, path string, atSec float64, w, h int) ([]byte, error)
	Linear func(ctx context.Context, dir, key string, durSec float64, w, h int, path string) (Index, error)

	once     sync.Once
	kick     chan struct{}
	mu       sync.Mutex
	want     map[int64]time.Time // files asked for (a play, a scrub), by when
	prunedAt time.Time
	usage    map[string]used // the previews on disk, by key (Pass)
}

// used is one file's previews on disk.
type used struct {
	bytes  int64
	viewed time.Time // index.json's mtime: made, or last viewed
}

func (w *Worker) init() {
	w.once.Do(func() {
		w.kick = make(chan struct{}, 1)
		w.want = map[int64]time.Time{}
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

func (w *Worker) settings(ctx context.Context) (bool, int64) {
	if w.Settings == nil {
		return true, DefaultBudget
	}
	on, budget := w.Settings(ctx)
	if budget <= 0 {
		budget = DefaultBudget
	}
	return on, budget
}

func (w *Worker) store() Store { return Store{Dir: w.Dir} }

// Index is the stored index of a file's previews (false: none yet). A hit
// marks them viewed (the budget deletes the least recently viewed first).
func (w *Worker) Index(path string, size, mtime int64) (Index, bool) {
	if w == nil {
		return Index{}, false
	}
	ix, ok := w.store().Index(path, size, mtime)
	if ok {
		p := filepath.Join(w.Dir, ix.Key, "index.json")
		if st, err := os.Stat(p); err == nil && w.now().Sub(st.ModTime()) >= viewedEvery {
			now := w.now()
			os.Chtimes(p, now, now)
		}
	}
	return ix, ok
}

// Sheet is the path of sheet n of a file's previews.
func (w *Worker) Sheet(path string, size, mtime int64, n int) (string, bool) {
	if w == nil {
		return "", false
	}
	return w.store().Sheet(path, size, mtime, n)
}

// Kick asks for a pass soon (a scan added files, the settings changed);
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

// Want puts a file someone plays (or scrubs) at the head of the queue (a
// pass that runs takes it next), the parts of a film in parts and other
// versions too; never blocks.
func (w *Worker) Want(fileID int64) {
	if w == nil {
		return
	}
	w.init()
	w.mu.Lock()
	w.want[fileID] = w.now()
	w.mu.Unlock()
	w.Kick()
}

// Run passes over the library until ctx is done: a minute after the start,
// after each Kick and every Idle.
func (w *Worker) Run(ctx context.Context) {
	w.init()
	idle := w.Idle
	if idle <= 0 {
		idle = 6 * time.Hour
	}
	wait := time.Minute
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
		wait = idle
		if err := w.Pass(ctx); err != nil && ctx.Err() == nil {
			w.log().Warn("trickplay: pass", "err", err)
		}
	}
}

// candidate is a library video file as the queue sees it.
type candidate struct {
	ID          int64
	Path        string
	Size, Mtime int64
	DurationMs  int64
	Probe       string
	// Shared: its title has other files (parts, versions): only when asked for
	Shared bool
	wanted bool
}

func (c candidate) key() string { return Key(c.Path, c.Size, c.Mtime) }

// candidates lists the films and episodes on disk long enough for
// previews: those asked for first (the latest first), then the started and
// unfinished ones (latest progress first), then the newest; a file sharing
// its title with others only when asked for. Requests older than wantTTL
// or for files not listed are forgotten.
func (w *Worker) candidates(ctx context.Context) ([]candidate, error) {
	rows, err := w.DB.QueryContext(ctx, `SELECT f.id, f.path, f.size, f.mtime, f.duration_ms, COALESCE(f.probe_json,''),
		  (SELECT COUNT(*) FROM files o WHERE o.item_id=f.item_id AND o.missing_since IS NULL) > 1,
		  (SELECT MAX(p.updated_at) FROM progress p WHERE p.item_id=f.item_id AND p.position_ms>0 AND p.finished=0) AS watching
		FROM files f JOIN items e ON e.id=f.item_id
		WHERE e.kind IN ('movie','episode') AND f.missing_since IS NULL AND e.missing_since IS NULL
		  AND COALESCE(f.duration_ms,0) >= ?
		ORDER BY watching IS NULL, watching DESC, e.added_at DESC, f.id DESC`, MinDuration.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []candidate
	for rows.Next() {
		var c candidate
		var watching sql.NullString
		if err := rows.Scan(&c.ID, &c.Path, &c.Size, &c.Mtime, &c.DurationMs, &c.Probe, &c.Shared, &watching); err != nil {
			return nil, err
		}
		all = append(all, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	listed := make(map[int64]bool, len(all))
	for _, c := range all {
		listed[c.ID] = true
	}
	now := w.now()
	w.mu.Lock()
	wantAt := make(map[int64]time.Time, len(w.want))
	for id, at := range w.want {
		if !listed[id] || now.Sub(at) >= wantTTL {
			delete(w.want, id)
			continue
		}
		wantAt[id] = at
	}
	w.mu.Unlock()
	out := all[:0]
	for _, c := range all {
		_, c.wanted = wantAt[c.ID]
		if c.Shared && !c.wanted {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := wantAt[out[i].ID], wantAt[out[j].ID]
		if ai.IsZero() != aj.IsZero() {
			return !ai.IsZero()
		}
		return ai.After(aj)
	})
	return out, nil
}

func (w *Worker) forget(id int64) {
	w.mu.Lock()
	delete(w.want, id)
	w.mu.Unlock()
}

// failure is a file's last failed attempt (Dir/<key>.fail).
type failure struct {
	Fails int   `json:"fails"`
	Retry int64 `json:"retry"` // unix seconds
}

func (w *Worker) failPath(key string) string { return filepath.Join(w.Dir, key+".fail") }

func (w *Worker) failureOf(key string) (failure, bool) {
	b, err := os.ReadFile(w.failPath(key))
	if err != nil {
		return failure{}, false
	}
	var f failure
	return f, json.Unmarshal(b, &f) == nil
}

func (w *Worker) failed(key string, permanent bool) {
	f, _ := w.failureOf(key)
	f.Fails++
	d := retryBase << min(f.Fails-1, 20)
	if permanent || d > retryMax || d <= 0 {
		d = retryMax
	}
	f.Retry = w.now().Add(d).Unix()
	if b, err := json.Marshal(f); err == nil {
		os.WriteFile(w.failPath(key), b, 0o644)
	}
}

// due says whether c still needs previews now.
func (w *Worker) due(c candidate) bool {
	k := c.key()
	if _, ok := w.usage[k]; ok {
		return false
	}
	if f, ok := w.failureOf(k); ok && w.now().Unix() < f.Retry {
		return false
	}
	return true
}

// Pass makes the previews of every file still without them, one at a
// time, the list read again after each file (a request meanwhile goes
// next), within the budget; and deletes those of files gone from the
// library (between files too). Turned off, it deletes them all.
func (w *Worker) Pass(ctx context.Context) error {
	w.init()
	if w.Dir == "" || w.DB == nil {
		return nil
	}
	if on, _ := w.settings(ctx); !on {
		w.clear()
		return nil
	}
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return err
	}
	w.sweepTmp()
	w.scanUsage()
	tried := map[int64]bool{}
	full := false // the background stopped at the budget
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		on, budget := w.settings(ctx)
		if !on {
			w.clear()
			return nil
		}
		if w.prunedAt.IsZero() || w.now().Sub(w.prunedAt) >= pruneEvery {
			if err := w.prune(ctx); err != nil {
				return err
			}
			w.prunedAt = w.now()
		}
		list, err := w.candidates(ctx)
		if err != nil {
			return err
		}
		var next *candidate
		for i := range list {
			c := &list[i]
			if tried[c.ID] {
				continue
			}
			if !w.due(*c) {
				if c.wanted {
					w.forget(c.ID)
				}
				continue
			}
			// at the budget only what someone asked for is made
			if !c.wanted && w.total() >= budget {
				if !full {
					w.log().Info("trickplay: the previews have used their space; more only for what is played", "budget", budget)
				}
				full = true
				continue
			}
			next = c
			break
		}
		if next == nil {
			return nil
		}
		tried[next.ID] = true
		if _, err := os.Stat(next.Path); err != nil {
			continue // not on disk (an unmounted drive): left for later
		}
		err = w.file(ctx, *next)
		w.forget(next.ID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, exec.ErrNotFound) {
				w.log().Warn("trickplay: no ffmpeg, no previews", "err", err)
				return nil
			}
			w.log().Warn("trickplay: file", "file", next.Path, "err", err)
			continue
		}
		w.fit(budget, next.key())
	}
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

func (w *Worker) ffmpeg() string {
	if w.FFmpeg != "" {
		return w.FFmpeg
	}
	return "ffmpeg"
}

func (w *Worker) frame(ctx context.Context, path string, at float64, tw, th int) ([]byte, error) {
	if err := w.wait(ctx); err != nil {
		return nil, err
	}
	if w.Frame != nil {
		return w.Frame(ctx, path, at, tw, th)
	}
	return FFmpegFrame(ctx, w.ffmpeg(), path, at, tw, th, func(cmd *exec.Cmd) error {
		return media.RunYieldingLimit(cmd, w.busy, yieldEvery, frameRunLimit)
	})
}

// seekable says whether a file can be read a frame at a time: it has a
// keyframe index, or is not a container that needs one to seek (an AVI's
// or a TS's seek works without one). A slow read of the index is no
// verdict: seekable.
func (w *Worker) seekable(path string) bool {
	if w.KeyIndex == nil {
		return true
	}
	err := w.KeyIndex(path)
	if err == nil || !errors.Is(err, media.ErrNoKeyIndex) {
		return true
	}
	msg := err.Error()
	// keyframes far apart still seek; another container seeks its own way
	return strings.Contains(msg, "not MP4 or Matroska") || strings.Contains(msg, "apart")
}

// file makes one file's previews in Dir/<key>.tmp, renamed to Dir/<key>
// when complete (a reader never sees half of them).
func (w *Worker) file(ctx context.Context, c candidate) error {
	key := c.key()
	var p media.ProbeResult
	if c.Probe != "" {
		json.Unmarshal([]byte(c.Probe), &p)
		if p.Video == nil {
			w.failed(key, true) // no picture: nothing to show
			return nil
		}
	}
	tw, th := TileWidth, DefaultTileHeight
	if p.Video != nil {
		th = TileHeight(p.Video.Width, p.Video.Height)
	}
	tmp := filepath.Join(w.Dir, key+".tmp")
	os.RemoveAll(tmp)
	began := w.now()
	dur := float64(c.DurationMs) / 1000
	if err := w.wait(ctx); err != nil { // the index read touches the disk too
		return err
	}
	var ix Index
	var err error
	var notFound error
	mode := "seek"
	if !w.seekable(c.Path) {
		mode = "linear"
		if w.Linear != nil {
			ix, err = w.Linear(ctx, tmp, key, dur, tw, th, c.Path)
		} else {
			ix, err = GenerateLinear(ctx, tmp, key, dur, tw, th, w.ffmpeg(), c.Path, func(cmd *exec.Cmd) error {
				return media.RunYieldingLimit(cmd, w.busy, yieldEvery, linearRunLimit)
			})
		}
		if errors.Is(err, exec.ErrNotFound) {
			notFound = err
		}
	} else {
		ix, err = Generate(ctx, tmp, key, dur, tw, th, func(ctx context.Context, at float64) ([]byte, error) {
			px, err := w.frame(ctx, c.Path, at, tw, th)
			if errors.Is(err, exec.ErrNotFound) {
				notFound = err
			}
			return px, err
		})
	}
	if err != nil {
		os.RemoveAll(tmp)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if notFound != nil {
			return notFound
		}
		w.failed(key, false)
		return err
	}
	final := filepath.Join(w.Dir, key)
	os.RemoveAll(final)
	if err := os.Rename(tmp, final); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	os.Remove(w.failPath(key))
	if w.usage != nil {
		w.usage[key] = used{bytes: dirBytes(final), viewed: w.now()}
	}
	w.log().Info("trickplay: previews made", "file", c.Path, "frames", ix.Count, "sheets", ix.Sheets, "mode", mode,
		"took", w.now().Sub(began).Round(time.Second))
	return nil
}

func dirBytes(dir string) int64 {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range ents {
		if fi, err := e.Info(); err == nil {
			n += fi.Size()
		}
	}
	return n
}

// scanUsage reads what the previews on disk take (once a pass; file and
// fit keep it current).
func (w *Worker) scanUsage() {
	w.usage = map[string]used{}
	ents, err := os.ReadDir(w.Dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() || strings.Contains(e.Name(), ".") {
			continue
		}
		d := filepath.Join(w.Dir, e.Name())
		st, err := os.Stat(filepath.Join(d, "index.json"))
		if err != nil {
			continue
		}
		w.usage[e.Name()] = used{bytes: dirBytes(d), viewed: st.ModTime()}
	}
}

func (w *Worker) total() int64 {
	var n int64
	for _, u := range w.usage {
		n += u.bytes
	}
	return n
}

// fit deletes the least recently viewed previews (never keep: the file
// just made) until they are within budget.
func (w *Worker) fit(budget int64, keep string) {
	total := w.total()
	if total <= budget {
		return
	}
	keys := make([]string, 0, len(w.usage))
	for k := range w.usage {
		if k != keep {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return w.usage[keys[i]].viewed.Before(w.usage[keys[j]].viewed) })
	for _, k := range keys {
		if total <= budget {
			break
		}
		total -= w.usage[k].bytes
		os.RemoveAll(filepath.Join(w.Dir, k))
		delete(w.usage, k)
	}
}

// clear deletes every preview (turned off: the space comes back).
func (w *Worker) clear() {
	ents, err := os.ReadDir(w.Dir)
	if err != nil || len(ents) == 0 {
		return
	}
	for _, e := range ents {
		os.RemoveAll(filepath.Join(w.Dir, e.Name()))
	}
	w.usage = map[string]used{}
	w.log().Info("trickplay: previews turned off, deleted")
}

// sweepTmp deletes the half-made previews a stop left behind.
func (w *Worker) sweepTmp() {
	ents, err := os.ReadDir(w.Dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			os.RemoveAll(filepath.Join(w.Dir, e.Name()))
		}
	}
}

// prune deletes the previews (and failure notes) of files no longer in
// the library or since replaced. A file marked missing keeps them (an
// unplugged drive would otherwise cost every preview): its row goes when
// the library forgets it, and its previews with it.
func (w *Worker) prune(ctx context.Context) error {
	rows, err := w.DB.QueryContext(ctx, `SELECT path, size, mtime FROM files`)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for rows.Next() {
		var p string
		var size, mtime int64
		if err := rows.Scan(&p, &size, &mtime); err != nil {
			rows.Close()
			return err
		}
		keep[Key(p, size, mtime)] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	ents, err := os.ReadDir(w.Dir)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		base, _, _ := strings.Cut(e.Name(), ".")
		if !keep[base] && !strings.HasSuffix(e.Name(), ".tmp") {
			os.RemoveAll(filepath.Join(w.Dir, e.Name()))
			delete(w.usage, base)
		}
	}
	return nil
}
