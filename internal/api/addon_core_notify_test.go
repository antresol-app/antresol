package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/mailer"
	"github.com/antresol-app/antresol/internal/notify"
)

// notifyTopics are the neutral addons' topics: "dl" an admins one and an
// adults one.
func notifyTopics(context.Context) []notify.AddonTopic {
	return []notify.AddonTopic{
		{Addon: "dl", Topic: "downloads", Title: map[string]string{"ru": "Загрузки", "en": "Downloads"}, Audience: "admins"},
		{Addon: "dl", Topic: "news", Title: map[string]string{"ru": "Новости", "en": "News"}, Audience: "adults"},
	}
}

type coreNotify struct {
	*addonEnv
	svc *notify.Service
}

// newCoreNotify: the addon env with a notify service; the addon "dl" has
// the notify permission, "hello" does not.
func newCoreNotify(t *testing.T) *coreNotify {
	c := &coreNotify{}
	c.addonEnv = newAddonEnv(t, func(d *Deps) {
		c.svc = &notify.Service{DB: d.DB, Users: d.Users, Secret: []byte("s"),
			Mail: func(context.Context) (mailer.Config, bool) { return mailer.Config{}, false }}
		c.svc.SetAddonTopics(notifyTopics)
		d.Notify = c.svc
	})
	c.fake.mu.Lock()
	in := c.fake.installed["hello"]
	in.ID, in.Manifest.ID, in.Manifest.Permissions = "dl", "dl", []string{"notify"}
	in.Manifest.Notify = []addons.NotifyTopic{{Topic: "downloads", Audience: "admins"}, {Topic: "news", Audience: "adults"}}
	c.fake.installed["dl"], c.fake.tokens["tok-dl"], c.fake.status["dl"] = in, "dl", addons.StatusRunning
	c.fake.mu.Unlock()
	return c
}

func (c *coreNotify) post(tok, body string) *testResponse {
	w := c.do("POST", "/api/addon-core/v1/notify", body, nil, "Authorization", "Bearer "+tok, "Content-Type", "application/json")
	return &testResponse{code: w.Code, body: w.Body.String()}
}

type testResponse struct {
	code int
	body string
}

func (c *coreNotify) uid(name string) int64 {
	c.t.Helper()
	var id int64
	if err := c.db.QueryRow(`SELECT id FROM users WHERE name=?`, name).Scan(&id); err != nil {
		c.t.Fatal(err)
	}
	return id
}

// prefs gives the profile name an address and turns the dl topics on.
func (c *coreNotify) prefs(name, email string, topics ...string) {
	c.t.Helper()
	u, err := c.store.Get(context.Background(), c.uid(name))
	if err != nil {
		c.t.Fatal(err)
	}
	p := notify.Prefs{Email: email}
	for _, t := range topics {
		p.AddonTopics = append(p.AddonTopics, notify.AddonPref{Addon: "dl", Topic: t, On: true})
	}
	if _, err := c.svc.SetPrefs(context.Background(), u, p); err != nil {
		c.t.Fatal(err)
	}
}

func TestAddonCoreNotify(t *testing.T) {
	c := newCoreNotify(t)
	c.prefs("Папа", "a@example.com", "downloads", "news")
	c.prefs("Маша", "m@example.com", "news")
	if r := c.post("tok-dl", `{"topic":"downloads","key":"k1","subject":{"ru":"Готово","en":"Done"},"link":"/item/3"}`); r.code != http.StatusAccepted || r.body != `{"queued":1}`+"\n" {
		t.Fatalf("admins: %d %q", r.code, r.body)
	}
	if r := c.post("tok-dl", `{"topic":"news","key":"k2","subject":{"ru":"Новость"},"body":{"ru":"Подробно"},"poster":"https://image.tmdb.org/t/p/a.jpg"}`); r.code != http.StatusAccepted || !strings.Contains(r.body, `"queued":2`) {
		t.Fatalf("adults: %d %s", r.code, r.body)
	}
	if r := c.post("tok-hello", `{"topic":"news","key":"k","subject":{"ru":"x"}}`); r.code != http.StatusForbidden {
		t.Fatalf("no permission: %d %s", r.code, r.body)
	}
	kid := c.uid("Вася")
	for _, tc := range []struct{ body, msg string }{
		{`{"topic":"nope","key":"k","subject":{"ru":"x"}}`, "Расширение не объявило эту тему уведомлений"},
		{`{"topic":"downloads","audience":"adults","key":"k","subject":{"ru":"x"}}`, "Неверная аудитория уведомления"},
		{`{"topic":"news","userIds":[` + itoa(kid) + `],"key":"k","subject":{"ru":"x"}}`, "Детскому профилю это недоступно"},
		{`{"topic":"news","key":"k","subject":{"ru":"x"},"link":"https://x"}`, "Ссылка уведомления должна вести на страницу приложения"},
	} {
		if r := c.post("tok-dl", tc.body); r.code != http.StatusBadRequest || !strings.Contains(r.body, tc.msg) {
			t.Fatalf("%s: %d %s", tc.body, r.code, r.body)
		}
	}
	long := strings.Repeat("я", 201)
	for _, body := range []string{
		`{"topic":"news","key":"","subject":{"ru":"x"}}`,
		`{"topic":"news","key":"` + strings.Repeat("k", 201) + `","subject":{"ru":"x"}}`,
		`{"topic":"news","key":"k","subject":{"ru":"` + long + `"}}`,
		`{"topic":"news","key":"k","subject":{}}`,
		`{"topic":"news","key":"k","subject":{"ru":"  ","en":"\t"}}`,
		`{"topic":"news","key":"  ","subject":{"ru":"x"}}`,
		`{"topic":"news","key":"k","subject":{"ru":"x"},"body":{"en":"` + strings.Repeat("b", 2001) + `"}}`,
	} {
		if r := c.post("tok-dl", body); r.code != http.StatusBadRequest {
			t.Fatalf("limits %.60s: %d %s", body, r.code, r.body)
		}
	}
	// exactly 200 characters (not bytes) is fine
	if r := c.post("tok-dl", `{"topic":"news","key":"k3","subject":{"ru":"`+strings.Repeat("я", 200)+`"}}`); r.code != http.StatusAccepted {
		t.Fatalf("200 chars: %d %s", r.code, r.body)
	}
	var n int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM notify_events WHERE topic LIKE 'addon:dl:%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 { // k1 (admin), k2 (admin, adult), k3 (admin, adult)
		t.Fatalf("events %d", n)
	}
}

func TestAddonCoreNotify_NoService(t *testing.T) {
	e := newAddonEnv(t)
	e.fake.mu.Lock()
	in := e.fake.installed["hello"]
	in.Manifest.Permissions = []string{"notify"}
	e.fake.installed["hello"] = in
	e.fake.mu.Unlock()
	w := e.do("POST", "/api/addon-core/v1/notify", `{"topic":"x","key":"k","subject":{"ru":"x"}}`, nil, "Authorization", "Bearer tok-hello", "Content-Type", "application/json")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestNotifyPrefs_AddonTopics(t *testing.T) {
	e, svc, _ := newNotifyEnv(t)
	svc.SetAddonTopics(notifyTopics)
	momID, mom := e.profile(`{"name":"Мама"}`)
	kidID, _ := e.profile(`{"name":"Вася","isKid":true}`)
	type out struct {
		Topics      []string           `json:"topics"`
		AddonTopics []notify.AddonPref `json:"addonTopics"`
	}
	w := e.do("GET", "/api/me/notify", "", e.admin, "Accept-Language", "en")
	got := decodeJSON[out](t, w.Body.Bytes())
	if w.Code != 200 || len(got.AddonTopics) != 2 || got.AddonTopics[0].Title != "Downloads" || got.AddonTopics[0].On ||
		got.AddonTopics[1].Topic != "news" || !strings.Contains(strings.Join(got.Topics, ","), "addon:dl:downloads") {
		t.Fatalf("admin: %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/me/notify", "", mom)
	if got := decodeJSON[out](t, w.Body.Bytes()); len(got.AddonTopics) != 1 || got.AddonTopics[0].Title != "Новости" {
		t.Fatalf("adult: %s", w.Body)
	}
	w = e.do("GET", "/api/users/"+itoa(kidID)+"/notify", "", e.admin)
	if got := decodeJSON[out](t, w.Body.Bytes()); w.Code != 200 || got.AddonTopics == nil || len(got.AddonTopics) != 0 || !strings.Contains(w.Body.String(), `"addonTopics":[]`) {
		t.Fatalf("kid: %d %s", w.Code, w.Body)
	}
	// a PUT toggles the rows it lists, leaves the others
	w = e.do("PUT", "/api/me/notify", `{"email":"a@example.com","addonTopics":[{"addon":"dl","topic":"news","on":true}]}`, e.admin)
	got = decodeJSON[out](t, w.Body.Bytes())
	if w.Code != 200 || got.AddonTopics[0].On || !got.AddonTopics[1].On {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	w = e.do("PUT", "/api/me/notify", `{"email":"a@example.com","addonTopics":[{"addon":"dl","topic":"downloads","on":true}]}`, e.admin)
	got = decodeJSON[out](t, w.Body.Bytes())
	if !got.AddonTopics[0].On || !got.AddonTopics[1].On {
		t.Fatalf("second put: %s", w.Body)
	}
	// without addonTopics (the core web's sheet) nothing changes
	w = e.do("PUT", "/api/me/notify", `{"email":"a@example.com","episodes":true}`, e.admin)
	if got := decodeJSON[out](t, w.Body.Bytes()); !got.AddonTopics[0].On || !got.AddonTopics[1].On {
		t.Fatalf("unlisted changed: %s", w.Body)
	}
	// an adult cannot turn an admins topic on
	w = e.do("PUT", "/api/me/notify", `{"email":"m@example.com","addonTopics":[{"addon":"dl","topic":"downloads","on":true}]}`, mom)
	if p, _ := svc.Prefs(context.Background(), momID); w.Code != 200 || p.On("addon:dl:downloads") {
		t.Fatalf("adult admins topic: %d %s", w.Code, w.Body)
	}
}
