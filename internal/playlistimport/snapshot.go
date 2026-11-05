// Package playlistimport moves a streaming service's library into Vault: a
// snapshot of liked songs, playlists and saved albums is previewed, matched
// to MusicBrainz release groups, acquired through the configured sources
// and rebuilt as playlists and stars.
package playlistimport

import (
	"errors"
	"slices"
	"strings"
	"time"
)

// Services that can be imported from.
const (
	ServiceSpotify = "spotify"
	ServiceApple   = "apple"
	ServiceYandex  = "yandex"
	ServiceYouTube = "youtube"
	ServiceDeezer  = "deezer"
	ServiceFile    = "file"
)

// Services lists every known service.
var Services = []string{ServiceSpotify, ServiceApple, ServiceYandex, ServiceYouTube, ServiceDeezer, ServiceFile}

// Snapshot limits.
const (
	MaxTracks    = 50000
	MaxPlaylists = 2000
)

// Snapshot errors (their text is shown as is).
var (
	ErrTooLarge       = errors.New("Файл слишком большой")
	ErrUnknownService = errors.New("Неизвестный сервис")
)

// Snapshot is what a reader (a connector, a file parser, the native app)
// read from a service's library.
type Snapshot struct {
	Service   string         `json:"service"`
	Account   string         `json:"account"`
	Tracks    []SnapTrack    `json:"tracks"`
	Playlists []SnapPlaylist `json:"playlists"`
	Albums    []SnapAlbum    `json:"albums"`
}

// SnapTrack is one song. Key identifies it within the snapshot (the
// service's track id); empty: derived from artist, title and album.
type SnapTrack struct {
	Key         string     `json:"key"`
	Artist      string     `json:"artist"`
	Title       string     `json:"title"`
	Album       string     `json:"album"`
	AlbumArtist string     `json:"albumArtist"`
	ISRC        string     `json:"isrc"`
	DurationMs  int64      `json:"durationMs"`
	AddedAt     *time.Time `json:"addedAt"`
	Liked       bool       `json:"liked"`
}

// SnapPlaylist is a playlist; Entries are track keys in the service's
// order. Readable false: the service did not give its songs.
type SnapPlaylist struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Readable bool     `json:"readable"`
	Entries  []string `json:"entries"`
}

// SnapAlbum is a saved album.
type SnapAlbum struct {
	Key     string     `json:"key"`
	Artist  string     `json:"artist"`
	Title   string     `json:"title"`
	Year    int        `json:"year"`
	AddedAt *time.Time `json:"addedAt"`
}

// norm is the comparison form of a name: lower case, single spaces.
func norm(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func trackKey(artist, title, album string) string {
	return norm(artist) + "|" + norm(title) + "|" + norm(album)
}

// earlier returns the earlier of two optional times.
func earlier(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case b.Before(*a):
		return b
	}
	return a
}

func fill(dst *string, src string) {
	if *dst == "" {
		*dst = src
	}
}

// Normalize checks and cleans a snapshot: trims names, drops songs without
// an artist or title, merges songs of one key (liked if any copy is, the
// earliest date added), and drops playlist entries naming unknown songs.
func (s *Snapshot) Normalize() error {
	s.Service = strings.ToLower(strings.TrimSpace(s.Service))
	if !slices.Contains(Services, s.Service) {
		return ErrUnknownService
	}
	if len(s.Tracks) > MaxTracks || len(s.Playlists) > MaxPlaylists || len(s.Albums) > MaxTracks {
		return ErrTooLarge
	}
	s.Account = strings.TrimSpace(s.Account)

	tracks := make([]SnapTrack, 0, len(s.Tracks))
	at := make(map[string]int, len(s.Tracks))
	for _, t := range s.Tracks {
		t.Key = strings.TrimSpace(t.Key)
		t.Artist, t.Title = strings.TrimSpace(t.Artist), strings.TrimSpace(t.Title)
		t.Album, t.AlbumArtist = strings.TrimSpace(t.Album), strings.TrimSpace(t.AlbumArtist)
		t.ISRC = strings.ToUpper(strings.TrimSpace(t.ISRC))
		t.DurationMs = max(t.DurationMs, 0)
		if t.Artist == "" || t.Title == "" {
			continue
		}
		if t.Key == "" {
			t.Key = trackKey(t.Artist, t.Title, t.Album)
		}
		if i, ok := at[t.Key]; ok {
			d := &tracks[i]
			d.Liked = d.Liked || t.Liked
			d.AddedAt = earlier(d.AddedAt, t.AddedAt)
			fill(&d.Album, t.Album)
			fill(&d.AlbumArtist, t.AlbumArtist)
			fill(&d.ISRC, t.ISRC)
			if d.DurationMs == 0 {
				d.DurationMs = t.DurationMs
			}
			continue
		}
		at[t.Key] = len(tracks)
		tracks = append(tracks, t)
	}
	s.Tracks = tracks

	playlists := make([]SnapPlaylist, 0, len(s.Playlists))
	seen := map[string]bool{}
	for _, p := range s.Playlists {
		p.ID, p.Name = strings.TrimSpace(p.ID), strings.TrimSpace(p.Name)
		if p.ID == "" {
			if p.Name == "" {
				continue
			}
			p.ID = "name:" + norm(p.Name)
		}
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		// a reader that gave the songs need not say the playlist is readable
		p.Readable = p.Readable || len(p.Entries) > 0
		entries := make([]string, 0, len(p.Entries))
		for _, k := range p.Entries {
			if _, ok := at[strings.TrimSpace(k)]; ok {
				entries = append(entries, strings.TrimSpace(k))
			}
		}
		p.Entries = entries
		playlists = append(playlists, p)
	}
	s.Playlists = playlists

	albums := make([]SnapAlbum, 0, len(s.Albums))
	albumAt := map[string]int{}
	for _, a := range s.Albums {
		a.Key, a.Artist, a.Title = strings.TrimSpace(a.Key), strings.TrimSpace(a.Artist), strings.TrimSpace(a.Title)
		if a.Title == "" {
			continue
		}
		if a.Key == "" {
			a.Key = norm(a.Artist) + "|" + norm(a.Title)
		}
		if i, ok := albumAt[a.Key]; ok {
			albums[i].AddedAt = earlier(albums[i].AddedAt, a.AddedAt)
			continue
		}
		albumAt[a.Key] = len(albums)
		albums = append(albums, a)
	}
	s.Albums = albums
	return nil
}
