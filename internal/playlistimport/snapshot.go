// Package playlistimport moves a streaming service's library into Vault: a
// snapshot of liked songs, playlists and saved albums is previewed, matched
// to MusicBrainz release groups, acquired through the configured sources
// and rebuilt as playlists and stars.
package playlistimport

import (
	"strings"

	"github.com/antresol-app/antresol/internal/playlistimport/model"
)

// Services that can be imported from.
const (
	ServiceSpotify = model.ServiceSpotify
	ServiceApple   = model.ServiceApple
	ServiceYandex  = model.ServiceYandex
	ServiceYouTube = model.ServiceYouTube
	ServiceDeezer  = model.ServiceDeezer
	ServiceFile    = model.ServiceFile
)

// Services lists every known service.
var Services = model.Services

// Snapshot limits.
const (
	MaxTracks    = model.MaxTracks
	MaxPlaylists = model.MaxPlaylists
)

// Snapshot errors (their text is shown as is).
var (
	ErrTooLarge       = model.ErrTooLarge
	ErrUnknownService = model.ErrUnknownService
)

// The snapshot a reader gives (model: Snapshot.Normalize checks and
// cleans it).
type (
	Snapshot     = model.Snapshot
	SnapTrack    = model.SnapTrack
	SnapPlaylist = model.SnapPlaylist
	SnapAlbum    = model.SnapAlbum
)

// norm is the comparison form of a name: lower case, single spaces.
func norm(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
