package channels

import (
	"context"
	"errors"
	"testing"
	"time"
)

// libraryFor is a library with channels for several generated keys.
func libraryFor(f *fixture) (ann int64, series int64) {
	for i := 0; i < 6; i++ {
		f.movie("C", 90, itemOpt{year: 1991 + i, rating: "6+", genres: []string{"Комедия", "семейный"}})
	}
	series, _ = f.series("Long", 1, 8, 20, itemOpt{year: 2010, rating: "0+", genres: []string{"Kids"}})
	ann = f.user("ann")
	for _, id := range []int64{1, 2, 3, 4, 5, 6} {
		f.exec(`INSERT INTO item_owners (item_id, user_id) VALUES (?,?)`, id, ann)
	}
	return ann, series
}

func byKey(t *testing.T, st *Store) map[string]Channel {
	t.Helper()
	all, err := st.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Channel{}
	for _, c := range all {
		by[c.AutoKey] = c
	}
	return by
}

// Deleting a generated channel removes it with everything that hangs on it
// and the generator does not make it again; giving it back lets the next
// pass make it. (The tester: «каналы только выключаются? их не удалить?»;
// the row used to stay as a disabled, edited one.)
func TestDeleteGeneratedIsRemembered(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ann, _ := libraryFor(f)
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}, MinRuntime: time.Nanosecond}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	by := byKey(t, f.st)
	com := by["genre:комедия"]
	if com.ID == 0 {
		t.Fatalf("no comedy channel: %v", keys(by))
	}
	before := len(by)
	// its schedule, a favourite, a last-channel mark
	if err := f.builder().Build(ctx, com); err != nil {
		t.Fatal(err)
	}
	if len(f.entries(com.ID)) == 0 {
		t.Fatal("no schedule to delete")
	}
	if err := f.st.SetFavorite(ctx, ann, com.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetLastChannel(ctx, ann, com.ID); err != nil {
		t.Fatal(err)
	}

	if err := f.st.Delete(ctx, com.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.Get(ctx, com.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the channel row stays: %v", err)
	}
	var n int
	for _, q := range []string{`SELECT count(*) FROM channel_playout WHERE channel_id=?`, `SELECT count(*) FROM channel_favorites WHERE channel_id=?`,
		`SELECT count(*) FROM channel_last WHERE channel_id=?`, `SELECT count(*) FROM channel_cursor WHERE channel_id=?`} {
		if err := f.db.QueryRow(q, com.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d %v", q, n, err)
		}
	}
	if last, _ := f.st.LastChannel(ctx, ann); last != 0 {
		t.Fatalf("last channel %d", last)
	}
	// the numbers close up: 1…N with no gap
	all, _ := f.st.All(ctx)
	if len(all) != before-1 {
		t.Fatalf("%d channels, want %d", len(all), before-1)
	}
	nums := map[int]bool{}
	for _, c := range all {
		nums[c.Number] = true
	}
	for i := 1; i <= len(all); i++ {
		if !nums[i] {
			t.Fatalf("numbers are not 1…%d: %v", len(all), nums)
		}
	}
	rs, err := f.st.Removed(ctx)
	if err != nil || len(rs) != 1 || rs[0].AutoKey != "genre:комедия" || rs[0].Name != "Комедии" || rs[0].Category != CatGenres || rs[0].UserID != 0 {
		t.Fatalf("removed: %+v %v", rs, err)
	}

	// the generator leaves it deleted, every pass
	for i := 0; i < 2; i++ {
		res, err := g.Sync(ctx)
		if err != nil || res != (SyncResult{}) {
			t.Fatalf("pass %d: %+v %v", i, res, err)
		}
		if _, ok := byKey(t, f.st)["genre:комедия"]; ok {
			t.Fatalf("pass %d made the deleted channel again", i)
		}
	}
	// Create refuses its key too (a delete landing while a pass works)
	if _, err := f.st.Create(ctx, Channel{Name: "Комедии", AutoKey: "genre:комедия", Enabled: true}); !errors.Is(err, ErrRemoved) {
		t.Fatalf("create of a deleted key: %v", err)
	}
	// the others stay as they were
	if _, ok := byKey(t, f.st)["mine:"+itoa(ann)]; !ok {
		t.Fatal("another generated channel went with it")
	}

	// give it back: the next pass makes it, after the shared ones (shared, then personal)
	if err := f.st.Restore(ctx, rs[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Restore(ctx, rs[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore twice: %v", err)
	}
	res, err := g.Sync(ctx)
	if err != nil || res.Created != 1 {
		t.Fatalf("after restore: %+v %v", res, err)
	}
	back := byKey(t, f.st)["genre:комедия"]
	if back.ID == 0 || !back.Enabled || back.Name != "Комедии" || back.Number >= byKey(t, f.st)["mine:"+itoa(ann)].Number {
		t.Fatalf("restored: %+v", back)
	}
	if all, _ := f.st.All(ctx); len(all) != before {
		t.Fatalf("%d channels after restore, want %d", len(all), before)
	}
	if rs, _ := f.st.Removed(ctx); len(rs) != 0 {
		t.Fatalf("still listed: %+v", rs)
	}
}

// A deleted «Моя медиатека» is a profile's own: it is remembered with its
// owner and only the owner (or an admin, for a shared one) gives it back.
func TestDeletePersonalGeneratedAndWhoRestores(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ann, _ := libraryFor(f)
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}, MinRuntime: time.Nanosecond}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	by := byKey(t, f.st)
	mine, shared := by["mine:"+itoa(ann)], by["movies"]
	if mine.ID == 0 || shared.ID == 0 {
		t.Fatalf("channels: %v", keys(by))
	}
	if err := f.st.Delete(ctx, mine.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Delete(ctx, shared.ID); err != nil {
		t.Fatal(err)
	}
	rs, _ := f.st.Removed(ctx)
	got := map[string]Removed{}
	for _, r := range rs {
		got[r.AutoKey] = r
	}
	if got["mine:"+itoa(ann)].UserID != ann || got["movies"].UserID != 0 {
		t.Fatalf("removed: %+v", rs)
	}
	ann2, admin := Viewer{ID: ann}, Viewer{ID: 99, Admin: true}
	other := Viewer{ID: 7}
	for _, c := range []struct {
		who  string
		v    Viewer
		key  string
		want bool
	}{
		{"owner, own", ann2, "mine:" + itoa(ann), true},
		{"owner, shared", ann2, "movies", false},
		{"other profile, own of another", other, "mine:" + itoa(ann), false},
		{"admin, shared", admin, "movies", true},
		{"admin, another's personal", admin, "mine:" + itoa(ann), false},
	} {
		if CanRestore(got[c.key], c.v) != c.want {
			t.Errorf("%s: CanRestore = %v", c.who, !c.want)
		}
	}
}

// A channel an admin edited and then deleted is deleted all the same; what
// the generator keeps of edited channels is no reason to keep a deleted one.
func TestDeleteEditedGeneratedAndHandMade(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	libraryFor(f)
	g := &Generator{Store: f.st, Catalog: Catalog{DB: f.db}, MinRuntime: time.Nanosecond}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	c := byKey(t, f.st)["genre:комедия"]
	c.Name, c.Edited = "Смешное", true
	if _, err := f.st.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	hand := f.create(Channel{Name: "Своё"})
	if err := f.st.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Delete(ctx, hand.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	by := byKey(t, f.st)
	if _, ok := by["genre:комедия"]; ok {
		t.Fatal("the edited channel came back")
	}
	if _, ok := by[""]; ok {
		t.Fatal("the hand-made channel is still there")
	}
	// a hand-made channel leaves no trace in the deleted list
	rs, _ := f.st.Removed(ctx)
	if len(rs) != 1 || rs[0].Name != "Смешное" || !rs[0].Edited {
		t.Fatalf("removed: %+v", rs)
	}
	// deleting a channel that is gone
	if err := f.st.Delete(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete again: %v", err)
	}
}
