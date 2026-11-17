package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/antresol-app/antresol/internal/addons"
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
// ends. Queue stays a true nil interface here (the albums are settled by
// the library alone, playlistimport.LibraryStep) until main sets it once,
// with the addons known: setImportQueue. usage is the disk the preview
// shows (nil: none).
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

// setImportQueue sets the import's download phase, once, before it runs:
// the albums the library lacks are left to an addon that declares
// import.services with the imports permission while one is installed and
// on (running or not); with none (or addons off: mgr nil) they are
// settled by the library alone. Offers tells the page the same, and
// whether an addon answers the review's search (importOffersOf).
func setImportQueue(svc *playlistimport.Service, repo *library.Repo, mgr *addons.Manager) {
	var finder playlistimport.AlbumFinder
	if repo != nil { // (a typed nil *Repo would not be a nil AlbumFinder)
		finder = repo
	}
	enabled := func() (bool, error) { return false, nil }
	svc.Offers = func(context.Context) playlistimport.Offers { return playlistimport.Offers{} }
	if mgr != nil && mgr.Store != nil {
		// a read error is not "no addon": the queue skips that tick (the
		// service logs it) rather than settle the addon's albums
		enabled = func() (bool, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			list, err := mgr.Store.List(ctx)
			if err != nil {
				return false, fmt.Errorf("addons list: %w", err)
			}
			return importsAddonOf(list), nil
		}
		// for display only: a read error promises nothing
		svc.Offers = func(ctx context.Context) playlistimport.Offers {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			list, err := mgr.Store.List(ctx)
			if err != nil {
				svc.Logger().Warn("import offers: addons list", "err", err)
				return playlistimport.Offers{}
			}
			return importOffersOf(list)
		}
	}
	svc.Queue = playlistimport.NewAddonDownloader(svc.Store, finder, enabled)
}

// setImportHooks gives the import the addons' import.* hooks, once,
// before it runs: the sign-in tiles (import.services), the stop of their
// reads (import.stop), the naming of a Takeout's videos (import.resolve)
// and the review's album search (import.search). With addons off (h nil)
// none: the import reads files alone and the search answers 503.
func setImportHooks(svc *playlistimport.Service, h *addons.Hooks) {
	if h == nil {
		return
	}
	svc.Connectors, svc.Reads, svc.Resolver, svc.Catalog = h.ImportServices(), h.ImportStopper(), h.ImportResolver(), h.ImportCatalog()
}

// importsAddonOf: list has an addon that is on, declares import.services
// and may use the imports operations.
func importsAddonOf(list []addons.Installed) bool {
	for _, in := range list {
		if in.Enabled && slices.Contains(in.Manifest.Hooks, "import.services") && slices.Contains(in.Manifest.Permissions, "imports") {
			return true
		}
	}
	return false
}

// importOffersOf is what the Import page may promise for list: downloads
// by the queue's own rule (importsAddonOf), a catalog search while an
// addon that is on declares import.search.
func importOffersOf(list []addons.Installed) playlistimport.Offers {
	o := playlistimport.Offers{Downloads: importsAddonOf(list)}
	for _, in := range list {
		if in.Enabled && slices.Contains(in.Manifest.Hooks, "import.search") {
			o.Catalog = true
		}
	}
	return o
}
