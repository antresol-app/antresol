package notify

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// arrived adds a movie to the library at the time at (added_at).
func (f *fixture) arrived(t *testing.T, title string, at time.Time) int64 {
	t.Helper()
	id, err := f.repo.UpsertMovie(f.ctx, library.MovieInput{Title: title, Year: 2018, Rating: "0+", File: library.FileInput{Path: "/m/" + title + ".mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	f.db.Exec(`UPDATE items SET added_at=? WHERE id=?`, at.UTC().Format(time.RFC3339), id)
	return id
}

// Turning «Просьбы» on is no news of old requests whose title came before.
func TestRequestsInLibraryCollector(t *testing.T) {
	f := newFixture(t)
	kid := f.user(t, "Маша", false, true)
	f.svc.RequestsOpen = func(context.Context) bool { return true } // the core build has none otherwise
	old := f.arrived(t, "Блуи", f.clock.Add(-48*time.Hour))
	f.db.Exec(`INSERT INTO kid_requests(user_id, kind, title_key, tmdb_id, title, status) VALUES(?, 'movie', 'tmdb:1', 1, 'Блуи', 'approved')`, kid.ID)
	f.db.Exec(`INSERT INTO kid_requests(user_id, kind, title_key, tmdb_id, title, status) VALUES(?, 'movie', 'tmdb:2', 2, 'Свинка Пеппа', 'approved')`, kid.ID)
	var fresh int64
	f.svc.Collectors = []func(context.Context) error{f.svc.RequestsInLibrary(func(_ context.Context, _ string, tmdb int, _, _ string) (int64, bool) {
		if tmdb == 1 {
			return old, true
		}
		return fresh, fresh != 0
	})}
	f.subscribe(t, kid, "masha@example.com", Prefs{Requests: true})
	f.collect(t)
	if n := f.pending(t, kid.ID); n != 0 {
		t.Fatalf("stale «Уже в медиатеке»: %d", n)
	}
	fresh = f.arrived(t, "Свинка Пеппа", f.clock.Add(time.Minute))
	f.collect(t)
	if n := f.pending(t, kid.ID); n != 1 {
		t.Fatalf("pending %d", n)
	}
}

func TestKidRequestApprovedAndInLibrary(t *testing.T) {
	f := newFixture(t)
	kid := f.user(t, "Маша", false, true)
	f.svc.RequestsOpen = func(context.Context) bool { return true }
	f.subscribe(t, kid, "masha@example.com", Prefs{Requests: true})
	res, err := f.db.Exec(`INSERT INTO kid_requests(user_id, kind, title_key, tmdb_id, title, year, poster, status) VALUES(?, 'movie', 'tmdb:1', 1, 'Блуи', 2018, 'https://image.tmdb.org/t/p/w185/b.jpg', 'approved')`, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	reqID, _ := res.LastInsertId()
	f.svc.RequestApproved(f.ctx, Request{ID: reqID, UserID: kid.ID, Kind: "movie", Title: "Блуи", Year: 2018})
	var item int64
	found := false
	f.svc.Collectors = []func(context.Context) error{f.svc.RequestsInLibrary(func(context.Context, string, int, string, string) (int64, bool) { return item, found })}
	f.collect(t)
	if f.pending(t, kid.ID) != 1 {
		t.Fatal("approved not queued alone")
	}
	item, found = f.arrived(t, "Блуи", f.clock.Add(time.Minute)), true
	f.collect(t)
	f.collect(t)
	if n := f.pending(t, kid.ID); n != 2 {
		t.Fatalf("pending %d", n)
	}
	f.clock = f.clock.Add(time.Hour)
	f.flush(t)
	m := f.out.all()[0]
	if !strings.Contains(m.Body, "Просьбу одобрили: Блуи (2018)") || !strings.Contains(m.Body, "Уже в медиатеке: Блуи (2018)") ||
		!strings.Contains(m.Body, "https://antresol.example/item/"+strconv.FormatInt(item, 10)) || !strings.Contains(m.HTML, "https://image.tmdb.org/t/p/w185/b.jpg") {
		t.Fatalf("%s\n%s", m.Body, m.HTML)
	}
}

// RequestsOpen offers «Просьбы» to kids (and only to them), in either edition.
func TestTopicsRequestsOpen(t *testing.T) {
	f := newFixture(t)
	admin := f.user(t, "Папа", true, false)
	kid := f.user(t, "Маша", false, true)
	f.svc.RequestsOpen = func(context.Context) bool { return true }
	if got := f.svc.TopicsFor(f.ctx, kid); !slices.Contains(got, Requests) {
		t.Errorf("kid %v", got)
	}
	if got := f.svc.TopicsFor(f.ctx, admin); slices.Contains(got, Requests) {
		t.Errorf("admin %v", got)
	}
	if _, err := f.svc.SetPrefs(f.ctx, kid, Prefs{Email: "masha@example.com", Requests: true}); err != nil {
		t.Fatal(err)
	}
	if p, err := f.svc.Prefs(f.ctx, kid.ID); err != nil || !p.Requests {
		t.Errorf("requests topic not kept: %+v %v", p, err)
	}
}
