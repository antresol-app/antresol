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
	// Scanners are the core's own (library): local reads, run with the
	// episode list (RefreshEpisodes); the addons' come through SetScanners.
	Scanners []Scanner
	Notify   Notifier
	// Interest lists the profiles to tell about a title's new episodes.
	Interest func(ctx context.Context, titleID int64) ([]int64, error)
	// SourceKinds are the source kinds a profile may open (the library's,
	// and the scan kinds of the addons it has access to): a profile is
	// told of a new episode only when one of its new sources is of such a
	// kind. An error tells that profile nothing (logged). nil: every kind.
	SourceKinds func(ctx context.Context, userID int64) (map[string]bool, error)
	Interval    time.Duration // 0 = 30m tick
	Now         func() time.Time
	Log         *slog.Logger
	// TitlePath is the web page of a title off the library that a letter
	// links to (nil or "": none).
	TitlePath func(t Title) string
	// OnAdded is called after each refresh that added source rows, with
	// all of them (every kind). An error is logged, not fatal.
	OnAdded []func(ctx context.Context, t Title, added []Source) error
	// Watched say which episodes a profile has watched (the library's
	// progress, and the addons' through tracking.watched); the feed leaves
	// them out.
	Watched []WatchedFunc
	// WatchedElsewhere say what a profile's linked lists (Shikimori,
	// MyShows) record as watched: the feed leaves them out too, but they
	// are not progress here (pushes and the episodes endpoint read only
	// Watched).
	WatchedElsewhere []WatchedFunc
	// WatchStates are the same with positions, for the episodes endpoint
	// (the library's, and the addons' through tracking.watched).
	WatchStates []WatchStateFunc

	// scanners are the scanners from outside the core (the addons'),
	// asked for again on every refresh (SetScanners)
	scanners func(ctx context.Context) []Scanner

	startAfter time.Duration // 0 = defaultStart (tests shorten it)

	kickOnce   sync.Once
	kickSignal chan struct{}
	kickMu     sync.Mutex
	kicked     map[int64]bool
}

// SetScanners adds scanners that come and go (the addons' scan kinds): f
// is called on every refresh and its scanners run after Scanners, in
// Finish (an addon can be slow; a page does not wait for it). One whose
// kind an earlier scanner has (library, or a repeat) is not run. Call it
// before Run.
func (s *Service) SetScanners(f func(ctx context.Context) []Scanner) { s.scanners = f }

// addonScanners are the scanners of SetScanners whose kind no earlier
// scanner has (Scanners', or a repeat).
func (s *Service) addonScanners(ctx context.Context) []Scanner {
	if s.scanners == nil {
		return nil
	}
	taken := map[string]bool{"library": true}
	for _, sc := range s.Scanners {
		taken[sc.Kind()] = true
	}
	var out []Scanner
	for _, sc := range s.scanners(ctx) {
		if taken[sc.Kind()] {
			continue
		}
		taken[sc.Kind()] = true
		out = append(out, sc)
	}
	return out
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
//
// It is RefreshEpisodes and then Finish: the page of an untracked title
// waits for the first and leaves the second to the background.
func (s *Service) Refresh(ctx context.Context, titleID int64) error {
	p, err := s.RefreshEpisodes(ctx, titleID)
	if err != nil {
		return err
	}
	return p.Finish(ctx)
}

// Pending is a refresh half done: the title's episode list is stored and the
// title is marked refreshed, and the core's own scanners (Scanners: the
// library's local reads) have run. Finish does the rest.
type Pending struct {
	s        *Service
	t        Title
	eps      []Episode
	status   string
	had      map[[2]int]bool // the episodes that had a source of any kind before this refresh
	scanned  map[string]bool // the kinds that scanned the title successfully before it
	added    []Source        // every row this refresh added so far
	mailable []Source        // those of a kind that has a baseline: the ones to tell about
}

// RefreshEpisodes is the first half of Refresh: the title's episode list,
// info and airing status from the skeleton, stored, the title marked
// refreshed (refreshed_at and the status, not next_check), and the core's
// own Scanners run. It is what a page needs; it never waits on an addon.
// Finish completes the refresh (until it has run, or when it is cut short,
// the title is still due for the refresher).
func (s *Service) RefreshEpisodes(ctx context.Context, titleID int64) (*Pending, error) {
	if s.Skeleton == nil {
		return nil, errNoSkeleton
	}
	t, ok, err := s.Store.TitleByID(ctx, titleID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("tracking: title %d not found", titleID)
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
		return nil, fmt.Errorf("tracking: episodes of %d: %w", titleID, err)
	}
	if info != (TitleInfo{}) {
		if err := s.Store.SetInfo(ctx, titleID, info); err != nil {
			return nil, err
		}
		t, _, err = s.Store.TitleByID(ctx, titleID)
		if err != nil {
			return nil, err
		}
	}
	if err := s.Store.ReplaceEpisodes(ctx, titleID, eps); err != nil {
		return nil, err
	}
	old, err := s.Store.Sources(ctx, titleID)
	if err != nil {
		return nil, err
	}
	had := map[[2]int]bool{}
	for _, x := range old {
		had[[2]int{x.Season, x.Episode}] = true
	}
	scanned, err := s.Store.ScannedKinds(ctx, titleID)
	if err != nil {
		return nil, err
	}
	p := &Pending{s: s, t: t, eps: eps, status: status, had: had, scanned: scanned}
	if err := p.scan(ctx, s.Scanners); err != nil {
		return nil, err
	}
	if err := s.Store.SetRefreshed(ctx, titleID, status, s.now()); err != nil {
		return nil, err
	}
	return p, nil
}

// scan runs scanners over the title, storing what each finds and keeping
// what is to be told.
func (p *Pending) scan(ctx context.Context, scanners []Scanner) error {
	s := p.s
	now := s.now()
	for _, sc := range scanners {
		srcs, err := sc.Scan(ctx, p.t, p.eps)
		if errors.Is(err, ErrUnchanged) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log().Warn("tracking: scan", "title", p.t.ID, "kind", sc.Kind(), "err", err)
			continue
		}
		a, err := s.Store.UpsertSources(ctx, p.t.ID, sc.Kind(), srcs, now)
		if err != nil {
			return err
		}
		p.added = append(p.added, a...)
		if p.scanned[sc.Kind()] {
			p.mailable = append(p.mailable, a...)
		} else if err := s.Store.MarkScanned(ctx, p.t.ID, sc.Kind()); err != nil {
			return err
		}
	}
	return nil
}

// notifyWait bounds telling about what a refresh found when its own context
// ended first.
const notifyWait = 15 * time.Second

// Finish is the second half of Refresh: the addons' scanners, then the
// letters and OnAdded for everything the refresh found (the core scanners'
// rows too), then the next check. When ctx ends mid-scan the rows already
// stored are still told about (their kinds' baseline is set, so a later
// round would not tell them) but the title is not checked: the refresher
// takes it again.
func (p *Pending) Finish(ctx context.Context) error {
	s := p.s
	err := p.scan(ctx, s.addonScanners(ctx))
	nctx := ctx
	if err != nil {
		if ctx.Err() == nil {
			return err
		}
		var cancel context.CancelFunc
		nctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), notifyWait)
		defer cancel()
	}
	now := s.now()
	s.notifyNew(nctx, p.t, p.eps, p.mailable, p.had, now)
	if len(p.added) > 0 {
		for _, h := range s.OnAdded {
			if err := h(nctx, p.t, p.added); err != nil {
				s.log().Warn("tracking: on added", "title", p.t.ID, "err", err)
			}
		}
	}
	if err != nil {
		return err
	}
	p.t.Status = p.status
	return s.Store.SetChecked(ctx, p.t.ID, p.status, NextCheck(p.t, p.eps, now), now)
}

// notifyNew queues one event per interested profile for each episode among
// added that had no source before, got one of a kind other than library and
// did not air more than mailMaxAge before now; only a profile that may open
// one of those kinds (SourceKinds) is told.
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
	nonLibrary := map[[2]int][]string{} // the new sources' kinds besides the library
	for _, a := range added {
		k := [2]int{a.Season, a.Episode}
		if had[k] {
			continue
		}
		if _, seen := nonLibrary[k]; !seen {
			order = append(order, k)
			nonLibrary[k] = nil
		}
		if a.Kind != "library" && !slices.Contains(nonLibrary[k], a.Kind) {
			nonLibrary[k] = append(nonLibrary[k], a.Kind)
		}
	}
	var fresh [][2]int
	for _, k := range order {
		// an unknown air date ("") is not old
		if len(nonLibrary[k]) > 0 && !(aired[k] != "" && aired[k] < oldest) {
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
	// mayOpen: profile uid may open one of the kinds (SourceKinds, read
	// once per profile; unreadable: none)
	kindsOf := map[int64]map[string]bool{}
	mayOpen := func(uid int64, kinds []string) bool {
		if s.SourceKinds == nil {
			return true
		}
		allowed, ok := kindsOf[uid]
		if !ok {
			var err error
			if allowed, err = s.SourceKinds(ctx, uid); err != nil {
				s.log().Warn("tracking: the source kinds of a profile", "title", t.ID, "user", uid, "err", err)
				allowed = nil
			}
			kindsOf[uid] = allowed
		}
		for _, k := range kinds {
			if allowed[k] {
				return true
			}
		}
		return false
	}
	for _, k := range fresh {
		for _, uid := range users {
			if !mayOpen(uid, nonLibrary[k]) {
				continue
			}
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

// StillURL is the full URL of an episode's TMDB still_path ("" stays "").
func StillURL(p string) string {
	if strings.HasPrefix(p, "/") {
		return tmdb.DefaultImageBase + "/w300" + p
	}
	return p
}

// NextCheck is when to refresh a title again: airing with an episode out
// today or yesterday in 2h, airing otherwise in 6h, upcoming in 24h, ended
// or unknown in 72h, and each title a little later by a share of that that
// is its own (checkJitter), so titles added together do not stay due
// together.
func NextCheck(t Title, eps []Episode, now time.Time) time.Time {
	d := checkInterval(t, eps, now)
	return now.Add(d + checkJitter(t.ID, d))
}

// checkInterval is the cadence of NextCheck before its jitter.
func checkInterval(t Title, eps []Episode, now time.Time) time.Duration {
	switch t.Status {
	case "airing":
		today := now.Format(time.DateOnly)
		yesterday := now.AddDate(0, 0, -1).Format(time.DateOnly)
		for _, e := range eps {
			if e.AirDate == today || e.AirDate == yesterday {
				return 2 * time.Hour
			}
		}
		return 6 * time.Hour
	case "upcoming":
		return 24 * time.Hour
	default:
		return 72 * time.Hour
	}
}

// checkJitterShare is the denominator of the most a title's check is put
// off: 1/5 of its interval.
const checkJitterShare = 5

// checkJitter is how much later than its interval a title's next check
// falls: 0 up to a fifth of the interval, fixed by the title's id. Titles
// that are imported together fall due together (a first refresh, then the
// same cadence), and a round of them is a wave of work for the scanners
// of the addons: one that searches many sites at once may pay a browser's
// worth of memory for each. The offset is a hash of the id, not a random
// draw: neighbouring ids land far apart (Fibonacci hashing), a title keeps
// its place in the spread from one check to the next, and the tests need
// no seam.
func checkJitter(id int64, interval time.Duration) time.Duration {
	h := uint64(id) * 0x9E3779B97F4A7C15
	frac := float64(h>>11) / (1 << 53) // [0, 1)
	return time.Duration(frac * float64(interval/checkJitterShare))
}
