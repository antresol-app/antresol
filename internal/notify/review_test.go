package notify

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/mailer"
)

// Verification letters: one a minute, five a day per profile, three a day
// per address (whichever profiles ask).
func TestVerificationDailyCaps(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	for i := range 5 {
		f.svc.SetPrefs(f.ctx, u, Prefs{Email: fmt.Sprintf("a%d@example.com", i)})
		if err := f.svc.SendVerification(f.ctx, u.ID); err != nil {
			t.Fatalf("letter %d: %v", i, err)
		}
		f.clock = f.clock.Add(2 * time.Minute)
	}
	f.svc.SetPrefs(f.ctx, u, Prefs{Email: "a9@example.com"})
	if err := f.svc.SendVerification(f.ctx, u.ID); !errors.Is(err, ErrTooMany) {
		t.Fatalf("sixth a day: %v", err)
	}
	f.clock = f.clock.Add(24 * time.Hour)
	if err := f.svc.SendVerification(f.ctx, u.ID); err != nil {
		t.Fatalf("next day: %v", err)
	}

	// one address, two profiles
	a := f.user(t, "Боря", false, false)
	b := f.user(t, "Вера", false, false)
	f.svc.SetPrefs(f.ctx, a, Prefs{Email: "same@example.com"})
	f.svc.SetPrefs(f.ctx, b, Prefs{Email: "SAME@example.com"})
	for i, uid := range []int64{a.ID, b.ID, a.ID} {
		if err := f.svc.SendVerification(f.ctx, uid); err != nil {
			t.Fatalf("letter %d: %v", i, err)
		}
		f.clock = f.clock.Add(2 * time.Minute)
	}
	if err := f.svc.SendVerification(f.ctx, b.ID); !errors.Is(err, ErrTooMany) {
		t.Fatalf("fourth to the address: %v", err)
	}
}

// The admin's route edits kids only: TopicsFor aside, an adult's settings
// are its own (checked by the API); here: the bounce flag and the
// one-click header that stops everything.
func TestListUnsubscribeStopsEverything(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Episodes: true, Available: true})
	ep := f.episode(t, "Тед Лассо", "16+", 1, 1)
	f.prog.Save(f.ctx, u.ID, ep, 1000, 60000)
	f.collect(t)
	f.episode(t, "Тед Лассо", "16+", 1, 2)
	f.collect(t)
	f.clock = f.clock.Add(time.Hour)
	f.flush(t)
	m := f.out.all()[0]
	tok := regexp.MustCompile(`<https://antresol\.example/api/notify/link/unsubscribe\?t=([^>]+)>`).FindStringSubmatch(m.Headers["List-Unsubscribe"])
	if tok == nil {
		t.Fatalf("header %q", m.Headers["List-Unsubscribe"])
	}
	info, err := f.svc.Unsubscribe(f.ctx, tok[1])
	if err != nil || info.Topic != TopicAll {
		t.Fatalf("%+v %v", info, err)
	}
	if p, _ := f.svc.Prefs(f.ctx, u.ID); p.Episodes || p.Available {
		t.Fatalf("still on %+v", p)
	}
	// the body's links stay per topic
	if !strings.Contains(m.Body, "Больше не присылать «Новые серии»") {
		t.Fatal(m.Body)
	}
}

func queueOne(t *testing.T, f *fixture, email string) int64 {
	t.Helper()
	u := f.user(t, email, false, false)
	f.subscribe(t, u, email, Prefs{Episodes: true})
	ep := f.episode(t, "Сериал "+email, "16+", 1, 1)
	f.prog.Save(f.ctx, u.ID, ep, 1000, 60000)
	f.collect(t)
	f.episode(t, "Сериал "+email, "16+", 1, 2)
	f.collect(t)
	f.clock = f.clock.Add(time.Hour)
	return u.ID
}

func TestPermanentRefusalUnconfirmsTheAddress(t *testing.T) {
	f := newFixture(t)
	uid := queueOne(t, f, "gone@example.com")
	f.out.fail = fmt.Errorf("%w: 550 no such user", mailer.ErrRecipient)
	f.flush(t)
	p, _ := f.svc.Prefs(f.ctx, uid)
	if p.Verified || !p.Bounced {
		t.Fatalf("after a 550: %+v", p)
	}
	f.out.fail = nil
	f.clock = f.clock.Add(2 * time.Hour)
	f.flush(t)
	if len(f.out.all()) != 0 {
		t.Fatal("sent to a refused address")
	}
	// confirmed again: no longer bounced
	f.db.Exec(`UPDATE notify_prefs SET verified=email WHERE user_id=?`, uid)
	if p, _ := f.svc.Prefs(f.ctx, uid); p.Bounced {
		t.Fatal("bounced after a new confirmation")
	}
}

func TestTransientFailureBacksOffAcrossRestarts(t *testing.T) {
	f := newFixture(t)
	uid := queueOne(t, f, "anya@example.com")
	f.out.fail = errors.New("421 try later")
	f.flush(t)
	restart := func() {
		f.svc = &Service{DB: f.db, Users: f.users, Secret: []byte("secret"), Send: f.out.send, Mail: f.svc.Mail, Now: f.svc.Now, Location: time.UTC}
	}
	restart()
	f.out.fail = nil
	f.clock = f.clock.Add(10 * time.Minute)
	f.flush(t)
	if len(f.out.all()) != 0 {
		t.Fatal("retried before the backoff (in memory only?)")
	}
	f.out.fail = errors.New("421 try later")
	f.clock = f.clock.Add(6 * time.Minute) // 16 min: the first retry
	f.flush(t)
	restart()
	f.out.fail = nil
	f.clock = f.clock.Add(20 * time.Minute) // under the second wait (30 min)
	f.flush(t)
	if len(f.out.all()) != 0 {
		t.Fatal("the second wait is not longer")
	}
	f.clock = f.clock.Add(11 * time.Minute)
	f.flush(t)
	if len(f.out.all()) != 1 || f.pending(t, uid) != 0 {
		t.Fatalf("letters %d pending %d", len(f.out.all()), f.pending(t, uid))
	}
}

// A crash during a send leaves its marker: after 10 minutes the letter
// counts as sent (it may have gone), never sent again.
func TestUnfinishedSendIsNotRepeated(t *testing.T) {
	f := newFixture(t)
	uid := queueOne(t, f, "anya@example.com")
	f.db.Exec(`UPDATE notify_events SET sending_at=? WHERE user_id=?`, f.clock.Add(-11*time.Minute).Unix(), uid)
	f.flush(t)
	if len(f.out.all()) != 0 || f.pending(t, uid) != 0 {
		t.Fatalf("resent: %d letters, %d pending", len(f.out.all()), f.pending(t, uid))
	}
	// a recent marker (a send in flight): the profile waits
	uid2 := queueOne(t, f, "borya@example.com")
	f.db.Exec(`UPDATE notify_events SET sending_at=? WHERE user_id=? AND sent_at IS NULL`, f.clock.Add(-time.Minute).Unix(), uid2)
	f.flush(t)
	if len(f.out.all()) != 0 {
		t.Fatal("sent while another send is in flight")
	}
}

// A watchlist title that goes and comes back: once in 30 days.
func TestWatchlistBackAtMostMonthly(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Available: true})
	id, _ := f.repo.UpsertMovie(f.ctx, library.MovieInput{Title: "Дюна", Year: 2021, Rating: "12+", File: library.FileInput{Path: "/m/dune.mkv"}})
	f.prog.AddToWatchlist(f.ctx, u.ID, id)
	f.collect(t)
	bounce := func() {
		f.db.Exec(`UPDATE items SET missing_since='2026-11-02T00:00:00Z' WHERE id=?`, id)
		f.collect(t)
		f.db.Exec(`UPDATE items SET missing_since=NULL WHERE id=?`, id)
		f.collect(t)
	}
	count := func() int {
		var n int
		f.db.QueryRow(`SELECT COUNT(*) FROM notify_events WHERE user_id=? AND topic='available'`, u.ID).Scan(&n)
		return n
	}
	bounce()
	f.clock = f.clock.Add(3 * 24 * time.Hour)
	bounce()
	if n := count(); n != 1 {
		t.Fatalf("events %d within 30 days", n)
	}
	f.clock = f.clock.Add(31 * 24 * time.Hour)
	bounce()
	if n := count(); n != 2 {
		t.Fatalf("events %d after 30 days", n)
	}
}

// An episode the profile was told about, made again (restored, rescanned
// after a move: a new row), is no news; by its numbers or by its file.
func TestRecreatedEpisodeIsNoNews(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "Аня", false, false)
	f.subscribe(t, u, "anya@example.com", Prefs{Episodes: true})
	ep := f.episode(t, "Тед Лассо", "16+", 1, 1)
	f.prog.Save(f.ctx, u.ID, ep, 1000, 60000)
	f.collect(t)
	e2, err := f.repo.UpsertEpisode(f.ctx, library.EpisodeInput{SeriesTitle: "Тед Лассо", SeriesRating: "16+", Season: 1, Episode: 2,
		File: library.FileInput{Path: "/s/ted/e2.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	f.collect(t)
	if f.pending(t, u.ID) != 1 {
		t.Fatal("not queued")
	}
	// the same numbers again, in a row of its own
	f.db.Exec(`DELETE FROM items WHERE id=?`, e2)
	f.repo.UpsertEpisode(f.ctx, library.EpisodeInput{SeriesTitle: "Тед Лассо", SeriesRating: "16+", Season: 1, Episode: 2,
		File: library.FileInput{Path: "/s/ted/e2-again.mkv"}})
	f.collect(t)
	// the same file under other numbers (a rename)
	f.db.Exec(`DELETE FROM files WHERE path='/s/ted/e2-again.mkv'`)
	f.repo.UpsertEpisode(f.ctx, library.EpisodeInput{SeriesTitle: "Тед Лассо", SeriesRating: "16+", Season: 1, Episode: 7,
		File: library.FileInput{Path: "/s/ted/e2.mkv"}})
	f.collect(t)
	var n int
	f.db.QueryRow(`SELECT COUNT(*) FROM notify_events WHERE user_id=?`, u.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("events %d", n)
	}
}

func TestEpisodeWithoutNumbersShowsItsTitle(t *testing.T) {
	evs := []stored{
		{groupID: 1, itemID: 1, data: Data{Title: "Планета Земля", Season: 1, Episode: 2}},
		{groupID: 1, itemID: 1, data: Data{Title: "Планета Земля", EpisodeTitle: "Горы"}},
	}
	got := renderEpisodes("ru", evs, func(string) string { return "" })
	if len(got) != 1 || got[0].Line != "Планета Земля — 2 новые серии: S01E02, «Горы»" {
		t.Fatalf("%+v", got)
	}
	got = renderEpisodes("ru", evs[1:], func(string) string { return "" })
	if got[0].Line != "Планета Земля — 1 новая серия: «Горы»" || strings.Contains(got[0].Line, "S00") {
		t.Fatalf("%+v", got)
	}
}

// Titles off the library (group and item ids below 1) are listed one line
// each, linked by their own path; a film is listed with its link too.
func TestOffLibraryTitlesGetTheirOwnLinesAndLinks(t *testing.T) {
	link := func(p string) string {
		if p == "" {
			return ""
		}
		return "https://x.example" + p
	}
	evs := []stored{
		{groupID: -1, data: Data{Title: "Alpha", Season: 1, Episode: 3, Path: "/title/series/1"}},
		{groupID: -2, data: Data{Title: "Beta", Season: 2, Episode: 1, Path: "/title/series/2"}},
	}
	got := renderEpisodes("ru", evs, link)
	if len(got) != 2 || got[0].Line != "Alpha — 1 новая серия: S01E03" || got[0].Link != "https://x.example/title/series/1" ||
		got[1].Line != "Beta — 1 новая серия: S02E01" || got[1].Link != "https://x.example/title/series/2" {
		t.Fatalf("%+v", got)
	}
	films := renderAvailable("ru", []stored{
		{groupID: -3, data: Data{Title: "Film", Kind: "movie", Path: "/title/movie/3"}},
		{groupID: -4, data: Data{Title: "Other", Kind: "movie", Path: "//evil.example/x"}},
	}, link)
	if len(films) != 2 || films[0].Line != "Film" || films[0].Link != "https://x.example/title/movie/3" || films[1].Link != "" {
		t.Fatalf("%+v", films)
	}
}
