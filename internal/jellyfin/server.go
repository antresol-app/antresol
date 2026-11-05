// Package jellyfin serves a Jellyfin-compatible API, so that Swiftfin and
// Infuse (Apple TV, iPhone) can use Vault. The same rules as the web app
// and Subsonic apply through the shared helpers: login with the profile
// name and its app password (auth.AppLogin, the shared throttle),
// passwordless profiles from the home network only, absolute URLs from
// auth.(*Network).Origin.
package jellyfin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/antresol-app/antresol/internal/screentime"
)

// Server is the Jellyfin API. Version is reported as the server version
// (VAULT_JELLYFIN_VERSION).
type Server struct {
	Users    *auth.Store
	Tokens   *auth.AppTokens
	Network  *auth.Network
	Throttle *auth.Throttle
	DB       *sql.DB
	Version  string
	Log      *slog.Logger

	// The library (Task 6): items, progress, artwork. ImageDir is
	// DataDir/images (also the resized-copy cache); DataDir finds the
	// subtitles Auto saved (DataDir/subs/ext).
	Library  *library.Repo
	Progress *progress.Store
	ImageDir string
	DataDir  string
	// Similar fills /Items/{id}/Similar from TMDB (the library's titles
	// only); nil without a TMDB key: the list is empty.
	Similar SimilarSource
	// Player plays movies and episodes (the web player's machinery,
	// api.Player); nil: browsing only, the playback routes are 404.
	Player Player
	// ScreenTime limits kid profiles' playback («Экранное время»): a
	// refused PlaybackInfo answers ErrorCode NotAllowed, master.m3u8 403;
	// the HLS segments and streams stop through Player. Playstate reports
	// count the time. nil: nothing is limited.
	ScreenTime *screentime.Service

	// Activity counts the media reads (direct streams, downloads, HLS
	// segments) once the request is authorised; nil: not counted.
	Activity interface {
		Begin()
		End()
	}

	plays plays

	rateMu sync.Mutex
	rateAt map[string]time.Time // the last full bitrate test of a token

	once sync.Once
	mux  http.Handler

	idMu sync.Mutex
	id   string

	unkMu   sync.Mutex
	unknown map[string]struct{}
}

// maxUnknownPaths bounds the set of unknown routes already logged.
const maxUnknownPaths = 1000

// jellyfinRoots are the first path segments of the Jellyfin API (lower
// case). None of them is a route of the web app.
var jellyfinRoots = map[string]bool{}

func init() {
	for _, s := range strings.Fields(`system users items shows videos sessions userviews useritems userplayeditems
		userfavoriteitems userimage playback playingitems branding quickconnect displaypreferences genres persons studios
		mediasegments socket livestreams library`) {
		jellyfinRoots[s] = true
	}
}

// IsJellyfinPath tells whether p belongs to the Jellyfin API (case
// insensitive): the main listener sends such requests here instead of the
// web app.
func IsJellyfinPath(p string) bool {
	p = strings.ToLower(p)
	if strings.HasPrefix(p, "/search/hints") {
		return true
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	return jellyfinRoots[first]
}

func (s *Server) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Server) init() {
	if s.Network == nil {
		s.Network = &auth.Network{}
	}
	if s.Throttle == nil {
		s.Throttle = auth.NewThrottle()
	}
	r := chi.NewRouter()
	r.Use(middleware.StripSlashes, middleware.GetHead)
	// without a login
	r.Get("/system/info/public", s.publicInfo)
	r.Get("/system/ping", s.ping)
	r.Post("/system/ping", s.ping)
	r.Get("/branding/configuration", s.branding)
	r.Get("/branding/css", s.brandingCSS)
	r.Get("/quickconnect/enabled", s.quickConnect)
	r.Get("/users/public", s.publicUsers)
	r.Post("/users/authenticatebyname", s.authenticateByName)
	// images: the token is optional (Swiftfin sends none), see itemImage
	r.Get("/items/{id}/images/{type}", s.itemImage)
	r.Get("/items/{id}/images/{type}/{idx}", s.itemImage)
	r.Get("/users/{userId}/images/{type}", s.noUserImage)
	r.Get("/userimage", s.noUserImage)
	// what players fetch without headers: the token or a play session
	s.mountPlayFiles(r)
	// with an access token
	r.Group(func(r chi.Router) {
		r.Use(s.requireToken)
		r.Get("/system/info", s.systemInfo)
		r.Get("/users/me", s.me)
		r.Get("/users/{userId}", s.me) // requireToken: it is the token's own profile
		r.Post("/sessions/logout", s.logout)
		r.Post("/sessions/capabilities", s.noContent)
		r.Post("/sessions/capabilities/full", s.noContent)
		r.Get("/sessions", s.sessions)
		r.Get("/displaypreferences/{id}", s.displayPreferences)
		r.Post("/displaypreferences/{id}", s.noContent)
		r.Get("/userviews", s.views)
		r.Get("/users/{userId}/views", s.views)
		r.Get("/library/mediafolders", s.views)
		r.Get("/search/hints", s.searchHints)
		s.mountLibrary(r)
		s.mountPlayback(r)
		s.mountPlaystate(r)
	})
	r.NotFound(s.notFound)
	r.MethodNotAllowed(s.notFound)
	s.mux = r
}

type origPathKey struct{}

// ServeHTTP routes case-insensitively: the path is lowercased before
// matching (Jellyfin routes ignore case; clients mix /Users and /users).
// The original path stays in the context for echoing ids.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.once.Do(s.init)
	if _, err := s.serverID(r.Context()); err != nil {
		if r.Context().Err() == nil {
			s.log().Error("jellyfin: server id", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}
	// the request language: Infuse and Swiftfin send the system's
	// Accept-Language; none → ru
	ctx := i18n.WithLanguage(r.Context(), i18n.Resolve(r))
	r2 := r.WithContext(context.WithValue(ctx, origPathKey{}, r.URL.Path))
	u := *r.URL
	u.Path, u.RawPath = strings.ToLower(u.Path), ""
	r2.URL = &u
	s.mux.ServeHTTP(w, r2)
}

// notFound: 404 with an empty body; each unknown path is logged once (the
// path only: the query may carry ApiKey) to show what clients still miss.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if len(p) > 200 {
		p = p[:200]
	}
	s.unkMu.Lock()
	if s.unknown == nil {
		s.unknown = map[string]struct{}{}
	}
	_, seen := s.unknown[p]
	if !seen {
		if len(s.unknown) >= maxUnknownPaths {
			seen = true // full: stay quiet rather than flood the log
		} else {
			s.unknown[p] = struct{}{}
		}
	}
	s.unkMu.Unlock()
	if !seen {
		s.log().Info("jellyfin: unknown route", "method", r.Method, "path", p)
	}
	w.WriteHeader(http.StatusNotFound)
}

// serverID is the server's Jellyfin id, made once and kept in settings:
// Swiftfin refuses a server whose id changes.
func (s *Server) serverID(ctx context.Context) (string, error) {
	s.idMu.Lock()
	defer s.idMu.Unlock()
	if s.id != "" {
		return s.id, nil
	}
	id, err := auth.LoadOrCreateSetting(ctx, s.DB, "jellyfin_server_id", 16)
	if err != nil {
		return "", err
	}
	s.id = id
	return id, nil
}

// sid is the cached server id (ServeHTTP loads it before any handler).
func (s *Server) sid() string {
	s.idMu.Lock()
	defer s.idMu.Unlock()
	return s.id
}

type sessionKey struct{}

// session is the access token of the request and the device it was
// issued to.
type session struct {
	token string
	dev   auth.Device
}

// requireToken checks the access token (401 with an empty body: both
// clients then sign in again; also for a passwordless profile outside
// home) and puts its profile in the context. A
// userId in the path or the query must be the token's own profile (403).
func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := ParseAuth(r).Token
		if tok == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		u, dev, err := s.Tokens.Lookup(r.Context(), tok)
		if errors.Is(err, auth.ErrNotFound) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err != nil {
			s.internal(w, r, "token lookup", err)
			return
		}
		// A profile without a login password is a home-only profile:
		// Subsonic and OPDS check that on every request (AppLogin), so a
		// token got at home stops working outside it.
		if !u.HasPassword && !s.Network.IsTrusted(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		for _, v := range []string{chi.URLParam(r, "userId"), qv(r, "userId")} {
			if v == "" {
				continue
			}
			if k, id, ok := ParseID(v); !ok || k != kindUser || id != u.ID {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		ctx := auth.WithUser(r.Context(), u)
		ctx = context.WithValue(ctx, sessionKey{}, session{token: tok, dev: dev})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type scopedKey struct{}

// requirePlay authorises what players fetch without the app's headers
// (playlists, segments, subtitles): the access token when the request has
// one (requireToken), else the play session in the URL (PlaySessionId, or
// the hls1/{play}/ segment of the path). A play session stands for the
// profile and the token PlaybackInfo was called with: it stops working
// when that token is revoked, outside home for a passwordless profile (as
// the token), and when the play session expires. Handlers bind it to its
// own item and file (scopedPlay).
func (s *Server) requirePlay(next http.Handler) http.Handler {
	withToken := s.requireToken(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ParseAuth(r).Token != "" {
			withToken.ServeHTTP(w, r)
			return
		}
		psid := chi.URLParam(r, "play")
		if psid == "" {
			psid = qv(r, "PlaySessionId")
		}
		p, ok := s.plays.lookup(psid)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		u, dev, err := s.Tokens.Lookup(r.Context(), p.token)
		if errors.Is(err, auth.ErrNotFound) || err == nil && (u.ID != p.uid || !u.HasPassword && !s.Network.IsTrusted(r)) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err != nil {
			s.internal(w, r, "token lookup", err)
			return
		}
		ctx := auth.WithUser(r.Context(), u)
		ctx = context.WithValue(ctx, sessionKey{}, session{token: p.token, dev: dev})
		ctx = context.WithValue(ctx, scopedKey{}, psid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// scopedPlay is the play session that authorised the request ("": the
// access token did).
func scopedPlay(r *http.Request) string {
	v, _ := r.Context().Value(scopedKey{}).(string)
	return v
}

// mediaRead counts a read of the media disk (Activity) until the returned
// func runs.
func (s *Server) mediaRead() func() {
	if s.Activity == nil {
		return func() {}
	}
	s.Activity.Begin()
	return s.Activity.End
}

func userOf(r *http.Request) auth.User {
	u, _ := auth.UserFrom(r.Context())
	return u
}

func sessionOf(r *http.Request) session {
	s, _ := r.Context().Value(sessionKey{}).(session)
	return s
}

// writeJSON sends v without a trailing newline.
func writeJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(b)
}

// internal logs err (never a secret) and answers 500 with an empty body,
// unless the client is gone.
func (s *Server) internal(w http.ResponseWriter, r *http.Request, what string, err error) {
	if r.Context().Err() != nil {
		return
	}
	s.log().Error("jellyfin: "+what, "err", err)
	w.WriteHeader(http.StatusInternalServerError)
}

func (s *Server) noContent(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
