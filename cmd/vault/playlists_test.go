package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/music"
	"github.com/antresol-app/antresol/internal/playlistimport"
)

// matchOnAlbum matches every song to the library's album «Сборник», except
// "x" (an album it lacks).
type matchOnAlbum struct{}

func (matchOnAlbum) Match(_ context.Context, t playlistimport.Track) (playlistimport.Decision, error) {
	d := playlistimport.Decision{State: playlistimport.TrackMatched, RecordingID: "rec-" + t.Key, ReleaseGroup: "rg-have", Title: t.Title,
		Artist: "Кино", Album: "Сборник", Year: 1988}
	if t.Key == "x" {
		d.ReleaseGroup, d.Album = "rg-absent", "Нет такого"
	}
	return d, nil
}

// TestPlaylistImportWithoutQueueUsesLibrary: the service main.go builds has
// no download queue (a true nil interface: a typed nil would skip
// LibraryStep and nothing would ever settle), so a library-only server
// settles the albums by its library and the job ends, with the playlist
// built from the songs the library has.
func TestPlaylistImportWithoutQueueUsesLibrary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	u, err := auth.NewStore(database).CreateFirstAdmin(ctx, "Папа", "secret")
	if err != nil {
		t.Fatal(err)
	}
	repo := library.NewRepo(database)
	var in []library.TrackInput
	for i, title := range []string{"a", "b"} {
		in = append(in, library.TrackInput{File: library.FileInput{Path: "/m/Кино/Сборник/0" + string(rune('1'+i)) + ".flac", Size: 1, Mtime: 1},
			AlbumDir: "/m/Кино/Сборник", Album: "Сборник", Title: title, Track: i + 1})
	}
	albumID, err := repo.UpsertAlbumTracks(ctx, "Кино", in)
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := repo.AlbumTracks(ctx, albumID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, tr := range tracks {
		ids[tr.Title] = tr.ID
	}

	svc := newPlaylistImport(database, &events.Bus{}, repo, nil, nil)
	if svc.Queue != nil {
		t.Fatalf("Queue = %#v: the core build has none", svc.Queue)
	}
	if svc.Matcher != nil {
		t.Fatal("Matcher set without MusicBrainz (a typed nil)")
	}
	svc.Matcher, svc.Tick = matchOnAlbum{}, 5*time.Millisecond
	id, err := svc.Create(ctx, u.ID, playlistimport.Snapshot{Service: "file", Tracks: []playlistimport.SnapTrack{
		{Key: "a", Artist: "Кино", Title: "a"}, {Key: "b", Artist: "Кино", Title: "b"}, {Key: "x", Artist: "Кино", Title: "x"}},
		Playlists: []playlistimport.SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "x", "b"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx) }()
	defer func() { cancel(); <-done }()
	for {
		j, err := svc.Store.Job(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == playlistimport.StatusDone {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("job stuck in %s", j.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var pid int64
	if err := database.QueryRow(`SELECT COALESCE(playlist_id, 0) FROM import_playlists WHERE external_id = 'p1'`).Scan(&pid); err != nil || pid == 0 {
		t.Fatalf("playlist %d %v", pid, err)
	}
	_, got, err := music.New(database).Playlist(ctx, u.ID, pid)
	if err != nil || len(got) != 2 || got[0] != ids["a"] || got[1] != ids["b"] {
		t.Fatalf("playlist songs %v (want a, b = %v) %v", got, ids, err)
	}
}
