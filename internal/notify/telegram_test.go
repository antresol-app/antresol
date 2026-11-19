package notify

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/telegram"
)

const (
	tgToken      = "123:test-token"
	tgOtherToken = "456:other-token"
)

type tgMsg struct {
	Chat int64
	Text string
}

type tgFailure struct {
	status int
	body   string
}

// fakeTG is the Telegram Bot API: no network, one httptest server. Each
// token is a bot (its getMe answer); sends and polls are recorded.
type fakeTG struct {
	t   *testing.T
	srv *httptest.Server

	mu      sync.Mutex
	bots    map[string]string // token → getMe result
	sent    []tgMsg
	calls   map[string]int
	updates []string // raw updates waiting for a poll
	offsets []int64  // the offset of each getUpdates
	fail    map[string][]tgFailure
	hang    map[string]bool // methods that never answer (until the caller gives up)

	active, maxActive atomic.Int32
}

func newFakeTG(t *testing.T) *fakeTG {
	t.Helper()
	g := &fakeTG{t: t, calls: map[string]int{}, fail: map[string][]tgFailure{}, bots: map[string]string{
		tgToken:      `{"id":42,"is_bot":true,"first_name":"Home Media","username":"home_bot"}`,
		tgOtherToken: `{"id":43,"is_bot":true,"first_name":"Other","username":"other_bot"}`,
	}}
	g.srv = httptest.NewServer(http.HandlerFunc(g.handle))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeTG) client() *telegram.Client { return &telegram.Client{Base: g.srv.URL} }

func reply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (g *fakeTG) handle(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/bot")
	token, method, _ := strings.Cut(rest, "/")
	g.mu.Lock()
	me, known := g.bots[token]
	if !ok || !known {
		g.mu.Unlock()
		reply(w, 401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		return
	}
	g.calls[method]++
	if g.hang[method] {
		g.mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body) // the server notices a caller that left only after the body is read
		<-r.Context().Done()
		return
	}
	if q := g.fail[method]; len(q) > 0 {
		f := q[0]
		g.fail[method] = q[1:]
		g.mu.Unlock()
		reply(w, f.status, f.body)
		return
	}
	g.mu.Unlock()
	var in struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
		Offset int64  `json:"offset"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	switch method {
	case "getMe":
		reply(w, 200, `{"ok":true,"result":`+me+`}`)
	case "sendMessage":
		g.mu.Lock()
		g.sent = append(g.sent, tgMsg{in.ChatID, in.Text})
		g.mu.Unlock()
		reply(w, 200, `{"ok":true,"result":{"message_id":1}}`)
	case "getUpdates":
		g.poll(w, r, in.Offset)
	default:
		reply(w, 404, `{"ok":false,"error_code":404,"description":"Not Found"}`)
	}
}

// poll is a long poll: it answers at once when updates wait, else after a
// short while (or when the caller gives up).
func (g *fakeTG) poll(w http.ResponseWriter, r *http.Request, offset int64) {
	n := g.active.Add(1)
	defer g.active.Add(-1)
	for {
		old := g.maxActive.Load()
		if n <= old || g.maxActive.CompareAndSwap(old, n) {
			break
		}
	}
	g.mu.Lock()
	g.offsets = append(g.offsets, offset)
	var out []string
	var keep []string
	for _, raw := range g.updates {
		var u struct {
			ID int64 `json:"update_id"`
		}
		_ = json.Unmarshal([]byte(raw), &u)
		switch {
		case u.ID < offset: // confirmed
		default:
			out = append(out, raw)
			keep = append(keep, raw)
		}
	}
	g.updates = keep
	g.mu.Unlock()
	if len(out) == 0 {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	reply(w, 200, `{"ok":true,"result":[`+strings.Join(out, ",")+`]}`)
}

func (g *fakeTG) setHang(method string, on bool) {
	g.mu.Lock()
	if g.hang == nil {
		g.hang = map[string]bool{}
	}
	g.hang[method] = on
	g.mu.Unlock()
}

func (g *fakeTG) queue(raw ...string) {
	g.mu.Lock()
	g.updates = append(g.updates, raw...)
	g.mu.Unlock()
}

func (g *fakeTG) failNext(method string, status int, body string) {
	g.mu.Lock()
	g.fail[method] = append(g.fail[method], tgFailure{status, body})
	g.mu.Unlock()
}

func (g *fakeTG) messages() []tgMsg {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]tgMsg(nil), g.sent...)
}

// reset forgets what was sent and asked so far (the confirmation of a link).
func (g *fakeTG) reset() {
	g.mu.Lock()
	g.sent = nil
	g.calls = map[string]int{}
	g.mu.Unlock()
}

func (g *fakeTG) count(method string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[method]
}

func (g *fakeTG) lastOffset() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.offsets) == 0 {
		return -1
	}
	return g.offsets[len(g.offsets)-1]
}

func rawStart(id, chat int64, user, lang, text string) string {
	return fmt.Sprintf(`{"update_id":%d,"message":{"message_id":1,"from":{"id":%d,"first_name":"Аня","username":%q,"language_code":%q},"chat":{"id":%d,"type":"private"},"text":%q}}`,
		id, chat, user, lang, chat, text)
}

func startUpdate(id, chat int64, user, lang, text string) telegram.Update {
	return telegram.Update{ID: id, Message: &telegram.Message{Text: text, Chat: telegram.Chat{ID: chat, Type: "private"},
		From: &telegram.User{ID: chat, Username: user, FirstName: "Аня", LanguageCode: lang}}}
}

// newTGFixture is a fixture whose server has a bot (the token set through
// getMe) and no waits between sends or for events to settle.
func newTGFixture(t *testing.T) (*fixture, *fakeTG) {
	t.Helper()
	f := newFixture(t)
	g := newFakeTG(t)
	f.svc.TG = g.client()
	f.svc.TelegramGap = -1
	f.svc.Settle = -1
	if _, err := f.svc.SetTelegramToken(f.ctx, tgToken); err != nil {
		t.Fatal(err)
	}
	return f, g
}

// link links chat with u the way a person does: the code, then /start.
func (f *fixture) link(t *testing.T, u auth.User, chat int64, lang string) {
	t.Helper()
	l, err := f.svc.StartTelegramLink(f.ctx, u, lang)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.handleUpdate(f.ctx, botRow{Token: tgToken}, startUpdate(1, chat, "anya", "ru", "/start "+l.Code))
	if p, err := f.svc.Prefs(f.ctx, u.ID); err != nil || !p.TelegramLinked {
		t.Fatalf("not linked: %+v %v", p, err)
	}
}

func (f *fixture) available(t *testing.T, uid int64, key, title string) {
	t.Helper()
	if _, err := f.svc.Enqueue(f.ctx, Event{UserID: uid, Topic: Available, Key: key, ItemID: 5,
		Data: Data{Title: title, Year: 2024, Kind: library.KindMovie}}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) tgPending(t *testing.T, uid int64) int {
	t.Helper()
	var n int
	f.db.QueryRow(`SELECT COUNT(*) FROM notify_events WHERE user_id=? AND tg_sent_at IS NULL`, uid).Scan(&n)
	return n
}

func TestTelegramTokenIsCheckedAndKept(t *testing.T) {
	f := newFixture(t)
	g := newFakeTG(t)
	f.svc.TG = g.client()
	var logs bytes.Buffer
	f.svc.Log = slog.New(slog.NewTextHandler(&logs, nil))

	if _, err := f.svc.SetTelegramToken(f.ctx, "not a token"); !errors.Is(err, ErrBadTelegramToken) || g.count("getMe") != 0 {
		t.Fatalf("a malformed token must not reach Telegram: %v, %d calls", err, g.count("getMe"))
	}
	if _, err := f.svc.SetTelegramToken(f.ctx, "999:unknown-token"); !errors.Is(err, ErrTelegramRejected) {
		t.Fatalf("rejected: %v", err)
	}
	if f.svc.TelegramBot(f.ctx).Set {
		t.Fatal("a rejected token was kept")
	}
	bot, err := f.svc.SetTelegramToken(f.ctx, "  "+tgToken+"\n")
	if err != nil || !bot.Set || bot.Username != "home_bot" || bot.Name != "Home Media" {
		t.Fatalf("set: %+v %v", bot, err)
	}
	// kept in the settings, never in what the screens get
	var raw string
	if err := f.db.QueryRow(`SELECT value FROM settings WHERE key='telegram'`).Scan(&raw); err != nil || !strings.Contains(raw, tgToken) {
		t.Fatalf("stored: %q %v", raw, err)
	}
	if b, _ := json.Marshal(f.svc.TelegramBot(f.ctx)); strings.Contains(string(b), "test-token") {
		t.Fatalf("the bot as the screens see it carries the token: %s", b)
	}
	if again, err := f.svc.CheckTelegram(f.ctx); err != nil || again.Username != "home_bot" {
		t.Fatalf("check: %+v %v", again, err)
	}
	if err := f.svc.ClearTelegramToken(f.ctx); err != nil || f.svc.TelegramBot(f.ctx).Set {
		t.Fatalf("clear: %v", err)
	}
	if _, err := f.svc.CheckTelegram(f.ctx); !errors.Is(err, ErrNoTelegram) {
		t.Fatalf("check without a token: %v", err)
	}
	// Telegram unreachable: an error that says so, and no token in the log
	g.srv.Close()
	if _, err := f.svc.SetTelegramToken(f.ctx, tgToken); !errors.Is(err, ErrTelegramUnreachable) {
		t.Fatalf("unreachable: %v", err)
	}
	if strings.Contains(logs.String(), "test-token") || strings.Contains(logs.String(), tgToken) {
		t.Fatalf("the log carries the token:\n%s", logs.String())
	}
}

func TestLinkingAChatByItsStartCode(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)

	l, err := f.svc.StartTelegramLink(f.ctx, u, "en")
	if err != nil || l.Bot != "home_bot" || !regexp.MustCompile(`^[a-z0-9]{16}$`).MatchString(l.Code) ||
		l.URL != "https://t.me/home_bot?start="+l.Code || !l.Expires.After(f.clock) {
		t.Fatalf("link: %+v %v", l, err)
	}
	var stored string
	f.db.QueryRow(`SELECT code_hash FROM notify_telegram_codes WHERE user_id=?`, u.ID).Scan(&stored)
	if stored == "" || strings.Contains(stored, l.Code) {
		t.Fatalf("the code is stored as is: %q", stored)
	}
	// a person opens the link and presses «Start»; the profile has no language
	// yet: the screen's is what the answer comes in
	f.svc.handleUpdate(f.ctx, botRow{Token: tgToken}, startUpdate(1, 77, "anya", "ru", "/start "+l.Code))
	p, _ := f.svc.Prefs(f.ctx, u.ID)
	if !p.TelegramLinked || p.TelegramName != "@anya" || !p.TelegramOn || p.Lang != "en" {
		t.Fatalf("prefs: %+v", p)
	}
	msgs := g.messages()
	if len(msgs) != 1 || msgs[0].Chat != 77 || msgs[0].Text != "Done! Antresol notifications for the profile “Аня” will arrive here." {
		t.Fatalf("confirmation: %+v", msgs)
	}
	// a code works once
	f.svc.handleUpdate(f.ctx, botRow{Token: tgToken}, startUpdate(2, 88, "boris", "en", "/start "+l.Code))
	msgs = g.messages()
	if len(msgs) != 2 || msgs[1].Chat != 88 || !strings.Contains(msgs[1].Text, "The link has expired") {
		t.Fatalf("a used code: %+v", msgs)
	}
	if p, _ := f.svc.Prefs(f.ctx, u.ID); p.TelegramName != "@anya" {
		t.Fatalf("the chat changed: %+v", p)
	}
	// the profile's own language wins over the screen's, in Russian here
	if _, err := f.svc.SetPrefs(f.ctx, u, Prefs{Lang: "ru"}); err != nil {
		t.Fatal(err)
	}
	f.link(t, u, 78, "en")
	if m := g.messages(); m[len(m)-1].Text != "Готово! Уведомления Antresol профиля «Аня» будут приходить сюда." {
		t.Fatalf("confirmation in the profile's language: %q", m[len(m)-1].Text)
	}
}

func TestKidsDoNotLinkAndAnOldCodeIsRefused(t *testing.T) {
	f, g := newTGFixture(t)
	kid := f.user(t, "Вася", false, true)
	if _, err := f.svc.StartTelegramLink(f.ctx, kid, "ru"); !errors.Is(err, ErrKidTelegram) {
		t.Fatalf("a kid got a code: %v", err)
	}
	u := f.user(t, "Аня", false, false)
	l, err := f.svc.StartTelegramLink(f.ctx, u, "ru")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE users SET is_kid=1 WHERE id=?`, u.ID); err != nil { // made a kid meanwhile
		t.Fatal(err)
	}
	f.svc.handleUpdate(f.ctx, botRow{Token: tgToken}, startUpdate(1, 77, "anya", "ru", "/start "+l.Code))
	if p, _ := f.svc.Prefs(f.ctx, u.ID); p.TelegramLinked {
		t.Fatal("a kid linked a chat")
	}
	if m := g.messages(); len(m) != 1 || !strings.HasPrefix(m[0].Text, "Ссылка устарела") {
		t.Fatalf("answer: %+v", m)
	}
}

func TestOnlyStartInAPrivateChatCounts(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	l, _ := f.svc.StartTelegramLink(f.ctx, u, "ru")
	b := botRow{Token: tgToken}
	group := startUpdate(1, -100, "anya", "ru", "/start "+l.Code)
	group.Message.Chat.Type = "group"
	f.svc.handleUpdate(f.ctx, b, group)
	f.svc.handleUpdate(f.ctx, b, startUpdate(2, 77, "anya", "ru", "hello"))
	f.svc.handleUpdate(f.ctx, b, telegram.Update{ID: 3})
	f.svc.handleUpdate(f.ctx, b, startUpdate(4, 77, "anya", "ru", "/stop"))
	if len(g.messages()) != 0 {
		t.Fatalf("answered: %+v", g.messages())
	}
	// «/start» alone says what to do (in Telegram's language)
	f.svc.handleUpdate(f.ctx, b, startUpdate(5, 77, "anya", "en-US", "/start"))
	if m := g.messages(); len(m) != 1 || !strings.Contains(m[0].Text, "Connect Telegram") {
		t.Fatalf("help: %+v", m)
	}
	// the link with the bot's name after the command
	f.svc.handleUpdate(f.ctx, b, startUpdate(6, 77, "anya", "ru", "/start@home_bot "+l.Code))
	if p, _ := f.svc.Prefs(f.ctx, u.ID); !p.TelegramLinked {
		t.Fatal("/start@bot did not link")
	}
	// an old code
	l2, _ := f.svc.StartTelegramLink(f.ctx, u, "ru")
	f.clock = f.clock.Add(linkTTL + time.Second)
	f.svc.handleUpdate(f.ctx, b, startUpdate(7, 78, "anya", "ru", "/start "+l2.Code))
	var chat int64
	f.db.QueryRow(`SELECT telegram_chat FROM notify_prefs WHERE user_id=?`, u.ID).Scan(&chat)
	if chat != 77 || len(g.messages()) != 3 {
		t.Fatalf("an expired code: chat %d, %+v", chat, g.messages())
	}
}

func TestAChatBelongsToOneProfile(t *testing.T) {
	f, _ := newTGFixture(t)
	a := f.user(t, "Аня", false, false)
	b := f.user(t, "Боря", false, false)
	f.link(t, a, 77, "ru")
	f.link(t, b, 77, "ru")
	pa, _ := f.svc.Prefs(f.ctx, a.ID)
	pb, _ := f.svc.Prefs(f.ctx, b.ID)
	if pa.TelegramLinked || !pb.TelegramLinked {
		t.Fatalf("a %+v b %+v", pa, pb)
	}
}

func TestDigestGoesToTheChatAloneWithoutAnAddress(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	if _, err := f.svc.SetPrefs(f.ctx, u, Prefs{Available: true, Lang: "ru"}); err != nil {
		t.Fatal(err)
	}
	f.available(t, u.ID, "early", "Рано") // no channel yet: nothing is queued
	if f.tgPending(t, u.ID) != 0 || f.pending(t, u.ID) != 0 {
		t.Fatal("queued without a channel")
	}
	f.link(t, u, 77, "ru")
	g.reset()
	f.available(t, u.ID, "k1", "Фильм")
	if f.tgPending(t, u.ID) != 1 || f.pending(t, u.ID) != 0 {
		t.Fatalf("pending tg %d mail %d", f.tgPending(t, u.ID), f.pending(t, u.ID))
	}
	f.flush(t)
	msgs := g.messages()
	if len(msgs) != 1 || msgs[0].Chat != 77 {
		t.Fatalf("messages: %+v", msgs)
	}
	want := "Новое в Antresol\n\nПоявилось то, что вы хотели посмотреть\n• Фильм (2024)\nhttps://antresol.example/item/5"
	if msgs[0].Text != want {
		t.Fatalf("text:\n%s\nwant:\n%s", msgs[0].Text, want)
	}
	if len(f.out.all()) != 0 {
		t.Fatal("a letter went to a profile without an address")
	}
	f.flush(t)
	f.clock = f.clock.Add(2 * time.Hour)
	f.flush(t)
	if len(g.messages()) != 1 {
		t.Fatalf("sent again: %+v", g.messages())
	}
	// without a public address: the lines alone
	f.svc.PublicURL = ""
	f.available(t, u.ID, "k2", "Другое")
	f.flush(t)
	if m := g.messages(); len(m) != 2 || strings.Contains(m[1].Text, "http") || !strings.Contains(m[1].Text, "• Другое (2024)") {
		t.Fatalf("no public address: %+v", m)
	}
}

func TestMailAndTelegramGoIndependently(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Available: true, Lang: "ru"})
	f.link(t, u, 77, "ru")
	g.reset()
	f.available(t, u.ID, "k1", "Фильм")
	if f.pending(t, u.ID) != 1 || f.tgPending(t, u.ID) != 1 {
		t.Fatal("both channels should wait")
	}
	f.out.fail = errors.New("smtp down")
	f.flush(t)
	if len(g.messages()) != 1 || f.tgPending(t, u.ID) != 0 || f.pending(t, u.ID) != 1 {
		t.Fatalf("telegram must go though mail fails: %d msgs, tg %d, mail %d", len(g.messages()), f.tgPending(t, u.ID), f.pending(t, u.ID))
	}
	f.out.fail = nil
	f.clock = f.clock.Add(time.Hour)
	f.flush(t)
	if len(f.out.all()) != 1 || len(g.messages()) != 1 {
		t.Fatalf("mail %d, telegram %d: each exactly once", len(f.out.all()), len(g.messages()))
	}
}

func TestTelegramLimitBlocksEverySendUntilRetryAfter(t *testing.T) {
	f, g := newTGFixture(t)
	a := f.user(t, "Аня", false, false)
	b := f.user(t, "Боря", false, false)
	for i, u := range []auth.User{a, b} {
		if _, err := f.svc.SetPrefs(f.ctx, u, Prefs{Available: true, Lang: "ru"}); err != nil {
			t.Fatal(err)
		}
		f.link(t, u, int64(70+i), "ru")
	}
	g.reset()
	f.available(t, a.ID, "k1", "Фильм")
	f.available(t, b.ID, "k1", "Фильм")
	g.failNext("sendMessage", 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 30","parameters":{"retry_after":30}}`)
	f.flush(t)
	if g.count("sendMessage") != 1 || len(g.messages()) != 0 {
		t.Fatalf("after the 429: %d calls, %d sent — nobody else may be asked", g.count("sendMessage"), len(g.messages()))
	}
	f.clock = f.clock.Add(10 * time.Second)
	f.flush(t)
	if g.count("sendMessage") != 1 {
		t.Fatalf("asked again within the retry_after: %d calls", g.count("sendMessage"))
	}
	f.clock = f.clock.Add(25 * time.Second) // 35 s in all
	f.flush(t)
	if len(g.messages()) != 2 || f.tgPending(t, a.ID)+f.tgPending(t, b.ID) != 0 {
		t.Fatalf("after the wait: %+v", g.messages())
	}
	var fails int
	f.db.QueryRow(`SELECT tg_fail_count FROM notify_prefs WHERE user_id=?`, a.ID).Scan(&fails)
	if fails != 0 {
		t.Fatalf("a 429 counted as a failure of the chat: %d", fails)
	}
}

func TestFailedTelegramSendWaitsLongerEachTime(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "a@example.com", Prefs{Available: true})
	f.link(t, u, 77, "ru")
	g.reset()
	f.available(t, u.ID, "k1", "Фильм")
	g.failNext("sendMessage", 500, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`)
	f.flush(t)
	if f.tgPending(t, u.ID) != 1 {
		t.Fatal("a failed send is not done")
	}
	f.flush(t) // within the wait: not asked
	if g.count("sendMessage") != 1 {
		t.Fatalf("asked within the wait: %d", g.count("sendMessage"))
	}
	f.clock = f.clock.Add(retryAfter + time.Minute)
	f.flush(t)
	if f.tgPending(t, u.ID) != 0 || len(g.messages()) != 1 {
		t.Fatalf("not retried: %+v", g.messages())
	}
}

func TestBlockedChatIsUnlinked(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Available: true})
	f.link(t, u, 77, "ru")
	g.reset()
	f.available(t, u.ID, "k1", "Фильм")
	g.failNext("sendMessage", 403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
	f.flush(t)
	p, _ := f.svc.Prefs(f.ctx, u.ID)
	if p.TelegramLinked || f.tgPending(t, u.ID) != 0 {
		t.Fatalf("a blocked chat stays linked: %+v, tg pending %d", p, f.tgPending(t, u.ID))
	}
	if len(f.out.all()) != 1 {
		t.Fatal("the mail of the profile was lost")
	}
	f.flush(t)
	if g.count("sendMessage") != 1 {
		t.Fatal("sent to an unlinked chat")
	}
}

func TestTelegramKeepsQuietHoursAndTheHour(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	if _, err := f.svc.SetPrefs(f.ctx, u, Prefs{Available: true, QuietHours: true}); err != nil {
		t.Fatal(err)
	}
	f.link(t, u, 77, "ru")
	g.reset()
	f.clock = time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)
	f.available(t, u.ID, "k1", "Фильм")
	f.flush(t)
	if len(g.messages()) != 0 {
		t.Fatal("sent at night")
	}
	f.clock = time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	f.flush(t)
	if len(g.messages()) != 1 {
		t.Fatalf("not sent in the morning: %+v", g.messages())
	}
	f.available(t, u.ID, "k2", "Ещё")
	f.clock = f.clock.Add(20 * time.Minute)
	f.flush(t)
	if len(g.messages()) != 1 {
		t.Fatal("a second message within the hour")
	}
	f.clock = f.clock.Add(41 * time.Minute)
	f.flush(t)
	if len(g.messages()) != 2 {
		t.Fatal("not sent after the hour")
	}
}

func TestChannelChoice(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Available: true})
	f.link(t, u, 77, "ru")
	g.reset()
	off := false
	// Telegram off: only mail waits
	if _, err := f.svc.SetChannels(f.ctx, u.ID, nil, &off); err != nil {
		t.Fatal(err)
	}
	f.available(t, u.ID, "k1", "Фильм")
	if f.pending(t, u.ID) != 1 || f.tgPending(t, u.ID) != 0 {
		t.Fatalf("telegram off: mail %d tg %d", f.pending(t, u.ID), f.tgPending(t, u.ID))
	}
	// a save of the topics leaves the channels as they are
	if p, err := f.svc.SetPrefs(f.ctx, u, Prefs{Email: "anya@example.com", Available: true}); err != nil || p.TelegramOn || !p.EmailOn || !p.TelegramLinked {
		t.Fatalf("set prefs: %+v %v", p, err)
	}
	// both off: nothing is queued, and what waited is done
	if _, err := f.svc.SetChannels(f.ctx, u.ID, &off, &off); err != nil {
		t.Fatal(err)
	}
	if f.pending(t, u.ID) != 0 || f.tgPending(t, u.ID) != 0 {
		t.Fatal("what waited for a channel turned off stays")
	}
	f.available(t, u.ID, "k2", "Другое")
	if n := f.pending(t, u.ID) + f.tgPending(t, u.ID); n != 0 {
		t.Fatalf("queued with both channels off: %d", n)
	}
	// Telegram only
	on := true
	if _, err := f.svc.SetChannels(f.ctx, u.ID, nil, &on); err != nil {
		t.Fatal(err)
	}
	f.available(t, u.ID, "k3", "Третье")
	f.flush(t)
	if len(g.messages()) != 1 || len(f.out.all()) != 0 {
		t.Fatalf("telegram only: %d messages, %d letters", len(g.messages()), len(f.out.all()))
	}
	// unlinking is the same as turning it off
	if err := f.svc.UnlinkTelegram(f.ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	f.available(t, u.ID, "k4", "Четвёртое")
	if f.tgPending(t, u.ID) != 0 {
		t.Fatal("queued for an unlinked chat")
	}
}

func TestPollerLinksAChatAndKeepsItsPlace(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	l, err := f.svc.StartTelegramLink(f.ctx, u, "ru")
	if err != nil {
		t.Fatal(err)
	}
	g.queue(rawStart(100, 77, "anya", "ru", "/start "+l.Code))
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.svc.RunTelegram(ctx); close(done) }()
	waitFor(t, "the chat linked", func() bool { p, _ := f.svc.Prefs(f.ctx, u.ID); return p.TelegramLinked })
	waitFor(t, "the next poll asks past the update", func() bool { return g.lastOffset() == 101 })
	if v, ok, _ := f.svc.state(f.ctx, stateTelegramOff); !ok || v != "42:101" {
		t.Fatalf("offset kept: %q %v", v, ok)
	}
	if m := g.messages(); len(m) != 1 || m[0].Chat != 77 {
		t.Fatalf("answers: %+v", m)
	}
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the poller did not stop")
	}
}

func TestPollerDoesNothingWithoutATokenAndFollowsIt(t *testing.T) {
	f := newFixture(t)
	g := newFakeTG(t)
	f.svc.TG = g.client()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.svc.RunTelegram(ctx); close(done) }()
	time.Sleep(150 * time.Millisecond)
	if n := g.count("getUpdates") + g.count("getMe"); n != 0 {
		t.Fatalf("%d requests without a token", n)
	}
	if polling, _ := f.svc.TelegramProblem(); polling {
		t.Fatal("polling without a token")
	}
	// the token set: polling starts at once
	if _, err := f.svc.SetTelegramToken(f.ctx, tgToken); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "polling", func() bool { return g.count("getUpdates") > 0 })
	if polling, problem := f.svc.TelegramProblem(); !polling || problem != "" {
		t.Fatalf("status: %v %q", polling, problem)
	}
	// the token cleared: the wait for news under way is cut short and no more asked
	if err := f.svc.ClearTelegramToken(f.ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "no poll in flight", func() bool { return g.active.Load() == 0 })
	time.Sleep(100 * time.Millisecond)
	n := g.count("getUpdates")
	time.Sleep(150 * time.Millisecond)
	if g.count("getUpdates") != n {
		t.Fatal("still polling after the token was cleared")
	}
	stop()
	<-done
}

func TestOnlyOnePollerRuns(t *testing.T) {
	f, g := newTGFixture(t)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	first := make(chan struct{})
	go func() { f.svc.RunTelegram(ctx); close(first) }()
	waitFor(t, "polling", func() bool { return g.count("getUpdates") > 0 })
	second := make(chan struct{})
	go func() { f.svc.RunTelegram(ctx); close(second) }()
	select {
	case <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("a second poller kept running")
	}
	time.Sleep(100 * time.Millisecond)
	if g.maxActive.Load() != 1 {
		t.Fatalf("%d polls at once", g.maxActive.Load())
	}
	stop()
	<-first
}

func TestPollerBacksOff(t *testing.T) {
	f, g := newTGFixture(t)
	var mu sync.Mutex
	var waits []time.Duration
	f.svc.pauseHook = func(d time.Duration) {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	g.failNext("getUpdates", 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`)
	g.failNext("getUpdates", 409, `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`)
	g.failNext("getUpdates", 502, `<html>bad gateway</html>`)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.svc.RunTelegram(ctx); close(done) }()
	waitFor(t, "recovered", func() bool {
		mu.Lock()
		defer mu.Unlock()
		polling, problem := f.svc.TelegramProblem()
		return len(waits) == 3 && polling && problem == "" && g.count("getUpdates") >= 4
	})
	mu.Lock()
	got := append([]time.Duration(nil), waits...)
	mu.Unlock()
	// the 429's own wait, then the failures in a row count: 10 s, 20 s (doubling from 5 s)
	if len(got) != 3 || got[0] != 7*time.Second || got[1] != 10*time.Second || got[2] != 20*time.Second {
		t.Fatalf("waits: %v", got)
	}
	stop()
	<-done
}

func TestAnotherBotDropsTheLinkedChats(t *testing.T) {
	f, _ := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	f.link(t, u, 77, "ru")
	// the same bot again keeps the chats, even through a cleared token
	if err := f.svc.ClearTelegramToken(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetTelegramToken(f.ctx, tgToken); err != nil {
		t.Fatal(err)
	}
	if p, _ := f.svc.Prefs(f.ctx, u.ID); !p.TelegramLinked {
		t.Fatal("the same bot lost the chats")
	}
	// another bot cannot write to them
	if _, err := f.svc.SetTelegramToken(f.ctx, tgOtherToken); err != nil {
		t.Fatal(err)
	}
	if p, _ := f.svc.Prefs(f.ctx, u.ID); p.TelegramLinked {
		t.Fatal("a chat of the old bot stays linked")
	}
}

func TestLongDigestsGoInParts(t *testing.T) {
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, "• Фильм номер "+strconv.Itoa(i)+" — "+strings.Repeat("я", 30))
	}
	text := strings.Join(lines, "\n")
	parts := chunks(text, 3500)
	if len(parts) < 3 {
		t.Fatalf("%d parts", len(parts))
	}
	for _, p := range parts {
		if n := len([]rune(p)); n > 3500 || n == 0 {
			t.Fatalf("a part of %d characters", n)
		}
	}
	if strings.Join(parts, "\n") != text {
		t.Fatal("the parts do not make the text")
	}
	if got := chunks(strings.Repeat("ы", 8000), 3500); len(got) != 3 {
		t.Fatalf("a long line: %d parts", len(got))
	}
}

func TestAChannelNobodyUsesIsDoneFromTheStart(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Available: true})
	f.available(t, u.ID, "k1", "Фильм")
	// an event queued for mail alone is done for the channel nobody uses
	var tg, mail sql.NullInt64
	f.db.QueryRow(`SELECT tg_sent_at, sent_at FROM notify_events WHERE user_id=?`, u.ID).Scan(&tg, &mail)
	if !tg.Valid || mail.Valid {
		t.Fatalf("tg_sent_at %v sent_at %v", tg, mail)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ---- strangers, kids, the poller's edges, shutdown ----

func TestAChatThatIsNotLinkedGetsOneAnswerAnHour(t *testing.T) {
	f, g := newTGFixture(t)
	b := botRow{Token: tgToken}
	for i := int64(0); i < 50; i++ {
		f.svc.handleUpdate(f.ctx, b, startUpdate(i, 77, "x", "ru", "/start"))
		f.svc.handleUpdate(f.ctx, b, startUpdate(100+i, 77, "x", "ru", "/start nosuchcode"))
	}
	if m := g.messages(); len(m) != 1 || m[0].Chat != 77 {
		t.Fatalf("100 starts from one chat: %d answers, want 1: %+v", len(m), m)
	}
	f.clock = f.clock.Add(strangerEvery + time.Minute)
	f.svc.handleUpdate(f.ctx, b, startUpdate(200, 77, "x", "ru", "/start"))
	if m := g.messages(); len(m) != 2 {
		t.Fatalf("an hour later the chat is answered again: %d", len(m))
	}
	// a person who links is still answered at once, whatever was said before
	u := f.user(t, "Аня", false, false)
	l, _ := f.svc.StartTelegramLink(f.ctx, u, "ru")
	f.svc.handleUpdate(f.ctx, b, startUpdate(201, 77, "x", "ru", "/start "+l.Code))
	if m := g.messages(); len(m) != 3 || !strings.HasPrefix(m[2].Text, "Готово") {
		t.Fatalf("the confirmation: %+v", m)
	}
}

func TestAnswersToStrangersAreCappedForTheServer(t *testing.T) {
	f, g := newTGFixture(t)
	b := botRow{Token: tgToken}
	for i := int64(0); i < 60; i++ {
		f.svc.handleUpdate(f.ctx, b, startUpdate(i, 1000+i, "x", "en", "/start"))
	}
	if n := len(g.messages()); n != strangerCap {
		t.Fatalf("60 strangers: %d answers, want %d", n, strangerCap)
	}
	f.clock = f.clock.Add(strangerWindow + time.Minute)
	f.svc.handleUpdate(f.ctx, b, startUpdate(900, 5000, "x", "en", "/start"))
	if n := len(g.messages()); n != strangerCap+1 {
		t.Fatalf("after the window: %d", n)
	}
}

func TestStrangerAnswersShareNothingWithTheDigests(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "a@example.com", Prefs{Available: true})
	f.link(t, u, 77, "ru")
	g.reset()
	b := botRow{Token: tgToken}

	// a 429 to a stranger's answer blocks nothing of the digests
	g.failNext("sendMessage", 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":300}}`)
	f.svc.handleUpdate(f.ctx, b, startUpdate(1, 900, "x", "ru", "/start"))
	if until := f.svc.tgBlockedUntil(); f.clock.Before(until) {
		t.Fatalf("a stranger's 429 blocks the digests until %v", until)
	}
	f.available(t, u.ID, "k1", "Фильм")
	f.flush(t)
	if m := g.messages(); len(m) != 1 || m[0].Chat != 77 {
		t.Fatalf("the digest: %+v", m)
	}
	// strangers are silent for as long as Telegram asked, then answered again
	f.svc.handleUpdate(f.ctx, b, startUpdate(2, 901, "x", "ru", "/start"))
	if len(g.messages()) != 1 {
		t.Fatal("answered inside the retry_after")
	}
	f.clock = f.clock.Add(6 * time.Minute)
	f.svc.handleUpdate(f.ctx, b, startUpdate(3, 902, "x", "ru", "/start"))
	if len(g.messages()) != 2 {
		t.Fatal("not answered after the retry_after")
	}

	// and a stranger's answer never waits for the digests' pacer
	f.svc.TelegramGap = time.Hour
	if err := f.svc.pace(f.ctx); err != nil { // takes the turn
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	f.svc.handleUpdate(ctx, b, startUpdate(4, 903, "x", "ru", "/start"))
	if len(g.messages()) != 3 {
		t.Fatalf("an answer waited for the pacer: %+v", g.messages())
	}
}

func TestAProfileThatBecameAKidLosesItsChat(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "a@example.com", Prefs{Available: true})
	f.link(t, u, 77, "ru")
	g.reset()
	f.available(t, u.ID, "k1", "Фильм")
	if _, err := f.db.Exec(`UPDATE users SET is_kid=1 WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
	if p, _ := f.svc.Prefs(f.ctx, u.ID); p.TelegramLinked {
		t.Fatal("the chat of a kid stays linked")
	}
	if len(g.messages()) != 0 {
		t.Fatalf("a digest went to a kid's chat: %+v", g.messages())
	}
	if f.tgPending(t, u.ID) != 0 {
		t.Fatal("what waited for the chat is not dropped")
	}

	// ForgetTelegram also takes a code that was not used yet
	a := f.user(t, "Боря", false, false)
	if _, err := f.svc.StartTelegramLink(f.ctx, a, "ru"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ForgetTelegram(f.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM notify_telegram_codes WHERE user_id=?`, a.ID); n != 0 {
		t.Fatalf("%d codes left", n)
	}
}

func TestTheOffsetBelongsToTheBotThatLeftIt(t *testing.T) {
	f, _ := newTGFixture(t)
	if err := f.svc.setState(f.ctx, stateTelegramOff, "42:500"); err != nil {
		t.Fatal(err)
	}
	if got := f.svc.telegramOffset(f.ctx, 42); got != 500 {
		t.Fatalf("own offset: %d", got)
	}
	if got := f.svc.telegramOffset(f.ctx, 43); got != 0 {
		t.Fatalf("another bot's offset applies: %d", got)
	}
	// the old bot's poller writes its place after the swap: the new bot ignores it
	if _, err := f.svc.SetTelegramToken(f.ctx, tgOtherToken); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.setState(f.ctx, stateTelegramOff, "42:9000"); err != nil {
		t.Fatal(err)
	}
	if got := f.svc.telegramOffset(f.ctx, 43); got != 0 {
		t.Fatalf("the new bot starts from the old bot's place: %d", got)
	}
	if err := f.svc.setState(f.ctx, stateTelegramOff, "700"); err != nil { // no bot id: not trusted
		t.Fatal(err)
	}
	if got := f.svc.telegramOffset(f.ctx, 43); got != 0 {
		t.Fatalf("an offset without a bot: %d", got)
	}
}

func TestAPollThatNeverAnswersIsGivenUp(t *testing.T) {
	f, g := newTGFixture(t)
	f.svc.PollTimeout = 50 * time.Millisecond
	g.setHang("getUpdates", true)
	var mu sync.Mutex
	var waits []time.Duration
	f.svc.pauseHook = func(d time.Duration) {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.svc.RunTelegram(ctx); close(done) }()
	waitFor(t, "the silent poll given up", func() bool {
		mu.Lock()
		defer mu.Unlock()
		_, problem := f.svc.TelegramProblem()
		return len(waits) >= 1 && problem == ProblemUnreachable
	})
	g.setHang("getUpdates", false)
	waitFor(t, "recovered", func() bool { _, problem := f.svc.TelegramProblem(); return problem == "" })
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the poller did not stop")
	}
}

func TestShutdownDuringASendLosesNoDigest(t *testing.T) {
	f, g := newTGFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "a@example.com", Prefs{Available: true})
	f.link(t, u, 77, "ru")
	g.reset()
	f.available(t, u.ID, "k1", "Фильм")
	g.setHang("sendMessage", true)
	ctx, stop := context.WithCancel(f.ctx)
	errc := make(chan error, 1)
	go func() { errc <- f.svc.Flush(ctx) }()
	waitFor(t, "the send under way", func() bool { return g.count("sendMessage") == 1 })
	stop() // the server is stopping
	select {
	case <-errc:
	case <-time.After(5 * time.Second):
		t.Fatal("the flush did not stop")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM notify_events WHERE user_id=? AND tg_sending_at IS NOT NULL AND tg_sent_at IS NULL`, u.ID); n != 0 {
		t.Fatalf("%d events stay «sending»: they would count as sent after a while", n)
	}
	if f.tgPending(t, u.ID) != 1 {
		t.Fatal("the digest is lost")
	}
	var fails int
	var next int64
	f.db.QueryRow(`SELECT tg_fail_count, tg_next_try_at FROM notify_prefs WHERE user_id=?`, u.ID).Scan(&fails, &next)
	if fails != 0 || next != 0 {
		t.Fatalf("a stop counted as a failure: %d, wait until %d", fails, next)
	}
	// the next start sends it
	g.setHang("sendMessage", false)
	f.flush(t)
	if m := g.messages(); len(m) != 1 || m[0].Chat != 77 || f.tgPending(t, u.ID) != 0 {
		t.Fatalf("after the restart: %+v", m)
	}
}
