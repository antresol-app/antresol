package auth

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
)

// MsgRemotePasswordless: a profile without a password used from outside
// the home network.
const MsgRemotePasswordless = "Для входа не из дома у профиля должен быть пароль. Попросите администратора задать его в разделе «Настройки → Сервер → Профили»."

// Guard is the one session check of the web API: the vault_session cookie
// (browsers) or Authorization: Bearer (the native app). A native token of a
// passwordless profile only works from the home network; web sessions keep
// their behaviour. A nil Network uses the defaults (see Network).
//
// With Keys set, a request with neither carries a media key (?mk=) instead,
// accepted only for GET/HEAD of media files (mediaKeyRoute): AVPlayer and
// AirPlay fetch those without the app's headers.
type Guard struct {
	Sessions *Sessions
	Network  *Network
	Keys     *MediaKeys
	// APIKeys: the profiles' personal API keys (Bearer antresol_…); nil:
	// none accepted.
	APIKeys *APIKeys
}

type sessionKey struct{}
type tokenKey struct{}
type mediaKeyKey struct{}

// ViaMediaKey returns the media key a request was let in with.
func ViaMediaKey(ctx context.Context) (string, bool) {
	k, ok := ctx.Value(mediaKeyKey{}).(string)
	return k, ok
}

// mediaKeyRoute: the paths a media key opens (file streams, HLS playlists
// and segments, a live channel's playlist and segments, music tracks,
// audiobook parts, an addon input's direct stream, a cached trailer, an
// addon's media prefix), for GET and HEAD only, and only on a plain path:
// the router matches the raw path, so a path with a raw form (an encoded
// slash, or any escape Go would not have written) could look like one of
// these prefixes here and reach another route there. No media URL the
// server hands out needs one (its ids are numbers, hex and [A-Za-z0-9_-]).
func mediaKeyRoute(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.URL.RawPath != "" {
		return false
	}
	p := r.URL.Path
	return strings.HasPrefix(p, "/api/stream/") || strings.HasPrefix(p, "/api/hls/") ||
		strings.HasPrefix(p, "/api/audio/") ||
		strings.HasPrefix(p, "/api/channels/live/") ||
		(strings.HasPrefix(p, "/api/trailers/") && strings.HasSuffix(p, ".mp4")) ||
		(strings.HasPrefix(p, "/api/tracks/") && strings.HasSuffix(p, "/stream")) ||
		inputDirect(p) || addonMedia(r)
}

// addonMedia: /api/addons/{id}/x/media/…, the prefix an addon serves its
// streams under (the proxy checks the rest of the path). mediaKeyRoute
// has refused a path with a raw form already: an encoded slash could hide
// "x/media/" in the id and reach another route.
func addonMedia(r *http.Request) bool {
	p := r.URL.Path
	id, rest, ok := strings.Cut(strings.TrimPrefix(p, "/api/addons/"), "/")
	return ok && strings.HasPrefix(p, "/api/addons/") && id != "" && strings.HasPrefix(rest, "x/media/")
}

// inputDirect: /api/play/input/{id}/direct, an addon input's file as it is.
func inputDirect(p string) bool {
	id, ok := strings.CutSuffix(strings.TrimPrefix(p, "/api/play/input/"), "/direct")
	return ok && strings.HasPrefix(p, "/api/play/input/") && id != "" && !strings.Contains(id, "/")
}

// SessionFrom returns the session put into ctx by Guard.Require.
func SessionFrom(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(sessionKey{}).(Session)
	return s, ok
}

// TokenFrom returns the caller's session token ("" outside Guard.Require).
func TokenFrom(ctx context.Context) string {
	t, _ := ctx.Value(tokenKey{}).(string)
	return t
}

// RequestToken is the session token of r: the cookie, else a Bearer token.
func RequestToken(r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return c.Value
	}
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// Require rejects requests without a valid session (401) and stores the
// user, the session and its token in the context.
func (g *Guard) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := RequestToken(r)
		if IsAPIKey(tok) {
			g.serveKey(w, r, tok, next)
			return
		}
		var mk string
		if tok == "" && g.Keys != nil && mediaKeyRoute(r) {
			mk = r.URL.Query().Get("mk")
		}
		if tok == "" && mk == "" {
			jsonErr(w, r, http.StatusUnauthorized, "Нужно войти")
			return
		}
		var u User
		var s Session
		var err error
		if mk != "" {
			u, s, err = g.Keys.Verify(r.Context(), mk)
		} else {
			u, s, err = g.Sessions.LookupSession(r.Context(), tok)
		}
		if errors.Is(err, ErrNotFound) {
			jsonErr(w, r, http.StatusUnauthorized, "Нужно войти")
			return
		}
		if err != nil {
			log.Printf("auth: session lookup: %v", err)
			jsonErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
			return
		}
		// a media key of a passwordless profile works at home only, whatever
		// session asked for it (a web key handed to an AirPlay receiver too)
		if (s.Kind == KindNative || mk != "") && !u.HasPassword && !g.Network.IsTrusted(r) {
			w.Header().Set("X-Vault-Error", "remote-passwordless")
			jsonErr(w, r, http.StatusForbidden, MsgRemotePasswordless)
			return
		}
		ctx := WithUser(r.Context(), u)
		ctx = WithSession(ctx, s)
		if mk != "" {
			ctx = context.WithValue(ctx, mediaKeyKey{}, mk)
		} else {
			ctx = context.WithValue(ctx, tokenKey{}, tok)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
