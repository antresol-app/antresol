// Package subsonic is the Subsonic API (/rest/*) for music apps: Amperfy,
// play:Sub and others. It authenticates every request with the profile's
// app password (no cookie session), reads the library through
// internal/library and keeps playlists, stars and plays in internal/music,
// the same state the web player shows.
package subsonic

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/audioserve"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/music"
	"github.com/antresol-app/antresol/internal/screentime"
)

// Server serves /rest/{method} and /rest/{method}.view, GET and POST
// (application/x-www-form-urlencoded).
type Server struct {
	Users   *auth.Store
	Library *library.Repo
	Music   *music.Store
	// Audio streams tracks (shared with the web API: the same transcode
	// slots and «Музыка вне дома»).
	Audio   audioserve.Deps
	Network *auth.Network
	// Throttle counts failed logins; shared with the web login, so guessing
	// here also locks the web login from that address (nil: an own one).
	Throttle *auth.Throttle
	Version  string
	// ImageDir is DataDir/images (downloaded and extracted covers); "":
	// only covers next to the media are served.
	ImageDir string

	// UserTranscodes caps one profile's transcodes at once (0: 2), so one
	// app password cannot hold every shared slot.
	UserTranscodes int

	// ScreenTime limits a kid profile's music when its audio limit is set
	// («Экранное время»): stream and download answer error 50 with the
	// kid's message; a stream's start counts the track. nil: no limits.
	ScreenTime *screentime.Service

	once     sync.Once
	throttle *auth.Throttle

	streamsMu sync.Mutex
	streams   map[int64]int // running transcodes per profile
}

// acquireStream takes one of the profile's transcode slots.
func (s *Server) acquireStream(userID int64) bool {
	limit := s.UserTranscodes
	if limit <= 0 {
		limit = 2
	}
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.streams == nil {
		s.streams = map[int64]int{}
	}
	if s.streams[userID] >= limit {
		return false
	}
	s.streams[userID]++
	return true
}

func (s *Server) releaseStream(userID int64) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.streams[userID]--; s.streams[userID] <= 0 {
		delete(s.streams, userID)
	}
}

// call is one request: its parameters (query and form) and the profile.
type call struct {
	s      *Server
	w      http.ResponseWriter
	r      *http.Request
	ctx    context.Context
	p      url.Values
	u      auth.User
	json   bool
	albums map[int64]*library.Item // visible albums (nil: hidden)
	// per-call caches
	stars       map[int64]time.Time
	artistNames map[int64]string
	// err is the first error a rendering helper met; the response then
	// becomes an internal error instead of a quietly incomplete one.
	err error
}

// keep records the first rendering error of the call.
func (c *call) keep(err error) {
	if c.err == nil {
		c.err = err
	}
}

// handler answers a call; nil when it wrote the response itself (media).
type handler func(c *call) *response

var methods map[string]handler

func init() {
	methods = map[string]handler{
		"ping":                      (*call).ping,
		"getlicense":                (*call).getLicense,
		"getopensubsonicextensions": (*call).getOpenSubsonicExtensions,
		"getuser":                   (*call).getUser,
		"getmusicfolders":           (*call).getMusicFolders,
		"getindexes":                (*call).getIndexes,
		"getartists":                (*call).getArtists,
		"getartist":                 (*call).getArtist,
		"getalbum":                  (*call).getAlbum,
		"getsong":                   (*call).getSong,
		"getgenres":                 (*call).getGenres,
		"getalbumlist":              (*call).getAlbumList,
		"getalbumlist2":             (*call).getAlbumList2,
		"getrandomsongs":            (*call).getRandomSongs,
		"search2":                   (*call).search2,
		"search3":                   (*call).search3,
		"getstarred":                (*call).getStarred,
		"getstarred2":               (*call).getStarred2,
		"stream":                    (*call).stream,
		"download":                  (*call).download,
		"getcoverart":               (*call).getCoverArt,
		"getlyrics":                 (*call).getLyrics,
		"getlyricsbysongid":         (*call).getLyricsBySongID,
		"scrobble":                  (*call).scrobble,
		"star":                      (*call).star,
		"unstar":                    (*call).unstar,
		"getplaylists":              (*call).getPlaylists,
		"getplaylist":               (*call).getPlaylist,
		"createplaylist":            (*call).createPlaylist,
		"updateplaylist":            (*call).updatePlaylist,
		"deleteplaylist":            (*call).deletePlaylist,
		"getmusicdirectory":         (*call).getMusicDirectory,
		// empty answers (stubs.go)
		"getscanstatus":            (*call).getScanStatus,
		"getnowplaying":            (*call).getNowPlaying,
		"getartistinfo":            (*call).getArtistInfo,
		"getartistinfo2":           (*call).getArtistInfo2,
		"getalbuminfo":             (*call).getAlbumInfo,
		"getalbuminfo2":            (*call).getAlbumInfo,
		"gettopsongs":              (*call).getTopSongs,
		"getsimilarsongs":          (*call).getSimilarSongs,
		"getsimilarsongs2":         (*call).getSimilarSongs2,
		"getplayqueue":             (*call).getPlayQueue,
		"saveplayqueue":            (*call).savePlayQueue,
		"getbookmarks":             (*call).getBookmarks,
		"getpodcasts":              (*call).getPodcasts,
		"getinternetradiostations": (*call).getInternetRadioStations,
	}
}

// maxOffset bounds a page offset (a search loads offset+size rows).
const maxOffset = 100000

// maxForm bounds a POST form (a playlist of 5000 songIds fits).
const maxForm = 1 << 20

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// the request language (idempotent under cmd/vault's i18n.Middleware)
	r = r.WithContext(i18n.WithLanguage(r.Context(), i18n.Resolve(r)))
	name := strings.TrimPrefix(r.URL.Path, "/rest/")
	name = strings.ToLower(strings.TrimSuffix(name, ".view"))
	c := &call{s: s, w: w, r: r, ctx: r.Context(), albums: map[int64]*library.Item{}, artistNames: map[int64]string{}}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	}
	if err := r.ParseForm(); err != nil {
		c.json = r.URL.Query().Get("f") == "json"
		write(w, c.json, s.failure(c.ctx, codeGeneric, "Не удалось разобрать запрос"))
		return
	}
	c.p = r.Form
	c.json = c.p.Get("f") == "json"
	if !c.authenticate() {
		return
	}
	h, ok := methods[name]
	if !ok {
		write(w, c.json, s.failure(c.ctx, codeGeneric, "Метод не поддерживается"))
		return
	}
	resp := h(c)
	if resp == nil {
		return // media: written by the handler
	}
	if c.err != nil {
		resp = c.internal(name, c.err)
	}
	write(w, c.json, resp)
}

func (s *Server) th() *auth.Throttle {
	s.once.Do(func() {
		s.throttle = s.Throttle
		if s.throttle == nil {
			s.throttle = auth.NewThrottle()
		}
	})
	return s.throttle
}

func (c *call) ok() *response { return c.s.newResponse() }

func (c *call) fail(code int, msg string) *response { return c.s.failure(c.ctx, code, msg) }

// internal logs err (never parameters: they hold passwords) and answers code 0.
func (c *call) internal(what string, err error) *response {
	log.Printf("subsonic: %s: %v", what, err)
	return c.fail(codeGeneric, msgInternal)
}

// missing is code 10 for a required parameter.
func (c *call) missing(name string) *response {
	return c.fail(codeMissing, i18n.T(c.ctx, "Не указан параметр %s", name))
}

// id parses a numeric id parameter, with an optional prefix ("al-").
func parseID(v, prefix string) (int64, bool) {
	v = strings.TrimPrefix(v, prefix)
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n > 0
}

// intParam reads an integer parameter (def when absent or invalid).
func (c *call) intParam(name string, def int) int {
	if n, err := strconv.Atoi(c.p.Get(name)); err == nil {
		return n
	}
	return def
}

// pageParams reads a size (default def, at most 500) and offset pair.
func (c *call) pageParams(sizeName, offsetName string, def int) (size, offset int) {
	size = c.intParam(sizeName, def)
	if size < 0 {
		size = def
	}
	size = min(size, 500)
	return size, min(max(c.intParam(offsetName, 0), 0), maxOffset)
}

func (c *call) ping() *response { return c.ok() }

func (c *call) getLicense() *response {
	resp := c.ok()
	resp.License = &license{Valid: true}
	return resp
}

func (c *call) getOpenSubsonicExtensions() *response {
	resp := c.ok()
	resp.OpenSubsonicExtensions = []extension{
		{Name: "formPost", Versions: []int{1}},
		{Name: "songLyrics", Versions: []int{1}},
		{Name: "transcodeOffset", Versions: []int{1}},
	}
	return resp
}

// getUser: a profile sees itself; an admin may look up any profile.
func (c *call) getUser() *response {
	name := c.p.Get("username")
	if name == "" {
		return c.missing("username")
	}
	u := c.u
	if !strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(c.u.Name)) {
		if !c.u.IsAdmin {
			return c.fail(codeForbidden, "Недостаточно прав")
		}
		var err error
		if u, err = c.s.Users.ByName(c.ctx, name); errors.Is(err, auth.ErrNotFound) {
			return c.fail(codeNotFound, "Профиль не найден")
		} else if err != nil {
			return c.internal("getUser", err)
		}
	}
	resp := c.ok()
	resp.User = &user{Username: u.Name, ScrobblingEnabled: true, AdminRole: u.IsAdmin, DownloadRole: true,
		PlaylistRole: true, CoverArtRole: true, StreamRole: true, Folder: []int{musicFolderID}}
	return resp
}
