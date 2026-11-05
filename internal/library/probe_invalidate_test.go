package library

import (
	"context"
	"testing"
)

// F26: a file replaced in place (same path, other size or mtime) must be
// probed again; a rescan of the unchanged file keeps its probe.
func TestUpsertClearsProbeWhenFileChanges(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	in := MovieInput{Title: "Брат", Year: 1997, File: FileInput{Path: "/m/Брат.mkv", Size: 100, Mtime: 1000}}
	id, err := r.UpsertMovie(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	f, err := r.FileByPath(ctx, in.File.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetProbe(ctx, f.ID, `{"Container":"mkv"}`, 5000); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpsertMovie(ctx, in); err != nil { // unchanged rescan
		t.Fatal(err)
	}
	if f, _ = r.FileByPath(ctx, in.File.Path); f.ProbeJSON == "" || f.DurationMs != 5000 {
		t.Fatalf("unchanged file lost its probe: %+v", f)
	}
	for _, change := range []FileInput{{Path: in.File.Path, Size: 200, Mtime: 1000}, {Path: in.File.Path, Size: 200, Mtime: 2000}} {
		if err := r.SetProbe(ctx, f.ID, `{"Container":"mkv"}`, 5000); err != nil {
			t.Fatal(err)
		}
		in.File = change
		if got, err := r.UpsertMovie(ctx, in); err != nil || got != id {
			t.Fatalf("%v %v", got, err)
		}
		if f, _ = r.FileByPath(ctx, in.File.Path); f.ProbeJSON != "" || f.DurationMs != 0 {
			t.Fatalf("replaced file (%+v) kept the stale probe: %+v", change, f)
		}
	}
}
