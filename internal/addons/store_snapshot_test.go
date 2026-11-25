package addons

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// Every write through a Store is seen by the next read of any Store over the
// database (the manager's and the holds' are two), however the reads of the
// snapshot and the write interleave.
func TestStoreSnapshotFollowsEveryWrite(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	w, r := &Store{DB: d}, &Store{DB: d} // writer, and another Store reading
	version := func(id string) string {
		t.Helper()
		in, err := r.Get(ctx, id)
		if err != nil {
			return err.Error()
		}
		return in.Version
	}
	a := sample("alpha")
	if v := version("alpha"); v != ErrNotFound.Error() {
		t.Fatalf("before any write: %s", v)
	}
	if err := w.Put(ctx, a, "tok-1"); err != nil {
		t.Fatal(err)
	}
	if v := version("alpha"); v != "1.0.0" {
		t.Fatalf("after Put: %s", v)
	}
	if id, err := r.IDForToken(ctx, "tok-1"); err != nil || id != "alpha" {
		t.Fatalf("token after Put: %q %v", id, err)
	}
	a.Version = "1.1.0"
	if err := w.Save(ctx, a); err != nil {
		t.Fatal(err)
	}
	if v := version("alpha"); v != "1.1.0" {
		t.Fatalf("after Save: %s", v)
	}
	if err := w.SetEnabled(ctx, "alpha", false); err != nil {
		t.Fatal(err)
	}
	if in, _ := r.Get(ctx, "alpha"); in.Enabled {
		t.Fatal("after SetEnabled: still on")
	}
	if err := w.ClearPrevVersion(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if in, _ := r.Get(ctx, "alpha"); in.PrevVersion != "" {
		t.Fatalf("after ClearPrevVersion: %q", in.PrevVersion)
	}
	if err := w.SetToken(ctx, "alpha", "tok-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.IDForToken(ctx, "tok-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the old token after SetToken: %v", err)
	}
	if id, err := r.IDForToken(ctx, "tok-2"); err != nil || id != "alpha" {
		t.Fatalf("the new token: %q %v", id, err)
	}
	if err := w.Put(ctx, sample("beta"), "tok-3"); err != nil {
		t.Fatal(err)
	}
	if list, err := r.List(ctx); err != nil || len(list) != 2 {
		t.Fatalf("list after a second Put: %d %v", len(list), err)
	}
	if err := w.Delete(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if v := version("alpha"); v != ErrNotFound.Error() {
		t.Fatalf("after Delete: %s", v)
	}
	if _, err := r.IDForToken(ctx, "tok-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the token of a deleted addon: %v", err)
	}
	// a failed write drops the snapshot too (the row may have changed)
	if err := w.SetEnabled(ctx, "gone", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetEnabled of none: %v", err)
	}
}

// Readers racing a writer never leave a snapshot older than a write that has
// returned: after each Put, a read of every Store sees it.
func TestStoreSnapshotRacingReaders(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	d := openTestDB(t)
	w, r := &Store{DB: d}, &Store{DB: d}
	a := sample("alpha")
	if err := w.Put(ctx, a, "tok"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				r.Get(ctx, "alpha")
				r.List(ctx)
				r.IDForToken(ctx, "tok")
			}
		}()
	}
	for i := 0; i < 150; i++ {
		a.Version = fmt.Sprintf("1.%d.0", i)
		if err := w.Put(ctx, a, "tok"); err != nil {
			t.Fatal(err)
		}
		for _, s := range []*Store{w, r} {
			if in, err := s.Get(ctx, "alpha"); err != nil || in.Version != a.Version {
				t.Fatalf("write %d: read %q %v", i, in.Version, err)
			}
		}
	}
	stop()
	wg.Wait()
}

// One row that does not decode fails its own Get and the List, as the
// queries did, and no other Get.
func TestStoreSnapshotBadManifest(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: openTestDB(t)}
	for _, id := range []string{"alpha", "beta"} {
		if err := s.Put(ctx, sample(id), "tok-"+id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec(`UPDATE addons SET manifest='not json' WHERE id='beta'`); err != nil {
		t.Fatal(err)
	}
	s.changed() // a write behind the Store's back, told
	if _, err := s.Get(ctx, "alpha"); err != nil {
		t.Fatalf("a good row: %v", err)
	}
	if _, err := s.Get(ctx, "beta"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("the bad row: %v", err)
	}
	if _, err := s.List(ctx); err == nil {
		t.Fatal("List must fail on a bad row")
	}
	if id, err := s.IDForToken(ctx, "tok-beta"); err != nil || id != "beta" {
		t.Fatalf("token of the bad row: %q %v", id, err)
	}
}
