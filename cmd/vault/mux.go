package main

import (
	"net/http"
	"strings"

	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/jellyfin"
)

// buildMux puts the Subsonic API (/rest/), OPDS (/opds) and the Jellyfin
// API next to the web API and the SPA. /rest, /opds and Jellyfin are
// outside the API router's csrf guard on purpose: they have no cookie
// session, every request carries its password or access token, so a
// cross-site request gains nothing; and music apps POST plain forms, which
// csrf would refuse. Jellyfin paths have known first segments in any case
// (jellyfin.IsJellyfinPath), none of which is a web app route, so they
// never shadow the SPA. A nil handler is not mounted. Their JSON, XML and
// feeds are gzipped like the web API's (a 100-item Jellyfin page is
// hundreds of KB); streams, downloads and pictures are left alone.
func buildMux(web, subsonic, opds, jf http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	// Subsonic and OPDS clients rarely send Accept-Language: no header
	// keeps them Russian; one that does gets its language
	if subsonic != nil {
		subsonic = api.Gzip(i18n.Middleware(subsonic), restBinary)
	}
	if opds != nil {
		opds = api.Gzip(i18n.Middleware(opds), opdsBinary)
	}
	if subsonic != nil {
		mux.Handle("/rest/", subsonic)
	}
	if opds != nil {
		mux.Handle("/opds", opds)
		mux.Handle("/opds/", opds)
	}
	if jf == nil {
		mux.Handle("/", web)
		return mux
	}
	jf = gzipJellyfin(jf)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if jellyfin.IsJellyfinPath(r.URL.Path) {
			jf.ServeHTTP(w, r)
			return
		}
		web.ServeHTTP(w, r)
	}))
	return mux
}

// gzipJellyfin compresses the Jellyfin API's JSON; the listener of its own
// (VAULT_JELLYFIN_LISTEN) wraps its handler the same way.
func gzipJellyfin(jf http.Handler) http.Handler { return api.Gzip(jf, jellyfinBinary) }

// restBinary: the Subsonic calls that answer with audio, video or a picture
// (the name is case-insensitive, with or without ".view").
func restBinary(p string) bool {
	name := strings.TrimSuffix(strings.ToLower(p[strings.LastIndexByte(p, '/')+1:]), ".view")
	switch name {
	case "stream", "download", "getcoverart", "getavatar", "hls", "hls.m3u8":
		return true
	}
	return false
}

// opdsBinary: the books and covers of an OPDS catalog.
func opdsBinary(p string) bool {
	return strings.HasPrefix(p, "/opds/files/") || strings.HasPrefix(p, "/opds/covers/")
}

// jellyfinBinary: the Jellyfin routes that stream, download or send a
// picture (paths are case-insensitive there).
func jellyfinBinary(p string) bool {
	p = strings.ToLower(p)
	return strings.HasPrefix(p, "/videos/") || strings.HasPrefix(p, "/livestreams/") ||
		strings.HasPrefix(p, "/playback/bitratetest") || strings.HasPrefix(p, "/userimage") ||
		strings.HasSuffix(p, "/download") || strings.Contains(p, "/images/") || strings.HasPrefix(p, "/socket")
}
