package metadata

import (
	"net/url"
	"strings"
)

// posterHosts are the catalogs whose covers a client may name (a kid's
// request keeps one for the admins' browsers to load).
var posterHosts = map[string]bool{"image.tmdb.org": true, "covers.openlibrary.org": true, "coverartarchive.org": true}

// CatalogPoster is p when it is a cover of posterHosts (https) or of this
// server's image cache (/api/images/<name>); "" otherwise.
func CatalogPoster(p string) string {
	p = strings.TrimSpace(p)
	if len(p) > 1000 {
		return ""
	}
	if name, ok := strings.CutPrefix(p, "/api/images/"); ok {
		if name != "" && !strings.ContainsAny(name, "/\\?#") && !strings.Contains(name, "..") {
			return p
		}
		return ""
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || !posterHosts[strings.ToLower(u.Hostname())] {
		return ""
	}
	return p
}
