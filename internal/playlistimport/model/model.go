// Package model holds the types and values of a playlist import that do
// not need its database: the snapshot a reader gives, the job, its songs
// and albums, their states and the import's errors. Package playlistimport
// names them under its own names (type aliases), so code built on either
// sees one type.
package model

import (
	"context"
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

// Job statuses: reading → preview → matching → downloading → done; failed,
// cancelled.
const (
	StatusReading     = "reading"
	StatusPreview     = "preview"
	StatusMatching    = "matching"
	StatusDownloading = "downloading"
	StatusDone        = "done"
	StatusFailed      = "failed"
	StatusCancelled   = "cancelled"
)

// Track states.
const (
	TrackPending     = "pending"
	TrackMatched     = "matched"
	TrackReview      = "review"
	TrackSkipped     = "skipped"
	TrackHave        = "have"
	TrackQueued      = "queued"
	TrackDownloading = "downloading"
	TrackDone        = "done"
	TrackNotFound    = "not_found"
	TrackFailed      = "failed"
)

// Group states.
const (
	GroupWaiting      = "waiting"
	GroupSearching    = "searching"
	GroupWaitingSpace = "waiting_space"
	// GroupWaitingDownload: the album downloads elsewhere (another job's
	// download, or «На сервер»): Group.WaitingFor names it.
	GroupWaitingDownload = "waiting_download"
	GroupDownloading     = "downloading"
	GroupDone            = "done"
	GroupHave            = "have"
	GroupNotFound        = "not_found"
	// GroupNoFormat: releases exist, none in «Формат загрузки»; it waits
	// for «Скачать в другом формате» (Group.AnyFormat).
	GroupNoFormat = "no_format"
	GroupFailed   = "failed"
)

// Store errors (their text is shown as is).
var (
	ErrNotFound = errors.New("Не найдено")
	ErrBusy     = errors.New("Импорт уже идёт")
)

// ErrNotReview: the song is not waiting for a decision (shown as is).
var ErrNotReview = errors.New("Не найдено")

// ErrNotRetryable: the group is not waiting for «Повторить» (shown as is).
var ErrNotRetryable = errors.New("Не найдено")

// ErrBadLink: the text is not a link to a playlist of the service.
var ErrBadLink = errors.New("playlistimport: not a playlist link")

// Job is one import run.
type Job struct {
	ID, UserID int64
	Service    string
	Account    string
	Status     string
	Favorites  bool // liked songs become «Избранное»
	Error      string
	Report     Report
	CreatedAt  time.Time
	UpdatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// Active reports whether the job is matching or downloading.
func (j Job) Active() bool { return j.Status == StatusMatching || j.Status == StatusDownloading }

// Report is what the import could not do (report_json).
type Report struct {
	SavedAlbumsMissing int `json:"savedAlbumsMissing"`
	// Unresolved: songs an uploaded file named only by an id (a Takeout
	// playlist video not in its library), left out of the import.
	Unresolved int `json:"unresolved"`
}

// Track is one song of a job.
type Track struct {
	ID, JobID      int64
	Key            string
	Artist, Title  string
	Album          string
	AlbumArtist    string
	ISRC           string
	DurationMs     int64
	AddedAt        *time.Time
	Liked          bool
	State, Reason  string
	MBRecording    string
	MBReleaseGroup string
	MBTitle        string
	MBArtist       string
	MBAlbum        string
	MBYear         int
	GroupID        int64 // 0: none
	ItemID         int64 // 0: none
}

// Decision is what matching decided for a song: State matched (with the
// recording and release group) or review (with the Russian Reason).
type Decision struct {
	State        string
	Reason       string
	RecordingID  string
	ReleaseGroup string
	Title        string
	Artist       string
	Album        string
	Year         int
	Aliases      []string
}

// Candidate is an album the catalog offers for a song in review (the
// fields the import's search and candidates endpoints serialize).
type Candidate struct {
	TMDBID        int    `json:"tmdbId"`
	ExtID         string `json:"extId"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	Subtitle      string `json:"subtitle"`
	Year          int    `json:"year"`
	Poster        string `json:"poster"`
	Overview      string `json:"overview"`
	InLibrary     bool   `json:"inLibrary"`
	LibraryItemID *int64 `json:"libraryItemId"`
	// Aliases: other names of the album, sent back with the request.
	Aliases []string `json:"aliases,omitempty"`
	// MatchedTrack: music found by a song title — the song of the query
	// the album holds; omitted otherwise.
	MatchedTrack string `json:"matchedTrack,omitempty"`
	// ByArtist: music of the artist the query names, listed first; omitted
	// for the other matches.
	ByArtist string `json:"byArtist,omitempty"`
}

// Catalog is the music catalog search for the review list (in the full
// build acquire.Service.DiscoverMedia, adapted).
type Catalog interface {
	DiscoverMedia(ctx context.Context, kind, q string) ([]Candidate, error)
}

// ServiceInfo is a tile of «Импорт»: how the service connects (redirect,
// paste, device code, a file, the app), the owner's setup steps while it is
// not configured, and the files it can also be read from.
type ServiceInfo struct {
	ID         string   `json:"id"`
	Configured bool     `json:"configured"`
	Connect    string   `json:"connect"`
	Setup      []string `json:"setup"`
	Files      []string `json:"files"`
}

// PublicPlaylist is a playlist anyone with its link can open.
type PublicPlaylist struct {
	ID   string
	Name string
}

// Group is one album of a job to acquire. Songs counts its selected songs.
type Group struct {
	ID, JobID     int64
	ReleaseGroup  string
	Artist, Album string
	Year          int
	Aliases       []string
	AlbumItemID   int64 // the library album its songs are in (0: none yet)
	State         string
	InfoHash      string
	Bytes         int64
	Attempts      int
	Error         string
	UpdatedAt     time.Time
	Songs         int
	// WaitingFor names the download a waiting_download album waits for;
	// WaitDone, WaitAt: its bytes and when they last grew; SkipInfoHash: a
	// download given up on (its own search leaves it out).
	WaitingFor   string
	SkipInfoHash string
	// AnyFormat: «Скачать в другом формате» — its search takes a release
	// of any format.
	AnyFormat bool
	// Dead: its own downloads given up for making no headway (oldest
	// first); its search leaves those releases out. Slow: given up for
	// being too slow; its search takes them only when nothing else holds
	// the songs.
	Dead, Slow []string
	// WaitDone is also a downloading album's bytes when they last grew;
	// Clock its download's running time (Queue.headway).
	WaitDone int64
	WaitAt   time.Time
	Clock    RunClock
}

// RunClock is a downloading album's own download's clock (run_s, quiet_s,
// run_base, wait_done): how long it has run, how long since its bytes
// last grew, its bytes when the clock started (-1: not yet) and then.
type RunClock struct {
	Run, Quiet time.Duration
	Base, Done int64
}

// AutoRetries: a group failed this many times is retried automatically.
const AutoRetries = 1

// TrackIDs are the ids of the songs.
func TrackIDs(ts []Track) []int64 {
	out := make([]int64, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}
