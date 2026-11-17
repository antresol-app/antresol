package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/addons"
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

// TestImportsAddonOf: only an addon that is on, declares import.services
// and has the imports permission takes the import's missing albums.
func TestImportsAddonOf(t *testing.T) {
	mk := func(enabled bool, hooks, perms []string) addons.Installed {
		return addons.Installed{ID: "a", Enabled: enabled, Manifest: addons.Manifest{ID: "a", Hooks: hooks, Permissions: perms}}
	}
	hooks, perms := []string{"import.services"}, []string{"imports"}
	for _, c := range []struct {
		name string
		list []addons.Installed
		want bool
	}{
		{"none", nil, false},
		{"on", []addons.Installed{mk(true, hooks, perms)}, true},
		{"off", []addons.Installed{mk(false, hooks, perms)}, false},
		{"no hook", []addons.Installed{mk(true, nil, perms)}, false},
		{"no permission", []addons.Installed{mk(true, hooks, []string{"events"})}, false},
		{"one of two", []addons.Installed{mk(true, nil, nil), mk(true, hooks, perms)}, true},
	} {
		if got := importsAddonOf(c.list); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// addons off: a queue that settles by the library alone
	svc := newPlaylistImport(nil, &events.Bus{}, nil, nil, nil)
	setImportQueue(svc, nil, nil)
	if svc.Queue == nil {
		t.Fatal("no queue set")
	}
}

// The sign-in tiles, the reads' stop, a Takeout's naming and the review's
// search come from the addons' import.* hooks; with addons off, none.
func TestSetImportHooks(t *testing.T) {
	svc := &playlistimport.Service{}
	setImportHooks(svc, nil)
	if svc.Connectors != nil || svc.Reads != nil || svc.Resolver != nil || svc.Catalog != nil {
		t.Fatalf("addons off: %+v", svc)
	}
	setImportHooks(svc, &addons.Hooks{})
	if svc.Connectors == nil || svc.Reads == nil || svc.Resolver == nil || svc.Catalog == nil {
		t.Fatalf("addons on: %+v", svc)
	}
}

// TestImportOffersOf: the page promises downloads by the queue's own rule
// (importsAddonOf) and a catalog search only while an addon that is on
// declares import.search; with addons off, neither.
func TestImportOffersOf(t *testing.T) {
	mk := func(enabled bool, hooks, perms []string) addons.Installed {
		return addons.Installed{ID: "a", Enabled: enabled, Manifest: addons.Manifest{ID: "a", Hooks: hooks, Permissions: perms}}
	}
	for _, c := range []struct {
		name string
		list []addons.Installed
		want playlistimport.Offers
	}{
		{"none", nil, playlistimport.Offers{}},
		{"downloads", []addons.Installed{mk(true, []string{"import.services"}, []string{"imports"})}, playlistimport.Offers{Downloads: true}},
		{"no permission", []addons.Installed{mk(true, []string{"import.services", "import.search"}, nil)}, playlistimport.Offers{Catalog: true}},
		{"catalog", []addons.Installed{mk(true, []string{"import.search"}, nil)}, playlistimport.Offers{Catalog: true}},
		{"off", []addons.Installed{mk(false, []string{"import.services", "import.search"}, []string{"imports"})}, playlistimport.Offers{}},
		{"other hooks only", []addons.Installed{mk(true, []string{"migrate.leftovers"}, []string{"events"})}, playlistimport.Offers{}},
		{"both", []addons.Installed{mk(true, []string{"import.services", "import.search"}, []string{"imports"})}, playlistimport.Offers{Downloads: true, Catalog: true}},
	} {
		if got := importOffersOf(c.list); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	// addons off: Offers answers neither
	svc := newPlaylistImport(nil, &events.Bus{}, nil, nil, nil)
	setImportQueue(svc, nil, nil)
	if svc.Offers == nil {
		t.Fatal("no Offers set")
	}
	if o := svc.Offers(context.Background()); o != (playlistimport.Offers{}) {
		t.Fatalf("addons off: %+v", o)
	}
}
