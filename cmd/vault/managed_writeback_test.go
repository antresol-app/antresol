package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
)

// A watched mark written into a device's managed account must not reach
// the person's linked accounts (Shikimori, MyShows): the hook skips it.
func TestWatchedChangeSkipsManagedAccounts(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	users, repo, prog := auth.NewStore(d), library.NewRepo(d), progress.New(d)
	person, _ := users.CreateFirstAdmin(ctx, "Papa", "secret-pass")
	tv, err := users.CreateManaged(ctx, person.ID, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	item, err := repo.UpsertMovie(ctx, library.MovieInput{Title: "Film", Year: 2000, File: library.FileInput{Path: "/m/film.mkv", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	var posted []int64
	prog.OnWatchedChange = skipManaged(users, func(_ context.Context, uid, _ int64) { posted = append(posted, uid) })
	if err := prog.MarkWatched(ctx, tv.ID, item, true); err != nil {
		t.Fatal(err)
	}
	if err := prog.PlayedAt(ctx, tv.ID, item, 590_000, 600_000, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if len(posted) != 0 {
		t.Fatalf("a managed account's mark was posted: %v", posted)
	}
	if err := prog.MarkWatched(ctx, person.ID, item, true); err != nil {
		t.Fatal(err)
	}
	if len(posted) != 1 || posted[0] != person.ID {
		t.Fatalf("the person's own mark: %v", posted)
	}
}
