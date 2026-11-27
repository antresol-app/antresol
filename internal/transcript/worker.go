package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/bgtask"
	"github.com/antresol-app/antresol/internal/library"
)

const (
	// pieceMs is a piece of a file sent to the service; shortPieceMs one
	// for a model that gives no segment times (the piece's are used), or
	// whose answer was cut short.
	pieceMs      = 10 * 60_000
	shortPieceMs = 30_000
	// tailMs: less than this left of a file joins the piece before it.
	tailMs = 30_000
	// emptyPiece: a cut under this holds no sound (a file shorter than its
	// stated length): nothing to ask about.
	emptyPiece = 1024

	// maxFails: a book that failed this many times in a row is given up on
	// (until the settings change).
	maxFails  = 6
	retryBase = time.Hour
	retryMax  = 7 * 24 * time.Hour
	// providerTries: a book the service fails on this many times in a row,
	// while it may be the service's own trouble, is set aside so that the
	// others go on.
	providerTries = 5

	// how long a service that fails is left alone: doubling from the first
	// to the second
	sttHoldFirst, sttHoldMax = time.Minute, time.Hour
	llmHoldFirst, llmHoldMax = 5 * time.Minute, 6 * time.Hour
	// limitMax bounds a rate limit's «retry after».
	limitMax = 24 * time.Hour

	startDelay = 5 * time.Minute
	idleEvery  = time.Hour
	// busyRetry: a pass that stopped for playback runs again this soon.
	busyRetry = time.Minute
	// missingKeep: the transcript of a book missing this long (an unmounted
	// disk comes back sooner) is removed.
	missingKeep = 7 * 24 * time.Hour
)

// ChapterTimeout is how long a text model may take over an hour of speech
// (the server sets it on the model).
const ChapterTimeout = 5 * time.Minute

var (
	errBusy = errors.New("transcript: held for playback")
	// errHeld: the speech service is left alone for a while.
	errHeld = errors.New("transcript: the service is held")
)

// hold leaves a service alone until a time.
type hold struct {
	until  time.Time
	fails  int
	reason string
}

func (h *hold) active(now time.Time) bool { return now.Before(h.until) }

func (h *hold) backoff(now time.Time, first, most time.Duration, reason string) {
	d := first << min(h.fails, 20)
	if d > most || d <= 0 {
		d = most
	}
	h.fails++
	h.until, h.reason = now.Add(d), reason
}

// Worker makes the transcripts and automatic chapters of the library's
// audiobooks in the background: the books being listened to first, then
// the newest; a piece of about ten minutes at a time, each kept as soon as
// it is done, so that a restart redoes none. It stops while playback needs
// the disk, waits as long as a rate limit asks, backs off from a service
// that fails, and retries a book that failed after an hour, doubling to a
// week. A transcript is made once for a book's files and removed when they
// change or the book goes.
type Worker struct {
	DB     *sql.DB
	FFmpeg string
	// STT is the speech-to-text service asked now; false: the feature is
	// off.
	STT func(ctx context.Context) (Config, bool)
	// LLM is the text model that splits a transcript into chapters now (nil,
	// or answering nil: none).
	LLM func(ctx context.Context) Asker
	// HTTP asks the speech service (nil: the default client).
	HTTP *http.Client
	// Busy: playback needs the media disk (nil: never).
	Busy func() bool
	// Disk: false while the media disk is away (nil: always there).
	Disk func() bool
	Log  *slog.Logger
	Now  func() time.Time
	// Idle: how often a pass runs unprompted (an hour when 0); StartDelay:
	// the first pass after Run starts (5 minutes when 0).
	Idle, StartDelay time.Duration
	// Cut overrides the ffmpeg read of a piece (tests).
	Cut func(ctx context.Context, path string, startMs, durMs int64) ([]byte, error)

	// Progress: the books finished of those there are («Задачи»).
	Progress bgtask.Tracker

	once sync.Once
	kick chan struct{}

	mu               sync.Mutex
	sttHold, llmHold hold
	// service: the speech service the holds and what is known of it are about
	service Config
	// untimed: its model gives no segment times
	untimed bool
	// strikes: the service's errors in a row on a book (speech, text)
	strikes, llmStrikes map[int64]int
	// yielded: the last pass stopped for playback
	yielded bool
}

func (w *Worker) init() {
	w.once.Do(func() {
		w.kick = make(chan struct{}, 1)
		w.strikes, w.llmStrikes = map[int64]int{}, map[int64]int{}
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

func (w *Worker) busy() bool   { return w.Busy != nil && w.Busy() }
func (w *Worker) diskOK() bool { return w.Disk == nil || w.Disk() }

// Kick asks for a pass soon (a scan added books); never blocks.
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

// Reconfigured tells the worker that the services it asks were changed:
// what failed is tried again now.
func (w *Worker) Reconfigured(ctx context.Context) error {
	w.init()
	w.mu.Lock()
	w.sttHold, w.llmHold, w.untimed = hold{}, hold{}, false
	clear(w.strikes)
	clear(w.llmStrikes)
	w.mu.Unlock()
	if w.DB == nil {
		return nil
	}
	if _, err := w.DB.ExecContext(ctx, `UPDATE transcripts SET state='pending', fails=0, retry_at=NULL, error='', piece_ms=0 WHERE state<>'ready'`); err != nil {
		return err
	}
	_, err := w.DB.ExecContext(ctx, `UPDATE transcripts SET chapters_fails=0, chapters_retry=NULL, chapters_error='' WHERE chapters_done=0`)
	w.Kick()
	return err
}

// Run passes over the books until ctx is done: StartDelay after the start
// (a Kick before then waits for it too), after each Kick and every Idle
// (sooner when a service's wait ends or playback stopped a pass).
func (w *Worker) Run(ctx context.Context) {
	w.init()
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
			w.log().Warn("transcript: pass", "err", err)
		}
		wait = w.wait()
	}
}

// wait is how long until the next unprompted pass.
func (w *Worker) wait() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.yielded {
		return busyRetry
	}
	d := w.Idle
	if d <= 0 {
		d = idleEvery
	}
	now := w.now()
	for _, h := range []hold{w.sttHold, w.llmHold} {
		if h.active(now) {
			d = min(d, h.until.Sub(now))
		}
	}
	return d
}

// speech is the service to ask now; a change of it ends what was held
// against the one before.
func (w *Worker) speech(ctx context.Context) (Config, bool) {
	if w.STT == nil {
		return Config{}, false
	}
	c, on := w.STT(ctx)
	if on {
		w.mu.Lock()
		if c != w.service {
			w.service, w.sttHold, w.untimed = c, hold{}, false
			clear(w.strikes)
		}
		w.mu.Unlock()
	}
	return c, on
}

func (w *Worker) held(h *hold) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return h.active(w.now())
}

// row is a stored transcript as a pass reads it.
type row struct {
	signature, state, language string
	pieceMs                    int64
	fails, chaptersFails       int
	retry, chaptersRetry       sql.NullString
	chaptersDone               bool
}

type bookFile struct {
	File
	path string
}

// book is an audiobook with what is stored for it.
type book struct {
	id    int64
	title string
	// files are the visible ones, in playback order
	files []bookFile
	sig   string
	// complete: every file's length is known (the probe pass reads them), so
	// the book has a timeline
	complete bool
	// realTitles: the files' own chapter titles say something
	realTitles bool
	row        *row
}

func (b *book) ready() bool {
	return b.row != nil && b.row.signature == b.sig && b.row.state == StateReady
}

func (b *book) wantsChapters() bool { return b.ready() && !b.row.chaptersDone && !b.realTitles }

func (b *book) transcriptDue(now string) bool {
	return b.row == nil || b.row.state == StatePending || b.row.state == StateFailed && b.row.retry.Valid && b.row.retry.String <= now
}

func (b *book) chaptersDue(now string) bool {
	return b.wantsChapters() && (b.row.chaptersFails == 0 || b.row.chaptersRetry.Valid && b.row.chaptersRetry.String <= now)
}

// Pass drops what is stale, then makes the transcripts and chapters due.
func (w *Worker) Pass(ctx context.Context) error {
	w.init()
	w.mu.Lock()
	w.yielded = false
	w.mu.Unlock()
	if w.DB == nil {
		return nil
	}
	if !w.diskOK() {
		w.Progress.SetState(bgtask.StateOffline)
		return nil
	}
	if err := w.collect(ctx); err != nil {
		return err
	}
	_, on := w.speech(ctx)
	llm := w.model(ctx)
	books, err := w.books(ctx)
	if err != nil {
		return err
	}
	finished := func(b *book) bool { return b.ready() && (llm == nil || !b.wantsChapters()) }
	done, waiting := 0, false
	for _, b := range books {
		if finished(b) {
			done++
		}
		waiting = waiting || llm != nil && b.wantsChapters()
	}
	if !on && !waiting {
		w.Progress.SetError("")
		w.Progress.SetState(bgtask.StateOff)
		return nil
	}
	w.Progress.Begin(done, len(books))
	defer func() {
		w.report(context.WithoutCancel(ctx), on, llm != nil)
		w.Progress.End(w.now())
	}()
	for _, b := range books {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !w.diskOK() {
			return nil
		}
		// stored for other files (some away for now): left alone
		if !b.complete || b.row != nil && b.row.signature != b.sig {
			continue
		}
		was := finished(b)
		if on && !b.ready() && b.transcriptDue(stamp(w.now())) && !w.held(&w.sttHold) {
			err := w.transcribe(ctx, b)
			switch {
			case errors.Is(err, errBusy):
				w.mu.Lock()
				w.yielded = true
				w.mu.Unlock()
				return nil
			case errors.Is(err, errHeld):
			case errors.Is(err, exec.ErrNotFound):
				w.log().Warn("transcript: no ffmpeg, no transcripts", "err", err)
				return nil
			case err != nil:
				return err
			}
		}
		if llm != nil && b.chaptersDue(stamp(w.now())) && !w.held(&w.llmHold) {
			// asked again for every book: a long pass follows the settings
			if m := w.model(ctx); m != nil {
				if err := w.chapters(ctx, m, b); err != nil {
					return err
				}
			}
		}
		if !was && finished(b) {
			w.Progress.Add(1)
		}
	}
	return nil
}

// model is the text model to ask now (nil: none).
func (w *Worker) model(ctx context.Context) Asker {
	if w.LLM == nil {
		return nil
	}
	return w.LLM(ctx)
}

// report says on «Задачи» why the worker waits, or what failed last.
func (w *Worker) report(ctx context.Context, speechOn, textOn bool) {
	w.mu.Lock()
	now, reason := w.now(), ""
	switch {
	case speechOn && w.sttHold.active(now):
		reason = w.sttHold.reason
	case textOn && w.llmHold.active(now):
		reason = w.llmHold.reason
	}
	w.mu.Unlock()
	if reason != "" {
		w.Progress.SetState(bgtask.StateWaiting)
		w.Progress.SetError(reason)
		return
	}
	var title, why string
	err := w.DB.QueryRowContext(ctx, `SELECT i.title, CASE WHEN t.state='failed' THEN t.error ELSE t.chapters_error END
		FROM transcripts t JOIN items i ON i.id=t.item_id
		WHERE i.missing_since IS NULL AND (t.state='failed' OR t.chapters_done=0 AND t.chapters_error<>'')
		ORDER BY t.updated_at DESC, t.item_id DESC LIMIT 1`).Scan(&title, &why)
	switch {
	case err == nil:
		w.Progress.SetError("«" + title + "»: " + why)
	case errors.Is(err, sql.ErrNoRows):
		w.Progress.SetError("")
	default:
		w.log().Warn("transcript: last failure", "err", err)
	}
}

// collect removes the transcripts of books whose files changed, and of
// those missing for a week. A book with some files away keeps its own that
// long too.
func (w *Worker) collect(ctx context.Context) error {
	cutoff := stamp(w.now().Add(-missingKeep))
	rows, err := w.DB.QueryContext(ctx, `SELECT t.item_id, t.signature, COALESCE(i.missing_since,''),
		EXISTS(SELECT 1 FROM files f WHERE f.item_id=t.item_id AND f.missing_since>?)
		FROM transcripts t JOIN items i ON i.id=t.item_id`, cutoff)
	if err != nil {
		return err
	}
	type stored struct {
		id           int64
		sig, missing string
		away         bool
	}
	var list []stored
	for rows.Next() {
		var s stored
		if err := rows.Scan(&s.id, &s.sig, &s.missing, &s.away); err != nil {
			rows.Close()
			return err
		}
		list = append(list, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(list) == 0 {
		return err
	}
	files, err := w.files(ctx)
	if err != nil {
		return err
	}
	for _, s := range list {
		var now []File
		for _, f := range files[s.id] {
			now = append(now, File{ID: f.ID, Size: f.Size, Mtime: f.Mtime})
		}
		sig := Signature(now)
		var gone bool
		switch {
		case s.missing != "":
			gone = s.missing <= cutoff
		case sig == s.sig:
		default:
			gone = !(s.away && partOf(sig, s.sig))
		}
		if !gone {
			continue
		}
		// the pieces and chapters go with it
		if _, err := w.DB.ExecContext(ctx, `DELETE FROM transcripts WHERE item_id=?`, s.id); err != nil {
			return err
		}
	}
	return nil
}

// files are the visible files of the audiobooks, by book.
func (w *Worker) files(ctx context.Context) (map[int64][]library.File, error) {
	rows, err := w.DB.QueryContext(ctx, `SELECT f.id, f.item_id, f.path, f.size, f.mtime, COALESCE(f.duration_ms,0), COALESCE(f.probe_json,'')
		FROM files f JOIN items i ON i.id=f.item_id WHERE i.kind='audiobook' AND f.missing_since IS NULL ORDER BY f.item_id, f.path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]library.File{}
	for rows.Next() {
		var f library.File
		if err := rows.Scan(&f.ID, &f.ItemID, &f.Path, &f.Size, &f.Mtime, &f.DurationMs, &f.ProbeJSON); err != nil {
			return nil, err
		}
		out[f.ItemID] = append(out[f.ItemID], f)
	}
	return out, rows.Err()
}

// books lists the visible audiobooks in the order they are worked on: those
// being listened to (latest first), then the newest.
func (w *Worker) books(ctx context.Context) ([]*book, error) {
	files, err := w.files(ctx)
	if err != nil {
		return nil, err
	}
	titles := map[int64][]string{}
	crow, err := w.DB.QueryContext(ctx, `SELECT c.item_id, c.title FROM chapters c JOIN items i ON i.id=c.item_id
		WHERE i.kind='audiobook' ORDER BY c.item_id, c.idx`)
	if err != nil {
		return nil, err
	}
	for crow.Next() {
		var id int64
		var t string
		if err := crow.Scan(&id, &t); err != nil {
			crow.Close()
			return nil, err
		}
		titles[id] = append(titles[id], t)
	}
	crow.Close()
	if err := crow.Err(); err != nil {
		return nil, err
	}
	stored := map[int64]*row{}
	srow, err := w.DB.QueryContext(ctx, `SELECT item_id, signature, state, language, piece_ms, fails, retry_at, chapters_done, chapters_fails, chapters_retry FROM transcripts`)
	if err != nil {
		return nil, err
	}
	for srow.Next() {
		var id int64
		r := &row{}
		if err := srow.Scan(&id, &r.signature, &r.state, &r.language, &r.pieceMs, &r.fails, &r.retry, &r.chaptersDone, &r.chaptersFails, &r.chaptersRetry); err != nil {
			srow.Close()
			return nil, err
		}
		stored[id] = r
	}
	srow.Close()
	if err := srow.Err(); err != nil {
		return nil, err
	}
	rows, err := w.DB.QueryContext(ctx, `SELECT i.id, i.title, COALESCE(i.meta_json,''),
		COALESCE((SELECT MAX(p.updated_at) FROM progress p WHERE p.item_id=i.id AND p.finished=0),'') AS listened
		FROM items i WHERE i.kind='audiobook' AND i.missing_since IS NULL
		ORDER BY listened DESC, i.added_at DESC, i.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*book
	for rows.Next() {
		var meta, listened string
		b := &book{}
		if err := rows.Scan(&b.id, &b.title, &meta, &listened); err != nil {
			return nil, err
		}
		var m struct {
			FilesOrder []int64 `json:"files_order"`
		}
		_ = json.Unmarshal([]byte(meta), &m) // no order: by path
		list := library.OrderFiles(files[b.id], m.FilesOrder)
		if len(list) == 0 {
			continue
		}
		b.complete = true
		var sig []File
		var stems []string
		for _, f := range list {
			bf := bookFile{File{ID: f.ID, Size: f.Size, Mtime: f.Mtime, DurationMs: f.Length()}, f.Path}
			b.complete = b.complete && bf.DurationMs > 0
			b.files, sig = append(b.files, bf), append(sig, bf.File)
			name := filepath.Base(f.Path)
			stems = append(stems, strings.TrimSuffix(name, filepath.Ext(name)))
		}
		b.sig = Signature(sig)
		b.realTitles = library.RealChapterTitles(b.title, titles[b.id], stems)
		b.row = stored[b.id]
		out = append(out, b)
	}
	return out, rows.Err()
}

// covered is how far into a file the finished pieces reach.
func (w *Worker) covered(ctx context.Context, item, file int64) (int64, error) {
	rows, err := w.DB.QueryContext(ctx, `SELECT start_ms, end_ms FROM transcript_pieces WHERE item_id=? AND file_id=? ORDER BY start_ms`, item, file)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var at int64
	for rows.Next() {
		var start, end int64
		if err := rows.Scan(&start, &end); err != nil {
			return 0, err
		}
		if start <= at {
			at = max(at, end)
		}
	}
	return at, rows.Err()
}

// transcribe makes the pieces a book lacks, one after another: errBusy
// when playback needs the disk, errHeld when the service is to be left
// alone. A book's own failure is stored, not returned.
func (w *Worker) transcribe(ctx context.Context, b *book) error {
	if b.row == nil {
		if _, err := w.DB.ExecContext(ctx, `INSERT INTO transcripts(item_id, signature, state, updated_at) VALUES(?,?,?,?)
			ON CONFLICT(item_id) DO NOTHING`, b.id, b.sig, StatePending, stamp(w.now())); err != nil {
			return err
		}
		b.row = &row{signature: b.sig, state: StatePending}
	}
	var total, before int64
	for _, f := range b.files {
		total += f.DurationMs
	}
	for _, f := range b.files {
		for {
			at, err := w.covered(ctx, b.id, f.ID)
			if err != nil {
				return err
			}
			if at >= f.DurationMs {
				break
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if w.busy() {
				return errBusy
			}
			// read at every piece: a change of the settings takes effect now
			cfg, on := w.speech(ctx)
			if !on {
				return nil
			}
			size := int64(pieceMs)
			w.mu.Lock()
			if b.row.pieceMs > 0 {
				size = b.row.pieceMs
			} else if w.untimed {
				size = shortPieceMs
			}
			w.mu.Unlock()
			end := at + size
			if f.DurationMs-end < min(tailMs, size/2) {
				end = f.DurationMs
			}
			if _, err := os.Stat(f.path); err != nil {
				return nil // not on disk (an unmounted drive): left for later
			}
			w.Progress.SetNow(b.title, int((before+at)*100/total))
			data, err := w.cut(ctx, f.path, at, end-at)
			if err != nil {
				if ctx.Err() != nil || errors.Is(err, exec.ErrNotFound) {
					return err
				}
				if !w.diskOK() {
					return nil // the disk went away under the read
				}
				return w.failed(ctx, b, err, true)
			}
			res := Result{Timed: true}
			if len(data) >= emptyPiece {
				client := &Client{Config: cfg, HTTP: w.HTTP}
				res, err = client.Transcribe(ctx, Audio{Data: data, Name: "piece.mp3", MIME: "audio/mp3", DurationMs: end - at})
				if err != nil {
					again, err := w.refused(ctx, b, size, err)
					if again {
						continue
					}
					return err
				}
			}
			if !res.Timed && end-at > shortPieceMs*3/2 {
				// no times of its own: short pieces, whose times will do
				if cfg.Provider != ProviderGemini { // a Whisper model never gives them
					w.mu.Lock()
					w.untimed = true
					w.mu.Unlock()
				}
				if err := w.shorten(ctx, b); err != nil {
					return err
				}
				continue
			}
			if err := w.savePiece(ctx, b, f.ID, at, end, res); err != nil {
				return err
			}
		}
		before += f.DurationMs
	}
	return w.complete(ctx, b)
}

func (w *Worker) cut(ctx context.Context, path string, startMs, durMs int64) ([]byte, error) {
	if w.Cut != nil {
		return w.Cut(ctx, path, startMs, durMs)
	}
	ff := w.FFmpeg
	if ff == "" {
		ff = "ffmpeg"
	}
	return cutPiece(ctx, ff, path, startMs, durMs, w.Busy)
}

// shorten cuts the rest of a book short.
func (w *Worker) shorten(ctx context.Context, b *book) error {
	b.row.pieceMs = shortPieceMs
	_, err := w.DB.ExecContext(ctx, `UPDATE transcripts SET piece_ms=? WHERE item_id=?`, shortPieceMs, b.id)
	return err
}

// refused acts on a request that failed: again, when the piece is to be
// tried shorter.
func (w *Worker) refused(ctx context.Context, b *book, size int64, cause error) (again bool, err error) {
	var e *Error
	if !errors.As(cause, &e) {
		return false, cause // ctx's end
	}
	if e.Kind == KindInput {
		if e.Shorter && size > shortPieceMs {
			return true, w.shorten(ctx, b)
		}
		return false, w.failed(ctx, b, e, true)
	}
	w.log().Warn("transcript: the speech service", "book", b.title, "err", e)
	w.mu.Lock()
	now := w.now()
	if e.Kind == KindProvider && e.Status >= 500 {
		// perhaps this piece alone: the others get their turn at last
		if w.strikes[b.id]++; w.strikes[b.id] >= providerTries {
			delete(w.strikes, b.id)
			w.sttHold = hold{}
			w.mu.Unlock()
			return false, w.failed(ctx, b, e, false)
		}
	}
	if e.Kind == KindRateLimit && e.RetryAfter > 0 {
		w.sttHold.until, w.sttHold.reason = now.Add(min(e.RetryAfter+time.Second, limitMax)), e.Error()
	} else {
		w.sttHold.backoff(now, sttHoldFirst, sttHoldMax, e.Error())
	}
	w.mu.Unlock()
	return false, errHeld
}

// retryAt is when something that failed fails times is tried again.
func (w *Worker) retryAt(fails int) string {
	d := retryBase << min(fails-1, 20)
	if d > retryMax || d <= 0 {
		d = retryMax
	}
	return stamp(w.now().Add(d))
}

// failed stores a book's failure: retried after an hour, doubling to a
// week; with giveUp, given up on after maxFails.
func (w *Worker) failed(ctx context.Context, b *book, cause error, giveUp bool) error {
	fails := b.row.fails + 1
	if !giveUp {
		fails = min(fails, maxFails-1)
	}
	var retry any
	if fails < maxFails {
		retry = w.retryAt(fails)
	}
	w.log().Warn("transcript: book failed", "book", b.title, "fails", fails, "err", cause)
	b.row.state, b.row.fails = StateFailed, fails
	_, err := w.DB.ExecContext(ctx, `UPDATE transcripts SET state=?, fails=?, retry_at=?, error=?, updated_at=? WHERE item_id=?`,
		StateFailed, fails, retry, cause.Error(), stamp(w.now()), b.id)
	return err
}

// savePiece keeps a finished piece: its segments placed within the file.
func (w *Worker) savePiece(ctx context.Context, b *book, file, start, end int64, res Result) error {
	segs := make([]Segment, len(res.Segments))
	for i, s := range res.Segments {
		segs[i] = Segment{start + s.StartMs, start + s.EndMs, s.Text}
	}
	raw, err := json.Marshal(segs)
	if err != nil {
		return err
	}
	tx, err := w.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO transcript_pieces(item_id, file_id, start_ms, end_ms, language, segments)
		VALUES(?,?,?,?,?,?)`, b.id, file, start, end, res.Language, string(raw)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE transcripts SET state=?, fails=0, retry_at=NULL, error='', updated_at=? WHERE item_id=?`,
		StatePending, stamp(w.now()), b.id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	b.row.state, b.row.fails = StatePending, 0
	w.mu.Lock()
	w.sttHold = hold{}
	delete(w.strikes, b.id)
	w.mu.Unlock()
	return nil
}

// complete marks a transcript whose every piece is done as ready, in the
// language most of its speech is in.
func (w *Worker) complete(ctx context.Context, b *book) error {
	var lang string
	err := w.DB.QueryRowContext(ctx, `SELECT language FROM transcript_pieces WHERE item_id=? AND language<>''
		GROUP BY language ORDER BY SUM(length(segments)) DESC, language LIMIT 1`, b.id).Scan(&lang)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := w.DB.ExecContext(ctx, `UPDATE transcripts SET state=?, language=?, fails=0, retry_at=NULL, error='', updated_at=? WHERE item_id=?`,
		StateReady, lang, stamp(w.now()), b.id); err != nil {
		return err
	}
	b.row.state, b.row.language = StateReady, lang
	w.log().Info("transcript: ready", "book", b.title)
	return nil
}

// chapters splits a ready transcript into chapters. A failure is stored or
// holds the model; only the database's errors are returned.
func (w *Worker) chapters(ctx context.Context, llm Asker, b *book) error {
	segs, err := Segments(ctx, w.DB, b.id)
	if err != nil {
		return err
	}
	offsets := map[int64]int64{}
	var at int64
	for _, f := range b.files {
		offsets[f.ID] = at
		at += f.DurationMs
	}
	w.Progress.SetNow(b.title, 100)
	chs, cause := splitChapters(ctx, llm, b.title, b.row.language, onTimeline(segs, offsets))
	if cause == nil {
		if err := saveChapters(ctx, w.DB, b.id, chs, w.now()); err != nil {
			return err
		}
		b.row.chaptersDone = true
		w.mu.Lock()
		w.llmHold = hold{}
		delete(w.llmStrikes, b.id)
		w.mu.Unlock()
		w.log().Info("transcript: chapters made", "book", b.title, "chapters", len(chs))
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.log().Warn("transcript: chapters", "book", b.title, "err", cause)
	// a bad answer counts against the book; a model that cannot be asked is
	// held, unless it keeps failing on this book alone
	bad := errors.Is(cause, errBadAnswer)
	if !bad {
		w.mu.Lock()
		if w.llmStrikes[b.id]++; w.llmStrikes[b.id] < providerTries {
			w.llmHold.backoff(w.now(), llmHoldFirst, llmHoldMax, cause.Error())
			w.mu.Unlock()
			return nil
		}
		delete(w.llmStrikes, b.id)
		w.llmHold = hold{}
		w.mu.Unlock()
	}
	fails := b.row.chaptersFails + 1
	if !bad {
		fails = min(fails, maxFails-1)
	}
	var retry any
	if fails < maxFails {
		retry = w.retryAt(fails)
	}
	b.row.chaptersFails = fails
	_, err = w.DB.ExecContext(ctx, `UPDATE transcripts SET chapters_fails=?, chapters_retry=?, chapters_error=?, updated_at=? WHERE item_id=?`,
		fails, retry, fmt.Sprint(cause), stamp(w.now()), b.id)
	return err
}
