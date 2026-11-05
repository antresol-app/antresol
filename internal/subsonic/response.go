package subsonic

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"log"
	"net/http"

	"github.com/antresol-app/antresol/internal/i18n"
)

// APIVersion is the Subsonic API version Vault speaks.
const APIVersion = "1.16.1"

// Subsonic error codes (answered with HTTP 200).
const (
	codeGeneric    = 0
	codeMissing    = 10
	codeAuth       = 40
	codeForbidden  = 50
	codeNotFound   = 70
	msgInternal    = "Что-то пошло не так"
	msgNotFound    = "Не найдено"
	msgSongMissing = "Песня не найдена"
)

// response is <subsonic-response>: the envelope plus at most one payload.
// XML puts scalars in attributes and lists in child elements; JSON uses the
// same names (wrapped in {"subsonic-response": …}).
type response struct {
	XMLName       xml.Name `xml:"subsonic-response" json:"-"`
	Xmlns         string   `xml:"xmlns,attr" json:"-"`
	Status        string   `xml:"status,attr" json:"status"`
	Version       string   `xml:"version,attr" json:"version"`
	Type          string   `xml:"type,attr" json:"type"`
	ServerVersion string   `xml:"serverVersion,attr" json:"serverVersion"`
	OpenSubsonic  bool     `xml:"openSubsonic,attr" json:"openSubsonic"`

	Error                  *apiError          `xml:"error,omitempty" json:"error,omitempty"`
	License                *license           `xml:"license,omitempty" json:"license,omitempty"`
	OpenSubsonicExtensions []extension        `xml:"openSubsonicExtensions,omitempty" json:"openSubsonicExtensions,omitempty"`
	MusicFolders           *musicFolders      `xml:"musicFolders,omitempty" json:"musicFolders,omitempty"`
	Indexes                *indexes           `xml:"indexes,omitempty" json:"indexes,omitempty"`
	Artists                *artistsID3        `xml:"artists,omitempty" json:"artists,omitempty"`
	Artist                 *artistWithAlbums  `xml:"artist,omitempty" json:"artist,omitempty"`
	Album                  *albumWithSongs    `xml:"album,omitempty" json:"album,omitempty"`
	AlbumList              *albumList         `xml:"albumList,omitempty" json:"albumList,omitempty"`
	AlbumList2             *albumList2        `xml:"albumList2,omitempty" json:"albumList2,omitempty"`
	Song                   *child             `xml:"song,omitempty" json:"song,omitempty"`
	RandomSongs            *songs             `xml:"randomSongs,omitempty" json:"randomSongs,omitempty"`
	Genres                 *genres            `xml:"genres,omitempty" json:"genres,omitempty"`
	SearchResult2          *searchResult2     `xml:"searchResult2,omitempty" json:"searchResult2,omitempty"`
	SearchResult3          *searchResult3     `xml:"searchResult3,omitempty" json:"searchResult3,omitempty"`
	Playlists              *playlists         `xml:"playlists,omitempty" json:"playlists,omitempty"`
	Playlist               *playlistWithSongs `xml:"playlist,omitempty" json:"playlist,omitempty"`
	Starred                *starred           `xml:"starred,omitempty" json:"starred,omitempty"`
	Starred2               *starred2          `xml:"starred2,omitempty" json:"starred2,omitempty"`
	Lyrics                 *lyrics            `xml:"lyrics,omitempty" json:"lyrics,omitempty"`
	LyricsList             *lyricsList        `xml:"lyricsList,omitempty" json:"lyricsList,omitempty"`
	User                   *user              `xml:"user,omitempty" json:"user,omitempty"`
	Directory              *directory         `xml:"directory,omitempty" json:"directory,omitempty"`

	// empty answers of methods Vault has nothing for (see stubs.go)
	ScanStatus            *scanStatus    `xml:"scanStatus,omitempty" json:"scanStatus,omitempty"`
	NowPlaying            *entries       `xml:"nowPlaying,omitempty" json:"nowPlaying,omitempty"`
	ArtistInfo2           *info          `xml:"artistInfo2,omitempty" json:"artistInfo2,omitempty"`
	ArtistInfo            *info          `xml:"artistInfo,omitempty" json:"artistInfo,omitempty"`
	AlbumInfo             *info          `xml:"albumInfo,omitempty" json:"albumInfo,omitempty"`
	TopSongs              *songs         `xml:"topSongs,omitempty" json:"topSongs,omitempty"`
	SimilarSongs2         *songs         `xml:"similarSongs2,omitempty" json:"similarSongs2,omitempty"`
	SimilarSongs          *songs         `xml:"similarSongs,omitempty" json:"similarSongs,omitempty"`
	Bookmarks             *bookmarks     `xml:"bookmarks,omitempty" json:"bookmarks,omitempty"`
	Podcasts              *podcasts      `xml:"podcasts,omitempty" json:"podcasts,omitempty"`
	InternetRadioStations *radioStations `xml:"internetRadioStations,omitempty" json:"internetRadioStations,omitempty"`
}

// directory is getMusicDirectory: an artist with its albums, or an album
// with its songs.
type directory struct {
	ID     string  `xml:"id,attr" json:"id"`
	Parent string  `xml:"parent,attr,omitempty" json:"parent,omitempty"`
	Name   string  `xml:"name,attr" json:"name"`
	Child  []child `xml:"child" json:"child"`
}

type scanStatus struct {
	Scanning bool `xml:"scanning,attr" json:"scanning"`
	Count    int  `xml:"count,attr" json:"count"`
}

type entries struct {
	Entry []child `xml:"entry" json:"entry"`
}

type info struct{}

type bookmarks struct {
	Bookmark []struct{} `xml:"bookmark" json:"bookmark"`
}

type podcasts struct {
	Channel []struct{} `xml:"channel" json:"channel"`
}

type radioStations struct {
	InternetRadioStation []struct{} `xml:"internetRadioStation" json:"internetRadioStation"`
}

type apiError struct {
	Code    int    `xml:"code,attr" json:"code"`
	Message string `xml:"message,attr" json:"message"`
}

type license struct {
	Valid bool `xml:"valid,attr" json:"valid"`
}

type extension struct {
	Name     string `xml:"name,attr" json:"name"`
	Versions []int  `xml:"versions" json:"versions"`
}

type musicFolder struct {
	ID   int    `xml:"id,attr" json:"id"`
	Name string `xml:"name,attr" json:"name"`
}

type musicFolders struct {
	MusicFolder []musicFolder `xml:"musicFolder" json:"musicFolder"`
}

// child is a song (or, in getAlbumList and search2, an album as a
// directory). Field names and meanings follow Navidrome.
type child struct {
	ID          string `xml:"id,attr" json:"id"`
	Parent      string `xml:"parent,attr,omitempty" json:"parent,omitempty"`
	IsDir       bool   `xml:"isDir,attr" json:"isDir"`
	Title       string `xml:"title,attr" json:"title"`
	Album       string `xml:"album,attr,omitempty" json:"album,omitempty"`
	Artist      string `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	Track       int    `xml:"track,attr,omitempty" json:"track,omitempty"`
	DiscNumber  int    `xml:"discNumber,attr,omitempty" json:"discNumber,omitempty"`
	Year        int    `xml:"year,attr,omitempty" json:"year,omitempty"`
	Genre       string `xml:"genre,attr,omitempty" json:"genre,omitempty"`
	CoverArt    string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	Size        int64  `xml:"size,attr,omitempty" json:"size,omitempty"`
	ContentType string `xml:"contentType,attr,omitempty" json:"contentType,omitempty"`
	Suffix      string `xml:"suffix,attr,omitempty" json:"suffix,omitempty"`
	Duration    int    `xml:"duration,attr,omitempty" json:"duration,omitempty"`
	BitRate     int    `xml:"bitRate,attr,omitempty" json:"bitRate,omitempty"`
	Path        string `xml:"path,attr,omitempty" json:"path,omitempty"`
	Created     string `xml:"created,attr,omitempty" json:"created,omitempty"`
	Starred     string `xml:"starred,attr,omitempty" json:"starred,omitempty"`
	AlbumID     string `xml:"albumId,attr,omitempty" json:"albumId,omitempty"`
	ArtistID    string `xml:"artistId,attr,omitempty" json:"artistId,omitempty"`
	Type        string `xml:"type,attr,omitempty" json:"type,omitempty"`
	// ReplayGain (OpenSubsonic): only on songs with a known gain
	ReplayGain *replayGain `xml:"replayGain,omitempty" json:"replayGain,omitempty"`
}

// replayGain is the OpenSubsonic ReplayGain of a song: gains in dB on the
// ReplayGain reference, peaks linear; a value not known is left out.
type replayGain struct {
	TrackGain *float64 `xml:"trackGain,attr,omitempty" json:"trackGain,omitempty"`
	AlbumGain *float64 `xml:"albumGain,attr,omitempty" json:"albumGain,omitempty"`
	TrackPeak *float64 `xml:"trackPeak,attr,omitempty" json:"trackPeak,omitempty"`
	AlbumPeak *float64 `xml:"albumPeak,attr,omitempty" json:"albumPeak,omitempty"`
}

type artistID3 struct {
	ID         string `xml:"id,attr" json:"id"`
	Name       string `xml:"name,attr" json:"name"`
	CoverArt   string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	AlbumCount int    `xml:"albumCount,attr" json:"albumCount"`
	Starred    string `xml:"starred,attr,omitempty" json:"starred,omitempty"`
}

type artistWithAlbums struct {
	artistID3
	Album []albumID3 `xml:"album" json:"album"`
}

type index struct {
	Name   string      `xml:"name,attr" json:"name"`
	Artist []artistID3 `xml:"artist" json:"artist"`
}

type indexes struct {
	LastModified    int64   `xml:"lastModified,attr" json:"lastModified"`
	IgnoredArticles string  `xml:"ignoredArticles,attr" json:"ignoredArticles"`
	Index           []index `xml:"index" json:"index"`
}

type artistsID3 struct {
	IgnoredArticles string  `xml:"ignoredArticles,attr" json:"ignoredArticles"`
	Index           []index `xml:"index" json:"index"`
}

type albumID3 struct {
	ID        string `xml:"id,attr" json:"id"`
	Name      string `xml:"name,attr" json:"name"`
	Artist    string `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	ArtistID  string `xml:"artistId,attr,omitempty" json:"artistId,omitempty"`
	CoverArt  string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	SongCount int    `xml:"songCount,attr" json:"songCount"`
	Duration  int    `xml:"duration,attr" json:"duration"`
	Created   string `xml:"created,attr" json:"created"`
	Year      int    `xml:"year,attr,omitempty" json:"year,omitempty"`
	Genre     string `xml:"genre,attr,omitempty" json:"genre,omitempty"`
	Starred   string `xml:"starred,attr,omitempty" json:"starred,omitempty"`
}

type albumWithSongs struct {
	albumID3
	Song []child `xml:"song" json:"song"`
}

type albumList struct {
	Album []child `xml:"album" json:"album"`
}

type albumList2 struct {
	Album []albumID3 `xml:"album" json:"album"`
}

type songs struct {
	Song []child `xml:"song" json:"song"`
}

type genre struct {
	SongCount  int    `xml:"songCount,attr" json:"songCount"`
	AlbumCount int    `xml:"albumCount,attr" json:"albumCount"`
	Value      string `xml:",chardata" json:"value"`
}

type genres struct {
	Genre []genre `xml:"genre" json:"genre"`
}

type searchResult2 struct {
	Artist []artistID3 `xml:"artist" json:"artist"`
	Album  []child     `xml:"album" json:"album"`
	Song   []child     `xml:"song" json:"song"`
}

type searchResult3 struct {
	Artist []artistID3 `xml:"artist" json:"artist"`
	Album  []albumID3  `xml:"album" json:"album"`
	Song   []child     `xml:"song" json:"song"`
}

type playlist struct {
	ID        string `xml:"id,attr" json:"id"`
	Name      string `xml:"name,attr" json:"name"`
	Owner     string `xml:"owner,attr" json:"owner"`
	Public    bool   `xml:"public,attr" json:"public"`
	SongCount int    `xml:"songCount,attr" json:"songCount"`
	Duration  int    `xml:"duration,attr" json:"duration"`
	Created   string `xml:"created,attr" json:"created"`
	Changed   string `xml:"changed,attr" json:"changed"`
	CoverArt  string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
}

type playlists struct {
	Playlist []playlist `xml:"playlist" json:"playlist"`
}

type playlistWithSongs struct {
	playlist
	Entry []child `xml:"entry" json:"entry"`
}

type starred struct {
	Artist []artistID3 `xml:"artist" json:"artist"`
	Album  []child     `xml:"album" json:"album"`
	Song   []child     `xml:"song" json:"song"`
}

type starred2 struct {
	Artist []artistID3 `xml:"artist" json:"artist"`
	Album  []albumID3  `xml:"album" json:"album"`
	Song   []child     `xml:"song" json:"song"`
}

type lyrics struct {
	Artist string `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	Title  string `xml:"title,attr,omitempty" json:"title,omitempty"`
	Value  string `xml:",chardata" json:"value"`
}

type lyricLine struct {
	Start *int64 `xml:"start,attr,omitempty" json:"start,omitempty"`
	Value string `xml:",chardata" json:"value"`
}

type structuredLyrics struct {
	DisplayArtist string      `xml:"displayArtist,attr,omitempty" json:"displayArtist,omitempty"`
	DisplayTitle  string      `xml:"displayTitle,attr,omitempty" json:"displayTitle,omitempty"`
	Lang          string      `xml:"lang,attr" json:"lang"`
	Synced        bool        `xml:"synced,attr" json:"synced"`
	Line          []lyricLine `xml:"line" json:"line"`
}

type lyricsList struct {
	StructuredLyrics []structuredLyrics `xml:"structuredLyrics" json:"structuredLyrics"`
}

type user struct {
	Username            string `xml:"username,attr" json:"username"`
	ScrobblingEnabled   bool   `xml:"scrobblingEnabled,attr" json:"scrobblingEnabled"`
	AdminRole           bool   `xml:"adminRole,attr" json:"adminRole"`
	SettingsRole        bool   `xml:"settingsRole,attr" json:"settingsRole"`
	DownloadRole        bool   `xml:"downloadRole,attr" json:"downloadRole"`
	UploadRole          bool   `xml:"uploadRole,attr" json:"uploadRole"`
	PlaylistRole        bool   `xml:"playlistRole,attr" json:"playlistRole"`
	CoverArtRole        bool   `xml:"coverArtRole,attr" json:"coverArtRole"`
	CommentRole         bool   `xml:"commentRole,attr" json:"commentRole"`
	PodcastRole         bool   `xml:"podcastRole,attr" json:"podcastRole"`
	StreamRole          bool   `xml:"streamRole,attr" json:"streamRole"`
	JukeboxRole         bool   `xml:"jukeboxRole,attr" json:"jukeboxRole"`
	ShareRole           bool   `xml:"shareRole,attr" json:"shareRole"`
	VideoConversionRole bool   `xml:"videoConversionRole,attr" json:"videoConversionRole"`
	Folder              []int  `xml:"folder" json:"folder"`
}

// newResponse is an "ok" envelope.
func (s *Server) newResponse() *response {
	return &response{Xmlns: "http://subsonic.org/restapi", Status: "ok", Version: APIVersion, Type: "antresol",
		ServerVersion: s.Version, OpenSubsonic: true}
}

// failure is a "failed" envelope with a Subsonic error code.
// msg is a Russian catalog key (or its rendering), answered in ctx's
// language.
func (s *Server) failure(ctx context.Context, code int, msg string) *response {
	r := s.newResponse()
	r.Status = "failed"
	r.Error = &apiError{Code: code, Message: i18n.Localize(ctx, msg)}
	return r
}

// write sends resp as JSON (f=json) or XML with HTTP 200.
func write(w http.ResponseWriter, asJSON bool, resp *response) {
	writeStatus(w, http.StatusOK, asJSON, resp)
}

// writeStatus is write with another HTTP status (503 for a busy stream:
// players check the status before the body).
func writeStatus(w http.ResponseWriter, status int, asJSON bool, resp *response) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	if asJSON {
		h.Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(map[string]*response{"subsonic-response": resp}); err != nil {
			log.Printf("subsonic: write json: %v", err)
		}
		return
	}
	h.Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(xml.Header))
	if err := xml.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("subsonic: write xml: %v", err)
	}
}
