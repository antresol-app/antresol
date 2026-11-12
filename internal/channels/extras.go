package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// Titles outside the library («ТВ» item G): a channel whose Rules.Extras is
// on also plays titles of its genres the library does not have, found and
// played by Service.Extras. This file is the neutral seam: a build without
// an Extras implementation (Service.Extras nil) has no such titles, and
// the rule changes nothing there.

// ExtraTitle is a title from outside the library a channel may play.
type ExtraTitle struct {
	// Key is the implementation's stable identity of the title (one per
	// channel; the same key on two channels is the same title).
	Key string
	// Kind is library.KindMovie or library.KindEpisode.
	Kind   string
	TMDBID int
	Title  string
	Year   int
	// Overview and Rating (the age rating, "" unknown) in the server's
	// metadata language; Genres are display names.
	Overview string
	Rating   string
	Genres   []string
	// Poster and Backdrop are TMDB image paths ("/abc.jpg"; "" none).
	Poster, Backdrop string
	// RuntimeMs is how long the programme lasts in the schedule.
	RuntimeMs int64
	// Season and Episode of an episode (0 otherwise).
	Season, Episode int
}

// Stream is what an ExtraTitle plays: a URL ffmpeg reads (it must be a
// loopback http URL: media.LiveInput.Check) with the request headers it
// needs. Release, when set, is called once the programme is over or was
// not played.
type Stream struct {
	URL     string
	Headers map[string]string
	Release func()
}

// Extras finds and plays titles outside the library for channels.
type Extras interface {
	// Ready says whether extras work now (configured and switched on).
	Ready(ctx context.Context) bool
	// Candidates are up to limit titles for rules that the library does
	// not have.
	Candidates(ctx context.Context, rules Rules, limit int) ([]ExtraTitle, error)
	// Resolve finds a stream of t for channel ch, shortly before it airs.
	Resolve(ctx context.Context, ch int64, t ExtraTitle) (Stream, error)
}

const (
	// extrasTTL: a channel's candidates are asked for again after a day,
	// extrasRetry after a failed ask.
	extrasTTL   = 24 * time.Hour
	extrasRetry = time.Hour
	// extrasMax bounds a channel's candidates.
	extrasMax = 60
	// extrasAskTimeout bounds one ask for candidates.
	extrasAskTimeout = 30 * time.Second
	// extraFailedFor: a candidate that did not play is left out of new
	// schedules this long.
	extraFailedFor = 24 * time.Hour
)

// ExtrasOn: the rules take titles outside the library — the rule is on,
// the channel is not a hand-picked list or an ordered one (an extra would
// break its order), and it shows movies.
func ExtrasOn(r Rules) bool {
	return r.Extras && len(r.Titles) == 0 && len(r.Picks) == 0 && r.Order != OrderOrdered && (len(r.Kinds) == 0 || contains(r.Kinds, library.KindMovie))
}

// SetExtras sets the implementation of titles outside the library (nil:
// none).
func (s *Service) SetExtras(e Extras) {
	s.Extras = e
	s.Builder.Extras = e
}

// ExtrasReady: this server can play titles outside the library now.
func (s *Service) ExtrasReady(ctx context.Context) bool {
	return s != nil && s.Extras != nil && s.Extras.Ready(ctx)
}

// extraUnit is the unit id of a title outside the library: negative (a
// library unit is positive) and the same for the same key on every
// channel, so the builder keeps it off two channels at once.
func extraUnit(key string) int64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return -int64(h.Sum64()&(1<<52-1)) - 1
}

// refreshExtras asks for c's candidates when they are a day old (an hour
// after a failed ask) and keeps them in channel_extras.
func (b *Builder) refreshExtras(ctx context.Context, c Channel, limit int) error {
	now := b.Store.now()
	var at int64
	if err := b.Store.DB.QueryRowContext(ctx, `SELECT extras_at_ms FROM channels WHERE id=?`, c.ID).Scan(&at); err != nil {
		return err
	}
	if at > 0 && now.Sub(fromMs(at)) < extrasTTL {
		return nil
	}
	actx, cancel := context.WithTimeout(ctx, extrasAskTimeout)
	ts, err := b.Extras.Candidates(actx, c.Rules, min(limit, extrasMax))
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("channels: channel %d: titles outside the library: %v", c.ID, err)
		_, err = b.Store.DB.ExecContext(ctx, `UPDATE channels SET extras_at_ms=? WHERE id=?`, ms(now.Add(extrasRetry-extrasTTL)), c.ID)
		return err
	}
	tx, err := b.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE channel_extras SET current=0 WHERE channel_id=?`, c.ID); err != nil {
		return err
	}
	stamp := now.UTC().Format(time.RFC3339)
	for _, t := range ts {
		if t.Key == "" || t.Title == "" || t.RuntimeMs <= 0 {
			continue
		}
		if t.Kind == "" {
			t.Kind = library.KindMovie
		}
		genres, _ := json.Marshal(t.Genres)
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_extras (channel_id, key, kind, tmdb_id, title, year, overview, rating, genres_json, poster, backdrop, runtime_ms, season, episode, current, fetched_at)
		  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?)
		  ON CONFLICT (channel_id, key) DO UPDATE SET kind=excluded.kind, tmdb_id=excluded.tmdb_id, title=excluded.title, year=excluded.year,
		    overview=excluded.overview, rating=excluded.rating, genres_json=excluded.genres_json, poster=excluded.poster, backdrop=excluded.backdrop,
		    runtime_ms=excluded.runtime_ms, season=excluded.season, episode=excluded.episode, current=1, fetched_at=excluded.fetched_at`,
			c.ID, t.Key, t.Kind, t.TMDBID, t.Title, t.Year, t.Overview, t.Rating, string(genres), t.Poster, t.Backdrop, t.RuntimeMs, t.Season, t.Episode, stamp); err != nil {
			return err
		}
	}
	// old candidates no schedule names any more go
	if _, err := tx.ExecContext(ctx, `DELETE FROM channel_extras WHERE channel_id=? AND current=0
	  AND id NOT IN (SELECT extra_id FROM channel_playout WHERE channel_id=? AND extra_id IS NOT NULL)`, c.ID, c.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channels SET extras_at_ms=? WHERE id=?`, ms(now), c.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// withExtras adds c's playable candidates to pool, at most as many as the
// library units (the picker then never airs two in a row: at most half
// of the programmes are from outside the library).
func (b *Builder) withExtras(ctx context.Context, c Channel, pool Pool) (Pool, error) {
	if b.Extras == nil || !ExtrasOn(c.Rules) || pool.Empty() {
		return pool, nil
	}
	if err := b.refreshExtras(ctx, c, len(pool.Units)); err != nil {
		if ctx.Err() != nil {
			return pool, err
		}
		log.Printf("channels: channel %d: titles outside the library: %v", c.ID, err)
	}
	rows, err := b.Store.DB.QueryContext(ctx, `SELECT id, key, title, year, rating, runtime_ms FROM channel_extras
	  WHERE channel_id=? AND current=1 AND runtime_ms > 0 AND failed_until_ms <= ? ORDER BY id LIMIT ?`, c.ID, ms(b.Store.now()), len(pool.Units))
	if err != nil {
		return pool, err
	}
	defer rows.Close()
	out := Pool{Units: append([]Unit(nil), pool.Units...), Progs: map[int64][]Prog{}}
	for k, v := range pool.Progs {
		out.Progs[k] = v
	}
	for rows.Next() {
		var id, dur int64
		var key string
		u := Unit{Kind: library.KindMovie}
		if err := rows.Scan(&id, &key, &u.Title, &u.Year, &u.Rating, &dur); err != nil {
			return pool, err
		}
		u.ID = extraUnit(key)
		if _, dup := out.Progs[u.ID]; dup {
			continue
		}
		u.Sort = u.Title
		out.Units = append(out.Units, u)
		out.Progs[u.ID] = []Prog{{ItemID: -id, UnitID: u.ID, ExtraID: id, Parts: []Part{{DurationMs: dur}}}}
	}
	return out, rows.Err()
}

// Extra is a programme of a schedule from outside the library.
func (s *Store) Extra(ctx context.Context, id int64) (ExtraTitle, error) {
	var t ExtraTitle
	var genres string
	err := s.DB.QueryRowContext(ctx, `SELECT key, kind, tmdb_id, title, year, overview, rating, genres_json, poster, backdrop, runtime_ms, season, episode
	  FROM channel_extras WHERE id=?`, id).Scan(&t.Key, &t.Kind, &t.TMDBID, &t.Title, &t.Year, &t.Overview, &t.Rating, &genres,
		&t.Poster, &t.Backdrop, &t.RuntimeMs, &t.Season, &t.Episode)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNothing
	}
	_ = json.Unmarshal([]byte(genres), &t.Genres)
	return t, err
}

// ExtraFailed leaves the candidate out of new schedules for a day and
// drops channel ch's schedule from entry seq on (the builder fills it
// again without it).
func (s *Store) ExtraFailed(ctx context.Context, ch, seq, extra int64) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE channel_extras SET failed_until_ms=? WHERE id=?`, ms(s.now().Add(extraFailedFor)), extra); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM channel_playout WHERE channel_id=? AND seq >= ?`, ch, seq)
	return err
}

const (
	// extraResolveTimeout bounds resolving a stream when its programme is
	// due; prefetchLead is how long before it starts it is resolved ahead.
	extraResolveTimeout = 20 * time.Second
	prefetchLead        = 2 * time.Minute
	// prefetchKeep: a stream resolved ahead that nobody took is released
	// this long after its programme started.
	prefetchKeep = 10 * time.Minute
	// maxExtraTries bounds the titles outside the library one Program call
	// tries before it skips the slot.
	maxExtraTries = 4
)

type entryKey struct{ ch, seq int64 }

// prefetched is a stream resolved ahead (done closed once st/err are set).
type prefetched struct {
	done  chan struct{}
	st    Stream
	err   error
	start time.Time
	// now wakes a prefetch still waiting for its time: the programme is
	// due already (the stream runs ahead of the schedule after a short one)
	now  chan struct{}
	once sync.Once
}

func (p *prefetched) hurry() { p.once.Do(func() { close(p.now) }) }

// resolveExtra resolves entry e's title.
func (s *Service) resolveExtra(ctx context.Context, e Entry) (Stream, error) {
	if s.Extras == nil {
		return Stream{}, errors.New("channels: no titles outside the library here")
	}
	t, err := s.Store.Extra(ctx, e.ExtraID)
	if err != nil {
		return Stream{}, err
	}
	rctx, cancel := context.WithTimeout(ctx, extraResolveTimeout)
	defer cancel()
	st, err := s.Extras.Resolve(rctx, e.Channel, t)
	if err == nil && st.URL == "" {
		err = errors.New("channels: no stream")
	}
	if err != nil && st.Release != nil {
		st.Release()
	}
	return st, err
}

// extraProgram is the Program of entry e (a title outside the library):
// its stream resolved ahead, else now. ok false: it would not play — it
// is left out for a day and the schedule made again from e on.
func (s *Service) extraProgram(ctx context.Context, e Entry) (Program, bool, error) {
	k := entryKey{e.Channel, e.Seq}
	s.pmu.Lock()
	pf := s.prefetch[k]
	delete(s.prefetch, k)
	s.pmu.Unlock()
	var st Stream
	var err error
	if pf != nil {
		pf.hurry()
		select {
		case <-pf.done:
			st, err = pf.st, pf.err
		case <-ctx.Done():
			return Program{}, false, ctx.Err()
		}
	} else {
		st, err = s.resolveExtra(ctx, e)
	}
	if err == nil {
		s.prefetchNext(e.Channel, e)
		return Program{Entry: e, URL: st.URL, Headers: st.Headers, Release: st.Release}, true, nil
	}
	if ctx.Err() != nil {
		return Program{}, false, ctx.Err()
	}
	log.Printf("channels: channel %d: the title outside the library (entry %d) would not play: %v", e.Channel, e.Seq, err)
	return Program{}, false, s.extraFailed(ctx, e)
}

// extraFailed leaves e's title out and builds the schedule again from e.
func (s *Service) extraFailed(ctx context.Context, e Entry) error {
	s.build.Lock()
	err := s.Store.ExtraFailed(ctx, e.Channel, e.Seq, e.ExtraID)
	s.build.Unlock()
	if err != nil {
		return err
	}
	return s.rebuild(ctx, e.Channel)
}

// prefetchNext resolves the programme after e ahead (prefetchLead before
// it starts) when it is a title outside the library: a source takes
// seconds to answer, more than the stream's lead. One that fails is left
// out at once, so the guide shows what will really play.
func (s *Service) prefetchNext(ch int64, e Entry) {
	if s.Extras == nil {
		return
	}
	ctx := context.Background()
	next, err := s.Store.At(ctx, ch, e.End)
	if err != nil || next.ExtraID == 0 || next.Seq <= e.Seq {
		return
	}
	k := entryKey{ch, next.Seq}
	now := s.Store.now()
	s.pmu.Lock()
	if s.prefetch == nil {
		s.prefetch = map[entryKey]*prefetched{}
	}
	for ok, old := range s.prefetch { // taken by nobody (the channel stopped)
		if now.Sub(old.start) > prefetchKeep {
			delete(s.prefetch, ok)
			go func(p *prefetched) {
				<-p.done
				if p.st.Release != nil {
					p.st.Release()
				}
			}(old)
		}
	}
	if s.prefetch[k] != nil {
		s.pmu.Unlock()
		return
	}
	pf := &prefetched{done: make(chan struct{}), now: make(chan struct{}), start: next.Start}
	s.prefetch[k] = pf
	s.pmu.Unlock()
	wait := max(0, next.Start.Sub(now)-prefetchLead)
	go func() {
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-pf.now:
			t.Stop()
		}
		pf.st, pf.err = s.resolveExtra(ctx, next)
		close(pf.done)
		if pf.err != nil {
			s.pmu.Lock()
			owned := s.prefetch[k] == pf // else Program took it and handles the failure
			if owned {
				delete(s.prefetch, k)
			}
			s.pmu.Unlock()
			if !owned {
				return
			}
			log.Printf("channels: channel %d: the title outside the library (entry %d) would not play: %v", ch, next.Seq, pf.err)
			if err := s.extraFailed(ctx, next); err != nil {
				log.Printf("channels: channel %d: %v", ch, err)
			}
		}
	}()
}

// describeExtras adds the Info of titles outside the library (by their
// channel_extras ids) to out, under their item ids (-id).
func (s *Service) describeExtras(ctx context.Context, ids []int64, out map[int64]Info) error {
	for part := range chunks(ids, 500) {
		in, args := inArgs(part)
		rows, err := s.Store.DB.QueryContext(ctx, `SELECT id, kind, title, year, overview, rating, genres_json, poster, backdrop, season, episode
		  FROM channel_extras WHERE id IN `+in, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var genres string
			x := Info{External: true}
			if err := rows.Scan(&id, &x.Kind, &x.Title, &x.Year, &x.Overview, &x.Rating, &genres, &x.Poster, &x.Backdrop, &x.Season, &x.Number); err != nil {
				rows.Close()
				return err
			}
			_ = json.Unmarshal([]byte(genres), &x.Genres)
			x.ItemID = -id
			out[x.ItemID] = x
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
