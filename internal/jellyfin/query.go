package jellyfin

import (
	"net/http"
	"strconv"
	"strings"
)

// qv is a query parameter, the key matched without regard to case
// (Swiftfin sends userId, Infuse UserId).
func qv(r *http.Request, key string) string {
	for k, v := range r.URL.Query() {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// qInt is an integer parameter, def when absent or not a number.
func qInt(r *http.Request, key string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(qv(r, key))); err == nil {
		return n
	}
	return def
}

// qBool is a boolean parameter ("true"/"false" in any case), def otherwise.
func qBool(r *http.Request, key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(qv(r, key))) {
	case "true", "1":
		return true
	case "false", "0":
		return false
	}
	return def
}

// qList is a comma-separated parameter without empty entries.
func qList(r *http.Request, key string) []string {
	var out []string
	for _, s := range strings.Split(qv(r, key), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
