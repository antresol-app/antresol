package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/db"
)

func testOps(t *testing.T) (*Ops, string, *[]string) {
	s, mr := testSandbox(t)
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	var changed []string
	return &Ops{Sandbox: s, DB: d, Changed: func(roots ...string) { changed = append(changed, roots...) }}, mr, &changed
}

func TestTrashAndRestore(t *testing.T) {
	o, mr, changed := testOps(t)
	ctx := context.Background()
	f := filepath.Join(mr, "media/movies/Брат (1997)/Брат.mkv")
	write(t, f, 42)
	write(t, filepath.Join(mr, "media/movies/other.mkv"), 1)

	res, err := o.Trash(ctx, "movies", []string{"Брат (1997)", "Брат (1997)/Брат.mkv", "other.mkv"}, false, 1)
	if err != nil || len(res.Warnings) != 0 || len(res.Trashed) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if exists(f) || !exists(filepath.Join(mr, ".vault-trash")) {
		t.Fatal("not moved to trash")
	}
	if len(*changed) == 0 {
		t.Fatal("no rescan requested")
	}
	items, err := o.ListTrash(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("%+v %v", items, err)
	}
	var brat TrashItem
	for _, it := range items {
		if it.Name == "Брат (1997)" {
			brat = it
		}
	}
	if !brat.IsDir || brat.Root != "movies" || brat.Path != "Брат (1997)" || brat.Size != 42 {
		t.Fatalf("trash item %+v", brat)
	}

	if err := o.Restore(ctx, brat.ID); err != nil {
		t.Fatal(err)
	}
	if !exists(f) {
		t.Fatal("not restored")
	}
	// restoring onto an existing name fails; a second trash of the same path is fine
	if _, err := o.Trash(ctx, "movies", []string{"Брат (1997)"}, false, 1); err != nil {
		t.Fatal(err)
	}
	write(t, f, 1)
	items, _ = o.ListTrash(ctx)
	for _, it := range items {
		if it.Name == "Брат (1997)" {
			if err := o.Restore(ctx, it.ID); !errors.Is(err, ErrExists) {
				t.Fatalf("restore over existing: %v", err)
			}
		}
	}
	for _, it := range items {
		if err := o.Delete(ctx, it.ID); err != nil {
			t.Fatal(err)
		}
	}
	if items, _ := o.ListTrash(ctx); len(items) != 0 {
		t.Fatalf("left: %+v", items)
	}
	if err := o.Restore(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore missing: %v", err)
	}
	if _, err := o.Trash(ctx, "movies", []string{""}, true, 1); err == nil {
		t.Fatal("trashing a root must fail")
	}
	if _, err := o.Trash(ctx, "movies", []string{"../series"}, true, 1); !errors.Is(err, ErrOutside) {
		t.Fatalf("trash outside: %v", err)
	}
}

func TestRenameMoveMkdir(t *testing.T) {
	o, mr, _ := testOps(t)
	ctx := context.Background()
	write(t, filepath.Join(mr, "media/movies/a.mkv"), 1)
	write(t, filepath.Join(mr, "media/movies/b.mkv"), 1)
	for _, n := range []string{"../x", "x/y", "", " a", "a\x00"} {
		if err := o.Rename(ctx, "movies", "a.mkv", n, false); !errors.Is(err, ErrBadName) {
			t.Errorf("rename to %q: %v", n, err)
		}
	}
	if err := o.Rename(ctx, "movies", "a.mkv", "b.mkv", false); !errors.Is(err, ErrExists) {
		t.Fatalf("rename over existing: %v", err)
	}
	if err := o.Rename(ctx, "movies", "", "x", false); err == nil {
		t.Fatal("renaming a root must fail")
	}
	if err := o.Rename(ctx, "movies", "a.mkv", "Брат 2 (2000) BDRip 1080p.mkv", false); err != nil {
		t.Fatal(err)
	}
	if err := o.Mkdir(ctx, "movies", "", "Брат 2 (2000)"); err != nil {
		t.Fatal(err)
	}
	if err := o.Move(ctx, "movies", "Брат 2 (2000) BDRip 1080p.mkv", "movies", "Брат 2 (2000)", false); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(mr, "media/movies/Брат 2 (2000)/Брат 2 (2000) BDRip 1080p.mkv")) {
		t.Fatal("not moved")
	}
	if err := o.Move(ctx, "movies", "Брат 2 (2000)", "movies", "Брат 2 (2000)", false); err == nil {
		t.Fatal("move into itself must fail")
	}
	if err := o.Move(ctx, "movies", "b.mkv", "series", "../movies/Брат 2 (2000)", false); !errors.Is(err, ErrOutside) {
		t.Fatalf("move outside: %v", err)
	}
	if err := o.Move(ctx, "movies", "b.mkv", "series", "", false); err != nil || !exists(filepath.Join(mr, "media/series/b.mkv")) {
		t.Fatalf("cross-root move: %v", err)
	}
}

// A sibling whose name sorts between a folder and its children ("X 2"
// sorts between "X" and "X/a.mkv") must not hide that "X/a.mkv" is inside "X".
func TestTrashNestedSelectionWithSiblingBetween(t *testing.T) {
	o, mr, _ := testOps(t)
	ctx := context.Background()
	write(t, filepath.Join(mr, "media/movies/X/a.mkv"), 3)
	write(t, filepath.Join(mr, "media/movies/X 2/b.mkv"), 2)
	write(t, filepath.Join(mr, "media/movies/X-3.mkv"), 1)
	res, err := o.Trash(ctx, "movies", []string{"X", "X 2", "X/a.mkv", "X-3.mkv", "X 2", "X/"}, false, 1)
	if err != nil || len(res.Trashed) != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, p := range []string{"X", "X 2", "X-3.mkv"} {
		if exists(filepath.Join(mr, "media/movies", p)) {
			t.Fatalf("%s not trashed", p)
		}
	}
}

// When a move fails halfway, the items already in the trash are reported
// together with the error.
func TestTrashReportsPartialResult(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	o, mr, changed := testOps(t)
	write(t, filepath.Join(mr, "media/movies/a.mkv"), 1)
	ro := filepath.Join(mr, "media/movies/ro")
	write(t, filepath.Join(ro, "b.mkv"), 1)
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	res, err := o.Trash(context.Background(), "movies", []string{"ro/b.mkv", "a.mkv"}, false, 1)
	if err == nil || len(res.Trashed) != 1 || res.Trashed[0].Path != "a.mkv" {
		t.Fatalf("%+v %v", res, err)
	}
	if len(*changed) == 0 {
		t.Fatal("no rescan after a partial trash")
	}
}

func TestMoveIntoStreamCacheRefused(t *testing.T) {
	o, mr, _ := testOps(t)
	ctx := context.Background()
	write(t, filepath.Join(mr, "media/movies/a.mkv"), 1)
	if err := o.Move(ctx, "movies", "a.mkv", "stream-cache", "", true); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("stream-cache root: %v", err)
	}
	if err := o.Move(ctx, "movies", "a.mkv", "disk", "tmp/vault-stream", true); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("stream-cache via disk: %v", err)
	}
	if !exists(filepath.Join(mr, "media/movies/a.mkv")) {
		t.Fatal("moved")
	}
}

func TestListTrashBadDate(t *testing.T) {
	o, _, _ := testOps(t)
	if _, err := o.DB.Exec(`INSERT INTO trash(root, orig_path, trash_path, name, is_dir, size, deleted_at) VALUES ('movies','a','x/0/a','a',0,1,'yesterday')`); err != nil {
		t.Fatal(err)
	}
	if _, err := o.ListTrash(context.Background()); err == nil {
		t.Fatal("bad deleted_at must be an error")
	}
}
