// Package notify is «Уведомления на почту»: a profile hears by mail when
// something it cares about arrives — new episodes of a series it watches,
// a title it wanted, a kid's request answered, (another build) one
// more topic for admins.
//
// Events are kept in the database as they happen (notify_events, 0056), so
// a restart neither loses nor repeats them: the key of an event is unique
// per profile. A digest gathers a profile's events into one letter, at most
// once an hour (Every), a while after the last one came (Settle), never
// during quiet hours (22:00–08:00 server time, when the profile keeps them)
// — those wait for the morning. Nothing is queued for a profile that has no
// address or has not turned the topic on, and nothing is sent to an
// address its own verification letter did not confirm.
//
// Telegram is a second channel (telegram.go): a profile that linked its
// chat with the server's bot gets the same events there, as plain text, on
// the same schedule. A profile chooses its channels: the address and the
// linked chat each work when they are set and on.
//
// The letters are text with a simple HTML form, in the profile's language;
// pictures are only remote TMDB poster links (no attachments), and the
// links to the titles and to «Отписаться» (a signed token, no login) need
// the server's public address (VAULT_PUBLIC_URL; none: no links).
package notify

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/mail"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/mailer"
	"github.com/antresol-app/antresol/internal/telegram"
)

// Topics: what a profile may hear about.
const (
	// Episodes: new episodes of a series the profile watches (progress),
	// keeps in «Хочу посмотреть» or (sources build) follows.
	Episodes = "episodes"
	// Available: a title the profile wanted is in the library and plays.
	Available = "available"
	// Requests: a kid's request was approved or is in the library.
	Requests = "requests"
)

// legacyDownloads: a column older servers wrote; addon topics of that name
// read it (D1: an addon topic without a row takes the column of its topic
// name). The core has no topic of that name; only unsubscribe-all writes
// the column (clearing it, so the fallback is off too).
const legacyDownloads = "downloads"

// AllTopics in the order letters list them.
var AllTopics = []string{Episodes, Available, Requests}

// TopicsFor are the topics u may turn on: the shared ones, for a kid
// Requests wherever kids' requests are open (RequestsOpen), and the addons'
// topics whose audience admits u ("addon:<id>:<topic>", by addon id; a kid
// none).
func (s *Service) TopicsFor(ctx context.Context, u auth.User) []string {
	out := []string{Episodes, Available}
	if u.IsKid && s != nil && s.RequestsOpen != nil && s.RequestsOpen(ctx) {
		out = append(out, Requests)
	}
	return append(out, s.addonTopicsFor(ctx, u)...)
}

// Prefs are a profile's notification settings.
type Prefs struct {
	Email string `json:"email"`
	// Verified: the address was confirmed by its verification letter.
	Verified   bool   `json:"verified"`
	Episodes   bool   `json:"episodes"`
	Available  bool   `json:"available"`
	Requests   bool   `json:"requests"`
	QuietHours bool   `json:"quietHours"`
	Lang       string `json:"lang"` // ru | en; "" the server's
	// Bounced: the mail server refused the address for good («Адрес не
	// принимает письма»); it is unconfirmed until confirmed again.
	Bounced bool `json:"bounced"`
	// AddonTopics: answers list the addon topics the profile may turn on
	// (AddonPrefs); a save changes the rows it lists and no other.
	AddonTopics []AddonPref `json:"addonTopics"`
	// EmailOn and TelegramOn are the channels the profile chose (both on
	// until it turns one off); mail also needs an address, Telegram a linked
	// chat. TelegramLinked and TelegramName (what to call the chat, «@name»)
	// say whether it is. All four are answers only: SetPrefs leaves them
	// (SetChannels turns a channel on or off, linking is telegram.go's).
	EmailOn        bool   `json:"emailOn"`
	TelegramOn     bool   `json:"telegramOn"`
	TelegramLinked bool   `json:"telegramLinked"`
	TelegramName   string `json:"telegramName"`

	addon addonState
}

// emailActive: mail is where this profile hears (an address, and the channel
// on); the address need not be confirmed yet, events wait for it.
func (p Prefs) emailActive() bool { return p.Email != "" && p.EmailOn }

// telegramActive: the profile's chat is linked and the channel on.
func (p Prefs) telegramActive() bool { return p.TelegramLinked && p.TelegramOn }

// hears reports whether any channel of the profile works.
func (p Prefs) hears() bool { return p.emailActive() || p.telegramActive() }

// TopicAll names every topic in an unsubscribe token (List-Unsubscribe).
const TopicAll = "all"

// On reports whether topic is turned on. An addon topic without a row
// takes the fixed column of its topic name, else it is off.
func (p Prefs) On(topic string) bool {
	if isAddonTopic(topic) {
		if on, ok := p.addon.rows[topic]; ok {
			return on
		}
		_, name, _ := ParseAddonTopic(topic)
		return p.addon.legacy[name]
	}
	switch topic {
	case Episodes:
		return p.Episodes
	case Available:
		return p.Available
	case Requests:
		return p.Requests
	}
	return false
}

func (p *Prefs) set(topic string, on bool) {
	switch topic {
	case Episodes:
		p.Episodes = on
	case Available:
		p.Available = on
	case Requests:
		p.Requests = on
	}
}

// Language is the language of p's letters.
func (p Prefs) Language() string {
	if p.Lang == i18n.EN || p.Lang == i18n.RU {
		return p.Lang
	}
	return i18n.Default
}

// Errors of SetPrefs and SendVerification.
var (
	ErrBadEmail   = errors.New("notify: not an e-mail address")
	ErrNoMail     = errors.New("notify: the server has no mail account")
	ErrNoEmail    = errors.New("notify: the profile has no address")
	ErrTooSoon    = errors.New("notify: a verification letter went a moment ago")
	ErrTooMany    = errors.New("notify: too many verification letters today")
	ErrBadToken   = errors.New("notify: invalid link")
	ErrStaleToken = errors.New("notify: the link is for another address")
)

// Defaults of Service.
const (
	DefaultEvery  = time.Hour
	DefaultSettle = 10 * time.Minute
	QuietFrom     = 22 // quiet hours: 22:00…
	QuietTo       = 8  // …08:00, server time
	// verifyGap: one verification letter a minute per profile.
	verifyGap = time.Minute
	// verifyTTL: how long a verification link works.
	verifyTTL = 7 * 24 * time.Hour
	// keepSent: how long sent events are kept (the same thing is not
	// queued twice meanwhile); keepPending: an unsent event older than
	// this (an address never confirmed) is dropped.
	keepSent    = 90 * 24 * time.Hour
	keepPending = 30 * 24 * time.Hour
	// retryAfter: a failed letter is tried again after this, doubling with
	// each failure in a row up to maxBackoff.
	retryAfter = 15 * time.Minute
	maxBackoff = 24 * time.Hour
	// sendingStale: a send marked this long ago never finished (a crash):
	// its events count as sent.
	sendingStale = 10 * time.Minute
	// verification letters a day: per profile, per address
	verifyPerProfile = 5
	verifyPerAddress = 3
	// returnGap: a watchlist title back on disk is news once in this long
	returnGap = 30 * 24 * time.Hour
	// keepSeen: how long notify_seen remembers what a profile was told
	keepSeen = 365 * 24 * time.Hour
)

// InterestFunc names more profiles that care about a title (the full
// build's «Следить» and «Хочу посмотреть» follows): kind is a library kind
// (series, movie), tmdbID its TMDB id.
type InterestFunc func(ctx context.Context, kind string, tmdbID int64) ([]int64, error)

// Service queues and sends the letters.
type Service struct {
	DB    *sql.DB
	Users *auth.Store
	// Secret signs the links of the letters (verify, unsubscribe).
	Secret []byte
	// Mail is the server's SMTP account and whether it is set up.
	Mail func(ctx context.Context) (mailer.Config, bool)
	// Send delivers a letter (nil: mailer.Send; tests: a fake).
	Send func(ctx context.Context, cfg mailer.Config, m mailer.Message) error
	// PublicURL is how browsers reach the server ("https://antresol.example",
	// no trailing slash; "": letters carry no links).
	PublicURL string
	// PublicURLOf, when set, is asked for the address instead of PublicURL
	// each time (an admin may save it while the server runs).
	PublicURLOf func() string
	// Now is the clock (nil: time.Now); Location the time zone of quiet
	// hours (nil: time.Local).
	Now      func() time.Time
	Location *time.Location
	// Every is the least time between two digests of a profile (0:
	// DefaultEvery); Settle how long a digest waits after its newest event
	// for more to come (0: DefaultSettle; negative: no wait).
	Every, Settle time.Duration
	// Interest: more profiles that care about a title (sources build).
	Interest []InterestFunc
	// Collectors run after the library collector (sources build: requests
	// whose title the library has now).
	Collectors []func(ctx context.Context) error
	// RequestsOpen says whether kids' requests work on this server (nil:
	// not here); when it does, kids may turn Requests on in either edition.
	RequestsOpen func(ctx context.Context) bool
	// TG calls the Telegram Bot API (nil: the real one; tests: a fake
	// server); TelegramGap is the least time between two sends to Telegram
	// (0: DefaultTelegramGap; negative: none).
	TG          *telegram.Client
	TelegramGap time.Duration
	// PollTimeout bounds one getUpdates (0: the wait plus a margin; tests).
	PollTimeout time.Duration
	Log         *slog.Logger

	collectMu sync.Mutex
	// the Telegram poller's and limiter's state (telegram.go)
	telegramState
	// pauseHook replaces the waits after a failed poll (tests)
	pauseHook func(time.Duration)
	// addonTopics lists the addons' topics (SetAddonTopics)
	addonTopics addonTopicsHolder
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) loc() *time.Location {
	if s.Location != nil {
		return s.Location
	}
	return time.Local
}

func (s *Service) every() time.Duration {
	if s.Every > 0 {
		return s.Every
	}
	return DefaultEvery
}

func (s *Service) settle() time.Duration {
	switch {
	case s.Settle > 0:
		return s.Settle
	case s.Settle < 0:
		return 0
	}
	return DefaultSettle
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default().With("component", "notify")
}

func (s *Service) send(ctx context.Context, cfg mailer.Config, m mailer.Message) error {
	if s.Send != nil {
		return s.Send(ctx, cfg, m)
	}
	return mailer.Send(ctx, cfg, m)
}

// MailReady is the server's mail account and whether it is set up.
func (s *Service) MailReady(ctx context.Context) (mailer.Config, bool) {
	if s.Mail == nil {
		return mailer.Config{}, false
	}
	return s.Mail(ctx)
}

// Links reports whether letters carry links (a public address is set).
func (s *Service) Links() bool { return s.linker()("/") != "" }

// Heading is a topic's name in lang (the section of a letter).
func Heading(lang, topic string) string {
	if h := headings[topic]; h != nil {
		return h(lang)
	}
	return topic
}

// ValidEmail: a plain address (no display name), not longer than 254.
func ValidEmail(s string) bool {
	if len(s) > 254 || strings.ContainsAny(s, "\r\n") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && strings.Contains(s[strings.LastIndexByte(s, '@')+1:], ".")
}

// Prefs are uid's settings (all off without a row; quiet hours on).
func (s *Service) Prefs(ctx context.Context, uid int64) (Prefs, error) {
	p, _, err := s.prefs(ctx, uid)
	return p, err
}

type prefsRow struct {
	verified     string
	lastSent     int64
	verifySentAt int64
	failCount    int
	nextTryAt    int64
	// the Telegram digests' own clock, and the linked chat
	tgLastSent int64
	tgFail     int
	tgNextTry  int64
	tgChat     int64
	// onAt: when each topic was last turned on (Unix seconds)
	onAt map[string]int64
}

func (s *Service) prefs(ctx context.Context, uid int64) (Prefs, prefsRow, error) {
	var p Prefs
	r := prefsRow{onAt: map[string]int64{}}
	var ep, av, rq, dl, qh, emailOn, tgOn int
	var epAt, avAt, rqAt int64
	var bounced string
	err := s.DB.QueryRowContext(ctx, `SELECT email, verified, episodes, available, requests, downloads, quiet_hours, lang, last_sent_at, verify_sent_at,
		episodes_at, available_at, requests_at, bounced, fail_count, next_try_at,
		email_on, telegram_on, telegram_chat, telegram_name, tg_last_sent_at, tg_fail_count, tg_next_try_at
		FROM notify_prefs WHERE user_id=?`, uid).Scan(&p.Email, &r.verified, &ep, &av, &rq, &dl, &qh, &p.Lang, &r.lastSent, &r.verifySentAt,
		&epAt, &avAt, &rqAt, &bounced, &r.failCount, &r.nextTryAt,
		&emailOn, &tgOn, &r.tgChat, &p.TelegramName, &r.tgLastSent, &r.tgFail, &r.tgNextTry)
	if errors.Is(err, sql.ErrNoRows) {
		p = Prefs{QuietHours: true, EmailOn: true, TelegramOn: true}
		return p, r, s.loadAddonRows(ctx, uid, &p)
	}
	if err != nil {
		return Prefs{}, r, err
	}
	p.Episodes, p.Available, p.Requests, p.QuietHours = ep != 0, av != 0, rq != 0, qh != 0
	p.EmailOn, p.TelegramOn, p.TelegramLinked = emailOn != 0, tgOn != 0, r.tgChat != 0
	// what an addon topic without a row falls back to (D1: the column of
	// the same topic name; legacyDownloads keeps the admins' old choice)
	p.addon.legacy = map[string]bool{Episodes: ep != 0, Available: av != 0, Requests: rq != 0, legacyDownloads: dl != 0}
	if err := s.loadAddonRows(ctx, uid, &p); err != nil {
		return Prefs{}, r, err
	}
	p.Verified = p.Email != "" && strings.EqualFold(r.verified, p.Email)
	p.Bounced = !p.Verified && p.Email != "" && strings.EqualFold(bounced, p.Email)
	r.onAt[Episodes], r.onAt[Available], r.onAt[Requests] = epAt, avAt, rqAt
	return p, r, nil
}

// OnSince is when uid turned topic on (zero: it is off).
func (s *Service) OnSince(ctx context.Context, uid int64, topic string) (time.Time, error) {
	p, r, err := s.prefs(ctx, uid)
	if err != nil || !p.On(topic) {
		return time.Time{}, err
	}
	return time.Unix(r.onAt[topic], 0), nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SetPrefs saves in for u: topics u may not turn on stay off, a changed
// address is unconfirmed again, a topic turned off loses what waited for it.
// Of the addon topics only those in.AddonTopics lists change.
func (s *Service) SetPrefs(ctx context.Context, u auth.User, in Prefs) (Prefs, error) {
	in.Email = strings.TrimSpace(in.Email)
	if in.Email != "" && !ValidEmail(in.Email) {
		return Prefs{}, ErrBadEmail
	}
	if in.Lang != i18n.RU && in.Lang != i18n.EN {
		in.Lang = ""
	}
	allowed := s.TopicsFor(ctx, u)
	for _, t := range AllTopics {
		if !slices.Contains(allowed, t) {
			in.set(t, false)
		}
	}
	old, row, err := s.prefs(ctx, u.ID)
	if err != nil {
		return Prefs{}, err
	}
	// when each topic went on: what happened before is no news
	at := map[string]int64{}
	for _, t := range AllTopics {
		at[t] = row.onAt[t]
		if in.On(t) && !old.On(t) {
			at[t] = s.now().Unix()
		}
	}
	// the legacyDownloads column is left as it is (D1 reads it)
	_, err = s.DB.ExecContext(ctx, `INSERT INTO notify_prefs(user_id, email, episodes, available, requests, quiet_hours, lang,
		  episodes_at, available_at, requests_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET email=excluded.email, episodes=excluded.episodes, available=excluded.available,
		  requests=excluded.requests, quiet_hours=excluded.quiet_hours, lang=excluded.lang,
		  episodes_at=excluded.episodes_at, available_at=excluded.available_at, requests_at=excluded.requests_at,
		  updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')`,
		u.ID, in.Email, b2i(in.Episodes), b2i(in.Available), b2i(in.Requests), b2i(in.QuietHours), in.Lang,
		at[Episodes], at[Available], at[Requests])
	if err != nil {
		return Prefs{}, err
	}
	for _, a := range in.AddonTopics {
		if k := AddonTopicKey(a.Addon, a.Topic); slices.Contains(allowed, k) {
			if err := s.setAddonPref(ctx, u.ID, k, a.On); err != nil {
				return Prefs{}, err
			}
		}
	}
	p, err := s.Prefs(ctx, u.ID)
	if err != nil {
		return Prefs{}, err
	}
	if err := s.dropOff(ctx, u.ID, p); err != nil {
		return Prefs{}, err
	}
	return p, nil
}

// SetChannels turns the profile's mail and Telegram on or off (nil: as it
// is). A channel turned off loses what waited in it.
func (s *Service) SetChannels(ctx context.Context, uid int64, email, telegram *bool) (Prefs, error) {
	if email != nil || telegram != nil {
		// a profile that never saved anything has no row yet: the defaults
		// (everything off, quiet hours on) are what a row starts from
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO notify_prefs(user_id) VALUES(?) ON CONFLICT(user_id) DO NOTHING`, uid); err != nil {
			return Prefs{}, err
		}
	}
	if email != nil {
		if _, err := s.DB.ExecContext(ctx, `UPDATE notify_prefs SET email_on=? WHERE user_id=?`, b2i(*email), uid); err != nil {
			return Prefs{}, err
		}
	}
	if telegram != nil {
		if _, err := s.DB.ExecContext(ctx, `UPDATE notify_prefs SET telegram_on=? WHERE user_id=?`, b2i(*telegram), uid); err != nil {
			return Prefs{}, err
		}
	}
	p, err := s.Prefs(ctx, uid)
	if err != nil {
		return Prefs{}, err
	}
	return p, s.dropOff(ctx, uid, p)
}

// dropOff forgets the unsent events of the topics p has off (or of every
// topic, when no channel works). A channel p does not use now (no address,
// switched off, no linked chat) is done for what waited in it: that was never
// sent and, when the channel comes back, it is no news.
func (s *Service) dropOff(ctx context.Context, uid int64, p Prefs) error {
	var off []any
	for _, t := range AllTopics {
		if !p.hears() || !p.On(t) {
			off = append(off, t)
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT topic FROM notify_events WHERE user_id=? AND (sent_at IS NULL OR tg_sent_at IS NULL) AND substr(topic, 1, ?)=?`,
		uid, len(addonPrefix), addonPrefix)
	if err != nil {
		return err
	}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		if !p.hears() || !p.On(t) {
			off = append(off, t)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(off) > 0 {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM notify_events WHERE user_id=? AND (sent_at IS NULL OR tg_sent_at IS NULL) AND topic IN (?`+
			strings.Repeat(",?", len(off)-1)+`)`, append([]any{uid}, off...)...); err != nil {
			return err
		}
	}
	now := s.now().Unix()
	if !p.emailActive() {
		if _, err := s.DB.ExecContext(ctx, `UPDATE notify_events SET sent_at=? WHERE user_id=? AND sent_at IS NULL`, now, uid); err != nil {
			return err
		}
	}
	if !p.telegramActive() {
		if _, err := s.DB.ExecContext(ctx, `UPDATE notify_events SET tg_sent_at=? WHERE user_id=? AND tg_sent_at IS NULL`, now, uid); err != nil {
			return err
		}
	}
	return nil
}

// wanting are the profiles with a channel that works and topic on.
func (s *Service) wanting(ctx context.Context, topic string) (map[int64]Prefs, error) {
	col := map[string]string{Episodes: "episodes", Available: "available", Requests: "requests"}[topic]
	where := col + `=1`
	if _, _, ok := ParseAddonTopic(topic); ok {
		where = `1` // a row, or the fixed column: Prefs.On below decides
	} else if col == "" {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id FROM notify_prefs WHERE ((email<>'' AND email_on=1) OR (telegram_chat<>0 AND telegram_on=1)) AND `+where)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[int64]Prefs, len(ids))
	for _, id := range ids {
		p, err := s.Prefs(ctx, id)
		if err != nil {
			return nil, err
		}
		if p.On(topic) {
			out[id] = p
		}
	}
	return out, nil
}

// Wants reports whether any profile has topic on (event points skip their
// work otherwise).
func (s *Service) Wants(ctx context.Context, topic string) bool {
	if s == nil {
		return false
	}
	w, err := s.wanting(ctx, topic)
	return err == nil && len(w) > 0
}
