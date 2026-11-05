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
	// Downloads is another build's topic (admins).
	Downloads = "downloads"
)

// AllTopics in the order letters list them.
var AllTopics = []string{Episodes, Available, Requests, Downloads}

// TopicsFor are the topics u may turn on: the shared ones, this edition's
// (editionTopics) and, for a kid, Requests wherever kids' requests are open
// (RequestsOpen).
func (s *Service) TopicsFor(ctx context.Context, u auth.User) []string {
	out := append([]string{Episodes, Available}, editionTopics(u)...)
	if u.IsKid && s != nil && s.RequestsOpen != nil && !slices.Contains(out, Requests) && s.RequestsOpen(ctx) {
		out = append(out, Requests)
	}
	return out
}

// Prefs are a profile's notification settings.
type Prefs struct {
	Email string `json:"email"`
	// Verified: the address was confirmed by its verification letter.
	Verified   bool   `json:"verified"`
	Episodes   bool   `json:"episodes"`
	Available  bool   `json:"available"`
	Requests   bool   `json:"requests"`
	Downloads  bool   `json:"downloads"`
	QuietHours bool   `json:"quietHours"`
	Lang       string `json:"lang"` // ru | en; "" the server's
	// Bounced: the mail server refused the address for good («Адрес не
	// принимает письма»); it is unconfirmed until confirmed again.
	Bounced bool `json:"bounced"`
}

// TopicAll names every topic in an unsubscribe token (List-Unsubscribe).
const TopicAll = "all"

// On reports whether topic is turned on.
func (p Prefs) On(topic string) bool {
	switch topic {
	case Episodes:
		return p.Episodes
	case Available:
		return p.Available
	case Requests:
		return p.Requests
	case Downloads:
		return p.Downloads
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
	case Downloads:
		p.Downloads = on
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
	Log          *slog.Logger

	collectMu sync.Mutex
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
	// onAt: when each topic was last turned on (Unix seconds)
	onAt map[string]int64
}

func (s *Service) prefs(ctx context.Context, uid int64) (Prefs, prefsRow, error) {
	var p Prefs
	r := prefsRow{onAt: map[string]int64{}}
	var ep, av, rq, dl, qh int
	var epAt, avAt, rqAt, dlAt int64
	var bounced string
	err := s.DB.QueryRowContext(ctx, `SELECT email, verified, episodes, available, requests, downloads, quiet_hours, lang, last_sent_at, verify_sent_at,
		episodes_at, available_at, requests_at, downloads_at, bounced, fail_count, next_try_at
		FROM notify_prefs WHERE user_id=?`, uid).Scan(&p.Email, &r.verified, &ep, &av, &rq, &dl, &qh, &p.Lang, &r.lastSent, &r.verifySentAt,
		&epAt, &avAt, &rqAt, &dlAt, &bounced, &r.failCount, &r.nextTryAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Prefs{QuietHours: true}, r, nil
	}
	if err != nil {
		return Prefs{}, r, err
	}
	p.Episodes, p.Available, p.Requests, p.Downloads, p.QuietHours = ep != 0, av != 0, rq != 0, dl != 0, qh != 0
	p.Verified = p.Email != "" && strings.EqualFold(r.verified, p.Email)
	p.Bounced = !p.Verified && p.Email != "" && strings.EqualFold(bounced, p.Email)
	r.onAt[Episodes], r.onAt[Available], r.onAt[Requests], r.onAt[Downloads] = epAt, avAt, rqAt, dlAt
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
	_, err = s.DB.ExecContext(ctx, `INSERT INTO notify_prefs(user_id, email, episodes, available, requests, downloads, quiet_hours, lang,
		  episodes_at, available_at, requests_at, downloads_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET email=excluded.email, episodes=excluded.episodes, available=excluded.available,
		  requests=excluded.requests, downloads=excluded.downloads, quiet_hours=excluded.quiet_hours, lang=excluded.lang,
		  episodes_at=excluded.episodes_at, available_at=excluded.available_at, requests_at=excluded.requests_at, downloads_at=excluded.downloads_at,
		  updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')`,
		u.ID, in.Email, b2i(in.Episodes), b2i(in.Available), b2i(in.Requests), b2i(in.Downloads), b2i(in.QuietHours), in.Lang,
		at[Episodes], at[Available], at[Requests], at[Downloads])
	if err != nil {
		return Prefs{}, err
	}
	if err := s.dropOff(ctx, u.ID, in); err != nil {
		return Prefs{}, err
	}
	return s.Prefs(ctx, u.ID)
}

// dropOff forgets the unsent events of the topics p has off (or of every
// topic, without an address).
func (s *Service) dropOff(ctx context.Context, uid int64, p Prefs) error {
	var off []any
	for _, t := range AllTopics {
		if p.Email == "" || !p.On(t) {
			off = append(off, t)
		}
	}
	if len(off) == 0 {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM notify_events WHERE user_id=? AND sent_at IS NULL AND topic IN (?`+
		strings.Repeat(",?", len(off)-1)+`)`, append([]any{uid}, off...)...)
	return err
}

// wanting are the profiles with an address and topic on.
func (s *Service) wanting(ctx context.Context, topic string) (map[int64]Prefs, error) {
	col := map[string]string{Episodes: "episodes", Available: "available", Requests: "requests", Downloads: "downloads"}[topic]
	if col == "" {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id FROM notify_prefs WHERE email<>'' AND `+col+`=1`)
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
		out[id] = p
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
