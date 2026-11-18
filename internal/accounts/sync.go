package accounts

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/animemap"
)

// Shikimori is the provider name of Shikimori links.
const Shikimori = "shikimori"

// Errors a Remote reports for a list it cannot read.
var (
	ErrRemoteNotFound  = errors.New("accounts: remote user not found")
	ErrRemoteForbidden = errors.New("accounts: remote list is private")
)

// Link statuses after a pull.
const (
	StatusOK       = "ok"
	StatusPrivate  = "private"
	StatusNotFound = "not_found"
	StatusError    = "error"
)

// RemoteAnime is the anime a remote list entry is about.
type RemoteAnime struct {
	ID                  int64 // Shikimori id (= MyAnimeList id)
	Name, Russian, Kind string
	AiredOn             string
	Episodes            int    // 0 = unknown (e.g. still airing)
	Status              string // anons | ongoing | released, "" = unknown
}

// RemoteRate is one entry of a remote anime list.
type RemoteRate struct {
	ID        int64
	Status    string // planned | watching | rewatching | completed | on_hold | dropped
	Score     int    // 0 = none
	Episodes  int
	UpdatedAt time.Time
	Anime     RemoteAnime
}

// Remote reads a profile's remote anime list, with the profile's access
// token when it has one ("" = anonymous: a private list reads only with
// its owner's token). A private list is ErrRemoteForbidden, an unknown
// user ErrRemoteNotFound, a rejected token ErrRemoteUnauthorized.
type Remote interface {
	AnimeRates(ctx context.Context, externalID int64, access string) ([]RemoteRate, error)
}

// rateListCap is the most entries one list read returns: a list that long
// may be cut, so nothing is read as removed from it.
const rateListCap = 5000

// AnimeMap maps remote anime to TMDB titles.
type AnimeMap interface {
	Resolve(ctx context.Context, a animemap.Title) (animemap.Entry, bool, error)
	// HasTMDB: some anime maps to the TMDB title (kind series | movie).
	HasTMDB(ctx context.Context, kind string, tmdbID int64) (bool, error)
	// FirstAnime is the anime standing for a TMDB title the remote list
	// lacks (a series: its season 1 from episode 1).
	FirstAnime(ctx context.Context, kind string, tmdbID int64) (animemap.Entry, bool, error)
}

// Mark is a profile's list mark on a title, as the tracking core keeps it.
type Mark struct {
	UserID, TitleID int64
	State           string // planned | watching | completed | on_hold | dropped
	Score           *int
	UpdatedAt       time.Time
	Origin          string
}

// TitleRef identifies a tracked title.
type TitleRef struct {
	ID          int64
	Kind        string // series | movie
	TMDBID      int64
	ShikimoriID int64 // 0 = unknown
}

// Tracker is what the sync needs of the tracking core. Its mark writes
// announce their origin to the core's mark-change subscribers, which is
// how remote-origin writes stay out of the outbox.
type Tracker interface {
	EnsureTitle(ctx context.Context, kind string, tmdbID int64, name string) (TitleRef, error)
	Title(ctx context.Context, titleID int64) (TitleRef, bool, error)
	// SetShikimoriID stores the title's Shikimori (and AniDB, 0 = unknown) id.
	SetShikimoriID(ctx context.Context, titleID, shikimoriID, anidbID int64) error
	Marks(ctx context.Context, uid int64) ([]Mark, error)
	Mark(ctx context.Context, uid, titleID int64) (Mark, bool, error)
	SetMark(ctx context.Context, m Mark) error
	DeleteMark(ctx context.Context, uid, titleID int64, origin string) error
}

// PullResult counts what a pull changed.
type PullResult struct {
	Imported int // marks created
	Updated  int // marks changed or removed
	Unmapped int // remote entries with no TMDB title
}

// Syncer keeps the profiles' marks and their Shikimori lists in step.
type Syncer struct {
	Accounts *Store
	Tracking Tracker
	Remote   Remote
	Map      AnimeMap
	// Kick asks the tracking core to refresh a title now (nil = no-op).
	Kick func(titleID int64)
	// Push drains a profile's outbox on every Run tick (nil = skip).
	Push func(ctx context.Context, uid int64) error
	// Writer writes the remote list (nil: nothing is pushed).
	Writer Writer
	// OAuth is the provider's OAuth client configuration.
	OAuth *OAuth
	// Watched reports the profile's watched episodes ({season, episode})
	// of a title (nil: none known).
	Watched func(ctx context.Context, uid int64, t TitleRef) (map[[2]int]bool, error)
	// Episodes lists a title's known episodes (nil: none known).
	Episodes func(ctx context.Context, titleID int64) ([]Episode, error)
	Now      func() time.Time
	Log      *slog.Logger
	// Sleep waits between profiles in Run (nil = a timer honouring ctx).
	Sleep func(ctx context.Context, d time.Duration) error

	mu    sync.Mutex
	locks map[int64]*sync.Mutex
}

// originMyShows is the origin of marks read from a MyShows list
// (internal/accounts/myshows.Provider). MyShows is read only here, but what
// it says goes on to Shikimori like a change made here (reconcile.go).
const originMyShows = "myshows"

const privateMsg = "Список скрыт: войдите через Shikimori"

func (s *Syncer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Syncer) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// userLock serialises the pulls of one profile.
func (s *Syncer) userLock(uid int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[int64]*sync.Mutex{}
	}
	l, ok := s.locks[uid]
	if !ok {
		l = &sync.Mutex{}
		s.locks[uid] = l
	}
	return l
}

// UserLock is the lock that serialises one profile's syncs: other
// providers' pulls take it too, so two lists never write a profile's
// marks at once.
func (s *Syncer) UserLock(uid int64) *sync.Mutex { return s.userLock(uid) }

// localState maps a remote status to a mark state ("" = unknown status).
func localState(status string) string {
	switch status {
	case "planned", "completed", "on_hold", "dropped":
		return status
	case "watching", "rewatching":
		return "watching"
	}
	return ""
}

func live(state string) bool {
	return state == "planned" || state == "watching" || state == "on_hold"
}

// writesQueued: the profile's local changes are queued for the remote
// list (enabled, or not yet decided).
func writesQueued(l Link) bool {
	switch l.WriteState {
	case "enabled", "unasked", "asked":
		return true
	}
	return false
}

func sameScore(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// mappable: the title is an anime a Shikimori list can hold.
func (s *Syncer) mappable(ctx context.Context, t TitleRef) (bool, error) {
	if t.ShikimoriID > 0 {
		return true, nil
	}
	if s.Map == nil {
		return false, nil
	}
	return s.Map.HasTMDB(ctx, t.Kind, t.TMDBID)
}

// MarkChanged is the tracking core's mark-change subscriber: a change to an
// anime title of a profile linked to Shikimori is queued for the remote
// list (upsert while the mark exists, delete once gone) unless the profile
// declined write-back. A change from Antresol or from another list
// (MyShows) goes out, so every list ends up with the reconciled state; a
// change read from Shikimori itself never echoes back. Only a removal made
// here deletes remotely: a title leaving another list says nothing about
// what was watched.
func (s *Syncer) MarkChanged(ctx context.Context, uid, titleID int64, origin string) {
	if origin == Shikimori {
		return
	}
	if err := s.markChanged(ctx, uid, titleID, origin == "antresol"); err != nil {
		s.log().Warn("accounts: queue mark change", "user", uid, "title", titleID, "err", err)
	}
}

func (s *Syncer) markChanged(ctx context.Context, uid, titleID int64, mayDelete bool) error {
	l, ok, err := s.Accounts.Link(ctx, uid, Shikimori)
	if err != nil || !ok || !writesQueued(l) {
		return err
	}
	t, ok, err := s.Tracking.Title(ctx, titleID)
	if err != nil || !ok {
		return err
	}
	if ok, err := s.mappable(ctx, t); err != nil || !ok {
		return err
	}
	marks, err := s.Tracking.Marks(ctx, uid)
	if err != nil {
		return err
	}
	op := "delete"
	for _, m := range marks {
		if m.TitleID == titleID {
			op = "upsert"
			break
		}
	}
	if op == "delete" && !mayDelete {
		return nil
	}
	return s.Accounts.Enqueue(ctx, uid, Shikimori, titleID, op, s.now())
}

// cour is one remote entry mapped to a TMDB title (a title may have
// several: the cours of an anime mapped to seasons of one TMDB series).
type cour struct {
	rate  RemoteRate
	state string // the local mark state of its remote status
	entry animemap.Entry
}

// above: cour a comes after b in the title (higher season, then offset).
func above(a, b cour) bool {
	if a.entry.Season != b.entry.Season {
		return a.entry.Season > b.entry.Season
	}
	if a.entry.Offset != b.entry.Offset {
		return a.entry.Offset > b.entry.Offset
	}
	return a.rate.Anime.ID > b.rate.Anime.ID
}

// aggregate is the mark a title's cours make together: watching when any
// cour is watched (or rewatched); otherwise the last cour (highest season,
// offset) decides, so every cour completed reads as completed. The
// deciding cour gives the score.
func aggregate(cs []cour) (string, *int) {
	var dec *cour
	for i := range cs {
		if cs[i].state == "watching" && (dec == nil || above(cs[i], *dec)) {
			dec = &cs[i]
		}
	}
	if dec == nil {
		for i := range cs {
			if dec == nil || above(cs[i], *dec) {
				dec = &cs[i]
			}
		}
	}
	var score *int
	if dec.rate.Score > 0 {
		v := dec.rate.Score
		score = &v
	}
	return dec.state, score
}

func sameMark(a Mark, aok bool, b Mark, bok bool) bool {
	if aok != bok {
		return false
	}
	return !aok || (a.State == b.State && sameScore(a.Score, b.Score) && a.UpdatedAt.Equal(b.UpdatedAt))
}

// PullOne reads the profile's Shikimori list into its marks. Remote-origin
// writes carry Origin "shikimori" and the remote updated_at. The first pull
// of a link imports every mapped remote entry by ImportWins (a mark here
// that says the profile got further, or is newer at the same rank, stays
// and is queued for the list) and queues the profile's anime marks the
// remote lacks; later a title's remote entries win only
// when one of them changed after the local mark and after our own last
// push of it. A title whose remote entries are all gone loses a local mark
// not touched since. A private or unknown list sets the link's status and
// changes no mark. The list is read with the profile's access token when it
// has one (a private list reads only so); the first-link rule never runs on
// an empty list read anonymously (it may be a private one).
func (s *Syncer) PullOne(ctx context.Context, uid int64) (PullResult, error) {
	lk := s.userLock(uid)
	lk.Lock()
	defer lk.Unlock()
	var res PullResult
	link, ok, err := s.Accounts.Link(ctx, uid, Shikimori)
	if err != nil {
		return res, err
	}
	if !ok {
		return res, errors.New("accounts: profile not linked")
	}
	rates, authed, relogin, err := s.readList(ctx, link)
	if err != nil {
		status, msg := StatusError, err.Error()
		switch {
		case errors.Is(err, ErrRemoteForbidden):
			status, msg = StatusPrivate, privateMsg
		case errors.Is(err, ErrRemoteNotFound):
			status, msg = StatusNotFound, "Пользователь Shikimori не найден"
		}
		if relogin {
			status, msg = StatusRelogin, reloginMsg
		}
		if serr := s.Accounts.SetStatus(ctx, uid, Shikimori, status, msg, s.now()); serr != nil {
			return res, serr
		}
		return res, err
	}
	res, err = s.apply(ctx, link, rates, authed)
	if err != nil {
		if serr := s.Accounts.SetStatus(ctx, uid, Shikimori, StatusError, err.Error(), s.now()); serr != nil {
			s.log().Warn("accounts: status", "user", uid, "err", serr)
		}
		return res, err
	}
	if err := s.Accounts.SetUnmapped(ctx, uid, Shikimori, res.Unmapped); err != nil {
		return res, err
	}
	if relogin {
		// the list read anonymously; the rejected sign-in still needs
		// renewing before anything is pushed
		return res, s.Accounts.SetStatus(ctx, uid, Shikimori, StatusRelogin, reloginMsg, s.now())
	}
	return res, s.Accounts.SetStatus(ctx, uid, Shikimori, StatusOK, "", s.now())
}

// readList reads the profile's remote list: with its access token when it
// has a usable one (refreshed like a push's: ahead of expiry, and once
// after a rejection), else anonymously. authed: the list was read with the
// token; relogin: the sign-in is rejected (now or before).
func (s *Syncer) readList(ctx context.Context, link Link) (rates []RemoteRate, authed, relogin bool, err error) {
	uid := link.UserID
	relogin = link.Status == StatusRelogin
	access := ""
	var tok Tokens
	refreshed := false
	if !relogin {
		var ok bool
		if tok, ok, err = s.Accounts.Tokens(ctx, uid, Shikimori); err != nil {
			return nil, false, relogin, err
		}
		if ok && tok.Access != "" {
			access = tok.Access
			if s.Writer != nil && !tok.Expires.IsZero() && tok.Expires.Before(s.now().Add(refreshAhead)) {
				refreshed = true
				if tok, err = s.refresh(ctx, uid, tok); err != nil {
					access, relogin = s.readAnonymously(ctx, link, err)
				} else {
					access = tok.Access
				}
			}
		}
	}
	rates, err = s.Remote.AnimeRates(ctx, link.ExternalID, access)
	if access != "" && errors.Is(err, ErrRemoteUnauthorized) {
		if s.Writer != nil && !refreshed {
			if tok, err = s.refresh(ctx, uid, tok); err == nil {
				access = tok.Access
				rates, err = s.Remote.AnimeRates(ctx, link.ExternalID, access)
			}
		}
		if err != nil && ctx.Err() == nil {
			// the token (or its refresh) failed: read as anyone would
			access, relogin = s.readAnonymously(ctx, link, err)
			rates, err = s.Remote.AnimeRates(ctx, link.ExternalID, "")
		}
	}
	return rates, err == nil && access != "", relogin, err
}

// readAnonymously handles a token that could not be used for a read: a
// rejected one sets the link to «relogin»; either way the read goes on
// without it.
func (s *Syncer) readAnonymously(ctx context.Context, link Link, err error) (access string, relogin bool) {
	s.log().Info("accounts: list read without sign-in", "user", link.UserID, "err", err)
	if errors.Is(err, ErrRemoteUnauthorized) {
		if serr := s.authFailed(ctx, link, err); !errors.Is(serr, ErrRemoteUnauthorized) {
			s.log().Warn("accounts: status", "user", link.UserID, "err", serr)
		}
		return "", true
	}
	return "", link.Status == StatusRelogin
}

func (s *Syncer) apply(ctx context.Context, link Link, rates []RemoteRate, authed bool) (PullResult, error) {
	var res PullResult
	uid := link.UserID
	first := !link.FirstPullDone
	twinList, err := s.Accounts.Rates(ctx, uid, Shikimori)
	if err != nil {
		return res, err
	}
	twins := map[int64]Rate{}        // by target
	titleTwins := map[int64][]Rate{} // by title
	for _, r := range twinList {
		twins[r.TargetID] = r
		titleTwins[r.TitleID] = append(titleTwins[r.TitleID], r)
	}
	markList, err := s.Tracking.Marks(ctx, uid)
	if err != nil {
		return res, err
	}
	marks := map[int64]Mark{}
	for _, m := range markList {
		marks[m.TitleID] = m
	}
	pending, err := s.Accounts.Outbox(ctx, uid, Shikimori)
	if err != nil {
		return res, err
	}
	queued := map[int64]OutboxItem{}
	for _, it := range pending {
		queued[it.TitleID] = it
	}

	// map the remote list onto titles
	byTitle := map[int64][]cour{}
	refs := map[int64]TitleRef{}
	listed := map[int64]bool{} // remote anime ids in the list
	resolveErr := false
	for _, r := range rates {
		// stored times are whole seconds: compare like with like
		r.UpdatedAt = r.UpdatedAt.Truncate(time.Second)
		listed[r.Anime.ID] = true
		state := localState(r.Status)
		if state == "" {
			s.log().Warn("accounts: unknown remote status", "user", uid, "status", r.Status)
			res.Unmapped++
			continue
		}
		e, ok, err := s.Map.Resolve(ctx, animemap.Title{MALID: r.Anime.ID, Name: r.Anime.Name, Russian: r.Anime.Russian, Kind: r.Anime.Kind, AiredOn: r.Anime.AiredOn})
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			resolveErr = true
			s.log().Warn("accounts: map anime", "anime", r.Anime.ID, "err", err)
		}
		if !ok || e.TMDBID == 0 {
			res.Unmapped++
			continue
		}
		name := r.Anime.Russian
		if name == "" {
			name = r.Anime.Name
		}
		t, err := s.Tracking.EnsureTitle(ctx, e.Kind, e.TMDBID, name)
		if err != nil {
			return res, err
		}
		byTitle[t.ID] = append(byTitle[t.ID], cour{rate: r, state: state, entry: e})
		refs[t.ID] = t
	}

	now := s.now()
	for tid, cs := range byTitle {
		if t := refs[tid]; t.ShikimoriID == 0 {
			// the first cour stands for the title
			c := cs[0]
			for _, x := range cs[1:] {
				if above(c, x) {
					c = x
				}
			}
			if err := s.Tracking.SetShikimoriID(ctx, tid, c.rate.Anime.ID, c.entry.AniDBID); err != nil {
				return res, err
			}
		}
		local, hasLocal := marks[tid]
		// the cours changed since the last pull, not echoes of our pushes;
		// on the first pull a cour never stored before wins outright (one
		// stored by an earlier, failed first pull follows the timestamps)
		var changedAt time.Time
		unseen := false
		for _, c := range cs {
			tw, known := twins[c.rate.Anime.ID]
			fresh := first && !known
			unseen = unseen || fresh
			if (fresh || c.rate.UpdatedAt.After(tw.RemoteUpdatedAt) && c.rate.UpdatedAt.After(tw.PushedAt)) && c.rate.UpdatedAt.After(changedAt) {
				changedAt = c.rate.UpdatedAt
			}
		}
		win := unseen
		if unseen && hasLocal {
			// an import: the further state wins (reconcile.go)
			st, _ := aggregate(cs)
			if win = ImportWins(st, changedAt, local, true); !win && writesQueued(link) {
				// the mark here goes to the list instead
				if err := s.Accounts.Enqueue(ctx, uid, Shikimori, tid, "upsert", now); err != nil {
					return res, err
				}
			}
		}
		if !win && !unseen && !changedAt.IsZero() {
			localAt := local.UpdatedAt
			if !hasLocal {
				if it, ok := queued[tid]; ok {
					localAt = it.CreatedAt // removed here, not yet pushed
				}
			}
			win = changedAt.After(localAt)
		}
		var markAt time.Time // the mark's time once a pull win settled it
		if win {
			state, score := aggregate(cs)
			// a change made here while the list was read stays
			fresh, freshOK, err := s.Tracking.Mark(ctx, uid, tid)
			if err != nil {
				return res, err
			}
			markAt = fresh.UpdatedAt
			if !sameMark(fresh, freshOK, local, hasLocal) {
				win = false
			} else if !hasLocal || local.State != state || !sameScore(local.Score, score) {
				markAt = changedAt
				if err := s.Tracking.SetMark(ctx, Mark{UserID: uid, TitleID: tid, State: state, Score: score,
					UpdatedAt: changedAt, Origin: Shikimori}); err != nil {
					return res, err
				}
				if hasLocal {
					res.Updated++
				} else {
					res.Imported++
				}
				if live(state) && s.Kick != nil {
					s.Kick(tid)
				}
			}
		}
		if win {
			// the remote change is newer than a queued local one
			if it, ok := queued[tid]; ok {
				if err := s.Accounts.Done(ctx, it.ID); err != nil {
					return res, err
				}
				delete(queued, tid)
			}
		}
		for _, c := range cs {
			tw := twins[c.rate.Anime.ID]
			if tw.TitleID != tid {
				tw.MarkAt = time.Time{} // it stood for another title
			}
			// our last push (pushed_at, local_episodes, mark_at) carries over
			if err := s.Accounts.SetRate(ctx, Rate{UserID: uid, Provider: Shikimori, TargetID: c.rate.Anime.ID, TitleID: tid,
				TMDBSeason: c.entry.Season, TMDBOffset: c.entry.Offset, RateID: c.rate.ID, AnimeEpisodes: c.rate.Anime.Episodes,
				AnimeStatus: c.rate.Anime.Status, RemoteStatus: c.rate.Status, RemoteEpisodes: c.rate.Episodes, LocalEpisodes: tw.LocalEpisodes,
				RemoteUpdatedAt: c.rate.UpdatedAt, PushedAt: tw.PushedAt, MarkAt: tw.MarkAt}); err != nil {
				return res, err
			}
		}
		if win {
			// the mark now says what the remote list says: a push writes
			// status and score again only once it changes here
			if err := s.Accounts.SetTitleMarkAt(ctx, uid, Shikimori, tid, markAt); err != nil {
				return res, err
			}
		}
	}

	// remote entries removed since the last pull (an empty list is never
	// read as "everything removed", nor one that may have been cut short)
	if len(rates) > 0 && len(rates) < rateListCap {
		for tid, tws := range titleTwins {
			var gone []Rate
			anyListed := false
			for _, tw := range tws {
				if listed[tw.TargetID] {
					anyListed = true
				} else {
					gone = append(gone, tw)
				}
			}
			if len(gone) == 0 {
				continue
			}
			// the whole title left the list (none of its cours remain)
			if _, mapped := byTitle[tid]; !mapped && !anyListed {
				var lastRemote time.Time
				for _, tw := range gone {
					if tw.RemoteUpdatedAt.After(lastRemote) {
						lastRemote = tw.RemoteUpdatedAt
					}
				}
				// read again: a change made here while the list was read stays
				local, ok, err := s.Tracking.Mark(ctx, uid, tid)
				if err != nil {
					return res, err
				}
				if ok {
					if !local.UpdatedAt.After(lastRemote) {
						if err := s.Tracking.DeleteMark(ctx, uid, tid, Shikimori); err != nil {
							return res, err
						}
						res.Updated++
					} else if writesQueued(link) {
						// changed here (or by another list) since: it goes
						// back to the list
						if err := s.Accounts.Enqueue(ctx, uid, Shikimori, tid, "upsert", now); err != nil {
							return res, err
						}
					}
				}
			}
			for _, tw := range gone {
				if err := s.Accounts.DeleteRate(ctx, uid, Shikimori, tw.TargetID); err != nil {
					return res, err
				}
			}
		}
	}

	if !first || resolveErr || len(rates) == 0 && !authed {
		// a failed lookup may hide a remote twin, and an empty list read
		// anonymously may be a private one: the first-link rule waits for
		// a pull that maps the whole list
		return res, nil
	}
	// first link: the profile's anime marks the remote lacks go out (not
	// told by a list that may have been cut short)
	if writesQueued(link) && len(rates) < rateListCap {
		for tid := range marks {
			if _, ok := byTitle[tid]; ok {
				continue
			}
			t, ok, err := s.Tracking.Title(ctx, tid)
			if err != nil {
				return res, err
			}
			if !ok || listed[t.ShikimoriID] {
				continue
			}
			if ok, err := s.mappable(ctx, t); err != nil {
				return res, err
			} else if ok {
				if err := s.Accounts.Enqueue(ctx, uid, Shikimori, tid, "upsert", now); err != nil {
					return res, err
				}
			}
		}
	}
	return res, s.Accounts.SetFirstPullDone(ctx, uid, Shikimori)
}

// Pull cadence: profiles with a «watching» mark are read more often.
const (
	pullWatching = 15 * time.Minute
	pullIdle     = 60 * time.Minute
	runTick      = time.Minute
)

// pullDue: a profile last pulled at last is due at now.
func pullDue(last time.Time, watching bool, now time.Time) bool {
	if last.IsZero() {
		return true
	}
	every := pullIdle
	if watching {
		every = pullWatching
	}
	return !now.Before(last.Add(every))
}

// jitter spreads the profiles' pulls a few seconds apart.
func jitter(uid int64) time.Duration {
	return time.Duration(uint64(uid)*2654435761%5000) * time.Millisecond
}

func (s *Syncer) sleep(ctx context.Context, d time.Duration) error {
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run pulls every linked profile on its cadence (15 min with a «watching»
// mark, else 60 min) and pushes every profile's due outbox items (Push)
// each minute, until ctx ends. With no links a tick costs one query.
func (s *Syncer) Run(ctx context.Context) {
	t := time.NewTicker(runTick)
	defer t.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Syncer) tick(ctx context.Context) {
	links, err := s.Accounts.Links(ctx, Shikimori)
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("accounts: links", "err", err)
		}
		return
	}
	for _, l := range links {
		if ctx.Err() != nil {
			return
		}
		if l.ExternalID == 0 {
			continue
		}
		watching := false
		if ms, err := s.Tracking.Marks(ctx, l.UserID); err == nil {
			for _, m := range ms {
				if m.State == "watching" {
					watching = true
					break
				}
			}
		}
		if !pullDue(l.LastSync, watching, s.now()) {
			continue
		}
		if s.sleep(ctx, jitter(l.UserID)) != nil {
			return
		}
		if r, err := s.PullOne(ctx, l.UserID); err != nil {
			if ctx.Err() == nil {
				s.log().Info("accounts: pull", "user", l.UserID, "err", err)
			}
		} else if r.Imported+r.Updated > 0 {
			s.log().Info("accounts: pulled", "user", l.UserID, "imported", r.Imported, "updated", r.Updated, "unmapped", r.Unmapped)
		}
	}
	if s.Push == nil {
		return
	}
	// pending pushes go out every tick, not only after a pull
	for _, l := range links {
		if ctx.Err() != nil {
			return
		}
		if err := s.Push(ctx, l.UserID); err != nil && ctx.Err() == nil {
			s.log().Info("accounts: push", "user", l.UserID, "err", err)
		}
	}
}
