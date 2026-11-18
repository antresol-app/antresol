package myshows

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/accounts"
	"github.com/antresol-app/antresol/internal/animemap"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/tracking"
)

// fakeProfile serves a fixed list (or an error).
type fakeProfile struct {
	entries []Entry
	err     error
	logins  []string
}

func (f *fakeProfile) ProfileShows(_ context.Context, login string) ([]Entry, error) {
	f.logins = append(f.logins, login)
	return f.entries, f.err
}

// fakeShows answers shows.GetById from a table of IMDb ids (fail: the
// lookup errors).
type fakeShows struct {
	imdb      map[int64]int64
	fail      bool // the service is unavailable
	failPlain bool // an error that is no outage
	calls     int
}

func (f *fakeShows) Show(_ context.Context, id int64) (ShowInfo, error) {
	f.calls++
	if f.fail {
		return ShowInfo{}, fmt.Errorf("myshows shows.GetById: %w: status 502", ErrUnavailable)
	}
	if f.failPlain {
		return ShowInfo{}, errors.New("myshows shows.GetById: bad result")
	}
	return ShowInfo{ID: id, IMDbID: f.imdb[id]}, nil
}

// trackerOf is accounts.Tracker over a real tracking.Store (the adapter
// main.go wires).
type trackerOf struct{ st *tracking.Store }

func (a trackerOf) EnsureTitle(ctx context.Context, kind string, tmdbID int64, name string) (accounts.TitleRef, error) {
	t, err := a.st.EnsureTitle(ctx, kind, tmdbID, name, "")
	return refOf(t), err
}

func (a trackerOf) Title(ctx context.Context, id int64) (accounts.TitleRef, bool, error) {
	t, ok, err := a.st.TitleByID(ctx, id)
	return refOf(t), ok, err
}

func (a trackerOf) SetShikimoriID(ctx context.Context, titleID, shikimoriID, anidbID int64) error {
	return a.st.SetExternalIDs(ctx, titleID, &shikimoriID, nil, nil)
}

func (a trackerOf) Marks(ctx context.Context, uid int64) ([]accounts.Mark, error) {
	ms, err := a.st.Marks(ctx, uid)
	out := make([]accounts.Mark, 0, len(ms))
	for _, m := range ms {
		out = append(out, accounts.Mark{UserID: m.UserID, TitleID: m.TitleID, State: string(m.State), Score: m.Score, UpdatedAt: m.UpdatedAt, Origin: m.Origin})
	}
	return out, err
}

func (a trackerOf) Mark(ctx context.Context, uid, titleID int64) (accounts.Mark, bool, error) {
	ms, err := a.Marks(ctx, uid)
	for _, m := range ms {
		if m.TitleID == titleID {
			return m, true, err
		}
	}
	return accounts.Mark{}, false, err
}

func (a trackerOf) SetMark(ctx context.Context, m accounts.Mark) error {
	return a.st.SetMark(ctx, tracking.Mark{UserID: m.UserID, TitleID: m.TitleID, State: tracking.MarkState(m.State), Score: m.Score, UpdatedAt: m.UpdatedAt, Origin: m.Origin})
}

func (a trackerOf) DeleteMark(ctx context.Context, uid, titleID int64, origin string) error {
	return a.st.DeleteMarkFrom(ctx, uid, titleID, origin)
}

func refOf(t tracking.Title) accounts.TitleRef {
	r := accounts.TitleRef{ID: t.ID, Kind: t.Kind, TMDBID: t.TMDBID}
	if t.ShikimoriID != nil {
		r.ShikimoriID = *t.ShikimoriID
	}
	return r
}

type pullEnv struct {
	t       *testing.T
	acc     *accounts.Store
	st      *tracking.Store
	uid     int64
	remote  *fakeProfile
	shows   *fakeShows
	finds   int
	findErr error // TMDB lookups fail with it
	hits    map[string]hit
	p       *Puller
	now     time.Time
}

// newPullEnv: a profile linked to MyShows as «tester»; shows 11..14 map
// through IMDb ids to series 101, movie 102, series 103, series 104.
func newPullEnv(t *testing.T) *pullEnv {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	r, err := d.Exec(`INSERT INTO users(name, avatar) VALUES('u', 'a')`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := r.LastInsertId()
	e := &pullEnv{t: t, acc: &accounts.Store{DB: d, Key: bytes.Repeat([]byte{7}, 32)}, st: &tracking.Store{DB: d}, uid: uid,
		remote: &fakeProfile{}, shows: &fakeShows{imdb: map[int64]int64{11: 1001, 12: 1002, 13: 1003, 14: 1004, 21: 2001, 22: 2002}},
		now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	if err := e.acc.SetNickname(context.Background(), uid, Provider, "tester", 0); err != nil {
		t.Fatal(err)
	}
	e.hits = map[string]hit{"tt0001001": {101, "series"}, "tt0001002": {102, "movie"}, "tt0001003": {103, "series"}, "tt0001004": {104, "series"}}
	m := &Mapper{DB: d, Client: e.shows, Now: func() time.Time { return e.now },
		Find: func(_ context.Context, imdb string) (int64, string, bool, error) {
			e.finds++
			if e.findErr != nil {
				return 0, "", false, e.findErr
			}
			h, ok := e.hits[imdb]
			return h.id, h.kind, ok, nil
		}}
	e.p = &Puller{Accounts: e.acc, Tracker: trackerOf{e.st}, Remote: e.remote, Map: m,
		Now: func() time.Time { return e.now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return e
}

type hit struct {
	id   int64
	kind string
}

func entry(show int64, status string, rating int) Entry {
	return Entry{ShowID: show, Title: "Сериал", TitleOriginal: "Show", Status: status, Rating: rating}
}

func (e *pullEnv) pull() Result {
	e.t.Helper()
	r, err := e.p.PullOne(context.Background(), e.uid)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *pullEnv) mark(kind string, tmdbID int64) *tracking.Mark {
	e.t.Helper()
	ctx := context.Background()
	ti, ok, err := e.st.TitleByTMDB(ctx, kind, tmdbID)
	if err != nil {
		e.t.Fatal(err)
	}
	if !ok {
		return nil
	}
	ms, err := e.st.Marks(ctx, e.uid)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, m := range ms {
		if m.TitleID == ti.ID {
			return &m
		}
	}
	return nil
}

// local writes a mark as if made in Antresol at at.
func (e *pullEnv) local(kind string, tmdbID int64, state tracking.MarkState, at time.Time) {
	e.t.Helper()
	ctx := context.Background()
	ti, err := e.st.EnsureTitle(ctx, kind, tmdbID, "x", "")
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: state, UpdatedAt: at}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *pullEnv) link() accounts.Link {
	e.t.Helper()
	l, ok, err := e.acc.Link(context.Background(), e.uid, Provider)
	if err != nil || !ok {
		e.t.Fatalf("link ok=%v err=%v", ok, err)
	}
	return l
}

func scoreOf(m *tracking.Mark) int {
	if m.Score == nil {
		return 0
	}
	return *m.Score
}

func TestPullImportsMapped(t *testing.T) {
	e := newPullEnv(t)
	e.remote.entries = []Entry{entry(11, "watching", 4), entry(12, "later", 0), entry(13, "finished", 5), entry(14, "cancelled", 1)}
	r := e.pull()
	if r.Imported != 4 || r.Updated != 0 || r.Unmapped != 0 {
		t.Fatalf("result %+v", r)
	}
	if len(e.remote.logins) != 1 || e.remote.logins[0] != "tester" {
		t.Fatalf("logins %v", e.remote.logins)
	}
	for _, c := range []struct {
		kind  string
		tmdb  int64
		state tracking.MarkState
		score int
	}{{"series", 101, tracking.Watching, 8}, {"movie", 102, tracking.Planned, 0}, {"series", 103, tracking.Completed, 10}, {"series", 104, tracking.Dropped, 2}} {
		m := e.mark(c.kind, c.tmdb)
		if m == nil || m.State != c.state || scoreOf(m) != c.score || m.Origin != Provider || !m.UpdatedAt.Equal(e.now) {
			t.Fatalf("%s %d: mark %+v", c.kind, c.tmdb, m)
		}
	}
	if m := e.mark("movie", 102); m.Score != nil {
		t.Fatalf("rating 0 must be no score, got %d", *m.Score)
	}
	if l := e.link(); l.Status != accounts.StatusOK || l.LastError != "" || !l.LastSync.Equal(e.now) || l.Unmapped != 0 {
		t.Fatalf("link %+v", l)
	}
}

func TestUnmappedShowsCounted(t *testing.T) {
	e := newPullEnv(t)
	// 20: no IMDb id; 21: TMDB does not know its IMDb id; 11: unknown status
	e.remote.entries = []Entry{entry(20, "watching", 0), entry(21, "finished", 3), entry(11, "rewatching", 0), entry(12, "later", 0)}
	r := e.pull()
	if r.Imported != 1 || r.Unmapped != 3 {
		t.Fatalf("result %+v", r)
	}
	if e.mark("series", 101) != nil {
		t.Fatal("an entry with an unknown status must not be guessed")
	}
	ms, _ := e.st.Marks(context.Background(), e.uid)
	if len(ms) != 1 {
		t.Fatalf("marks %+v", ms)
	}
	if l := e.link(); l.Unmapped != 3 || l.Status != accounts.StatusOK {
		t.Fatalf("link %+v", l)
	}
}

func TestUnchangedEntryNeverOverwrites(t *testing.T) {
	e := newPullEnv(t)
	e.remote.entries = []Entry{entry(11, "watching", 4), entry(12, "later", 0)}
	e.pull()
	// changed here after the first pull (one an hour later, one 5 minutes
	// before the next pull)
	e.local("series", 101, tracking.Completed, e.now.Add(time.Hour))
	e.local("movie", 102, tracking.Dropped, e.now.Add(-time.Hour))
	e.now = e.now.Add(2 * time.Hour)
	r := e.pull()
	if r.Imported+r.Updated != 0 {
		t.Fatalf("result %+v", r)
	}
	if m := e.mark("series", 101); m.State != tracking.Completed || m.Origin != "antresol" || m.Score != nil {
		t.Fatalf("local change overwritten: %+v", m)
	}
	if m := e.mark("movie", 102); m.State != tracking.Dropped || m.Origin != "antresol" {
		t.Fatalf("local change overwritten: %+v", m)
	}
}

func TestChangedEntryApplies(t *testing.T) {
	e := newPullEnv(t)
	e.remote.entries = []Entry{entry(11, "watching", 4), entry(12, "later", 0)}
	first := e.now
	e.pull()
	e.local("series", 101, tracking.Completed, e.now.Add(time.Hour))
	e.now = e.now.Add(2 * time.Hour)
	// 11: status and rating changed on MyShows; 12: rating only
	e.remote.entries = []Entry{entry(11, "cancelled", 2), entry(12, "later", 3)}
	r := e.pull()
	if r.Updated != 2 || r.Imported != 0 {
		t.Fatalf("result %+v", r)
	}
	if m := e.mark("series", 101); m.State != tracking.Dropped || scoreOf(m) != 4 || m.Origin != Provider || !m.UpdatedAt.Equal(e.now) {
		t.Fatalf("mark %+v", m)
	}
	if m := e.mark("movie", 102); m.State != tracking.Planned || scoreOf(m) != 6 || !m.UpdatedAt.After(first) {
		t.Fatalf("mark %+v", m)
	}
	// a third pull of the same data changes nothing
	e.now = e.now.Add(time.Hour)
	if r := e.pull(); r.Imported+r.Updated != 0 {
		t.Fatalf("result %+v", r)
	}
}

func TestRemovalDeletesOnlyUntouchedMyShowsMark(t *testing.T) {
	e := newPullEnv(t)
	e.remote.entries = []Entry{entry(11, "watching", 0), entry(12, "later", 0), entry(13, "finished", 0)}
	e.pull()
	e.local("movie", 102, tracking.Watching, e.now.Add(time.Minute)) // changed here
	e.now = e.now.Add(time.Hour)
	e.remote.entries = []Entry{entry(13, "finished", 0)}
	r := e.pull()
	if r.Updated != 1 {
		t.Fatalf("result %+v", r)
	}
	if m := e.mark("series", 101); m != nil {
		t.Fatalf("untouched MyShows mark not removed: %+v", m)
	}
	if m := e.mark("movie", 102); m == nil || m.State != tracking.Watching {
		t.Fatalf("a mark changed here must stay: %+v", m)
	}
	if m := e.mark("series", 103); m == nil {
		t.Fatal("a listed title lost its mark")
	}
	rs, err := e.acc.Rates(context.Background(), e.uid, Provider)
	if err != nil || len(rs) != 1 || rs[0].TargetID != 13 {
		t.Fatalf("twins %+v err=%v", rs, err)
	}
}

func TestEmptyListRemovesNothing(t *testing.T) {
	e := newPullEnv(t)
	e.remote.entries = []Entry{entry(11, "watching", 0)}
	e.pull()
	e.now = e.now.Add(time.Hour)
	e.remote.entries = []Entry{}
	if r := e.pull(); r.Updated != 0 {
		t.Fatalf("result %+v", r)
	}
	if e.mark("series", 101) == nil {
		t.Fatal("an empty list must not read as everything removed")
	}
	if l := e.link(); l.Status != accounts.StatusOK {
		t.Fatalf("link %+v", l)
	}
	if rs, _ := e.acc.Rates(context.Background(), e.uid, Provider); len(rs) != 1 {
		t.Fatalf("twins %+v", rs)
	}
}

// anyAnime says every title is an anime Shikimori can hold.
type anyAnime struct{}

func (anyAnime) Resolve(context.Context, animemap.Title) (animemap.Entry, bool, error) {
	return animemap.Entry{}, false, nil
}
func (anyAnime) HasTMDB(context.Context, string, int64) (bool, error) { return true, nil }
func (anyAnime) FirstAnime(context.Context, string, int64) (animemap.Entry, bool, error) {
	return animemap.Entry{}, false, nil
}

// What MyShows says goes to Shikimori (accounts reconcile.go): its marks
// are queued there, its removals are not, and nothing is queued for
// MyShows (read only).
func TestMyShowsChangesGoToShikimori(t *testing.T) {
	e := newPullEnv(t)
	ctx := context.Background()
	if err := e.acc.SetNickname(ctx, e.uid, accounts.Shikimori, "nick", 42); err != nil {
		t.Fatal(err)
	}
	if err := e.acc.SetWriteState(ctx, e.uid, accounts.Shikimori, "enabled"); err != nil {
		t.Fatal(err)
	}
	sy := &accounts.Syncer{Accounts: e.acc, Tracking: trackerOf{e.st}, Map: anyAnime{}, Now: func() time.Time { return e.now },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	e.st.OnMarkChanged = sy.MarkChanged
	e.p.Lock = sy.UserLock
	e.remote.entries = []Entry{entry(11, "watching", 3), entry(12, "later", 0)}
	e.pull()
	if q, _ := e.acc.Outbox(ctx, e.uid, accounts.Shikimori); len(q) != 2 || q[0].Op != "upsert" || q[1].Op != "upsert" {
		t.Fatalf("first pull not queued for Shikimori: %+v", q)
	}
	if _, err := e.acc.DB.Exec(`DELETE FROM account_outbox`); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(time.Hour)
	e.remote.entries = []Entry{entry(11, "finished", 5)} // a change and a removal
	if r := e.pull(); r.Updated != 2 {
		t.Fatalf("result %+v", r)
	}
	changed, _, _ := e.st.TitleByTMDB(ctx, "series", 101)
	if q, _ := e.acc.Outbox(ctx, e.uid, accounts.Shikimori); len(q) != 1 || q[0].TitleID != changed.ID || q[0].Op != "upsert" {
		t.Fatalf("want only the change queued (no delete): %+v", q)
	}
	if q, _ := e.acc.Outbox(ctx, e.uid, Provider); len(q) != 0 {
		t.Fatalf("myshows outbox %+v", q)
	}
	if _, err := e.acc.DB.Exec(`DELETE FROM account_outbox`); err != nil {
		t.Fatal(err)
	}
	// control: a change made here is queued for Shikimori
	e.local("series", 101, tracking.Dropped, e.now.Add(time.Minute))
	if q, _ := e.acc.Outbox(ctx, e.uid, accounts.Shikimori); len(q) != 1 {
		t.Fatalf("antresol change not queued: %+v", q)
	}
	if q, _ := e.acc.Outbox(ctx, e.uid, Provider); len(q) != 0 {
		t.Fatalf("myshows outbox %+v", q)
	}
}

func TestBadLoginKeepsMarks(t *testing.T) {
	e := newPullEnv(t)
	e.remote.entries = []Entry{entry(11, "watching", 0)}
	e.pull()
	for _, c := range []struct {
		err    error
		status string
		msg    string
	}{
		{ErrNotFound, accounts.StatusNotFound, "Пользователь MyShows не найден"},
		{ErrPrivate, accounts.StatusPrivate, "Профиль MyShows скрыт"},
		{ErrRateLimited, accounts.StatusError, "MyShows не отвечает, попробуйте позже"},
		{errors.New("boom"), accounts.StatusError, "MyShows не отвечает, попробуйте позже"},
	} {
		e.now = e.now.Add(time.Hour)
		e.remote.entries, e.remote.err = nil, c.err
		if _, err := e.p.PullOne(context.Background(), e.uid); !errors.Is(err, c.err) {
			t.Fatalf("err %v want %v", err, c.err)
		}
		if l := e.link(); l.Status != c.status || l.LastError != c.msg || !l.LastSync.Equal(e.now) {
			t.Fatalf("%v: link %+v", c.err, l)
		}
		if m := e.mark("series", 101); m == nil || m.State != tracking.Watching {
			t.Fatalf("%v: mark %+v", c.err, m)
		}
		if rs, _ := e.acc.Rates(context.Background(), e.uid, Provider); len(rs) != 1 {
			t.Fatalf("%v: twins %+v", c.err, rs)
		}
	}
}

func TestMappingCachedAcrossPulls(t *testing.T) {
	e := newPullEnv(t)
	e.remote.entries = []Entry{entry(11, "watching", 0), entry(12, "later", 0), entry(20, "later", 0)} // 20: a miss
	e.pull()
	if e.shows.calls != 3 || e.finds != 2 {
		t.Fatalf("first pull: show calls %d finds %d", e.shows.calls, e.finds)
	}
	e.now = e.now.Add(time.Hour)
	e.remote.entries = append(e.remote.entries, entry(13, "finished", 0))
	r := e.pull()
	if e.shows.calls != 4 || e.finds != 3 || len(e.remote.logins) != 2 {
		t.Fatalf("second pull: show calls %d finds %d list reads %d", e.shows.calls, e.finds, len(e.remote.logins))
	}
	if r.Imported != 1 || r.Unmapped != 1 {
		t.Fatalf("result %+v", r)
	}
}

func TestRepeatedLookupErrorsStopPull(t *testing.T) {
	e := newPullEnv(t)
	e.remote.entries = []Entry{entry(11, "watching", 0)}
	e.pull()
	e.now = e.now.Add(time.Hour)
	// 11 changed (needs no lookup: its twin knows the title); five new
	// shows all fail to look up
	e.shows.fail = true
	e.remote.entries = []Entry{entry(11, "finished", 0), entry(12, "later", 0), entry(13, "later", 0), entry(14, "later", 0), entry(21, "later", 0), entry(22, "later", 0)}
	if _, err := e.p.PullOne(context.Background(), e.uid); err == nil {
		t.Fatal("want an error")
	}
	if e.shows.calls != 1+3 { // 1st pull: 11; then three failures
		t.Fatalf("show calls %d: the pull must stop after 3 failures in a row", e.shows.calls)
	}
	if l := e.link(); l.Status != accounts.StatusError || l.LastError != "MyShows не отвечает, попробуйте позже" {
		t.Fatalf("link %+v", l)
	}
	if m := e.mark("series", 101); m == nil || m.State != tracking.Watching {
		t.Fatalf("an aborted pull changed a mark: %+v", m)
	}
}

func TestPlainLookupErrorsDoNotStopPull(t *testing.T) {
	e := newPullEnv(t)
	e.remote.entries = []Entry{entry(11, "watching", 0)}
	e.pull()
	e.now = e.now.Add(time.Hour)
	e.shows.failPlain = true
	e.remote.entries = []Entry{entry(11, "finished", 0), entry(12, "later", 0), entry(13, "later", 0), entry(14, "later", 0), entry(21, "later", 0)}
	r := e.pull()
	if e.shows.calls != 1+4 || r.Unmapped != 0 || r.Updated != 1 { // failed lookups are not "unmapped"
		t.Fatalf("show calls %d result %+v", e.shows.calls, r)
	}
	if m := e.mark("series", 101); m == nil || m.State != tracking.Completed {
		t.Fatalf("mark %+v", m)
	}
	if l := e.link(); l.Status != accounts.StatusOK {
		t.Fatalf("link %+v", l)
	}
	// not cached: looked up again next pull
	e.shows.failPlain = false
	e.now = e.now.Add(time.Hour)
	if r := e.pull(); r.Imported != 3 || r.Unmapped != 1 {
		t.Fatalf("result %+v", r)
	}
}

func TestFirstPullKeepsExistingMarks(t *testing.T) {
	e := newPullEnv(t)
	ctx := context.Background()
	// a Shikimori-origin mark and one made here, both before the link
	ti, err := e.st.EnsureTitle(ctx, "series", 101, "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.Completed, UpdatedAt: e.now.Add(-time.Hour), Origin: "shikimori"}); err != nil {
		t.Fatal(err)
	}
	e.local("movie", 102, tracking.Dropped, e.now.Add(-time.Hour))
	e.remote.entries = []Entry{entry(11, "watching", 4), entry(12, "later", 0), entry(13, "finished", 0)}
	if r := e.pull(); r.Imported != 1 || r.Updated != 0 {
		t.Fatalf("first pull %+v", r)
	}
	if m := e.mark("series", 101); m.State != tracking.Completed || m.Origin != "shikimori" || m.Score != nil {
		t.Fatalf("Shikimori mark replaced: %+v", m)
	}
	if m := e.mark("movie", 102); m.State != tracking.Dropped || m.Origin != "antresol" {
		t.Fatalf("local mark replaced: %+v", m)
	}
	if m := e.mark("series", 103); m == nil || m.Origin != Provider {
		t.Fatalf("unmarked title not filled: %+v", m)
	}
	if !e.link().FirstPullDone {
		t.Fatal("first pull not recorded")
	}
	// the kept titles' entries are seen: the same data changes nothing
	e.now = e.now.Add(time.Hour)
	if r := e.pull(); r.Imported+r.Updated != 0 {
		t.Fatalf("second pull %+v", r)
	}
	// a later change on MyShows applies
	e.now = e.now.Add(time.Hour)
	e.remote.entries = []Entry{entry(11, "cancelled", 4), entry(12, "later", 0), entry(13, "finished", 0)}
	if r := e.pull(); r.Updated != 1 {
		t.Fatalf("third pull %+v", r)
	}
	if m := e.mark("series", 101); m.State != tracking.Dropped || scoreOf(m) != 8 || m.Origin != Provider || !m.UpdatedAt.Equal(e.now) {
		t.Fatalf("MyShows change not applied: %+v", m)
	}
	if m := e.mark("movie", 102); m.State != tracking.Dropped || m.Origin != "antresol" {
		t.Fatalf("unchanged entry overwrote: %+v", m)
	}
}

func TestFirstPullLookupErrorsKeepMarks(t *testing.T) {
	for _, outage := range []bool{false, true} {
		e := newPullEnv(t)
		e.local("series", 101, tracking.Completed, e.now.Add(-time.Hour))
		e.local("movie", 102, tracking.Dropped, e.now.Add(-time.Hour))
		// two lookups fail (an outage, or a plain error): below the stop
		e.shows.fail, e.shows.failPlain = outage, !outage
		e.remote.entries = []Entry{entry(11, "watching", 4), entry(12, "later", 0)}
		if r := e.pull(); r.Imported+r.Updated+r.Unmapped != 0 {
			t.Fatalf("outage=%v first pull %+v", outage, r)
		}
		if e.link().FirstPullDone {
			t.Fatalf("outage=%v: first pull recorded despite failed lookups", outage)
		}
		e.shows.fail, e.shows.failPlain = false, false
		e.now = e.now.Add(time.Hour)
		if r := e.pull(); r.Imported+r.Updated != 0 {
			t.Fatalf("outage=%v clean pull %+v", outage, r)
		}
		if m := e.mark("series", 101); m.State != tracking.Completed || m.Origin != "antresol" {
			t.Fatalf("outage=%v: mark replaced %+v", outage, m)
		}
		if m := e.mark("movie", 102); m.State != tracking.Dropped || m.Origin != "antresol" {
			t.Fatalf("outage=%v: mark replaced %+v", outage, m)
		}
		if !e.link().FirstPullDone {
			t.Fatalf("outage=%v: clean pull not recorded", outage)
		}
		// a real MyShows change later applies
		e.now = e.now.Add(time.Hour)
		e.remote.entries = []Entry{entry(11, "cancelled", 4), entry(12, "later", 0)}
		if r := e.pull(); r.Updated != 1 {
			t.Fatalf("outage=%v change pull %+v", outage, r)
		}
		if m := e.mark("series", 101); m.State != tracking.Dropped || m.Origin != Provider {
			t.Fatalf("outage=%v: change not applied %+v", outage, m)
		}
	}
}

func TestMissResolvingLaterKeepsMark(t *testing.T) {
	e := newPullEnv(t)
	e.local("series", 105, tracking.Completed, e.now.Add(-time.Hour))
	// 21 → series 105 (marked here), 22 → series 106 (no mark): both
	// unknown to TMDB at first
	e.remote.entries = []Entry{entry(21, "watching", 3), entry(22, "watching", 3)}
	if r := e.pull(); r.Unmapped != 2 {
		t.Fatalf("first pull %+v", r)
	}
	if !e.link().FirstPullDone {
		t.Fatal("a pull with only misses is complete")
	}
	e.hits["tt0002001"] = hit{105, "series"}
	e.hits["tt0002002"] = hit{106, "series"}
	e.now = e.now.Add(31 * 24 * time.Hour)
	if r := e.pull(); r.Imported != 1 || r.Updated != 0 {
		t.Fatalf("resolving pull %+v", r)
	}
	if m := e.mark("series", 105); m.State != tracking.Completed || m.Origin != "antresol" {
		t.Fatalf("mark replaced when its entry mapped later: %+v", m)
	}
	if m := e.mark("series", 106); m == nil || m.State != tracking.Watching || m.Origin != Provider {
		t.Fatalf("unmarked title not filled: %+v", m)
	}
	e.now = e.now.Add(time.Hour)
	e.remote.entries = []Entry{entry(21, "finished", 3), entry(22, "watching", 3)}
	if r := e.pull(); r.Updated != 1 {
		t.Fatalf("change pull %+v", r)
	}
	if m := e.mark("series", 105); m.State != tracking.Completed || m.Origin != Provider || scoreOf(m) != 6 {
		t.Fatalf("change not applied: %+v", m)
	}
}

func TestUnlinkClearsSeenEntries(t *testing.T) {
	e := newPullEnv(t)
	e.remote.entries = []Entry{entry(11, "watching", 0), entry(20, "later", 0)}
	e.pull()
	count := func() (n int) {
		e.acc.DB.QueryRow(`SELECT COUNT(*) FROM myshows_entries WHERE user_id=?`, e.uid).Scan(&n)
		return n
	}
	if count() != 2 {
		t.Fatalf("seen entries %d", count())
	}
	if err := e.acc.Unlink(context.Background(), e.uid, Provider); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatalf("unlink kept %d seen entries", count())
	}
}

// blockingMap holds every lookup until released.
type blockingMap struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingMap) Resolve(ctx context.Context, showID int64) (int64, string, bool, error) {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
	case <-ctx.Done():
		return 0, "", false, ctx.Err()
	}
	return 101, "series", true, nil
}

func TestLookupPhaseDoesNotHoldLock(t *testing.T) {
	e := newPullEnv(t)
	var mu sync.Mutex
	e.p.Lock = func(int64) *sync.Mutex { return &mu }
	bm := &blockingMap{started: make(chan struct{}), release: make(chan struct{})}
	e.p.Map = bm
	e.remote.entries = []Entry{entry(11, "watching", 0)}
	done := make(chan error, 1)
	go func() { _, err := e.p.PullOne(context.Background(), e.uid); done <- err }()
	select {
	case <-bm.started:
	case err := <-done:
		t.Fatalf("pull ended before any lookup: %v", err)
	}
	if !mu.TryLock() {
		close(bm.release)
		<-done
		t.Fatal("the profile lock is held while shows are looked up")
	}
	mu.Unlock()
	close(bm.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if m := e.mark("series", 101); m == nil {
		t.Fatal("pull did not write after the lookups")
	}
}

func TestRelinkDuringLookupWritesNothing(t *testing.T) {
	e := newPullEnv(t)
	ctx := context.Background()
	bm := &blockingMap{started: make(chan struct{}), release: make(chan struct{})}
	e.p.Map = bm
	e.remote.entries = []Entry{entry(11, "watching", 0)}
	done := make(chan error, 1)
	go func() { _, err := e.p.PullOne(ctx, e.uid); done <- err }()
	select {
	case <-bm.started:
	case err := <-done:
		t.Fatalf("pull ended before any lookup: %v", err)
	}
	// unlinked and linked to another login while the shows are looked up
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.acc.Unlink(ctx, e.uid, Provider))
	must(e.acc.SetNickname(ctx, e.uid, Provider, "other", 0))
	fresh := e.link()
	close(bm.release)
	if err := <-done; err == nil {
		t.Fatal("want an error: the link changed")
	}
	if ms, _ := e.st.Marks(ctx, e.uid); len(ms) != 0 {
		t.Fatalf("old login's list written into the new link: %+v", ms)
	}
	var rows int
	e.acc.DB.QueryRow(`SELECT COUNT(*) FROM myshows_entries WHERE user_id=?`, e.uid).Scan(&rows)
	if rs, _ := e.acc.Rates(ctx, e.uid, Provider); len(rs) != 0 || rows != 0 {
		t.Fatalf("twins %+v seen rows %d", rs, rows)
	}
	if l := e.link(); l.FirstPullDone || l.Status != fresh.Status || !l.LastSync.Equal(fresh.LastSync) || l.Nickname != "other" {
		t.Fatalf("link %+v", l)
	}
}

// An unlink while the shows are looked up leaves no marks, twins or entries.
func TestUnlinkDuringLookupWritesNothing(t *testing.T) {
	e := newPullEnv(t)
	ctx := context.Background()
	bm := &blockingMap{started: make(chan struct{}), release: make(chan struct{})}
	e.p.Map = bm
	e.remote.entries = []Entry{entry(11, "watching", 0)}
	done := make(chan error, 1)
	go func() { _, err := e.p.PullOne(ctx, e.uid); done <- err }()
	select {
	case <-bm.started:
	case err := <-done:
		t.Fatalf("pull ended before any lookup: %v", err)
	}
	if err := e.acc.Unlink(ctx, e.uid, Provider); err != nil {
		t.Fatal(err)
	}
	close(bm.release)
	if err := <-done; err == nil {
		t.Fatal("want an error: the link is gone")
	}
	if ms, _ := e.st.Marks(ctx, e.uid); len(ms) != 0 {
		t.Fatalf("marks written after unlink: %+v", ms)
	}
	var rows int
	e.acc.DB.QueryRow(`SELECT COUNT(*) FROM myshows_entries WHERE user_id=?`, e.uid).Scan(&rows)
	if rs, _ := e.acc.Rates(ctx, e.uid, Provider); len(rs) != 0 || rows != 0 {
		t.Fatalf("twins %+v seen rows %d", rs, rows)
	}
}

// seqProfile serves lists in turn (the last one thereafter).
type seqProfile struct {
	mu    sync.Mutex
	lists [][]Entry
	reads int
}

func (s *seqProfile) ProfileShows(context.Context, string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.lists[min(s.reads, len(s.lists)-1)]
	s.reads++
	return l, nil
}

// firstBlocks holds only the first lookup until released.
type firstBlocks struct {
	blockingMap
	n int32
	m sync.Mutex
}

func (f *firstBlocks) Resolve(ctx context.Context, showID int64) (int64, string, bool, error) {
	f.m.Lock()
	f.n++
	first := f.n == 1
	f.m.Unlock()
	if first {
		return f.blockingMap.Resolve(ctx, showID)
	}
	return 101, "series", true, nil
}

func TestConcurrentPullsNeverFlipBack(t *testing.T) {
	e := newPullEnv(t)
	ctx := context.Background()
	sp := &seqProfile{lists: [][]Entry{{entry(11, "watching", 0)}, {entry(11, "finished", 0)}}}
	e.p.Remote = sp
	fb := &firstBlocks{blockingMap: blockingMap{started: make(chan struct{}), release: make(chan struct{})}}
	e.p.Map = fb
	first := make(chan error, 1)
	go func() { _, err := e.p.PullOne(ctx, e.uid); first <- err }()
	select {
	case <-fb.started:
	case err := <-first:
		t.Fatalf("pull ended before any lookup: %v", err)
	}
	second := make(chan error, 1)
	go func() { _, err := e.p.PullOne(ctx, e.uid); second <- err }()
	time.Sleep(100 * time.Millisecond) // let the second pull run as far as it can
	close(fb.release)
	for _, c := range []chan error{first, second} {
		if err := <-c; err != nil {
			t.Fatal(err)
		}
	}
	if m := e.mark("series", 101); m == nil || m.State != tracking.Completed {
		t.Fatalf("an older list landed last: %+v", m)
	}
	var status string
	e.acc.DB.QueryRow(`SELECT status FROM myshows_entries WHERE user_id=? AND show_id=11`, e.uid).Scan(&status)
	if status != "finished" || sp.reads != 2 {
		t.Fatalf("seen status %q, list reads %d", status, sp.reads)
	}
}

func TestNewLoginResetsLink(t *testing.T) {
	e := newPullEnv(t)
	ctx := context.Background()
	e.remote.entries = []Entry{entry(11, "watching", 0), entry(20, "later", 0)}
	e.pull()
	count := func() (n int) {
		e.acc.DB.QueryRow(`SELECT COUNT(*) FROM myshows_entries WHERE user_id=?`, e.uid).Scan(&n)
		return n
	}
	// the same login again: nothing reset
	if err := e.acc.SetNickname(ctx, e.uid, Provider, "tester", 0); err != nil {
		t.Fatal(err)
	}
	if l := e.link(); !l.FirstPullDone || count() != 2 {
		t.Fatalf("same login reset the link: %+v rows %d", l, count())
	}
	if err := e.acc.SetNickname(ctx, e.uid, Provider, "other", 0); err != nil {
		t.Fatal(err)
	}
	l := e.link()
	rs, _ := e.acc.Rates(ctx, e.uid, Provider)
	if l.FirstPullDone || !l.LastSync.IsZero() || l.Status != accounts.StatusOK || l.LastError != "" || l.Unmapped != 0 || l.Nickname != "other" || len(rs) != 0 || count() != 0 {
		t.Fatalf("new login not reset: %+v twins %+v rows %d", l, rs, count())
	}
	if e.mark("series", 101) == nil {
		t.Fatal("a relink must not delete the profile's marks")
	}
}

// The tester's case from the MyShows side: Shikimori has a title «planned»,
// MyShows has it «finished». Linking MyShows is an import, and finished is
// further: the mark becomes completed (accounts.ImportWins). A less
// advanced MyShows entry never replaces a mark (TestFirstPullKeepsExistingMarks).
func TestFirstPullFinishedUpgradesPlanned(t *testing.T) {
	e := newPullEnv(t)
	ctx := context.Background()
	ti, err := e.st.EnsureTitle(ctx, "movie", 102, "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.Planned, UpdatedAt: e.now.Add(-time.Hour), Origin: "shikimori"}); err != nil {
		t.Fatal(err)
	}
	e.remote.entries = []Entry{entry(12, "finished", 5)}
	if r := e.pull(); r.Updated != 1 {
		t.Fatalf("first pull %+v", r)
	}
	if m := e.mark("movie", 102); m.State != tracking.Completed || m.Origin != Provider || scoreOf(m) != 10 {
		t.Fatalf("finished did not win: %+v", m)
	}
	// settled: the same list changes nothing
	e.now = e.now.Add(time.Hour)
	if r := e.pull(); r.Imported+r.Updated != 0 {
		t.Fatalf("second pull %+v", r)
	}
}

// A watched count that changed on MyShows with the status unchanged writes
// no mark, so the other lists are told through EpisodesChanged.
func TestCountChangeTellsEpisodesChanged(t *testing.T) {
	e := newPullEnv(t)
	var told []int64
	e.p.EpisodesChanged = func(_ context.Context, uid, tid int64) { told = append(told, tid) }
	e.remote.entries = []Entry{{ShowID: 11, Title: "Сериал", Status: "watching", WatchedEpisodes: 3}}
	e.pull()
	if len(told) != 0 {
		t.Fatalf("first pull told %v", told)
	}
	e.now = e.now.Add(time.Hour)
	e.remote.entries = []Entry{{ShowID: 11, Title: "Сериал", Status: "watching", WatchedEpisodes: 4}}
	e.pull()
	ti, _, _ := e.st.TitleByTMDB(context.Background(), "series", 101)
	if len(told) != 1 || told[0] != ti.ID {
		t.Fatalf("told %v (want %d)", told, ti.ID)
	}
	tw, ok, _ := e.acc.Rate(context.Background(), e.uid, Provider, 11)
	if !ok || tw.RemoteEpisodes != 4 {
		t.Fatalf("count not stored %+v", tw)
	}
}
