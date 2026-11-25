package addonkit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// UserHeader carries the acting profile on proxied requests and on hooks
// run for a profile (JSON, non-ASCII escaped).
const UserHeader = "X-Antresol-User"

// UserSigHeader carries the server's signature of UserHeader's value:
// "<unix seconds>.<hex HMAC-SHA256 of "<seconds>.<value>">", keyed with the
// process's own key (UserKeyEnv). Another process that can open the addon's
// socket does not have the key, so it cannot act as an admin of the addon.
const UserSigHeader = "X-Antresol-User-Sig"

const (
	// UserKeyEnv is the environment variable holding the key the server
	// signs UserHeader with for this process; unset: the server does not
	// sign (an older one), and the header is taken as it is.
	UserKeyEnv = "ANTRESOL_ADDON_USER_KEY"
	// AcceptUnsignedEnv is "on" when the admin lets a header without a
	// signature through (an escape hatch for requests made by hand). Any
	// other value, and unset: with a key set, an unsigned header is
	// refused. A wrong or stale signature is refused either way.
	AcceptUnsignedEnv = "ANTRESOL_ADDON_ACCEPT_UNSIGNED"
)

// userSigWindow is how far from now a signature's time may be.
const userSigWindow = 5 * time.Minute

// SignUser is the value of UserSigHeader for the value of UserHeader under
// key, made at now (the server's side; an addon's tests may make one).
func SignUser(key, userHeader string, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	return ts + "." + userMAC(key, ts, userHeader)
}

func userMAC(key, ts, userHeader string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(ts + "." + userHeader))
	return hex.EncodeToString(m.Sum(nil))
}

// userSigOK: sig is SignUser of userHeader under key, made within
// userSigWindow of now.
func userSigOK(key, userHeader, sig string, now time.Time) bool {
	ts, mac, ok := strings.Cut(sig, ".")
	if !ok {
		return false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || now.Sub(time.Unix(sec, 0)).Abs() > userSigWindow {
		return false
	}
	return hmac.Equal([]byte(mac), []byte(userMAC(key, ts, userHeader)))
}

// userTrusted: the header h with the signature sig (maybe none) may be taken.
func userTrusted(key, h, sig string) bool {
	if sig == "" {
		return os.Getenv(AcceptUnsignedEnv) == "on"
	}
	return userSigOK(key, h, sig, time.Now())
}

// UserFrom is the profile in the request's X-Antresol-User; ok is false
// when the header is missing or broken, or the server's signature of it
// (X-Antresol-User-Sig) is wrong, stale or missing (unless the admin set
// AcceptUnsignedEnv to "on"). With no key (a server before signing) the
// header is taken as it is.
func UserFrom(r *http.Request) (*User, bool) {
	h := r.Header.Get(UserHeader)
	if h == "" {
		return nil, false
	}
	if key := os.Getenv(UserKeyEnv); key != "" && !userTrusted(key, h, r.Header.Get(UserSigHeader)) {
		return nil, false
	}
	var u User
	if err := json.Unmarshal([]byte(h), &u); err != nil {
		return nil, false
	}
	return &u, true
}

// RequireAdmin lets only admins through to next; anyone else gets 403
// {"error": "Недостаточно прав"}. The server already refuses x/admin/… to
// non-admins; this is defence in depth for an addon's /api/admin/….
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := UserFrom(r); !ok || !u.IsAdmin || u.IsKid {
			WriteError(w, http.StatusForbidden, "Недостаточно прав")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WriteJSON answers status with v as JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteError answers status with {"error": msg}, the contract's error
// shape.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}
