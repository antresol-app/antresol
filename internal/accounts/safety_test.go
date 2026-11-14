package accounts

import (
	"context"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/animemap"
	"github.com/antresol-app/antresol/internal/tracking"
)

// The remote list's safety: a push never demotes, re-scores or lowers what
// the person did elsewhere, never runs before the list was imported, and a
// delete here keeps completed history.

// writesTo returns the updates a push sent to rate id.
func (e *pushEnv) writesTo(rateID int64) []RateWrite {
	var out []RateWrite
	for _, u := range e.w.updates {
		if u.RateID == rateID {
			out = append(out, u.W)
		}
	}
	return out
}

func remoteRate(id, anime int64, status string, score, episodes int, at time.Time) RemoteRate {
	r := rate(id, anime, status, score, at)
	r.Episodes = episodes
	return r
}

// series 400 as Shikimori holds it: cour 1 (MAL 31) completed 12/12 with
// score 8, cour 2 (MAL 32) watching 3; pulled, so the mark is «watching».
func (e *pushEnv) importTwoCours() int64 {
	e.t.Helper()
	old := e.now.Add(-time.Hour).Truncate(time.Second)
	c1 := remoteRate(71, 31, "completed", 8, 12, old)
	c1.Anime.Episodes = 12
	e.remote.rates = []RemoteRate{c1, remoteRate(72, 32, "watching", 0, 3, old)}
	e.pull()
	ti, ok, err := e.st.TitleByTMDB(context.Background(), "series", 400)
	if err != nil || !ok {
		e.t.Fatalf("title %v %v", ok, err)
	}
	if m := e.mark("series", 400); m == nil || m.State != tracking.Watching {
		e.t.Fatalf("mark %+v", m)
	}
	e.clearOutbox()
	return ti.ID
}

// C1 repro 1: re-watching S1E1 here (an episode change, no mark change)
// once wrote {watching, episodes 1, score 0} over the completed cour 1.
func TestEpisodeOnlyPushNeverDemotesCompletedCour(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	tid := e.importTwoCours()
	e.episodes[tid] = aired(1, 1, 24)
	e.watched[400] = watchedRange(1, 1, 1)
	e.sy.EpisodesChanged(ctx, e.uid, tid)
	e.push()
	if len(e.w.updates)+len(e.w.creates) != 0 {
		t.Fatalf("an episode change here rewrote the list: updates %+v creates %+v", e.w.updates, e.w.creates)
	}
	// the mark changed here (score): only cour 2 takes it, cour 1 stays
	score := 7
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: tid, State: tracking.Watching, Score: &score, UpdatedAt: e.now}))
	e.push()
	if w := e.writesTo(71); len(w) != 0 {
		t.Fatalf("completed cour written: %+v", w)
	}
	w := e.writesTo(72)
	if len(w) != 1 || w[0].Status != "watching" || w[0].Score == nil || *w[0].Score != 7 || w[0].Episodes != nil {
		t.Fatalf("cour 2 %+v", w)
	}
	// a «planned» mark over the completed cour likewise never demotes it
	e.w.updates = nil
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: tid, State: tracking.Planned, UpdatedAt: e.now.Add(time.Minute)}))
	e.w.at = e.now.Add(2 * time.Minute)
	e.push()
	if w := e.writesTo(71); len(w) != 0 {
		t.Fatalf("completed cour demoted: %+v", w)
	}
}

// C1 repro 2: a cour watched to 19 elsewhere, E20 watched here: no push
// may send episodes 1.
func TestPartialLocalCountKeepsRemote(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	e.remote.rates = []RemoteRate{remoteRate(61, 1, "watching", 6, 19, e.now.Add(-time.Hour))}
	e.pull()
	ti, _, _ := e.st.TitleByTMDB(ctx, "series", 101)
	e.clearOutbox()
	e.episodes[ti.ID] = aired(1, 1, 24)
	e.watched[101] = watchedRange(1, 20, 20)
	e.sy.EpisodesChanged(ctx, e.uid, ti.ID)
	e.push()
	if len(e.w.updates) != 0 {
		t.Fatalf("episode change pushed %+v", e.w.updates)
	}
	// a mark change here carries the state and score, not the count
	score := 9
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.OnHold, Score: &score, UpdatedAt: e.now}))
	e.push()
	w := e.writesTo(61)
	if len(w) != 1 || w[0].Status != "on_hold" || *w[0].Score != 9 || w[0].Episodes != nil {
		t.Fatalf("writes %+v", w)
	}
}

// Ruling 2: a count goes down only when it is the one Antresol last wrote.
func TestUnwatchLowersOnlyOwnCount(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	old := e.now.Add(-time.Hour)
	for _, c := range []struct {
		name         string
		prev, rem, n int
		want         int // -1 = nothing sent
	}{
		{"own count", 5, 5, 4, 4},
		{"advanced elsewhere", 3, 12, 2, -1},
		{"same as remote", 0, 6, 6, -1},
		{"above remote", 3, 6, 7, 7},
	} {
		e.w.updates = nil
		ti, err := e.st.EnsureTitle(ctx, "series", 101, "x", "")
		must(t, err)
		must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 1, TitleID: ti.ID, TMDBSeason: 1, RateID: 61,
			RemoteStatus: "watching", RemoteEpisodes: c.rem, LocalEpisodes: c.prev, RemoteUpdatedAt: old, PushedAt: old, MarkAt: old.Add(-time.Hour)}))
		// the mark as last synced (mark_at): only episodes changed here
		must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.Watching, UpdatedAt: old.Add(-time.Hour), Origin: Shikimori}))
		e.watched[101] = watchedRange(1, 1, c.n)
		must(t, e.acc.Enqueue(ctx, e.uid, Shikimori, ti.ID, "upsert", e.now))
		e.push()
		w := e.writesTo(61)
		switch {
		case c.want < 0 && len(w) != 0:
			t.Fatalf("%s: sent %+v", c.name, w)
		case c.want >= 0 && (len(w) != 1 || w[0].Episodes == nil || *w[0].Episodes != c.want || w[0].Status != "" || w[0].Score != nil):
			t.Fatalf("%s: writes %+v", c.name, w)
		}
		if c.want >= 0 {
			if tw, _, _ := e.acc.Rate(ctx, e.uid, Shikimori, 1); tw.RemoteEpisodes != c.want || tw.LocalEpisodes != c.n {
				t.Fatalf("%s: twin %+v", c.name, tw)
			}
		}
	}
}

// A pull records the remote count and the anime's airing status, and keeps
// what Antresol last wrote (local_episodes, pushed_at).
func TestPullKeepsLocalEpisodes(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	ti, err := e.st.EnsureTitle(ctx, "series", 101, "x", "")
	must(t, err)
	pushed := e.now.Add(-2 * time.Hour).Truncate(time.Second)
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 1, TitleID: ti.ID, TMDBSeason: 1, RateID: 61,
		RemoteStatus: "watching", RemoteEpisodes: 5, LocalEpisodes: 5, PushedAt: pushed}))
	r := remoteRate(61, 1, "watching", 0, 7, e.now.Add(-time.Hour))
	r.Anime.Status = "ongoing"
	e.remote.rates = []RemoteRate{r}
	e.pull()
	tw, _, _ := e.acc.Rate(ctx, e.uid, Shikimori, 1)
	if tw.RemoteEpisodes != 7 || tw.LocalEpisodes != 5 || !tw.PushedAt.Equal(pushed) || tw.AnimeStatus != "ongoing" {
		t.Fatalf("twin %+v", tw)
	}
}

// Ruling 1: a local «completed» completes every remote cour of the title
// that has finished airing; the score goes to the last finished one.
func TestCompletedCompletesEveryFinishedCour(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	e.amap.m[33] = animemap.Entry{MALID: 33, Kind: "series", TMDBID: 400, Season: 2}
	ti, err := e.st.EnsureTitle(ctx, "series", 400, "x", "")
	must(t, err)
	old := e.now.Add(-time.Hour)
	for _, r := range []Rate{
		{TargetID: 31, TMDBSeason: 1, RateID: 71, RemoteStatus: "watching", AnimeEpisodes: 12, RemoteEpisodes: 4, AnimeStatus: "released"},
		{TargetID: 32, TMDBSeason: 1, TMDBOffset: 12, RateID: 72, RemoteStatus: "planned", AnimeEpisodes: 12},
		{TargetID: 33, TMDBSeason: 2, RateID: 73, RemoteStatus: "planned", AnimeStatus: "anons"},
	} {
		r.UserID, r.Provider, r.TitleID, r.RemoteUpdatedAt = e.uid, Shikimori, ti.ID, old
		must(t, e.acc.SetRate(ctx, r))
	}
	e.watched[400] = watchedRange(1, 1, 2)
	score := 9
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.Completed, Score: &score, UpdatedAt: e.now}))
	e.w.at = e.now.Add(time.Second) // the remote stamps the write after the mark
	e.push()
	if len(e.w.creates) != 0 {
		t.Fatalf("created %+v", e.w.creates)
	}
	w1, w2 := e.writesTo(71), e.writesTo(72)
	if len(w1) != 1 || w1[0].Status != "completed" || *w1[0].Episodes != 12 || w1[0].Score != nil {
		t.Fatalf("cour 1 %+v", w1)
	}
	if len(w2) != 1 || w2[0].Status != "completed" || *w2[0].Episodes != 12 || w2[0].Score == nil || *w2[0].Score != 9 {
		t.Fatalf("cour 2 %+v", w2)
	}
	if w := e.writesTo(73); len(w) != 0 {
		t.Fatalf("announced cour written %+v", w)
	}
	// an episode change afterwards repeats nothing
	e.w.updates = nil
	e.watched[400] = watchedRange(1, 1, 3)
	must(t, e.acc.Enqueue(ctx, e.uid, Shikimori, ti.ID, "upsert", e.now))
	e.push()
	if len(e.w.updates) != 0 {
		t.Fatalf("repeated %+v", e.w.updates)
	}
}

// Ruling 1: completed and rewatched cours keep their status; a re-scored
// completed title still sends the score (to the last finished cour).
func TestCompletedLeavesRewatching(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	ti, err := e.st.EnsureTitle(ctx, "series", 400, "x", "")
	must(t, err)
	old := e.now.Add(-time.Hour)
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 31, TitleID: ti.ID, TMDBSeason: 1, RateID: 71,
		RemoteStatus: "rewatching", AnimeEpisodes: 12, RemoteEpisodes: 2, RemoteUpdatedAt: old}))
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 32, TitleID: ti.ID, TMDBSeason: 1, TMDBOffset: 12, RateID: 72,
		RemoteStatus: "completed", AnimeEpisodes: 12, RemoteEpisodes: 12, RemoteUpdatedAt: old}))
	e.watched[400] = watchedRange(1, 1, 24)
	score := 6
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.Completed, Score: &score, UpdatedAt: e.now}))
	e.push()
	if w := e.writesTo(71); len(w) != 0 {
		t.Fatalf("rewatching cour written %+v", w)
	}
	w := e.writesTo(72)
	if len(w) != 1 || w[0].Status != "" || w[0].Episodes != nil || w[0].Score == nil || *w[0].Score != 6 {
		t.Fatalf("cour 2 %+v", w)
	}
}

// C2: nothing goes out before the first pull imported the remote list.
func TestPushWaitsForFirstPull(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	_, err := e.acc.DB.Exec(`UPDATE account_links SET first_pull_done=0`)
	must(t, err)
	e.local("series", 101, tracking.Watching, e.now.Add(-time.Hour))
	e.push()
	if len(e.w.creates)+len(e.w.updates)+len(e.w.deletes) != 0 || len(e.outbox()) != 1 {
		t.Fatalf("pushed before the first pull: creates %+v outbox %+v", e.w.creates, e.outbox())
	}
	// the remote holds it: the first pull imports it and the queued push
	// is dropped, nothing is created
	e.remote.rates = []RemoteRate{remoteRate(61, 1, "completed", 8, 12, e.now.Add(-2*time.Hour))}
	e.pull()
	e.push()
	if len(e.w.creates)+len(e.w.updates) != 0 {
		t.Fatalf("creates %+v updates %+v", e.w.creates, e.w.updates)
	}
	if m := e.mark("series", 101); m == nil || m.State != tracking.Completed {
		t.Fatalf("mark %+v", m)
	}
	_ = ctx
}

// C2: an empty list read anonymously may be a private one: the first-link
// rule does not run and the first pull is not recorded.
func TestEmptyAnonymousFirstPullPushesNothing(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	must(t, e.acc.SetWriteState(ctx, e.uid, Shikimori, "enabled"))
	e.local("series", 101, tracking.Watching, e.now.Add(-time.Hour))
	e.local("movie", 102, tracking.Completed, e.now.Add(-time.Hour))
	e.clearOutbox()
	e.remote.rates = []RemoteRate{}
	e.pull()
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("first-link rule ran on an anonymous empty list: %+v", items)
	}
	if l, _, _ := e.acc.Link(ctx, e.uid, Shikimori); l.FirstPullDone {
		t.Fatal("first pull recorded")
	}
	if e.mark("series", 101) == nil || e.mark("movie", 102) == nil {
		t.Fatal("local marks touched")
	}
}

// C2: a private list is read with the profile's token (refreshed when
// about to expire, and once after a rejection); signing in makes it
// readable.
func TestPrivateListPullsWithToken(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	_, err := e.acc.DB.Exec(`UPDATE account_links SET first_pull_done=0`)
	must(t, err)
	e.remote.private = true
	e.remote.rates = []RemoteRate{remoteRate(61, 1, "completed", 8, 12, e.now.Add(-2*time.Hour))}
	e.pull()
	if got := e.remote.accesses; len(got) != 1 || got[0] != "a0" {
		t.Fatalf("reads %q", got)
	}
	if m := e.mark("series", 101); m == nil || m.State != tracking.Completed {
		t.Fatalf("mark %+v", m)
	}
	if l, _, _ := e.acc.Link(ctx, e.uid, Shikimori); !l.FirstPullDone || l.Status != StatusOK {
		t.Fatalf("link %+v", l)
	}
	// about to expire: refreshed first, the rotated pair stored
	e.remote.accesses = nil
	must(t, e.acc.SetTokens(ctx, e.uid, Shikimori, Tokens{Access: "a0", Refresh: "r1", Expires: e.now.Add(time.Minute)}))
	e.pull()
	if got := e.remote.accesses; len(got) != 1 || got[0] != "a1" {
		t.Fatalf("reads %q", got)
	}
	if tok, _, _ := e.acc.Tokens(ctx, e.uid, Shikimori); tok.Access != "a1" || tok.Refresh != "r2" {
		t.Fatalf("tokens %+v", tok)
	}
	// rejected: refreshed once and read again
	e.remote.accesses = nil
	e.remote.rejected = map[string]bool{"a1": true}
	e.pull()
	if got := e.remote.accesses; len(got) != 2 || got[0] != "a1" || got[1] != "a2" {
		t.Fatalf("reads %q", got)
	}
	// every token rejected: «relogin», and the list is read anonymously
	e.remote.accesses = nil
	e.remote.rejected = map[string]bool{"a2": true, "a3": true}
	if _, err := e.sy.PullOne(ctx, e.uid); err == nil {
		t.Fatal("private list read anonymously without error")
	}
	if got := e.remote.accesses; len(got) != 3 || got[2] != "" {
		t.Fatalf("reads %q", got)
	}
	if l, _, _ := e.acc.Link(ctx, e.uid, Shikimori); l.Status != StatusRelogin {
		t.Fatalf("link %+v", l)
	}
}

// I1: removing a title here deletes only its cours not completed or
// rewatched; the kept ones are not re-imported by the next pull.
func TestDeleteKeepsCompletedCours(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	tid := e.importTwoCours()
	must(t, e.st.DeleteMark(ctx, e.uid, tid))
	e.push()
	if len(e.w.deletes) != 1 || e.w.deletes[0] != 72 {
		t.Fatalf("deletes %v", e.w.deletes)
	}
	if _, ok, _ := e.acc.Rate(ctx, e.uid, Shikimori, 31); !ok {
		t.Fatal("completed cour's twin dropped")
	}
	if _, ok, _ := e.acc.Rate(ctx, e.uid, Shikimori, 32); ok {
		t.Fatal("deleted cour's twin kept")
	}
	// the remote now holds cour 1 only, unchanged: nothing comes back
	e.remote.rates = e.remote.rates[:1]
	if r := e.pull(); r.Imported+r.Updated != 0 {
		t.Fatalf("pull %+v", r)
	}
	if m := e.mark("series", 400); m != nil {
		t.Fatalf("re-imported %+v", m)
	}
	if items := e.outbox(); len(items) != 0 {
		t.Fatalf("outbox %+v", items)
	}
}

// An entry removed remotely is not created again by a push carrying only
// episode counts; its twin is forgotten.
func TestGoneEntryNotRecreatedFromEpisodes(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	e.remote.rates = []RemoteRate{remoteRate(61, 1, "watching", 0, 2, e.now.Add(-time.Hour))}
	e.pull()
	ti, _, _ := e.st.TitleByTMDB(ctx, "series", 101)
	e.w.gone = map[int64]bool{61: true}
	e.watched[101] = watchedRange(1, 1, 5)
	must(t, e.acc.Enqueue(ctx, e.uid, Shikimori, ti.ID, "upsert", e.now))
	e.push()
	if len(e.w.creates) != 0 {
		t.Fatalf("recreated %+v", e.w.creates)
	}
	if _, ok, _ := e.acc.Rate(ctx, e.uid, Shikimori, 1); ok {
		t.Fatal("twin kept")
	}
}

// Minor 1: a list read at the size cap may be cut short: nothing missing
// from it reads as removed.
func TestCappedListReadsNoRemovals(t *testing.T) {
	e := newSyncEnv(t)
	e.remote.rates = []RemoteRate{rate(61, 1, "watching", 0, e.now.Add(-time.Hour))}
	e.pull()
	e.remote.rates = nil
	for i := range rateListCap {
		e.remote.rates = append(e.remote.rates, rate(int64(10000+i), int64(100000+i), "planned", 0, e.now.Add(-time.Hour)))
	}
	e.pull()
	if m := e.mark("series", 101); m == nil {
		t.Fatal("mark removed by a capped list")
	}
}

// pullCours imports series 400 from two remote cours (MAL 31: E1–12, MAL 32:
// E13–24) and returns its title id.
func (e *pushEnv) pullCours(c1, c2 RemoteRate) int64 {
	e.t.Helper()
	c1.Anime.Episodes, c2.Anime.Episodes = 12, 12
	e.remote.rates = []RemoteRate{c1, c2}
	e.pull()
	ti, ok, err := e.st.TitleByTMDB(context.Background(), "series", 400)
	must(e.t, err)
	if !ok {
		e.t.Fatal("not imported")
	}
	return ti.ID
}

// watchHere marks episodes of series 400 watched here and pushes.
func (e *pushEnv) watchHere(tid int64, w map[[2]int]bool) {
	e.t.Helper()
	e.w.updates, e.w.creates = nil, nil
	e.watched[400] = w
	e.episodes[tid] = aired(1, 1, 24)
	e.sy.EpisodesChanged(context.Background(), e.uid, tid)
	e.push()
}

// episodesOnly: the writes to a cour carry n episodes and no status or score.
func episodesOnly(t *testing.T, what string, w []RateWrite, n int) {
	t.Helper()
	if len(w) != 1 || w[0].Status != "" || w[0].Score != nil || w[0].Episodes == nil || *w[0].Episodes != n {
		t.Fatalf("%s: writes %+v", what, w)
	}
}

// C1 residual: a pull sets the mark's time to its newest changed cour, so
// an older cour of the title must not take an untouched mark for a change:
// watching S1E1 here sends cour 1 its episodes only. The remote's clock,
// however far off, plays no part.
func TestEpisodePushLeavesOlderCourStatus(t *testing.T) {
	for _, skew := range []struct {
		name string
		at   time.Time // the remote's updated_at of cour 2
	}{
		{"remote clock right", time.Time{}},
		{"remote clock far ahead", time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"remote clock far behind", time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		for _, c := range []struct {
			name     string
			c1, c2   RemoteRate
			wantMark tracking.MarkState
		}{
			{"(a) planned + completed", remoteRate(71, 31, "planned", 0, 0, time.Time{}), remoteRate(72, 32, "completed", 0, 12, time.Time{}), tracking.Completed},
			{"(b) on_hold 7 + dropped", remoteRate(71, 31, "on_hold", 7, 0, time.Time{}), remoteRate(72, 32, "dropped", 0, 0, time.Time{}), tracking.Dropped},
		} {
			t.Run(skew.name+" "+c.name, func(t *testing.T) {
				e := newPushEnv(t)
				newer := e.now.Add(-time.Hour)
				if !skew.at.IsZero() {
					newer = skew.at
				}
				c.c1.UpdatedAt, c.c2.UpdatedAt = newer.Add(-24*time.Hour), newer
				tid := e.pullCours(c.c1, c.c2)
				if m := e.mark("series", 400); m == nil || m.State != c.wantMark {
					t.Fatalf("imported mark %+v", m)
				}
				e.watchHere(tid, watchedRange(1, 1, 1))
				episodesOnly(t, "cour 1", e.writesTo(71), 1)
				if w := e.writesTo(72); len(w) != 0 {
					t.Fatalf("cour 2 written: %+v", w)
				}
				// the next pull sees our echo only (the remote holds our
				// write, updated_at as answered): nothing changes, and the
				// next push writes nothing
				e.remote.rates[0].Episodes, e.remote.rates[0].UpdatedAt = 1, e.w.at
				e.pull()
				if m := e.mark("series", 400); m.State != c.wantMark {
					t.Fatalf("mark after echo %+v", m)
				}
				e.watchHere(tid, watchedRange(1, 1, 1))
				if len(e.w.updates)+len(e.w.creates) != 0 {
					t.Fatalf("ping-pong: %+v %+v", e.w.updates, e.w.creates)
				}
				// a real change here (our clock: before a far-ahead remote
				// one) still sends status and score
				score := 9
				must(t, e.st.SetMark(context.Background(), tracking.Mark{UserID: e.uid, TitleID: tid, State: tracking.Watching, Score: &score, UpdatedAt: e.now}))
				e.w.updates = nil
				e.push()
				w := e.writesTo(71)
				if len(w) != 1 || w[0].Status != "watching" || w[0].Score == nil || *w[0].Score != 9 {
					t.Fatalf("local change: writes %+v", w)
				}
				if w := e.writesTo(72); len(w) != 0 {
					t.Fatalf("kept or later cour written: %+v", w)
				}
			})
		}
	}
}

// Without a mark change a push only moves cour statuses up: a fully
// watched earlier cour watching→completed, the current one planned→watching;
// never from on hold or dropped, never a score.
func TestEpisodePushUpgradesOnly(t *testing.T) {
	for _, c := range []struct {
		name         string
		s1, s2       string
		want1, want2 string
	}{
		{"upgrade", "watching", "planned", "completed", "watching"},
		{"planned earlier cour", "planned", "planned", "completed", "watching"},
		{"held and dropped stay", "on_hold", "dropped", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newPushEnv(t)
			ctx := context.Background()
			ti, err := e.st.EnsureTitle(ctx, "series", 400, "x", "")
			must(t, err)
			synced := e.now.Add(-time.Hour).Truncate(time.Second)
			must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.Watching, UpdatedAt: synced, Origin: Shikimori}))
			must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 31, TitleID: ti.ID, TMDBSeason: 1, RateID: 71,
				RemoteStatus: c.s1, AnimeEpisodes: 12, RemoteEpisodes: 5, MarkAt: synced}))
			must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 32, TitleID: ti.ID, TMDBSeason: 1, TMDBOffset: 12, RateID: 72,
				RemoteStatus: c.s2, AnimeEpisodes: 12, MarkAt: synced}))
			e.watchHere(ti.ID, watchedRange(1, 1, 14))
			w1, w2 := e.writesTo(71), e.writesTo(72)
			if len(w1) != 1 || w1[0].Status != c.want1 || w1[0].Score != nil || *w1[0].Episodes != 12 {
				t.Fatalf("cour 1: writes %+v", w1)
			}
			if len(w2) != 1 || w2[0].Status != c.want2 || w2[0].Score != nil || *w2[0].Episodes != 2 {
				t.Fatalf("cour 2: writes %+v", w2)
			}
		})
	}
}

// Mark times are whole seconds: a change made within the second of the
// last push's mark time reads as no change (episodes only) until the mark
// changes again; one a second later is a change.
func TestMarkChangeSameSecondAsPush(t *testing.T) {
	e := newPushEnv(t)
	ctx := context.Background()
	ti, err := e.st.EnsureTitle(ctx, "series", 400, "x", "")
	must(t, err)
	must(t, e.acc.SetRate(ctx, Rate{UserID: e.uid, Provider: Shikimori, TargetID: 31, TitleID: ti.ID, TMDBSeason: 1, RateID: 71,
		RemoteStatus: "planned", AnimeEpisodes: 12}))
	at := e.now.Truncate(time.Second)
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.Watching, UpdatedAt: at}))
	e.push()
	if w := e.writesTo(71); len(w) != 1 || w[0].Status != "watching" {
		t.Fatalf("first push %+v", w)
	}
	e.w.updates = nil
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.OnHold, UpdatedAt: at.Add(500 * time.Millisecond)}))
	e.push()
	if w := e.writesTo(71); len(w) != 0 {
		t.Fatalf("same second: %+v", w)
	}
	must(t, e.st.SetMark(ctx, tracking.Mark{UserID: e.uid, TitleID: ti.ID, State: tracking.OnHold, UpdatedAt: at.Add(time.Second)}))
	e.push()
	if w := e.writesTo(71); len(w) != 1 || w[0].Status != "on_hold" {
		t.Fatalf("next second: %+v", w)
	}
}
