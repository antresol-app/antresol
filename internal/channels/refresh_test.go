package channels

import (
	"context"
	"strings"
	"testing"
	"time"
)

// refreshFixture is a service over a fixture library with one channel of
// its films, built once.
func refreshFixture(t *testing.T) (*fixture, *Service, Channel) {
	t.Helper()
	f := newFixture(t)
	for i := 0; i < 6; i++ {
		f.movie("M", 120, itemOpt{year: 2000 + i})
	}
	c := f.create(Channel{Name: "Films", Rules: Rules{Kinds: []string{"movie"}}})
	svc := New(f.db, nil)
	svc.Store.Now = f.st.Now
	svc.Builder.Horizon = 5 * time.Hour
	svc.SetWarm(func(context.Context, []int64) {})
	if err := svc.BuildChannel(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return f, svc, c
}

// hideLibrary makes every read of the library's titles fail: whatever
// still works afterwards did not read it.
func (f *fixture) hideLibrary() {
	f.exec(`ALTER TABLE items RENAME TO items_off`)
}

func (f *fixture) showLibrary() {
	f.exec(`ALTER TABLE items_off RENAME TO items`)
}

// A schedule that already reaches the horizon is not built again: no pool
// (the library) is read for it, and nothing is asked of the warm hook.
func TestBuildOfSettledScheduleReadsNoLibrary(t *testing.T) {
	f, svc, c := refreshFixture(t)
	ctx := context.Background()
	before := f.entries(c.ID)
	if len(before) == 0 {
		t.Fatal("nothing built")
	}
	<-svc.warmKick // the first build asked for the guide's text
	f.hideLibrary()
	f.now = f.now.Add(10 * time.Minute) // a tick later: still reaches now + horizon - 10 min
	if added, err := svc.Builder.Build(ctx, c); err != nil || added {
		t.Fatalf("build of a settled schedule: %v %v", added, err)
	}
	if err := svc.BuildChannel(ctx, c); err != nil {
		t.Fatalf("channel build read the library: %v", err)
	}
	select {
	case <-svc.warmKick:
		t.Fatal("the warm hook was asked after a build that changed nothing")
	default:
	}
	if after := f.entries(c.ID); len(after) != len(before) {
		t.Fatalf("schedule changed: %d entries, then %d", len(before), len(after))
	}
}

// A schedule with a programme old enough to forget is not settled: the
// build runs, and trims it.
func TestBuildOfScheduleWithOldRowsTrims(t *testing.T) {
	f, svc, c := refreshFixture(t)
	ctx := context.Background()
	old := func() (n int) {
		f.db.QueryRow(`SELECT count(*) FROM channel_playout WHERE channel_id=? AND end_ms < ?`, c.ID, ms(f.now.Add(-DefaultKeep))).Scan(&n)
		return n
	}
	f.exec(`UPDATE channel_playout SET end_ms=? WHERE channel_id=? AND seq=(SELECT min(seq) FROM channel_playout WHERE channel_id=?)`,
		ms(f.now.Add(-DefaultKeep-time.Hour)), c.ID, c.ID)
	if old() != 1 {
		t.Fatalf("setup: %d old rows", old())
	}
	f.hideLibrary()
	if _, err := svc.Builder.Build(ctx, c); err == nil {
		t.Fatal("a schedule with an old row was taken for settled")
	}
	f.showLibrary()
	if _, err := svc.Builder.Build(ctx, c); err != nil {
		t.Fatal(err)
	}
	if old() != 0 {
		t.Fatal("the old row was not trimmed")
	}
}

// A timer tick that does not plan reads the generated channels' library
// only when a schedule needs more; a plan (a kick, the admin's request) reads
// it, and a failed plan does not keep the schedules from being extended.
func TestRefreshWithoutPlanLeavesTheLibraryAlone(t *testing.T) {
	f, svc, c := refreshFixture(t)
	ctx := context.Background()
	f.hideLibrary()
	f.now = f.now.Add(10 * time.Minute)
	if err := svc.refresh(ctx, false); err != nil {
		t.Fatalf("a refresh without a plan: %v", err)
	}
	if err := svc.Refresh(ctx); err == nil {
		t.Fatal("a full refresh planned without the library")
	}
	// the plan failed; the schedule that needed more is still extended
	f.showLibrary()
	f.exec(`ALTER TABLE franchises RENAME TO franchises_off`) // the plan reads these, the build does not
	f.now = f.now.Add(2 * time.Hour)
	last := func() int64 {
		es := f.entries(c.ID)
		return ms(es[len(es)-1].End)
	}
	was := last()
	err := svc.Refresh(ctx)
	if err == nil || !strings.Contains(err.Error(), "franchises") {
		t.Fatalf("the plan's error is lost: %v", err)
	}
	if last() <= was {
		t.Fatal("a failed plan stopped the schedules from being extended")
	}
}

// A series whose title is longer than a channel name takes does not stop
// the generated channels: its channel is named by the title cut to fit.
func TestSyncNamesSeriesChannelByFittedTitle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	long := strings.Repeat("Очень длинное название сериала ", 4)
	s, _ := f.series(long, 1, 8, 20, itemOpt{year: 2010})
	f.series("Short", 1, 3, 20, itemOpt{})
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}, MinRuntime: time.Nanosecond}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := f.st.All(ctx)
	for _, c := range all {
		if c.AutoKey == "series:"+itoa(s) {
			if n := len([]rune(c.Name)); n == 0 || n > maxFranchiseName {
				t.Fatalf("name %q (%d runes)", c.Name, n)
			}
			return
		}
	}
	t.Fatalf("no channel of the long series: %+v", all)
}

// A channel the store refuses is logged and passed over: the others are
// still made.
func TestSyncGoesOnPastAChannelTheStoreRefuses(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		f.movie("C", 90, itemOpt{year: 1991 + i, genres: []string{"Комедия"}})
	}
	f.exec(`CREATE TRIGGER refuse BEFORE INSERT ON channels WHEN NEW.auto_key = 'genre:комедия'
	  BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}, MinRuntime: time.Nanosecond}
	res, err := g.Sync(ctx)
	if err != nil {
		t.Fatalf("one refused channel stopped the pass: %v", err)
	}
	all, _ := f.st.All(ctx)
	have := map[string]bool{}
	for _, c := range all {
		have[c.AutoKey] = true
	}
	if !have["movies"] || !have["decade:1990"] || have["genre:комедия"] || res.Created != len(all) {
		t.Fatalf("created %d: %v", res.Created, have)
	}
}
