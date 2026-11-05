package files

import (
	"context"
	"errors"

	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

// A file that appears between the plan and the move makes the delete plan
// again: here a video nobody indexed, so the folder stays and only the
// film goes.
func TestDeleteReplansWhenFilesAppear(t *testing.T) {
	e := newItemEnv(t)
	dir := filepath.Join(e.mv, "Брат (1997)")
	film := filepath.Join(dir, "Брат (1997).mkv")
	write(t, film, 4)
	e.rescan(t)
	it := e.itemOf(t, film)
	late := filepath.Join(dir, "extra", "new.mkv")
	calls := 0
	beforeCheck = func() {
		calls++
		if calls == 1 {
			write(t, late, 1)
		}
	}
	t.Cleanup(func() { beforeCheck = nil })
	res, err := e.d.Delete(context.Background(), it.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(late) || exists(film) || len(res.Trashed) != 1 || res.Trashed[0].IsDir {
		t.Fatalf("late %v, %+v", exists(late), res.Trashed)
	}
}

// Files that keep appearing refuse the delete; nothing moves.
func TestDeleteRefusesWhenFilesKeepAppearing(t *testing.T) {
	e := newItemEnv(t)
	dir := filepath.Join(e.mv, "Брат (1997)")
	film := filepath.Join(dir, "Брат (1997).mkv")
	write(t, film, 4)
	e.rescan(t)
	it := e.itemOf(t, film)
	n := 0
	beforeCheck = func() {
		n++
		write(t, filepath.Join(dir, "extra", string(rune('a'+n))+".jpg"), 1)
	}
	t.Cleanup(func() { beforeCheck = nil })
	res, err := e.d.Delete(context.Background(), it.ID, 1)
	if !errors.Is(err, ErrPlanChanged) || len(res.Trashed) != 0 || !exists(film) {
		t.Fatalf("%v %+v", err, res)
	}
}

// A file dropped in after the last check goes with the folder: it stays
// there (no folder is made again at the old place, so the folder can be
// restored) and the result counts it.
func TestDeleteCountsLateFiles(t *testing.T) {
	e := newItemEnv(t)
	dir := filepath.Join(e.mv, "Брат (1997)")
	film := filepath.Join(dir, "Брат (1997).mkv")
	write(t, film, 4)
	e.rescan(t)
	it := e.itemOf(t, film)
	beforeMove = func() { write(t, filepath.Join(dir, "poster2.jpg"), 1) }
	t.Cleanup(func() { beforeMove = nil })
	res, err := e.d.Delete(context.Background(), it.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Late != 1 || exists(dir) {
		t.Fatalf("late %d, folder back %v", res.Late, exists(dir))
	}
	if err := e.d.Ops.Restore(context.Background(), res.Trashed[0].ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !exists(filepath.Join(dir, "poster2.jpg")) || !exists(film) {
		t.Fatal("not restored whole")
	}
}

// A scan that does not let go answers at once; nothing moves.
func TestDeleteScanBusy(t *testing.T) {
	e := newItemEnv(t)
	film := filepath.Join(e.mv, "Брат (1997)", "Брат (1997).mkv")
	write(t, film, 4)
	e.rescan(t)
	e.d.Hold = func(context.Context) (func(), error) { return nil, library.ErrScanBusy }
	if _, err := e.d.Delete(context.Background(), e.itemOf(t, film).ID, 1); !errors.Is(err, library.ErrScanBusy) || !exists(film) {
		t.Fatalf("%v", err)
	}
}

// In the item's own folder a file that is neither the item's nor a
// sidecar, picture or cover (a manuscript) keeps the folder.
func TestDeleteOwnFolderWithDocumentStays(t *testing.T) {
	e := newItemEnv(t)
	dir := filepath.Join(e.mv, "Брат (1997)")
	film := filepath.Join(dir, "Брат (1997).mkv")
	write(t, film, 4)
	write(t, filepath.Join(dir, "cover.jpg"), 1)
	doc := filepath.Join(dir, "сценарий.docx")
	write(t, doc, 1)
	e.rescan(t)
	res, err := e.d.Delete(context.Background(), e.itemOf(t, film).ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trashed) != 1 || res.Trashed[0].IsDir || !exists(doc) {
		t.Fatalf("%+v", res.Trashed)
	}
}

// Linked data: a download still being put into the library refuses the
// delete; one linked only by its item, whose library files carry no mark,
// counts as covered only when its media files all match the item's files.
func TestDeleteLinkedEdges(t *testing.T) {
	e := newItemEnv(t)
	film := filepath.Join(e.mv, "Брат (1997)", "Брат (1997).mkv")
	write(t, film, 4)
	e.rescan(t)
	it := e.itemOf(t, film)
	set, err := e.d.Plan(context.Background(), it.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if !e.d.coversLinked(ctx, set, Linked{ItemID: it.ID, Files: []LinkedFile{{Path: "Brat/Brat.mkv", Size: 4}, {Path: "Brat/info.nfo", Size: 9}}}) {
		t.Error("matching files not covered")
	}
	if e.d.coversLinked(ctx, set, Linked{ItemID: it.ID, Files: []LinkedFile{{Path: "Brat/Brat.mkv", Size: 4}, {Path: "Brat/Brat2.mkv", Size: 9}}}) {
		t.Error("a download with another film covered")
	}
	if e.d.coversLinked(ctx, set, Linked{ItemID: it.ID}) {
		t.Error("a download with no files covered")
	}
	data := filepath.Join(e.mr, "torrents", "Brat")
	write(t, filepath.Join(data, "Brat.mkv"), 4)
	e.d.Linked = func(context.Context, library.DeleteSet) []Linked {
		return []Linked{{Path: data, Key: "aa", Busy: true}}
	}
	if _, err := e.d.Delete(ctx, it.ID, 1); !errors.Is(err, ErrImporting) || !exists(film) || !exists(data) {
		t.Fatalf("importing: %v", err)
	}

}
