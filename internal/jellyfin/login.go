package jellyfin

import (
	"crypto/subtle"
	"encoding/json"
	"math"
	"net/http"
	"strconv"

	"github.com/antresol-app/antresol/internal/auth"
)

// authenticateByName is the Jellyfin login: the profile name and its app
// password (never the login password: Swiftfin and Infuse store what is
// typed, as Subsonic and OPDS apps do), through auth.AppLogin with the
// shared throttle. Success issues an access token for the device.
// Failures are 401 with an empty body; a locked key is 429 + Retry-After.
func (s *Server) authenticateByName(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string
		Pw       string
		Password string // older Emby clients
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	pw := in.Pw
	if pw == "" {
		pw = in.Password
	}
	if in.Username == "" || pw == "" { // as OPDS: no credentials is no strike
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	out, err := auth.AppLogin(r.Context(), s.Users, s.Network, s.Throttle, r, "jellyfin", in.Username,
		func(app string) bool { return subtle.ConstantTimeCompare([]byte(pw), []byte(app)) == 1 })
	if err != nil {
		s.internal(w, r, "login", err)
		return
	}
	switch out.Result {
	case auth.LoginOK:
	case auth.LoginThrottled:
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(out.Wait.Seconds()))))
		w.WriteHeader(http.StatusTooManyRequests)
		return
	default:
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ci := ParseAuth(r)
	dev := auth.Device{ID: clip(ci.DeviceID), Name: clip(ci.Device), Client: clip(ci.Client), Version: clip(ci.Version)}
	tok, err := s.Tokens.Issue(r.Context(), out.User.ID, "jellyfin", dev)
	if err != nil {
		s.internal(w, r, "issue token", err)
		return
	}
	s.log().Info("jellyfin: login", "profile", out.User.ID, "client", dev.Client, "device", dev.Name)
	writeJSON(w, AuthenticationResult{
		User:        s.userDto(out.User),
		SessionInfo: s.sessionDto(out.User, session{token: tok, dev: dev}),
		AccessToken: tok,
		ServerId:    s.sid(),
	})
}

// clip bounds what a client says about itself before it is stored.
func clip(v string) string {
	if r := []rune(v); len(r) > 128 {
		return string(r[:128])
	}
	return v
}
