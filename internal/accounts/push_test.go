package accounts

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/animemap"
	"github.com/antresol-app/antresol/internal/tracking"
)

// fakeWrite is one write a fakeWriter received.
type fakeWrite struct {
	UserID, Anime, RateID int64
	W                     RateWrite
}

// fakeWriter records the writes; every answer carries updated_at at.
type fakeWriter struct {
	mu        sync.Mutex
	refreshes []string // refresh tokens it was given
	creates   []fakeWrite
	updates   []fakeWrite
	deletes   []int64
	nextID    int64
	at        time.Time
	writeErr  error         // every write fails with it
	delay     time.Duration // each refresh takes this long
	noUpdated bool          // write answers lack updated_at
	reread    int
	episodes  map[int64]int  // anime episode counts
	gone      map[int64]bool // rate ids removed remotely (updates: not found)
}

func (f *fakeWriter) AnimeEpisodes(_ context.Context, animeID int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.episodes[animeID], nil
}

func (f *fakeWriter) Refresh(_ context.Context, c OAuthClient, refresh string) (Tokens, error) {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if c.ID == "" || c.Secret == "" {
		return Tokens{}, errors.New("no client")
	}
	f.refreshes = append(f.refreshes, refresh)
	n := len(f.refreshes)
	return Tokens{Access: "a" + string(rune('0'+n)), Refresh: "r" + string(rune('1'+n)), Expires: f.at.Add(24 * time.Hour)}, nil
}

func (f *fakeWriter) answer(id int64) RateWritten {
	if f.noUpdated {
		return RateWritten{ID: id}
	}
	return RateWritten{ID: id, UpdatedAt: f.at}
}

func (f *fakeWriter) CreateRate(_ context.Context, access string, userID, animeID int64, w RateWrite) (RateWritten, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return RateWritten{}, f.writeErr
	}
	f.nextID++
	id := 500 + f.nextID
	f.creates = append(f.creates, fakeWrite{UserID: userID, Anime: animeID, RateID: id, W: w})
	return f.answer(id), nil
}

func (f *fakeWriter) UpdateRate(_ context.Context, access string, rateID int64, w RateWrite) (RateWritten, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return RateWritten{}, f.writeErr
	}
	if f.gone[rateID] {
		return RateWritten{}, ErrRemoteNotFound
	}
	f.updates = append(f.updates, fakeWrite{RateID: rateID, W: w})
	return f.answer(rateID), nil
}

func (f *fakeWriter) DeleteRate(_ context.Context, access string, rateID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.deletes = append(f.deletes, rateID)
	return nil
}

func (f *fakeWriter) RateUpdatedAt(_ context.Context, access string, rateID int64) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reread++
	return f.at, nil
}

type pushEnv struct {
	*syncEnv
	w        *fakeWriter
	watched  map[int64]map[[2]int]bool // by TMDB id
	episodes map[int64][]Episode       // by title id
}

// newPushEnv: newSyncEnv with write-back enabled, the first pull done, an
// OAuth client and fresh tokens; series 400 is one TMDB season split in
// two cours (MAL 31: S1 E1–12, MAL 32: S1 E13+).
func newPushEnv(t *testing.T) *pushEnv {
	e := &pushEnv{syncEnv: newSyncEnv(t), w: &fakeWriter{}, watched: map[int64]map[[2]int]bool{}, episodes: map[int64][]Episode{}}
	ctx := context.Background()
	e.w.at = e.now.Add(-time.Minute).Truncate(time.Second)
	e.amap.m[31] = animemap.Entry{MALID: 31, Kind: "series", TMDBID: 400, Season: 1}
	e.amap.m[32] = animemap.Entry{MALID: 32, Kind: "series", TMDBID: 400, Season: 1, Offset: 12}
	e.sy.Writer = e.w
	e.sy.OAuth = &OAuth{Store: e.acc, PublicURL: "https://media.example", EnvClientID: "cid", EnvClientSecret: "sec"}
	e.sy.Watched = func(_ context.Context, uid int64, ti TitleRef) (map[[2]int]bool, error) {
		return e.watched[ti.TMDBID], nil
	}
	e.sy.Episodes = func(_ context.Context, titleID int64) ([]Episode, error) { return e.episodes[titleID], nil }
	must(t, e.acc.SetWriteState(ctx, e.uid, Shikimori, "enabled"))
	must(t, e.acc.SetFirstPullDone(ctx, e.uid, Shikimori))
	must(t, e.acc.SetTokens(ctx, e.uid, Shikimori, Tokens{Access: "a0", Refresh: "r1", Expires: e.now.Add(20 * time.Hour)}))
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (e *pushEnv) push() {
	e.t.Helper()
	must(e.t, e.sy.PushOne(context.Background(), e.uid))
}

func aired(season, from, to int) []Episode {
	var out []Episode
	for n := from; n <= to; n++ {
		out = append(out, Episode{Season: season, Number: n, AirDate: "2026-01-01"})
	}
	return out
}

func watchedRange(season, from, to int) map[[2]int]bool {
	m := map[[2]int]bool{}
	for n := from; n <= to; n++ {
		m[[2]int{season, n}] = true
	}
	return m
}

func TestPushCreatesThenUpdatesRate(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	tid := e.local("series", 101, tracking.Watching, e.now.Add(-time.Hour))
	e.push()
	if len(e.w.creates) != 1 {
		t.Fatalf("creates %+v", e.w.creates)
	}
	c := e.w.creates[0]
	if c.UserID != 42 || c.Anime != 1 || c.W.Status != "watching" || c.W.Score == nil || *c.W.Score != 0 || c.W.Episodes != nil {
		t.Fatalf("create %+v %+v", c, c.W)
	}
	tw, ok, _ := e.acc.Rate(ctx, e.uid, Shikimori, 1)
	if !ok || tw.TitleID != tid || tw.RateID != c.RateID || tw.TMDBSeason != 1 || tw.RemoteStatus != "watching" || !tw.PushedAt.Equal(e.w.at) {
		t.Fatalf("twin %+v %v", tw, ok)
	}
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("outbox %+v", items)
	}
	// changed here: the same entry is updated
	score := 8
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: tid, State: tracking.Completed, Score: &score, UpdatedAt: e.now}))
	e.push()
	if len(e.w.creates) != 1 || len(e.w.updates) != 1 {
		t.Fatalf("creates %d updates %+v", len(e.w.creates), e.w.updates)
	}
	u := e.w.updates[0]
	if u.RateID != c.RateID || u.W.Status != "completed" || *u.W.Score != 8 {
		t.Fatalf("update %+v %+v", u, u.W)
	}
	// removed here: a completed entry stays (TestDeleteKeepsCompletedCours)
	must(t, e.st.DeleteMark(ctx, e.uid, tid))
	e.push()
	if len(e.w.deletes) != 0 || len(e.outbox()) != 0 {
		t.Fatalf("deletes %v outbox %+v", e.w.deletes, e.outbox())
	}
	// a planned entry removed here is deleted
	pid := e.local("series", 104, tracking.Planned, e.now.Add(-time.Hour))
	e.push()
	if len(e.w.creates) != 2 {
		t.Fatalf("creates %+v", e.w.creates)
	}
	must(t, e.st.DeleteMark(ctx, e.uid, pid))
	e.push()
	if len(e.w.deletes) != 1 || e.w.deletes[0] != e.w.creates[1].RateID {
		t.Fatalf("deletes %v", e.w.deletes)
	}
	if _, ok, _ := e.acc.Rate(ctx, e.uid, Shikimori, 4); ok {
		t.Fatal("twin kept after delete")
	}
	// a queued delete with nothing left to delete is just done
	must(t, e.acc.Enqueue(ctx, e.uid, Shikimori, pid, "delete", e.now))
	e.push()
	if len(e.w.deletes) != 1 || len(e.outbox()) != 0 {
		t.Fatalf("deletes %v outbox %+v", e.w.deletes, e.outbox())
	}
}

func TestPushEpisodesFromWatched(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	// S1 offset 0: E1–E3 watched → 3 episodes, watching
	tid := e.local("series", 101, tracking.Watching, e.now.Add(-time.Hour))
	e.episodes[tid] = aired(1, 1, 12)
	e.watched[101] = watchedRange(1, 1, 3)
	e.push()
	if len(e.w.creates) != 1 || *e.w.creates[0].W.Episodes != 3 || e.w.creates[0].W.Status != "watching" {
		t.Fatalf("creates %+v", e.w.creates)
	}

	// series 400: two cours of one TMDB season (pulled earlier); E1–E15
	// watched: cour 1 (E1–12) completed with 12, cour 2 (E13+) watching with 3
	e.w.creates = nil
	ti, err := e.st.EnsureTitle(ctx, "series", 400, "x", "")
	must(t, err)
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 31, TitleID: ti.ID, TMDBSeason: 1, RateID: 71, RemoteStatus: "watching", AnimeEpisodes: 12}))
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 32, TitleID: ti.ID, TMDBSeason: 1, TMDBOffset: 12, RateID: 72, RemoteStatus: "planned"}))
	e.episodes[ti.ID] = aired(1, 1, 24)
	e.watched[400] = watchedRange(1, 1, 15)
	score := 7
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.Watching, Score: &score, UpdatedAt: e.now}))
	e.push()
	if len(e.w.creates) != 0 || len(e.w.updates) != 2 {
		t.Fatalf("creates %+v updates %+v", e.w.creates, e.w.updates)
	}
	byRate := map[int64]RateWrite{}
	for _, u := range e.w.updates {
		byRate[u.RateID] = u.W
	}
	if w := byRate[71]; w.Status != "completed" || w.Episodes == nil || *w.Episodes != 12 {
		t.Fatalf("cour 1 %+v", w)
	}
	if w := byRate[72]; w.Status != "watching" || w.Episodes == nil || *w.Episodes != 3 || w.Score == nil || *w.Score != 7 {
		t.Fatalf("cour 2 %+v", w)
	}

	// a remote «rewatching» is kept while the mark stays «watching»: no
	// status, no episodes (what was watched here says nothing of a rewatch)
	e.w.updates = nil
	tw, _, _ := e.acc.Rate(ctx, e.uid, Shikimori, 32)
	tw.RemoteStatus = "rewatching"
	must(t, e.acc.SetRate(ctx, tw))
	must(t, e.acc.Enqueue(ctx, e.uid, Shikimori, ti.ID, "upsert", e.now))
	e.push()
	for _, u := range e.w.updates {
		if u.RateID == 72 && (u.W.Status != "" || u.W.Episodes != nil) {
			t.Fatalf("rewatching overwritten: %+v", u.W)
		}
	}
	if tw, _, _ := e.acc.Rate(ctx, e.uid, Shikimori, 32); tw.RemoteStatus != "rewatching" {
		t.Fatalf("twin %+v", tw)
	}
}

func TestPushRereadsMissingUpdatedAt(t *testing.T) {
	e := newPushEnv(t)
	e.w.noUpdated = true
	e.local("series", 101, tracking.Planned, e.now.Add(-time.Hour))
	e.push()
	tw, _, _ := e.acc.Rate(context.Background(), e.uid, Shikimori, 1)
	if e.w.reread != 1 || !tw.PushedAt.Equal(e.w.at) {
		t.Fatalf("reread %d pushed %v", e.w.reread, tw.PushedAt)
	}
}

func TestRefreshIsSerialised(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	// tokens about to expire, writes failing (the outbox stays)
	must(t, e.acc.SetTokens(ctx, e.uid, Shikimori, Tokens{Access: "a0", Refresh: "r1", Expires: e.now.Add(time.Minute)}))
	e.w.writeErr = errors.New("boom")
	e.w.delay = 20 * time.Millisecond
	e.local("series", 101, tracking.Planned, e.now.Add(-time.Hour))
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.sy.PushOne(ctx, e.uid)
		}()
	}
	wg.Wait()
	if len(e.w.refreshes) != 1 || e.w.refreshes[0] != "r1" {
		t.Fatalf("refreshes %v", e.w.refreshes)
	}
	tok, _, _ := e.acc.Tokens(ctx, e.uid, Shikimori)
	if tok.Refresh != "r2" || tok.Access != "a1" {
		t.Fatalf("rotated tokens not stored: %+v", tok)
	}
	items := e.outbox()
	if len(items) != 1 || items[0].Attempts == 0 || items[0].LastError == "" || !items[0].NextAt.After(e.now) {
		t.Fatalf("outbox %+v", items)
	}
}

func TestRevokedTokenKeepsOutbox(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	e.w.writeErr = ErrRemoteUnauthorized
	e.local("series", 101, tracking.Planned, e.now.Add(-time.Hour))
	if err := e.sy.PushOne(ctx, e.uid); err == nil {
		t.Fatal("no error")
	}
	if len(e.w.refreshes) != 1 {
		t.Fatalf("refreshes %v (one refresh before giving up)", e.w.refreshes)
	}
	l, _, _ := e.acc.Link(ctx, e.uid, Shikimori)
	if l.Status != StatusRelogin || l.LastError == "" {
		t.Fatalf("link %+v", l)
	}
	items := e.outbox()
	if len(items) != 1 || items[0].Attempts != 0 {
		t.Fatalf("outbox %+v", items)
	}
	// while a new sign-in is needed nothing is tried
	e.push()
	if len(e.w.refreshes) != 1 {
		t.Fatalf("retried %v", e.w.refreshes)
	}
	// a pull (anonymous) succeeds but the sign-in is still needed
	e.pull()
	if l, _, _ := e.acc.Link(ctx, e.uid, Shikimori); l.Status != StatusRelogin || l.LastError == "" {
		t.Fatalf("pull cleared relogin: %+v", l)
	}
}

func TestEchoOfOwnPushIsIgnored(t *testing.T) {
	e := newPushEnv(t)
	tid := e.local("series", 101, tracking.Planned, e.now.Add(-time.Hour))
	e.watched[101] = watchedRange(1, 1, 2)
	e.push()
	if len(e.w.creates) != 1 {
		t.Fatalf("creates %+v", e.w.creates)
	}
	// Shikimori turned «planned» with episodes into «watching»; its
	// updated_at is our push's
	e.remote.rates = []RemoteRate{rate(e.w.creates[0].RateID, 1, "watching", 0, e.w.at.Add(400*time.Millisecond))}
	if r := e.pull(); r.Updated != 0 || r.Imported != 0 {
		t.Fatalf("echo imported: %+v", r)
	}
	if m := e.mark("series", 101); m == nil || m.State != tracking.Planned || m.Origin != "antresol" {
		t.Fatalf("mark %+v", m)
	}
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("outbox %+v (title %d)", items, tid)
	}
}

func TestPushSkipsUnmappableTitle(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	// title 900 is not in the map: queued by hand (as if mapped before)
	ti, err := e.st.EnsureTitle(ctx, "series", 900, "x", "")
	must(t, err)
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.Watching, UpdatedAt: e.now}))
	must(t, e.acc.Enqueue(ctx, e.uid, Shikimori, ti.ID, "upsert", e.now))
	e.push()
	if len(e.w.creates) != 0 || len(e.outbox()) != 0 {
		t.Fatalf("creates %+v outbox %+v", e.w.creates, e.outbox())
	}
}

func TestEpisodesChangedEnqueues(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	tid := e.local("series", 101, tracking.Watching, e.now.Add(-time.Hour))
	e.clearOutbox()
	e.sy.EpisodesChanged(ctx, e.uid, tid)
	if items := e.outbox(); len(items) != 1 || items[0].Op != "upsert" || items[0].TitleID != tid {
		t.Fatalf("outbox %+v", items)
	}
	// no mark: nothing queued (never a delete)
	e.clearOutbox()
	ti, _ := e.st.EnsureTitle(ctx, "series", 104, "x", "")
	e.sy.EpisodesChanged(ctx, e.uid, ti.ID)
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("outbox %+v", items)
	}
}

// A cour created by a push is bounded by the anime's length.
func TestCreatedCourIsBounded(t *testing.T) {
	e := newPushEnv(t)
	e.w.episodes = map[int64]int{1: 12}
	tid := e.local("series", 101, tracking.Watching, e.now.Add(-time.Hour))
	e.episodes[tid] = aired(1, 1, 24)
	e.watched[101] = watchedRange(1, 1, 15)
	e.push()
	if len(e.w.creates) != 1 || *e.w.creates[0].W.Episodes != 12 {
		t.Fatalf("creates %+v", e.w.creates)
	}
	if tw, _, _ := e.acc.Rate(context.Background(), e.uid, Shikimori, 1); tw.AnimeEpisodes != 12 {
		t.Fatalf("twin %+v", tw)
	}
}

// Earlier cours: a remote «rewatching» stays, and without a next aired
// episode only a fully watched cour is completed.
func TestEarlierCoursKept(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	ti, err := e.st.EnsureTitle(ctx, "series", 400, "x", "")
	must(t, err)
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 31, TitleID: ti.ID, TMDBSeason: 1, RateID: 71, RemoteStatus: "watching", AnimeEpisodes: 12, RemoteEpisodes: 5}))
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 32, TitleID: ti.ID, TMDBSeason: 1, TMDBOffset: 12, RateID: 72, RemoteStatus: "planned"}))
	// no episode list: E1–5 and E13–14 watched, the place is E14 (cour 2)
	w := watchedRange(1, 1, 5)
	w[[2]int{1, 13}], w[[2]int{1, 14}] = true, true
	e.watched[400] = w
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.Watching, UpdatedAt: e.now}))
	e.push()
	if len(e.w.updates) != 1 || e.w.updates[0].RateID != 72 || *e.w.updates[0].W.Episodes != 2 {
		t.Fatalf("updates %+v", e.w.updates)
	}
	// every episode of cour 1 watched, but it is rewatched remotely
	e.w.updates = nil
	tw, _, _ := e.acc.Rate(ctx, e.uid, Shikimori, 31)
	tw.RemoteStatus = "rewatching"
	must(t, e.acc.SetRate(ctx, tw))
	e.watched[400] = watchedRange(1, 1, 14)
	e.episodes[ti.ID] = aired(1, 1, 24)
	must(t, e.acc.Enqueue(ctx, e.uid, Shikimori, ti.ID, "upsert", e.now))
	e.push()
	for _, u := range e.w.updates {
		if u.RateID == 71 {
			t.Fatalf("rewatched cour written: %+v", u.W)
		}
	}
}

// A profile that watched nothing of a title here (it watches elsewhere)
// must not wipe the remote episode counts when it changes the mark: an
// existing entry gets no episodes then; a new one starts at 0.
func TestPushKeepsRemoteEpisodesWhenNoneWatchedHere(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	ti, err := e.st.EnsureTitle(ctx, "series", 400, "x", "")
	must(t, err)
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 31, TitleID: ti.ID, TMDBSeason: 1, RateID: 71, RemoteStatus: "watching", AnimeEpisodes: 12, RemoteEpisodes: 12}))
	score := 9
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.Completed, Score: &score, UpdatedAt: e.now}))
	e.push()
	if len(e.w.updates) != 1 {
		t.Fatalf("updates %+v", e.w.updates)
	}
	if w := e.w.updates[0].W; w.Status != "completed" || w.Score == nil || *w.Score != 9 || w.Episodes != nil {
		t.Fatalf("update %+v (episodes %v)", w, w.Episodes)
	}
	// a movie entry likewise
	e.amap.m[91] = animemap.Entry{MALID: 91, Kind: "movie", TMDBID: 900}
	mv, err := e.st.EnsureTitle(ctx, "movie", 900, "m", "")
	must(t, err)
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 91, TitleID: mv.ID, RateID: 81, RemoteStatus: "completed"}))
	e.w.updates = nil
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: mv.ID, State: tracking.Completed, Score: &score, UpdatedAt: e.now}))
	e.push()
	if len(e.w.updates) != 1 || e.w.updates[0].W.Episodes != nil {
		t.Fatalf("movie updates %+v", e.w.updates)
	}
}
