// Package library holds the media library model (items, files, titles),
// its SQLite repository and the video filesystem scanner.
package library

import (
	"strings"
	"time"
)

// Item kinds. Hierarchies (parent_id): series → season → episode;
// artist → album → track; comic → comic_volume. Movies, books and
// audiobooks stand alone.
//
// Who owns a files row: a plain track owns its file (files.item_id = the
// track, tracks.file_id = the same row); a CUE image belongs to its album
// and all the album's tracks point at that one file_id; a book, an
// audiobook and a comic volume own their files.
//
// items.match_key finds an item again on a rescan:
//   - album:     "<abs. album folder>|<release.Normalize(album)>"
//   - artist:    release.Normalize(name)
//   - book:      "<abs. folder>|<release.Normalize(title from parseBookFile)>"
//   - audiobook: "<abs. book folder>" (a single .m4b: "<abs. file path>")
//   - comic:     "<abs. series folder>" (files right in the root:
//     "<root>|<release.Normalize(series)>")
//   - comic_volume, track and video kinds: NULL (found by their file).
//
// The music scanner owns track rows: a rescan of a changed album folder
// rewrites track titles, numbers and artists from the tags (a future
// track-level enricher or manual edit needs a lock flag, e.g.
// meta_json.title_locked). Album title/year are only filled when empty.
const (
	KindMovie       = "movie"
	KindSeries      = "series"
	KindSeason      = "season"
	KindEpisode     = "episode"
	KindArtist      = "artist"
	KindAlbum       = "album"
	KindTrack       = "track"
	KindBook        = "book"
	KindAudiobook   = "audiobook"
	KindComic       = "comic"
	KindComicVolume = "comic_volume"
)

// KindMusic is the kind a request or catalog search names for an album (its
// rows are artist, album and track). The other request kinds are item kinds:
// KindMovie, KindSeries, KindBook, KindAudiobook, KindComic.
const KindMusic = "music"

// IsUnratedKind reports whether items of this kind carry no age rating of
// their own: music and books. Limited (kid) profiles see them when their
// rating is empty; a rating, when set, still applies. A comic volume
// follows its series.
func IsUnratedKind(kind string) bool {
	switch kind {
	case KindArtist, KindAlbum, KindTrack, KindBook, KindAudiobook, KindComic, KindComicVolume:
		return true
	}
	return false
}

// audioExt lists the audio files the music and audiobook scanners index
// (and the background probe inspects).
var audioExt = map[string]bool{
	".mp3": true, ".flac": true, ".m4a": true, ".m4b": true, ".aac": true, ".ogg": true, ".oga": true,
	".opus": true, ".wav": true, ".aiff": true, ".aif": true, ".ape": true, ".wv": true,
}

// ChapterInput is one audiobook chapter: FileIndex points into the book's
// files (in playback order), StartMs/EndMs are positions within that file
// (EndMs nil = to the end of the file).
type ChapterInput struct {
	Title     string
	FileIndex int
	StartMs   int64
	EndMs     *int64
}

// Item is a row of the items table. NULL columns are mapped to zero values.
// Poster/Backdrop are either an http(s) URL / cache key set by the metadata
// layer, or "file:<absolute path>" for artwork found next to the media.
type Item struct {
	ID            int64     `json:"id"`
	Kind          string    `json:"kind"`
	ParentID      int64     `json:"parent_id,omitempty"`
	Title         string    `json:"title"`
	OriginalTitle string    `json:"original_title,omitempty"`
	SortTitle     string    `json:"sort_title"`
	Year          int       `json:"year,omitempty"`
	Overview      string    `json:"overview,omitempty"`
	Rating        string    `json:"rating,omitempty"` // canonical RU form: 0+ 6+ 12+ 16+ 18+ (or raw if unrecognised)
	TmdbID        int64     `json:"tmdb_id,omitempty"`
	SeasonNo      int       `json:"season_no,omitempty"`
	EpisodeNo     int       `json:"episode_no,omitempty"`
	Poster        string    `json:"poster,omitempty"`
	Backdrop      string    `json:"backdrop,omitempty"`
	AddedAt       time.Time `json:"added_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	MetaJSON      string    `json:"meta_json,omitempty"`
}

// File is a row of the files table.
type File struct {
	ID              int64  `json:"id"`
	ItemID          int64  `json:"item_id"`
	Path            string `json:"path"`
	Size            int64  `json:"size"`
	Mtime           int64  `json:"mtime"`
	DurationMs      int64  `json:"duration_ms,omitempty"`
	ProbeJSON       string `json:"probe_json,omitempty"`
	TorrentInfohash string `json:"torrent_infohash,omitempty"`
}

// FileInput describes a media file to attach to an item.
type FileInput struct {
	Path     string
	Size     int64
	Mtime    int64
	Infohash string
}

// MovieInput is the scanner/acquire-side description of a movie.
type MovieInput struct {
	Title         string
	OriginalTitle string
	Year          int
	Overview      string
	Rating        string
	TmdbID        int64
	Poster        string
	Backdrop      string
	AltTitles     []string
	ExtIDs        map[string]string // meta_json keys "tvdb_id", "imdb_id" (read by the TMDB enricher)
	Genres        []string          // meta_json "genres" when it has none (nfo)
	File          FileInput         // used when Files is empty
	Files         []FileInput       // parts of one movie (cd1, cd2), in playback order
}

// EpisodeInput describes an episode together with its series. Series and
// season rows are created on demand. A series is found by tmdb id, else by
// normalised title.
type EpisodeInput struct {
	SeriesTitle    string
	SeriesOriginal string
	SeriesYear     int
	SeriesTmdbID   int64
	SeriesRating   string
	SeriesPoster   string
	SeriesBackdrop string
	SeriesAlt      []string
	SeriesExtIDs   map[string]string // meta_json keys "tvdb_id", "imdb_id" (read by the TMDB enricher)
	SeriesOverview string            // tvshow.nfo plot (fills an empty overview)
	SeriesGenres   []string          // meta_json "genres" when it has none (tvshow.nfo)
	Season         int
	Episode        int
	Title          string // episode title, optional
	Overview       string // episode nfo plot (fills an empty overview)
	File           FileInput
}

// Metadata is applied by SetMetadata; empty fields leave stored values unchanged.
type Metadata struct {
	Title         string
	OriginalTitle string
	Year          int
	Overview      string
	Rating        string
	TmdbID        int64
	Poster        string
	Backdrop      string
	MetaJSON      string
}

// ListQuery filters List. Empty Kind means movie+series; any item kind may
// be named. Sort: "added" (default, newest first), "title", "year" (newest
// first). MaxRating, when set, hides items above it and video without a
// recognised rating; music and books without a rating stay visible
// (IsUnratedKind), a comic volume follows its series. Owner, when set,
// keeps the titles that profile brought (item_owners: «Моя медиатека»).
type ListQuery struct {
	Kind      string
	Sort      string
	Limit     int
	Offset    int
	MaxRating string
	Owner     int64
	// NoTotal: List skips the count (its total is 0), for a shelf that
	// shows no total.
	NoTotal bool
}

// ScanStats summarises one ScanVideo / ScanAll run.
type ScanStats struct {
	Movies     int // movie files indexed
	Episodes   int // episode files indexed
	Albums     int
	Tracks     int
	Books      int
	Audiobooks int
	Comics     int // comic volumes (files or image folders) indexed
	Files      int
	Skipped    int // media files that could not be classified
	Removed    int // files dropped because they disappeared
}

var ruRatings = []string{"0+", "6+", "12+", "16+", "18+"}

var ratingRank = map[string]int{
	"0+": 0, "6+": 1, "12+": 2, "16+": 3, "18+": 4,
	"G": 0, "PG": 1, "PG-13": 2, "R": 3, "NC-17": 4,
	"TV-Y": 0, "TV-G": 0, "TV-Y7": 1, "TV-PG": 1, "TV-14": 3, "TV-MA": 4,
}

// RatingRank returns the position (0..4) of a rating on the RU scale.
// Accepts RU ("12+", "12"), MPAA/TV ("PG-13", "PG13", "TV-14", "TV-Y7-FV")
// and noisy forms ("Rated R", "RU-16+", "US:PG").
func RatingRank(r string) (int, bool) {
	for _, tok := range strings.FieldsFunc(strings.ToUpper(r), func(c rune) bool {
		return c == ' ' || c == ':' || c == '_' || c == '/' || c == ',' || c == '(' || c == ')'
	}) {
		tok = strings.TrimPrefix(tok, "RU-")
		tok = strings.TrimPrefix(tok, "US-")
		tok = strings.TrimSuffix(tok, "-FV")
		switch tok {
		case "PG13":
			tok = "PG-13"
		case "NC17":
			tok = "NC-17"
		case "0", "6", "12", "16", "18":
			tok += "+"
		}
		if v, ok := ratingRank[tok]; ok {
			return v, true
		}
	}
	return 0, false
}

// NormalizeRating maps a recognised rating to the canonical RU form and
// returns unrecognised non-empty input trimmed, as is.
func NormalizeRating(r string) string {
	if v, ok := RatingRank(r); ok {
		return ruRatings[v]
	}
	return strings.TrimSpace(r)
}
