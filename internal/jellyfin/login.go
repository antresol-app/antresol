package jellyfin

import (
	"crypto/subtle"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
)

// loginBodyWait is how long the login's body may take (a variable for the tests).
var loginBodyWait = 30 * time.Second

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
	// open to anyone: a client that drips the body must not hold the connection
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(loginBodyWait))
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
	// History: a password of one of the person's managed accounts binds the
	// token to that account's history (a TV's Infuse keeps the TV's history)
	dev := auth.Device{ID: clip(ci.DeviceID), Name: clip(ci.Device), Client: clip(ci.Client), Version: clip(ci.Version), History: out.History}
	tok, err := s.Tokens.Issue(r.Context(), out.User.ID, "jellyfin", dev)
	if err != nil {
		s.internal(w, r, "issue token", err)
		return
	}
	s.log().Info("jellyfin: login", "profile", out.User.ID, "history", out.History.ID, "client", dev.Client, "device", dev.Name)
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
