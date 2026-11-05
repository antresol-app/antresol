package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

type itemEnv struct {
	d      *ItemDelete
	scan   *library.Scanner
	mr     string
	mv, sr string
}

func newItemEnv(t *testing.T) *itemEnv {
	t.Helper()
	o, mr, _ := testOps(t)
	repo := library.NewRepo(o.DB)
	mv, sr := filepath.Join(mr, "media/movies"), filepath.Join(mr, "media/series")
	return &itemEnv{
		d:    &ItemDelete{Ops: o, Library: repo, Roots: []string{mv, sr}},
		scan: &library.Scanner{Repo: repo, Movies: mv, Series: sr},
		mr:   mr, mv: mv, sr: sr,
	}
}

func (e *itemEnv) rescan(t *testing.T) {
	t.Helper()
	if _, err := e.scan.ScanVideo(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (e *itemEnv) itemOf(t *testing.T, path string) library.Item {
	t.Helper()
	it, _, err := e.d.Library.ItemByPath(context.Background(), path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return it
}

func (e *itemEnv) shown(id int64) bool {
	_, err := e.d.Library.Item(context.Background(), id)
	return err == nil
}

// A movie in its own folder: the folder goes to the trash whole (with its
// subtitles and poster), the movie leaves the library at once, and a
// restore from the trash brings it back with the same id.
func TestDeleteMovieFolder(t *testing.T) {
	e := newItemEnv(t)
	ctx := context.Background()
	dir := filepath.Join(e.mv, "Брат (1997)")
	f := filepath.Join(dir, "Брат (1997).mkv")
	write(t, f, 40)
	write(t, filepath.Join(dir, "Брат (1997).ru.srt"), 2)
	write(t, filepath.Join(dir, "poster.jpg"), 3)
	e.rescan(t)
	it := e.itemOf(t, f)

	res, err := e.d.Delete(ctx, it.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trashed) != 1 || res.Trashed[0].Path != "Брат (1997)" || !res.Trashed[0].IsDir || res.Size != 45 {
		t.Fatalf("trashed %+v", res)
	}
	if exists(dir) || e.shown(it.ID) {
		t.Fatal("movie still there")
	}
	if err := e.d.Ops.Restore(ctx, res.Trashed[0].ID); err != nil {
		t.Fatal(err)
	}
	e.rescan(t)
	if !e.shown(it.ID) {
		t.Fatal("restored movie not back under its id")
	}
}

// One season of a series goes with its folder; the series stays. Then the
// whole series takes its folder.
func TestDeleteSeasonThenSeries(t *testing.T) {
	e := newItemEnv(t)
	ctx := context.Background()
	s1 := filepath.Join(e.sr, "Сёгун", "Season 01")
	write(t, filepath.Join(s1, "Сёгун.S01E01.mkv"), 5)
	write(t, filepath.Join(s1, "Сёгун.S01E02.mkv"), 5)
	s2ep := filepath.Join(e.sr, "Сёгун", "Season 02", "Сёгун.S02E01.mkv")
	write(t, s2ep, 7)
	e.rescan(t)
	ep := e.itemOf(t, filepath.Join(s1, "Сёгун.S01E01.mkv"))
	season, err := e.d.Library.Item(ctx, ep.ParentID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.d.Delete(ctx, season.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trashed) != 1 || res.Trashed[0].Path != "Сёгун/Season 01" || len(res.Items) != 3 {
		t.Fatalf("season delete %+v", res)
	}
	if !e.shown(season.ParentID) || e.shown(season.ID) || !exists(s2ep) {
		t.Fatal("series or season 2 broke")
	}
	res, err = e.d.Delete(ctx, season.ParentID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trashed) != 1 || res.Trashed[0].Path != "Сёгун" || e.shown(season.ParentID) {
		t.Fatalf("series delete %+v", res)
	}
}

// Two movies in one folder: only the deleted one's file and its sidecars
// go; the other movie and the folder stay. Media the library has not
// indexed keeps its folder too.
func TestDeleteMovieInSharedFolder(t *testing.T) {
	e := newItemEnv(t)
	ctx := context.Background()
	dir := filepath.Join(e.mv, "Коллекция")
	a, b := filepath.Join(dir, "Брат (1997).mkv"), filepath.Join(dir, "Брат 2 (2000).mkv")
	write(t, a, 4)
	write(t, filepath.Join(dir, "Брат (1997).srt"), 1)
	write(t, b, 4)
	write(t, filepath.Join(dir, "Брат 2 (2000).srt"), 1)
	other := filepath.Join(e.mv, "Сёстры (2001)")
	write(t, filepath.Join(other, "Сёстры (2001).mkv"), 3)
	write(t, filepath.Join(other, "soundtrack.flac"), 3) // not the library's
	e.rescan(t)
	ia, ib, io := e.itemOf(t, a), e.itemOf(t, b), e.itemOf(t, filepath.Join(other, "Сёстры (2001).mkv"))

	res, err := e.d.Delete(ctx, ia.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trashed) != 2 || exists(a) || exists(filepath.Join(dir, "Брат (1997).srt")) {
		t.Fatalf("trashed %+v", res.Trashed)
	}
	if !exists(b) || !exists(filepath.Join(dir, "Брат 2 (2000).srt")) || !e.shown(ib.ID) || e.shown(ia.ID) {
		t.Fatal("the other movie broke")
	}
	if _, err := e.d.Delete(ctx, io.ID, 1); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(other, "soundtrack.flac")) {
		t.Fatal("unindexed media trashed with the movie")
	}
}

// An unavailable disk, a missing library folder or no file on disk refuse
// the delete and change nothing.
func TestDeleteRefusesOfflineMedia(t *testing.T) {
	e := newItemEnv(t)
	ctx := context.Background()
	f := filepath.Join(e.mv, "Брат (1997)", "Брат (1997).mkv")
	write(t, f, 4)
	ep := filepath.Join(e.sr, "Сёгун", "Сёгун.S01E01.mkv")
	write(t, ep, 4)
	e.rescan(t)
	it, epIt := e.itemOf(t, f), e.itemOf(t, ep)

	e.d.Offline = func() string { return "missing folder /media" }
	if _, err := e.d.Delete(ctx, it.ID, 1); !errors.Is(err, ErrMediaOffline) {
		t.Fatalf("offline: %v", err)
	}
	e.d.Offline = nil
	if !exists(f) || !e.shown(it.ID) {
		t.Fatal("changed while offline")
	}

	// the series folder unmounted
	if err := os.RemoveAll(e.sr); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.Delete(ctx, epIt.ID, 1); !errors.Is(err, ErrMediaOffline) {
		t.Fatalf("missing root: %v", err)
	}
	if !e.shown(epIt.ID) {
		t.Fatal("forgotten with its folder missing")
	}

	// the root is there but the files are not
	if err := os.MkdirAll(e.sr, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.Delete(ctx, epIt.ID, 1); !errors.Is(err, ErrNothingOnDisk) {
		t.Fatalf("nothing on disk: %v", err)
	}
	if !e.shown(epIt.ID) {
		t.Fatal("forgotten with nothing on disk")
	}
}

// A file row outside the library folders, or under a folder link leading
// out of them, refuses the delete.
func TestDeleteRefusesPathsOutsideRoots(t *testing.T) {
	e := newItemEnv(t)
	ctx := context.Background()
	outside := filepath.Join(e.mr, "elsewhere", "x.mkv")
	write(t, outside, 3)
	id, err := e.d.Library.UpsertMovie(ctx, library.MovieInput{Title: "X", File: library.FileInput{Path: outside, Size: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.Delete(ctx, id, 1); !errors.Is(err, ErrOutside) {
		t.Fatalf("outside: %v", err)
	}
	link := filepath.Join(e.mv, "Ссылка")
	if err := os.Symlink(filepath.Dir(outside), link); err != nil {
		t.Fatal(err)
	}
	id2, err := e.d.Library.UpsertMovie(ctx, library.MovieInput{Title: "Y", File: library.FileInput{Path: filepath.Join(link, "x.mkv"), Size: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.Delete(ctx, id2, 1); !errors.Is(err, ErrOutside) {
		t.Fatalf("through a link: %v", err)
	}
	if !exists(outside) {
		t.Fatal("deleted outside the library")
	}
}
