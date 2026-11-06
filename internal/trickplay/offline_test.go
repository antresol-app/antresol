package trickplay

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

// Files listed but not on disk (an unmounted drive) are walked past
// without reading the library again for each of them.
func TestWorkerMissingFilesFewQueries(t *testing.T) {
	fx := newWorkerFixture(t)
	ctx := context.Background()
	gone := t.TempDir()
	for i := range 30 {
		p := filepath.Join(gone, fmt.Sprintf("gone%02d.mkv", i))
		if _, err := fx.repo.UpsertMovie(ctx, library.MovieInput{Title: fmt.Sprintf("Gone %d", i), File: library.FileInput{Path: p, Size: 100, Mtime: 1}}); err != nil {
			t.Fatal(err)
		}
		f, err := fx.repo.FileByPath(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.repo.SetProbe(ctx, f.ID, `{"Video":{"Width":1920,"Height":800}}`, 600_000); err != nil {
			t.Fatal(err)
		}
	}
	for _, title := range []string{"Old", "Watching", "Newest", "NoPicture"} {
		os.Remove(fx.paths[title])
	}
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fx.reads) != 0 {
		t.Fatalf("read %v", fx.order())
	}
	if fx.w.queries > 2 {
		t.Fatalf("%d candidate queries for a pass that made nothing", fx.w.queries)
	}
}

// Made previews still re-read the list (a request meanwhile goes next),
// and a Want between files is taken next.
func TestWorkerWantMidPass(t *testing.T) {
	fx := newWorkerFixture(t)
	ctx := context.Background()
	asked := false
	frame := fx.w.Frame
	fx.w.Frame = func(ctx context.Context, path string, at float64, w, h int) ([]byte, error) {
		if !asked && path == fx.paths["Watching"] {
			asked = true
			fx.w.Want(fx.ids["Old"])
		}
		return frame(ctx, path, at, w, h)
	}
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := fx.order(), []string{"Watching", "Old", "Newest"}; !eq(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

// While the media disk is offline a pass does nothing at all.
func TestWorkerOfflineSkipsPass(t *testing.T) {
	fx := newWorkerFixture(t)
	fx.w.Offline = func() string { return "offline" }
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fx.reads) != 0 || fx.w.queries != 0 {
		t.Fatalf("offline, yet read %v (%d queries)", fx.order(), fx.w.queries)
	}
}
