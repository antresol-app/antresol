package notify

// Telegram, the second channel of «Уведомления».
//
// The administrator gives the server a bot (a token from @BotFather); the
// token is kept in the settings table like the SMTP password and never
// leaves the server. A profile links its own chat with the bot: the settings
// make a one-time code (the link t.me/<bot>?start=<code>), the person opens
// it and presses «Start», and the server — which asks Telegram for new
// messages (long polling, so a home server behind NAT needs no public
// address) — matches the code, stores the chat for the profile and answers
// in the profile's language. One poller runs per server and only while a
// token is set. Kid profiles do not link: no code is made for them and a
// code made before the profile became a kid is refused.
//
// Digests go out on the mail's schedule (digest.go): the same events, the
// same quiet hours, a message of plain text with a link to the title when
// the server has a public address. Telegram's limits are respected: sends
// are spaced (TelegramGap), a 429 blocks every send until its retry_after has
// passed, and a chat the bot cannot write to (blocked, deleted) is unlinked.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/telegram"
)

// Errors of the Telegram settings and linking.
var (
	// ErrNoTelegram: the server has no bot (the administrator sets it up).
	ErrNoTelegram = errors.New("notify: the server has no Telegram bot")
	// ErrKidTelegram: kid profiles do not link Telegram.
	ErrKidTelegram = errors.New("notify: a kid profile does not link Telegram")
	// ErrBadTelegramToken: not the shape of a bot token.
	ErrBadTelegramToken = errors.New("notify: not a bot token")
	// ErrTelegramRejected: Telegram does not know the token.
	ErrTelegramRejected = errors.New("notify: Telegram rejected the token")
	// ErrTelegramUnreachable: no (usable) answer from Telegram.
	ErrTelegramUnreachable = errors.New("notify: no answer from Telegram")
)

const (
	settingsTelegram = "telegram"        // the settings row of the bot
	stateTelegramOff = "telegram_offset" // notify_state: the next update to ask for
	// linkTTL: how long a «Подключить Telegram» code works.
	linkTTL = 15 * time.Minute
	// pollSeconds: how long one getUpdates waits for news.
	pollSeconds = 25
	// pollMargin: how much longer than the wait itself one getUpdates may take
	// before the connection is given up as dead.
	pollMargin = 15 * time.Second
	// strangerEvery: one answer to a chat that is not a linked person (a bare
	// «/start», a code that does not work); strangerCap: the most such answers
	// in strangerWindow for the whole server.
	strangerEvery  = time.Hour
	strangerWindow = time.Hour
	strangerCap    = 20
	// DefaultTelegramGap is the least time between two sends to Telegram (its
	// limit is about one message a second to a chat).
	DefaultTelegramGap = time.Second
	// chunkRunes: a message longer than this goes in parts (Telegram takes
	// 4096 characters).
	chunkRunes = 3500
	// longest wait we honour after a 429, and the longest between polls after
	// failures.
	maxRetryAfter = 10 * time.Minute
	maxPollWait   = 5 * time.Minute
	// idleCheck: a poller without a token looks again this often (a change of
	// the token wakes it at once).
	idleCheck = 5 * time.Minute
)

// Problems of the poller, as TelegramProblem says them.
const (
	ProblemUnauthorized = "unauthorized" // Telegram no longer accepts the token
	ProblemConflict     = "conflict"     // another program asks for this bot's messages, or a webhook is set
	ProblemUnreachable  = "unreachable"  // no answer
)

// botRow is the bot in the settings table.
type botRow struct {
	Token    string `json:"token"`
	ID       int64  `json:"id"` // kept when the token is cleared: the same bot again keeps its links
	Username string `json:"username"`
	Name     string `json:"name"`
}

// Bot is the server's bot as the screens see it: never the token.
type Bot struct {
	Set      bool   `json:"set"`
	Username string `json:"username,omitempty"` // without "@"
	Name     string `json:"name,omitempty"`
}

func (s *Service) tg() *telegram.Client {
	if s.TG != nil {
		return s.TG
	}
	return &telegram.Client{HTTP: &http.Client{}}
}

func (s *Service) loadBot(ctx context.Context) (botRow, error) {
	raw, _, err := db.GetSetting(ctx, s.DB, settingsTelegram)
	if err != nil {
		return botRow{}, err
	}
	var b botRow
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return botRow{}, nil // none, or unreadable: as if none
	}
	return b, nil
}

func (s *Service) saveBot(ctx context.Context, b botRow) error {
	return db.PutJSON(ctx, s.DB, settingsTelegram, b)
}

// TelegramBot is the server's bot (Set false: none, Telegram is off).
func (s *Service) TelegramBot(ctx context.Context) Bot {
	if s == nil {
		return Bot{}
	}
	b, err := s.loadBot(ctx)
	if err != nil || b.Token == "" {
		return Bot{}
	}
	return Bot{Set: true, Username: b.Username, Name: b.Name}
}

// askMe is getMe with the errors a screen can say.
func (s *Service) askMe(ctx context.Context, token string) (telegram.Me, error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	me, err := s.tg().GetMe(cctx, token)
	switch {
	case err == nil:
		return me, nil
	case telegram.IsUnauthorized(err):
		return me, ErrTelegramRejected
	}
	s.log().Warn("notify: telegram getMe", "err", err)
	return me, ErrTelegramUnreachable
}

// SetTelegramToken checks token with getMe and, when it is a bot's, keeps it
// as the server's bot and wakes the poller. A different bot than before
// drops the chats linked with the old one (they cannot be written to).
func (s *Service) SetTelegramToken(ctx context.Context, token string) (Bot, error) {
	token = strings.TrimSpace(token)
	if !telegram.ValidToken(token) {
		return Bot{}, ErrBadTelegramToken
	}
	me, err := s.askMe(ctx, token)
	if err != nil {
		return Bot{}, err
	}
	old, err := s.loadBot(ctx)
	if err != nil {
		return Bot{}, err
	}
	if old.ID != 0 && old.ID != me.ID {
		// the poller stops asking the old bot before its chats are forgotten
		// (its offset is kept with the bot's id and never applies to another)
		s.TelegramKick()
		if err := s.forgetChats(ctx); err != nil {
			return Bot{}, err
		}
	}
	if err := s.saveBot(ctx, botRow{Token: token, ID: me.ID, Username: me.Username, Name: me.Name}); err != nil {
		return Bot{}, err
	}
	s.TelegramKick()
	return Bot{Set: true, Username: me.Username, Name: me.Name}, nil
}

// CheckTelegram asks Telegram about the stored token again («Проверить»).
func (s *Service) CheckTelegram(ctx context.Context) (Bot, error) {
	b, err := s.loadBot(ctx)
	if err != nil {
		return Bot{}, err
	}
	if b.Token == "" {
		return Bot{}, ErrNoTelegram
	}
	me, err := s.askMe(ctx, b.Token)
	if err != nil {
		return Bot{}, err
	}
	if me.Username != b.Username || me.Name != b.Name {
		b.Username, b.Name = me.Username, me.Name
		if err := s.saveBot(ctx, b); err != nil {
			return Bot{}, err
		}
	}
	return Bot{Set: true, Username: me.Username, Name: me.Name}, nil
}

// ClearTelegramToken forgets the token: the poller stops and nothing goes to
// Telegram. The chats stay linked (the same bot again picks them up); the
// bot's id is kept for that.
func (s *Service) ClearTelegramToken(ctx context.Context) error {
	b, err := s.loadBot(ctx)
	if err != nil {
		return err
	}
	if err := s.saveBot(ctx, botRow{ID: b.ID}); err != nil {
		return err
	}
	s.TelegramKick()
	return nil
}

// forgetChats unlinks every chat (another bot now) and starts the updates
// over.
func (s *Service) forgetChats(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id FROM notify_prefs WHERE telegram_chat<>0`)
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
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range uids {
		if err := s.UnlinkTelegram(ctx, id); err != nil {
			return err
		}
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM notify_telegram_codes`); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM notify_state WHERE key=?`, stateTelegramOff)
	return err
}

// TelegramLink is what «Подключить Telegram» shows: the link to open (and the
// code it carries, to type to the bot by hand), and until when it works.
type TelegramLink struct {
	Code    string    `json:"code"`
	URL     string    `json:"url"`
	Bot     string    `json:"bot"`
	Expires time.Time `json:"expires"`
}

func linkCodeHash(code string) string {
	h := sha256.Sum256([]byte("telegram-link|" + code))
	return hex.EncodeToString(h[:])
}

const codeAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func newLinkCode() (string, error) {
	var b strings.Builder
	for i := 0; i < 16; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(codeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// StartTelegramLink makes u a one-time code (the earlier one stops working);
// lang is the language of the screen, for the bot's answer. A kid gets none.
func (s *Service) StartTelegramLink(ctx context.Context, u auth.User, lang string) (TelegramLink, error) {
	if u.IsKid {
		return TelegramLink{}, ErrKidTelegram
	}
	b, err := s.loadBot(ctx)
	if err != nil {
		return TelegramLink{}, err
	}
	if b.Token == "" || b.Username == "" {
		return TelegramLink{}, ErrNoTelegram
	}
	if lang != i18n.RU && lang != i18n.EN {
		lang = ""
	}
	code, err := newLinkCode()
	if err != nil {
		return TelegramLink{}, err
	}
	now := s.now()
	exp := now.Add(linkTTL)
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM notify_telegram_codes WHERE expires_at < ?`, now.Unix()); err != nil {
		return TelegramLink{}, err
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO notify_telegram_codes(code_hash, user_id, lang, expires_at) VALUES(?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET code_hash=excluded.code_hash, lang=excluded.lang, expires_at=excluded.expires_at`,
		linkCodeHash(code), u.ID, lang, exp.Unix()); err != nil {
		return TelegramLink{}, err
	}
	return TelegramLink{Code: code, URL: "https://t.me/" + b.Username + "?start=" + code, Bot: b.Username, Expires: exp}, nil
}

var codeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// redeemCode uses up a code: the profile it was made for, and the language
// of the screen that made it. A code works once.
func (s *Service) redeemCode(ctx context.Context, code string) (uid int64, lang string, ok bool, err error) {
	if !codeRe.MatchString(code) {
		return 0, "", false, nil
	}
	err = s.DB.QueryRowContext(ctx, `DELETE FROM notify_telegram_codes WHERE code_hash=? AND expires_at>? RETURNING user_id, lang`,
		linkCodeHash(code), s.now().Unix()).Scan(&uid, &lang)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	return uid, lang, err == nil, err
}

// linkChat makes chat uid's. A chat belongs to one profile: another that had
// it loses it. The profile's language is kept when it has one.
func (s *Service) linkChat(ctx context.Context, uid, chat int64, label, lang string) error {
	var other []int64
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id FROM notify_prefs WHERE telegram_chat=? AND user_id<>?`, chat, uid)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		other = append(other, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range other {
		if err := s.UnlinkTelegram(ctx, id); err != nil {
			return err
		}
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO notify_prefs(user_id, telegram_chat, telegram_name, telegram_on, lang) VALUES(?,?,?,1,?)
		ON CONFLICT(user_id) DO UPDATE SET telegram_chat=excluded.telegram_chat, telegram_name=excluded.telegram_name, telegram_on=1,
		  tg_fail_count=0, tg_next_try_at=0, lang=CASE WHEN notify_prefs.lang='' THEN excluded.lang ELSE notify_prefs.lang END,
		  updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')`, uid, chat, label, lang); err != nil {
		return err
	}
	return nil
}

// UnlinkTelegram forgets uid's chat («Отключить»); what waited for it is done.
func (s *Service) UnlinkTelegram(ctx context.Context, uid int64) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE notify_prefs SET telegram_chat=0, telegram_name='', tg_fail_count=0, tg_next_try_at=0 WHERE user_id=?`, uid); err != nil {
		return err
	}
	p, err := s.Prefs(ctx, uid)
	if err != nil {
		return err
	}
	return s.dropOff(ctx, uid, p)
}

// ForgetTelegram lets go of uid's Telegram altogether: the chat, what waited
// for it, and a link code not yet used. It is what a profile that became a
// kid gets (a kid has no Telegram), and what an administrator does for a kid.
func (s *Service) ForgetTelegram(ctx context.Context, uid int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM notify_telegram_codes WHERE user_id=?`, uid); err != nil {
		return err
	}
	return s.UnlinkTelegram(ctx, uid)
}

// ---- the poller ----

// TelegramKick makes the poller look at the settings again now (the token
// was set, changed or cleared): a wait for news under way is cut short.
func (s *Service) TelegramKick() {
	s.tgMu.Lock()
	s.tgGen++
	cancel := s.tgCancel
	if s.tgWake == nil {
		s.tgWake = make(chan struct{}, 1)
	}
	wake := s.tgWake
	s.tgMu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

// TelegramProblem is what keeps the poller from working ("": nothing; one of
// the Problem constants), and whether a poller is running with a token.
func (s *Service) TelegramProblem() (polling bool, problem string) {
	s.tgMu.Lock()
	defer s.tgMu.Unlock()
	return s.tgPolling, s.tgProblem
}

func (s *Service) setPoll(polling bool, problem string) {
	s.tgMu.Lock()
	s.tgPolling, s.tgProblem = polling, problem
	s.tgMu.Unlock()
}

// polling marks that a poller with a token is at work; a problem it had stays
// until an answer comes.
func (s *Service) polling() {
	s.tgMu.Lock()
	s.tgPolling = true
	s.tgMu.Unlock()
}

// beginPoll registers the wait for news that cancel ends; when the settings
// changed since gen was read, it ends at once.
func (s *Service) beginPoll(cancel context.CancelFunc, gen uint64) {
	s.tgMu.Lock()
	s.tgCancel = cancel
	stale := s.tgGen != gen
	s.tgMu.Unlock()
	if stale {
		cancel()
	}
}

func (s *Service) endPoll() {
	s.tgMu.Lock()
	s.tgCancel = nil
	s.tgMu.Unlock()
}

func (s *Service) generation() uint64 {
	s.tgMu.Lock()
	defer s.tgMu.Unlock()
	return s.tgGen
}

func (s *Service) wakeCh() chan struct{} {
	s.tgMu.Lock()
	defer s.tgMu.Unlock()
	if s.tgWake == nil {
		s.tgWake = make(chan struct{}, 1)
	}
	return s.tgWake
}

// pause waits d, or less when the settings change, or until ctx ends.
func (s *Service) pause(ctx context.Context, d time.Duration) {
	if s.pauseHook != nil {
		s.pauseHook(d)
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-s.wakeCh():
	case <-t.C:
	}
}

// idle waits for a token: until the settings change, or a while.
func (s *Service) idle(ctx context.Context) {
	t := time.NewTimer(idleCheck)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-s.wakeCh():
	case <-t.C:
	}
}

// pollWait is the wait after the n-th failure in a row: 5 s, doubling, at
// most five minutes.
func pollWait(n int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < n && d < maxPollWait; i++ {
		d *= 2
	}
	return min(d, maxPollWait)
}

// RunTelegram asks Telegram for the bot's messages until ctx ends. Only one
// runs per server (a second call returns at once), and it does nothing —
// not even a request — while no token is set.
func (s *Service) RunTelegram(ctx context.Context) {
	if !s.tgRunning.CompareAndSwap(false, true) {
		return
	}
	defer s.tgRunning.Store(false)
	defer s.setPoll(false, "")
	fails := 0
	for ctx.Err() == nil {
		gen := s.generation()
		b, err := s.loadBot(ctx)
		if err != nil {
			s.log().Warn("notify: telegram settings", "err", err)
			s.pause(ctx, time.Minute)
			continue
		}
		if b.Token == "" {
			s.setPoll(false, "")
			fails = 0
			s.idle(ctx)
			continue
		}
		offset := s.telegramOffset(ctx, b.ID)
		s.polling()
		pctx, cancel := context.WithCancel(ctx)
		s.beginPoll(cancel, gen)
		// a connection that went silent (a dropped route, a changed network)
		// must not hold the poller until the system notices
		tctx, tcancel := context.WithTimeout(pctx, s.pollTimeout())
		ups, err := s.tg().GetUpdates(tctx, b.Token, offset, pollSeconds)
		s.endPoll()
		kicked := pctx.Err() != nil
		tcancel()
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if kicked {
				continue // the settings changed: look again at once
			}
			fails++
			wait, problem := pollWait(fails), ProblemUnreachable
			switch d, limited := telegram.RetryAfter(err); {
			case limited:
				wait = min(max(d, time.Second), maxRetryAfter)
			case telegram.IsUnauthorized(err):
				problem = ProblemUnauthorized
			case telegram.IsConflict(err):
				problem = ProblemConflict
			}
			if fails == 1 || fails%20 == 0 {
				s.log().Warn("notify: telegram updates", "err", err, "problem", problem)
			}
			s.setPoll(true, problem)
			s.pause(ctx, wait)
			continue
		}
		fails = 0
		s.setPoll(true, "")
		for _, up := range ups {
			s.handleUpdate(ctx, b, up)
			offset = up.ID + 1
		}
		if len(ups) > 0 {
			if err := s.setState(ctx, stateTelegramOff, strconv.FormatInt(b.ID, 10)+":"+strconv.FormatInt(offset, 10)); err != nil {
				s.log().Warn("notify: telegram offset", "err", err)
			}
		}
	}
}

// pollTimeout is how long one getUpdates may take in all.
func (s *Service) pollTimeout() time.Duration {
	if s.PollTimeout > 0 {
		return s.PollTimeout
	}
	return pollSeconds*time.Second + pollMargin
}

// telegramOffset is the next update to ask bot for. It is kept with the bot's
// id: what another bot (an earlier token) left does not apply.
func (s *Service) telegramOffset(ctx context.Context, bot int64) int64 {
	v, ok, err := s.state(ctx, stateTelegramOff)
	if err != nil || !ok {
		return 0
	}
	id, num, found := strings.Cut(v, ":")
	if !found || id != strconv.FormatInt(bot, 10) {
		return 0
	}
	n, _ := strconv.ParseInt(num, 10, 64)
	return n
}

// chatLang is the language to answer a stranger in: Russian for a Russian
// Telegram, the server's default when it says nothing, else English.
func chatLang(code string) string {
	switch {
	case code == "":
		return i18n.Default
	case strings.HasPrefix(strings.ToLower(code), "ru"):
		return i18n.RU
	}
	return i18n.EN
}

// startCommand splits "/start code" (also "/start@Bot code").
func startCommand(text string) (arg string, ok bool) {
	f := strings.Fields(text)
	if len(f) == 0 {
		return "", false
	}
	cmd, _, _ := strings.Cut(f[0], "@")
	if cmd != "/start" {
		return "", false
	}
	if len(f) > 1 {
		arg = f[1]
	}
	return arg, true
}

// chatLabel is what to call a chat on screen: «@name», else the first name.
func chatLabel(u *telegram.User) string {
	l := strings.TrimSpace(u.FirstName)
	if u.Username != "" {
		l = "@" + u.Username
	}
	if utf8.RuneCountInString(l) > 64 {
		l = string([]rune(l)[:64])
	}
	return l
}

// handleUpdate answers one update: «/start <code>» in a person's own chat
// links the chat to the profile the code was made for. Everything else is
// ignored.
func (s *Service) handleUpdate(ctx context.Context, b botRow, up telegram.Update) {
	m := up.Message
	if m == nil || m.Chat.Type != "private" || m.From == nil {
		return
	}
	arg, ok := startCommand(m.Text)
	if !ok {
		return
	}
	// to a person who has just linked: through the same limits as a digest
	say := func(text string) {
		if err := s.tgSend(ctx, b.Token, m.Chat.ID, text); err != nil {
			s.log().Warn("notify: telegram answer", "err", err)
		}
	}
	// to anyone else: rarely, on a limiter of its own (see answerStranger)
	sayStranger := func(text string) { s.answerStranger(ctx, b.Token, m.Chat.ID, text) }
	lang := chatLang(m.From.LanguageCode)
	expired := func() {
		sayStranger(i18n.TL(lang, "Ссылка устарела. Откройте «Настройки → Уведомления» и нажмите «Подключить Telegram» ещё раз."))
	}
	if arg == "" {
		sayStranger(i18n.TL(lang, "Это бот уведомлений %s. Чтобы получать их здесь, откройте «Настройки → Уведомления» и нажмите «Подключить Telegram».", product))
		return
	}
	uid, codeLang, ok, err := s.redeemCode(ctx, arg)
	if err != nil {
		s.log().Warn("notify: telegram code", "err", err)
		return
	}
	if !ok {
		expired()
		return
	}
	u, err := s.Users.Get(ctx, uid)
	if err != nil || u.IsKid {
		expired() // the profile is gone, or became a kid after the code was made
		return
	}
	if err := s.linkChat(ctx, uid, m.Chat.ID, chatLabel(m.From), codeLang); err != nil {
		s.log().Warn("notify: telegram link", "user", uid, "err", err)
		say(i18n.TL(lang, "Не получилось подключить Telegram, попробуйте ещё раз."))
		return
	}
	p, err := s.Prefs(ctx, uid)
	if err == nil {
		lang = p.Language()
	}
	say(i18n.TL(lang, "Готово! Уведомления %s профиля «%s» будут приходить сюда.", product, u.Name))
}

// answerStranger answers a chat that has not proved to be a person of the
// house (a bare «/start», a link that no longer works). Anyone can write to a
// bot, so such answers are rare: one to a chat in strangerEvery, strangerCap
// for the whole server in strangerWindow, the rest silently dropped. They
// never wait in the digests' queue and never touch its limits: a send does
// not take the pacer's turn, and a 429 to one only silences these answers for
// as long as Telegram asked. While the digests are blocked by a 429 of their
// own, nothing goes here either.
func (s *Service) answerStranger(ctx context.Context, token string, chat int64, text string) {
	now := s.now()
	if now.Before(s.tgBlockedUntil()) || !s.strangerAllowed(chat, now) {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := s.tg().SendMessage(sctx, token, chat, text)
	if d, limited := telegram.RetryAfter(err); limited {
		s.strangerMu.Lock()
		s.strangerOff = now.Add(min(max(d, time.Second), maxRetryAfter))
		s.strangerMu.Unlock()
		return
	}
	if err != nil {
		s.log().Debug("notify: telegram answer to a stranger", "err", err)
	}
}

// strangerAllowed says whether an answer to chat may go now, and counts it.
func (s *Service) strangerAllowed(chat int64, now time.Time) bool {
	s.strangerMu.Lock()
	defer s.strangerMu.Unlock()
	if now.Before(s.strangerOff) {
		return false
	}
	if last, ok := s.strangerChats[chat]; ok && now.Sub(last) < strangerEvery {
		return false
	}
	// forget what is older than the window (at most strangerCap are kept)
	keep := s.strangerSent[:0]
	for _, at := range s.strangerSent {
		if now.Sub(at) < strangerWindow {
			keep = append(keep, at)
		}
	}
	s.strangerSent = keep
	if len(s.strangerSent) >= strangerCap {
		return false
	}
	if s.strangerChats == nil {
		s.strangerChats = map[int64]time.Time{}
	}
	for c, at := range s.strangerChats {
		if now.Sub(at) >= strangerEvery {
			delete(s.strangerChats, c)
		}
	}
	s.strangerChats[chat] = now
	s.strangerSent = append(s.strangerSent, now)
	return true
}

// ---- sending ----

// tgBlockedUntil is when sends may go again after a 429.
func (s *Service) tgBlockedUntil() time.Time {
	s.tgMu.Lock()
	defer s.tgMu.Unlock()
	return s.tgBlocked
}

func (s *Service) tgBlock(d time.Duration) {
	until := s.now().Add(min(max(d, time.Second), maxRetryAfter))
	s.tgMu.Lock()
	if until.After(s.tgBlocked) {
		s.tgBlocked = until
	}
	s.tgMu.Unlock()
}

func (s *Service) tgGap() time.Duration {
	switch {
	case s.TelegramGap > 0:
		return s.TelegramGap
	case s.TelegramGap < 0:
		return 0
	}
	return DefaultTelegramGap
}

// pace spaces the sends: it returns when the gap since the last one has
// passed (or ctx ended).
func (s *Service) pace(ctx context.Context) error {
	gap := s.tgGap()
	if gap == 0 {
		return nil
	}
	s.tgPaceMu.Lock()
	wait := time.Until(s.tgNext)
	s.tgNext = time.Now().Add(max(wait, 0) + gap)
	s.tgPaceMu.Unlock()
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// tgSend sends one message through the limits: nothing goes while a 429 is
// in force (the error says for how long), sends are spaced, and a 429 of its
// own blocks the rest.
func (s *Service) tgSend(ctx context.Context, token string, chat int64, text string) error {
	if until := s.tgBlockedUntil(); s.now().Before(until) {
		return &telegram.Error{Method: "sendMessage", Code: http.StatusTooManyRequests, Description: "blocked until the retry_after has passed",
			RetryAfter: until.Sub(s.now())}
	}
	if err := s.pace(ctx); err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := s.tg().SendMessage(sctx, token, chat, text)
	if d, limited := telegram.RetryAfter(err); limited {
		s.tgBlock(d)
	}
	return err
}

// flushTelegram sends the Telegram digests that are due.
func (s *Service) flushTelegram(ctx context.Context, now time.Time) error {
	b, err := s.loadBot(ctx)
	if err != nil || b.Token == "" {
		return err
	}
	uids, err := s.pendingUsers(ctx, telegramChannel)
	if err != nil {
		return err
	}
	for _, uid := range uids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.now().Before(s.tgBlockedUntil()) {
			return nil // a 429 is in force: nobody is asked until it has passed
		}
		if err := s.flushChat(ctx, b.Token, uid, now); err != nil && ctx.Err() == nil {
			s.log().Warn("notify: telegram digest", "user", uid, "err", err)
		}
	}
	return nil
}

// composeChat is a digest as plain text: a heading, the topics' lines, and
// each line's link (Telegram makes them tappable) under it.
func composeChat(lang string, sections []section) string {
	var t strings.Builder
	t.WriteString(i18n.TL(lang, "Новое в %s", product) + "\n")
	for _, s := range sections {
		t.WriteString("\n" + s.Heading + "\n")
		for _, e := range s.Entries {
			t.WriteString("• " + e.Line + "\n")
			if e.Detail != "" {
				t.WriteString(e.Detail + "\n")
			}
			if e.Link != "" {
				t.WriteString(e.Link + "\n")
			}
		}
	}
	return strings.TrimRight(t.String(), "\n")
}

// chunks cuts text at line ends into parts of at most n runes (a longer line
// is cut where it is).
func chunks(text string, n int) []string {
	var out []string
	var cur strings.Builder
	size := 0
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, strings.TrimRight(cur.String(), "\n"))
			cur.Reset()
			size = 0
		}
	}
	for _, line := range strings.Split(text, "\n") {
		for utf8.RuneCountInString(line) > n {
			flush()
			r := []rune(line)
			out = append(out, string(r[:n]))
			line = string(r[n:])
		}
		l := utf8.RuneCountInString(line) + 1
		if size+l > n {
			flush()
		}
		cur.WriteString(line + "\n")
		size += l
	}
	flush()
	return out
}

func (s *Service) flushChat(ctx context.Context, token string, uid int64, now time.Time) error {
	p, row, err := s.prefs(ctx, uid)
	if err != nil {
		return err
	}
	if s.Users != nil && p.telegramActive() {
		// a profile that became a kid has no Telegram: its chat is let go
		if u, err := s.Users.Get(ctx, uid); err == nil && u.IsKid {
			return s.ForgetTelegram(ctx, uid)
		}
	}
	if !p.telegramActive() || now.Unix() < row.tgNextTry || (p.QuietHours && s.quiet(now)) || now.Sub(time.Unix(row.tgLastSent, 0)) < s.every() {
		return nil
	}
	b, err := s.takeBatch(ctx, telegramChannel, uid, p, now)
	if err != nil || b == nil {
		return err
	}
	lang := p.Language()
	link := s.linker()
	text := composeChat(lang, sectionsOf(b, lang, link, func(string) string { return "" }))
	if err := s.markSending(ctx, telegramChannel, b, now); err != nil {
		return err
	}
	// the server is stopping: the bookkeeping still has to be written, so it
	// goes on a context of its own (a short one)
	done, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for i, part := range chunks(text, chunkRunes) {
		if err := s.tgSend(ctx, token, row.tgChat, part); err != nil {
			if i > 0 {
				// the first part is out: sending again would repeat it
				s.log().Warn("notify: telegram digest cut short", "user", uid, "err", err)
				break
			}
			if derr := s.unmarkSending(done, telegramChannel, b); derr != nil {
				err = errors.Join(err, derr)
			}
			if ctx.Err() != nil {
				return ctx.Err() // stopped, not failed: the digest goes at the next start
			}
			return s.chatFailed(ctx, uid, row, now, err)
		}
	}
	if err := s.markSent(done, telegramChannel, b, now); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(done, `UPDATE notify_prefs SET tg_last_sent_at=?, tg_fail_count=0, tg_next_try_at=0 WHERE user_id=?`, now.Unix(), uid)
	return err
}

// chatFailed records a digest that did not go: a chat the bot cannot write to
// is unlinked; a 429 waits as long as Telegram said (it is no fault of the
// chat); anything else waits longer each time, kept across restarts.
func (s *Service) chatFailed(ctx context.Context, uid int64, row prefsRow, now time.Time, err error) error {
	if telegram.IsGone(err) {
		s.log().Info("notify: telegram chat is gone, unlinked", "user", uid)
		return s.UnlinkTelegram(ctx, uid)
	}
	if d, limited := telegram.RetryAfter(err); limited {
		_, derr := s.DB.ExecContext(ctx, `UPDATE notify_prefs SET tg_next_try_at=? WHERE user_id=?`, now.Add(max(d, time.Second)).Unix(), uid)
		return errors.Join(err, derr)
	}
	_, derr := s.DB.ExecContext(ctx, `UPDATE notify_prefs SET tg_fail_count=?, tg_next_try_at=? WHERE user_id=?`,
		row.tgFail+1, now.Add(backoff(row.tgFail+1)).Unix(), uid)
	return errors.Join(err, derr)
}

// state of the poller and the limits (fields of Service).
type telegramState struct {
	tgMu      sync.Mutex
	tgPolling bool
	tgProblem string
	tgGen     uint64
	tgCancel  context.CancelFunc
	tgWake    chan struct{}
	tgBlocked time.Time // no send before this (a 429)
	tgRunning atomic.Bool

	tgPaceMu sync.Mutex
	tgNext   time.Time // the next send may go at this time (real clock)

	// the answers to strangers (answerStranger)
	strangerMu    sync.Mutex
	strangerChats map[int64]time.Time // chat → when it was answered last
	strangerSent  []time.Time         // when the answers of the last window went
	strangerOff   time.Time           // no answer before this (a 429 to one)
}
