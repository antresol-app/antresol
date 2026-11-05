package files

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// fakeTorrents records what Ops asks of the download client.
type fakeTorrents struct {
	follow, stop []string
	stopErr      error
	plans        [][2]string
	stopped      []string
	paused       []string
	resumed      []string
	relocated    [][2]string
	// existedAtStop: the data was still in place when the download was stopped.
	existedAtStop []bool
	watch         string
}

func (f *fakeTorrents) PlanRelocate(from, to string) ([]string, []string) {
	f.plans = append(f.plans, [2]string{from, to})
	if to == "" {
		return nil, append(append([]string{}, f.follow...), f.stop...)
	}
	return f.follow, f.stop
}

func (f *fakeTorrents) Relocate(_ context.Context, from, to string, rename func() error) error {
	f.relocated = append(f.relocated, [2]string{from, to})
	return rename()
}

// Pause fails with stopErr; the data must still be in place then.
func (f *fakeTorrents) Pause(_ context.Context, ih string) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	f.paused = append(f.paused, ih)
	f.existedAtStop = append(f.existedAtStop, exists(f.watch))
	return nil
}

func (f *fakeTorrents) Resume(_ context.Context, ih string) error {
	f.resumed = append(f.resumed, ih)
	return nil
}

func (f *fakeTorrents) Stop(_ context.Context, ih string) error {
	f.stopped = append(f.stopped, ih)
	return nil
}

// F6: trashing download data stops the download (data kept) before the files
// move, so it can never download them again.
func TestTrashStopsTorrents(t *testing.T) {
	o, mr, _ := testOps(t)
	dir := filepath.Join(mr, "torrents/movies/Брат.1997.BDRip")
	write(t, filepath.Join(dir, "Брат.mkv"), 10)
	ft := &fakeTorrents{follow: []string{"aa"}, watch: dir}
	o.Torrents = ft
	res, err := o.Trash(context.Background(), "downloads", []string{"movies/Брат.1997.BDRip"}, true, 1)
	if err != nil || len(res.Trashed) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(ft.stopped) != 1 || ft.stopped[0] != "aa" || len(ft.paused) != 1 || !ft.existedAtStop[0] {
		t.Fatalf("stopped %v (data present then: %v)", ft.stopped, ft.existedAtStop)
	}
	if exists(dir) {
		t.Fatal("not trashed")
	}
}

func TestTrashKeepsDataWhenTorrentCannotStop(t *testing.T) {
	o, mr, _ := testOps(t)
	dir := filepath.Join(mr, "torrents/movies/Брат.1997.BDRip")
	write(t, filepath.Join(dir, "Брат.mkv"), 10)
	busy := errors.New("organizing")
	o.Torrents = &fakeTorrents{stop: []string{"aa"}, stopErr: busy}
	_, err := o.Trash(context.Background(), "downloads", []string{"movies/Брат.1997.BDRip"}, true, 1)
	if !errors.Is(err, ErrTorrent) || !errors.Is(err, busy) {
		t.Fatalf("err %v", err)
	}
	if !exists(dir) {
		t.Fatal("data trashed although the torrent kept running")
	}
	if items, _ := o.ListTrash(context.Background()); len(items) != 0 {
		t.Fatalf("trash rows %+v", items)
	}
}

// F6: a move re-points downloads that can follow and stops the others
// first; a rename of a file inside a download stops it.
func TestMoveAndRenameKeepTorrentsInStep(t *testing.T) {
	o, mr, _ := testOps(t)
	ctx := context.Background()
	write(t, filepath.Join(mr, "torrents/movies/A/a.mkv"), 1)
	ft := &fakeTorrents{follow: []string{"aa"}, stop: []string{"bb"}, watch: filepath.Join(mr, "torrents/movies")}
	o.Torrents = ft
	write(t, filepath.Join(mr, "media/archive/.keep"), 0)
	if err := o.Move(ctx, "downloads", "movies", "library", "archive", true); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(mr, "torrents/movies")
	if len(ft.stopped) != 1 || ft.stopped[0] != "bb" || len(ft.paused) != 1 || !ft.existedAtStop[0] {
		t.Fatalf("stopped %v", ft.stopped)
	}
	if len(ft.relocated) != 1 || ft.relocated[0] != [2]string{src, filepath.Join(mr, "media/archive/movies")} {
		t.Fatalf("relocated %v", ft.relocated)
	}
	if exists(src) {
		t.Fatal("not moved")
	}

	write(t, filepath.Join(mr, "torrents/B/b.mkv"), 1)
	ft2 := &fakeTorrents{stop: []string{"cc"}, watch: filepath.Join(mr, "torrents/B/b.mkv")}
	o.Torrents = ft2
	if err := o.Rename(ctx, "downloads", "B/b.mkv", "c.mkv", true); err != nil {
		t.Fatal(err)
	}
	if len(ft2.stopped) != 1 || len(ft2.relocated) != 0 || !exists(filepath.Join(mr, "torrents/B/c.mkv")) {
		t.Fatalf("stopped %v relocated %v", ft2.stopped, ft2.relocated)
	}
	if p := ft2.plans[0]; p[0] != filepath.Join(mr, "torrents/B/b.mkv") || p[1] != filepath.Join(mr, "torrents/B/c.mkv") {
		t.Fatalf("plan %v", p)
	}
}

// Deferred minor (Task 17a): the stream cache is app-managed: no new
// folders and no renames there (it prunes by download, renamed files would
// be orphaned).
func TestStreamCacheMkdirRenameRefused(t *testing.T) {
	o, mr, _ := testOps(t)
	ctx := context.Background()
	write(t, filepath.Join(mr, "tmp/vault-stream/abc/Movie.mkv"), 1)
	if err := o.Mkdir(ctx, "stream-cache", "", "new"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("mkdir: %v", err)
	}
	if err := o.Rename(ctx, "stream-cache", "abc/Movie.mkv", "x.mkv", true); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("rename: %v", err)
	}
	if err := o.Rename(ctx, "disk", "tmp/vault-stream/abc", "zzz", true); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("rename through the disk root: %v", err)
	}
	if err := o.Mkdir(ctx, "downloads", "", "ok"); err != nil {
		t.Fatalf("mkdir elsewhere: %v", err)
	}
}

// C-N2: a rename that fails after the downloads were held releases them: they
// are not stopped (and their rows not dropped) for a move that never happened.
func TestFailedMoveReleasesTorrents(t *testing.T) {
	o, mr, _ := testOps(t)
	write(t, filepath.Join(mr, "torrents/B/b.mkv"), 1)
	ft := &fakeTorrents{stop: []string{"cc"}, watch: filepath.Join(mr, "torrents/B/b.mkv")}
	o.Torrents = ft
	err := o.moveData(context.Background(), filepath.Join(mr, "torrents/B/missing.mkv"), filepath.Join(mr, "torrents/B/x.mkv"))
	if err == nil {
		t.Fatal("rename of a missing file worked")
	}
	if len(ft.paused) != 1 || len(ft.resumed) != 1 || len(ft.stopped) != 0 {
		t.Fatalf("paused %v resumed %v stopped %v", ft.paused, ft.resumed, ft.stopped)
	}
}
