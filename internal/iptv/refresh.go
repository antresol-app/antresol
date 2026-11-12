package iptv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/channels"
)

// Refreshing: a source's playlist and its guides are fetched, parsed and
// checked before anything is stored (the guard), its entries become
// logical channel rows (channels.kind='external') numbered from
// FirstNumber, and the guides keep the programmes of matched channels in
// the window [now − EPGBefore, now + EPGAfter).
const (
	// FirstNumber: external channels take the free numbers from here up.
	FirstNumber = 100
	// MaxDropShare: a refresh losing more than this share of the present
	// channels waits for the admin's confirmation (ConfirmDrop).
	MaxDropShare = 0.30
	EPGBefore    = 24 * time.Hour
	EPGAfter     = 7 * 24 * time.Hour
	// OpenAge: «при открытии» refreshes what is older than this.
	OpenAge = time.Hour
	// RetryAfter: a failed scheduled refresh is tried again after this.
	RetryAfter = 30 * time.Minute
	// Tick: how often Run looks for what is due.
	Tick = 10 * time.Minute
)

// The reasons a refresh failed (Source.LastErr, EPGURL.LastErr): i18n keys,
// or a Go error's own text (which never carries credentials: the
// fetcher shows URLs only through StripCreds).
const (
	ErrTextNotM3U     = "ответ не похож на плейлист M3U"
	ErrTextEmpty      = "плейлист пуст"
	ErrTextTooMany    = "в плейлисте слишком много каналов"
	ErrTextTooLarge   = "файл слишком большой"
	ErrTextTruncated  = "файл оборвался на середине"
	ErrTextTimeout    = "сервер не ответил вовремя"
	ErrTextNotXMLTV   = "ответ не похож на телепрограмму XMLTV"
	ErrTextXtreamOff  = "Xtream выключен в настройках ТВ"
	ErrTextNoURL      = "у источника нет адреса"
	ErrTextBadSecret  = "не удалось расшифровать адрес источника"
	ErrTextUnknownSrc = "плейлист не найден"
)

// ErrXtreamOff: an Xtream source while Xtream is off in the TV settings.
var ErrXtreamOff = errors.New("iptv: xtream is off")

// errNoURL: a source or guide without an address.
var errNoURL = errors.New("iptv: no url")

// ErrText is what LastErr keeps of err.
func ErrText(err error) string {
	var h ErrHTTP
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotM3U):
		return ErrTextNotM3U
	case errors.Is(err, ErrEmpty):
		return ErrTextEmpty
	case errors.Is(err, ErrTooManyEntries):
		return ErrTextTooMany
	case errors.Is(err, ErrTooLarge):
		return ErrTextTooLarge
	case errors.Is(err, ErrNotXMLTV):
		return ErrTextNotXMLTV
	case errors.Is(err, ErrXtreamOff):
		return ErrTextXtreamOff
	case errors.Is(err, errNoURL):
		return ErrTextNoURL
	case errors.Is(err, ErrSealed):
		return ErrTextBadSecret
	case errors.Is(err, io.ErrUnexpectedEOF):
		return ErrTextTruncated
	case errors.Is(err, context.DeadlineExceeded):
		return ErrTextTimeout
	case errors.As(err, &h):
		return "HTTP " + strconv.Itoa(h.Code)
	}
	return err.Error()
}

// Service refreshes sources and guides and assembles what viewers see
// (assemble.go).
type Service struct {
	Store *Store
	// Channels reads the logical channel rows (nil: on Store.DB).
	Channels *channels.Store
	Fetch    *Fetcher
	Seal     *Sealer
	Now      func() time.Time
	// ScreenLimited: the profile has screen-time limits (it then sees no
	// external channels); nil: none has.
	ScreenLimited func(ctx context.Context, userID int64) bool

	srcMu sync.Mutex // one playlist refresh at a time
	epgMu sync.Mutex // one guide refresh at a time

	omu     sync.Mutex
	opening bool
	base    context.Context // Run's context: what OnOpen runs in
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) chans() *channels.Store {
	if s.Channels != nil {
		return s.Channels
	}
	return &channels.Store{DB: s.Store.DB, Now: s.Now}
}

// Result is what a playlist refresh found.
type Result struct {
	// Total: the playlist's entries; New: entries not seen before; Lost:
	// present entries the playlist no longer has.
	Total, New, Lost int
	// Shown, Hidden: the source's present entries by the decision in effect.
	Shown, Hidden int
	// PendingDrop: nothing was stored, Lost waits for ConfirmDrop.
	PendingDrop int
}

// PlaylistURL is the source's playlist address (opened, with credentials).
func (s *Service) PlaylistURL(ctx context.Context, src Source) (string, error) {
	if src.Kind == KindXtream {
		if !s.Store.XtreamEnabled(ctx) {
			return "", ErrXtreamOff
		}
		pass, err := s.Seal.Open(src.XtreamPassEnc)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(src.XtreamServer) == "" {
			return "", errNoURL
		}
		m3u, _ := XtreamURLs(src.XtreamServer, src.XtreamUser, pass)
		return m3u, nil
	}
	u, err := s.Seal.Open(src.URLEnc)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(u) == "" {
		return "", errNoURL
	}
	return u, nil
}

// RefreshSource fetches a source's playlist and, when it passes the guard,
// stores it: its entries, their decisions and their channel rows. An
// empty answer, HTML, a cut file or a parse error leave the stored
// playlist as it was (LastErr says why); losing more than MaxDropShare of
// the present entries stores nothing and sets PendingDrop, unless force.
func (s *Service) RefreshSource(ctx context.Context, id int64, force bool) (Result, error) {
	s.srcMu.Lock()
	defer s.srcMu.Unlock()
	src, err := s.Store.Source(ctx, id)
	if err != nil {
		return Result{}, err
	}
	now := s.now()
	src.LastTry = now
	fail := func(err error) (Result, error) {
		src.LastErr = ErrText(err)
		if _, serr := s.Store.SaveSource(ctx, src); serr != nil {
			return Result{}, serr
		}
		return Result{}, err
	}
	raw, err := s.PlaylistURL(ctx, src)
	if err != nil {
		return fail(err)
	}
	fctx, cancel := context.WithTimeout(ctx, M3UTimeout)
	defer cancel()
	rc, err := s.Fetch.Open(fctx, raw, "")
	if err != nil {
		return fail(err)
	}
	pl, err := ParseM3U(rc, MaxEntries)
	rc.Close()
	if err != nil {
		return fail(err)
	}
	old, err := s.Store.Members(ctx, src.ID)
	if err != nil {
		return Result{}, err
	}
	ms, err := s.toMembers(pl.Entries)
	if err != nil {
		return fail(err)
	}
	res := Result{Total: len(ms)}
	known, present := map[string]bool{}, 0
	for _, m := range old {
		known[m.Ident] = true
		if m.Present {
			present++
		}
	}
	idents := map[string]bool{}
	for _, m := range ms {
		idents[m.Ident] = true
		if !known[m.Ident] {
			res.New++
		}
	}
	for _, m := range old {
		if m.Present && !idents[m.Ident] {
			res.Lost++
		}
	}
	if !force && present > 0 && float64(res.Lost) > MaxDropShare*float64(present) {
		src.PendingDrop, src.LastErr = res.Lost, ""
		res.PendingDrop = res.Lost
		if _, err := s.Store.SaveSource(ctx, src); err != nil {
			return Result{}, err
		}
		return res, nil
	}
	if src.DefaultShown == nil {
		// fixed at the first good refresh: a playlist growing past
		// BigPlaylist later does not hide what was shown
		v := len(ms) < BigPlaylist
		src.DefaultShown = &v
	}
	if err := s.Store.ReplaceMembers(ctx, src.ID, ms); err != nil {
		return Result{}, err
	}
	if err := s.autoEPG(ctx, src.ID, pl.EPGURLs); err != nil {
		return Result{}, err
	}
	src.LastOK, src.LastErr, src.PendingDrop = now, "", 0
	if _, err := s.Store.SaveSource(ctx, src); err != nil {
		return Result{}, err
	}
	if err := s.Sync(ctx); err != nil {
		return Result{}, err
	}
	cur, err := s.Store.Members(ctx, src.ID)
	if err != nil {
		return Result{}, err
	}
	for _, m := range cur {
		if !m.Present {
			continue
		}
		if m.Shown {
			res.Shown++
		} else {
			res.Hidden++
		}
	}
	return res, nil
}

// ConfirmDrop refreshes a source accepting what it loses.
func (s *Service) ConfirmDrop(ctx context.Context, id int64) (Result, error) {
	return s.RefreshSource(ctx, id, true)
}

// toMembers makes a playlist's entries members: their Ident (made unique
// within the playlist: a second entry with the same tvg-id is told apart
// by its URL, then by its place), the stream URL sealed.
func (s *Service) toMembers(es []Entry) ([]Member, error) {
	out := make([]Member, 0, len(es))
	seen := make(map[string]bool, len(es))
	for i, e := range es {
		id := Ident(e)
		if seen[id] {
			alt := id + "|" + Ident(Entry{URL: e.URL, Name: e.Name})
			if seen[alt] {
				alt = id + "|" + strconv.Itoa(i)
			}
			id = alt
		}
		seen[id] = true
		enc, err := s.Seal.Seal(e.URL)
		if err != nil {
			return nil, err
		}
		out = append(out, Member{Ident: id, TvgID: e.TvgID, TvgName: e.TvgName, Name: e.Name, Group: e.Group, Logo: e.Logo,
			URLEnc: enc, UserAgent: e.UserAgent, Referrer: e.Referrer, Position: i + 1})
	}
	return out, nil
}

// autoEPG replaces a source's guide URLs taken from its playlist's url-tvg
// (Auto); one that stays keeps its row, and so its programmes.
func (s *Service) autoEPG(ctx context.Context, sourceID int64, urls []string) error {
	have, err := s.Store.EPGURLs(ctx, sourceID)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, u := range urls {
		want[u] = true
	}
	kept := map[string]bool{}
	maxPos := 0
	for _, e := range have {
		maxPos = max(maxPos, e.Position)
		raw, err := s.Seal.Open(e.URLEnc)
		if err != nil {
			raw = ""
		}
		if !e.Auto {
			kept[raw] = true // an admin's own wins: never a second row
			continue
		}
		if want[raw] && !kept[raw] {
			kept[raw] = true
			continue
		}
		if err := s.Store.DeleteEPGURL(ctx, e.ID); err != nil {
			return err
		}
	}
	for _, u := range urls {
		if kept[u] {
			continue
		}
		kept[u] = true
		enc, err := s.Seal.Seal(u)
		if err != nil {
			return err
		}
		maxPos++
		if _, err := s.Store.SaveEPGURL(ctx, EPGURL{SourceID: sourceID, URLEnc: enc, Position: maxPos, RefreshHours: 24, Auto: true}); err != nil {
			return err
		}
	}
	return nil
}

// Sync makes the decision in effect of every present entry (the source's
// rules, then the admin's) and brings the logical channel rows in step:
// an entry without a channel joins the row of its DedupeKey (another
// source's same channel) or gets a new one, numbered from FirstNumber; a
// row shows when any present entry of it is shown, with the name and the
// category of its first shown entry (by source position); a row whose
// entries all went is disabled, keeping its number.
func (s *Service) Sync(ctx context.Context) error {
	srcs, err := s.Store.Sources(ctx)
	if err != nil {
		return err
	}
	ms, err := s.Store.AllMembers(ctx)
	if err != nil {
		return err
	}
	bySrc := map[int64]Source{}
	for _, src := range srcs {
		bySrc[src.ID] = src
	}
	total := map[int64]int{}
	for _, m := range ms {
		if m.Present {
			total[m.SourceID]++
		}
	}
	decided := map[int64]Decision{}
	changed := map[int64]Decision{}
	for _, m := range ms {
		src, ok := bySrc[m.SourceID]
		if !ok || !m.Present {
			continue
		}
		d := ApplyManual(Defaults(src, Entry{Name: m.Name, Group: m.Group, TvgID: m.TvgID, TvgName: m.TvgName}, total[m.SourceID]), m.Manual)
		decided[m.ID] = d
		if shown := d.Shown != nil && *d.Shown; shown != m.Shown || d.Why != m.Why {
			changed[m.ID] = d
		}
	}
	if len(changed) > 0 {
		if err := s.Store.SetDecided(ctx, changed); err != nil {
			return err
		}
	}
	return s.syncRows(ctx, bySrc, ms, decided)
}

type extRow struct {
	id, number            int64
	name, category, why   string
	key                   string
	enabled, numberManual bool
}

func (s *Service) syncRows(ctx context.Context, bySrc map[int64]Source, ms []Member, decided map[int64]Decision) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows := map[int64]*extRow{}
	byKey := map[string]int64{}
	used := map[int64]int64{} // number → channel
	q, err := tx.QueryContext(ctx, `SELECT id, number, name, category, why, COALESCE(auto_key,''), enabled, number_manual, kind FROM channels`)
	if err != nil {
		return err
	}
	for q.Next() {
		var r extRow
		var kind string
		if err := q.Scan(&r.id, &r.number, &r.name, &r.category, &r.why, &r.key, &r.enabled, &r.numberManual, &kind); err != nil {
			q.Close()
			return err
		}
		used[r.number] = r.id
		if r.key != "" {
			byKey[r.key] = r.id
		}
		if kind == channels.KindExternal {
			rows[r.id] = &r
		}
	}
	q.Close()
	if err := q.Err(); err != nil {
		return err
	}
	// the order entries rank in: source position, then playlist position
	order := append([]Member(nil), ms...)
	sort.SliceStable(order, func(i, j int) bool {
		a, b := bySrc[order[i].SourceID], bySrc[order[j].SourceID]
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return order[i].Position < order[j].Position
	})
	next := int64(FirstNumber)
	freeNumber := func() int64 {
		for used[next] != 0 {
			next++
		}
		return next
	}
	// a row's sources (an entry joins a row only once per source)
	rowSrc := map[int64]map[int64]bool{}
	for _, m := range order {
		if m.ChannelID != 0 && rows[m.ChannelID] != nil {
			if rowSrc[m.ChannelID] == nil {
				rowSrc[m.ChannelID] = map[int64]bool{}
			}
			rowSrc[m.ChannelID][m.SourceID] = true
		}
	}
	setChannel, err := tx.PrepareContext(ctx, `UPDATE iptv_members SET channel_id=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer setChannel.Close()
	for i := range order {
		m := &order[i]
		if !m.Present || (m.ChannelID != 0 && rows[m.ChannelID] != nil) {
			continue
		}
		d := decided[m.ID]
		var row int64
		for _, key := range []string{DedupeKey(m.Name), "ext=" + strings.ToLower(strings.TrimSpace(m.Name)), "ext#" + strconv.FormatInt(m.SourceID, 10) + ":" + m.Ident} {
			id, ok := byKey[key]
			if ok && rows[id] != nil && !rowSrc[id][m.SourceID] {
				row = id
				break
			}
			if !ok {
				n := freeNumber()
				res, err := tx.ExecContext(ctx, `INSERT INTO channels (number, name, category, rules_json, auto_key, enabled, kind, why, seed)
					VALUES (?,?,?,'{}',?,?,?,?,0)`, n, nameOr(d.Name, m.Name), d.Group, key, b2i(d.Shown != nil && *d.Shown), channels.KindExternal, d.Why)
				if err != nil {
					return fmt.Errorf("iptv: channel row %q: %w", m.Name, err)
				}
				row, _ = res.LastInsertId()
				used[n], byKey[key] = row, row
				rows[row] = &extRow{id: row, number: n, name: nameOr(d.Name, m.Name), category: d.Group, why: d.Why, key: key, enabled: d.Shown != nil && *d.Shown}
				break
			}
		}
		if row == 0 {
			continue // cannot happen: the last key is the entry's own
		}
		if rowSrc[row] == nil {
			rowSrc[row] = map[int64]bool{}
		}
		rowSrc[row][m.SourceID] = true
		m.ChannelID = row
		if _, err := setChannel.ExecContext(ctx, row, m.ID); err != nil {
			return err
		}
	}
	// each row from its entries
	byRow := map[int64][]Member{}
	for _, m := range order {
		if m.ChannelID != 0 && rows[m.ChannelID] != nil {
			byRow[m.ChannelID] = append(byRow[m.ChannelID], m)
		}
	}
	upd, err := tx.PrepareContext(ctx, `UPDATE channels SET number=?, name=?, category=?, enabled=?, number_manual=?, why=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer upd.Close()
	ids := make([]int64, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		r := rows[id]
		want := *r
		var first, shown *Member
		manualNumber := 0
		for i := range byRow[id] {
			m := &byRow[id][i]
			if !m.Present {
				continue
			}
			if first == nil {
				first = m
			}
			d := decided[m.ID]
			if shown == nil && d.Shown != nil && *d.Shown {
				shown = m
			}
			if manualNumber == 0 && m.Manual != nil && m.Manual.Number != 0 {
				manualNumber = m.Manual.Number
			}
		}
		lead := shown
		if lead == nil {
			lead = first
		}
		if lead == nil {
			want.enabled = false // every entry went: the row waits, keeping its number
		} else {
			d := decided[lead.ID]
			want.enabled = shown != nil
			want.name, want.category, want.why = nameOr(d.Name, lead.Name), d.Group, d.Why
		}
		if manualNumber != 0 {
			if n := int64(manualNumber); n != r.number && used[n] == 0 {
				delete(used, r.number)
				used[n] = id
				want.number = n
			}
			want.numberManual = want.number == int64(manualNumber)
		} else {
			want.numberManual = false
		}
		if want != *r {
			if _, err := upd.ExecContext(ctx, want.number, want.name, want.category, want.enabled, want.numberManual, want.why, id); err != nil {
				return fmt.Errorf("iptv: channel row %d: %w", id, err)
			}
			*r = want
		}
	}
	return tx.Commit()
}

func nameOr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}

// DeleteSource removes a source with its entries, guides and programmes,
// and the channel rows no other source feeds (their favourites go with
// them).
func (s *Service) DeleteSource(ctx context.Context, id int64) error {
	s.srcMu.Lock()
	defer s.srcMu.Unlock()
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE kind=? AND id IN (SELECT channel_id FROM iptv_members WHERE source_id=?)
		AND id NOT IN (SELECT channel_id FROM iptv_members WHERE source_id<>? AND channel_id IS NOT NULL)`, channels.KindExternal, id, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM iptv_sources WHERE id=?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.Sync(ctx)
}

// ---- guides ----

// RefreshEPG fetches one guide URL and, when it parses to the end,
// replaces its programmes in one transaction: those of the channels its
// source's entries match (tvg-id, then names; spec goal 3) and those bound
// to it by hand, within [now − EPGBefore, now + EPGAfter). A guide that
// fails to parse leaves the stored programmes as they were.
func (s *Service) RefreshEPG(ctx context.Context, epgURLID int64) error {
	s.epgMu.Lock()
	defer s.epgMu.Unlock()
	e, err := s.Store.EPGURL(ctx, epgURLID)
	if err != nil {
		return err
	}
	now := s.now()
	e.LastTry = now
	fail := func(err error) error {
		e.LastErr = ErrText(err)
		if _, serr := s.Store.SaveEPGURL(ctx, e); serr != nil {
			return serr
		}
		return err
	}
	raw, err := s.Seal.Open(e.URLEnc)
	if err != nil {
		return fail(err)
	}
	if strings.TrimSpace(raw) == "" {
		return fail(errNoURL)
	}
	all, err := s.Store.AllMembers(ctx)
	if err != nil {
		return err
	}
	prio, err := s.guidePriority(ctx)
	if err != nil {
		return err
	}
	var own []Member // the source's present entries an automatic binding is made for
	for _, m := range all {
		if m.SourceID == e.SourceID && m.Present && !m.EPGManual {
			own = append(own, m)
		}
	}
	fctx, cancel := context.WithTimeout(ctx, EPGTimeout)
	defer cancel()
	rc, err := s.Fetch.Open(fctx, raw, "")
	if err != nil {
		return fail(err)
	}
	var chans []XMLChannel
	var keepIDs map[string]bool
	var matched []MatchResult
	built := -1
	build := func() {
		if built == len(chans) {
			return
		}
		built = len(chans)
		matched = NewMatcher(chans).MatchAll(own)
		keepIDs = map[string]bool{}
		for i, r := range matched {
			if r.XMLID != "" && s.takes(own[i], e, prio) {
				keepIDs[r.XMLID] = true
			}
		}
		for _, m := range all {
			if m.EPGManual && m.EPGURLID == e.ID && m.EPGID != "" {
				keepIDs[m.EPGID] = true
			}
		}
	}
	var progs []XMLProgramme
	err = ParseXMLTV(rc, func(id string) bool { build(); return keepIDs[id] }, now.Add(-EPGBefore), now.Add(EPGAfter),
		func(c XMLChannel) { chans = append(chans, c) },
		func(p XMLProgramme) { progs = append(progs, p) })
	rc.Close()
	if err != nil {
		return fail(err)
	}
	build()
	fillStops(progs)
	if err := s.storeGuide(ctx, e, chans, progs, own, matched, prio); err != nil {
		return err
	}
	e.LastOK, e.LastErr = now, ""
	_, err = s.Store.SaveEPGURL(ctx, e)
	return err
}

// guidePriority ranks every guide URL: its source's position, then its own.
type guideRank struct {
	source int64
	rank   int
}

func (s *Service) guidePriority(ctx context.Context) (map[int64]guideRank, error) {
	srcs, err := s.Store.Sources(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]guideRank{}
	for _, src := range srcs {
		es, err := s.Store.EPGURLs(ctx, src.ID)
		if err != nil {
			return nil, err
		}
		for i, e := range es {
			out[e.ID] = guideRank{source: src.ID, rank: i}
		}
	}
	return out, nil
}

// takes: an automatic binding of m to guide e replaces m's current one
// (none, the same guide, a guide that went, one of lower priority or of
// another source).
func (s *Service) takes(m Member, e EPGURL, prio map[int64]guideRank) bool {
	if m.EPGID == "" || m.EPGURLID == 0 || m.EPGURLID == e.ID {
		return true
	}
	cur, ok := prio[m.EPGURLID]
	if !ok || cur.source != m.SourceID {
		return true
	}
	return prio[e.ID].rank < cur.rank
}

// fillStops gives a programme without a stop the next one's start on its
// channel (the last one an hour).
func fillStops(ps []XMLProgramme) {
	last := map[string]int{}
	for i := range ps {
		if j, ok := last[ps[i].Channel]; ok && ps[j].Stop.IsZero() && ps[i].Start.After(ps[j].Start) {
			ps[j].Stop = ps[i].Start
		}
		last[ps[i].Channel] = i
	}
	for i := range ps {
		if ps[i].Stop.IsZero() {
			ps[i].Stop = ps[i].Start.Add(time.Hour)
		}
	}
}

// progBatch: programmes per INSERT statement.
const progBatch = 200

func (s *Service) storeGuide(ctx context.Context, e EPGURL, chans []XMLChannel, progs []XMLProgramme, own []Member, matched []MatchResult, prio map[int64]guideRank) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM iptv_programmes WHERE epg_url_id=?`, `DELETE FROM iptv_xml_channels WHERE epg_url_id=?`} {
		if _, err := tx.ExecContext(ctx, q, e.ID); err != nil {
			return err
		}
	}
	chStmt, err := tx.PrepareContext(ctx, `INSERT INTO iptv_xml_channels (epg_url_id, xml_id, names_json, icon) VALUES (?,?,?,?)
		ON CONFLICT(epg_url_id, xml_id) DO NOTHING`)
	if err != nil {
		return err
	}
	defer chStmt.Close()
	for _, c := range chans {
		names := c.Names
		if names == nil {
			names = []string{}
		}
		if _, err := chStmt.ExecContext(ctx, e.ID, c.ID, jsonText(names), c.Icon); err != nil {
			return err
		}
	}
	for start := 0; start < len(progs); start += progBatch {
		part := progs[start:min(start+progBatch, len(progs))]
		var b strings.Builder
		b.WriteString(`INSERT INTO iptv_programmes (epg_url_id, xml_channel, start_ms, end_ms, title, sub_title, descr, category, icon) VALUES `)
		args := make([]any, 0, 9*len(part))
		for i, p := range part {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString("(?,?,?,?,?,?,?,?,?)")
			args = append(args, e.ID, p.Channel, p.Start.UnixMilli(), p.Stop.UnixMilli(), p.Title, p.SubTitle, p.Desc, p.Category, p.Icon)
		}
		if _, err := tx.ExecContext(ctx, b.String(), args...); err != nil {
			return err
		}
	}
	for i, m := range own {
		r := matched[i]
		switch {
		case r.XMLID != "" && s.takes(m, e, prio):
			if r.XMLID != m.EPGID || e.ID != m.EPGURLID || r.How != m.EPGHow {
				if err := setMemberEPG(ctx, tx, m.ID, e.ID, r.XMLID, r.How, false); err != nil {
					return err
				}
			}
		case r.XMLID == "" && m.EPGURLID == e.ID && m.EPGID != "":
			if err := setMemberEPG(ctx, tx, m.ID, 0, "", "", false); err != nil { // this guide lost it
				return err
			}
		}
	}
	return tx.Commit()
}

// ---- schedule ----

// Run refreshes what is due every Tick until ctx ends; OnOpen's refreshes
// run in ctx too.
func (s *Service) Run(ctx context.Context) {
	s.omu.Lock()
	s.base = ctx
	s.omu.Unlock()
	t := time.NewTicker(Tick)
	defer t.Stop()
	for {
		s.refreshDue(ctx, false)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// OnOpen refreshes, in the background, the sources and guides with
// «при открытии» whose last good refresh is older than OpenAge; a call
// while one runs does nothing.
func (s *Service) OnOpen(ctx context.Context) {
	s.omu.Lock()
	if s.opening {
		s.omu.Unlock()
		return
	}
	s.opening = true
	base := s.base
	s.omu.Unlock()
	if base == nil {
		base = context.WithoutCancel(ctx)
	}
	go func() {
		defer func() {
			s.omu.Lock()
			s.opening = false
			s.omu.Unlock()
		}()
		s.refreshDue(base, true)
	}()
}

// due: a refresh by interval (hours, 0 off) or, onOpen, by «при открытии».
func (s *Service) due(onOpen, refreshOnOpen bool, hours int, lastOK, lastTry time.Time) bool {
	now := s.now()
	if onOpen {
		return refreshOnOpen && now.Sub(lastOK) >= OpenAge && now.Sub(lastTry) >= time.Minute
	}
	return hours > 0 && now.Sub(lastOK) >= time.Duration(hours)*time.Hour && now.Sub(lastTry) >= min(RetryAfter, time.Duration(hours)*time.Hour)
}

func (s *Service) refreshDue(ctx context.Context, onOpen bool) {
	srcs, err := s.Store.Sources(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("iptv: sources: %v", err)
		}
		return
	}
	for _, src := range srcs {
		if ctx.Err() != nil {
			return
		}
		fresh := false
		if s.due(onOpen, src.RefreshOnOpen, src.RefreshHours, src.LastOK, src.LastTry) {
			res, err := s.RefreshSource(ctx, src.ID, false)
			if err != nil {
				log.Printf("iptv: source %d: %s", src.ID, ErrText(err))
				continue
			}
			fresh = res.New > 0
		}
		es, err := s.Store.EPGURLs(ctx, src.ID)
		if err != nil {
			log.Printf("iptv: guides of %d: %v", src.ID, err)
			continue
		}
		for _, e := range es {
			if ctx.Err() != nil {
				return
			}
			// new channels need their programmes: a guide not tried within the hour goes again
			if s.due(onOpen, e.RefreshOnOpen, e.RefreshHours, e.LastOK, e.LastTry) || (fresh && s.now().Sub(e.LastTry) >= OpenAge) ||
				(e.LastTry.IsZero() && src.LastOK.After(e.LastTry)) {
				if err := s.RefreshEPG(ctx, e.ID); err != nil {
					log.Printf("iptv: guide %d: %s", e.ID, ErrText(err))
				}
			}
		}
	}
}

// RefreshSourceEPGs refreshes every guide of a source (after the admin's
// «Обновить»); errors are in the guides' LastErr.
func (s *Service) RefreshSourceEPGs(ctx context.Context, sourceID int64) {
	es, err := s.Store.EPGURLs(ctx, sourceID)
	if err != nil {
		log.Printf("iptv: guides of %d: %v", sourceID, err)
		return
	}
	for _, e := range es {
		if err := s.RefreshEPG(ctx, e.ID); err != nil && ctx.Err() == nil {
			log.Printf("iptv: guide %d: %s", e.ID, ErrText(err))
		}
	}
}

// RejectDrop refuses a pending drop: the stored list stays, PendingDrop
// is cleared (the next refresh asks again if the provider still loses
// them).
func (s *Service) RejectDrop(ctx context.Context, id int64) (Source, error) {
	s.srcMu.Lock()
	defer s.srcMu.Unlock()
	src, err := s.Store.Source(ctx, id)
	if err != nil {
		return src, err
	}
	src.PendingDrop = 0
	return s.Store.SaveSource(ctx, src)
}

// GuideChannel is a channel of a stored guide (iptv_xml_channels).
type GuideChannel struct {
	EPGURLID, SourceID int64
	XMLID              string
	Names              []string
	Icon               string
}

// SearchGuide finds guide channels whose id or a display-name contains q
// (case folded), at most limit, by guide priority.
func (s *Service) SearchGuide(ctx context.Context, q string, limit int) ([]GuideChannel, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT c.epg_url_id, e.source_id, c.xml_id, c.names_json, c.icon
		FROM iptv_xml_channels c JOIN iptv_epg_urls e ON e.id=c.epg_url_id JOIN iptv_sources s ON s.id=e.source_id
		ORDER BY s.position, s.id, e.position, e.id, c.rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GuideChannel{}
	for rows.Next() {
		var g GuideChannel
		var names string
		if err := rows.Scan(&g.EPGURLID, &g.SourceID, &g.XMLID, &names, &g.Icon); err != nil {
			return nil, err
		}
		if q != "" && !strings.Contains(strings.ToLower(g.XMLID+"\n"+names), q) {
			continue
		}
		if err := json.Unmarshal([]byte(names), &g.Names); err != nil || g.Names == nil {
			g.Names = []string{}
		}
		out = append(out, g)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}
