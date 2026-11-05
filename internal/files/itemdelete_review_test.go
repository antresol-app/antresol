package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// setLink marks library files as made from the download key.
func (e *itemEnv) setLink(t *testing.T, key string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := e.d.Ops.DB.Exec(`UPDATE files SET torrent_infohash=? WHERE path=?`, key, p); err != nil {
			t.Fatal(err)
		}
	}
}

// Linked data goes to the trash first (the trash stops what holds it) and
// is listed in the result — but only once everything the library made from
// it goes: a download that also gave another item keeps running.
func TestDeleteLinkedDataOnlyWhenCoveredWhole(t *testing.T) {
	e := newItemEnv(t)
	ctx := context.Background()
	a := filepath.Join(e.mv, "Брат (1997)", "Брат (1997).mkv")
	b := filepath.Join(e.mv, "Брат 2 (2000)", "Брат 2 (2000).mkv")
	write(t, a, 4)
	write(t, b, 4)
	data := filepath.Join(e.mr, "torrents", "Brat.Collection")
	write(t, filepath.Join(data, "Brat.mkv"), 4)
	write(t, filepath.Join(data, "Brat2.mkv"), 4)
	e.rescan(t)
	e.setLink(t, "aa", a, b)
	ia, ib := e.itemOf(t, a), e.itemOf(t, b)
	// (the fake answers every path: it only holds the download's data)
	ft := &fakeTorrents{watch: data}
	e.d.Ops.Torrents = ft
	e.d.Linked = func(context.Context, library.DeleteSet) []Linked { return []Linked{{Path: data, Key: "aa"}} }

	res, err := e.d.Delete(ctx, ia.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(data) || len(res.Trashed) != 1 || exists(a) {
		t.Fatalf("one of two: data %v, %+v", exists(data), res.Trashed)
	}
	for _, p := range ft.plans {
		if p[0] == data {
			t.Fatal("the download was asked to stop for one of its items")
		}
	}
	// the last one: the download goes too, first, and is listed
	ft.stop = []string{"aa"}
	res, err = e.d.Delete(ctx, ib.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if exists(data) || len(ft.stopped) == 0 || !ft.existedAtStop[0] || len(res.Trashed) != 2 || res.Trashed[0].Root != "downloads" {
		t.Fatalf("last one: data %v, stopped %v, %+v", exists(data), ft.stopped, res.Trashed)
	}
	if res.Size != 4 {
		t.Fatalf("size counts the library's files: %d", res.Size)
	}
}

// Nothing moves when one path cannot go into the trash: another file
// system, or a folder that cannot be written.
func TestDeletePreflightMovesNothing(t *testing.T) {
	e := newItemEnv(t)
	ctx := context.Background()
	f := filepath.Join(e.mv, "Брат (1997)", "Брат (1997).mkv")
	write(t, f, 4)
	data := filepath.Join(e.mr, "torrents", "Brat.1997")
	write(t, filepath.Join(data, "Brat.mkv"), 4)
	e.rescan(t)
	e.setLink(t, "aa", f)
	it := e.itemOf(t, f)
	e.d.Ops.Torrents = &fakeTorrents{stop: []string{"aa"}}
	e.d.Linked = func(context.Context, library.DeleteSet) []Linked { return []Linked{{Path: data, Key: "aa"}} }

	old := devOf
	t.Cleanup(func() { devOf = old })
	devOf = func(p string) (uint64, error) {
		if within(e.mv, p) {
			return 2, nil // the movies on another disk
		}
		return 1, nil
	}
	res, err := e.d.Delete(ctx, it.ID, 1)
	if !errors.Is(err, ErrCrossDevice) || len(res.Trashed) != 0 {
		t.Fatalf("cross-device: %v %+v", err, res)
	}
	if !exists(data) || !exists(f) {
		t.Fatal("something moved before the refusal")
	}
	devOf = old

	if os.Geteuid() != 0 { // root writes anywhere
		dir := filepath.Dir(f)
		os.Chmod(dir, 0o555)
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		if _, err := e.d.Delete(ctx, it.ID, 1); err == nil || !exists(data) || !exists(f) {
			t.Fatalf("read-only folder: %v, data %v", err, exists(data))
		}
	}
}

// A file left on its own takes only its own sidecars: «Alien.ru.srt» and
// «Alien.jpg», not «Alien.Resurrection.ru.srt» or «Alien.2.jpg».
func TestDeleteSidecarNames(t *testing.T) {
	for name, want := range map[string]bool{
		"Alien.jpg": true, "Alien.ru.srt": true, "Alien.en.forced.srt": true, "Alien.nfo": true,
		"Alien.Resurrection.ru.srt": false, "Alien.2.jpg": false, "Aliens.srt": false, "Alien-poster.jpg": false,
	} {
		if got := isSidecarName(name, "Alien"); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
	e := newItemEnv(t)
	dir := filepath.Join(e.mv, "Чужие")
	for _, n := range []string{"Alien (1979).mkv", "Alien (1979).Resurrection.srt", "Alien (1979).ru.srt", "Alien Resurrection (1997).mkv"} {
		write(t, filepath.Join(dir, n), 2)
	}
	e.rescan(t)
	it := e.itemOf(t, filepath.Join(dir, "Alien (1979).mkv"))
	res, err := e.d.Delete(context.Background(), it.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trashed) != 2 || !exists(filepath.Join(dir, "Alien (1979).Resurrection.srt")) {
		t.Fatalf("%+v", res.Trashed)
	}
}

// Above the item's own folder nothing else may be: a director's photo
// keeps his folder, only the film's folder goes.
func TestDeleteParentWithPicturesStays(t *testing.T) {
	e := newItemEnv(t)
	film := filepath.Join(e.mv, "Тарковский", "Солярис (1972)", "Солярис (1972).mkv")
	write(t, film, 3)
	write(t, filepath.Join(e.mv, "Тарковский", "Солярис (1972)", "poster.jpg"), 1)
	photo := filepath.Join(e.mv, "Тарковский", "photo.jpg")
	write(t, photo, 1)
	e.rescan(t)
	it := e.itemOf(t, film)
	res, err := e.d.Delete(context.Background(), it.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trashed) != 1 || res.Trashed[0].Path != "Тарковский/Солярис (1972)" || !exists(photo) {
		t.Fatalf("%+v", res.Trashed)
	}
	// with nothing else there the parent goes whole
	film2 := filepath.Join(e.mv, "Герман", "Хрусталёв (1998)", "Хрусталёв (1998).mkv")
	write(t, film2, 3)
	e.rescan(t)
	res, err = e.d.Delete(context.Background(), e.itemOf(t, film2).ID, 1)
	if err != nil || len(res.Trashed) != 1 || res.Trashed[0].Path != "Герман" {
		t.Fatalf("%+v %v", res.Trashed, err)
	}
}

// Deletes hold the scans and run one at a time; a second request for the
// same item while it runs is told so.
func TestDeleteHoldsScansAndRefusesTwice(t *testing.T) {
	e := newItemEnv(t)
	f := filepath.Join(e.mv, "Брат (1997)", "Брат (1997).mkv")
	write(t, f, 4)
	e.rescan(t)
	it := e.itemOf(t, f)
	var mu sync.Mutex
	held, entered, gate := 0, make(chan struct{}), make(chan struct{})
	e.d.Hold = func(context.Context) (func(), error) {
		mu.Lock()
		held++
		mu.Unlock()
		return func() {}, nil
	}
	beforeMove = func() { close(entered); <-gate }
	t.Cleanup(func() { beforeMove = nil })
	done := make(chan error, 1)
	go func() { _, err := e.d.Delete(context.Background(), it.ID, 1); done <- err }()
	<-entered
	if _, err := e.d.Delete(context.Background(), it.ID, 1); !errors.Is(err, ErrDeleting) {
		t.Fatalf("second request: %v", err)
	}
	close(gate)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delete hung")
	}
	if held != 1 {
		t.Fatalf("scans held %d times", held)
	}
}
