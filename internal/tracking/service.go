package tracking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/antresol-app/antresol/internal/notify"
)

// Scanner finds the sources of one kind for a title's episodes.
type Scanner interface {
	Kind() string
	Scan(ctx context.Context, t Title, eps []Episode) ([]Source, error)
}

// Skeleton fetches a title's episode list and airing status
// (airing | ended | upcoming | unknown).
type Skeleton interface {
	Episodes(ctx context.Context, t Title) (eps []Episode, status string, err error)
}

// TitleInfo is what a skeleton learns about a title besides its episodes:
// its name and TMDB poster path, and its age certification (RU form, "" =
// unknown).
type TitleInfo struct{ Name, Poster, Cert string }

// InfoSkeleton is a Skeleton that also tells the title's TitleInfo from the
// same reads; Refresh stores it (SetInfo).
type InfoSkeleton interface {
	EpisodesInfo(ctx context.Context, t Title) (eps []Episode, status string, info TitleInfo, err error)
}

// Notifier queues a letter event (*notify.Service).
type Notifier interface {
	Enqueue(ctx context.Context, e notify.Event) (bool, error)
}

// Refresher defaults.
const (
	defaultInterval = 30 * time.Minute
	defaultStart    = time.Minute // the first round after start (the server settles first)
	dueBatch        = 50          // titles refreshed per round at most
	// mailMaxAge: an episode aired longer ago is never mailed (a safety net
	// beside the per-kind baseline).
	mailMaxAge = 30 * 24 * time.Hour
)

var errNoSkeleton = errors.New("tracking: no episode source (TMDB key unset)")

// ErrUnchanged is what a scanner returns when it did not look this time
// (its own cadence is slower than the title's): its kind's rows stay as they
// are, last_seen included, and nothing is logged.
var ErrUnchanged = errors.New("tracking: scanner skipped this title")

// Service refreshes tracked titles: their episode lists from Skeleton, the
// sources of each episode from Scanners, and tells the interested profiles
// about episodes that became available.
type Service struct {
	Store    *Store
	Skeleton Skeleton
	Scanners []Scanner
	Notify   Notifier
	// Interest lists the profiles to tell about a title's new episodes.
	Interest func(ctx context.Context, titleID int64) ([]int64, error)
	Interval time.Duration // 0 = 30m tick
	Now      func() time.Time
	Log      *slog.Logger
	// TitlePath is the web page of a title off the library that a letter
	// links to (nil or "": none).
	TitlePath func(t Title) string
	// OnAdded is called after each refresh that added source rows, with
	// all of them (every kind). An error is logged, not fatal.
	OnAdded []func(ctx context.Context, t Title, added []Source) error
	// Watched say which episodes a profile has watched (the library's
	// progress, and more in the full build); the feed leaves them out.
	Watched []WatchedFunc

	startAfter time.Duration // 0 = defaultStart (tests shorten it)

	kickOnce   sync.Once
	kickSignal chan struct{}
	kickMu     sync.Mutex
	kicked     map[int64]bool
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) signal() chan struct{} {
	s.kickOnce.Do(func() { s.kickSignal = make(chan struct{}, 1) })
	return s.kickSignal
}

// Kick asks Run to refresh one title soon, due or not. Never blocks.
func (s *Service) Kick(titleID int64) {
	s.kickMu.Lock()
	if s.kicked == nil {
		s.kicked = map[int64]bool{}
	}
	s.kicked[titleID] = true
	s.kickMu.Unlock()
	select {
	case s.signal() <- struct{}{}:
	default:
	}
}

func (s *Service) takeKicked() []int64 {
	s.kickMu.Lock()
	defer s.kickMu.Unlock()
	ids := make([]int64, 0, len(s.kicked))
	for id := range s.kicked {
		ids = append(ids, id)
	}
	clear(s.kicked)
	slices.Sort(ids)
	return ids
}

// Run refreshes the due titles a minute after the start, then every
// Interval, and the titles Kick asks for, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	start := s.startAfter
	if start <= 0 {
		start = defaultStart
	}
	wake := s.signal()
	t := time.NewTimer(start)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			for _, id := range s.takeKicked() {
				if ctx.Err() != nil {
					return
				}
				if err := s.Refresh(ctx, id); err != nil && ctx.Err() == nil {
					s.log().Warn("tracking: refresh", "title", id, "err", err)
				}
			}
		case <-t.C:
			s.refreshDue(ctx)
			t.Reset(interval)
		}
	}
}

// refreshDue refreshes the titles whose next check has come; one title's
// error does not stop the others.
func (s *Service) refreshDue(ctx context.Context) {
	// watchlisted library titles are tracked: their rows first
	if err := s.Store.EnsureWatchlistTitles(ctx); err != nil && ctx.Err() == nil {
		s.log().Warn("tracking: watchlist titles", "err", err)
	}
	due, err := s.Store.Due(ctx, s.now(), dueBatch)
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("tracking: due titles", "err", err)
		}
		return
	}
	for _, t := range due {
		if ctx.Err() != nil {
			return
		}
		if err := s.Refresh(ctx, t.ID); err != nil && ctx.Err() == nil {
			s.log().Warn("tracking: refresh", "title", t.ID, "err", err)
		}
	}
}

// Refresh fetches a title's episode list, runs every scanner and records
// what they found. A scanner that fails keeps its kind's old rows (the
// error is logged; ErrUnchanged keeps them silently). Profiles from
// Interest are told about each episode that had no source of any kind
// before. A kind's first successful scan of a
// title is its baseline and tells nobody (a scanner down at the title's
// first refresh, or added later, does not mail the back catalogue), and an
// episode aired more than mailMaxAge ago is never mailed.
func (s *Service) Refresh(ctx context.Context, titleID int64) error {
	if s.Skeleton == nil {
		return errNoSkeleton
	}
	t, ok, err := s.Store.TitleByID(ctx, titleID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("tracking: title %d not found", titleID)
	}
	var eps []Episode
	var status string
	var info TitleInfo
	if is, ok := s.Skeleton.(InfoSkeleton); ok {
		eps, status, info, err = is.EpisodesInfo(ctx, t)
	} else {
		eps, status, err = s.Skeleton.Episodes(ctx, t)
	}
	if err != nil {
		return fmt.Errorf("tracking: episodes of %d: %w", titleID, err)
	}
	if info != (TitleInfo{}) {
		if err := s.Store.SetInfo(ctx, titleID, info); err != nil {
			return err
		}
		t, _, err = s.Store.TitleByID(ctx, titleID)
		if err != nil {
			return err
		}
	}
	if err := s.Store.ReplaceEpisodes(ctx, titleID, eps); err != nil {
		return err
	}
	old, err := s.Store.Sources(ctx, titleID)
	if err != nil {
		return err
	}
	had := map[[2]int]bool{}
	for _, x := range old {
		had[[2]int{x.Season, x.Episode}] = true
	}
	scanned, err := s.Store.ScannedKinds(ctx, titleID)
	if err != nil {
		return err
	}
	now := s.now()
	var added, mailable []Source
	for _, sc := range s.Scanners {
		srcs, err := sc.Scan(ctx, t, eps)
		if errors.Is(err, ErrUnchanged) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log().Warn("tracking: scan", "title", titleID, "kind", sc.Kind(), "err", err)
			continue
		}
		a, err := s.Store.UpsertSources(ctx, titleID, sc.Kind(), srcs, now)
		if err != nil {
			return err
		}
		added = append(added, a...)
		if scanned[sc.Kind()] {
			mailable = append(mailable, a...)
		} else if err := s.Store.MarkScanned(ctx, titleID, sc.Kind()); err != nil {
			return err
		}
	}
	s.notifyNew(ctx, t, eps, mailable, had, now)
	if len(added) > 0 {
		for _, h := range s.OnAdded {
			if err := h(ctx, t, added); err != nil {
				s.log().Warn("tracking: on added", "title", titleID, "err", err)
			}
		}
	}
	t.Status = status
	return s.Store.SetChecked(ctx, titleID, status, NextCheck(t, eps, now), now)
}

// notifyNew queues one event per interested profile for each episode among
// added that had no source before, got one of a kind other than library and
// did not air more than mailMaxAge before now.
func (s *Service) notifyNew(ctx context.Context, t Title, eps []Episode, added []Source, had map[[2]int]bool, now time.Time) {
	if s.Notify == nil || s.Interest == nil || len(added) == 0 {
		return
	}
	names, aired := map[[2]int]string{}, map[[2]int]string{}
	for _, e := range eps {
		names[[2]int{e.Season, e.Number}] = e.Name
		aired[[2]int{e.Season, e.Number}] = e.AirDate
	}
	oldest := now.Add(-mailMaxAge).Format(time.DateOnly)
	// an episode whose only new sources are the library's is left to
	// notify.Collect, which already mails new library episodes
	var order [][2]int
	nonLibrary := map[[2]int]bool{}
	for _, a := range added {
		k := [2]int{a.Season, a.Episode}
		if had[k] {
			continue
		}
		if _, seen := nonLibrary[k]; !seen {
			order = append(order, k)
			nonLibrary[k] = false
		}
		if a.Kind != "library" {
			nonLibrary[k] = true
		}
	}
	var fresh [][2]int
	for _, k := range order {
		// an unknown air date ("") is not old
		if nonLibrary[k] && !(aired[k] != "" && aired[k] < oldest) {
			fresh = append(fresh, k)
		}
	}
	if len(fresh) == 0 {
		return
	}
	users, err := s.Interest(ctx, t.ID)
	if err != nil {
		s.log().Warn("tracking: interested profiles", "title", t.ID, "err", err)
		return
	}
	path := ""
	if s.TitlePath != nil {
		path = s.TitlePath(t)
	}
	for _, k := range fresh {
		for _, uid := range users {
			// listed under the title: a group id of its own (negative: it
			// never meets a library series id)
			e := notify.Event{UserID: uid, Topic: notify.Episodes, GroupID: -t.ID,
				Key: fmt.Sprintf("trk:%d:S%02dE%02d", t.ID, k[0], k[1]),
				Data: notify.Data{Title: t.Name, Season: k[0], Episode: k[1],
					EpisodeTitle: names[k], Poster: posterURL(t.Poster), Path: path}}
			if t.Kind == "movie" {
				// a film is not an episode: «появилось то, что вы хотели»
				e.Topic = notify.Available
				e.Data = notify.Data{Title: t.Name, Kind: "movie", Poster: posterURL(t.Poster), Path: path}
			}
			if _, err := s.Notify.Enqueue(ctx, e); err != nil {
				s.log().Warn("tracking: notify", "title", t.ID, "user", uid, "err", err)
			}
		}
	}
}

func posterURL(p string) string { return PosterURL(p) }

// PosterURL makes a TMDB poster path ("/p.jpg") a full image URL.
func PosterURL(p string) string {
	if strings.HasPrefix(p, "/") {
		return tmdb.DefaultImageBase + "/w342" + p
	}
	return p
}

// NextCheck is when to refresh a title again: airing with an episode out
// today or yesterday in 2h, airing otherwise in 6h, upcoming in 24h, ended
// or unknown in 72h.
func NextCheck(t Title, eps []Episode, now time.Time) time.Time {
	switch t.Status {
	case "airing":
		today := now.Format(time.DateOnly)
		yesterday := now.AddDate(0, 0, -1).Format(time.DateOnly)
		for _, e := range eps {
			if e.AirDate == today || e.AirDate == yesterday {
				return now.Add(2 * time.Hour)
			}
		}
		return now.Add(6 * time.Hour)
	case "upcoming":
		return now.Add(24 * time.Hour)
	default:
		return now.Add(72 * time.Hour)
	}
}
