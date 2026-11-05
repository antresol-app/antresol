package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

func TestPlaybackRequest(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		// media reads
		{"GET", "/api/stream/5", true},
		{"GET", "/api/hls/abc/seg-3.m4s", true},
		{"GET", "/api/hls/abc/init.mp4", true},
		{"GET", "/api/torrent-stream/ih/0", true},
		{"GET", "/api/audio/7", true},
		{"GET", "/api/tracks/9/stream", true},
		{"GET", "/rest/stream.view", true},
		{"GET", "/rest/stream", true},
		// Jellyfin counts its own reads after auth (jellyfin.Server.Activity)
		{"GET", "/Videos/abc/stream.mkv", false},
		{"GET", "/videos/abc/hls1/main/3.ts", false},
		{"GET", "/Items/abc/Download", false},
		{"GET", "/Videos/abc/main.m3u8", false},
		{"POST", "/Items/abc/PlaybackInfo", false},
		// other big readers of the disk
		{"GET", "/api/download/5", true},
		{"GET", "/api/device-copies/3/file", true},
		{"GET", "/rest/download.view", true},
		{"GET", "/rest/download", true},
		{"GET", "/opds/files/12/book.epub", true},
		{"GET", "/api/files/content", true},
		// not reads of the media: a paused player keeps sending these
		{"PUT", "/api/hls/abc", false},
		{"GET", "/api/hls/abc/index.m3u8", false},
		{"POST", "/api/play/5", false},
		{"POST", "/api/torrent-play/ih/0", false},
		{"PUT", "/api/progress/12", false},
		{"POST", "/Sessions/Playing/Progress", false},
		{"GET", "/videos/abc/master.m3u8", false},
		{"GET", "/api/device-copies", false},
		{"GET", "/api/items/5", false},
		// scrub previews are generated images, not the media disk
		{"GET", "/api/files/5/trickplay", false},
		{"GET", "/api/files/5/trickplay/0.jpg", false},
		{"GET", "/api/tracks/9", false},
		{"GET", "/opds", false},
		{"GET", "/", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := playbackRequest(r); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

// The web app's page loads hold the background readers back for a moment
// (they share the one slow disk); its polls and the event stream do not,
// or an open tab would hold them forever.
func TestBrowsingRequest(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/home", true},
		{"GET", "/api/items/5", true},
		{"GET", "/api/items", true},
		{"GET", "/api/music/albums/3", true},
		{"GET", "/api/search", true},
		{"GET", "/api/torrents", true},
		{"GET", "/api/images/abc.jpg", true},
		{"GET", "/api/stats", true},
		// polls and streams of an open tab
		{"GET", "/api/events", false},
		{"GET", "/api/library/pending", false},
		{"GET", "/api/library/status", false},
		{"GET", "/api/acquire/progress", false},
		{"GET", "/api/disk", false},
		{"GET", "/api/hls/abc/index.m3u8", false},
		{"PUT", "/api/hls/abc", false},
		{"PUT", "/api/progress/12", false},
		{"GET", "/api/torrents/abc/status", false},
		{"GET", "/api/migrate/state", false},
		{"GET", "/api/items/5/voiceovers/downloads", false},
		{"GET", "/api/screen-time/limited", false},
		{"GET", "/api/me/screen-time", false},
		{"GET", "/api/requests/count", false},
		{"GET", "/api/import/jobs/3", false},
		// the other page loads
		{"GET", "/api/items/5/related", true},
		{"GET", "/api/items/5/family", true},
		{"GET", "/api/music/home", true},
		{"GET", "/api/music/artists/4", true},
		{"GET", "/api/books/home", true},
		{"GET", "/api/books/7", true},
		{"GET", "/api/discover/movie/550", true},
		{"GET", "/api/settings", true},
		{"GET", "/api/me", true},
		// playback is counted as playback
		{"GET", "/api/stream/5", false},
		{"GET", "/api/tracks/9/stream", false},
		// not the web app's
		{"GET", "/rest/getAlbumList2.view", false},
		{"GET", "/", false},
		{"GET", "/assets/index-abc.js", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := browsingRequest(r); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestTrackBrowsing(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1000, 0)
	a := &media.Activity{Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }}
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	h := trackBrowsing(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/home" && !a.Reading(browseQuiet) {
			t.Error("a page load in flight: not browsing")
		}
	}), a)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/library/pending", nil))
	if a.Reading(browseQuiet) {
		t.Fatal("a poll counted as browsing")
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/home", nil))
	if !a.Reading(browseQuiet) {
		t.Fatal("just browsed: quiet at once")
	}
	advance(browseQuiet + time.Second)
	if a.Reading(browseQuiet) {
		t.Fatal("still browsing after the quiet")
	}
}

func TestTrackPlaybackTouches(t *testing.T) {
	a := &media.Activity{}
	h := trackPlayback(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), a)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/items/5", nil))
	if a.Within(time.Minute) {
		t.Fatal("a page view counted as playback")
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/stream/5", nil))
	if !a.Within(time.Minute) {
		t.Fatal("a stream read not counted")
	}
}

// A long response (a direct play reading bytes=0- for an hour) counts as
// playback for as long as it runs, not only the 2 min after it began; and
// 2 min after it ends.
func TestTrackPlaybackLongResponse(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1000, 0)
	a := &media.Activity{Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }}
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	inside, release := make(chan struct{}), make(chan struct{})
	h := trackPlayback(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(inside)
		<-release
	}), a)
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/stream/5", nil))
		close(done)
	}()
	<-inside
	advance(time.Hour)
	if a.Within(2*time.Minute) || !a.Reading(2*time.Minute) {
		t.Fatal("an hour into the response: not reading")
	}
	close(release)
	<-done
	if !a.Reading(2 * time.Minute) {
		t.Fatal("just ended: quiet at once")
	}
	advance(3 * time.Minute)
	if a.Reading(2 * time.Minute) {
		t.Fatal("3 min after the end: still reading")
	}
}
