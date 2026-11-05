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

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/mailer"
)

// Run sends the digests that are due, every minute, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
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

// quiet reports whether t is in the quiet hours.
func (s *Service) quiet(t time.Time) bool {
	h := t.In(s.loc()).Hour()
	return h >= QuietFrom || h < QuietTo
}

// Flush sends every profile's digest that is due: the address confirmed,
// not in its quiet hours, an hour since the last one, and its newest event
// Settle old (or its oldest an hour). Sent events are kept a while (keys),
// then pruned with events that never could go.
func (s *Service) Flush(ctx context.Context) error {
	now := s.now()
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM notify_events WHERE (sent_at IS NOT NULL AND sent_at < ?) OR (sent_at IS NULL AND created_at < ?)`,
		now.Add(-keepSent).Unix(), now.Add(-keepPending).Unix()); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM notify_seen WHERE at < ?`, now.Add(-keepSeen).Unix()); err != nil {
		return err
	}
	if s.Mail == nil {
		return nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT user_id FROM notify_events WHERE sent_at IS NULL`)
	if err != nil {
		return err
	}
	var uids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		uids = append(uids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(uids) == 0 {
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
	if !p.Verified || now.Unix() < row.nextTryAt || (p.QuietHours && s.quiet(now)) || now.Sub(time.Unix(row.lastSent, 0)) < s.every() {
		return nil
	}
	// a send that never finished (a crash): after sendingStale it counts as
	// sent — it may well have gone — and is never sent again
	if _, err := s.DB.ExecContext(ctx, `UPDATE notify_events SET sent_at=sending_at WHERE user_id=? AND sent_at IS NULL AND sending_at < ?`,
		uid, now.Add(-sendingStale).Unix()); err != nil {
		return err
	}
	var inFlight int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notify_events WHERE user_id=? AND sent_at IS NULL AND sending_at IS NOT NULL`, uid).Scan(&inFlight); err != nil {
		return err
	}
	if inFlight > 0 {
		return nil // another send of this profile's is under way
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, topic, item_id, group_id, data_json, created_at FROM notify_events
		WHERE user_id=? AND sent_at IS NULL ORDER BY created_at, id LIMIT 500`, uid)
	if err != nil {
		return err
	}
	byTopic := map[string][]stored{}
	var ids []any
	var oldest, newest int64
	for rows.Next() {
		var e stored
		var raw string
		if err := rows.Scan(&e.id, &e.topic, &e.itemID, &e.groupID, &raw, &e.created); err != nil {
			rows.Close()
			return err
		}
		if !p.On(e.topic) || renderers[e.topic] == nil {
			continue // turned off meanwhile (dropOff), or not this edition's
		}
		e.decode(raw)
		byTopic[e.topic] = append(byTopic[e.topic], e)
		ids = append(ids, e.id)
		if oldest == 0 || e.created < oldest {
			oldest = e.created
		}
		newest = max(newest, e.created)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return err
	}
	if now.Sub(time.Unix(newest, 0)) < s.settle() && now.Sub(time.Unix(oldest, 0)) < s.every() {
		return nil // more may come: a season arrives an episode at a time
	}
	name := ""
	if s.Users != nil {
		if u, err := s.Users.Get(ctx, uid); err == nil {
			name = u.Name
		}
	}
	lang := p.Language()
	link := s.linker()
	var sections []section
	for _, t := range AllTopics {
		evs := byTopic[t]
		if len(evs) == 0 {
			continue
		}
		sec := section{Topic: t, Heading: headings[t](lang), Entries: renderers[t](lang, evs, link)}
		if u := link("/api/notify/link/unsubscribe?t=" + s.UnsubscribeToken(uid, t, p.Email)); u != "" {
			sec.Unsubscribe = u
			sec.OffLabel = i18n.TL(lang, "Больше не присылать «%s»", sec.Heading)
		}
		sections = append(sections, sec)
	}
	l, err := compose(lang, name, sections)
	if err != nil {
		return err
	}
	m := mailer.Message{To: p.Email, Subject: l.Subject, Body: l.Text, HTML: l.HTML}
	// the mail app's one-click stops everything; the body's links one topic each
	if u := link("/api/notify/link/unsubscribe?t=" + s.UnsubscribeToken(uid, TopicAll, p.Email)); u != "" {
		m.Headers = map[string]string{"List-Unsubscribe": "<" + u + ">", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"}
	}
	in := `(?` + strings.Repeat(",?", len(ids)-1) + `)`
	// the marker first: a crash during the send never sends these twice
	if _, err := s.DB.ExecContext(ctx, `UPDATE notify_events SET sending_at=? WHERE id IN `+in, append([]any{now.Unix()}, ids...)...); err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := s.send(sctx, cfg, m); err != nil {
		if _, derr := s.DB.ExecContext(ctx, `UPDATE notify_events SET sending_at=NULL WHERE id IN `+in, ids...); derr != nil {
			err = errors.Join(err, derr)
		}
		return s.failed(ctx, uid, p.Email, row.failCount, now, err)
	}
	// sent: the events are done and the hour starts now
	if _, err := s.DB.ExecContext(ctx, `UPDATE notify_events SET sent_at=? WHERE id IN `+in, append([]any{now.Unix()}, ids...)...); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE notify_prefs SET last_sent_at=?, fail_count=0, next_try_at=0 WHERE user_id=?`, now.Unix(), uid)
	return err
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
