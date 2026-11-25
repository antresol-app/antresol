package addons

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/antresol-app/antresol/addonkit"
)

// The profile an addon acts for travels in X-Antresol-User, which any
// process that can open the addon's socket (another addon's, running as the
// same user) could write as well. So the server signs it: every process gets
// its own key in its environment (addonkit.UserKeyEnv), the server signs the
// header with the key of the socket it talks to (X-Antresol-User-Sig), and
// addonkit.UserFrom checks it. Another addon does not know the key.

// userKeySecret is this server run's secret the process keys come from; it
// never leaves the server.
var userKeySecret = hex32Random() + hex32Random()

// UserKey is the key the addon process listening on sock signs and checks
// X-Antresol-User with. A socket belongs to one process (its name has the
// start's number), so a restarted addon has a new key.
func UserKey(sock string) string {
	m := hmac.New(sha256.New, []byte(userKeySecret))
	m.Write([]byte("addon-user-key\x00" + sock))
	return hex.EncodeToString(m.Sum(nil))
}

// SetUser puts the profile header (value, as ASCIIJSON made it) and its
// signature on a request for the addon process listening on sock.
func SetUser(h http.Header, sock, value string) {
	h.Set(addonkit.UserHeader, value)
	h.Set(addonkit.UserSigHeader, addonkit.SignUser(UserKey(sock), value, time.Now()))
}
