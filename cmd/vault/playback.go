package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

// playbackRequest reports a request that reads the media disk: a video or
// audio stream, an HLS segment, a live stream, and the other big
// readers (downloads, offline copies, OPDS and file-browser files), from
// the web API and Subsonic. Requests a paused player keeps
// sending (the HLS heartbeat, playlists, progress saves, play starts) are
// not: they would hold the analysis off for as long as a tab stays open.
func playbackRequest(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/hls/"):
		// /api/hls/{session}/{name}: a segment (not the playlist, not the PUT heartbeat)
		rest := strings.TrimPrefix(p, "/api/hls/")
		_, name, ok := strings.Cut(rest, "/")
		return r.Method == http.MethodGet && ok && name != "" && !strings.HasSuffix(name, ".m3u8")
	case strings.HasPrefix(p, "/api/device-copies/"):
		return strings.HasSuffix(p, "/file")
	case strings.HasPrefix(p, "/api/tracks/"):
		return strings.HasSuffix(p, "/stream")
	case strings.HasPrefix(p, "/api/stream/"), strings.HasPrefix(p, "/api/torrent-stream/"),
		strings.HasPrefix(p, "/api/audio/"), strings.HasPrefix(p, "/api/download/"),
		p == "/api/files/content", strings.HasPrefix(p, "/opds/files/"),
		strings.HasPrefix(p, "/rest/stream"), strings.HasPrefix(p, "/rest/download"):
		return true
	}
	// Jellyfin's media reads (streams, downloads, HLS segments) are counted
	// by jellyfin.Server itself, once the token or play session is checked
	// (Server.Activity): an unauthorised request never holds the analysis off.
	return false
}

// browseQuiet: the background readers (scrub previews, loudness, the
// skip-segment analysis, cleaned voice-overs and their fit) stay paused
// this long after the web app's last page-load request.
const browseQuiet = 3 * time.Second

// browsingRequest reports a request of the web app loading a page: a GET
// of the API (or an image) that is not playback, nor one of the requests
// an open tab keeps sending by itself (the event stream, the polls of
// pending downloads, of a download's progress, of the disk and of a scan).
// Background ffmpeg readers pause while it runs and for browseQuiet after:
// on the one slow disk their sequential reads otherwise put every page's
// database and image reads behind theirs.
//
// An allowlist of the calls pages make when they open (and their images):
// whatever an open page may repeat by itself (a download's status, the
// move's state, a voice-over's downloads, the screen-time and request
// counts, an import job) stays out, or one tab would hold the readers
// forever.
func browsingRequest(r *http.Request) bool {
	p := r.URL.Path
	if r.Method != http.MethodGet || playbackRequest(r) {
		return false
	}
	if pageLoads[p] || strings.HasPrefix(p, "/api/images/") {
		return true
	}
	seg := strings.Split(strings.TrimPrefix(p, "/api/"), "/")
	switch {
	case len(seg) == 2 && (seg[0] == "items" || seg[0] == "books" || seg[0] == "ratings"):
		return true // a title page, a book page
	case len(seg) == 3 && seg[0] == "items" && pageParts[seg[2]]:
		return true
	case len(seg) == 3 && seg[0] == "music" && (seg[1] == "albums" || seg[1] == "artists" || seg[1] == "playlists"):
		return true
	case len(seg) == 3 && seg[0] == "discover":
		return true // a title not in the library
	}
	return false
}

// pageLoads are the calls of the web app's pages, as they open.
var pageLoads = map[string]bool{
	"/api/me": true, "/api/home": true, "/api/items": true, "/api/search": true, "/api/discover": true,
	"/api/torrents": true, "/api/settings": true, "/api/stats": true, "/api/stats/storage": true,
	"/api/sources": true, "/api/users": true, "/api/follow": true, "/api/requests": true, "/api/requests/mine": true,
	"/api/arrivals": true, "/api/files/list": true, "/api/import/jobs": true, "/api/torrent-engine": true,
	"/api/music/home": true, "/api/music/genres": true, "/api/music/albums": true, "/api/music/artists": true,
	"/api/music/songs": true, "/api/music/playlists": true, "/api/books/home": true, "/api/books": true,
}

// pageParts are the title page's own calls beside /api/items/{id}.
var pageParts = map[string]bool{"family": true, "related": true, "collection": true, "track-prefs": true, "delete": true, "voiceovers": true}

// trackBrowsing counts the web app's page loads (browsingRequest) in a.
func trackBrowsing(h http.Handler, a *media.Activity) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if browsingRequest(r) {
			a.Begin()
			defer a.End()
		}
		h.ServeHTTP(w, r)
	})
}

// trackPlayback counts every playback request in flight from its start to
// the handler's return, and the quiet from then (media.Activity: the
// skip-segment analysis and the scrub previews wait for the disk to be
// idle): a direct play's single range read can stream for an hour.
func trackPlayback(h http.Handler, a *media.Activity) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if playbackRequest(r) {
			a.Begin()
			defer a.End()
		}
		h.ServeHTTP(w, r)
	})
}
