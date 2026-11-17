package addons

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/library"
)

// refFixture: a database with two library files, /m/a.mkv and /m/b.mkv.
func refFixture(t *testing.T) (*RefStore, *sql.DB, map[string]int64) {
	t.Helper()
	d := openTestDB(t)
	r := library.NewRepo(d)
	ctx := context.Background()
	ids := map[string]int64{}
	for _, p := range []string{"/m/a.mkv", "/m/b.mkv"} {
		if _, err := r.UpsertMovie(ctx, library.MovieInput{Title: p, Year: 2000, File: library.FileInput{Path: p, Size: 1}}); err != nil {
			t.Fatal(err)
		}
		f, err := r.FileByPath(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		ids[p] = f.ID
	}
	return &RefStore{DB: d}, d, ids
}

func refRow(t *testing.T, d *sql.DB, fileID int64) (addon, ref string) {
	t.Helper()
	err := d.QueryRow(`SELECT addon_id, ref FROM addon_file_refs WHERE file_id=?`, fileID).Scan(&addon, &ref)
	if err == sql.ErrNoRows {
		return "", ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return addon, ref
}

func TestBind_BoundAndMissing(t *testing.T) {
	s, d, ids := refFixture(t)
	ctx := context.Background()
	got, err := s.Bind(ctx, "a", "r1", []string{"/m/a.mkv", "/m/none.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Bound != 1 || !reflect.DeepEqual(got.Missing, []string{"/m/none.mkv"}) || len(got.Taken) != 0 {
		t.Fatalf("bound %+v", got)
	}
	if a, r := refRow(t, d, ids["/m/a.mkv"]); a != "a" || r != "r1" {
		t.Fatalf("row %q %q", a, r)
	}
	// rebinding its own file to another ref moves it
	if got, err := s.Bind(ctx, "a", "r2", []string{"/m/a.mkv"}); err != nil || got.Bound != 1 {
		t.Fatalf("rebind %+v %v", got, err)
	}
	if a, r := refRow(t, d, ids["/m/a.mkv"]); a != "a" || r != "r2" {
		t.Fatalf("row after rebind %q %q", a, r)
	}
}

func TestBind_OtherAddonTaken(t *testing.T) {
	s, d, ids := refFixture(t)
	ctx := context.Background()
	if _, err := s.Bind(ctx, "a", "r1", []string{"/m/a.mkv"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Bind(ctx, "b", "x", []string{"/m/a.mkv", "/m/b.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Bound != 1 || !reflect.DeepEqual(got.Taken, []string{"/m/a.mkv"}) || len(got.Missing) != 0 {
		t.Fatalf("bind by b %+v", got)
	}
	if a, r := refRow(t, d, ids["/m/a.mkv"]); a != "a" || r != "r1" {
		t.Fatalf("a's row overwritten: %q %q", a, r)
	}
	got, err = s.BindMany(ctx, "b", []addonkit.RefPath{{Ref: "y", Path: "/m/a.mkv"}, {Ref: "y", Path: "/m/b.mkv"}, {Ref: "y", Path: "/m/none.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Bound != 1 || !reflect.DeepEqual(got.Taken, []string{"/m/a.mkv"}) || !reflect.DeepEqual(got.Missing, []string{"/m/none.mkv"}) {
		t.Fatalf("BindMany by b %+v", got)
	}
	if a, r := refRow(t, d, ids["/m/a.mkv"]); a != "a" || r != "r1" {
		t.Fatalf("a's row overwritten by BindMany: %q %q", a, r)
	}
	if a, r := refRow(t, d, ids["/m/b.mkv"]); a != "b" || r != "y" {
		t.Fatalf("b's own row %q %q", a, r)
	}
}

func TestBind_RefuseBadRef(t *testing.T) {
	s, _, _ := refFixture(t)
	ctx := context.Background()
	for _, ref := range []string{"", strings.Repeat("x", 129), "a\nb", "ключ", "a\x7f"} {
		if _, err := s.Bind(ctx, "a", ref, []string{"/m/a.mkv"}); err == nil {
			t.Errorf("ref %q accepted", ref)
		}
		if _, err := s.BindMany(ctx, "a", []addonkit.RefPath{{Ref: ref, Path: "/m/a.mkv"}}); err == nil {
			t.Errorf("BindMany ref %q accepted", ref)
		}
	}
	if _, err := s.Bind(ctx, "", "r", []string{"/m/a.mkv"}); err == nil {
		t.Error("empty addon id accepted")
	}
	if !ValidRef(strings.Repeat("~", 128)) || !ValidRef("a b") {
		t.Error("valid refs refused")
	}
}

func TestRefs_CascadeUnbindRefsOf(t *testing.T) {
	s, d, ids := refFixture(t)
	ctx := context.Background()
	if _, err := s.Bind(ctx, "a", "r1", []string{"/m/a.mkv"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bind(ctx, "b", "r2", []string{"/m/b.mkv"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.RefsOf(ctx, []int64{ids["/m/a.mkv"], ids["/m/b.mkv"], 999})
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]AddonRef{ids["/m/a.mkv"]: {Addon: "a", Ref: "r1"}, ids["/m/b.mkv"]: {Addon: "b", Ref: "r2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RefsOf %v", got)
	}
	if got, err := s.RefsOf(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("RefsOf(nil) %v %v", got, err)
	}
	// Unbind removes only that addon's ref
	if err := s.Unbind(ctx, "b", "r1"); err != nil {
		t.Fatal(err)
	}
	if a, _ := refRow(t, d, ids["/m/a.mkv"]); a != "a" {
		t.Fatal("another addon's Unbind removed a's row")
	}
	if err := s.Unbind(ctx, "b", "r2"); err != nil {
		t.Fatal(err)
	}
	if a, _ := refRow(t, d, ids["/m/b.mkv"]); a != "" {
		t.Fatal("Unbind left the row")
	}
	// the row goes with its file
	if _, err := d.Exec(`DELETE FROM files WHERE id=?`, ids["/m/a.mkv"]); err != nil {
		t.Fatal(err)
	}
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM addon_file_refs`).Scan(&n)
	if n != 0 {
		t.Fatalf("rows after file delete: %d", n)
	}
}

func TestHolds_SaveLoadDrop(t *testing.T) {
	s, _, _ := refFixture(t)
	ctx := context.Background()
	if err := s.SaveHolds(ctx, "a", []byte(`[{"key":"1"}]`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveHolds(ctx, "a", []byte(`[{"key":"2"}]`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveHolds(ctx, "b", []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadHolds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || string(got["a"]) != `[{"key":"2"}]` || string(got["b"]) != `[]` {
		t.Fatalf("LoadHolds %q", got)
	}
	if err := s.DropHolds(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LoadHolds(ctx); len(got) != 1 || got["b"] == nil {
		t.Fatalf("after DropHolds %q", got)
	}
}

func TestRefs_DropRefs(t *testing.T) {
	s, d, ids := refFixture(t)
	ctx := context.Background()
	if _, err := s.Bind(ctx, "a", "r1", []string{"/m/a.mkv"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bind(ctx, "b", "r2", []string{"/m/b.mkv"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DropRefs(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if a, _ := refRow(t, d, ids["/m/a.mkv"]); a != "" {
		t.Fatal("a's ref kept")
	}
	if a, _ := refRow(t, d, ids["/m/b.mkv"]); a != "b" {
		t.Fatal("b's ref dropped")
	}
}

// Remove drops the addon's holds always, its refs only with its data.
func TestManager_RemoveDropsHoldsAndRefs(t *testing.T) {
	for _, withData := range []bool{false, true} {
		m, dev := newManager(t)
		m.Refs = &RefStore{DB: m.Store.DB}
		ctx := context.Background()
		r := library.NewRepo(m.Store.DB)
		if _, err := r.UpsertMovie(ctx, library.MovieInput{Title: "a", Year: 2000, File: library.FileInput{Path: "/m/a.mkv", Size: 1}}); err != nil {
			t.Fatal(err)
		}
		writeLocal(t, dev, "hello", "1.0.0", "ok", false)
		if _, err := m.Install(ctx, "local:hello", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Refs.Bind(ctx, "hello", "r", []string{"/m/a.mkv"}); err != nil {
			t.Fatal(err)
		}
		if err := m.Refs.SaveHolds(ctx, "hello", []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
		if err := m.Remove(ctx, "hello", withData); err != nil {
			t.Fatal(err)
		}
		if h, _ := m.Refs.LoadHolds(ctx); len(h) != 0 {
			t.Fatalf("data=%v: holds kept %q", withData, h)
		}
		var n int
		m.Store.DB.QueryRow(`SELECT COUNT(*) FROM addon_file_refs WHERE addon_id='hello'`).Scan(&n)
		if withData && n != 0 || !withData && n != 1 {
			t.Fatalf("data=%v: %d refs left", withData, n)
		}
	}
}

// A path given twice is bound once, under its last ref.
func TestBind_DuplicatePathsCountOnce(t *testing.T) {
	s, d, ids := refFixture(t)
	ctx := context.Background()
	got, err := s.Bind(ctx, "a", "r1", []string{"/m/a.mkv", "/m/a.mkv", "/m/none.mkv", "/m/none.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Bound != 1 || !reflect.DeepEqual(got.Missing, []string{"/m/none.mkv"}) {
		t.Fatalf("Bind %+v", got)
	}
	got, err = s.BindMany(ctx, "a", []addonkit.RefPath{{Ref: "x", Path: "/m/b.mkv"}, {Ref: "y", Path: "/m/b.mkv"}})
	if err != nil || got.Bound != 1 {
		t.Fatalf("BindMany %+v %v", got, err)
	}
	if _, r := refRow(t, d, ids["/m/b.mkv"]); r != "y" {
		t.Fatalf("ref %q, want the last", r)
	}
}

// Replace leaves the addon's ref on exactly the listed files.
func TestRefs_Replace(t *testing.T) {
	s, d, ids := refFixture(t)
	ctx := context.Background()
	r := library.NewRepo(d)
	if _, err := r.UpsertMovie(ctx, library.MovieInput{Title: "c", Year: 2001, File: library.FileInput{Path: "/m/c.mkv", Size: 1}}); err != nil {
		t.Fatal(err)
	}
	c, _ := r.FileByPath(ctx, "/m/c.mkv")
	if _, err := s.Bind(ctx, "a", "r1", []string{"/m/a.mkv", "/m/b.mkv"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bind(ctx, "a", "r2", []string{"/m/c.mkv"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Replace(ctx, "a", "r1", []string{"/m/b.mkv", "/m/c.mkv", "/m/none.mkv", "/m/b.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Bound != 2 || !reflect.DeepEqual(got.Missing, []string{"/m/none.mkv"}) || len(got.Taken) != 0 {
		t.Fatalf("replace %+v", got)
	}
	if a, _ := refRow(t, d, ids["/m/a.mkv"]); a != "" {
		t.Fatal("a.mkv kept the ref it was not listed under")
	}
	for _, id := range []int64{ids["/m/b.mkv"], c.ID} {
		if a, ref := refRow(t, d, id); a != "a" || ref != "r1" {
			t.Fatalf("file %d: %q %q", id, a, ref)
		}
	}
	// another addon's file is taken and stays; its own ref r1 is untouched
	if _, err := s.Bind(ctx, "b", "r1", []string{"/m/a.mkv"}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Replace(ctx, "a", "r1", []string{"/m/a.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Bound != 0 || !reflect.DeepEqual(got.Taken, []string{"/m/a.mkv"}) {
		t.Fatalf("replace with taken %+v", got)
	}
	if a, ref := refRow(t, d, ids["/m/a.mkv"]); a != "b" || ref != "r1" {
		t.Fatalf("b's row %q %q", a, ref)
	}
	for _, id := range []int64{ids["/m/b.mkv"], c.ID} {
		if a, _ := refRow(t, d, id); a != "" {
			t.Fatalf("file %d kept a's r1", id)
		}
	}
	// an empty set clears the ref; a bad ref or no addon is refused
	if _, err := s.Replace(ctx, "a", "r1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Replace(ctx, "a", "", nil); err == nil {
		t.Error("empty ref accepted")
	}
	if _, err := s.Replace(ctx, "", "r1", nil); err == nil {
		t.Error("empty addon accepted")
	}
}
