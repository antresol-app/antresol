// Package api wires the HTTP router.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/disk"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/lyrics"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/mediaimport"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/antresol-app/antresol/internal/music"
	"github.com/antresol-app/antresol/internal/notify"
	"github.com/antresol-app/antresol/internal/playlistimport"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/antresol-app/antresol/internal/screentime"
	"github.com/antresol-app/antresol/internal/segments"
	"github.com/antresol-app/antresol/internal/subtitles"
	"github.com/antresol-app/antresol/internal/version"
	"github.com/antresol-app/antresol/internal/webui"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Deps holds handler dependencies; extended by later tasks.
type Deps struct {
	Users    *auth.Store
	Sessions *auth.Sessions
	// Network says which clients are at home (passwordless profiles, first
	// setup); nil: loopback and private addresses, no trusted proxy.
	Network *auth.Network
	// Throttle counts failed password checks per client and profile; one
	// is shared by the web login, Subsonic and OPDS (nil: the web login
	// gets its own).
	Throttle *auth.Throttle

	// Library, playback and progress (all optional; mounted when Library and Progress are set).
	Library  *library.Repo
	Progress *progress.Store
	Jobs     *library.Jobs // scan/enrich job; nil disables POST /api/library/scan
	HLS      HLS           // usually *media.HLSManager
	DataDir  string        // images/ (TMDB cache) and subs/ (WebVTT cache) live here
	FFmpeg   string
	FFprobe  string
	// Probe overrides media.Probe (tests).
	Probe func(ctx context.Context, path string) (media.ProbeResult, error)
	// Keyframe overrides media.KeyframeBefore (tests).
	Keyframe func(ctx context.Context, input string, t float64) (float64, error)
	// ClientIsRemote overrides Network for the playback quality cap
	// («Качество вне дома» applies outside the home network; tests).
	ClientIsRemote func(*http.Request) bool
	// AudioTranscoder runs the music/audiobook transcodes of
	// /api/tracks/{id}/stream and /api/audio/{fileId} (shared with
	// Subsonic); nil: one is made from FFmpeg with 4 slots.
	AudioTranscoder *media.AudioTranscoder
	// Music holds playlists, stars and plays; nil disables /api/music/*
	// (and the listening shelves of /api/home).
	Music *music.Store
	// ComicsCache is where RAR/7z volumes are unpacked for the page API
	// (the scanner's Scanner.ComicsCache); "": DataDir/comics-cache.
	ComicsCache string

	// Files is the file browser; nil disables /api/files/*.
	Files *files.Service

	// Events is the event bus behind GET /api/events; nil disables it.
	Events *events.Bus

	// DiskUsage is the media disk's fill level (GET /api/disk, and the disk
	// figures of /api/stats without the file browser); nil disables
	// /api/disk.
	DiskUsage func() (disk.Usage, error)

	// DB enables /api/stats* and /api/settings (admin).
	DB *sql.DB
	// SettingsDefaults are the settings before anything was saved;
	// ApplySettings puts saved settings into effect.
	SettingsDefaults AppSettings
	ApplySettings    func(AppSettings)

	// Subtitles searches and downloads OpenSubtitles subtitles (admin,
	// /api/subtitles/*); nil without a key (503).
	Subtitles *subtitles.Client

	// Genres names TMDB genre ids in the request's language (nil: the
	// stored names, mapped by i18n.Genre).
	Genres GenreNamer
	// Translations shows movies and series (titles, overviews, cast,
	// episodes) in the request's language when the library stores them in
	// another (nil: the stored text).
	Translations Translator
	// Related are the «Похожие» and «Коллекция» rows of movie and series
	// pages (/api/items/{id}/related, /api/items/{id}/collection); nil
	// without a TMDB key: no rows.
	Related RelatedSource
	// Ratings are the IMDb and Rotten Tomatoes ratings of
	// /api/ratings/{imdbId} (nil: none, the answer is {}).
	Ratings Ratings

	// ServerID tells apps which server they talk to (GET /api/health);
	// "": loaded from DB (LoadServerID), none without DB.
	ServerID string

	// MediaKeys issues the native app's media keys (POST /api/auth/media-key)
	// and lets the session guard accept them on media routes (?mk=); nil
	// disables both.
	MediaKeys *auth.MediaKeys

	// DeviceCopies makes the native app's offline copies of videos
	// (/api/device-copies*) and keeps the comic page zips
	// (/api/comics/{fileId}/pages.zip); nil: both answer 503.
	DeviceCopies *media.DeviceCopies

	// Lyrics looks song words up online (LRCLIB) when the file has none or
	// only plain ones; nil: only the words in the files (tracks then say
	// hasLyrics only when they have some).
	Lyrics *lyrics.Client

	// Segments are the recap, intro and credits of the library's videos
	// (segments: of POST /api/play); nil: none.
	Segments Segmenter

	// Trickplay are the scrub previews of the library's videos
	// (/api/files/{fileId}/trickplay); nil: none (404).
	Trickplay Trickplay

	// CastPhotos keeps the cast's TMDB photos in the image cache and
	// fetches a missing one on its first view; nil: cast photos 404.
	CastPhotos *tmdb.Photos
	// Artwork keeps TMDB posters and logos the same way (Size
	// tmdb.ArtSize): a title's art in the viewer's language when the
	// library stores another (/api/images/art/{name}); nil: the stored art.
	Artwork *tmdb.Photos

	// Notify is «Уведомления на почту» (/api/me/notify*, the admin's
	// /api/users/{id}/notify* and the letters' /api/notify/link/*); nil
	// disables them.
	Notify *notify.Service

	// ScreenTime counts and limits kid profiles' playback («Экранное
	// время»): /api/me/screen-time, the admin's /api/users/{id}/screen-time
	// and the checks of every playback route; nil: nothing is limited.
	ScreenTime *screentime.Service

	// Requests is where kids' requests get their titles (TMDB, covers, the
	// library): «Попросить» and the admin's «Просьбы» (/api/requests*); nil
	// disables them. Needs DB.
	Requests RequestCatalog
	// RequestsOpen says whether kids' requests are offered on this server
	// (me.canRequest for a kid, me.requests for an admin); nil: closed.
	RequestsOpen func(context.Context) bool

	// MediaImport is «Управление → Переезд» (admin, /api/migrate*): the
	// import from Jellyfin, Kavita and Audiobookshelf; the sources build adds
	// its own sources to the same service. nil disables it. Needs Users.
	MediaImport *mediaimport.Service

	// PlaylistImport is «Импорт медиатеки» (admin, /api/import/*): playlists
	// and favourites from files, matched and rebuilt in the library, in both
	// editions; another build adds more to the same
	// service (Deps.ImportOAuth). nil disables it. Needs Users.
	PlaylistImport *playlistimport.Service

	// LibraryOnly is me.libraryOnly: the server serves its library only
	// (always in the core build; VAULT_LIBRARY_ONLY in the sources build,
	// whose features that bring titles from outside are then nil).
	LibraryOnly bool

	// Channels are «ТВ-каналы» (/api/channels…); nil disables them. Needs
	// DB and Library.
	Channels *channels.Service
	// IPTV opens the channels to IPTV players (VAULT_IPTV=on): an M3U
	// playlist and an XMLTV guide on per-profile token URLs
	// (/iptv/{token}/…, /api/channels/iptv). Off by default: channels are
	// watched in Antresol's own apps.
	IPTV bool

	// fullDeps are the sources build's own dependencies (deps_full.go); an
	// empty struct in the core build. Set them by name: deps.Acquire.
	fullDeps
}

// Segmenter is the part of *segments.Worker the API uses.
type Segmenter interface {
	// ForPlay is what is stored for a file, never waiting for a lookup.
	ForPlay(ctx context.Context, fileID, durationMs int64, movie bool) []segments.Play
}

// LoadServerID returns the server's id (16 random bytes, hex), made once
// and kept in settings as server_id.
func LoadServerID(ctx context.Context, db *sql.DB) (string, error) {
	return auth.LoadOrCreateSetting(ctx, db, "server_id", 16)
}

func NewRouter(deps Deps) http.Handler { return newRouter(deps, nil) }

// newRouter builds the router; onLib (optional) gets the library handlers
// when they are mounted (NewRouterWithPlayer).
func newRouter(deps Deps, onLib func(*lib)) http.Handler {
	r := chi.NewRouter()
	// the request's interface language (ru | en) for everything below
	r.Use(i18n.Middleware)
	r.Use(csrf)
	r.Use(clientCaps)
	// JSON and the app's own text files, gzipped for a client that takes
	// it (a 160 KB downloads list is ~15 KB); media, images and the event
	// stream are left alone
	r.Use(compressText)
	if deps.ServerID == "" && deps.DB != nil {
		id, err := LoadServerID(context.Background(), deps.DB)
		if err != nil {
			log.Printf("api: server id: %v", err)
		}
		deps.ServerID = id
	}
	serverID := deps.ServerID
	r.Get("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version.Version, "serverId": serverID})
	})
	// the source offer and the licences (AGPL §13): public
	mountAbout(r)
	// kids' requests: with the database and a catalog
	var requests *kidRequests
	requestsOpen := deps.RequestsOpen
	if deps.DB != nil && deps.Requests != nil {
		requests = &kidRequests{db: deps.DB, cat: deps.Requests, bus: deps.Events, notify: deps.Notify, open: deps.RequestsOpen}
	} else {
		requestsOpen = nil
	}
	// the one session check of every authenticated route
	guard := &auth.Guard{Sessions: deps.Sessions, Network: deps.Network, Keys: deps.MediaKeys}
	if deps.Users != nil && deps.Sessions != nil {
		mountAuth(r, deps.Users, guard, deps.Throttle, newMeCaps(deps), deps.LibraryOnly || libraryEdition, requestsOpen)
	}
	var h *lib
	if deps.Sessions != nil && deps.Library != nil && deps.Progress != nil {
		if deps.AudioTranscoder == nil {
			ff := deps.FFmpeg
			if ff == "" {
				ff = "ffmpeg"
			}
			deps.AudioTranscoder = &media.AudioTranscoder{FFmpeg: ff, Limit: defaultAudioLimit}
		}
		if deps.ComicsCache == "" && deps.DataDir != "" {
			deps.ComicsCache = filepath.Join(deps.DataDir, "comics-cache")
		}
		h = &lib{Deps: deps, guard: guard, comics: newComicArchives(comicArchivesOpen, deps.ComicsCache), requests: requests}
		if onLib != nil {
			onLib(h)
		}
		r.Group(func(r chi.Router) {
			r.Use(guard.Require)
			mountLibrary(r, h)
			mountRelated(r, h)
			mountPlay(r, h)
			mountTrackPrefs(r, h)
			mountUILanguage(r, h)
			mountAudio(r, h)
			mountBooks(r, h)
			mountAnnotations(r, h)
			mountComics(r, h)
			mountDeviceCopies(r, h)
			if deps.Music != nil {
				mountMusic(r, h)
			}
			mountImages(r, h)
			mountSubtitles(r, h)
			if deps.Notify != nil && deps.Users != nil {
				mountNotify(r, h)
			}
			mountTrickplay(r, h)
			mountScreenTime(r, h)
			if requests != nil {
				mountKidRequests(r, requests)
			}
			if deps.Channels != nil && deps.DB != nil {
				ca := newChannelsAPI(deps, h)
				mountChannels(r, ca)
				if deps.IPTV {
					mountIPTVLinks(r, ca)
				}
			}
		})
		if deps.Channels != nil && deps.DB != nil && deps.IPTV {
			mountIPTV(r, newChannelsAPI(deps, nil)) // a token in the path, no session
		}
	}
	if deps.Notify != nil {
		mountNotifyLinks(r, deps.Notify) // the letters' links: a token, no session
	}
	if deps.Sessions != nil && deps.Events != nil {
		r.Group(func(r chi.Router) {
			r.Use(guard.Require)
			mountEvents(r, deps.Events)
		})
	}
	if deps.Sessions != nil && deps.DiskUsage != nil {
		r.Group(func(r chi.Router) {
			r.Use(guard.Require)
			mountDisk(r, deps.DiskUsage)
		})
	}
	if deps.Sessions != nil && deps.DB != nil {
		r.Group(func(r chi.Router) {
			r.Use(guard.Require)
			mountStats(r, newStats(deps, time.Now))
			mountSettings(r, newSettingsAPI(deps))
		})
	}
	if deps.Sessions != nil && deps.Users != nil && deps.MediaImport != nil {
		r.Group(func(r chi.Router) {
			r.Use(guard.Require)
			mountMediaImport(r, deps)
		})
	}
	if deps.Sessions != nil && deps.PlaylistImport != nil {
		r.Group(func(r chi.Router) {
			r.Use(guard.Require)
			mountImport(r, deps)
		})
	}
	// the sources build's own routes (none in the core build)
	mountFull(r, deps, guard, h)
	if deps.Sessions != nil && deps.Files != nil {
		r.Group(func(r chi.Router) {
			r.Use(guard.Require)
			mountFiles(r, deps.Files)
			mountItemDelete(r, deps.Files, deps.Events)
		})
	}
	r.Handle("/api/*", http.NotFoundHandler())
	r.NotFound(spa(webui.FS).ServeHTTP)
	return r
}

// compressTypes are the response types the router gzips: the API's JSON
// and the web app's text files. Never media, images, archives or
// text/event-stream (its events must reach the tab at once).
var compressTypes = []string{"application/json", "text/html", "text/css", "text/javascript", "application/javascript",
	"application/manifest+json", "image/svg+xml", "text/plain", "application/xml", "audio/x-mpegurl"}

// compressText gzips the GET responses of compressTypes. Media routes and
// range requests are passed straight through, unwrapped: a wrapped writer
// would cost them net/http's sendfile.
func compressText(next http.Handler) http.Handler {
	z := middleware.Compress(5, compressTypes...)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Range") != "" || mediaRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		z.ServeHTTP(&varyWriter{ResponseWriter: w}, r)
	})
}

// varyWriter adds Vary: Accept-Encoding to every compressible answer, the
// plain ones too (the compressor adds it only to what it compresses): a
// shared cache must not hand one client's encoding to another.
type varyWriter struct {
	http.ResponseWriter
	done bool
}

func (v *varyWriter) vary() {
	if v.done {
		return
	}
	v.done = true
	h := v.Header()
	if !compressible(h.Get("Content-Type")) {
		return
	}
	for _, x := range h.Values("Vary") {
		for _, f := range strings.Split(x, ",") {
			if strings.EqualFold(strings.TrimSpace(f), "Accept-Encoding") {
				return
			}
		}
	}
	h.Add("Vary", "Accept-Encoding")
}

func (v *varyWriter) WriteHeader(code int) { v.vary(); v.ResponseWriter.WriteHeader(code) }

func (v *varyWriter) Write(b []byte) (int, error) {
	if !v.done && v.Header().Get("Content-Type") == "" {
		v.Header().Set("Content-Type", http.DetectContentType(b)) // as net/http would
	}
	v.vary()
	return v.ResponseWriter.Write(b)
}

func (v *varyWriter) Flush() {
	if f, ok := v.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (v *varyWriter) Unwrap() http.ResponseWriter { return v.ResponseWriter }

// mediaRoute: the API's binary and streamed responses (files, streams,
// images, archives, the event stream).
func mediaRoute(p string) bool {
	for _, pre := range []string{"/api/stream/", "/api/hls/", "/api/audio/", "/api/download/", "/api/torrent-stream/",
		"/api/images/", "/api/comics/", "/api/device-copies/", "/api/events", "/api/online/s/", "/api/files/content",
		"/api/files/thumb", "/api/subs/", "/api/channels/live/"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	if strings.HasPrefix(p, "/iptv/") {
		// the IPTV players' streams and pictures (their playlist and guide are text)
		return strings.Contains(p, "/live/") || strings.Contains(p, "/logo/") || strings.Contains(p, "/art/")
	}
	return strings.HasPrefix(p, "/api/") && (strings.HasSuffix(p, "/stream") || strings.HasSuffix(p, "/zip") ||
		strings.Contains(p, "/trickplay"))
}

// compressible reports whether the router gzips a response of type ct.
func compressible(ct string) bool {
	ct, _, _ = strings.Cut(ct, ";")
	return slices.Contains(compressTypes, strings.TrimSpace(ct))
}

// appCSP is the Content-Security-Policy of the web app. Only the app's own
// scripts run: book sections are same-origin blob: iframes that inherit this
// policy, so scripts inside an EPUB (inline, external or on* handlers) are
// blocked. blob: covers hls.js (MediaSource, its worker), pdf.js's worker
// and foliate's section documents, styles and fonts; images may come from
// metadata hosts (TMDB, Cover Art Archive posters in search results).
const appCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' blob:; " +
	"img-src * data: blob:; font-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self' blob:; " +
	"worker-src 'self' blob:; frame-src 'self' blob:; manifest-src 'self'; object-src 'none'; " +
	"base-uri 'self'; form-action 'self'; frame-ancestors 'self'"

// spa serves static files from fsys, falling back to index.html for unknown
// paths (client routes); missing files under assets/ are 404. The service
// worker (/sw.js) is no-cache.
func spa(fsys fs.FS) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Security-Policy", appCSP)
		p := strings.TrimPrefix(req.URL.Path, "/")
		if p != "" {
			if st, err := fs.Stat(fsys, p); err == nil && !st.IsDir() {
				if p == "sw.js" {
					// the browser checks for a new service worker on every load
					w.Header().Set("Cache-Control", "no-cache")
				}
				if strings.HasPrefix(p, "assets/") {
					// a build's hashed file never changes under its name
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, req)
				return
			}
		}
		// A missing build asset (a tab open across an upgrade asks for the old
		// hashed chunk) is a 404, not index.html served as JavaScript.
		if strings.HasPrefix(p, "assets/") {
			http.NotFound(w, req)
			return
		}
		b, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
}
