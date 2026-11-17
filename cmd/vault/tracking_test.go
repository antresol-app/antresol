package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/notify"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/antresol-app/antresol/internal/tracking"
)

type noLetters struct{}

func (noLetters) Enqueue(context.Context, notify.Event) (bool, error) { return false, nil }

// A letter about a title off the library links to its title page, in
// every edition (the library-only one too).
func TestTracking_TitlePath(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	s := newTrackingService(&tracking.Store{DB: d}, library.NewRepo(d), progress.New(d), noLetters{}, nil)
	if s.TitlePath == nil {
		t.Fatal("no title path")
	}
	if got := s.TitlePath(tracking.Title{Kind: "series", TMDBID: 1399}); got != "/title/series/1399" {
		t.Fatalf("series: %q", got)
	}
	if got := s.TitlePath(tracking.Title{Kind: "movie", TMDBID: 7}); got != "/title/movie/7" {
		t.Fatalf("movie: %q", got)
	}
}
