// Package iptv imports external IPTV playlists (M3U, Xtream) and XMLTV
// guides into «ТВ»: sources and their entries (this file), sealed secrets
// (secret.go), the M3U parser (m3u.go), fetching with limits (fetch.go),
// the streaming XMLTV parser (xmltv.go), matching channels to the guide
// (match.go) and the editor's rules and identity (rules.go).
package iptv

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Kinds of a source.
const (
	KindM3U    = "m3u"
	KindXtream = "xtream"
)

// Store reads and writes the iptv_* tables.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Source is a row of iptv_sources.
type Source struct {
	ID   int64
	Name string
	// Kind: KindM3U or KindXtream.
	Kind string
	// URLEnc: the playlist URL, sealed (Sealer.Seal).
	URLEnc       []byte
	XtreamServer string
	XtreamUser   string
	// XtreamPassEnc: the Xtream password, sealed.
	XtreamPassEnc []byte
	// DefaultShown: new channels shown or hidden; nil: by the playlist's
	// size (shown under 300 channels).
	DefaultShown *bool
	// HideURLs: IPTV links carry /iptv/{token}/ext/{channel} instead of the
	// provider's URL.
	HideURLs        bool
	PresetTimeshift bool
	PresetAdult     bool
	// RefreshHours: 0 = off.
	RefreshHours  int
	RefreshOnOpen bool
	Position      int
	// Profiles that see the source; empty: every profile without limits.
	Profiles []int64
	LastOK   time.Time
	LastTry  time.Time
	LastErr  string
	// PendingDrop: channels a refresh would lose, waiting for confirmation.
	PendingDrop int
}

// EPGURL is a row of iptv_epg_urls.
type EPGURL struct {
	ID, SourceID int64
	// URLEnc: the guide URL, sealed.
	URLEnc        []byte
	Position      int
	RefreshHours  int
	RefreshOnOpen bool
	// Auto: taken from the playlist's url-tvg (replaced on refresh).
	Auto    bool
	LastOK  time.Time
	LastTry time.Time
	LastErr string
}

// Member is a row of iptv_members: one entry of a source's playlist.
type Member struct {
	ID, SourceID int64
	// ChannelID: the logical channel (channels.kind='external'); 0: none yet.
	ChannelID int64
	// Ident: tvg:<id> | url:<url without credentials> | name:<normalised>.
	Ident   string
	TvgID   string
	TvgName string
	Name    string
	Group   string
	Logo    string
	// URLEnc: the stream URL, sealed (it may carry credentials).
	URLEnc    []byte
	UserAgent string
	Referrer  string
	// EPGID: the matched XMLTV channel id of guide EPGURLID (0: any guide
	// of the source, by priority); EPGHow: tvg-id|name|norm-name|manual.
	EPGID     string
	EPGURLID  int64
	EPGHow    string
	EPGManual bool
	// Manual: the admin's decision (nil: none).
	Manual *Decision
	// Shown, Why: the decision in effect (the rules, then Manual), written
	// by SetDecided.
	Shown bool
	Why   string
	// Present: in the last good playlist.
	Present   bool
	Position  int
	FirstSeen time.Time
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Sources returns every source by position.
func (s *Store) Sources(ctx context.Context) ([]Source, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, kind, url_enc, xtream_server, xtream_user, xtream_pass_enc,
		default_shown, hide_urls, preset_timeshift, preset_adult, refresh_hours, refresh_on_open, position,
		profiles_json, last_ok_ms, last_try_ms, last_err, pending_drop
		FROM iptv_sources ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var src Source
		var def sql.NullInt64
		var hide, ts, adult, onOpen int
		var profiles string
		var ok, try int64
		if err := rows.Scan(&src.ID, &src.Name, &src.Kind, &src.URLEnc, &src.XtreamServer, &src.XtreamUser, &src.XtreamPassEnc,
			&def, &hide, &ts, &adult, &src.RefreshHours, &onOpen, &src.Position,
			&profiles, &ok, &try, &src.LastErr, &src.PendingDrop); err != nil {
			return nil, err
		}
		if def.Valid {
			v := def.Int64 != 0
			src.DefaultShown = &v
		}
		src.HideURLs, src.PresetTimeshift, src.PresetAdult, src.RefreshOnOpen = hide != 0, ts != 0, adult != 0, onOpen != 0
		if err := json.Unmarshal([]byte(profiles), &src.Profiles); err != nil {
			return nil, fmt.Errorf("iptv source %d profiles: %w", src.ID, err)
		}
		src.LastOK, src.LastTry = fromMS(ok), fromMS(try)
		out = append(out, src)
	}
	return out, rows.Err()
}

// Source returns one source (sql.ErrNoRows when there is none).
func (s *Store) Source(ctx context.Context, id int64) (Source, error) {
	all, err := s.Sources(ctx)
	if err != nil {
		return Source{}, err
	}
	for _, src := range all {
		if src.ID == id {
			return src, nil
		}
	}
	return Source{}, sql.ErrNoRows
}

// SaveSource inserts (ID 0) or updates a source and returns it with its ID.
func (s *Store) SaveSource(ctx context.Context, src Source) (Source, error) {
	if src.Kind == "" {
		src.Kind = KindM3U
	}
	if src.Kind != KindM3U && src.Kind != KindXtream {
		return src, fmt.Errorf("iptv: unknown source kind %q", src.Kind)
	}
	profiles := src.Profiles
	if profiles == nil {
		profiles = []int64{}
	}
	pj, _ := json.Marshal(profiles)
	var def any
	if src.DefaultShown != nil {
		def = b2i(*src.DefaultShown)
	}
	args := []any{src.Name, src.Kind, src.URLEnc, src.XtreamServer, src.XtreamUser, src.XtreamPassEnc,
		def, b2i(src.HideURLs), b2i(src.PresetTimeshift), b2i(src.PresetAdult), src.RefreshHours, b2i(src.RefreshOnOpen), src.Position,
		string(pj), ms(src.LastOK), ms(src.LastTry), src.LastErr, src.PendingDrop}
	if src.ID == 0 {
		res, err := s.DB.ExecContext(ctx, `INSERT INTO iptv_sources (name, kind, url_enc, xtream_server, xtream_user, xtream_pass_enc,
			default_shown, hide_urls, preset_timeshift, preset_adult, refresh_hours, refresh_on_open, position,
			profiles_json, last_ok_ms, last_try_ms, last_err, pending_drop) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
		if err != nil {
			return src, err
		}
		src.ID, err = res.LastInsertId()
		return src, err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE iptv_sources SET name=?, kind=?, url_enc=?, xtream_server=?, xtream_user=?, xtream_pass_enc=?,
		default_shown=?, hide_urls=?, preset_timeshift=?, preset_adult=?, refresh_hours=?, refresh_on_open=?, position=?,
		profiles_json=?, last_ok_ms=?, last_try_ms=?, last_err=?, pending_drop=? WHERE id=?`, append(args, src.ID)...)
	if err != nil {
		return src, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return src, sql.ErrNoRows
	}
	return src, nil
}

// DeleteSource removes a source with its guide URLs, programmes and
// members (the logical channel rows are the caller's).
func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM iptv_sources WHERE id=?`, id)
	return err
}

// EPGURLs returns a source's guide URLs by priority.
func (s *Store) EPGURLs(ctx context.Context, sourceID int64) ([]EPGURL, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, source_id, url_enc, position, refresh_hours, refresh_on_open, auto,
		last_ok_ms, last_try_ms, last_err FROM iptv_epg_urls WHERE source_id=? ORDER BY position, id`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EPGURL
	for rows.Next() {
		var e EPGURL
		var onOpen, auto int
		var ok, try int64
		if err := rows.Scan(&e.ID, &e.SourceID, &e.URLEnc, &e.Position, &e.RefreshHours, &onOpen, &auto, &ok, &try, &e.LastErr); err != nil {
			return nil, err
		}
		e.RefreshOnOpen, e.Auto = onOpen != 0, auto != 0
		e.LastOK, e.LastTry = fromMS(ok), fromMS(try)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SaveEPGURL inserts (ID 0) or updates a guide URL.
func (s *Store) SaveEPGURL(ctx context.Context, e EPGURL) (EPGURL, error) {
	if e.SourceID == 0 {
		return e, errors.New("iptv: epg url without a source")
	}
	args := []any{e.SourceID, e.URLEnc, e.Position, e.RefreshHours, b2i(e.RefreshOnOpen), b2i(e.Auto), ms(e.LastOK), ms(e.LastTry), e.LastErr}
	if e.ID == 0 {
		res, err := s.DB.ExecContext(ctx, `INSERT INTO iptv_epg_urls (source_id, url_enc, position, refresh_hours, refresh_on_open, auto,
			last_ok_ms, last_try_ms, last_err) VALUES (?,?,?,?,?,?,?,?,?)`, args...)
		if err != nil {
			return e, err
		}
		e.ID, err = res.LastInsertId()
		return e, err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE iptv_epg_urls SET source_id=?, url_enc=?, position=?, refresh_hours=?, refresh_on_open=?, auto=?,
		last_ok_ms=?, last_try_ms=?, last_err=? WHERE id=?`, append(args, e.ID)...)
	if err != nil {
		return e, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return e, sql.ErrNoRows
	}
	return e, nil
}

// DeleteEPGURL removes a guide URL with its programmes and channels.
func (s *Store) DeleteEPGURL(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM iptv_epg_urls WHERE id=?`, id)
	return err
}

// Members returns a source's entries by position (absent ones included).
func (s *Store) Members(ctx context.Context, sourceID int64) ([]Member, error) {
	return s.members(ctx, `WHERE source_id=? ORDER BY present DESC, position, id`, sourceID)
}

// AllMembers returns the entries of every source (absent ones included),
// by source, then position.
func (s *Store) AllMembers(ctx context.Context) ([]Member, error) {
	return s.members(ctx, `ORDER BY source_id, present DESC, position, id`)
}

// ChannelMembers returns the entries of one logical channel.
func (s *Store) ChannelMembers(ctx context.Context, channelID int64) ([]Member, error) {
	return s.members(ctx, `WHERE channel_id=? ORDER BY source_id, position, id`, channelID)
}

const memberCols = `id, source_id, COALESCE(channel_id, 0), ident, tvg_id, tvg_name, name, grp, logo, url_enc,
	user_agent, referrer, epg_id, epg_url_id, epg_how, epg_manual, manual_json, shown, why, present, position, first_seen_ms`

func (s *Store) members(ctx context.Context, where string, args ...any) ([]Member, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+memberCols+` FROM iptv_members `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var epgManual, shown, present int
		var manual string
		var seen int64
		if err := rows.Scan(&m.ID, &m.SourceID, &m.ChannelID, &m.Ident, &m.TvgID, &m.TvgName, &m.Name, &m.Group, &m.Logo, &m.URLEnc,
			&m.UserAgent, &m.Referrer, &m.EPGID, &m.EPGURLID, &m.EPGHow, &epgManual, &manual, &shown, &m.Why, &present, &m.Position, &seen); err != nil {
			return nil, err
		}
		m.EPGManual, m.Shown, m.Present, m.FirstSeen = epgManual != 0, shown != 0, present != 0, fromMS(seen)
		if manual != "" {
			var d Decision
			if err := json.Unmarshal([]byte(manual), &d); err != nil {
				return nil, fmt.Errorf("iptv member %d manual: %w", m.ID, err)
			}
			m.Manual = &d
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReplaceMembers stores a source's playlist: entries are kept by Ident
// (their channel, guide binding and manual decision stay), new ones are
// added, and those not in ms are marked Present=false. A member's
// ChannelID is written only when set; the guide binding and the manual
// decision only through SetMemberEPG and SetManual.
func (s *Store) ReplaceMembers(ctx context.Context, sourceID int64, ms []Member) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE iptv_members SET present=0 WHERE source_id=?`, sourceID); err != nil {
		return err
	}
	now := s.now().UnixMilli()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO iptv_members (source_id, channel_id, ident, tvg_id, tvg_name, name, grp, logo, url_enc,
		user_agent, referrer, present, position, first_seen_ms) VALUES (?,?,?,?,?,?,?,?,?,?,?,1,?,?)
		ON CONFLICT(source_id, ident) DO UPDATE SET
		  channel_id=COALESCE(excluded.channel_id, iptv_members.channel_id),
		  tvg_id=excluded.tvg_id, tvg_name=excluded.tvg_name, name=excluded.name, grp=excluded.grp, logo=excluded.logo, url_enc=excluded.url_enc,
		  user_agent=excluded.user_agent, referrer=excluded.referrer, present=1, position=excluded.position`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, m := range ms {
		if m.Ident == "" {
			return fmt.Errorf("iptv: member %q without an ident", m.Name)
		}
		var ch any
		if m.ChannelID != 0 {
			ch = m.ChannelID
		}
		pos := m.Position
		if pos == 0 {
			pos = i
		}
		if _, err := stmt.ExecContext(ctx, sourceID, ch, m.Ident, m.TvgID, m.TvgName, m.Name, m.Group, m.Logo, m.URLEnc,
			m.UserAgent, m.Referrer, pos, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetMemberChannel points a member at its logical channel row.
func (s *Store) SetMemberChannel(ctx context.Context, memberID, channelID int64) error {
	var ch any
	if channelID != 0 {
		ch = channelID
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE iptv_members SET channel_id=? WHERE id=?`, ch, memberID)
	return err
}

// SetMemberEPG stores a member's guide binding: channel xmlID of guide
// epgURLID (0: any guide of the source; how: tvg-id|name|norm-name|manual;
// xmlID "" clears it). A manual binding is replaced only by another manual
// call: an automatic one (manual=false) leaves it.
func (s *Store) SetMemberEPG(ctx context.Context, memberID, epgURLID int64, xmlID, how string, manual bool) error {
	return setMemberEPG(ctx, s.DB, memberID, epgURLID, xmlID, how, manual)
}

func setMemberEPG(ctx context.Context, ex execer, memberID, epgURLID int64, xmlID, how string, manual bool) error {
	if xmlID == "" {
		epgURLID, how = 0, ""
	}
	q := `UPDATE iptv_members SET epg_id=?, epg_url_id=?, epg_how=?, epg_manual=? WHERE id=?`
	if !manual {
		q += ` AND epg_manual=0`
	}
	_, err := ex.ExecContext(ctx, q, xmlID, epgURLID, how, b2i(manual), memberID)
	return err
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SetDecided stores the decision in effect of members (shown, why), in one
// transaction.
func (s *Store) SetDecided(ctx context.Context, ds map[int64]Decision) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE iptv_members SET shown=?, why=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, d := range ds {
		if _, err := stmt.ExecContext(ctx, b2i(d.Shown != nil && *d.Shown), d.Why, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetManual stores (nil: clears) a member's manual decision.
func (s *Store) SetManual(ctx context.Context, memberID int64, d *Decision) error {
	v := ""
	if d != nil {
		b, err := json.Marshal(d)
		if err != nil {
			return err
		}
		v = string(b)
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE iptv_members SET manual_json=? WHERE id=?`, v, memberID)
	return err
}

// EPGURL returns one guide URL (sql.ErrNoRows when there is none).
func (s *Store) EPGURL(ctx context.Context, id int64) (EPGURL, error) {
	var src int64
	if err := s.DB.QueryRowContext(ctx, `SELECT source_id FROM iptv_epg_urls WHERE id=?`, id).Scan(&src); err != nil {
		return EPGURL{}, err
	}
	es, err := s.EPGURLs(ctx, src)
	if err != nil {
		return EPGURL{}, err
	}
	for _, e := range es {
		if e.ID == id {
			return e, nil
		}
	}
	return EPGURL{}, sql.ErrNoRows
}

// settingXtream: Xtream sources are on (settings "iptv_xtream" = "1").
const settingXtream = "iptv_xtream"

// XtreamEnabled: the admin turned Xtream sources on in the TV settings
// (off by default).
func (s *Store) XtreamEnabled(ctx context.Context) bool {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingXtream).Scan(&v)
	return err == nil && v == "1"
}

// SetXtreamEnabled turns Xtream sources on or off.
func (s *Store) SetXtreamEnabled(ctx context.Context, on bool) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		settingXtream, strconv.Itoa(b2i(on)))
	return err
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
