package lyrics

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// How long a stored answer stands before the track is asked again.
const (
	// NoneTTL: the service had no words for the track.
	NoneTTL = 90 * 24 * time.Hour
	// PlainTTL: only plain words were found; a synced version may appear.
	PlainTTL = 180 * 24 * time.Hour
	// errorBase doubles with each failure in a row, up to errorMax.
	errorBase = time.Hour
	errorMax  = 7 * 24 * time.Hour
	// pauseBase: the service failing pauses the whole queue; doubles up
	// to pauseMax while it keeps failing.
	pauseBase = 10 * time.Minute
	pauseMax  = 6 * time.Hour
	// failRun failures in a row pause the queue.
	failRun = 3

	batch = 50
)

// never is the next try of synced words found: they are kept for good.
var never = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// Auto fetches the words of the library's tracks from LRCLIB in the
// background and keeps them (library.Repo track_lyrics), so they are there
// when the service is not. Tracks whose file has synced words are skipped.
// New tracks go first (Kick after a scan), then the rest of the library;
// the pace is the Client's Limit (one request a second). Answers stand for
// a long time (none: NoneTTL, plain only: PlainTTL, synced: for good);
// failures are retried per track with backoff, and failRun failures in a
// row pause the queue (10 min, doubling to 6 h), so an outage costs a few
// requests, not a pass over the library.
type Auto struct {
	Client *Client
	Repo   *library.Repo
	// Idle: how often the queue looks for due tracks unprompted (1 h when 0).
	Idle time.Duration
	Now  func() time.Time
	Log  *slog.Logger

	once sync.Once
	kick chan struct{}

	fails    int // failures in a row
	pause    time.Duration
	resumeAt time.Time // paused until
}

func (a *Auto) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Auto) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

func (a *Auto) init() { a.once.Do(func() { a.kick = make(chan struct{}, 1) }) }

// Kick asks for a pass soon (new tracks were scanned); never blocks.
func (a *Auto) Kick() {
	if a == nil {
		return
	}
	a.init()
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// Run works the queue until ctx is done.
func (a *Auto) Run(ctx context.Context) {
	a.init()
	idle := a.Idle
	if idle <= 0 {
		idle = time.Hour
	}
	for {
		wait := idle
		if err := a.Pass(ctx); err != nil && ctx.Err() == nil {
			a.log().Warn("lyrics: pass", "err", err)
		}
		if d := a.resumeAt.Sub(a.now()); d > 0 {
			wait = d // paused: a kick does not resume early
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			continue
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-a.kick:
			t.Stop()
		case <-t.C:
		}
	}
}

// Pass looks up every due track until none is left, the queue pauses
// (the service failing) or ctx is done.
func (a *Auto) Pass(ctx context.Context) error {
	if a.Client == nil || a.Repo == nil {
		return nil
	}
	if a.now().Before(a.resumeAt) {
		return nil
	}
	for {
		list, err := a.Repo.LyricsCandidates(ctx, a.now(), batch)
		if err != nil || len(list) == 0 {
			return err
		}
		for _, tr := range list {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := a.one(ctx, tr); err != nil {
				return err
			}
			if a.now().Before(a.resumeAt) {
				return nil
			}
		}
	}
}

// one looks one track up and stores the answer (or the failure).
func (a *Auto) one(ctx context.Context, tr library.Track) error {
	now := a.now()
	if Synced.MatchString(strings.TrimSpace(tr.Lyrics)) {
		// the loose SQL filter let synced file words through: store a long
		// «none» so the track is not selected again (the file wins anyway)
		return a.Repo.SaveTrackLyrics(ctx, tr.ID, library.StoredLyrics{Status: library.LyricsNone, Source: Source, FetchedAt: now, NextTry: never})
	}
	prev, _, err := a.Repo.TrackLyrics(ctx, tr.ID)
	if err != nil {
		return err
	}
	res, err := a.Client.fetch(ctx, QueryOf(tr))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		n := 1
		if prev.Status == library.LyricsError {
			n = prev.Attempts + 1
		}
		back := errorBase << min(n-1, 10)
		if back > errorMax {
			back = errorMax
		}
		a.fails++
		if a.fails >= failRun {
			if a.pause == 0 {
				a.pause = pauseBase
			} else if a.pause *= 2; a.pause > pauseMax {
				a.pause = pauseMax
			}
			a.resumeAt = now.Add(a.pause)
			a.log().Warn("lyrics: LRCLIB is not answering, queue paused", "for", a.pause, "err", err)
		}
		if prev.Status == library.LyricsFound {
			// a re-check of plain words failed: keep them, try later
			prev.NextTry, prev.FetchedAt = now.Add(back), now
			return a.Repo.SaveTrackLyrics(ctx, tr.ID, prev)
		}
		return a.Repo.SaveTrackLyrics(ctx, tr.ID, library.StoredLyrics{Status: library.LyricsError, Source: Source,
			FetchedAt: now, NextTry: now.Add(back), Attempts: n})
	}
	a.fails, a.pause = 0, 0
	if res == nil && prev.Status == library.LyricsFound {
		// the re-check of plain words found nothing new: keep them
		prev.NextTry, prev.FetchedAt = now.Add(PlainTTL), now
		return a.Repo.SaveTrackLyrics(ctx, tr.ID, prev)
	}
	return a.Repo.SaveTrackLyrics(ctx, tr.ID, Stored(res, now))
}

// Stored is what to keep of a service answer (nil: none) got at now.
func Stored(res *Result, now time.Time) library.StoredLyrics {
	s := library.StoredLyrics{Status: library.LyricsNone, Source: Source, FetchedAt: now, NextTry: now.Add(NoneTTL)}
	switch {
	case res == nil:
	case res.Synced != "":
		s.Status, s.Synced, s.Plain, s.NextTry = library.LyricsFound, res.Synced, res.Plain, never
	default:
		s.Status, s.Plain, s.NextTry = library.LyricsFound, res.Plain, now.Add(PlainTTL)
	}
	return s
}

// Source names LRCLIB in stored lyrics and the lyrics endpoint.
const Source = "lrclib"

// QueryOf is the lookup of a library track.
func QueryOf(tr library.Track) Query {
	return Query{Artist: tr.Artist, Title: tr.Title, Album: tr.Album, DurationSec: int((tr.DurationMs + 500) / 1000)}
}
