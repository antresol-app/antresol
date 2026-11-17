package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// The voice-over fit worker runs in every build, whenever the server has
// a database and ffmpeg (E8): no build leaves it to an edition.
func TestFitWorker_RunsInCoreBuild(t *testing.T) {
	ff := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ff, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &sql.DB{}
	if !fitRuns(d, ff) {
		t.Fatal("a database and ffmpeg: the fit worker does not run")
	}
	if fitRuns(nil, ff) {
		t.Fatal("no database: the fit worker runs")
	}
	for _, p := range []string{"", filepath.Join(t.TempDir(), "none")} {
		if fitRuns(d, p) {
			t.Fatalf("ffmpeg %q missing: the fit worker runs", p)
		}
	}
}
