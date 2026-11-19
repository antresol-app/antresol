package notify

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
)

// Topics from addons: an addon's manifest declares its notification
// topics (topic, title, audience), and the addon queues events for them
// through the core API (QueueAddon). Each is the topic
// "addon:<addon id>:<topic>" here: a profile turns it on or off like a
// core topic (notify_addon_prefs, 0092), and a letter lists it in a
// section of its own after the core topics, headed by the manifest title.
//
// A profile with no row for an addon topic takes the notify_prefs column
// of the same topic name when there is one, else the topic is off: a
// choice made before the topic moved to an addon carries over.

// AddonTopic is a notification topic an addon's manifest declares.
type AddonTopic struct {
	Addon, Topic string
	// Title names the topic in each interface language ("ru", "en").
	Title map[string]string
	// Audience: "admins" or "adults" (kids never hear from addons).
	Audience string
}

// AddonTopics are the topics of the addons installed and on, in any order
// (main sets it from the addon manager; nil: none).
type AddonTopics func(ctx context.Context) []AddonTopic

// AddonPref is a profile's choice for one addon topic (Prefs.AddonTopics):
// Title is the topic's name in the asker's language (answers only).
type AddonPref struct {
	Addon string `json:"addon"`
	Topic string `json:"topic"`
	Title string `json:"title"`
	On    bool   `json:"on"`
}

// AddonEvent is what an addon asks to tell (POST /notify of the core API).
// Exactly one of UserIDs and Audience names the recipients; with neither,
// the topic's own audience does. Audience may only narrow the topic's.
type AddonEvent struct {
	Addon, Topic string
	// Key names the thing within the addon: one key, one letter line.
	Key string
	// Link: a page of the app ("/item/3"); never a URL elsewhere.
	Link string
	// Poster: a remote TMDB image over https (anything else is dropped).
	Poster   string
	UserIDs  []int64
	Audience string
	// Subject and Body by interface language ("ru", "en").
	Subject, Body map[string]string
}

// Errors of QueueAddon.
var (
	ErrNoTopic      = errors.New("notify: the addon declares no such topic")
	ErrBadAudience  = errors.New("notify: bad audience")
	ErrKidRecipient = errors.New("notify: a kid profile cannot hear from addons")
	ErrBadLink      = errors.New("notify: the link is not a page of the app")
)

// Audiences of an addon topic.
const (
	AudienceAdmins = "admins"
	AudienceAdults = "adults"
)

const addonPrefix = "addon:"

var (
	addonIDRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`) // as addons' manifest ids
	addonTopicRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}$`) // as their notify topics
)

// AddonTopicKey is the topic of addon's topic here ("addon:<id>:<topic>").
func AddonTopicKey(addon, topic string) string { return addonPrefix + addon + ":" + topic }

// ParseAddonTopic splits an addon topic key; ok is false for anything else.
func ParseAddonTopic(key string) (addon, topic string, ok bool) {
	rest, ok := strings.CutPrefix(key, addonPrefix)
	if !ok {
		return "", "", false
	}
	addon, topic, ok = strings.Cut(rest, ":")
	if !ok || !addonIDRe.MatchString(addon) || !addonTopicRe.MatchString(topic) {
		return "", "", false
	}
	return addon, topic, true
}

// isAddonTopic: key is an addon topic's.
func isAddonTopic(key string) bool { return strings.HasPrefix(key, addonPrefix) }

// addonState is what Prefs knows of addon topics: the rows, and the fixed
// columns a topic without a row falls back to.
type addonState struct {
	rows   map[string]bool // addon topic key → enabled
	legacy map[string]bool // notify_prefs column → on
}

type addonTopicsHolder struct {
	mu sync.RWMutex
	f  AddonTopics
}

// SetAddonTopics names the function that lists the addons' topics.
func (s *Service) SetAddonTopics(f AddonTopics) {
	s.addonTopics.mu.Lock()
	s.addonTopics.f = f
	s.addonTopics.mu.Unlock()
}

// declared are the addons' valid topics, by addon id, then in manifest
// order, each once.
func (s *Service) declared(ctx context.Context) []AddonTopic {
	if s == nil {
		return nil
	}
	s.addonTopics.mu.RLock()
	f := s.addonTopics.f
	s.addonTopics.mu.RUnlock()
	if f == nil {
		return nil
	}
	var out []AddonTopic
	seen := map[string]bool{}
	for _, t := range f(ctx) {
		k := AddonTopicKey(t.Addon, t.Topic)
		if _, _, ok := ParseAddonTopic(k); !ok || seen[k] || (t.Audience != AudienceAdmins && t.Audience != AudienceAdults) {
			continue
		}
		seen[k] = true
		out = append(out, t)
	}
	slices.SortStableFunc(out, func(a, b AddonTopic) int { return strings.Compare(a.Addon, b.Addon) })
	return out
}

func (s *Service) declaredTopic(ctx context.Context, addon, topic string) (AddonTopic, bool) {
	for _, t := range s.declared(ctx) {
		if t.Addon == addon && t.Topic == topic {
			return t, true
		}
	}
	return AddonTopic{}, false
}

// admits: audience lets u hear (a kid never).
func admits(audience string, u auth.User) bool {
	if u.IsKid {
		return false
	}
	switch audience {
	case AudienceAdmins:
		return u.IsAdmin
	case AudienceAdults:
		return true
	}
	return false
}

// addonTopicsFor are the addon topic keys u may turn on.
func (s *Service) addonTopicsFor(ctx context.Context, u auth.User) []string {
	var out []string
	for _, t := range s.declared(ctx) {
		if admits(t.Audience, u) {
			out = append(out, AddonTopicKey(t.Addon, t.Topic))
		}
	}
	return out
}

// pickText is m in lang, else in Russian, else in English.
func pickText(m map[string]string, lang string) string {
	for _, l := range []string{lang, i18n.RU, i18n.EN} {
		if v := strings.TrimSpace(m[l]); v != "" {
			return v
		}
	}
	return ""
}

// AddonPrefs are u's addon topics with p's choices, titled in lang ([]
// for none).
func (s *Service) AddonPrefs(ctx context.Context, u auth.User, p Prefs, lang string) []AddonPref {
	out := []AddonPref{}
	for _, t := range s.declared(ctx) {
		if !admits(t.Audience, u) {
			continue
		}
		title := pickText(t.Title, lang)
		if title == "" {
			title = t.Topic
		}
		out = append(out, AddonPref{Addon: t.Addon, Topic: t.Topic, Title: title, On: p.On(AddonTopicKey(t.Addon, t.Topic))})
	}
	return out
}

// TopicHeading is a topic's name in lang: an addon topic's manifest title
// (its own name while the addon is not there), else Heading.
func (s *Service) TopicHeading(ctx context.Context, lang, topic string) string {
	addon, name, ok := ParseAddonTopic(topic)
	if !ok {
		return Heading(lang, topic)
	}
	if t, ok := s.declaredTopic(ctx, addon, name); ok {
		if h := pickText(t.Title, lang); h != "" {
			return h
		}
	}
	return name
}

// loadAddonRows reads uid's addon topic rows into p.
func (s *Service) loadAddonRows(ctx context.Context, uid int64, p *Prefs) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT addon_id, topic, enabled FROM notify_addon_prefs WHERE user_id=?`, uid)
	if err != nil {
		return err
	}
	defer rows.Close()
	p.addon.rows = map[string]bool{}
	for rows.Next() {
		var a, t string
		var on int
		if err := rows.Scan(&a, &t, &on); err != nil {
			return err
		}
		p.addon.rows[AddonTopicKey(a, t)] = on != 0
	}
	return rows.Err()
}

// setAddonPref saves uid's choice for an addon topic key.
func (s *Service) setAddonPref(ctx context.Context, uid int64, key string, on bool) error {
	addon, topic, ok := ParseAddonTopic(key)
	if !ok {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO notify_addon_prefs(user_id, addon_id, topic, enabled) VALUES(?,?,?,?)
		ON CONFLICT(user_id, addon_id, topic) DO UPDATE SET enabled=excluded.enabled`, uid, addon, topic, b2i(on))
	return err
}

// AppPath is p when it is a page of the app ("/…", not "//…", no
// backslash, space or control character), else "".
func AppPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return ""
	}
	for _, c := range p {
		if c == '\\' || c <= ' ' || c == 0x7f {
			return ""
		}
	}
	return p
}

// QueueAddon queues e for its recipients' next digests and reports how
// many were queued: the topic must be the addon's, every named recipient
// one its audience admits (a kid never), and only profiles with an address
// and the topic on are queued. A key the profile already has is skipped.
func (s *Service) QueueAddon(ctx context.Context, e AddonEvent) (int, error) {
	if s == nil {
		return 0, nil
	}
	t, ok := s.declaredTopic(ctx, e.Addon, e.Topic)
	if !ok {
		return 0, ErrNoTopic
	}
	audience := t.Audience
	if e.Audience != "" {
		if len(e.UserIDs) > 0 || (e.Audience != AudienceAdmins && e.Audience != AudienceAdults) ||
			(t.Audience == AudienceAdmins && e.Audience != AudienceAdmins) {
			return 0, ErrBadAudience
		}
		audience = e.Audience
	}
	if e.Link != "" && AppPath(e.Link) == "" {
		return 0, ErrBadLink
	}
	key := AddonTopicKey(e.Addon, e.Topic)
	var uids []int64
	if len(e.UserIDs) > 0 {
		for _, id := range e.UserIDs {
			u, err := s.Users.Get(ctx, id)
			if errors.Is(err, auth.ErrNotFound) {
				continue // gone meanwhile: nobody to tell
			}
			if err != nil {
				return 0, err
			}
			if u.IsKid {
				return 0, ErrKidRecipient
			}
			if !admits(audience, u) {
				return 0, ErrBadAudience
			}
			if !slices.Contains(uids, id) {
				uids = append(uids, id)
			}
		}
	} else {
		want, err := s.wanting(ctx, key)
		if err != nil {
			return 0, err
		}
		for id := range want {
			u, err := s.Users.Get(ctx, id)
			if err != nil || !admits(audience, u) {
				continue
			}
			uids = append(uids, id)
		}
		slices.Sort(uids)
	}
	ev := Event{Topic: key, Key: key + ":" + e.Key,
		Data: Data{Subject: e.Subject, Body: e.Body, Path: e.Link, Poster: e.Poster}}
	n := 0
	for _, id := range uids {
		p, err := s.Prefs(ctx, id)
		if err != nil {
			return n, err
		}
		if !p.hears() || !p.On(key) {
			continue
		}
		ev.UserID = id
		ok, err := s.insert(ctx, ev)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

// DropAddon forgets a removed addon: its profiles' choices and its unsent
// events (sent ones stay until pruned, as every sent event).
func (s *Service) DropAddon(ctx context.Context, addon string) error {
	if s == nil {
		return nil
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM notify_addon_prefs WHERE addon_id=?`, addon); err != nil {
		return err
	}
	prefix := addonPrefix + addon + ":"
	_, err := s.DB.ExecContext(ctx, `DELETE FROM notify_events WHERE sent_at IS NULL AND substr(topic, 1, ?)=?`, len(prefix), prefix)
	return err
}

// renderAddon is the one renderer of every addon topic: a line per event
// (its subject), the body as the detail, the link and the poster.
func renderAddon(lang string, evs []stored, link func(string) string) []entry {
	out := make([]entry, 0, len(evs))
	for _, e := range evs {
		out = append(out, entry{Line: pickText(e.data.Subject, lang), Detail: pickText(e.data.Body, lang),
			Link: link(AppPath(e.data.Path)), Poster: e.data.Poster})
	}
	return out
}
