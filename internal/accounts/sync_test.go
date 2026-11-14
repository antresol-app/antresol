package accounts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/animemap"
	"github.com/antresol-app/antresol/internal/tracking"
)

// fakeRemote serves a fixed anime list (or an error). With private set,
// an anonymous read is ErrRemoteForbidden (or, privateEmpty, an empty
// list) and only an access token not in rejected reads the list.
type fakeRemote struct {
	rates        []RemoteRate
	err          error
	calls        int
	accesses     []string // the token of every read ("" = anonymous)
	private      bool
	privateEmpty bool
	rejected     map[string]bool
}

func (f *fakeRemote) AnimeRates(_ context.Context, userID int64, access string) ([]RemoteRate, error) {
	f.calls++
	f.accesses = append(f.accesses, access)
	if access != "" && f.rejected[access] {
		return nil, ErrRemoteUnauthorized
	}
	if f.private && access == "" {
		if f.privateEmpty {
			return []RemoteRate{}, nil
		}
		return nil, ErrRemoteForbidden
	}
	return f.rates, f.err
}

// fakeMap maps MAL ids to fixed entries; HasTMDB answers from them. fail
// makes a MAL id's lookup error; onResolve runs inside every lookup.
type fakeMap struct {
	m         map[int64]animemap.Entry
	fail      map[int64]bool
	onResolve func()
}

func (f *fakeMap) Resolve(_ context.Context, a animemap.Title) (animemap.Entry, bool, error) {
	if f.onResolve != nil {
		f.onResolve()
	}
	if f.fail[a.MALID] {
		return animemap.Entry{}, false, errors.New("lookup failed")
	}
	e, ok := f.m[a.MALID]
	return e, ok, nil
}

func (f *fakeMap) HasTMDB(_ context.Context, kind string, tmdbID int64) (bool, error) {
	for _, e := range f.m {
		if e.Kind == kind && e.TMDBID == tmdbID {
			return true, nil
		}
	}
	return false, nil
}

// trackerOf adapts a real tracking.Store (the same adapter main.go wires).
type trackerOf struct{ st *tracking.Store }

func (a trackerOf) EnsureTitle(ctx context.Context, kind string, tmdbID int64, name string) (TitleRef, error) {
	t, err := a.st.EnsureTitle(ctx, kind, tmdbID, name, "")
	return refOf(t), err
}

func (a trackerOf) Title(ctx context.Context, id int64) (TitleRef, bool, error) {
	t, ok, err := a.st.TitleByID(ctx, id)
	return refOf(t), ok, err
}

func (a trackerOf) SetShikimoriID(ctx context.Context, titleID, shikimoriID, anidbID int64) error {
	var an *int64
	if anidbID > 0 {
		an = &anidbID
	}
	return a.st.SetExternalIDs(ctx, titleID, &shikimoriID, nil, an)
}

func (a trackerOf) Marks(ctx context.Context, uid int64) ([]Mark, error) {
	ms, err := a.st.Marks(ctx, uid)
	out := make([]Mark, 0, len(ms))
	for _, m := range ms {
		out = append(out, Mark{UserID: m.UserID, TitleID: m.TitleID, State: string(m.State), Score: m.Score, UpdatedAt: m.UpdatedAt, Origin: m.Origin})
	}
	return out, err
}

func (a trackerOf) Mark(ctx context.Context, uid, titleID int64) (Mark, bool, error) {
	ms, err := a.Marks(ctx, uid)
	for _, m := range ms {
		if m.TitleID == titleID {
			return m, true, err
		}
	}
	return Mark{}, false, err
}

func (a trackerOf) SetMark(ctx context.Context, m Mark) error {
	return a.st.SetMark(ctx, tracking.Mark{UserID: m.UserID, TitleID: m.TitleID, State: tracking.MarkState(m.State), Score: m.Score, UpdatedAt: m.UpdatedAt, Origin: m.Origin})
}

func (a trackerOf) DeleteMark(ctx context.Context, uid, titleID int64, origin string) error {
	return a.st.DeleteMarkFrom(ctx, uid, titleID, origin)
}

func refOf(t tracking.Title) TitleRef {
	r := TitleRef{ID: t.ID, Kind: t.Kind, TMDBID: t.TMDBID}
	if t.ShikimoriID != nil {
		r.ShikimoriID = *t.ShikimoriID
	}
	return r
}

type syncEnv struct {
	t      *testing.T
	acc    *Store
	uid    int64
	st     *tracking.Store
	remote *fakeRemote
	sy     *Syncer
	amap   *fakeMap
	kicked []int64
	now    time.Time
}

// newSyncEnv: a linked profile (nickname, external id 42, write unasked),
// a map of MAL 1 → series 101, MAL 2 → movie 102, MAL 4 → series 104,
// MAL 21/22 → series 300 seasons 1/2.
func newSyncEnv(t *testing.T) *syncEnv {
	acc, uid := newStore(t)
	e := &syncEnv{t: t, acc: acc, uid: uid, st: &tracking.Store{DB: acc.DB}, remote: &fakeRemote{},
		now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	e.amap = &fakeMap{m: map[int64]animemap.Entry{
		1:  {MALID: 1, Kind: "series", TMDBID: 101, Season: 1},
		2:  {MALID: 2, Kind: "movie", TMDBID: 102},
		4:  {MALID: 4, Kind: "series", TMDBID: 104, Season: 1},
		21: {MALID: 21, Kind: "series", TMDBID: 300, Season: 1},
		22: {MALID: 22, Kind: "series", TMDBID: 300, Season: 2},
	}}
	e.sy = &Syncer{Accounts: acc, Tracking: trackerOf{e.st}, Remote: e.remote, Map: e.amap,
		Kick: func(id int64) { e.kicked = append(e.kicked, id) },
		Now:  func() time.Time { return e.now },
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil))}
	e.st.OnMarkChanged = e.sy.MarkChanged
	return e
}

func rate(id, anime int64, status string, score int, at time.Time) RemoteRate {
	return RemoteRate{ID: id, Status: status, Score: score, UpdatedAt: at, Anime: RemoteAnime{ID: anime, Name: "Anime", Russian: "Аниме", Kind: "tv"}}
}

func (e *syncEnv) pull() PullResult {
	e.t.Helper()
	r, err := e.sy.PullOne(context.Background(), e.uid)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// mark returns the profile's mark on (kind, tmdbID), nil when none.
func (e *syncEnv) mark(kind string, tmdbID int64) *tracking.Mark {
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
func (e *syncEnv) local(kind string, tmdbID int64, state tracking.MarkState, at time.Time) int64 {
	e.t.Helper()
	ctx := context.Background()
	ti, err := e.st.EnsureTitle(ctx, kind, tmdbID, "x", "")
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: state, UpdatedAt: at}); err != nil {
		e.t.Fatal(err)
	}
	return ti.ID
}

func (e *syncEnv) outbox() []OutboxItem {
	e.t.Helper()
	items, err := e.acc.Outbox(context.Background(), e.uid, Shikimori)
	if err != nil {
		e.t.Fatal(err)
	}
	return items
}

func (e *syncEnv) clearOutbox() {
	if _, err := e.acc.DB.Exec(`DELETE FROM account_outbox`); err != nil {
		e.t.Fatal(err)
	}
}

func TestPullImportsMappedRates(t *testing.T) {
	e := newSyncEnv(t)
	at := e.now.Add(-time.Hour)
	e.remote.rates = []RemoteRate{
		rate(11, 1, "watching", 8, at),
		rate(12, 2, "completed", 0, at),
		rate(13, 3, "planned", 0, at), // MAL 3: unmapped
	}
	r := e.pull()
	if r.Imported != 2 || r.Unmapped != 1 {
		t.Fatalf("%+v", r)
	}
	s := e.mark("series", 101)
	if s == nil || s.State != tracking.Watching || s.Score == nil || *s.Score != 8 || s.Origin != "shikimori" || !s.UpdatedAt.Equal(at) {
		t.Fatalf("series mark %+v", s)
	}
	m := e.mark("movie", 102)
	if m == nil || m.State != tracking.Completed || m.Score != nil || m.Origin != "shikimori" {
		t.Fatalf("movie mark %+v", m)
	}
	ti, _, _ := e.st.TitleByTMDB(context.Background(), "series", 101)
	if ti.ShikimoriID == nil || *ti.ShikimoriID != 1 {
		t.Fatalf("shikimori id %+v", ti.ShikimoriID)
	}
	if len(e.kicked) != 1 || e.kicked[0] != ti.ID {
		t.Fatalf("kicked %v", e.kicked)
	}
	tw, ok, _ := e.acc.Rate(context.Background(), e.uid, Shikimori, 1)
	if !ok || tw.TitleID != ti.ID || tw.RemoteStatus != "watching" || tw.RateID != 11 || !tw.RemoteUpdatedAt.Equal(at) {
		t.Fatalf("twin %+v %v", tw, ok)
	}
	l, _, _ := e.acc.Link(context.Background(), e.uid, Shikimori)
	if l.Status != "ok" || !l.LastSync.Equal(e.now) {
		t.Fatalf("link %+v", l)
	}
}

func TestRemoteWriteDoesNotEnqueue(t *testing.T) {
	e := newSyncEnv(t)
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, e.now.Add(-time.Hour))}
	e.pull()
	// a later remote change applied by a later pull: still nothing queued
	e.remote.rates = []RemoteRate{rate(11, 1, "completed", 9, e.now.Add(-time.Minute))}
	if r := e.pull(); r.Updated != 1 {
		t.Fatalf("%+v", r)
	}
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("remote write enqueued %+v", items)
	}
}

func TestFirstLinkImportsAndPushesOnlyMissing(t *testing.T) {
	e := newSyncEnv(t)
	// Antresol: series 101 watching (Shikimori has it completed), series
	// 104 only here (anime), series 900 only here (not anime)
	newer := e.now.Add(-time.Minute)
	e.local("series", 101, tracking.Watching, newer)
	only := e.local("series", 104, tracking.Planned, newer)
	e.local("series", 900, tracking.Watching, newer)
	e.clearOutbox() // marks made before the link existed
	e.remote.rates = []RemoteRate{rate(11, 1, "completed", 7, e.now.Add(-48*time.Hour))}
	r := e.pull()
	if r.Updated != 1 {
		t.Fatalf("%+v", r)
	}
	if m := e.mark("series", 101); m == nil || m.State != tracking.Completed || m.Origin != "shikimori" {
		t.Fatalf("remote did not win on first link: %+v", m)
	}
	items := e.outbox()
	if len(items) != 1 || items[0].TitleID != only || items[0].Op != "upsert" {
		t.Fatalf("outbox %+v (want only title %d)", items, only)
	}
	// declined write-back: nothing is queued on a first link
	e2 := newSyncEnv(t)
	e2.local("series", 104, tracking.Planned, newer)
	e2.clearOutbox()
	if err := e2.acc.SetWriteState(context.Background(), e2.uid, Shikimori, "declined"); err != nil {
		t.Fatal(err)
	}
	e2.pull()
	if items := e2.outbox(); len(items) != 0 {
		t.Fatalf("declined queued %+v", items)
	}
}

func TestLaterPullLastChangeWins(t *testing.T) {
	e := newSyncEnv(t)
	t0 := e.now.Add(-48 * time.Hour)
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, t0), rate(12, 2, "planned", 0, t0)}
	e.pull()
	// series 101 changed here after the remote's change: kept
	e.local("series", 101, tracking.Dropped, e.now.Add(-time.Hour))
	// both remote entries changed; 101's remote change is older than ours
	e.remote.rates = []RemoteRate{rate(11, 1, "completed", 0, e.now.Add(-2*time.Hour)), rate(12, 2, "completed", 6, e.now.Add(-30*time.Minute))}
	r := e.pull()
	if r.Updated != 1 {
		t.Fatalf("%+v", r)
	}
	if m := e.mark("series", 101); m == nil || m.State != tracking.Dropped || m.Origin != "antresol" {
		t.Fatalf("local newer lost: %+v", m)
	}
	if m := e.mark("movie", 102); m == nil || m.State != tracking.Completed || *m.Score != 6 {
		t.Fatalf("remote newer not applied: %+v", m)
	}
	// the echo of our own push (updated_at == pushed_at) is not re-imported
	ti, _, _ := e.st.TitleByTMDB(context.Background(), "series", 101)
	tw, _, _ := e.acc.Rate(context.Background(), e.uid, Shikimori, 1)
	if tw.TitleID != ti.ID {
		t.Fatalf("twin %+v", tw)
	}
	tw.PushedAt = e.now.Add(-10 * time.Minute)
	if err := e.acc.SetRate(context.Background(), tw); err != nil {
		t.Fatal(err)
	}
	e.remote.rates[0] = rate(11, 1, "watching", 0, tw.PushedAt)
	e.pull()
	if m := e.mark("series", 101); m == nil || m.State != tracking.Dropped {
		t.Fatalf("echo re-imported: %+v", m)
	}
}

func TestRemoteRemovalDeletesUntouchedMark(t *testing.T) {
	e := newSyncEnv(t)
	t0 := e.now.Add(-48 * time.Hour)
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, t0), rate(12, 2, "planned", 0, t0), rate(14, 4, "planned", 0, t0)}
	e.pull()
	// 102 touched here since; 101 untouched; both removed remotely
	e.local("movie", 102, tracking.Watching, e.now.Add(-time.Hour))
	e.clearOutbox()
	e.remote.rates = []RemoteRate{rate(14, 4, "planned", 0, t0)}
	e.pull()
	if m := e.mark("series", 101); m != nil {
		t.Fatalf("untouched mark kept: %+v", m)
	}
	if m := e.mark("movie", 102); m == nil || m.State != tracking.Watching {
		t.Fatalf("touched mark deleted: %+v", m)
	}
	// the mark touched here survives and goes back to the list
	mv, _, _ := e.st.TitleByTMDB(context.Background(), "movie", 102)
	if items := e.outbox(); len(items) != 1 || items[0].TitleID != mv.ID || items[0].Op != "upsert" {
		t.Fatalf("outbox %+v", items)
	}
	if _, ok, _ := e.acc.Rate(context.Background(), e.uid, Shikimori, 2); ok {
		t.Fatal("removed entry's twin kept")
	}
	// an empty list never deletes marks
	e.remote.rates = nil
	e.pull()
	if m := e.mark("series", 104); m == nil {
		t.Fatal("empty list deleted a mark")
	}
}

func TestPrivateListDoesNotTouchMarks(t *testing.T) {
	e := newSyncEnv(t)
	t0 := e.now.Add(-48 * time.Hour)
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, t0)}
	e.pull()
	for _, c := range []struct {
		err    error
		status string
	}{
		{ErrRemoteForbidden, "private"},
		{ErrRemoteNotFound, "not_found"},
		{errors.New("boom"), "error"},
	} {
		e.remote.err = c.err
		if _, err := e.sy.PullOne(context.Background(), e.uid); err == nil {
			t.Fatalf("%v: no error", c.err)
		}
		l, _, _ := e.acc.Link(context.Background(), e.uid, Shikimori)
		if l.Status != c.status || l.LastError == "" {
			t.Fatalf("%v: link %+v", c.err, l)
		}
		if c.status == "private" && l.LastError != "Список скрыт: войдите через Shikimori" {
			t.Fatalf("private message %q", l.LastError)
		}
		if m := e.mark("series", 101); m == nil || m.State != tracking.Watching {
			t.Fatalf("%v: mark touched %+v", c.err, m)
		}
	}
}

func TestMarkChangedEnqueuesOnlyAntresolAnime(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	tid := e.local("series", 101, tracking.Watching, e.now) // anime (mapped)
	e.local("series", 900, tracking.Watching, e.now)        // not anime
	items := e.outbox()
	if len(items) != 1 || items[0].TitleID != tid || items[0].Op != "upsert" {
		t.Fatalf("%+v", items)
	}
	if err := e.st.DeleteMark(ctx, e.uid, tid); err != nil {
		t.Fatal(err)
	}
	if items := e.outbox(); len(items) != 1 || items[0].Op != "delete" {
		t.Fatalf("%+v", items)
	}
	for _, ws := range []string{"enabled", "asked", "declined"} {
		e.clearOutbox()
		e.acc.SetWriteState(ctx, e.uid, Shikimori, ws)
		e.local("series", 101, tracking.Planned, e.now)
		if n := len(e.outbox()); (ws == "declined") != (n == 0) {
			t.Fatalf("write_state %s: %d queued", ws, n)
		}
	}
}

func TestPullDue(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		last     time.Time
		watching bool
		want     bool
	}{
		{time.Time{}, false, true},
		{now.Add(-16 * time.Minute), true, true},
		{now.Add(-16 * time.Minute), false, false},
		{now.Add(-61 * time.Minute), false, true},
		{now.Add(-5 * time.Minute), true, false},
	} {
		if got := pullDue(c.last, c.watching, now); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
}

func (e *syncEnv) titleID(kind string, tmdbID int64) int64 {
	e.t.Helper()
	ti, ok, err := e.st.TitleByTMDB(context.Background(), kind, tmdbID)
	if err != nil || !ok {
		e.t.Fatalf("no title %s/%d: %v", kind, tmdbID, err)
	}
	return ti.ID
}

func TestFractionalRemoteTimeIsNotAChange(t *testing.T) {
	e := newSyncEnv(t)
	t0 := e.now.Add(-48 * time.Hour)
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, t0.Add(500*time.Millisecond))}
	e.pull()
	// changed here within the same second as the remote's change, queued
	e.local("series", 101, tracking.Dropped, t0)
	if len(e.outbox()) != 1 {
		t.Fatal("local change not queued")
	}
	// the same remote entry again: no change, nothing overwritten
	e.pull()
	if m := e.mark("series", 101); m == nil || m.State != tracking.Dropped {
		t.Fatalf("unchanged entry won: %+v", m)
	}
	if len(e.outbox()) != 1 {
		t.Fatal("queued intent dropped")
	}
}

func TestFirstLinkWaitsForFullMapping(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	e.local("series", 104, tracking.Watching, e.now.Add(-time.Minute))
	// a title known by its Shikimori id, listed remotely but unmapped
	known := e.local("series", 500, tracking.Watching, e.now.Add(-time.Minute))
	sid := int64(7)
	if err := e.st.SetExternalIDs(ctx, known, &sid, nil, nil); err != nil {
		t.Fatal(err)
	}
	e.clearOutbox()
	e.amap.fail = map[int64]bool{4: true}
	e.remote.rates = []RemoteRate{rate(14, 4, "completed", 0, e.now.Add(-48*time.Hour)), rate(17, 7, "completed", 0, e.now.Add(-48*time.Hour))}
	e.pull()
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("enqueued despite a failed lookup: %+v", items)
	}
	if l, _, _ := e.acc.Link(ctx, e.uid, Shikimori); l.FirstPullDone {
		t.Fatal("first pull marked done after a failed lookup")
	}
	// the lookup works now: still the first pull, the remote wins
	e.amap.fail = nil
	e.pull()
	if m := e.mark("series", 104); m == nil || m.State != tracking.Completed {
		t.Fatalf("remote did not win: %+v", m)
	}
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("listed twins enqueued: %+v", items)
	}
	if l, _, _ := e.acc.Link(ctx, e.uid, Shikimori); !l.FirstPullDone {
		t.Fatal("first pull not recorded")
	}
}

func TestFirstPullIsRecorded(t *testing.T) {
	for _, name := range []string{"empty", "unmapped"} {
		e := newSyncEnv(t)
		only := e.local("series", 104, tracking.Planned, e.now.Add(-time.Minute))
		e.clearOutbox()
		// an empty list counts only when read with the owner's token
		must(t, e.acc.SetTokens(context.Background(), e.uid, Shikimori, Tokens{Access: "a0", Refresh: "r1", Expires: e.now.Add(20 * time.Hour)}))
		if name == "unmapped" {
			e.remote.rates = []RemoteRate{rate(13, 3, "planned", 0, e.now.Add(-time.Hour))}
		}
		e.pull()
		if items := e.outbox(); len(items) != 1 || items[0].TitleID != only {
			t.Fatalf("%s: first pull %+v", name, items)
		}
		e.clearOutbox()
		e.pull()
		if items := e.outbox(); len(items) != 0 {
			t.Fatalf("%s: second pull re-enqueued %+v", name, items)
		}
	}
}

func TestCoursAggregate(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name   string
		rates  []RemoteRate
		want   tracking.MarkState
		status string // remote status kept for cour 21
	}{
		{"S1 completed + S2 watching", []RemoteRate{rate(31, 21, "completed", 0, t0), rate(32, 22, "watching", 0, t0)}, tracking.Watching, "completed"},
		{"S1 watching + S2 planned newer", []RemoteRate{rate(31, 21, "watching", 0, t0), rate(32, 22, "planned", 0, t0.Add(time.Hour))}, tracking.Watching, "watching"},
		{"S1 rewatching + S2 completed", []RemoteRate{rate(31, 21, "rewatching", 0, t0), rate(32, 22, "completed", 0, t0)}, tracking.Watching, "rewatching"},
		{"both completed", []RemoteRate{rate(31, 21, "completed", 8, t0), rate(32, 22, "completed", 9, t0)}, tracking.Completed, "completed"},
		{"S1 completed + S2 planned", []RemoteRate{rate(31, 21, "completed", 0, t0), rate(32, 22, "planned", 0, t0)}, tracking.Planned, "completed"},
	} {
		e := newSyncEnv(t)
		e.remote.rates = c.rates
		e.pull()
		if m := e.mark("series", 300); m == nil || m.State != c.want {
			t.Fatalf("%s: %+v", c.name, m)
		}
		cours, err := e.acc.TitleRates(ctx, e.uid, Shikimori, e.titleID("series", 300))
		if err != nil || len(cours) != 2 || cours[0].TargetID != 21 || cours[0].TMDBSeason != 1 || cours[1].TMDBSeason != 2 || cours[0].RemoteStatus != c.status {
			t.Fatalf("%s: cours %+v %v", c.name, cours, err)
		}
	}
}

func TestCourRemovedWhileAnotherRemains(t *testing.T) {
	e := newSyncEnv(t)
	t0 := e.now.Add(-48 * time.Hour)
	e.remote.rates = []RemoteRate{rate(31, 21, "completed", 0, t0), rate(32, 22, "watching", 0, t0)}
	e.pull()
	e.remote.rates = []RemoteRate{rate(31, 21, "completed", 0, t0)}
	e.pull()
	if m := e.mark("series", 300); m == nil || m.State != tracking.Watching {
		t.Fatalf("mark changed by a cour's removal: %+v", m)
	}
	cours, _ := e.acc.TitleRates(context.Background(), e.uid, Shikimori, e.titleID("series", 300))
	if len(cours) != 1 || cours[0].TargetID != 21 {
		t.Fatalf("cours %+v", cours)
	}
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("outbox %+v", items)
	}
}

func TestEditDuringPullIsKept(t *testing.T) {
	e := newSyncEnv(t)
	t0 := e.now.Add(-48 * time.Hour)
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, t0)}
	e.pull()
	// the remote changed; while the list is mapped the profile edits the
	// mark (its time older than the remote's, so only the re-read saves it)
	e.remote.rates = []RemoteRate{rate(11, 1, "completed", 0, e.now.Add(-time.Minute))}
	e.amap.onResolve = func() {
		e.amap.onResolve = nil
		e.local("series", 101, tracking.Dropped, t0.Add(time.Second))
	}
	e.pull()
	if m := e.mark("series", 101); m == nil || m.State != tracking.Dropped {
		t.Fatalf("edit during pull clobbered: %+v", m)
	}
	if len(e.outbox()) != 1 {
		t.Fatal("the edit's queued intent was dropped")
	}
}

func (f *fakeMap) FirstAnime(_ context.Context, kind string, tmdbID int64) (animemap.Entry, bool, error) {
	var best animemap.Entry
	ok := false
	for _, e := range f.m {
		if e.Kind != kind || e.TMDBID != tmdbID || (kind == "series" && (e.Season != 1 || e.Offset != 0)) {
			continue
		}
		if !ok || e.MALID < best.MALID {
			best, ok = e, true
		}
	}
	return best, ok, nil
}

// A first pull retried after a failed lookup: cours stored by the failed
// attempt follow last-change-wins, only cours seen for the first time win
// outright.
func TestRetriedFirstPullHonoursKnownCours(t *testing.T) {
	e := newSyncEnv(t)
	t0 := e.now.Add(-48 * time.Hour)
	e.amap.fail = map[int64]bool{4: true}
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, t0), rate(14, 4, "completed", 0, t0)}
	e.pull()
	// changed here after the remote's change, before the retry
	e.local("series", 101, tracking.Dropped, e.now.Add(-time.Hour))
	e.local("series", 104, tracking.Watching, e.now.Add(-time.Hour))
	e.amap.fail = nil
	e.pull()
	if m := e.mark("series", 101); m == nil || m.State != tracking.Dropped {
		t.Fatalf("known cour won outright: %+v", m)
	}
	if m := e.mark("series", 104); m == nil || m.State != tracking.Completed {
		t.Fatalf("new cour did not win: %+v", m)
	}
}

// A mark changed here while the list was read is not deleted by the
// remote removal of its title.
func TestRemovalRereadsMark(t *testing.T) {
	e := newSyncEnv(t)
	t0 := e.now.Add(-48 * time.Hour)
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, t0), rate(14, 4, "planned", 0, t0)}
	e.pull()
	e.remote.rates = []RemoteRate{rate(14, 4, "planned", 0, t0)}
	e.amap.onResolve = func() {
		e.amap.onResolve = nil
		e.local("series", 101, tracking.OnHold, e.now)
	}
	e.pull()
	if m := e.mark("series", 101); m == nil || m.State != tracking.OnHold {
		t.Fatalf("edited mark deleted: %+v", m)
	}
}

// myshowsMark writes a mark as the MyShows puller would.
func (e *syncEnv) myshowsMark(kind string, tmdbID int64, state tracking.MarkState, at time.Time) int64 {
	e.t.Helper()
	ctx := context.Background()
	ti, err := e.st.EnsureTitle(ctx, kind, tmdbID, "x", "")
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: state, UpdatedAt: at, Origin: "myshows"}); err != nil {
		e.t.Fatal(err)
	}
	return ti.ID
}

func TestFirstLinkNeverPushesMyShowsMarks(t *testing.T) {
	e := newSyncEnv(t)
	newer := e.now.Add(-time.Minute)
	e.myshowsMark("series", 104, tracking.Planned, newer) // anime, read from MyShows
	only := e.local("movie", 102, tracking.Planned, newer)
	e.clearOutbox()
	e.remote.rates = []RemoteRate{rate(11, 1, "completed", 7, e.now.Add(-48*time.Hour))}
	e.pull()
	if items := e.outbox(); len(items) != 1 || items[0].TitleID != only {
		t.Fatalf("outbox %+v (want only title %d)", items, only)
	}
	if m := e.mark("series", 104); m == nil || m.Origin != "myshows" {
		t.Fatalf("MyShows mark changed: %+v", m)
	}
}

func TestRemovalNeverRequeuesMyShowsMark(t *testing.T) {
	e := newSyncEnv(t)
	t0 := e.now.Add(-48 * time.Hour)
	e.remote.rates = []RemoteRate{rate(11, 1, "watching", 0, t0), rate(14, 4, "planned", 0, t0)}
	e.pull()
	// MyShows changed series 101 since; Shikimori then drops it
	e.myshowsMark("series", 101, tracking.Completed, e.now.Add(-time.Hour))
	e.clearOutbox()
	e.remote.rates = []RemoteRate{rate(14, 4, "planned", 0, t0)}
	e.pull()
	if m := e.mark("series", 101); m == nil || m.State != tracking.Completed || m.Origin != "myshows" {
		t.Fatalf("newer MyShows mark lost: %+v", m)
	}
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("MyShows mark queued for Shikimori: %+v", items)
	}
}
