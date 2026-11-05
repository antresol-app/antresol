package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/mailer"
	"github.com/antresol-app/antresol/internal/notify"
)

type notifyOut struct {
	notify.Prefs
	Topics      []string `json:"topics"`
	MailReady   bool     `json:"mailReady"`
	PublicLinks bool     `json:"publicLinks"`
}

type fakeMail struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (f *fakeMail) send(_ context.Context, _ mailer.Config, m mailer.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeMail) last() mailer.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[len(f.sent)-1]
}

func newNotifyEnv(t *testing.T) (*libEnv, *notify.Service, *fakeMail) {
	fm := &fakeMail{}
	var svc *notify.Service
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		svc = &notify.Service{DB: db, Users: d.Users, Secret: []byte("s"), Send: fm.send, PublicURL: "https://antresol.example",
			Mail: func(context.Context) (mailer.Config, bool) {
				return mailer.Config{Host: "smtp.example", From: "me@example.com"}, true
			}}
		d.Notify = svc
	})
	return e, svc, fm
}

func TestNotifyPrefsOwnAndKids(t *testing.T) {
	e, _, fm := newNotifyEnv(t)
	momID, mom := e.profile(`{"name":"Мама"}`)
	kidID, kid := e.profile(`{"name":"Вася","isKid":true}`)
	_ = momID
	w := e.do("GET", "/api/me/notify", "", mom)
	got := decodeJSON[notifyOut](t, w.Body.Bytes())
	if w.Code != 200 || got.Email != "" || got.Episodes || !got.QuietHours || !got.MailReady || !got.PublicLinks ||
		!slices.Contains(got.Topics, "episodes") || slices.Contains(got.Topics, "downloads") {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	w = e.do("PUT", "/api/me/notify", `{"email":"mama@example.com","episodes":true,"downloads":true,"quietHours":false,"lang":"en"}`, mom)
	got = decodeJSON[notifyOut](t, w.Body.Bytes())
	if w.Code != 200 || got.Email != "mama@example.com" || !got.Episodes || got.Downloads || got.QuietHours || got.Lang != "en" || got.Verified {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/me/notify", `{"email":"not an address"}`, mom); w.Code != http.StatusBadRequest {
		t.Fatalf("bad address: %d %s", w.Code, w.Body)
	}
	// the verification letter and its code
	if w := e.do("POST", "/api/me/notify/verify", "", mom); w.Code != http.StatusNoContent {
		t.Fatalf("verify: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/me/notify/verify", "", mom); w.Code != http.StatusTooManyRequests {
		t.Fatalf("verify again: %d %s", w.Code, w.Body)
	}
	code := regexp.MustCompile(`\b\d{6}\b`).FindString(fm.last().Body)
	if w := e.do("POST", "/api/me/notify/confirm", `{"code":"12"}`, mom); w.Code != http.StatusBadRequest {
		t.Fatalf("wrong code: %d %s", w.Code, w.Body)
	}
	w = e.do("POST", "/api/me/notify/confirm", `{"code":"`+code+`"}`, mom)
	if got := decodeJSON[notifyOut](t, w.Body.Bytes()); w.Code != 200 || !got.Verified {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	// a kid's are the admin's
	if w := e.do("PUT", "/api/me/notify", `{"email":"kid@example.com","episodes":true}`, kid); w.Code != http.StatusForbidden {
		t.Fatalf("kid put: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/me/notify/verify", "", kid); w.Code != http.StatusForbidden {
		t.Fatalf("kid verify: %d %s", w.Code, w.Body)
	}
	path := "/api/users/" + itoa(kidID) + "/notify"
	if w := e.do("PUT", path, `{"email":"kid@example.com","episodes":true}`, kid); w.Code != http.StatusForbidden {
		t.Fatalf("kid via users: %d", w.Code)
	}
	w = e.do("PUT", path, `{"email":"kid@example.com","episodes":true,"downloads":true}`, e.admin)
	got = decodeJSON[notifyOut](t, w.Body.Bytes())
	if w.Code != 200 || !got.Episodes || got.Downloads {
		t.Fatalf("admin put: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", path+"/verify", "", e.admin); w.Code != http.StatusNoContent || fm.last().To != "kid@example.com" {
		t.Fatalf("admin verify: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me/notify", "", kid); w.Code != 200 || !strings.Contains(w.Body.String(), "kid@example.com") {
		t.Fatalf("kid reads: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/users/99999/notify", "", e.admin); w.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", w.Code)
	}
	// an adult's are its own: not even an admin reads or changes them
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/users/" + itoa(momID) + "/notify", ""},
		{"PUT", "/api/users/" + itoa(momID) + "/notify", `{"email":"evil@example.com"}`},
		{"POST", "/api/users/" + itoa(momID) + "/notify/verify", ""},
		{"POST", "/api/users/" + itoa(momID) + "/notify/confirm", `{"code":"123456"}`},
	} {
		if w := e.do(c.method, c.path, c.body, e.admin); w.Code != http.StatusForbidden {
			t.Fatalf("%s %s: %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
	w = e.do("GET", "/api/me/notify", "", mom)
	if got := decodeJSON[notifyOut](t, w.Body.Bytes()); got.Email != "mama@example.com" {
		t.Fatalf("an adult's address changed: %s", w.Body)
	}
	// the admin's own may have one more topic (another build only)
	w = e.do("GET", "/api/me/notify", "", e.admin)
	if got := decodeJSON[notifyOut](t, w.Body.Bytes()); slices.Contains(got.Topics, "downloads") == libraryEdition {
		t.Fatalf("admin topics %v", got.Topics)
	}
}

func TestNotifyLinksWithoutLogin(t *testing.T) {
	e, svc, fm := newNotifyEnv(t)
	momID, mom := e.profile(`{"name":"Мама"}`)
	e.do("PUT", "/api/me/notify", `{"email":"mama@example.com","episodes":true,"available":true,"lang":"ru"}`, mom)
	e.do("POST", "/api/me/notify/verify", "", mom)
	link := regexp.MustCompile(`https://antresol\.example(/api/notify/link/verify\?t=\S+)`).FindStringSubmatch(fm.last().Body)
	if link == nil {
		t.Fatal(fm.last().Body)
	}
	w := e.do("GET", link[1], "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Адрес подтверждён") || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("verify link: %d %s", w.Code, w.Body)
	}
	if p, _ := svc.Prefs(context.Background(), momID); !p.Verified {
		t.Fatal("not confirmed")
	}
	if w := e.do("GET", "/api/notify/link/verify?t=bogus", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bogus: %d", w.Code)
	}

	tok := svc.UnsubscribeToken(momID, notify.Episodes, "mama@example.com")
	path := "/api/notify/link/unsubscribe?t=" + url.QueryEscape(tok)
	// GET only asks (mail scanners open links)
	w = e.do("GET", path, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<form method="post"`) {
		t.Fatalf("unsubscribe page: %d %s", w.Code, w.Body)
	}
	if p, _ := svc.Prefs(context.Background(), momID); !p.Episodes {
		t.Fatal("GET unsubscribed")
	}
	// the page's form, from another site's mail client (one-click): no CSRF refusal
	r := httptest.NewRequest("POST", path, strings.NewReader("List-Unsubscribe=One-Click"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Origin", "https://mail.example")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Новые серии") {
		t.Fatalf("one-click: %d %s", rec.Code, rec.Body)
	}
	if p, _ := svc.Prefs(context.Background(), momID); p.Episodes || !p.Available {
		t.Fatalf("after unsubscribe %+v", p)
	}
	// other POSTs keep their CSRF check
	r = httptest.NewRequest("PUT", "/api/me/notify", strings.NewReader("a=b"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(mom)
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("form PUT: %d", rec.Code)
	}
}
