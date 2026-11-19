package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/mailer"
	"github.com/antresol-app/antresol/internal/notify"
	"github.com/antresol-app/antresol/internal/telegram"
)

const apiTGToken = "123:test-token"

// fakeBot is the Telegram Bot API for the handlers' tests: one bot, the
// updates a test queues, the messages the server sends. No network.
type fakeBot struct {
	mu      sync.Mutex
	srv     *httptest.Server
	updates []string
	sent    []string
}

func newFakeBot(t *testing.T) *fakeBot {
	t.Helper()
	b := &fakeBot{}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rest, _ := strings.CutPrefix(r.URL.Path, "/bot")
		token, method, _ := strings.Cut(rest, "/")
		if token != apiTGToken {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
			return
		}
		switch method {
		case "getMe":
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":42,"first_name":"Home Media","username":"home_bot"}}`)
		case "sendMessage":
			var in struct {
				Text string `json:"text"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			b.mu.Lock()
			b.sent = append(b.sent, in.Text)
			b.mu.Unlock()
			_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
		case "getUpdates":
			b.mu.Lock()
			out := strings.Join(b.updates, ",")
			b.updates = nil
			b.mu.Unlock()
			if out == "" {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(20 * time.Millisecond):
				}
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":[`+out+`]}`)
		}
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *fakeBot) queue(update string) {
	b.mu.Lock()
	b.updates = append(b.updates, update)
	b.mu.Unlock()
}

func (b *fakeBot) messages() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.sent...)
}

func newTelegramEnv(t *testing.T) (*libEnv, *notify.Service, *fakeBot) {
	t.Helper()
	bot := newFakeBot(t)
	var svc *notify.Service
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		svc = &notify.Service{DB: db, Users: d.Users, Secret: []byte("s"), PublicURL: "https://antresol.example",
			TG: &telegram.Client{Base: bot.srv.URL}, TelegramGap: -1,
			Mail: func(context.Context) (mailer.Config, bool) {
				return mailer.Config{Host: "smtp.example", From: "me@example.com"}, true
			}}
		d.Notify = svc
	})
	return e, svc, bot
}

func TestTelegramBotSettingsAreTheAdminsAndNeverShowTheToken(t *testing.T) {
	e, _, _ := newTelegramEnv(t)
	_, mom := e.profile(`{"name":"Мама"}`)
	// the bot is the administrator's
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/settings/telegram", ""},
		{"PUT", "/api/settings/telegram", `{"token":"` + apiTGToken + `"}`},
		{"POST", "/api/settings/telegram/check", ""},
		{"DELETE", "/api/settings/telegram", ""},
	} {
		if w := e.do(c.method, c.path, c.body, mom); w.Code != http.StatusForbidden {
			t.Fatalf("%s %s by a profile: %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
	if w := e.do("GET", "/api/settings/telegram", "", e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"set":false`) {
		t.Fatalf("no bot yet: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/settings/telegram/check", "", e.admin); w.Code != http.StatusConflict {
		t.Fatalf("check without a token: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/settings/telegram", `{"token":"not a token"}`, e.admin); w.Code != http.StatusBadRequest {
		t.Fatalf("not a token: %d %s", w.Code, w.Body)
	}
	w := e.do("PUT", "/api/settings/telegram", `{"token":"999:unknown-token"}`, e.admin)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Telegram не принял токен") {
		t.Fatalf("a token Telegram refuses: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/settings/telegram", `{"token":"999:unknown-token"}`, e.admin, "Accept-Language", "en"); !strings.Contains(w.Body.String(), "didn’t accept the token") {
		t.Fatalf("in English: %s", w.Body)
	}
	w = e.do("PUT", "/api/settings/telegram", `{"token":"`+apiTGToken+`"}`, e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"username":"home_bot"`) || !strings.Contains(w.Body.String(), `"set":true`) {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}
	for _, c := range []struct{ method, path string }{
		{"PUT", "/api/settings/telegram"}, {"GET", "/api/settings/telegram"}, {"POST", "/api/settings/telegram/check"},
	} {
		body := ""
		if c.method == "PUT" {
			body = `{"token":"` + apiTGToken + `"}`
		}
		if w := e.do(c.method, c.path, body, e.admin); w.Code != 200 || strings.Contains(w.Body.String(), "test-token") {
			t.Fatalf("%s %s: %d — the token must never come back: %s", c.method, c.path, w.Code, w.Body)
		}
	}
	w = e.do("DELETE", "/api/settings/telegram", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"set":false`) {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
}

func TestTelegramLinkingFromTheProfile(t *testing.T) {
	e, svc, bot := newTelegramEnv(t)
	_, mom := e.profile(`{"name":"Мама"}`)
	_, kid := e.profile(`{"name":"Вася","isKid":true}`)
	kidID := func() int64 {
		var id int64
		e.db.QueryRow(`SELECT id FROM users WHERE name='Вася'`).Scan(&id)
		return id
	}()

	// no bot yet: nothing to connect to
	got := decodeJSON[notifyOut](t, e.do("GET", "/api/me/notify", "", mom).Body.Bytes())
	if got.TelegramReady || got.TelegramLinked {
		t.Fatalf("without a bot: %+v", got)
	}
	if w := e.do("POST", "/api/me/notify/telegram/link", "", mom); w.Code != http.StatusConflict {
		t.Fatalf("link without a bot: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/settings/telegram", `{"token":"`+apiTGToken+`"}`, e.admin); w.Code != 200 {
		t.Fatalf("bot: %d %s", w.Code, w.Body)
	}
	got = decodeJSON[notifyOut](t, e.do("GET", "/api/me/notify", "", mom).Body.Bytes())
	if !got.TelegramReady || got.TelegramBot != "home_bot" || got.TelegramLinked || !got.TelegramOn || !got.EmailOn {
		t.Fatalf("with a bot: %+v", got)
	}

	// kids do not link, on their own or through the admin
	for _, c := range []struct {
		method, path string
		who          *http.Cookie
	}{
		{"POST", "/api/me/notify/telegram/link", kid},
		{"DELETE", "/api/me/notify/telegram", kid},
		{"POST", "/api/users/" + itoa(kidID) + "/notify/telegram/link", e.admin},
	} {
		if w := e.do(c.method, c.path, "", c.who); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s: %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
	if p, _ := svc.Prefs(context.Background(), kidID); p.TelegramLinked {
		t.Fatal("a kid linked Telegram")
	}

	// the code and the link
	w := e.do("POST", "/api/me/notify/telegram/link", "", mom, "Accept-Language", "en")
	var link notify.TelegramLink
	if err := json.Unmarshal(w.Body.Bytes(), &link); err != nil || w.Code != 200 || link.Code == "" ||
		link.URL != "https://t.me/home_bot?start="+link.Code || link.Bot != "home_bot" || !link.Expires.After(time.Now()) {
		t.Fatalf("link: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("a one-time code must not be cached")
	}

	// the person presses «Start»: the server's poller finds the chat
	bot.queue(`{"update_id":5,"message":{"message_id":1,"from":{"id":7,"first_name":"Мама","username":"mama_tg","language_code":"ru"},"chat":{"id":7,"type":"private"},"text":"/start ` + link.Code + `"}}`)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.RunTelegram(ctx); close(done) }()
	defer func() { stop(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got = decodeJSON[notifyOut](t, e.do("GET", "/api/me/notify", "", mom).Body.Bytes())
		if got.TelegramLinked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the chat was not linked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.TelegramName != "@mama_tg" || !got.TelegramOn {
		t.Fatalf("linked: %+v", got)
	}
	if m := bot.messages(); len(m) != 1 || !strings.Contains(m[0], "Мама") {
		t.Fatalf("confirmation: %v", m)
	}

	// the channels: a client that does not know them leaves them as they are
	w = e.do("PUT", "/api/me/notify", `{"episodes":true,"telegramOn":false}`, mom)
	got = decodeJSON[notifyOut](t, w.Body.Bytes())
	if w.Code != 200 || got.TelegramOn || !got.EmailOn || !got.Episodes || !got.TelegramLinked {
		t.Fatalf("telegram off: %d %s", w.Code, w.Body)
	}
	w = e.do("PUT", "/api/me/notify", `{"episodes":true,"email":"mama@example.com"}`, mom)
	got = decodeJSON[notifyOut](t, w.Body.Bytes())
	if got.TelegramOn || !got.EmailOn {
		t.Fatalf("an old client's save changed the channels: %s", w.Body)
	}
	w = e.do("PUT", "/api/me/notify", `{"episodes":true,"emailOn":false,"telegramOn":true}`, mom)
	got = decodeJSON[notifyOut](t, w.Body.Bytes())
	if got.EmailOn || !got.TelegramOn {
		t.Fatalf("channels: %s", w.Body)
	}

	// «Отключить»
	w = e.do("DELETE", "/api/me/notify/telegram", "", mom)
	got = decodeJSON[notifyOut](t, w.Body.Bytes())
	if w.Code != 200 || got.TelegramLinked || got.TelegramName != "" {
		t.Fatalf("unlink: %d %s", w.Code, w.Body)
	}
}

// A profile that becomes a kid has no Telegram: the chat is let go when the
// administrator makes it one, and the administrator can let go of a kid's
// chat that is still there.
func TestAKidHasNoTelegramChat(t *testing.T) {
	e, svc, _ := newTelegramEnv(t)
	adult, _ := e.profile(`{"name":"Дядя"}`)
	_, mom := e.profile(`{"name":"Мама"}`)
	linkRow := func(id int64) {
		t.Helper()
		if _, err := e.db.Exec(`INSERT INTO notify_prefs(user_id, telegram_chat, telegram_name, telegram_on) VALUES(?,?,?,1)
			ON CONFLICT(user_id) DO UPDATE SET telegram_chat=excluded.telegram_chat, telegram_name=excluded.telegram_name`, id, 500+id, "@x"); err != nil {
			t.Fatal(err)
		}
	}
	linked := func(id int64) bool {
		t.Helper()
		p, err := svc.Prefs(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		return p.TelegramLinked
	}

	// becoming a kid lets go of the chat
	linkRow(adult)
	if !linked(adult) {
		t.Fatal("setup")
	}
	if w := e.do("PATCH", "/api/users/"+itoa(adult), `{"name":"Отец"}`, e.admin); w.Code != 200 || !linked(adult) {
		t.Fatalf("a rename must keep the chat: %d %s", w.Code, w.Body)
	}
	if w := e.do("PATCH", "/api/users/"+itoa(adult), `{"isKid":true}`, e.admin); w.Code != 200 {
		t.Fatalf("make a kid: %d %s", w.Code, w.Body)
	}
	if linked(adult) {
		t.Fatal("a profile that became a kid keeps its Telegram chat")
	}

	// a chat that is still there (set some other way): the administrator lets go
	linkRow(adult)
	if w := e.do("DELETE", "/api/users/"+itoa(adult)+"/notify/telegram", "", mom); w.Code != http.StatusForbidden || !linked(adult) {
		t.Fatalf("a profile that is not an administrator: %d %s", w.Code, w.Body)
	}
	w := e.do("DELETE", "/api/users/"+itoa(adult)+"/notify/telegram", "", e.admin)
	got := decodeJSON[notifyOut](t, w.Body.Bytes())
	if w.Code != 200 || got.TelegramLinked || linked(adult) {
		t.Fatalf("admin unlink: %d %s", w.Code, w.Body)
	}

	// an adult's chat is its own: not even the administrator reaches it
	var momID int64
	e.db.QueryRow(`SELECT id FROM users WHERE name='Мама'`).Scan(&momID)
	linkRow(momID)
	if w := e.do("DELETE", "/api/users/"+itoa(momID)+"/notify/telegram", "", e.admin); w.Code != http.StatusForbidden || !linked(momID) {
		t.Fatalf("an adult's chat: %d %s", w.Code, w.Body)
	}
}
