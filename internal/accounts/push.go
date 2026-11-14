package accounts

import (
	"context"
	"errors"
	"math"
	"time"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/animemap"
)

// ErrRemoteUnauthorized: the remote rejected the access or refresh token
// (revoked, expired); a new sign-in is needed.
var ErrRemoteUnauthorized = errors.New("accounts: remote sign-in rejected")

// StatusRelogin: the link's tokens were rejected; pushes wait for a new
// sign-in (the outbox is kept).
const StatusRelogin = "relogin"

const reloginMsg = "Нужно войти в Shikimori заново"

// OAuthClient is the provider's OAuth client registration.
type OAuthClient struct {
	ID, Secret, RedirectURI string
}

// RateWrite is what a push sets on a remote list entry. Nil pointers are
// left as they are; Score 0 means "no score".
type RateWrite struct {
	Status          string
	Score, Episodes *int
}

// RateWritten is the remote's answer to a write.
type RateWritten struct {
	ID        int64
	UpdatedAt time.Time // zero when the answer lacks it
}

// Writer writes a profile's remote list with its access token. A rejected
// token is ErrRemoteUnauthorized, an entry gone ErrRemoteNotFound.
type Writer interface {
	// Refresh trades a refresh token for new tokens (the remote rotates
	// the refresh token: the returned one replaces it).
	Refresh(ctx context.Context, c OAuthClient, refresh string) (Tokens, error)
	CreateRate(ctx context.Context, access string, userID, animeID int64, w RateWrite) (RateWritten, error)
	UpdateRate(ctx context.Context, access string, rateID int64, w RateWrite) (RateWritten, error)
	DeleteRate(ctx context.Context, access string, rateID int64) error
	// RateUpdatedAt reads an entry's updated_at back.
	RateUpdatedAt(ctx context.Context, access string, rateID int64) (time.Time, error)
	// AnimeEpisodes is a remote title's episode count (0 = unknown).
	AnimeEpisodes(ctx context.Context, animeID int64) (int, error)
}

// Episode is one episode of a title's known episode list.
type Episode struct {
	Season, Number int
	AirDate        string // YYYY-MM-DD, "" = unknown
}

// PushResult counts what a push did.
type PushResult struct {
	Pushed  int // outbox items written
	Skipped int // items with no remote anime to write to
	Failed  int // items left for a retry
}

// refreshAhead: tokens expiring sooner are refreshed before use.
const refreshAhead = 5 * time.Minute

var errUnpushable = errors.New("accounts: no remote title to write")

// backoff is the wait after the attempts-th failure (1 min doubling, at
// most 6 h).
func backoff(attempts int) time.Duration {
	return min(time.Minute<<min(attempts, 10), 6*time.Hour)
}

// shortErr is an error message short enough to store and show.
func shortErr(err error) string {
	msg := err.Error()
	if utf8.RuneCountInString(msg) <= 120 {
		return msg
	}
	r := []rune(msg)
	return string(r[:120]) + "…"
}

// PushOne writes the profile's due outbox items to its Shikimori list:
// only with write-back enabled and tokens stored. Tokens expiring within
// 5 minutes are refreshed first (and once more after a rejected write),
// the rotated ones stored before use; pushes and pulls of one profile are
// serialised. A token rejected after the refresh sets the link to
// «relogin» and keeps the outbox; other failures wait with a backoff.
func (s *Syncer) PushOne(ctx context.Context, uid int64) error {
	r, err := s.push(ctx, uid)
	if r.Pushed+r.Skipped+r.Failed > 0 {
		s.log().Info("accounts: pushed", "user", uid, "pushed", r.Pushed, "skipped", r.Skipped, "failed", r.Failed)
	}
	return err
}

func (s *Syncer) push(ctx context.Context, uid int64) (PushResult, error) {
	var res PushResult
	if s.Writer == nil {
		return res, nil
	}
	lk := s.userLock(uid)
	lk.Lock()
	defer lk.Unlock()
	link, ok, err := s.Accounts.Link(ctx, uid, Shikimori)
	if err != nil || !ok || link.WriteState != "enabled" || link.Status == StatusRelogin || link.ExternalID == 0 || !link.FirstPullDone {
		// nothing goes out before the remote list was imported: a push
		// could overwrite entries not seen yet
		return res, err
	}
	items, err := s.Accounts.Outbox(ctx, uid, Shikimori)
	if err != nil {
		return res, err
	}
	now := s.now()
	var due []OutboxItem
	for _, it := range items {
		if !it.NextAt.After(now) {
			due = append(due, it)
		}
	}
	if len(due) == 0 {
		return res, nil
	}
	tok, ok, err := s.Accounts.Tokens(ctx, uid, Shikimori)
	if err != nil || !ok || tok.Access == "" {
		return res, err // not signed in yet
	}
	refreshed := false
	if !tok.Expires.IsZero() && tok.Expires.Before(now.Add(refreshAhead)) {
		if tok, err = s.refresh(ctx, uid, tok); err != nil {
			return res, s.authFailed(ctx, link, err)
		}
		refreshed = true
	}
	for _, it := range due {
		perr := s.pushItem(ctx, link, tok.Access, it)
		if errors.Is(perr, ErrRemoteUnauthorized) && !refreshed {
			refreshed = true
			if tok, err = s.refresh(ctx, uid, tok); err != nil {
				return res, s.authFailed(ctx, link, err)
			}
			perr = s.pushItem(ctx, link, tok.Access, it)
		}
		switch {
		case perr == nil || errors.Is(perr, errUnpushable):
			if err := s.Accounts.DoneAt(ctx, it.ID, it.Seq); err != nil {
				return res, err
			}
			if perr == nil {
				res.Pushed++
			} else {
				res.Skipped++
			}
		case errors.Is(perr, ErrRemoteUnauthorized):
			return res, s.authFailed(ctx, link, perr)
		case ctx.Err() != nil:
			return res, ctx.Err()
		default:
			if err := s.Accounts.Failed(ctx, it.ID, shortErr(perr), now.Add(backoff(it.Attempts))); err != nil {
				return res, err
			}
			res.Failed++
		}
	}
	return res, nil
}

// refresh trades the refresh token for new tokens and stores them.
func (s *Syncer) refresh(ctx context.Context, uid int64, tok Tokens) (Tokens, error) {
	if tok.Refresh == "" {
		return tok, ErrRemoteUnauthorized
	}
	if s.OAuth == nil {
		return tok, errors.New("accounts: no OAuth client configured")
	}
	c, ok, err := s.OAuth.Client(ctx)
	if err != nil {
		return tok, err
	}
	if !ok {
		return tok, errors.New("accounts: no OAuth client configured")
	}
	nt, err := s.Writer.Refresh(ctx, c, tok.Refresh)
	if err != nil {
		return tok, err
	}
	if nt.Refresh == "" {
		nt.Refresh = tok.Refresh
	}
	if err := s.Accounts.SetTokens(ctx, uid, Shikimori, nt); err != nil {
		return tok, err
	}
	return nt, nil
}

// authFailed records a rejected token as «relogin»; other errors pass.
func (s *Syncer) authFailed(ctx context.Context, link Link, err error) error {
	if errors.Is(err, ErrRemoteUnauthorized) {
		if serr := s.Accounts.SetStatus(ctx, link.UserID, Shikimori, StatusRelogin, reloginMsg, link.LastSync); serr != nil {
			return serr
		}
	}
	return err
}

// deletable: a delete here removes the cour from the remote list (a
// completed or rewatched one, or one of unknown status, keeps its history).
func deletable(r Rate) bool {
	switch r.RemoteStatus {
	case "planned", "watching", "on_hold", "dropped":
		return true
	}
	return r.RateID == 0
}

// pushItem writes one outbox item: a delete removes the title's cours
// that are not completed or rewatched, an upsert writes what planCours
// picks.
func (s *Syncer) pushItem(ctx context.Context, link Link, access string, it OutboxItem) error {
	uid := link.UserID
	rates, err := s.Accounts.TitleRates(ctx, uid, Shikimori, it.TitleID)
	if err != nil {
		return err
	}
	if it.Op == "delete" {
		for _, r := range rates {
			if !deletable(r) {
				continue // its twin stays: the next pull sees no change in it
			}
			if r.RateID != 0 {
				if err := s.Writer.DeleteRate(ctx, access, r.RateID); err != nil && !errors.Is(err, ErrRemoteNotFound) {
					return err
				}
			}
			if err := s.Accounts.DeleteRate(ctx, uid, Shikimori, r.TargetID); err != nil {
				return err
			}
		}
		return nil
	}
	m, ok, err := s.Tracking.Mark(ctx, uid, it.TitleID)
	if err != nil || !ok {
		return err // gone since: its delete is queued
	}
	t, ok, err := s.Tracking.Title(ctx, it.TitleID)
	if err != nil {
		return err
	}
	if !ok {
		return errUnpushable
	}
	if len(rates) == 0 {
		// not in the remote list yet: the anime standing for the title
		e, ok, err := s.Map.FirstAnime(ctx, t.Kind, t.TMDBID)
		if err != nil {
			return err
		}
		if !ok {
			if t.ShikimoriID == 0 {
				return errUnpushable
			}
			e = animemap.Entry{MALID: t.ShikimoriID, Season: 1}
			if t.Kind == "movie" {
				e.Season = 0
			}
		}
		r := Rate{UserID: uid, Provider: Shikimori, TargetID: e.MALID, TitleID: it.TitleID, TMDBSeason: e.Season, TMDBOffset: e.Offset}
		// its length bounds the cour's window (unknown: unbounded until a
		// pull tells)
		if n, err := s.Writer.AnimeEpisodes(ctx, e.MALID); err == nil {
			r.AnimeEpisodes = n
		} else if ctx.Err() != nil {
			return ctx.Err()
		} else {
			s.log().Info("accounts: anime episodes", "anime", e.MALID, "err", err)
		}
		rates = []Rate{r}
	}
	var watched map[[2]int]bool
	if s.Watched != nil {
		if watched, err = s.Watched(ctx, uid, t); err != nil {
			return err
		}
	}
	var eps []Episode
	if s.Episodes != nil && t.Kind != "movie" {
		if eps, err = s.Episodes(ctx, it.TitleID); err != nil {
			return err
		}
	}
	for _, cw := range planCours(t.Kind, m, rates, watched, eps, s.now().Format(time.DateOnly)) {
		if err := s.writeCour(ctx, link, access, cw); err != nil {
			return err // mark_at stays: the retry still writes the mark
		}
	}
	// the remote list now holds this mark: until it changes here, pushes
	// write episodes (and status upgrades) only
	return s.Accounts.SetTitleMarkAt(ctx, uid, Shikimori, it.TitleID, m.UpdatedAt)
}

// writeCour creates or updates one remote entry and records its twin with
// pushed_at = the remote's updated_at, so the next pull knows its echo. An
// entry gone remotely is created again only when the mark has a status to
// write to it; otherwise its twin is forgotten (the next pull settles it).
func (s *Syncer) writeCour(ctx context.Context, link Link, access string, cw courWrite) error {
	r := cw.rate
	var out RateWritten
	var err error
	if r.RateID != 0 {
		out, err = s.Writer.UpdateRate(ctx, access, r.RateID, cw.w)
		if out.ID == 0 {
			out.ID = r.RateID
		}
	}
	gone := errors.Is(err, ErrRemoteNotFound)
	if gone && cw.w.Status == "" {
		return s.Accounts.DeleteRate(ctx, link.UserID, Shikimori, r.TargetID)
	}
	created := r.RateID == 0 || gone
	if created {
		out, err = s.Writer.CreateRate(ctx, access, link.ExternalID, r.TargetID, cw.w)
	}
	if err != nil {
		return err
	}
	at := out.UpdatedAt
	if at.IsZero() {
		if at, err = s.Writer.RateUpdatedAt(ctx, access, out.ID); err != nil || at.IsZero() {
			at = s.now()
		}
	}
	r.RateID, r.PushedAt = out.ID, at.Truncate(time.Second)
	if cw.w.Status != "" {
		r.RemoteStatus = cw.w.Status
	}
	if created {
		r.RemoteEpisodes, r.LocalEpisodes = 0, 0
	}
	if cw.w.Episodes != nil {
		r.RemoteEpisodes, r.LocalEpisodes = *cw.w.Episodes, cw.n
	}
	return s.Accounts.SetRate(ctx, r)
}

// courWrite is one remote entry to write, what to write to it, and the
// watched episodes of its window here (n).
type courWrite struct {
	rate Rate
	w    RateWrite
	n    int
}

func (cw courWrite) empty() bool {
	return cw.w.Status == "" && cw.w.Score == nil && cw.w.Episodes == nil
}

// kept: a remote cour completed or rewatched keeps its status whatever the
// mark says (only its score may change).
func kept(r Rate) bool {
	return r.RemoteStatus == "completed" || r.RemoteStatus == "rewatching"
}

// airing: the cour's anime is announced or still airing (never completed).
func airing(r Rate) bool {
	return r.AnimeStatus == "anons" || r.AnimeStatus == "ongoing"
}

// syncedAt is the mark time a title's last pull win or push recorded
// (mark_at, the latest over its cours: a cour added since has none).
func syncedAt(rates []Rate) time.Time {
	var at time.Time
	for _, r := range rates {
		if r.MarkAt.After(at) {
			at = r.MarkAt
		}
	}
	return at
}

// markNewer: the mark changed here since the title's last pull win or push
// (its time is not the one recorded then, synced), or the cour is not in
// the remote list yet. Only then are a cour's status and score written:
// watched episodes alone never rewrite them. Only our own record is
// compared, never the remote's updated_at, so a skewed remote clock does
// not matter. Times are whole seconds: a mark changed again within the
// second of the time recorded (a pulled or pushed one) reads as unchanged
// until its next change; a differing time, earlier or later, is a change
// (a mark set here after one pulled with a future remote time is earlier).
func markNewer(m Mark, r Rate, synced time.Time) bool {
	if r.RateID == 0 {
		return true
	}
	return m.UpdatedAt.Unix() != synced.Unix()
}

// upgradable: a cour a push without a mark change may move up (planned or
// watching; never one on hold, dropped, completed or rewatched).
func upgradable(r Rate) bool {
	return r.RemoteStatus == "planned" || r.RemoteStatus == "watching"
}

// pushEpisodes is the episode count to send a cour with n of its window
// watched here (nil: leave the remote's). A new entry gets n when above 0.
// An existing one gets n only above its remote count, or below it when n
// dropped since our last write and the remote still holds that write's
// count (the person unmarked episodes here); a count advanced elsewhere
// is never lowered.
// A cour kept remotely (completed, or a rewatch in progress) gets none:
// what was watched here says nothing of a rewatch.
func pushEpisodes(r Rate, n int) *int {
	if kept(r) {
		return nil
	}
	if r.RateID == 0 {
		if n > 0 {
			return &n
		}
		return nil
	}
	rem, prev := r.RemoteEpisodes, r.LocalEpisodes
	if n > rem || n < rem && n < prev && rem == prev {
		return &n
	}
	return nil
}

// completedEpisodes is the episode count a cour being completed gets: the
// anime's length when known and above the remote count, else as
// pushEpisodes.
func completedEpisodes(r Rate, n int) *int {
	if r.AnimeEpisodes > 0 && r.AnimeEpisodes > r.RemoteEpisodes {
		v := r.AnimeEpisodes
		return &v
	}
	return pushEpisodes(r, n)
}

// planCours picks what a mark writes to a title's cours (rates, in season
// and offset order). Episode counts follow pushEpisodes on every cour
// considered; status and score go to a cour only when the mark changed
// since the title's last sync (markNewer), and a cour completed or
// rewatched remotely keeps its status (kept). Without a mark change a
// cour's status only moves up: one planned or watching (not airing) whose
// every episode is watched here is completed (an earlier cour, or any cour
// of a series marked completed), and the current cour, planned, is set
// watching once episodes go to it; no score is sent.
//
// A series marked completed completes every cour of it in the remote list
// except kept and airing ones (episodes: the anime's length when above the
// remote count); the score goes to the last finished cour; no entry is
// created but the first one of a title the list lacks.
//
// Any other mark: the cour holding the next unwatched aired episode (else
// the last watched one, else the first episode), moved past cours kept
// remotely (past a rewatched one only when the mark is not «watching»),
// gets the mark's state and score; the cours before it are completed
// (without a next aired episode only those whose every episode is
// watched); later cours are not touched. A cour's episodes are the watched
// episodes in its window: TMDB season TMDBSeason, episodes TMDBOffset+1 ..
// TMDBOffset+AnimeEpisodes (unknown: up to the next cour of that season).
// A movie's entries all get the mark, episodes 1 once watched.
func planCours(kind string, m Mark, rates []Rate, watched map[[2]int]bool, eps []Episode, today string) []courWrite {
	score := 0
	if m.Score != nil {
		score = *m.Score
	}
	synced := syncedAt(rates)
	var out []courWrite
	add := func(cw courWrite) {
		if !cw.empty() {
			out = append(out, cw)
		}
	}
	if kind == "movie" {
		n := 0
		if watched[[2]int{0, 0}] {
			n = 1
		}
		for _, r := range rates {
			cw := courWrite{rate: r, n: n, w: RateWrite{Episodes: pushEpisodes(r, n)}}
			if markNewer(m, r, synced) {
				if !kept(r) {
					cw.w.Status = m.State
					if m.State == "completed" {
						cw.w.Episodes = completedEpisodes(r, n)
					}
				}
				cw.w.Score = &score
			}
			add(cw)
		}
		return out
	}
	// windows: (lo, hi] of TMDB episode numbers in the cour's season
	hi := make([]int, len(rates))
	for i, r := range rates {
		hi[i] = math.MaxInt
		if r.AnimeEpisodes > 0 {
			hi[i] = r.TMDBOffset + r.AnimeEpisodes
		}
		for _, x := range rates[i+1:] {
			if x.TMDBSeason == r.TMDBSeason && x.TMDBOffset > r.TMDBOffset {
				hi[i] = min(hi[i], x.TMDBOffset)
				break
			}
		}
	}
	in := func(i int, k [2]int) bool {
		return k[0] == rates[i].TMDBSeason && k[1] > rates[i].TMDBOffset && k[1] <= hi[i]
	}
	count := func(i int) int {
		n := 0
		for k, ok := range watched {
			if ok && in(i, k) {
				n++
			}
		}
		if a := rates[i].AnimeEpisodes; a > 0 {
			n = min(n, a)
		}
		return n
	}

	// allWatched: n watched episodes are all of cour i (its length, else
	// the known episodes in its window)
	allWatched := func(i, n int) bool {
		size := 0
		if hi[i] != math.MaxInt {
			size = hi[i] - rates[i].TMDBOffset
		} else {
			for _, e := range eps {
				if in(i, [2]int{e.Season, e.Number}) {
					size++
				}
			}
		}
		return size > 0 && n >= size
	}
	if m.State == "completed" {
		// the score goes to the last finished cour (the last one when all
		// are airing): an announced or airing cour is not scored for it
		dec := len(rates) - 1
		for i := len(rates) - 1; i >= 0; i-- {
			if !airing(rates[i]) {
				dec = i
				break
			}
		}
		for i, r := range rates {
			n := count(i)
			cw := courWrite{rate: r, n: n, w: RateWrite{Episodes: pushEpisodes(r, n)}}
			newer := markNewer(m, r, synced)
			if newer && !kept(r) && !airing(r) || !newer && upgradable(r) && !airing(r) && allWatched(i, n) {
				cw.w.Status = "completed"
				cw.w.Episodes = completedEpisodes(r, n)
			}
			if i == dec && newer {
				cw.w.Score = &score
			}
			add(cw)
		}
		return out
	}

	// the episode the profile is at
	target, found := [2]int{}, false
	for _, e := range eps {
		k := [2]int{e.Season, e.Number}
		if e.Season > 0 && e.AirDate != "" && e.AirDate <= today && !watched[k] {
			target, found = k, true
			break
		}
	}
	// no next aired episode: the profile's place comes from what it watched
	fromWatched := !found
	if !found {
		for k, ok := range watched {
			if ok && k[0] > 0 && (!found || k[0] > target[0] || k[0] == target[0] && k[1] > target[1]) {
				target, found = k, true
			}
		}
	}
	if !found {
		target = [2]int{1, 1}
	}
	cur := -1
	for i := range rates {
		if in(i, target) {
			cur = i
		}
	}
	if cur < 0 {
		// past every window, or between them: the last cour before it
		cur = 0
		for i, r := range rates {
			if r.TMDBSeason < target[0] || r.TMDBSeason == target[0] && r.TMDBOffset < target[1] {
				cur = i
			}
		}
	}
	// a cour completed remotely is never demoted: the mark goes to the
	// next one (a rewatched one keeps a «watching» mark)
	for cur < len(rates) && (rates[cur].RemoteStatus == "completed" || rates[cur].RemoteStatus == "rewatching" && m.State != "watching") {
		cur++
	}
	for i, r := range rates[:min(cur+1, len(rates))] {
		n := count(i)
		cw := courWrite{rate: r, n: n, w: RateWrite{Episodes: pushEpisodes(r, n)}}
		newer := markNewer(m, r, synced)
		switch {
		case i < cur && newer:
			if !kept(r) && (!fromWatched || allWatched(i, n)) {
				cw.w.Status = "completed"
				cw.w.Episodes = completedEpisodes(r, n)
			}
		case i < cur:
			// episodes only: a cour watched through here is finished
			if upgradable(r) && !airing(r) && allWatched(i, n) {
				cw.w.Status = "completed"
				cw.w.Episodes = completedEpisodes(r, n)
			}
		case newer:
			if !kept(r) {
				cw.w.Status = m.State
			}
			cw.w.Score = &score
		default:
			// episodes only: a planned cour being watched here is started
			if r.RemoteStatus == "planned" && cw.w.Episodes != nil && *cw.w.Episodes > 0 {
				cw.w.Status = "watching"
			}
		}
		add(cw)
	}
	return out
}

// EpisodesChanged is the core's episode-watched subscriber: a profile's
// watched episodes of a title changed, so its marked anime title is queued
// for the remote list (with its new episode counts). Never a delete.
func (s *Syncer) EpisodesChanged(ctx context.Context, uid, titleID int64) {
	if err := s.episodesChanged(ctx, uid, titleID); err != nil {
		s.log().Warn("accounts: queue episodes", "user", uid, "title", titleID, "err", err)
	}
}

func (s *Syncer) episodesChanged(ctx context.Context, uid, titleID int64) error {
	l, ok, err := s.Accounts.Link(ctx, uid, Shikimori)
	if err != nil || !ok || !writesQueued(l) {
		return err
	}
	if _, ok, err := s.Tracking.Mark(ctx, uid, titleID); err != nil || !ok {
		return err
	}
	t, ok, err := s.Tracking.Title(ctx, titleID)
	if err != nil || !ok {
		return err
	}
	if ok, err := s.mappable(ctx, t); err != nil || !ok {
		return err
	}
	return s.Accounts.Enqueue(ctx, uid, Shikimori, titleID, "upsert", s.now())
}
