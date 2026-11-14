package myshows

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/accounts"
)

// Provider is the provider name of MyShows links (the link's nickname is
// the MyShows login). The link is read only: nothing is ever written to
// MyShows and nothing is queued for it.
const Provider = "myshows"

// Link status messages (i18n catalog keys).
const (
	msgNotFound    = "Пользователь MyShows не найден"
	msgPrivate     = "Профиль MyShows скрыт"
	msgUnavailable = "MyShows не отвечает, попробуйте позже"
	msgLocal       = "Не удалось прочитать данные"
	msgTMDB        = "TMDB недоступен, попробуем позже"
)

// Pull cadence: MyShows gives no per-entry times, so every pull reads the
// whole list; keep it light.
const (
	pullEvery = 60 * time.Minute
	runTick   = time.Minute
	// maxLookupFailures in a row that say the service is struggling
	// (Unavailable: transport, 5xx, rate limit) stop a pull.
	maxLookupFailures = 3
)

var errLookups = errors.New("myshows: show lookups keep failing")

// Remote reads a public profile's list.
type Remote interface {
	ProfileShows(ctx context.Context, login string) ([]Entry, error)
}

// Resolver maps a MyShows show to a TMDB title (Mapper).
type Resolver interface {
	Resolve(ctx context.Context, showID int64) (tmdbID int64, kind string, ok bool, err error)
}

// Result counts what a pull changed.
type Result struct {
	Imported int // marks created
	Updated  int // marks changed or removed
	Unmapped int // list entries with no TMDB title (or an unknown status)
}

// Puller reads profiles' public MyShows lists into their marks.
type Puller struct {
	Accounts *accounts.Store
	Tracker  accounts.Tracker
	Remote   Remote
	Map      Resolver
	// Kick asks the tracking core to refresh a title now (nil = no-op).
	Kick func(titleID int64)
	// Lock is the per-profile sync lock shared with the other providers
	// (accounts.Syncer.UserLock); nil = the puller's own locks.
	Lock func(uid int64) *sync.Mutex
	Now  func() time.Time
	Log  *slog.Logger

	mu        sync.Mutex
	locks     map[int64]*sync.Mutex
	pullLocks map[int64]*sync.Mutex // whole pulls of one profile, one at a time
}

// pullLock serialises whole pulls of one profile (list read included), so
// an older list read never lands after a newer one. It is the puller's
// own: holding it blocks no other provider.
func (p *Puller) pullLock(uid int64) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pullLocks == nil {
		p.pullLocks = map[int64]*sync.Mutex{}
	}
	l, ok := p.pullLocks[uid]
	if !ok {
		l = &sync.Mutex{}
		p.pullLocks[uid] = l
	}
	return l
}

// sameLink: the link is still the one a pull started with (not unlinked
// and linked again, nor set to another login, meanwhile).
func sameLink(a, b accounts.Link) bool {
	return a.Nickname == b.Nickname && a.ExternalID == b.ExternalID && a.LinkedAt.Equal(b.LinkedAt)
}

func (p *Puller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Puller) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

func (p *Puller) userLock(uid int64) *sync.Mutex {
	if p.Lock != nil {
		return p.Lock(uid)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.locks == nil {
		p.locks = map[int64]*sync.Mutex{}
	}
	l, ok := p.locks[uid]
	if !ok {
		l = &sync.Mutex{}
		p.locks[uid] = l
	}
	return l
}

// localState maps a MyShows watch status to a mark state ("" = unknown).
func localState(status string) string {
	switch status {
	case "watching":
		return "watching"
	case "later":
		return "planned"
	case "finished":
		return "completed"
	case "cancelled":
		return "dropped"
	}
	return ""
}

// score maps a MyShows rating (1..5) to a mark score (2..10); 0 = none.
func score(rating int) *int {
	if rating < 1 || rating > 5 {
		return nil
	}
	v := rating * 2
	return &v
}

func live(state string) bool {
	return state == "planned" || state == "watching" || state == "on_hold"
}

func sameScore(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// PullOne reads the profile's MyShows list into its marks.
//
// MyShows is a read-only secondary source and gives no per-entry times, so
// the last seen status and rating of every list entry, mapped or not, are
// kept in myshows_entries. An entry is applied only when its status or
// rating differs from that row, or when it is new since the link (no row,
// on a pull after the first-link one); the mark then carries the pull time
// and origin "myshows". Every other mapped entry follows the keep rule: it
// fills a title with no mark and never replaces one. That covers the
// first-link pull, and an entry seen before but mapped only now (a lookup
// that failed, or a miss that resolved later). So a mark made here or by
// another provider stays until MyShows really changes.
//
// Mapped entries keep a twin in account_rates (provider myshows, target_id
// = show id) linking the show to its title. An entry gone from the list
// removes the mark only when it is still the one this puller wrote (origin
// myshows, same time); an empty list removes nothing. Two shows mapped to
// one title: the first one in list order that is to be applied sets the
// mark in a pull; both are recorded as seen.
//
// The list read and the show lookups (network) run without the profile's
// lock; it is taken only to write, and everything is read again under it
// (a link unlinked or linked anew meanwhile gets nothing written). Whole
// pulls of one profile run one at a time (a second one waits, then reads
// the list itself).
// A missing or private profile, a failed read, or three lookups in a row
// finding the service unavailable set the link's status and change no
// mark. The first-link pull counts as done only once no lookup failed.
func (p *Puller) PullOne(ctx context.Context, uid int64) (Result, error) {
	pl := p.pullLock(uid)
	pl.Lock()
	defer pl.Unlock()
	var res Result
	start, ok, err := p.Accounts.Link(ctx, uid, Provider)
	link := start
	if err != nil {
		return res, err
	}
	if !ok {
		return res, errors.New("myshows: profile not linked")
	}
	entries, err := p.Remote.ProfileShows(ctx, link.Nickname)
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		status, msg := accounts.StatusError, msgUnavailable
		switch {
		case errors.Is(err, ErrNotFound):
			status, msg = accounts.StatusNotFound, msgNotFound
		case errors.Is(err, ErrPrivate):
			status, msg = accounts.StatusPrivate, msgPrivate
		}
		return res, p.failed(ctx, uid, status, msg, err)
	}
	maps, err := p.resolve(ctx, uid, entries)
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		msg := msgLocal
		if errors.Is(err, errLookups) {
			msg = msgUnavailable
		}
		return res, p.failed(ctx, uid, accounts.StatusError, msg, err)
	}

	lk := p.userLock(uid)
	lk.Lock()
	defer lk.Unlock()
	// read again: the link may have changed while the list was read
	link, ok, err = p.Accounts.Link(ctx, uid, Provider)
	if err != nil {
		return res, err
	}
	if !ok || !sameLink(link, start) {
		// unlinked, or linked anew, meanwhile: this list is not its
		return res, errors.New("myshows: link changed during the pull")
	}
	now := p.now().Truncate(time.Second) // stored times are whole seconds
	res, resolveErr, err := p.apply(ctx, link, entries, maps, now)
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if serr := p.Accounts.SetStatus(ctx, uid, Provider, accounts.StatusError, msgLocal, now); serr != nil {
			p.log().Warn("myshows: status", "user", uid, "err", serr)
		}
		return res, err
	}
	if err := p.Accounts.SetUnmapped(ctx, uid, Provider, res.Unmapped); err != nil {
		return res, err
	}
	if !link.FirstPullDone && !resolveErr {
		if err := p.Accounts.SetFirstPullDone(ctx, uid, Provider); err != nil {
			return res, err
		}
	}
	for _, m := range maps {
		if m.tmdb {
			// some titles could not be looked up: say so, and the next
			// pull (an hour on) tries again
			return res, p.Accounts.SetStatus(ctx, uid, Provider, accounts.StatusError, msgTMDB, now)
		}
	}
	return res, p.Accounts.SetStatus(ctx, uid, Provider, accounts.StatusOK, "", now)
}

// failed records a pull that changed nothing.
func (p *Puller) failed(ctx context.Context, uid int64, status, msg string, err error) error {
	if serr := p.Accounts.SetStatus(ctx, uid, Provider, status, msg, p.now().Truncate(time.Second)); serr != nil {
		return serr
	}
	return err
}

// mapping is the outcome of one show lookup.
type mapping struct {
	tmdbID int64
	kind   string
	ok     bool
	failed bool // the lookup errored (not cached: tried again next pull)
	tmdb   bool // ... it was TMDB that failed
}

// resolve looks up the list's shows that have no twin yet (they are known
// already). It writes nothing but the map cache.
func (p *Puller) resolve(ctx context.Context, uid int64, entries []Entry) (map[int64]mapping, error) {
	twinList, err := p.Accounts.Rates(ctx, uid, Provider)
	if err != nil {
		return nil, err
	}
	twins := map[int64]bool{}
	for _, r := range twinList {
		twins[r.TargetID] = true
	}
	out := map[int64]mapping{}
	fails := 0
	for _, e := range entries {
		if twins[e.ShowID] || localState(e.Status) == "" {
			continue
		}
		if _, done := out[e.ShowID]; done {
			continue
		}
		tmdbID, kind, ok, err := p.Map.Resolve(ctx, e.ShowID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			p.log().Warn("myshows: map show", "show", e.ShowID, "err", err)
			if Unavailable(err) {
				if fails++; fails >= maxLookupFailures {
					return nil, fmt.Errorf("%w: %v", errLookups, err)
				}
			}
			out[e.ShowID] = mapping{failed: true, tmdb: errors.Is(err, ErrFind)}
			continue
		}
		fails = 0
		out[e.ShowID] = mapping{tmdbID: tmdbID, kind: kind, ok: ok}
	}
	return out, nil
}

type seen struct {
	status string
	score  int
}

func (p *Puller) seenEntries(ctx context.Context, uid int64) (map[int64]seen, error) {
	rows, err := p.Accounts.DB.QueryContext(ctx, `SELECT show_id, status, score FROM myshows_entries WHERE user_id=?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]seen{}
	for rows.Next() {
		var id int64
		var s seen
		if err := rows.Scan(&id, &s.status, &s.score); err != nil {
			return nil, err
		}
		out[id] = s
	}
	return out, rows.Err()
}

func (p *Puller) setSeen(ctx context.Context, uid int64, e Entry, now time.Time) error {
	_, err := p.Accounts.DB.ExecContext(ctx, `INSERT INTO myshows_entries(user_id, show_id, status, score, first_seen) VALUES(?,?,?,?,?)
		ON CONFLICT(user_id, show_id) DO UPDATE SET status=excluded.status, score=excluded.score`,
		uid, e.ShowID, e.Status, e.Rating, now.Unix())
	return err
}

// apply writes a pull under the profile's lock. resolveErr: an entry's
// lookup failed (the first-link pull is not done yet).
func (p *Puller) apply(ctx context.Context, link accounts.Link, entries []Entry, maps map[int64]mapping, now time.Time) (res Result, resolveErr bool, err error) {
	uid := link.UserID
	first := !link.FirstPullDone
	twinList, err := p.Accounts.Rates(ctx, uid, Provider)
	if err != nil {
		return res, false, err
	}
	twins := map[int64]accounts.Rate{}
	for _, r := range twinList {
		twins[r.TargetID] = r
	}
	rows, err := p.seenEntries(ctx, uid)
	if err != nil {
		return res, false, err
	}
	markList, err := p.Tracker.Marks(ctx, uid)
	if err != nil {
		return res, false, err
	}
	marks := map[int64]accounts.Mark{}
	for _, m := range markList {
		marks[m.TitleID] = m
	}

	listed := map[int64]bool{}      // show ids in the list
	listedTitle := map[int64]bool{} // titles of listed shows
	for _, e := range entries {
		listed[e.ShowID] = true
		if tw, ok := twins[e.ShowID]; ok {
			listedTitle[tw.TitleID] = true
		}
	}
	applied := map[int64]bool{} // titles set by this pull (two shows may map to one)
	for _, e := range entries {
		row, wasSeen := rows[e.ShowID]
		changed := wasSeen && (row.status != e.Status || row.score != e.Rating)
		tw, known := twins[e.ShowID]
		state := localState(e.Status)
		if state == "" {
			p.log().Warn("myshows: unknown watch status", "user", uid, "status", e.Status)
			res.Unmapped++
			if err := p.setSeen(ctx, uid, e, now); err != nil {
				return res, false, err
			}
			continue
		}
		tid := tw.TitleID
		if !known {
			m, ok := maps[e.ShowID]
			if !ok || m.failed {
				// not mapped this time: a change stays detectable (the row
				// keeps its old state); an entry of the first-link pull is
				// recorded so it never reads as new later
				resolveErr = true
				if !wasSeen && first {
					if err := p.setSeen(ctx, uid, e, now); err != nil {
						return res, false, err
					}
				}
				continue
			}
			if !m.ok {
				res.Unmapped++
				if err := p.setSeen(ctx, uid, e, now); err != nil {
					return res, false, err
				}
				continue
			}
			name := e.Title
			if name == "" {
				name = e.TitleOriginal
			}
			t, err := p.Tracker.EnsureTitle(ctx, m.kind, m.tmdbID, name)
			if err != nil {
				return res, false, err
			}
			tid = t.ID
			listedTitle[tid] = true
		}
		// apply: a real MyShows change, or an entry new since the link;
		// keep: fill the title only when it has no mark
		apply := changed || !known && !wasSeen && !first
		keep := !apply && !known
		if (apply || keep) && !applied[tid] {
			applied[tid] = true
			st, sc := state, score(e.Rating)
			local, has := marks[tid]
			// read again: a change made here while this pull ran stays
			cur, curHas, merr := p.Tracker.Mark(ctx, uid, tid)
			if merr != nil {
				return res, false, merr
			}
			unchanged := curHas == has && (!has || cur.UpdatedAt.Equal(local.UpdatedAt))
			if unchanged && !(keep && has) && (!has || local.State != st || !sameScore(local.Score, sc)) {
				mk := accounts.Mark{UserID: uid, TitleID: tid, State: st, Score: sc, UpdatedAt: now, Origin: Provider}
				if err := p.Tracker.SetMark(ctx, mk); err != nil {
					return res, false, err
				}
				marks[tid] = mk
				tw.MarkAt = now // the mark this puller wrote (see removal)
				if has {
					res.Updated++
				} else {
					res.Imported++
				}
				if live(st) && p.Kick != nil {
					p.Kick(tid)
				}
			}
		}
		if !known || changed || tw.MarkAt.Equal(now) || tw.RemoteEpisodes != e.WatchedEpisodes || tw.AnimeEpisodes != e.TotalEpisodes {
			at := tw.RemoteUpdatedAt
			if changed || !known {
				at = now
			}
			if err := p.Accounts.SetRate(ctx, accounts.Rate{UserID: uid, Provider: Provider, TargetID: e.ShowID, TitleID: tid,
				RemoteStatus: e.Status, RemoteEpisodes: e.WatchedEpisodes, AnimeEpisodes: e.TotalEpisodes,
				RemoteUpdatedAt: at, MarkAt: tw.MarkAt}); err != nil {
				return res, false, err
			}
		}
		if !wasSeen || changed {
			if err := p.setSeen(ctx, uid, e, now); err != nil {
				return res, false, err
			}
		}
	}

	// entries gone from the list (an empty list is never read as
	// "everything removed")
	if len(entries) == 0 {
		return res, resolveErr, nil
	}
	for _, tw := range twinList {
		if listed[tw.TargetID] {
			continue
		}
		if !listedTitle[tw.TitleID] && !tw.MarkAt.IsZero() {
			// read again: only the mark this puller wrote goes
			m, ok, err := p.Tracker.Mark(ctx, uid, tw.TitleID)
			if err != nil {
				return res, false, err
			}
			if ok && m.Origin == Provider && m.UpdatedAt.Equal(tw.MarkAt) {
				if err := p.Tracker.DeleteMark(ctx, uid, tw.TitleID, Provider); err != nil {
					return res, false, err
				}
				res.Updated++
			}
		}
		if err := p.Accounts.DeleteRate(ctx, uid, Provider, tw.TargetID); err != nil {
			return res, false, err
		}
	}
	for id := range rows {
		if !listed[id] {
			if _, err := p.Accounts.DB.ExecContext(ctx, `DELETE FROM myshows_entries WHERE user_id=? AND show_id=?`, uid, id); err != nil {
				return res, false, err
			}
		}
	}
	return res, resolveErr, nil
}

// Run pulls every linked profile once an hour (a new link on the next
// minute's tick) until ctx ends. With no links a tick costs one query.
func (p *Puller) Run(ctx context.Context) {
	t := time.NewTicker(runTick)
	defer t.Stop()
	for {
		p.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *Puller) tick(ctx context.Context) {
	links, err := p.Accounts.Links(ctx, Provider)
	if err != nil {
		if ctx.Err() == nil {
			p.log().Warn("myshows: links", "err", err)
		}
		return
	}
	for _, l := range links {
		if ctx.Err() != nil {
			return
		}
		if l.Nickname == "" || !l.LastSync.IsZero() && p.now().Before(l.LastSync.Add(pullEvery)) {
			continue
		}
		if r, err := p.PullOne(ctx, l.UserID); err != nil {
			if ctx.Err() == nil {
				p.log().Info("myshows: pull", "user", l.UserID, "err", err)
			}
		} else if r.Imported+r.Updated > 0 {
			p.log().Info("myshows: pulled", "user", l.UserID, "imported", r.Imported, "updated", r.Updated, "unmapped", r.Unmapped)
		}
	}
}
