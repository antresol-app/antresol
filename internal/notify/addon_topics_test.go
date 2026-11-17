package notify

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
)

// Neutral addon ids: "dl" declares an admins topic and an adults one,
// "hello" an adults one.
const (
	dlDownloads = "addon:dl:downloads"
	dlNews      = "addon:dl:news"
	helloGreet  = "addon:hello:greet"
)

func (f *fixture) addonTopics() {
	f.svc.SetAddonTopics(func(context.Context) []AddonTopic {
		return []AddonTopic{
			// out of id order on purpose: letters sort by addon id
			{Addon: "hello", Topic: "greet", Title: map[string]string{"ru": "Приветы", "en": "Greetings"}, Audience: "adults"},
			{Addon: "dl", Topic: "downloads", Title: map[string]string{"ru": "Загрузки", "en": "Downloads"}, Audience: "admins"},
			{Addon: "dl", Topic: "news", Title: map[string]string{"ru": "Новости", "en": "News"}, Audience: "adults"},
		}
	})
}

func on(keys ...string) []AddonPref {
	out := make([]AddonPref, 0, len(keys))
	for _, k := range keys {
		a, t, _ := ParseAddonTopic(k)
		out = append(out, AddonPref{Addon: a, Topic: t, On: true})
	}
	return out
}

func (f *fixture) events(t *testing.T, topic string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM notify_events WHERE topic=?`, topic).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// queue queues e and fails unless it went to want profiles.
func (f *fixture) queue(t *testing.T, e AddonEvent, want int) {
	t.Helper()
	if n, err := f.svc.QueueAddon(f.ctx, e); err != nil || n != want {
		t.Fatalf("queue %+v: %d %v (want %d)", e, n, err, want)
	}
}

// count is the one number query answers.
func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func subj(s string) map[string]string { return map[string]string{"ru": s, "en": s + " (en)"} }

func TestQueueAddon_AudienceAdmins(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	a1, a2 := f.user(t, "Админ", true, false), f.user(t, "Админ 2", true, false)
	u := f.user(t, "Аня", false, false)
	for i, x := range []auth.User{a1, a2, u} {
		f.subscribe(t, x, "p"+string(rune('a'+i))+"@example.com", Prefs{AddonTopics: on(dlDownloads)})
	}
	if p, _ := f.svc.Prefs(f.ctx, u.ID); p.On(dlDownloads) {
		t.Fatal("an adult turned an admins topic on")
	}
	n, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "downloads", Key: "k1", Subject: subj("Готово")})
	if err != nil || n != 2 {
		t.Fatalf("queued %d %v", n, err)
	}
	if f.pending(t, a1.ID) != 1 || f.pending(t, a2.ID) != 1 || f.pending(t, u.ID) != 0 {
		t.Fatal("wrong recipients")
	}
}

func TestQueueAddon_KidRefused(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	u := f.user(t, "Аня", false, false)
	kid := f.user(t, "Вася", false, true)
	f.subscribe(t, u, "anya@example.com", Prefs{AddonTopics: on(dlNews)})
	_, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "news", Key: "k", UserIDs: []int64{u.ID, kid.ID}, Subject: subj("x")})
	if !errors.Is(err, ErrKidRecipient) {
		t.Fatalf("err = %v", err)
	}
	if n := f.events(t, dlNews); n != 0 {
		t.Fatalf("queued %d", n)
	}
	// a kid never even has the topics
	for _, k := range f.svc.TopicsFor(f.ctx, kid) {
		if strings.HasPrefix(k, "addon:") {
			t.Fatalf("kid topics %v", f.svc.TopicsFor(f.ctx, kid))
		}
	}
}

func TestQueueAddon_UndeclaredTopic(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	for _, e := range []AddonEvent{{Addon: "dl", Topic: "nope"}, {Addon: "other", Topic: "news"}, {Addon: "hello", Topic: "news"}} {
		e.Key, e.Subject = "k", subj("x")
		if _, err := f.svc.QueueAddon(f.ctx, e); !errors.Is(err, ErrNoTopic) {
			t.Fatalf("%+v: %v", e, err)
		}
	}
}

func TestQueueAddon_NarrowOnly(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	a := f.user(t, "Админ", true, false)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, a, "a@example.com", Prefs{AddonTopics: on(dlDownloads, dlNews)})
	f.subscribe(t, u, "u@example.com", Prefs{AddonTopics: on(dlNews)})
	if _, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "downloads", Audience: "adults", Key: "k", Subject: subj("x")}); !errors.Is(err, ErrBadAudience) {
		t.Fatalf("widened: %v", err)
	}
	if _, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "news", Audience: "everyone", Key: "k", Subject: subj("x")}); !errors.Is(err, ErrBadAudience) {
		t.Fatalf("unknown audience: %v", err)
	}
	if _, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "news", Audience: "admins", UserIDs: []int64{a.ID}, Key: "k", Subject: subj("x")}); !errors.Is(err, ErrBadAudience) {
		t.Fatalf("both userIds and audience: %v", err)
	}
	// an admins topic never reaches a named adult
	if _, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "downloads", UserIDs: []int64{u.ID}, Key: "k", Subject: subj("x")}); !errors.Is(err, ErrBadAudience) {
		t.Fatalf("adult on admins topic: %v", err)
	}
	n, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "news", Audience: "admins", Key: "k", Subject: subj("x")})
	if err != nil || n != 1 || f.pending(t, a.ID) != 1 || f.pending(t, u.ID) != 0 {
		t.Fatalf("narrowed: %d %v", n, err)
	}
	n, err = f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "news", Key: "k2", Subject: subj("x")})
	if err != nil || n != 2 {
		t.Fatalf("default audience: %d %v", n, err)
	}
}

func TestQueueAddon_LinkRules(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "u@example.com", Prefs{AddonTopics: on(dlNews)})
	for _, l := range []string{"https://x", "//x", "x/y", `/a\b`, "/a b", "javascript:alert(1)"} {
		if _, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "news", Key: "k", Link: l, Subject: subj("x")}); !errors.Is(err, ErrBadLink) {
			t.Fatalf("%q: %v", l, err)
		}
	}
	if n, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "news", Key: "k", Link: "/item/3", Subject: subj("x")}); err != nil || n != 1 {
		t.Fatalf("/item/3: %d %v", n, err)
	}
	var raw string
	if err := f.db.QueryRow(`SELECT data_json FROM notify_events WHERE topic=?`, dlNews).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"path":"/item/3"`) || !strings.Contains(raw, `"subject":{`) {
		t.Fatalf("stored %s", raw)
	}
}

func TestQueueAddon_KeyDedup(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "u@example.com", Prefs{AddonTopics: on(dlNews, helloGreet)})
	for range 2 {
		if _, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "news", Key: "same", Subject: subj("x")}); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "news", Key: "same", Subject: subj("x")}); n != 0 {
		t.Fatalf("dup queued %d", n)
	}
	// another addon's same key is its own
	if n, _ := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "hello", Topic: "greet", Key: "same", Subject: subj("x")}); n != 1 {
		t.Fatalf("other addon's key: %d", n)
	}
	if f.pending(t, u.ID) != 2 {
		t.Fatalf("pending %d", f.pending(t, u.ID))
	}
}

func TestAddonPref_LegacyColumnDefault(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	a := f.user(t, "Админ", true, false)
	f.subscribe(t, a, "a@example.com", Prefs{})
	if _, err := f.db.Exec(`UPDATE notify_prefs SET downloads=1 WHERE user_id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.Prefs(f.ctx, a.ID)
	if err != nil || !p.On(dlDownloads) || p.On(dlNews) {
		t.Fatalf("fallback: %v %v %v", p.On(dlDownloads), p.On(dlNews), err)
	}
	if !f.svc.Wants(f.ctx, dlDownloads) {
		t.Fatal("wanting misses the fallback")
	}
	if _, err := f.db.Exec(`INSERT INTO notify_addon_prefs(user_id, addon_id, topic, enabled) VALUES(?, 'dl', 'downloads', 0)`, a.ID); err != nil {
		t.Fatal(err)
	}
	if p, _ := f.svc.Prefs(f.ctx, a.ID); p.On(dlDownloads) {
		t.Fatal("a row with 0 must win")
	}
}

// An admin's save keeps the carried-over column (D1): SetPrefs never
// writes it, so the addon topic without a row still reads the old choice;
// the core's prefs have no topic of that name.
func TestAddonPref_SaveKeepsLegacyColumn(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	a := f.user(t, "Админ", true, false)
	f.subscribe(t, a, "a@example.com", Prefs{})
	if _, err := f.db.Exec(`UPDATE notify_prefs SET downloads=1 WHERE user_id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.SetPrefs(f.ctx, a, Prefs{Email: "a@example.com", Episodes: true})
	if err != nil || !p.On(dlDownloads) {
		t.Fatalf("after save: %v %v", p.On(dlDownloads), err)
	}
	var col int
	if err := f.db.QueryRow(`SELECT downloads FROM notify_prefs WHERE user_id=?`, a.ID).Scan(&col); err != nil || col != 1 {
		t.Fatalf("column %d %v", col, err)
	}
	if b, _ := json.Marshal(p); strings.Contains(string(b), `"downloads"`) {
		t.Fatalf("prefs name the column: %s", b)
	}
	if slices.Contains(f.svc.TopicsFor(f.ctx, a), "downloads") {
		t.Fatal("a core topic of that name")
	}
}

func TestLetter_AddonSection(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	a := f.user(t, "Админ", true, false)
	f.subscribe(t, a, "a@example.com", Prefs{Lang: "en", AddonTopics: on(dlDownloads)})
	if _, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "downloads", Key: "k", Link: "/item/3",
		Subject: map[string]string{"ru": "Готово", "en": "Done"}, Body: map[string]string{"ru": "Всё хорошо", "en": "All good"}}); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(2 * time.Hour)
	f.flush(t)
	sent := f.out.all()
	if len(sent) != 1 {
		t.Fatalf("sent %d", len(sent))
	}
	m := sent[0]
	if m.Subject != "Done" || !strings.Contains(m.Body, "\nDownloads\n• Done\n  All good\n  https://antresol.example/item/3\n") {
		t.Fatalf("letter %q\n%s", m.Subject, m.Body)
	}
	if !strings.Contains(m.HTML, ">Downloads</h2>") || !strings.Contains(m.HTML, "All good") || !strings.Contains(m.Body, "“Downloads”") {
		t.Fatalf("html %s", m.HTML)
	}
	if f.pending(t, a.ID) != 0 {
		t.Fatal("not marked sent")
	}
}

func TestUnsubscribeAddonTopic(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	a := f.user(t, "Админ", true, false)
	f.subscribe(t, a, "a@example.com", Prefs{Episodes: true, AddonTopics: on(dlDownloads, dlNews)})
	f.queue(t, AddonEvent{Addon: "dl", Topic: "downloads", Key: "k", Subject: subj("x")}, 1)
	tok := f.svc.UnsubscribeToken(a.ID, dlDownloads, "a@example.com")
	info, err := f.svc.Unsubscribe(f.ctx, tok)
	if err != nil || info.Topic != dlDownloads {
		t.Fatalf("%+v %v", info, err)
	}
	if h := f.svc.TopicHeading(f.ctx, "en", info.Topic); h != "Downloads" {
		t.Fatalf("heading %q", h)
	}
	p, _ := f.svc.Prefs(f.ctx, a.ID)
	if p.On(dlDownloads) || !p.On(dlNews) || !p.Episodes {
		t.Fatalf("after: %+v", p)
	}
	if f.pending(t, a.ID) != 0 {
		t.Fatal("its events stayed")
	}
	if _, err := f.svc.Unsubscribe(f.ctx, f.svc.UnsubscribeToken(a.ID, "addon:dl:Bad|x", "a@example.com")); !errors.Is(err, ErrBadToken) {
		t.Fatalf("bad key: %v", err)
	}
}

func TestUnsubscribeAll_IncludesAddonTopics(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	a := f.user(t, "Админ", true, false)
	f.subscribe(t, a, "a@example.com", Prefs{Episodes: true, AddonTopics: on(dlNews, helloGreet)})
	if _, err := f.db.Exec(`UPDATE notify_prefs SET downloads=1 WHERE user_id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	f.queue(t, AddonEvent{Addon: "hello", Topic: "greet", Key: "k", Subject: subj("x")}, 1)
	if _, err := f.svc.Unsubscribe(f.ctx, f.svc.UnsubscribeToken(a.ID, TopicAll, "a@example.com")); err != nil {
		t.Fatal(err)
	}
	p, _ := f.svc.Prefs(f.ctx, a.ID)
	for _, k := range []string{dlNews, helloGreet, dlDownloads, Episodes} {
		if p.On(k) {
			t.Fatalf("%s still on", k)
		}
	}
	if f.pending(t, a.ID) != 0 {
		t.Fatal("events stayed")
	}
}

func TestQueueAddon_PosterRule(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "u@example.com", Prefs{AddonTopics: on(dlNews)})
	f.queue(t, AddonEvent{Addon: "dl", Topic: "news", Key: "a", Poster: "https://image.tmdb.org/t/p/w185/a.jpg", Subject: subj("x")}, 1)
	f.queue(t, AddonEvent{Addon: "dl", Topic: "news", Key: "b", Poster: "https://evil.example/a.jpg", Subject: subj("x")}, 1)
	rows, err := f.db.Query(`SELECT key, data_json FROM notify_events ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var k, d string
		if err := rows.Scan(&k, &d); err != nil {
			t.Fatal(err)
		}
		got[k] = d
	}
	if len(got) != 2 {
		t.Fatalf("%v", got)
	}
	for k, d := range got {
		hasTMDB := strings.Contains(d, "image.tmdb.org")
		if strings.HasSuffix(k, ":a") != hasTMDB || strings.Contains(d, "evil") {
			t.Fatalf("%s: %s", k, d)
		}
	}
}

func TestDigest_AddonSectionsAfterCore(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "u@example.com", Prefs{Available: true, Lang: "ru", AddonTopics: on(dlNews, helloGreet)})
	// queued in the opposite order of the letter's
	f.queue(t, AddonEvent{Addon: "hello", Topic: "greet", Key: "h", Subject: subj("Привет")}, 1)
	f.queue(t, AddonEvent{Addon: "dl", Topic: "news", Key: "n", Subject: subj("Новость")}, 1)
	if ok, err := f.svc.Enqueue(f.ctx, Event{UserID: u.ID, Topic: Available, Key: "av:1", Data: Data{Title: "Фильм"}}); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	f.clock = f.clock.Add(2 * time.Hour)
	f.flush(t)
	m := f.out.all()[0].Body
	iCore, iDl, iHello := strings.Index(m, "Фильм"), strings.Index(m, "\nНовости\n"), strings.Index(m, "\nПриветы\n")
	if iCore < 0 || iDl < 0 || iHello < 0 || !(iCore < iDl && iDl < iHello) {
		t.Fatalf("order %d %d %d\n%s", iCore, iDl, iHello, m)
	}
}

func TestDropAddon(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "u@example.com", Prefs{AddonTopics: on(dlNews, helloGreet)})
	f.queue(t, AddonEvent{Addon: "dl", Topic: "news", Key: "sent", Subject: subj("x")}, 1)
	if _, err := f.db.Exec(`UPDATE notify_events SET sent_at=1 WHERE key LIKE '%sent'`); err != nil {
		t.Fatal(err)
	}
	f.queue(t, AddonEvent{Addon: "dl", Topic: "news", Key: "unsent", Subject: subj("x")}, 1)
	f.queue(t, AddonEvent{Addon: "hello", Topic: "greet", Key: "unsent", Subject: subj("x")}, 1)
	if err := f.svc.DropAddon(f.ctx, "dl"); err != nil {
		t.Fatal(err)
	}
	rows := f.count(t, `SELECT COUNT(*) FROM notify_addon_prefs WHERE addon_id='dl'`)
	sent := f.count(t, `SELECT COUNT(*) FROM notify_events WHERE topic=? AND sent_at IS NOT NULL`, dlNews)
	unsentDl := f.count(t, `SELECT COUNT(*) FROM notify_events WHERE topic=? AND sent_at IS NULL`, dlNews)
	hello := f.count(t, `SELECT COUNT(*) FROM notify_events WHERE topic=?`, helloGreet)
	if rows != 0 || sent != 1 || unsentDl != 0 || hello != 1 {
		t.Fatalf("rows %d sent %d unsent %d hello %d", rows, sent, unsentDl, hello)
	}
	if helloRows := f.count(t, `SELECT COUNT(*) FROM notify_addon_prefs WHERE addon_id='hello'`); helloRows != 1 {
		t.Fatalf("hello rows %d", helloRows)
	}
}

func TestTopicsFor_AddonAudience(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	a := f.user(t, "Админ", true, false)
	u := f.user(t, "Аня", false, false)
	ta, tu := strings.Join(f.svc.TopicsFor(f.ctx, a), ","), strings.Join(f.svc.TopicsFor(f.ctx, u), ",")
	if !strings.Contains(ta, dlDownloads) || !strings.Contains(ta, dlNews) || !strings.Contains(ta, helloGreet) {
		t.Fatalf("admin %s", ta)
	}
	if strings.Contains(tu, dlDownloads) || !strings.Contains(tu, dlNews) {
		t.Fatalf("adult %s", tu)
	}
	if ps := f.svc.AddonPrefs(f.ctx, a, Prefs{}, "en"); len(ps) != 3 || ps[0].Addon != "dl" || ps[0].Title != "Downloads" || ps[2].Addon != "hello" {
		t.Fatalf("prefs %+v", ps)
	}
}

// More waiting lines of an addon that is now off than a digest reads at
// once never hold back the profile's other lines.
func TestDigest_OffAddonBacklogDoesNotBlock(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "u@example.com", Prefs{Available: true, AddonTopics: on(dlNews)})
	for i := range 501 {
		if n, err := f.svc.QueueAddon(f.ctx, AddonEvent{Addon: "dl", Topic: "news", Key: strconv.Itoa(i), Subject: subj("x")}); err != nil || n != 1 {
			t.Fatalf("queue %d: %d %v", i, n, err)
		}
	}
	// dl is switched off: only hello's topics are listed now
	f.svc.SetAddonTopics(func(context.Context) []AddonTopic {
		return []AddonTopic{{Addon: "hello", Topic: "greet", Title: map[string]string{"ru": "Приветы"}, Audience: "adults"}}
	})
	f.clock = f.clock.Add(time.Minute)
	if ok, err := f.svc.Enqueue(f.ctx, Event{UserID: u.ID, Topic: Available, Key: "av:1", Data: Data{Title: "Фильм"}}); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	f.clock = f.clock.Add(2 * time.Hour)
	f.flush(t)
	sent := f.out.all()
	if len(sent) != 1 || !strings.Contains(sent[0].Body, "Фильм") || strings.Contains(sent[0].Body, "• x") {
		t.Fatalf("sent %d", len(sent))
	}
	if n := f.events(t, dlNews); n != 501 || f.pending(t, u.ID) != 501 {
		t.Fatalf("the off addon's lines: %d pending %d", n, f.pending(t, u.ID))
	}
}

// A profile edited after its addon lines were queued (turned kid, or an
// admin demoted) gets none of them: the audience is checked again at send.
func TestDigest_AddonAudienceRecheckedAtSend(t *testing.T) {
	f := newFixture(t)
	f.addonTopics()
	a := f.user(t, "Админ", true, false)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, a, "a@example.com", Prefs{Available: true, AddonTopics: on(dlDownloads)})
	f.subscribe(t, u, "u@example.com", Prefs{Available: true, AddonTopics: on(dlNews)})
	f.queue(t, AddonEvent{Addon: "dl", Topic: "downloads", Key: "d", Subject: subj("Ошибка загрузки")}, 1)
	f.queue(t, AddonEvent{Addon: "dl", Topic: "news", Key: "n", Subject: subj("Новость")}, 1)
	for _, x := range []auth.User{a, u} {
		if ok, err := f.svc.Enqueue(f.ctx, Event{UserID: x.ID, Topic: Available, Key: "av:1", Data: Data{Title: "Фильм"}}); !ok || err != nil {
			t.Fatalf("%v %v", ok, err)
		}
	}
	// the owner edits the profiles in «Профили»
	if _, err := f.db.Exec(`UPDATE users SET is_admin=0 WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE users SET is_kid=1 WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(2 * time.Hour)
	f.flush(t)
	sent := f.out.all()
	if len(sent) != 2 {
		t.Fatalf("sent %d", len(sent))
	}
	for _, m := range sent {
		if !strings.Contains(m.Body, "Фильм") {
			t.Fatalf("core line lost:\n%s", m.Body)
		}
		if strings.Contains(m.Body, "Ошибка загрузки") || strings.Contains(m.Body, "Новость") {
			t.Fatalf("an addon line went to a profile its topic no longer admits (%s):\n%s", m.To, m.Body)
		}
	}
}
