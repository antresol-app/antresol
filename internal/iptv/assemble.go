package iptv

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/channels"
)

// What a viewer sees of the external channels (spec clarification 1):
// the editor (the decision in effect) → the profile's access to the
// source → the set (its sources, its hidden groups and channels) →
// limited profiles (an age or a screen-time limit) see none. The same
// channel in several sources of the set is one channel whose streams are
// ordered by source position: the first is primary, the others fallbacks.

// Set is a row of iptv_sets: which sources (and which of their groups and
// channels not) a device or a link sees. ID 0 is «all my channels».
type Set struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Sources        []int64  `json:"sources"`
	HiddenGroups   []string `json:"hiddenGroups"`
	HiddenChannels []int64  `json:"hiddenChannels"`
}

// Stream is one way to play a channel: a source's entry.
type Stream struct {
	URL, UserAgent, Referrer string
	SourceID                 int64
	// HideURL: the source hides provider addresses (IPTV links and app
	// answers carry a redirect of Antresol instead).
	HideURL bool
}

// EPGKey is a guide channel: xml id XMLID of guide EPGURLID.
type EPGKey struct {
	EPGURLID int64
	XMLID    string
}

// ExtChannel is an external channel as one viewer and set see it.
type ExtChannel struct {
	Channel   channels.Channel
	ChannelID int64
	// Streams: primary first.
	Streams []Stream
	// EPGKeys: where its programmes are, by priority.
	EPGKeys []EPGKey
	// TvgID, Logo: of the first entry that has one.
	TvgID, Logo string
	// HideURL: the primary stream's source hides provider addresses.
	HideURL bool
}

// Limited: the viewer sees no external channels (an age limit, or screen
// time limits: Antresol cannot count what it does not stream).
func (s *Service) Limited(ctx context.Context, v channels.Viewer) bool {
	if v.MaxRating != "" {
		return true
	}
	return s.ScreenLimited != nil && s.ScreenLimited(ctx, v.ID)
}

// Visible returns the external channels viewer v sees in set setID (0:
// every source the profile may see), by channel id.
func (s *Service) Visible(ctx context.Context, v channels.Viewer, setID int64) (map[int64]ExtChannel, error) {
	out := map[int64]ExtChannel{}
	if s.Limited(ctx, v) {
		return out, nil
	}
	set, err := s.setOrAll(ctx, setID)
	if err != nil {
		return nil, err
	}
	srcs, err := s.Store.Sources(ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[int64]Source{}
	for _, src := range srcs {
		if len(src.Profiles) > 0 && !slices.Contains(src.Profiles, v.ID) {
			continue
		}
		if set.ID != 0 && !slices.Contains(set.Sources, src.ID) {
			continue
		}
		allowed[src.ID] = src
	}
	if len(allowed) == 0 {
		return out, nil
	}
	guides := map[int64][]int64{}
	for id := range allowed {
		es, err := s.Store.EPGURLs(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			guides[id] = append(guides[id], e.ID)
		}
	}
	rows, err := s.externalRows(ctx)
	if err != nil {
		return nil, err
	}
	ms, err := s.Store.AllMembers(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := allowed[ms[i].SourceID], allowed[ms[j].SourceID]
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return ms[i].Position < ms[j].Position
	})
	hiddenGroup := map[string]bool{}
	for _, g := range set.HiddenGroups {
		hiddenGroup[strings.ToLower(strings.TrimSpace(g))] = true
	}
	for _, m := range ms {
		src, ok := allowed[m.SourceID]
		if !ok || !m.Present || !m.Shown || m.ChannelID == 0 {
			continue
		}
		c, ok := rows[m.ChannelID]
		if !ok || !c.Enabled || hiddenGroup[strings.ToLower(strings.TrimSpace(c.Category))] || slices.Contains(set.HiddenChannels, c.ID) {
			continue
		}
		u, err := s.Seal.Open(m.URLEnc)
		if err != nil || u == "" {
			continue
		}
		x, seen := out[c.ID]
		if !seen {
			x = ExtChannel{Channel: c, ChannelID: c.ID, HideURL: src.HideURLs}
		}
		x.Streams = append(x.Streams, Stream{URL: u, UserAgent: m.UserAgent, Referrer: m.Referrer, SourceID: src.ID, HideURL: src.HideURLs})
		if x.TvgID == "" {
			x.TvgID = m.TvgID
		}
		if x.Logo == "" {
			x.Logo = m.Logo
		}
		if m.EPGID != "" {
			keys := []EPGKey{{m.EPGURLID, m.EPGID}}
			if m.EPGURLID == 0 {
				keys = keys[:0]
				for _, g := range guides[m.SourceID] {
					keys = append(keys, EPGKey{g, m.EPGID})
				}
			}
			for _, k := range keys {
				if !slices.Contains(x.EPGKeys, k) {
					x.EPGKeys = append(x.EPGKeys, k)
				}
			}
		}
		out[c.ID] = x
	}
	return out, nil
}

// externalRows reads the external channel rows by id.
func (s *Service) externalRows(ctx context.Context) (map[int64]channels.Channel, error) {
	all, err := s.chans().All(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]channels.Channel{}
	for _, c := range all {
		if c.External() {
			out[c.ID] = c
		}
	}
	return out, nil
}

func (s *Service) setOrAll(ctx context.Context, id int64) (Set, error) {
	if id == 0 {
		return Set{}, nil
	}
	set, err := s.Store.Set(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Set{}, nil // a set that went: all my channels
	}
	return set, err
}

// SetFor is the set a request sees: its IPTV link's (app_tokens.set_id),
// else its app device's, else the profile's default (device ""), else 0.
func (s *Service) SetFor(ctx context.Context, userID int64, deviceID, token string) int64 {
	db := s.Store.DB
	if token != "" {
		var id sql.NullInt64
		if err := db.QueryRowContext(ctx, `SELECT set_id FROM app_tokens WHERE token=? AND user_id=?`, token, userID).Scan(&id); err == nil && id.Valid {
			return id.Int64
		}
	}
	devs := []string{""}
	if deviceID != "" {
		devs = []string{deviceID, ""}
	}
	for _, dev := range devs {
		var id int64
		if err := db.QueryRowContext(ctx, `SELECT set_id FROM iptv_set_assign WHERE user_id=? AND device_id=?`, userID, dev).Scan(&id); err == nil {
			return id
		}
	}
	return 0
}

// ---- sets ----

// Sets returns every set by name.
func (s *Store) Sets(ctx context.Context) ([]Set, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, sources_json, hidden_groups_json, hidden_channels_json FROM iptv_sets ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Set{}
	for rows.Next() {
		var x Set
		var a, b, c string
		if err := rows.Scan(&x.ID, &x.Name, &a, &b, &c); err != nil {
			return nil, err
		}
		if err := errors.Join(json.Unmarshal([]byte(a), &x.Sources), json.Unmarshal([]byte(b), &x.HiddenGroups), json.Unmarshal([]byte(c), &x.HiddenChannels)); err != nil {
			return nil, fmt.Errorf("iptv set %d: %w", x.ID, err)
		}
		x.norm()
		out = append(out, x)
	}
	return out, rows.Err()
}

func (x *Set) norm() {
	if x.Sources == nil {
		x.Sources = []int64{}
	}
	if x.HiddenGroups == nil {
		x.HiddenGroups = []string{}
	}
	if x.HiddenChannels == nil {
		x.HiddenChannels = []int64{}
	}
}

// Set returns one set (sql.ErrNoRows when there is none).
func (s *Store) Set(ctx context.Context, id int64) (Set, error) {
	all, err := s.Sets(ctx)
	if err != nil {
		return Set{}, err
	}
	for _, x := range all {
		if x.ID == id {
			return x, nil
		}
	}
	return Set{}, sql.ErrNoRows
}

// SaveSet inserts (ID 0) or updates a set.
func (s *Store) SaveSet(ctx context.Context, x Set) (Set, error) {
	x.Name = strings.TrimSpace(x.Name)
	x.norm()
	args := []any{x.Name, jsonText(x.Sources), jsonText(x.HiddenGroups), jsonText(x.HiddenChannels)}
	if x.ID == 0 {
		res, err := s.DB.ExecContext(ctx, `INSERT INTO iptv_sets (name, sources_json, hidden_groups_json, hidden_channels_json) VALUES (?,?,?,?)`, args...)
		if err != nil {
			return x, err
		}
		x.ID, err = res.LastInsertId()
		return x, err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE iptv_sets SET name=?, sources_json=?, hidden_groups_json=?, hidden_channels_json=? WHERE id=?`, append(args, x.ID)...)
	if err != nil {
		return x, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return x, sql.ErrNoRows
	}
	return x, nil
}

// DeleteSet removes a set; its devices and links go back to «all my
// channels».
func (s *Store) DeleteSet(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM iptv_sets WHERE id=?`, id)
	return err
}

// AssignSet gives a profile's app device (deviceID ”: the profile's
// default) set setID (0: «all my channels», the assignment removed).
func (s *Store) AssignSet(ctx context.Context, userID int64, deviceID string, setID int64) error {
	if setID == 0 {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM iptv_set_assign WHERE user_id=? AND device_id=?`, userID, deviceID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO iptv_set_assign (user_id, device_id, set_id) VALUES (?,?,?)
		ON CONFLICT(user_id, device_id) DO UPDATE SET set_id=excluded.set_id`, userID, deviceID, setID)
	return err
}

// Assignments returns every device's set: user → device → set.
func (s *Store) Assignments(ctx context.Context) (map[int64]map[string]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id, device_id, set_id FROM iptv_set_assign`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]int64{}
	for rows.Next() {
		var u, set int64
		var d string
		if err := rows.Scan(&u, &d, &set); err != nil {
			return nil, err
		}
		if out[u] == nil {
			out[u] = map[string]int64{}
		}
		out[u][d] = set
	}
	return out, rows.Err()
}

// SetTokenSet gives an IPTV link (an app token) set setID (0: none, the
// device's or the profile's).
func (s *Store) SetTokenSet(ctx context.Context, token string, setID int64) error {
	var v any
	if setID != 0 {
		v = setID
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE app_tokens SET set_id=? WHERE token=?`, v, token)
	return err
}

// Programme is a stored programme of an external channel.
type Programme struct {
	EPGURLID                              int64
	XMLID                                 string
	Start, End                            time.Time
	Title, SubTitle, Desc, Category, Icon string
}

// Programmes returns the programmes overlapping [from, to) of channels
// (channel id → its guide keys by priority): those of the first key that
// has any, by start.
func (s *Service) Programmes(ctx context.Context, chans map[int64][]EPGKey, from, to time.Time) (map[int64][]Programme, error) {
	ids := map[string]bool{}
	for _, ks := range chans {
		for _, k := range ks {
			ids[k.XMLID] = true
		}
	}
	all := make([]string, 0, len(ids))
	for id := range ids {
		all = append(all, id)
	}
	found := map[EPGKey][]Programme{}
	for start := 0; start < len(all); start += 500 {
		part := all[start:min(start+500, len(all))]
		args := []any{}
		for _, id := range part {
			args = append(args, id)
		}
		args = append(args, from.UnixMilli(), to.UnixMilli())
		rows, err := s.Store.DB.QueryContext(ctx, `SELECT epg_url_id, xml_channel, start_ms, end_ms, title, sub_title, descr, category, icon
			FROM iptv_programmes WHERE xml_channel IN (?`+strings.Repeat(",?", len(part)-1)+`) AND end_ms > ? AND start_ms < ? ORDER BY start_ms`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p Programme
			var a, b int64
			if err := rows.Scan(&p.EPGURLID, &p.XMLID, &a, &b, &p.Title, &p.SubTitle, &p.Desc, &p.Category, &p.Icon); err != nil {
				rows.Close()
				return nil, err
			}
			p.Start, p.End = time.UnixMilli(a).UTC(), time.UnixMilli(b).UTC()
			k := EPGKey{p.EPGURLID, p.XMLID}
			found[k] = append(found[k], p)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	out := map[int64][]Programme{}
	for ch, ks := range chans {
		for _, k := range ks {
			if ps := found[k]; len(ps) > 0 {
				out[ch] = ps
				break
			}
		}
	}
	return out, nil
}
