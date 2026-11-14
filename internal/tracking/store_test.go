package tracking

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return &Store{DB: d}
}

func addUser(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	r, err := s.DB.Exec(`INSERT INTO users(name, avatar) VALUES(?, 'a')`, name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	return id
}

func TestEnsureTitleIdempotent(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a, err := s.EnsureTitle(ctx, "series", 100, "Show", "/p.jpg")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.EnsureTitle(ctx, "series", 100, "Show again", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 || a.ID != b.ID || b.Name != "Show" {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM tracked_titles`).Scan(&n)
	if n != 1 {
		t.Fatalf("rows=%d", n)
	}
	if _, ok, _ := s.TitleByTMDB(ctx, "movie", 100); ok {
		t.Fatal("movie 100 must not exist")
	}
}

func TestMarksPerProfile(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	u1, u2 := addUser(t, s, "one"), addUser(t, s, "two")
	ti, _ := s.EnsureTitle(ctx, "series", 1, "S", "")
	sc := 8
	if err := s.SetMark(ctx, Mark{UserID: u1, TitleID: ti.ID, State: Planned, Score: &sc}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMark(ctx, Mark{UserID: u2, TitleID: ti.ID, State: Watching}); err != nil {
		t.Fatal(err)
	}
	ms, err := s.Marks(ctx, u1)
	if err != nil || len(ms) != 1 || ms[0].State != Planned || ms[0].Score == nil || *ms[0].Score != 8 || ms[0].Origin != "antresol" {
		t.Fatalf("%+v %v", ms, err)
	}
	if ms, _ := s.Marks(ctx, u1, Watching); len(ms) != 0 {
		t.Fatalf("filter: %+v", ms)
	}
	if err := s.SetMark(ctx, Mark{UserID: u1, TitleID: ti.ID, State: "bogus"}); err == nil {
		t.Fatal("bad state accepted")
	}
	if _, err := s.DB.Exec(`DELETE FROM users WHERE id=?`, u2); err != nil {
		t.Fatal(err)
	}
	if ms, _ := s.Marks(ctx, u2); len(ms) != 0 {
		t.Fatalf("cascade: %+v", ms)
	}
	if ms, _ := s.Marks(ctx, u1); len(ms) != 1 {
		t.Fatal("u1 mark lost")
	}
	if err := s.DeleteMark(ctx, u1, ti.ID); err != nil {
		t.Fatal(err)
	}
	if ms, _ := s.Marks(ctx, u1); len(ms) != 0 {
		t.Fatal("delete failed")
	}
}

func TestUpsertSourcesReportsOnlyNew(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	ti, _ := s.EnsureTitle(ctx, "series", 1, "S", "")
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(48 * time.Hour)
	mk := func(ep int, ref string) Source {
		return Source{Season: 1, Episode: ep, Ref: ref, Provider: "p", Quality: "1080p", Labels: []string{"ru"}}
	}
	added, err := s.UpsertSources(ctx, ti.ID, "torrent", []Source{mk(1, "a"), mk(2, "b"), mk(3, "c")}, t0)
	if err != nil || len(added) != 3 {
		t.Fatalf("%v %v", added, err)
	}
	added, err = s.UpsertSources(ctx, ti.ID, "torrent", []Source{mk(1, "a"), mk(2, "b"), mk(4, "d")}, t1)
	if err != nil || len(added) != 1 || added[0].Ref != "d" || added[0].Kind != "torrent" {
		t.Fatalf("%v %v", added, err)
	}
	got, _ := s.Sources(ctx, ti.ID)
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	for _, x := range got {
		switch x.Ref {
		case "c":
			t.Fatal("c must be deleted")
		case "a", "b":
			if !x.FirstSeen.Equal(t0) || !x.LastSeen.Equal(t1) {
				t.Fatalf("%+v", x)
			}
		case "d":
			if !x.FirstSeen.Equal(t1) {
				t.Fatalf("%+v", x)
			}
		}
		if len(x.Labels) != 1 || x.Labels[0] != "ru" {
			t.Fatalf("labels %+v", x)
		}
	}
	// another kind is untouched
	if _, err := s.UpsertSources(ctx, ti.ID, "library", []Source{mk(1, "f")}, t1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSources(ctx, ti.ID, "torrent", nil, t1); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Sources(ctx, ti.ID)
	if len(got) != 1 || got[0].Kind != "library" {
		t.Fatalf("%+v", got)
	}
}

func TestTrackedIgnoresCompletedAndDropped(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	u := addUser(t, s, "one")
	states := []MarkState{Planned, Watching, OnHold, Completed, Dropped}
	var ids []int64
	for i, st := range states {
		ti, _ := s.EnsureTitle(ctx, "series", int64(i+1), "T", "")
		ids = append(ids, ti.ID)
		if err := s.SetMark(ctx, Mark{UserID: u, TitleID: ti.ID, State: st}); err != nil {
			t.Fatal(err)
		}
	}
	extra, _ := s.EnsureTitle(ctx, "movie", 99, "X", "")
	got, err := s.Tracked(ctx)
	if err != nil || len(got) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	s.ExtraTracked = func(context.Context) ([]int64, error) { return []int64{extra.ID}, nil }
	if got, _ = s.Tracked(ctx); len(got) != 4 {
		t.Fatalf("extra: %+v", got)
	}
}

func TestDueAndSetChecked(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	u := addUser(t, s, "one")
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	var ts []Title
	for i := 1; i <= 3; i++ {
		ti, _ := s.EnsureTitle(ctx, "series", int64(i), "T", "")
		s.SetMark(ctx, Mark{UserID: u, TitleID: ti.ID, State: Watching})
		ts = append(ts, ti)
	}
	if err := s.SetChecked(ctx, ts[0].ID, "airing", now.Add(-time.Hour), now); err != nil {
		t.Fatal(err)
	}
	s.SetChecked(ctx, ts[1].ID, "", now.Add(time.Hour), now)
	got, err := s.Due(ctx, now, 10)
	if err != nil || len(got) != 2 || got[0].ID != ts[2].ID || got[1].ID != ts[0].ID {
		t.Fatalf("%+v %v", got, err)
	}
	if got, _ = s.Due(ctx, now, 1); len(got) != 1 {
		t.Fatal("limit")
	}
	if a, ok, _ := s.TitleByID(ctx, ts[0].ID); !ok || a.Status != "airing" || !a.Refreshed.Equal(now) {
		t.Fatalf("checked: %+v", a)
	}
	if b, _, _ := s.TitleByID(ctx, ts[1].ID); b.Status != "unknown" {
		t.Fatalf("status kept: %+v", b)
	}
	if _, ok, _ := s.TitleByID(ctx, 999); ok {
		t.Fatal("999")
	}
	if err := s.ReplaceEpisodes(ctx, ts[0].ID, []Episode{{Season: 1, Number: 1, AirDate: "2026-01-01"}}); err != nil {
		t.Fatal(err)
	}
}

func TestSourceMetaRoundTrip(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	ti, _ := s.EnsureTitle(ctx, "series", 1, "S", "")
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fi := 3
	m := SourceMeta{ItemID: 5, Provider: "b1", Voice: "Дубляж", Lang: "ru", Audio: "Dub", Studios: []string{"X"}, Subs: []string{"en"}, FileIndex: &fi}
	if _, err := s.UpsertSources(ctx, ti.ID, "online", []Source{{Season: 1, Episode: 1, Ref: "r", Meta: m}}, t0); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Sources(ctx, ti.ID)
	if len(got) != 1 || !reflect.DeepEqual(got[0].Meta, m) {
		t.Fatalf("%+v", got)
	}
	m2 := SourceMeta{Lang: "en", Audio: "Original"}
	if _, err := s.UpsertSources(ctx, ti.ID, "online", []Source{{Season: 1, Episode: 1, Ref: "r", Meta: m2}}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Sources(ctx, ti.ID)
	if len(got) != 1 || !reflect.DeepEqual(got[0].Meta, m2) || !got[0].FirstSeen.Equal(t0) || !got[0].LastSeen.Equal(t0.Add(time.Hour)) {
		t.Fatalf("%+v", got)
	}
}

func TestChoicePerProfile(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	u1, u2 := addUser(t, s, "one"), addUser(t, s, "two")
	if _, ok, err := s.Choice(ctx, u1, "series", 7); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	now := time.Now()
	c1, c2 := Choice{Kind: "online", Provider: "b1", Voice: "A"}, Choice{Kind: "torrent", Quality: "1080p", Lang: "ja"}
	if err := s.SetChoice(ctx, u1, "series", 7, c1, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChoice(ctx, u2, "series", 7, c2, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChoice(ctx, u1, "series", 7, Choice{Kind: "library"}, now); err != nil {
		t.Fatal(err)
	}
	if c, ok, _ := s.Choice(ctx, u1, "series", 7); !ok || c.Kind != "library" {
		t.Fatalf("%+v", c)
	}
	if c, ok, _ := s.Choice(ctx, u2, "series", 7); !ok || c != c2 {
		t.Fatalf("%+v", c)
	}
	if _, err := s.DB.Exec(`DELETE FROM users WHERE id=?`, u2); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM title_choices WHERE user_id=?`, u2).Scan(&n)
	if n != 0 {
		t.Fatalf("cascade: %d", n)
	}
}

// TestUpsertSourcesKeepsFirstSeenOnRefChange: a row whose ref changed (a
// library ref migration, a new pinned version, a re-uploaded torrent) keeps
// the episode's first_seen of that kind, is not reported added and does not
// come back in the feed.
func TestUpsertSourcesKeepsFirstSeenOnRefChange(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	u := addUser(t, s, "one")
	ti, _ := s.EnsureTitle(ctx, "movie", 5, "M", "")
	s.SetMark(ctx, Mark{UserID: u, TitleID: ti.ID, State: Watching})
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.AddDate(0, 2, 0)
	if _, err := s.UpsertSources(ctx, ti.ID, "library", []Source{{Ref: "17"}}, t0); err != nil {
		t.Fatal(err)
	}
	added, err := s.UpsertSources(ctx, ti.ID, "library", []Source{{Ref: "item:17"}}, t1)
	if err != nil || len(added) != 0 {
		t.Fatalf("added %v %v", added, err)
	}
	got, _ := s.Sources(ctx, ti.ID)
	if len(got) != 1 || got[0].Ref != "item:17" || !got[0].FirstSeen.Equal(t0) || !got[0].LastSeen.Equal(t1) {
		t.Fatalf("%+v", got)
	}
	if f, _ := s.Feed(ctx, u, t1.Add(-time.Hour), nil); len(f) != 0 {
		t.Fatalf("feed %+v", f)
	}
	// a second episode of the same kind is still new and added
	added, _ = s.UpsertSources(ctx, ti.ID, "library", []Source{{Ref: "item:17"}, {Season: 1, Episode: 2, Ref: "x"}}, t1)
	if len(added) != 1 || added[0].Ref != "x" {
		t.Fatalf("added %+v", added)
	}
}
