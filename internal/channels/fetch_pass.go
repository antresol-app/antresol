package channels

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// The fetch pass of channels with fetching (fetch.go): every fetchEvery,
// for each such channel, its rows are made from its titles, what arrived
// is taken into the schedule, late programmes give their slots away, the
// schedule is extended, the programmes due are asked for; then the addon
// is told what is wanted, and what aired is cleaned up.

const (
	// fetchEvery: how often the pass runs (well below fetchLateMargin +
	// fetchOrganizeTime, so a late fetch is seen in time).
	fetchEvery = 2 * time.Minute
	// fetchFailedFor: a programme the addon could not get is not asked
	// for again before this.
	fetchFailedFor = 6 * time.Hour
	// fetchExpandEvery: a channel's titles are named again (new episodes)
	fetchExpandEvery = 24 * time.Hour
	// fetchBootstrap: a channel with nothing to air asks for at most this
	// many of its next programmes at once.
	fetchBootstrap = 2
	// fetchRequestSlack: a programme is asked for this long before its
	// lead time runs out (two passes and some).
	fetchRequestSlack = 2*fetchEvery + 5*time.Minute
	// fetchTalkTimeout bounds one call of the addon.
	fetchTalkTimeout = 20 * time.Second
)

// SetFetch sets what fetches programmes (nil: nothing; programmes not on
// disk are never scheduled then), what names a channel's titles' programmes
// (nil: titles are not expanded) and what removes a library item that aired
// (nil: nothing is removed).
func (s *Service) SetFetch(f Fetcher, cat FetchCatalog, remove func(ctx context.Context, itemID int64) error) {
	s.Fetcher, s.FetchCatalog, s.FetchRemove = f, cat, remove
}

// FetchReady: this server fetches programmes for channels now.
func (s *Service) FetchReady(ctx context.Context) bool {
	return s != nil && s.Fetcher != nil && s.Fetcher.Ready(ctx)
}

// FetchKick asks for a fetch pass soon (a channel's fetching was edited).
func (s *Service) FetchKick() {
	if s == nil {
		return
	}
	s.init()
	select {
	case s.fetchKick <- struct{}{}:
	default:
	}
}

func (s *Service) fetchLoop(ctx context.Context) {
	t := time.NewTicker(fetchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.fetchKick:
		}
		if err := s.FetchPass(ctx); err != nil && ctx.Err() == nil {
			log.Printf("channels: fetch: %v", err)
		}
	}
}

// FetchPass runs the pass once.
func (s *Service) FetchPass(ctx context.Context) error {
	all, err := s.Store.All(ctx)
	if err != nil {
		return err
	}
	live := map[int64]Channel{}
	for _, c := range all {
		if c.Enabled && !c.External() && FetchOn(c.Rules) {
			live[c.ID] = c
		}
	}
	var errs []error
	if err := s.leaveOrder(ctx, live); err != nil {
		errs = append(errs, err)
	}
	for _, c := range all {
		if _, ok := live[c.ID]; !ok {
			continue
		}
		if err := s.fetchChannel(ctx, c); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			errs = append(errs, fmt.Errorf("channel %d: %w", c.ID, err))
		}
	}
	if err := s.fetchTalk(ctx); err != nil && !errors.Is(err, errNoFetch) {
		errs = append(errs, fmt.Errorf("addon: %w", err))
	}
	byID := map[int64]Channel{}
	for _, c := range all {
		byID[c.ID] = c
	}
	if err := s.fetchCleanup(ctx, byID, live); err != nil {
		errs = append(errs, fmt.Errorf("clean-up: %w", err))
	}
	return errors.Join(errs...)
}

// leaveOrder takes the rows of channels that no longer fetch (disabled,
// deleted, rules changed) out of any order; what they asked for and still
// fetch is dropped, what is not theirs is forgotten. Fetched programmes
// stay until the clean-up.
func (s *Service) leaveOrder(ctx context.Context, live map[int64]Channel) error {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT DISTINCT channel_id FROM channel_fetch WHERE pos >= 0`)
	if err != nil {
		return err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if _, ok := live[id]; !ok {
			gone = append(gone, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range gone {
		if _, err := s.Store.DB.ExecContext(ctx, `UPDATE channel_fetch SET pos=-1 WHERE channel_id=?`, id); err != nil {
			return err
		}
		// a channel that stopped fetching (switched off, its rules
		// changed) does not air what its old schedule had ahead: that is
		// not an airing of what was fetched for it
		s.build.Lock()
		err := s.Store.CutAfterCurrent(ctx, id)
		s.build.Unlock()
		if err != nil {
			return err
		}
	}
	return s.forgetLeft(ctx)
}

// forgetLeft drops the rows out of their channel's order that hold nothing
// on disk for it.
func (s *Service) forgetLeft(ctx context.Context) error {
	left, err := fetchRows(ctx, s.Store.DB, `pos < 0 AND state <> ?`, fetchReady)
	if err != nil || len(left) == 0 {
		return err
	}
	var drops []string
	for _, r := range left {
		if r.State == fetchRequested {
			drops = append(drops, r.key())
		}
	}
	if _, err := s.Store.DB.ExecContext(ctx, `DELETE FROM channel_fetch WHERE pos < 0 AND state <> ?`, fetchReady); err != nil {
		return err
	}
	return s.addDrops(ctx, drops)
}

// addDrops queues keys for the addon to drop, unless another row still
// wants them.
func (s *Service) addDrops(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	wanted := map[string]bool{}
	rows, err := fetchRows(ctx, s.Store.DB, `state IN ('requested','ready')`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		wanted[r.key()] = true
	}
	old := s.drops(ctx)
	for _, k := range keys {
		if !wanted[k] && !slices.Contains(old, k) {
			old = append(old, k)
		}
	}
	b, _ := json.Marshal(old)
	return s.Store.setSetting(ctx, settingFetchDrops, string(b))
}

func (s *Service) drops(ctx context.Context) []string {
	var out []string
	if v, ok := s.Store.setting(ctx, s.Store.DB, settingFetchDrops); ok {
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

func (s *Service) fetchChannel(ctx context.Context, c Channel) error {
	if err := s.expand(ctx, c); err != nil {
		if ctx.Err() != nil {
			return err
		}
		log.Printf("channels: channel %d: naming its titles' programmes: %v", c.ID, err)
	}
	s.build.Lock()
	err := s.resolve(ctx, c)
	if err == nil {
		err = s.cutLate(ctx, c)
	}
	s.build.Unlock()
	if err != nil {
		return err
	}
	if err := s.BuildChannel(ctx, c); err != nil {
		return err
	}
	s.build.Lock()
	defer s.build.Unlock()
	if err := s.requestDue(ctx, c); err != nil {
		return err
	}
	return s.bootstrap(ctx, c)
}

func titlesHash(ts []FetchTitle) string {
	b, _ := json.Marshal(ts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// expand makes c's rows from its titles when they changed, and once a day
// (new episodes): every programme in order; a row whose programme is no
// longer there leaves the order.
func (s *Service) expand(ctx context.Context, c Channel) error {
	if s.FetchCatalog == nil {
		return nil
	}
	now := s.Store.now()
	want := titlesHash(c.Rules.Fetch.Titles)
	var stored string
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT fetch_hash FROM channels WHERE id=?`, c.ID).Scan(&stored); err != nil {
		return err
	}
	h, at, _ := cutHash(stored)
	if h == want && now.Sub(fromMs(at)) < fetchExpandEvery {
		return nil
	}
	var progs []FetchProgramme
	var tmdbs []int
	for _, t := range c.Rules.Fetch.Titles {
		ps, err := s.FetchCatalog.Programmes(ctx, t)
		if err != nil {
			return fmt.Errorf("%s %d: %w", t.Kind, t.TMDBID, err)
		}
		for range ps {
			tmdbs = append(tmdbs, t.TMDBID)
		}
		progs = append(progs, ps...)
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE channel_fetch SET pos=-1 WHERE channel_id=?`, c.ID); err != nil {
		return err
	}
	for i, p := range progs {
		kind := library.KindMovie
		if p.Kind == library.KindEpisode {
			kind = library.KindEpisode
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_fetch (channel_id, pos, kind, tmdb_id, season, episode, title, episode_title, year, overview, rating, poster, runtime_ms)
		  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		  ON CONFLICT (channel_id, kind, tmdb_id, season, episode) DO UPDATE SET pos=excluded.pos, title=excluded.title, episode_title=excluded.episode_title,
		    year=excluded.year, overview=excluded.overview, rating=excluded.rating, poster=excluded.poster, runtime_ms=excluded.runtime_ms`,
			c.ID, i, kind, tmdbs[i], p.Season, p.Episode, p.Title, p.EpisodeTitle, p.Year, p.Overview, p.Rating, p.Poster, p.RuntimeMs); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channels SET fetch_hash=? WHERE id=?`, want+"@"+strconv.FormatInt(ms(now), 10), c.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if h != want && h != "" {
		// the schedule ahead was made of the old titles
		if err := s.Store.CutAfterCurrent(ctx, c.ID); err != nil {
			return err
		}
	}
	return s.forgetLeft(ctx)
}

func cutHash(v string) (string, int64, bool) {
	for i := len(v) - 1; i >= 0; i-- {
		if v[i] == '@' {
			at, err := strconv.ParseInt(v[i+1:], 10, 64)
			return v[:i], at, err == nil
		}
	}
	return "", 0, false
}

// resolve brings c's rows in step with the library: what was asked for
// and arrived is the channel's (ready), what is on disk otherwise is the
// library's, what went from disk is wanted again.
func (s *Service) resolve(ctx context.Context, c Channel) error {
	rows, err := fetchRows(ctx, s.Store.DB, `channel_id=?`, c.ID)
	if err != nil {
		return err
	}
	disk, err := onDisk(ctx, s.Store.DB, rows)
	if err != nil {
		return err
	}
	now := ms(s.Store.now())
	for _, r := range rows {
		p, ok := disk[r.key()]
		on := ok && len(p.Parts) > 0
		switch {
		case on && r.State == fetchRequested && !r.Existing:
			files, _ := json.Marshal(p.Files)
			size := r.Size
			var got int64
			if err := s.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(sum(size),0) FROM files WHERE item_id=? AND missing_since IS NULL`, p.ItemID).Scan(&got); err == nil && got > 0 {
				size = got
			}
			_, err = s.Store.DB.ExecContext(ctx, `UPDATE channel_fetch SET state=?, item_id=?, files_json=?, size_bytes=?, done_bytes=?, ready_ms=?, error='' WHERE id=?`,
				fetchReady, p.ItemID, string(files), size, size, now, r.ID)
		case on && (r.State == fetchWanted || r.State == fetchRemoved || r.State == fetchFailed || r.State == fetchRequested):
			_, err = s.Store.DB.ExecContext(ctx, `UPDATE channel_fetch SET state=?, item_id=?, error='' WHERE id=?`, fetchLibrary, p.ItemID, r.ID)
		case on && r.ItemID != p.ItemID:
			_, err = s.Store.DB.ExecContext(ctx, `UPDATE channel_fetch SET item_id=? WHERE id=?`, p.ItemID, r.ID)
		case !ok && (r.State == fetchReady || r.State == fetchLibrary || r.State == fetchKept):
			// gone from disk (someone deleted it): wanted again
			_, err = s.Store.DB.ExecContext(ctx, `UPDATE channel_fetch SET state=?, item_id=0, files_json='[]', existing=0, size_bytes=0, done_bytes=0, aired_ms=0 WHERE id=?`,
				fetchWanted, r.ID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// placeholder is a schedule row of a programme not on disk.
type placeholder struct {
	seq, start, fetch int64
}

func (s *Service) placeholders(ctx context.Context, ch int64) ([]placeholder, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT seq, start_ms, fetch_id FROM channel_playout WHERE channel_id=? AND fetch_id IS NOT NULL AND end_ms > ? ORDER BY seq`,
		ch, ms(s.Store.now()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []placeholder
	for rows.Next() {
		var p placeholder
		if err := rows.Scan(&p.seq, &p.start, &p.fetch); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) rowsByID(ctx context.Context, ch int64) (map[int64]fetchRow, error) {
	rows, err := fetchRows(ctx, s.Store.DB, `channel_id=?`, ch)
	if err != nil {
		return nil, err
	}
	out := map[int64]fetchRow{}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// cutLate drops c's schedule from the first programme not on disk that
// arrived (it is scheduled again with its file), that will not be on disk
// in time, or that may not be fetched any more; the build after fills the
// slots with what is available. What is on air and before it stays.
func (s *Service) cutLate(ctx context.Context, c Channel) error {
	ph, err := s.placeholders(ctx, c.ID)
	if err != nil || len(ph) == 0 {
		return err
	}
	rows, err := s.rowsByID(ctx, c.ID)
	if err != nil {
		return err
	}
	plan, err := newFetchPlan(ctx, s.Store, c)
	if err != nil {
		return err
	}
	now := s.Store.now()
	for _, p := range ph {
		r, ok := rows[p.fetch]
		deadline := p.start - fetchLateMargin.Milliseconds()
		late := !ok || r.Pos < 0
		if !late {
			switch r.State {
			case fetchRequested:
				at, known := plan.eta[r.ID]
				late = !known || at > deadline
			case fetchWanted, fetchRemoved:
				late = ms(now.Add(plan.lead(r.bytes()))) > deadline
			case fetchFailed:
				late = r.FailedUntilMs > ms(now) || ms(now.Add(plan.lead(r.bytes()))) > deadline
			default: // on disk now: scheduled again with its file
				late = true
			}
		}
		if late {
			if r.State == fetchRequested {
				log.Printf("channels: channel %d: «%s» will not be on disk in time for %s; the slot goes to another programme", c.ID, r.Title, fromMs(p.start).Format(time.RFC3339))
			}
			_, err := s.Store.DB.ExecContext(ctx, `DELETE FROM channel_playout WHERE channel_id=? AND seq >= ?`, c.ID, p.seq)
			return err
		}
	}
	return nil
}

// requestDue asks for the programmes c's schedule has ahead once their
// lead time is reached (within the next pass).
func (s *Service) requestDue(ctx context.Context, c Channel) error {
	ph, err := s.placeholders(ctx, c.ID)
	if err != nil || len(ph) == 0 {
		return err
	}
	rows, err := s.rowsByID(ctx, c.ID)
	if err != nil {
		return err
	}
	plan, err := newFetchPlan(ctx, s.Store, c)
	if err != nil {
		return err
	}
	now := s.Store.now()
	asked := map[int64]bool{}
	for _, p := range ph {
		r, ok := rows[p.fetch]
		if !ok || asked[r.ID] || r.State == fetchRequested || (r.State == fetchFailed && r.FailedUntilMs > ms(now)) {
			continue
		}
		est := r.bytes()
		// asked for while a couple of passes are left before it would be
		// late (cutLate)
		if p.start-fetchLateMargin.Milliseconds()-plan.lead(est).Milliseconds() > ms(now.Add(fetchRequestSlack)) {
			continue
		}
		plan.queue += est
		asked[r.ID] = true
		if err := s.markRequested(ctx, r.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) markRequested(ctx context.Context, id int64) error {
	_, err := s.Store.DB.ExecContext(ctx, `UPDATE channel_fetch SET state=?, requested_ms=?, existing=0, error='', size_bytes=0, done_bytes=0 WHERE id=?`,
		fetchRequested, ms(s.Store.now()), id)
	return err
}

// bootstrap: a channel whose schedule does not reach the horizon (its
// next programmes are not on disk and cannot be in time for any slot)
// asks for its next programmes in order at once, a few at a time and
// within the budgets, so it gets something to air.
func (s *Service) bootstrap(ctx context.Context, c Channel) error {
	now := s.Store.now()
	var end sql.NullInt64
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT max(end_ms) FROM channel_playout WHERE channel_id=?`, c.ID).Scan(&end); err != nil {
		return err
	}
	if end.Valid && end.Int64 >= ms(now.Add(s.Builder.horizon()/2)) {
		return nil
	}
	rows, err := fetchRows(ctx, s.Store.DB, `channel_id=? AND pos >= 0 ORDER BY pos`, c.ID)
	if err != nil || len(rows) == 0 {
		return err
	}
	busy := 0
	for _, r := range rows {
		if r.State == fetchRequested {
			busy++
		}
	}
	if busy >= fetchBootstrap {
		return nil
	}
	// from the programme after the last one scheduled, round the order
	from := 0
	var last sql.NullInt64
	var lastFetch sql.NullInt64
	_ = s.Store.DB.QueryRowContext(ctx, `SELECT item_id, fetch_id FROM channel_playout WHERE channel_id=? ORDER BY seq DESC LIMIT 1`, c.ID).Scan(&last, &lastFetch)
	for i, r := range rows {
		if (lastFetch.Valid && r.ID == lastFetch.Int64) || (last.Valid && r.ItemID != 0 && r.ItemID == last.Int64) {
			from = i + 1
		}
	}
	use, err := fetchUsageRows(ctx, s.Store.DB, now)
	if err != nil {
		return err
	}
	by, all := sumUsage(use)
	chanUsed, budget, allBudget := by[c.ID], c.Rules.Fetch.budget(), int64(s.Store.FetchGlobalGB(ctx)*(1<<30))
	for k := range len(rows) {
		if busy >= fetchBootstrap {
			break
		}
		r := rows[(from+k)%len(rows)]
		if !(r.State == fetchWanted || r.State == fetchRemoved || (r.State == fetchFailed && r.FailedUntilMs <= ms(now))) {
			continue
		}
		est := r.bytes()
		if chanUsed+est > budget || all+est > allBudget {
			break // in order: the next one waits for room
		}
		chanUsed, all = chanUsed+est, all+est
		busy++
		if err := s.markRequested(ctx, r.ID); err != nil {
			return err
		}
	}
	return nil
}

// fetchTalk tells the addon every programme asked for (it fetches what it
// does not yet) and the keys to drop, and takes its answers: sizes and
// progress, failures, the speed.
func (s *Service) fetchTalk(ctx context.Context) error {
	if s.Fetcher == nil || !s.Fetcher.Ready(ctx) {
		return errNoFetch
	}
	rows, err := fetchRows(ctx, s.Store.DB, `state=? ORDER BY requested_ms, id`, fetchRequested)
	if err != nil {
		return err
	}
	var want []FetchWant
	seen := map[string]bool{}
	for _, r := range rows {
		k := r.key()
		if seen[k] {
			continue
		}
		seen[k] = true
		w := FetchWant{Key: k, Kind: r.Kind, TMDBID: r.TMDBID, Season: r.Season, Episode: r.Episode, Title: r.Title, Year: r.Year}
		want = append(want, w)
	}
	drops := s.drops(ctx)
	// the ready ones too: the addon says whether it keeps them
	ready, err := fetchRows(ctx, s.Store.DB, `state=?`, fetchReady)
	if err != nil {
		return err
	}
	for _, r := range ready {
		if k := r.key(); !seen[k] {
			seen[k] = true
			want = append(want, FetchWant{Key: k, Kind: r.Kind, TMDBID: r.TMDBID, Season: r.Season, Episode: r.Episode, Title: r.Title, Year: r.Year})
		}
	}
	if len(want) == 0 && len(drops) == 0 {
		return nil
	}
	tctx, cancel := context.WithTimeout(ctx, fetchTalkTimeout)
	states, err := s.Fetcher.Fetch(tctx, want, drops)
	cancel()
	if err != nil {
		return err
	}
	if len(drops) > 0 {
		if _, err := s.Store.DB.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, settingFetchDrops); err != nil {
			return err
		}
	}
	now := s.Store.now()
	var rate int64
	keep := map[string]bool{}
	for _, st := range states {
		keep[st.Key] = st.Keep
		if st.State == FetchDownloading {
			rate += max(st.Rate, 0)
		}
		var err error
		switch {
		case st.State == FetchFailed:
			msg := st.Error
			if len([]rune(msg)) > 300 {
				msg = string([]rune(msg)[:300])
			}
			_, err = s.Store.DB.ExecContext(ctx, `UPDATE channel_fetch SET state=?, failed_until_ms=?, error=? WHERE state=? AND kind=? AND tmdb_id=? AND season=? AND episode=?`,
				append([]any{fetchFailed, ms(now.Add(fetchFailedFor)), msg, fetchRequested}, keyArgs(st.Key)...)...)
		default:
			_, err = s.Store.DB.ExecContext(ctx, `UPDATE channel_fetch SET size_bytes=?, done_bytes=?, existing=existing OR ? WHERE state=? AND kind=? AND tmdb_id=? AND season=? AND episode=?`,
				append([]any{max(st.Size, 0), max(st.Done, 0), st.Existing, fetchRequested}, keyArgs(st.Key)...)...)
		}
		if err != nil {
			return err
		}
	}
	s.fmu.Lock()
	s.fetchKeep = keep
	s.fmu.Unlock()
	return s.Store.sawFetchSpeed(ctx, float64(rate))
}

// keyArgs are a fetchKey's kind, tmdb id, season and episode.
func keyArgs(k string) []any {
	parts := strings.SplitN(k, ":", 4)
	for len(parts) < 4 {
		parts = append(parts, "0")
	}
	t, _ := strconv.Atoi(parts[1])
	se, _ := strconv.Atoi(parts[2])
	ep, _ := strconv.Atoi(parts[3])
	return []any{parts[0], t, se, ep}
}

// fetchCleanup removes what was fetched for channels once it aired and
// its grace period is over — unless a schedule still has it ahead, someone
// in the household keeps it (kept: progress, a watchlist, a star, a mark,
// the addon), or the library has more of it than what arrived for the
// channel. What the library had before is never in these rows (library).
func (s *Service) fetchCleanup(ctx context.Context, all, live map[int64]Channel) error {
	rows, err := fetchRows(ctx, s.Store.DB, `state=? ORDER BY item_id, id`, fetchReady)
	if err != nil || len(rows) == 0 {
		return err
	}
	now := s.Store.now()
	byItem := map[int64][]fetchRow{}
	var items []int64
	for _, r := range rows {
		var aired sql.NullInt64
		if err := s.Store.DB.QueryRowContext(ctx, `SELECT max(end_ms) FROM channel_playout WHERE channel_id=? AND item_id=? AND end_ms <= ?`,
			r.Channel, r.ItemID, ms(now)).Scan(&aired); err != nil {
			return err
		}
		if aired.Valid && aired.Int64 > r.AiredMs {
			r.AiredMs = aired.Int64
			if _, err := s.Store.DB.ExecContext(ctx, `UPDATE channel_fetch SET aired_ms=? WHERE id=?`, r.AiredMs, r.ID); err != nil {
				return err
			}
		}
		if byItem[r.ItemID] == nil {
			items = append(items, r.ItemID)
		}
		byItem[r.ItemID] = append(byItem[r.ItemID], r)
	}
	s.fmu.Lock()
	addonKeeps := s.fetchKeep
	s.fmu.Unlock()
	var drops []string
	for _, item := range items {
		group := byItem[item]
		due := true
		for _, r := range group {
			_, on := live[r.Channel]
			// the channel's grace period, while it is there (disabled too)
			grace := all[r.Channel].Rules.Fetch.grace()
			var from int64
			switch {
			case r.AiredMs > 0:
				from = r.AiredMs
			case !on || r.Pos < 0: // it left the channel before it aired
				from = r.ReadyMs
			default:
				due = false // not aired yet
			}
			if from == 0 || ms(now) < from+grace.Milliseconds() {
				due = false
			}
		}
		if !due {
			continue
		}
		var ahead int
		if err := s.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM channel_playout p JOIN channels c ON c.id=p.channel_id AND c.enabled=1 WHERE p.item_id=? AND p.end_ms > ?`, item, ms(now)).Scan(&ahead); err != nil {
			return err
		}
		if ahead > 0 {
			continue
		}
		why, err := s.keptBy(ctx, group[0])
		if err != nil {
			return err
		}
		if why == "" && addonKeeps[group[0].key()] {
			why = "the addon"
		}
		files, err := s.itemFiles(ctx, item)
		if err != nil {
			return err
		}
		if why == "" {
			for _, f := range files {
				if !slices.Contains(group[0].Files, f) {
					why = "files not fetched for the channel"
				}
			}
		}
		state := fetchRemoved
		switch {
		case why != "":
			state = fetchKept
			log.Printf("channels: «%s» was fetched for a channel and aired; kept (%s)", group[0].Title, why)
		case len(files) == 0: // gone already
		case s.FetchRemove == nil:
			continue
		default:
			if err := s.FetchRemove(ctx, item); err != nil {
				log.Printf("channels: removing «%s» after it aired: %v", group[0].Title, err)
				continue
			}
			log.Printf("channels: «%s» aired; removed", group[0].Title)
		}
		for _, r := range group {
			if state == fetchRemoved {
				_, err = s.Store.DB.ExecContext(ctx, `UPDATE channel_fetch SET state=?, item_id=0, files_json='[]', size_bytes=0, done_bytes=0 WHERE id=?`, state, r.ID)
				drops = append(drops, r.key())
			} else {
				_, err = s.Store.DB.ExecContext(ctx, `UPDATE channel_fetch SET state=? WHERE id=?`, state, r.ID)
			}
			if err != nil {
				return err
			}
		}
	}
	if err := s.addDrops(ctx, drops); err != nil {
		return err
	}
	return s.forgetLeft(ctx)
}

func (s *Service) itemFiles(ctx context.Context, item int64) ([]int64, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id FROM files WHERE item_id=? AND missing_since IS NULL`, item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// keptBy says why the household keeps r's item ("" when nobody does):
// someone's progress on it, it or its title on a watchlist or starred, or
// its title marked (planned, watching, on hold, completed).
func (s *Service) keptBy(ctx context.Context, r fetchRow) (string, error) {
	var title int64
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(sr.id, i.id) FROM items i
	  LEFT JOIN items se ON se.id=i.parent_id AND i.kind='episode' LEFT JOIN items sr ON sr.id=se.parent_id WHERE i.id=?`, r.ItemID).Scan(&title); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	kind := library.KindMovie
	if r.Kind == library.KindEpisode {
		kind = library.KindSeries
	}
	checks := []struct {
		why, q string
		args   []any
	}{
		{"progress", `SELECT count(*) FROM progress WHERE item_id=? AND (position_ms > 0 OR finished=1)`, []any{r.ItemID}},
		{"watchlist", `SELECT count(*) FROM watchlist WHERE item_id IN (?,?)`, []any{r.ItemID, title}},
		{"star", `SELECT count(*) FROM stars WHERE item_id IN (?,?)`, []any{r.ItemID, title}},
		{"mark", `SELECT count(*) FROM title_marks m JOIN tracked_titles t ON t.id=m.title_id WHERE t.kind=? AND t.tmdb_id=? AND m.state <> 'dropped'`, []any{kind, r.TMDBID}},
	}
	for _, c := range checks {
		var n int
		if err := s.Store.DB.QueryRowContext(ctx, c.q, c.args...).Scan(&n); err != nil {
			return "", err
		}
		if n > 0 {
			return c.why, nil
		}
	}
	return "", nil
}

// fetchCutAt drops channel ch's schedule from entry seq on (a programme
// not on disk at its slot) and builds it again.
func (s *Service) fetchCutAt(ctx context.Context, ch, seq int64) error {
	s.build.Lock()
	_, err := s.Store.DB.ExecContext(ctx, `DELETE FROM channel_playout WHERE channel_id=? AND seq >= ?`, ch, seq)
	s.build.Unlock()
	if err != nil {
		return err
	}
	return s.rebuild(ctx, ch)
}

// describeFetch is the Info of programmes not on disk (fetchItem ids).
func (s *Service) describeFetch(ctx context.Context, ids []int64, out map[int64]Info) error {
	for part := range chunks(ids, 500) {
		in, args := inArgs(part)
		rows, err := fetchRows(ctx, s.Store.DB, `id IN `+in, args...)
		if err != nil {
			return err
		}
		for _, r := range rows {
			x := Info{ItemID: fetchItem(r.ID), Kind: r.Kind, Title: r.Title, Episode: r.EpTitle, Season: r.Season, Number: r.Episode,
				Overview: r.Overview, Year: r.Year, Rating: r.Rating, External: true, Poster: r.Poster, ExtraKey: "fetch:" + r.key()}
			out[x.ItemID] = x
		}
	}
	return nil
}
