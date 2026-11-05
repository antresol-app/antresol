package notify

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/mailer"
	"github.com/antresol-app/antresol/internal/progress"
)

// outbox is the fake mail server: it keeps what would have gone.
type outbox struct {
	mu   sync.Mutex
	sent []mailer.Message
	fail error
}

func (o *outbox) send(_ context.Context, _ mailer.Config, m mailer.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail != nil {
		return o.fail
	}
	o.sent = append(o.sent, m)
	return nil
}

func (o *outbox) all() []mailer.Message {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]mailer.Message(nil), o.sent...)
}

type fixture struct {
	ctx   context.Context
	db    *sql.DB
	svc   *Service
	repo  *library.Repo
	prog  *progress.Store
	users *auth.Store
	out   *outbox
	clock time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	f := &fixture{ctx: context.Background(), db: d, repo: library.NewRepo(d), prog: progress.New(d), users: auth.NewStore(d), out: &outbox{},
		clock: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	f.svc = &Service{DB: d, Users: f.users, Secret: []byte("secret"), Send: f.out.send, PublicURL: "https://antresol.example",
		Mail: func(context.Context) (mailer.Config, bool) {
			return mailer.Config{Host: "smtp.example", From: "me@example.com"}, true
		},
		Now: func() time.Time { return f.clock }, Location: time.UTC}
	return f
}

func (f *fixture) user(t *testing.T, name string, admin, kid bool) auth.User {
	t.Helper()
	u, err := f.users.CreateUser(f.ctx, auth.NewUser{Name: name, Password: "password123", IsAdmin: admin, IsKid: kid})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// subscribe sets u's address (confirmed) and topics.
func (f *fixture) subscribe(t *testing.T, u auth.User, email string, p Prefs) {
	t.Helper()
	p.Email = email
	if _, err := f.svc.SetPrefs(f.ctx, u, p); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE notify_prefs SET verified=email WHERE user_id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
}

var fileNo atomic.Int64

func (f *fixture) episode(t *testing.T, series string, rating string, season, ep int) int64 {
	t.Helper()
	id, err := f.repo.UpsertEpisode(f.ctx, library.EpisodeInput{SeriesTitle: series, SeriesRating: rating, Season: season, Episode: ep,
		File: library.FileInput{Path: "/s/" + series + "/" + strconv.FormatInt(fileNo.Add(1), 10) + ".mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fixture) seriesOf(t *testing.T, ep int64) int64 {
	t.Helper()
	var id int64
	if err := f.db.QueryRow(`SELECT s.parent_id FROM items e JOIN items s ON s.id=e.parent_id WHERE e.id=?`, ep).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fixture) collect(t *testing.T) {
	t.Helper()
	if err := f.svc.Collect(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) flush(t *testing.T) {
	t.Helper()
	if err := f.svc.Flush(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) pending(t *testing.T, uid int64) int {
	t.Helper()
	var n int
	f.db.QueryRow(`SELECT COUNT(*) FROM notify_events WHERE user_id=? AND sent_at IS NULL`, uid).Scan(&n)
	return n
}

func TestNothingIsQueuedOrSentByDefault(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	ep := f.episode(t, "Тед Лассо", "16+", 1, 1)
	f.prog.Save(f.ctx, u.ID, ep, 1000, 60000)
	f.collect(t) // the first look: the library as it is
	f.episode(t, "Тед Лассо", "16+", 1, 2)
	f.collect(t)
	if n := f.pending(t, u.ID); n != 0 {
		t.Fatalf("queued %d without settings", n)
	}
	p, err := f.svc.Prefs(f.ctx, u.ID)
	if err != nil || p.Email != "" || p.Episodes || p.Available || p.Requests || p.Downloads || !p.QuietHours {
		t.Fatalf("defaults %+v %v", p, err)
	}
	// an address but no topic: still nothing
	f.subscribe(t, u, "anya@example.com", Prefs{})
	f.episode(t, "Тед Лассо", "16+", 1, 3)
	f.collect(t)
	f.clock = f.clock.Add(2 * time.Hour)
	f.flush(t)
	if f.pending(t, u.ID) != 0 || len(f.out.all()) != 0 {
		t.Fatal("sent with every topic off")
	}
}

func TestFirstCollectIsNoNews(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Episodes: true})
	ep := f.episode(t, "Тед Лассо", "16+", 1, 1)
	f.prog.Save(f.ctx, u.ID, ep, 1000, 60000)
	f.episode(t, "Тед Лассо", "16+", 1, 2)
	f.collect(t)
	if n := f.pending(t, u.ID); n != 0 {
		t.Fatalf("the library as it was is news: %d", n)
	}
}

func TestNewEpisodesDigestGroupedBySeries(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	other := f.user(t, "Боря", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Episodes: true, Lang: "ru"})
	f.subscribe(t, other, "borya@example.com", Prefs{Episodes: true})
	first := f.episode(t, "Тед Лассо", "16+", 1, 10)
	f.prog.Save(f.ctx, u.ID, first, 1000, 60000)
	f.collect(t)
	f.episode(t, "Тед Лассо", "16+", 2, 1)
	f.episode(t, "Тед Лассо", "16+", 2, 2)
	f.episode(t, "Тед Лассо", "16+", 2, 3)
	f.collect(t)
	f.collect(t) // again: nothing twice
	if n := f.pending(t, u.ID); n != 3 {
		t.Fatalf("pending %d, want 3", n)
	}
	if n := f.pending(t, other.ID); n != 0 {
		t.Fatalf("a profile that does not watch it got %d", n)
	}
	// it settles first (more episodes may come)
	f.flush(t)
	if len(f.out.all()) != 0 {
		t.Fatal("sent before it settled")
	}
	f.clock = f.clock.Add(11 * time.Minute)
	f.flush(t)
	sent := f.out.all()
	if len(sent) != 1 {
		t.Fatalf("sent %d letters", len(sent))
	}
	m := sent[0]
	if m.To != "anya@example.com" || m.Subject != "Тед Лассо — 3 новые серии: S02E01–E03" {
		t.Fatalf("letter %q %q", m.To, m.Subject)
	}
	link := "https://antresol.example/item/" + strconv.FormatInt(f.seriesOf(t, first), 10)
	if !strings.Contains(m.Body, "Тед Лассо — 3 новые серии: S02E01–E03") || !strings.Contains(m.Body, link) || !strings.Contains(m.HTML, link) {
		t.Fatalf("body:\n%s\n%s", m.Body, m.HTML)
	}
	if !strings.Contains(m.Body, "/api/notify/link/unsubscribe?t=") ||
		!strings.HasPrefix(m.Headers["List-Unsubscribe"], "<https://antresol.example/api/notify/link/unsubscribe?t=") ||
		m.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("no unsubscribe link: %v", m.Headers)
	}
	if strings.Contains(m.HTML, "<img") {
		t.Fatal("a picture without a TMDB poster")
	}
	if f.pending(t, u.ID) != 0 {
		t.Fatal("events still pending after the letter")
	}
	// at most once an hour
	f.episode(t, "Тед Лассо", "16+", 2, 4)
	f.collect(t)
	f.clock = f.clock.Add(20 * time.Minute)
	f.flush(t)
	if len(f.out.all()) != 1 {
		t.Fatal("a second letter within the hour")
	}
	f.clock = f.clock.Add(41 * time.Minute)
	f.flush(t)
	if got := f.out.all(); len(got) != 2 || got[1].Subject != "Тед Лассо — 1 новая серия: S02E04" {
		t.Fatalf("second letter %v", got)
	}
}

// A row deleted and a new one with the same id (SQLite reuses the largest
// rowid): the new episode is still news.
func TestReusedIDIsStillNews(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Episodes: true})
	ep := f.episode(t, "Тед Лассо", "16+", 1, 1)
	f.prog.Save(f.ctx, u.ID, ep, 1000, 60000)
	f.collect(t)
	e2 := f.episode(t, "Тед Лассо", "16+", 1, 2)
	f.collect(t)
	if f.pending(t, u.ID) != 1 {
		t.Fatal("first new episode not queued")
	}
	if _, err := f.db.Exec(`DELETE FROM items WHERE id=?`, e2); err != nil {
		t.Fatal(err)
	}
	e3 := f.episode(t, "Тед Лассо", "16+", 1, 3)
	if e3 != e2 {
		t.Skipf("id not reused (%d, %d)", e2, e3)
	}
	// the new row is a second later than the old one
	f.db.Exec(`UPDATE items SET added_at=strftime('%Y-%m-%dT%H:%M:%SZ', added_at, '+1 second') WHERE id=?`, e3)
	f.collect(t)
	if n := f.pending(t, u.ID); n != 2 {
		t.Fatalf("pending %d, want 2", n)
	}
}

// An item written late with an earlier added_at (a scan's write that
// committed after the collector looked) is still found.
func TestLateCommitWithinTheWindow(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Episodes: true})
	ep := f.episode(t, "Тед Лассо", "16+", 1, 1)
	f.prog.Save(f.ctx, u.ID, ep, 1000, 60000)
	f.collect(t)
	late := f.episode(t, "Тед Лассо", "16+", 1, 2)
	f.db.Exec(`UPDATE items SET added_at=strftime('%Y-%m-%dT%H:%M:%SZ', added_at, '-5 minutes') WHERE id=?`, late)
	f.collect(t)
	if n := f.pending(t, u.ID); n != 1 {
		t.Fatalf("pending %d", n)
	}
}

func TestWatchlistSeriesAndKidRatingLimit(t *testing.T) {
	f := newFixture(t)
	kid := f.user(t, "Маша", false, true) // 12+ by default
	adult := f.user(t, "Аня", false, false)
	f.subscribe(t, kid, "masha@example.com", Prefs{Episodes: true})
	f.subscribe(t, adult, "anya@example.com", Prefs{Episodes: true, Lang: "en"})
	a := f.episode(t, "Блуи", "0+", 1, 1)
	b := f.episode(t, "Во все тяжкие", "18+", 1, 1)
	f.prog.AddToWatchlist(f.ctx, kid.ID, f.seriesOf(t, a))
	f.prog.AddToWatchlist(f.ctx, kid.ID, f.seriesOf(t, b)) // added before the limit, say
	f.prog.AddToWatchlist(f.ctx, adult.ID, f.seriesOf(t, b))
	f.collect(t)
	f.episode(t, "Блуи", "0+", 1, 2)
	f.episode(t, "Во все тяжкие", "18+", 1, 2)
	f.episode(t, "Во все тяжкие", "18+", 1, 4)
	f.collect(t)
	if n := f.pending(t, kid.ID); n != 1 {
		t.Fatalf("kid pending %d, want 1 (Блуи only)", n)
	}
	f.clock = f.clock.Add(time.Hour)
	f.flush(t)
	var en mailer.Message
	for _, m := range f.out.all() {
		if m.To == "anya@example.com" {
			en = m
		}
		if m.To == "masha@example.com" && strings.Contains(m.Body, "тяжкие") {
			t.Fatal("a kid heard of an 18+ series")
		}
	}
	if en.Subject != "Во все тяжкие — 2 new episodes: S01E02, S01E04" || !strings.Contains(en.Body, "New in Antresol") {
		t.Fatalf("english letter %q\n%s", en.Subject, en.Body)
	}
}

func TestQuietHoursWaitForTheMorning(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	night := f.user(t, "Сова", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Episodes: true, QuietHours: true})
	f.subscribe(t, night, "owl@example.com", Prefs{Episodes: true, QuietHours: false})
	ep := f.episode(t, "Тед Лассо", "16+", 1, 1)
	f.prog.Save(f.ctx, u.ID, ep, 1000, 60000)
	f.prog.Save(f.ctx, night.ID, ep, 1000, 60000)
	f.collect(t)
	f.clock = time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)
	f.episode(t, "Тед Лассо", "16+", 1, 2)
	f.collect(t)
	f.clock = f.clock.Add(time.Hour) // 00:00
	f.flush(t)
	if got := f.out.all(); len(got) != 1 || got[0].To != "owl@example.com" {
		t.Fatalf("at night: %v", got)
	}
	f.clock = time.Date(2026, 11, 3, 7, 59, 0, 0, time.UTC)
	f.flush(t)
	if len(f.out.all()) != 1 {
		t.Fatal("sent before 08:00")
	}
	f.clock = time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	f.flush(t)
	if got := f.out.all(); len(got) != 2 || got[1].To != "anya@example.com" {
		t.Fatalf("in the morning: %v", got)
	}
}

func TestUnverifiedAddressGetsNothingAndFailureRetries(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	if _, err := f.svc.SetPrefs(f.ctx, u, Prefs{Email: "anya@example.com", Episodes: true}); err != nil {
		t.Fatal(err)
	}
	ep := f.episode(t, "Тед Лассо", "16+", 1, 1)
	f.prog.Save(f.ctx, u.ID, ep, 1000, 60000)
	f.collect(t)
	f.episode(t, "Тед Лассо", "16+", 1, 2)
	f.collect(t)
	f.clock = f.clock.Add(time.Hour)
	f.flush(t)
	if len(f.out.all()) != 0 || f.pending(t, u.ID) != 1 {
		t.Fatal("sent to an unconfirmed address")
	}
	f.db.Exec(`UPDATE notify_prefs SET verified=email WHERE user_id=?`, u.ID)
	f.out.fail = errors.New("smtp down")
	f.flush(t)
	if f.pending(t, u.ID) != 1 {
		t.Fatal("a failed letter lost its events")
	}
	f.out.fail = nil
	f.clock = f.clock.Add(5 * time.Minute)
	f.flush(t)
	if len(f.out.all()) != 0 {
		t.Fatal("retried at once")
	}
	f.clock = f.clock.Add(15 * time.Minute)
	f.flush(t)
	if len(f.out.all()) != 1 {
		t.Fatal("not retried")
	}
	// a changed address is unconfirmed again
	p, _ := f.svc.SetPrefs(f.ctx, u, Prefs{Email: "new@example.com", Episodes: true})
	if p.Verified {
		t.Fatal("a new address is confirmed")
	}
}

func TestRestartKeepsPendingAndNeverRepeats(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Episodes: true})
	ep := f.episode(t, "Тед Лассо", "16+", 1, 1)
	f.prog.Save(f.ctx, u.ID, ep, 1000, 60000)
	f.collect(t)
	f.episode(t, "Тед Лассо", "16+", 1, 2)
	f.collect(t)
	// a new Service on the same database, as after a restart; no public address
	f.svc = &Service{DB: f.db, Users: f.users, Secret: []byte("secret"), Send: f.out.send, Mail: f.svc.Mail, Now: f.svc.Now, Location: time.UTC}
	f.collect(t)
	f.clock = f.clock.Add(time.Hour)
	f.flush(t)
	f.flush(t)
	if got := f.out.all(); len(got) != 1 {
		t.Fatalf("letters %d", len(got))
	}
	if m := f.out.all()[0]; strings.Contains(m.Body, "http") || strings.Contains(m.HTML, "href") || m.Headers != nil {
		t.Fatalf("links without a public address:\n%s", m.Body)
	}
}

func TestUnsubscribeLinkTurnsTheTopicOff(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Episodes: true, Available: true})
	tok := f.svc.UnsubscribeToken(u.ID, Episodes, "anya@example.com")
	if _, err := f.svc.Unsubscribe(f.ctx, tok+"x"); !errors.Is(err, ErrBadToken) {
		t.Fatalf("a forged token: %v", err)
	}
	info, err := f.svc.CheckUnsubscribe(f.ctx, tok)
	if err != nil || info.Topic != Episodes || info.UserID != u.ID {
		t.Fatalf("%+v %v", info, err)
	}
	if p, _ := f.svc.Prefs(f.ctx, u.ID); !p.Episodes {
		t.Fatal("the check applied it")
	}
	if _, err := f.svc.Unsubscribe(f.ctx, tok); err != nil {
		t.Fatal(err)
	}
	p, _ := f.svc.Prefs(f.ctx, u.ID)
	if p.Episodes || !p.Available {
		t.Fatalf("after unsubscribe %+v", p)
	}
	// a link to an old address does nothing
	f.subscribe(t, u, "new@example.com", Prefs{Episodes: true})
	if _, err := f.svc.Unsubscribe(f.ctx, tok); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("old address: %v", err)
	}
}

func TestVerificationLetterCodeAndLink(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	if err := f.svc.SendVerification(f.ctx, u.ID); !errors.Is(err, ErrNoEmail) {
		t.Fatalf("no address: %v", err)
	}
	f.svc.SetPrefs(f.ctx, u, Prefs{Email: "anya@example.com"})
	if err := f.svc.SendVerification(f.ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SendVerification(f.ctx, u.ID); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("second at once: %v", err)
	}
	m := f.out.all()[0]
	if m.To != "anya@example.com" || !strings.Contains(m.Subject, "проверка адреса") || m.HTML == "" {
		t.Fatalf("%q %q", m.To, m.Subject)
	}
	code := regexp.MustCompile(`\b\d{6}\b`).FindString(m.Body)
	if code == "" {
		t.Fatalf("no code:\n%s", m.Body)
	}
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	if err := f.svc.ConfirmCode(f.ctx, u.ID, wrong); !errors.Is(err, ErrBadCode) {
		t.Fatalf("wrong code: %v", err)
	}
	if err := f.svc.ConfirmCode(f.ctx, u.ID, code); err != nil {
		t.Fatal(err)
	}
	if p, _ := f.svc.Prefs(f.ctx, u.ID); !p.Verified {
		t.Fatal("not confirmed by the code")
	}
	// the link of the letter confirms too
	f.svc.SetPrefs(f.ctx, u, Prefs{Email: "other@example.com"})
	f.clock = f.clock.Add(2 * time.Minute)
	if err := f.svc.SendVerification(f.ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	link := regexp.MustCompile(`https://antresol\.example/api/notify/link/verify\?t=(\S+)`).FindStringSubmatch(f.out.all()[1].Body)
	if link == nil {
		t.Fatalf("no link:\n%s", f.out.all()[1].Body)
	}
	if _, err := f.svc.Confirm(f.ctx, link[1]); err != nil {
		t.Fatal(err)
	}
	if p, _ := f.svc.Prefs(f.ctx, u.ID); !p.Verified || p.Email != "other@example.com" {
		t.Fatalf("link: %+v", p)
	}
	// five wrong codes use a code up
	f.svc.SetPrefs(f.ctx, u, Prefs{Email: "third@example.com"})
	f.clock = f.clock.Add(2 * time.Minute)
	f.svc.SendVerification(f.ctx, u.ID)
	code = regexp.MustCompile(`\b\d{6}\b`).FindString(f.out.all()[2].Body)
	for range 5 {
		f.svc.ConfirmCode(f.ctx, u.ID, "abc")
	}
	if err := f.svc.ConfirmCode(f.ctx, u.ID, code); !errors.Is(err, ErrBadCode) {
		t.Fatalf("after 5 wrong codes: %v", err)
	}
	// no mail account
	f.svc.Mail = func(context.Context) (mailer.Config, bool) { return mailer.Config{}, false }
	f.clock = f.clock.Add(2 * time.Minute)
	if err := f.svc.SendVerification(f.ctx, u.ID); !errors.Is(err, ErrNoMail) {
		t.Fatalf("no account: %v", err)
	}
}

func TestSetPrefsChecksAddressAndTopics(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	if _, err := f.svc.SetPrefs(f.ctx, u, Prefs{Email: "Аня <a@example.com>"}); !errors.Is(err, ErrBadEmail) {
		t.Fatalf("display name: %v", err)
	}
	if _, err := f.svc.SetPrefs(f.ctx, u, Prefs{Email: "a@localhost"}); !errors.Is(err, ErrBadEmail) {
		t.Fatalf("no domain: %v", err)
	}
	p, err := f.svc.SetPrefs(f.ctx, u, Prefs{Email: " a@example.com ", Downloads: true, Requests: true, Episodes: true, Lang: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Email != "a@example.com" || p.Downloads || p.Requests || !p.Episodes || p.Lang != "" {
		t.Fatalf("an adult's %+v", p)
	}
}

func TestTopicTurnedOffDropsWhatWaited(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Episodes: true})
	ep := f.episode(t, "Тед Лассо", "16+", 1, 1)
	f.prog.Save(f.ctx, u.ID, ep, 1000, 60000)
	f.collect(t)
	f.episode(t, "Тед Лассо", "16+", 1, 2)
	f.collect(t)
	f.svc.SetPrefs(f.ctx, u, Prefs{Email: "anya@example.com"})
	if f.pending(t, u.ID) != 0 {
		t.Fatal("events of a topic turned off kept")
	}
}

func TestWatchlistTitleBackOnDisk(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Available: true})
	id, err := f.repo.UpsertMovie(f.ctx, library.MovieInput{Title: "Дюна", Year: 2021, Rating: "12+", File: library.FileInput{Path: "/m/dune.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	f.db.Exec(`UPDATE items SET meta_json='{"poster_url":"https://image.tmdb.org/t/p/w185/dune.jpg"}' WHERE id=?`, id)
	f.prog.AddToWatchlist(f.ctx, u.ID, id)
	f.collect(t)
	f.db.Exec(`UPDATE items SET missing_since='2026-11-02T00:00:00Z' WHERE id=?`, id)
	f.collect(t)
	if f.pending(t, u.ID) != 0 {
		t.Fatal("queued while missing")
	}
	f.db.Exec(`UPDATE items SET missing_since=NULL WHERE id=?`, id)
	f.collect(t)
	f.collect(t)
	if n := f.pending(t, u.ID); n != 1 {
		t.Fatalf("pending %d", n)
	}
	f.clock = f.clock.Add(time.Hour)
	f.flush(t)
	m := f.out.all()[0]
	if m.Subject != "Дюна (2021)" || !strings.Contains(m.HTML, `<img src="https://image.tmdb.org/t/p/w185/dune.jpg"`) ||
		!strings.Contains(m.Body, "https://antresol.example/item/") {
		t.Fatalf("%q\n%s", m.Subject, m.HTML)
	}
}

func TestEpisodeRanges(t *testing.T) {
	got := episodeRanges([]se{{2, 3}, {2, 1}, {2, 2}, {2, 5}, {3, 1}, {3, 2}, {2, 2}})
	if got != "S02E01–E03, S02E05, S03E01–E02" {
		t.Fatal(got)
	}
}

func TestRemotePosterOnlyTMDBHTTPS(t *testing.T) {
	for in, want := range map[string]string{
		"https://image.tmdb.org/t/p/w185/a.jpg": "https://image.tmdb.org/t/p/w185/a.jpg",
		"http://image.tmdb.org/t/p/w185/a.jpg":  "",
		"https://evil.example/a.jpg":            "",
		"abc123.jpg":                            "",
		`https://image.tmdb.org/a.jpg"onerror=`: "",
	} {
		if got := RemotePoster(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestHTMLEscapesTitles(t *testing.T) {
	l, err := compose("ru", "<b>Аня</b>", []section{{Topic: Available, Heading: "H", Entries: []entry{{Line: `<script>alert(1)</script>`, Link: "https://a.example/item/1"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(l.HTML, "<script>") || strings.Contains(l.HTML, "<b>Аня") {
		t.Fatal(l.HTML)
	}
}
