package progress

import (
	"testing"
	"time"
)

// playEnv: one user and one item, a clock the reports carry explicitly
// (the server's now stays ahead of them).
type playEnv struct {
	*env
	u, item int64
	t0      time.Time
}

func newPlayEnv(t *testing.T) *playEnv {
	e := newEnv(t)
	p := &playEnv{env: e, u: e.user("a"), item: e.movie("Film"), t0: time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)}
	e.s.Now = func() time.Time { return p.t0.Add(30 * 24 * time.Hour) }
	return p
}

const filmMs = 3 * 3_600_000

// play reports pos (seconds) at sec seconds after t0, as a player does.
func (p *playEnv) play(sec, pos int) {
	p.t.Helper()
	p.must(p.s.PlayedAt(ctx, p.u, p.item, int64(pos)*1000, filmMs, p.t0.Add(time.Duration(sec)*time.Second)))
}

// played is the user's total played time of the item, seconds.
func (p *playEnv) played() float64 {
	p.t.Helper()
	var ms int64
	if err := p.s.db.QueryRow(`SELECT COALESCE(SUM(ms),0) FROM watch_time WHERE user_id=? AND item_id=?`, p.u, p.item).Scan(&ms); err != nil {
		p.t.Fatal(err)
	}
	return float64(ms) / 1000
}

func (p *playEnv) want(sec float64) {
	p.t.Helper()
	if got := p.played(); got != sec {
		p.t.Fatalf("played %v s, want %v", got, sec)
	}
}

func TestPlayedNaturalPlayback(t *testing.T) {
	p := newPlayEnv(t)
	for s := 10; s <= 600; s += 10 {
		p.play(s, s)
	}
	p.want(600)
}

// The owner's case: a 3-hour film sought to 2:00 and stopped is not two
// hours watched.
func TestPlayedForwardSeekCountsRealTime(t *testing.T) {
	p := newPlayEnv(t)
	p.play(10, 10)
	p.play(20, 20)
	p.play(30, 7200) // sought to 2:00 between two ticks
	p.want(20 + 25)  // 10 s of real time at the fastest speed, plus slack
	p.play(40, 7210)
	p.want(55)
	// the first report sought far already: one report interval at most
	p2 := newPlayEnv(t)
	p2.item = p2.movie("Other")
	p2.play(5, 7200)
	p2.want(25)
}

func TestPlayedBackwardSeekCountsNothing(t *testing.T) {
	p := newPlayEnv(t)
	p.play(10, 10)
	p.play(20, 20)
	p.play(30, 5) // back
	p.want(20)
	p.play(40, 15) // the rewatch counts again
	p.want(30)
}

func TestPlayedPauseResume(t *testing.T) {
	p := newPlayEnv(t)
	p.play(10, 10)
	p.play(15, 15) // paused (the app reports the pause)
	p.play(3600+10, 25)
	p.want(25)
}

// An offline queue keeps one position per item: watched for an hour
// without the server, it comes back as one long delta over a long time,
// which reads as a new sitting: one interval. Sent twice (no answer the
// first time) or after a newer report, it counts nothing more.
func TestPlayedOfflineReplay(t *testing.T) {
	p := newPlayEnv(t)
	p.play(10, 10)
	p.play(3610, 3610)
	p.want(10 + 25)
	p.play(3610, 3610) // the same report again
	p.want(35)
	p.play(3620, 3620)
	p.want(45)
	p.play(3615, 3615) // an older one from another queue: stale
	p.want(45)
	if g, _ := p.s.Get(ctx, p.u, p.item); g.PositionMs != 3_620_000 {
		t.Fatalf("position %d", g.PositionMs)
	}
}

// Stopped at 0:10 last week, sought to 2:50 today to see the ending: the
// jump is not 2:40 watched, at most one report interval.
func TestPlayedSeekInANewSitting(t *testing.T) {
	p := newPlayEnv(t)
	p.play(10, 600)
	p.want(25)
	week := 7 * 24 * 3600
	p.play(week, 170*60)
	p.want(25 + 25)
	p.play(week+10, 170*60+10) // and plays on from there
	p.want(60)
	// a minute's pause, then 10 s more: counts in full
	p.play(week+80, 170*60+20)
	p.want(70)
}

// Two devices on one item: the deltas between their positions are capped
// by the real time, never the gap between the positions.
func TestPlayedTwoDevices(t *testing.T) {
	p := newPlayEnv(t)
	p.play(10, 4000) // device A, far into the film
	p.play(15, 100)  // device B, near the start
	p.play(20, 4010)
	p.play(25, 110)
	if got := p.played(); got > 25+3*(5*2+5) {
		t.Fatalf("played %v s", got)
	}
}

// Imports, sharing and «Отметить как просмотренное» record positions, not
// playing: nothing counts.
func TestPlayedNotFromSaveOrMark(t *testing.T) {
	p := newPlayEnv(t)
	p.must(p.s.SaveAt(ctx, p.u, p.item, 3_600_000, filmMs, p.t0))
	p.must(p.s.SaveListenAt(ctx, p.u, p.item, 7_200_000, filmMs, p.t0.Add(time.Hour)))
	p.must(p.s.MarkWatched(ctx, p.u, p.item, true))
	p.want(0)
}

func TestPlayedAudiobookAtSpeed(t *testing.T) {
	p := newPlayEnv(t)
	ms := func(sec, pos int) {
		p.must(p.s.ListenedAt(ctx, p.u, p.item, int64(pos)*1000, filmMs, p.t0.Add(time.Duration(sec)*time.Second)))
	}
	for s := 10; s <= 100; s += 10 {
		ms(s, 3*s) // 3×: book time is what counts
	}
	p.want(300)
	ms(110, 3000) // a seek
	p.want(300 + 35)
}

func TestPlayedPerDay(t *testing.T) {
	p := newPlayEnv(t)
	p.t0 = time.Date(2026, 3, 1, 23, 59, 50, 0, time.UTC)
	p.play(0, 100)
	p.play(10, 110) // crosses midnight: lands on the day of the report
	var day string
	var ms int64
	if err := p.s.db.QueryRow(`SELECT day, ms FROM watch_time WHERE user_id=? AND ms=10000`, p.u).Scan(&day, &ms); err != nil {
		t.Fatal(err)
	}
	if day != "2026-03-02" {
		t.Fatalf("day %s", day)
	}
}
