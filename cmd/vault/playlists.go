package main

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/antresol-app/antresol/internal/disk"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/musicbrainz"
	"github.com/antresol-app/antresol/internal/music"
	"github.com/antresol-app/antresol/internal/playlistimport"
)

// newPlaylistImport builds «Импорт медиатеки», core in both editions:
// playlists and favourites from files, matched on MusicBrainz (the shared
// client, so its 1 request/s limiter too; nil: songs cannot be matched)
// and rebuilt into playlists and stars after every album and when the job
// ends. Queue stays a true nil interface: with no download queue the
// service settles the albums by the library alone (playlistimport.
// LibraryStep); the sources build sets Queue and the rest (wire_full.go).
// usage is the disk the preview shows (nil: none).
func newPlaylistImport(database *sql.DB, bus *events.Bus, repo *library.Repo, mb *musicbrainz.Client, usage func() (disk.Usage, error)) *playlistimport.Service {
	svc := &playlistimport.Service{Store: &playlistimport.Store{DB: database}, Bus: bus, Library: repo, Disk: usage,
		Log: slog.Default().With("component", "import")}
	if mb != nil { // (a typed nil *Client would not be a nil Recordings)
		svc.Matcher = playlistimport.NewMatcher(mb)
	}
	musicStore := music.New(database)
	svc.Rebuild = func(ctx context.Context, jobID int64) error {
		return playlistimport.Rebuild(ctx, svc.Store, musicStore, jobID)
	}
	return svc
}
