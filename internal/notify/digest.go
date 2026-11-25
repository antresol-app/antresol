package notify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/mailer"
)

// pruneEvery: how often Run deletes what is past keepSent, keepPending and
// keepSeen. The deletes lock the database for writing even when no row
// matches, so they stay out of the minute loop.
const pruneEvery = time.Hour

// Run sends the digests that are due, every minute, until ctx ends, and
// prunes the old events when it starts and every pruneEvery after.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	var prunedAt time.Time
	for {
		if now := s.now(); now.Sub(prunedAt) >= pruneEvery {
			prunedAt = now
			if err := s.prune(ctx, now); err != nil && ctx.Err() == nil {
				s.log().Warn("notify: prune", "err", err)
			}
		}
		if err := s.Flush(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("notify: digests", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// prune deletes the events kept long enough: sent ones after keepSent,
// unsent ones after keepPending (they never could go), and what a profile
// was told about after keepSeen.
func (s *Service) prune(ctx context.Context, now time.Time) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM notify_events WHERE (sent_at IS NOT NULL AND tg_sent_at IS NOT NULL AND max(sent_at, tg_sent_at) < ?)
		OR ((sent_at IS NULL OR tg_sent_at IS NULL) AND created_at < ?)`,
		now.Add(-keepSent).Unix(), now.Add(-keepPending).Unix()); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM notify_seen WHERE at < ?`, now.Add(-keepSeen).Unix())
	return err
}

// quiet reports whether t is in the quiet hours.
func (s *Service) quiet(t time.Time) bool {
	h := t.In(s.loc()).Hour()
	return h >= QuietFrom || h < QuietTo
}

// Flush sends every profile's digest that is due, by mail and by Telegram:
// the channel's address confirmed (or the chat linked), not in the
// profile's quiet hours, an hour since the last one, and its newest event
// Settle old (or its oldest an hour). Sent events are kept a while (keys;
// Run prunes them). A failing channel never holds back the other.
func (s *Service) Flush(ctx context.Context) error {
	now := s.now()
	mailErr := s.flushMail(ctx, now)
	return errors.Join(mailErr, s.flushTelegram(ctx, now))
}

// pendingUsers are the profiles with something waiting in ch.
func (s *Service) pendingUsers(ctx context.Context, ch channel) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT user_id FROM notify_events WHERE `+ch.sent+` IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		uids = append(uids, id)
	}
	return uids, rows.Err()
}

func (s *Service) flushMail(ctx context.Context, now time.Time) error {
	if s.Mail == nil {
		return nil
	}
	uids, err := s.pendingUsers(ctx, mailChannel)
	if err != nil || len(uids) == 0 {
		return err
	}
	cfg, ok := s.Mail(ctx)
	if !ok {
		return nil
	}
	for _, uid := range uids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.flushOne(ctx, cfg, uid, now); err != nil {
			s.log().Warn("notify: digest", "user", uid, "err", err)
		}
	}
	return nil
}

// channel names the columns of a channel's delivery marks in notify_events:
// sent (when it went), sending (a send under way).
type channel struct{ sent, sending string }

var (
	mailChannel     = channel{"sent_at", "sending_at"}
	telegramChannel = channel{"tg_sent_at", "tg_sending_at"}
)

// backoff is the wait after the n-th failure in a row: 15 min, doubling,
// at most a day.
func backoff(n int) time.Duration {
	d := retryAfter
	for i := 1; i < n && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}

// failed records a letter that did not go: a permanent refusal of the
// address unconfirms it («Адрес не принимает письма») and stops retries;
// anything else waits longer each time (kept across restarts).
func (s *Service) failed(ctx context.Context, uid int64, email string, fails int, now time.Time, err error) error {
	if errors.Is(err, mailer.ErrRecipient) {
		_, derr := s.DB.ExecContext(ctx, `UPDATE notify_prefs SET verified='', bounced=?, fail_count=0, next_try_at=0 WHERE user_id=?`, email, uid)
		return errors.Join(err, derr)
	}
	_, derr := s.DB.ExecContext(ctx, `UPDATE notify_prefs SET fail_count=?, next_try_at=? WHERE user_id=?`,
		fails+1, now.Add(backoff(fails+1)).Unix(), uid)
	return errors.Join(err, derr)
}

func (s *Service) flushOne(ctx context.Context, cfg mailer.Config, uid int64, now time.Time) error {
	p, row, err := s.prefs(ctx, uid)
	if err != nil {
		return err
	}
	if !p.EmailOn || !p.Verified || now.Unix() < row.nextTryAt || (p.QuietHours && s.quiet(now)) || now.Sub(time.Unix(row.lastSent, 0)) < s.every() {
		return nil
	}
	b, err := s.takeBatch(ctx, mailChannel, uid, p, now)
	if err != nil || b == nil {
		return err
	}
	lang := p.Language()
	link := s.linker()
	sections := sectionsOf(b, lang, link, func(topic string) string {
		return link("/api/notify/link/unsubscribe?t=" + s.UnsubscribeToken(uid, topic, p.Email))
	})
	l, err := compose(lang, b.name, sections)
	if err != nil {
		return err
	}
	m := mailer.Message{To: p.Email, Subject: l.Subject, Body: l.Text, HTML: l.HTML}
	// the mail app's one-click stops everything; the body's links one topic each
	if u := link("/api/notify/link/unsubscribe?t=" + s.UnsubscribeToken(uid, TopicAll, p.Email)); u != "" {
		m.Headers = map[string]string{"List-Unsubscribe": "<" + u + ">", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"}
	}
	// the marker first: a crash during the send never sends these twice
	if err := s.markSending(ctx, mailChannel, b, now); err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := s.send(sctx, cfg, m); err != nil {
		if derr := s.unmarkSending(ctx, mailChannel, b); derr != nil {
			err = errors.Join(err, derr)
		}
		return s.failed(ctx, uid, p.Email, row.failCount, now, err)
	}
	// sent: the events are done and the hour starts now
	if err := s.markSent(ctx, mailChannel, b, now); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE notify_prefs SET last_sent_at=?, fail_count=0, next_try_at=0 WHERE user_id=?`, now.Unix(), uid)
	return err
}

// batch is what one digest of one channel says: a profile's waiting events,
// by topic.
type batch struct {
	byTopic        map[string][]stored
	ids            []any
	oldest, newest int64
	decl           []AddonTopic // the addons' topics now, in the order letters list them
	name           string       // the profile's name
}

func (s *Service) markSending(ctx context.Context, ch channel, b *batch, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE notify_events SET `+ch.sending+`=? WHERE id IN `+db.In(len(b.ids)), append([]any{now.Unix()}, b.ids...)...)
	return err
}

func (s *Service) unmarkSending(ctx context.Context, ch channel, b *batch) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE notify_events SET `+ch.sending+`=NULL WHERE id IN `+db.In(len(b.ids)), b.ids...)
	return err
}

func (s *Service) markSent(ctx context.Context, ch channel, b *batch, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE notify_events SET `+ch.sent+`=? WHERE id IN `+db.In(len(b.ids)), append([]any{now.Unix()}, b.ids...)...)
	return err
}

// takeBatch gathers what uid's next digest of ch says (nil: nothing to send
// now). A send that never finished (a crash) counts, after sendingStale, as
// sent — it may well have gone — and is never sent again.
func (s *Service) takeBatch(ctx context.Context, ch channel, uid int64, p Prefs, now time.Time) (*batch, error) {
	if _, err := s.DB.ExecContext(ctx, `UPDATE notify_events SET `+ch.sent+`=`+ch.sending+` WHERE user_id=? AND `+ch.sent+` IS NULL AND `+ch.sending+` < ?`,
		uid, now.Add(-sendingStale).Unix()); err != nil {
		return nil, err
	}
	var inFlight int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notify_events WHERE user_id=? AND `+ch.sent+` IS NULL AND `+ch.sending+` IS NOT NULL`, uid).Scan(&inFlight); err != nil {
		return nil, err
	}
	if inFlight > 0 {
		return nil, nil // another send of this profile's is under way
	}
	// the addons' topics now: the events of an addon that is off or gone
	// wait (removing it drops them), left out before the batch's LIMIT so
	// that however many there are they never hold back the others
	decl := s.declared(ctx)
	declared := map[string]bool{}
	args := []any{uid, len(addonPrefix), addonPrefix}
	for _, t := range decl {
		k := AddonTopicKey(t.Addon, t.Topic)
		declared[k] = true
		args = append(args, k)
	}
	addonOK := ""
	if len(decl) > 0 {
		addonOK = ` OR topic IN ` + db.In(len(decl))
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, topic, item_id, group_id, data_json, created_at FROM notify_events
		WHERE user_id=? AND `+ch.sent+` IS NULL AND (substr(topic, 1, ?)<>?`+addonOK+`) ORDER BY created_at, id LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	// the profile as it is now: an addon line queued before the owner made
	// it a kid (or took its admin away) is not sent past its topic's audience
	b := &batch{byTopic: map[string][]stored{}, decl: decl}
	var who *auth.User
	if s.Users != nil {
		if u, err := s.Users.Get(ctx, uid); err == nil {
			b.name, who = u.Name, &u
		}
	}
	audience := map[string]string{}
	for _, t := range decl {
		audience[AddonTopicKey(t.Addon, t.Topic)] = t.Audience
	}
	for rows.Next() {
		var e stored
		var raw string
		if err := rows.Scan(&e.id, &e.topic, &e.itemID, &e.groupID, &raw, &e.created); err != nil {
			rows.Close()
			return nil, err
		}
		if !p.On(e.topic) || rendererOf(e.topic) == nil || (isAddonTopic(e.topic) && !declared[e.topic]) {
			continue // turned off meanwhile (dropOff), not this edition's, or its addon is off
		}
		if isAddonTopic(e.topic) && s.Users != nil && (who == nil || !admits(audience[e.topic], *who)) {
			continue // the profile is no longer in the topic's audience
		}
		e.decode(raw)
		b.byTopic[e.topic] = append(b.byTopic[e.topic], e)
		b.ids = append(b.ids, e.id)
		if b.oldest == 0 || e.created < b.oldest {
			b.oldest = e.created
		}
		b.newest = max(b.newest, e.created)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(b.ids) == 0 {
		return nil, err
	}
	if now.Sub(time.Unix(b.newest, 0)) < s.settle() && now.Sub(time.Unix(b.oldest, 0)) < s.every() {
		return nil, nil // more may come: a season arrives an episode at a time
	}
	return b, nil
}

// sections lists the batch by topic: the core's, then the addons' by addon
// id and manifest order. unsub makes the link that turns a topic off ("":
// the channel has none).
func sectionsOf(b *batch, lang string, link func(string) string, unsub func(topic string) string) []section {
	var sections []section
	add := func(t, heading string) {
		evs := b.byTopic[t]
		if len(evs) == 0 {
			return
		}
		sec := section{Topic: t, Heading: heading, Entries: rendererOf(t)(lang, evs, link)}
		if u := unsub(t); u != "" {
			sec.Unsubscribe = u
			sec.OffLabel = i18n.TL(lang, "Больше не присылать «%s»", sec.Heading)
		}
		sections = append(sections, sec)
	}
	for _, t := range AllTopics {
		if len(b.byTopic[t]) > 0 {
			add(t, headings[t](lang))
		}
	}
	for _, t := range b.decl {
		k := AddonTopicKey(t.Addon, t.Topic)
		h := pickText(t.Title, lang)
		if h == "" {
			h = t.Topic
		}
		add(k, h)
	}
	return sections
}

func codeHash(code, email string) string {
	h := sha256.Sum256([]byte(code + "|" + addrHash(email)))
	return hex.EncodeToString(h[:])
}

// codeTTL: how long the code of a verification letter works; codeTries:
// wrong codes before it stops working.
const (
	codeTTL   = 24 * time.Hour
	codeTries = 5
)

// SendVerification sends uid's address a letter with a code (typed into
// the settings) and, with a public address, a link that confirms it.
func (s *Service) SendVerification(ctx context.Context, uid int64) error {
	p, row, err := s.prefs(ctx, uid)
	if err != nil {
		return err
	}
	if p.Email == "" {
		return ErrNoEmail
	}
	if s.Mail == nil {
		return ErrNoMail
	}
	cfg, ok := s.Mail(ctx)
	if !ok {
		return ErrNoMail
	}
	now := s.now()
	if d := now.Sub(time.Unix(row.verifySentAt, 0)); d >= 0 && d < verifyGap {
		return ErrTooSoon
	}
	// a day's cap per profile and per address (any profile's): the button
	// must not make the server's mailbox a way to flood someone
	addr := addrHash(p.Email)
	day := now.Add(-24 * time.Hour).Unix()
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM notify_verify_log WHERE at < ?`, day); err != nil {
		return err
	}
	var mine, theirs int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE user_id=?), COUNT(*) FILTER (WHERE addr=?) FROM notify_verify_log WHERE at >= ?`,
		uid, addr, day).Scan(&mine, &theirs); err != nil {
		return err
	}
	if mine >= verifyPerProfile || theirs >= verifyPerAddress {
		return ErrTooMany
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	// the gap and the caps hold even when the letter fails (no hammering)
	if _, err := s.DB.ExecContext(ctx, `UPDATE notify_prefs SET verify_sent_at=?, verify_code=?, verify_tries=0 WHERE user_id=?`,
		now.Unix(), codeHash(code, p.Email), uid); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO notify_verify_log(user_id, addr, at) VALUES(?,?,?)`, uid, addr, now.Unix()); err != nil {
		return err
	}
	lang := p.Language()
	link := s.linker()("/api/notify/link/verify?t=" + s.verifyToken(uid, p.Email))
	subject := i18n.TL(lang, "%s: проверка адреса для уведомлений", product)
	intro := i18n.TL(lang, "Это проверочное письмо %s. Чтобы получать уведомления на этот адрес, введите код в настройках уведомлений:", product)
	text := intro + "\n\n    " + code + "\n"
	html := `<!doctype html><html lang="` + lang + `"><head><meta charset="utf-8"></head><body style="font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#1d1d1f">` +
		`<p>` + htmlEscape(intro) + `</p><p style="font-size:28px;letter-spacing:4px;font-weight:600">` + code + `</p>`
	if link != "" {
		or := i18n.TL(lang, "Или подтвердите адрес по ссылке:")
		text += "\n" + or + "\n" + link + "\n"
		html += `<p>` + htmlEscape(or) + ` <a href="` + htmlEscape(link) + `">` + htmlEscape(i18n.TL(lang, "Подтвердить адрес")) + `</a></p>`
	}
	ignore := i18n.TL(lang, "Если вы не включали уведомления, просто не обращайте внимания на это письмо.")
	text += "\n" + ignore + "\n"
	html += `<p style="color:#6e6e73;font-size:13px">` + htmlEscape(ignore) + `</p></body></html>`
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	err = s.send(sctx, cfg, mailer.Message{To: p.Email, Subject: subject, Body: text, HTML: html})
	if errors.Is(err, mailer.ErrRecipient) {
		if _, derr := s.DB.ExecContext(ctx, `UPDATE notify_prefs SET verified='', bounced=? WHERE user_id=?`, p.Email, uid); derr != nil {
			err = errors.Join(err, derr)
		}
	}
	return err
}

// ErrBadCode: the code is wrong, expired or used up.
var ErrBadCode = fmt.Errorf("notify: wrong code")

// ConfirmCode confirms uid's address with the code of its letter.
func (s *Service) ConfirmCode(ctx context.Context, uid int64, code string) error {
	p, row, err := s.prefs(ctx, uid)
	if err != nil {
		return err
	}
	if p.Email == "" || s.now().Sub(time.Unix(row.verifySentAt, 0)) > codeTTL {
		return ErrBadCode
	}
	// a try is taken before the code is compared, in one statement: two
	// guesses at once cannot both slip under the limit
	var stored string
	err = s.DB.QueryRowContext(ctx, `UPDATE notify_prefs SET verify_tries=verify_tries+1
		WHERE user_id=? AND verify_code<>'' AND verify_tries<? RETURNING verify_code`, uid, codeTries).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBadCode
	}
	if err != nil {
		return err
	}
	if codeHash(strings.TrimSpace(code), p.Email) != stored {
		return ErrBadCode
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE notify_prefs SET verified=email, bounced='', verify_code='', verify_tries=0 WHERE user_id=? AND verify_code=?`, uid, stored)
	return err
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(s)
}
