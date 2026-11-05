package jellyfin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// Player is the web player's machinery (api.Player): the probe cache, the
// quality caps, HLS sessions, file streaming and subtitle extraction. The
// request's profile is in its context (requireToken); the caller has
// checked that the profile may see the file. nil: the playback routes
// answer 404 (browsing only).
type Player interface {
	Probe(ctx context.Context, f library.File) (media.ProbeResult, error)
	// Caps applies the «Авто» quality: the original at home, «Качество вне
	// дома» from outside.
	Caps(r *http.Request, c media.ClientCaps) media.ClientCaps
	// StartHLS starts a session of the profile from the start of f.
	StartHLS(r *http.Request, f library.File, d media.Decision, durationMs int64, replace string) (string, error)
	// ServeHLS serves "index.m3u8" (URIs through uri) or a segment;
	// media.ErrNoSession (nothing written): not the profile's, or gone.
	ServeHLS(w http.ResponseWriter, r *http.Request, sid, name string, uri func(string) string) error
	TouchHLS(r *http.Request, sid string) bool
	// TimelineOffset: the timestamp (s) the stream start has in the
	// session's segments (false: unknown).
	TimelineOffset(sid string) (float64, bool)
	StopHLS(r *http.Request, sid string)
	ServeFile(w http.ResponseWriter, r *http.Request, f library.File)
	// ServeSubtitle answers with the WebVTT of stream idx (external: 1000+).
	ServeSubtitle(w http.ResponseWriter, r *http.Request, f library.File, idx int)
	// ExternalAudio lists the audio files beside f (stream externalAudioBase+i)
	// and its cleaned voice-overs, placed against startMs (the probe's).
	ExternalAudio(ctx context.Context, f library.File, durationMs int64, startMs *float64) []media.ExternalAudio
}

// play is what a PlaybackInfo answer decided, under its PlaySessionId:
// master.m3u8 starts the HLS session from it. The PlaySessionId (128
// random bits) is also the credential of the play's URLs (playlists,
// segments, subtitles: players fetch them without headers): it is bound
// to the profile and to the access token it was issued for, and ends with
// the play session (requirePlay).
type play struct {
	uid      int64
	token    string // the access token PlaybackInfo was called with
	device   string // its DeviceId (a new play on the device replaces its HLS session)
	itemID   int64
	file     library.File
	d        media.Decision
	duration int64 // ms
	// subs are the text subtitles listed in the HLS manifest (Hls delivery)
	subs    []MediaStream
	hls     string // the HLS session, "" until started
	created time.Time
	used    time.Time
	start   *sync.Mutex // one HLS start at a time (Server.startHLS)
}

// playTTL: a play session unused this long is forgotten (its HLS session
// idled out long before).
const playTTL = 6 * time.Hour

// maxPlays bounds the registry; maxPlaysPerProfile bounds one profile's
// share of it (a client calling PlaybackInfo in a loop evicts only its
// profile's oldest play sessions, never another profile's).
const (
	maxPlays           = 2000
	maxPlaysPerProfile = 20
)

// plays are the play sessions of PlaybackInfo, by PlaySessionId.
type plays struct {
	mu sync.Mutex
	m  map[string]*play
}

func newPlaySessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return hex.EncodeToString(b[:])
}

// oldestLocked is the least recently used play session (of profile uid;
// 0: of anyone).
func (ps *plays) oldestLocked(uid int64) string {
	var oldest string
	for k, v := range ps.m {
		if (uid == 0 || v.uid == uid) && (oldest == "" || v.used.Before(ps.m[oldest].used)) {
			oldest = k
		}
	}
	return oldest
}

// add registers p under id (newPlaySessionID).
func (ps *plays) add(id string, p *play) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.m == nil {
		ps.m = map[string]*play{}
	}
	now := time.Now()
	mine := 0
	for k, v := range ps.m {
		switch {
		case now.Sub(v.used) > playTTL:
			delete(ps.m, k)
		case v.uid == p.uid:
			mine++
		}
	}
	if mine >= maxPlaysPerProfile {
		delete(ps.m, ps.oldestLocked(p.uid))
	} else if len(ps.m) >= maxPlays {
		// only reachable with more than maxPlays/maxPlaysPerProfile profiles
		delete(ps.m, ps.oldestLocked(0))
	}
	p.created, p.used, p.start = now, now, &sync.Mutex{}
	ps.m[id] = p
}

// get is the profile's play session id (a copy; ok false: none or another
// profile's).
func (ps *plays) get(id string, uid int64) (play, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p := ps.m[id]
	if p == nil || p.uid != uid {
		return play{}, false
	}
	p.used = time.Now()
	return *p, true
}

// lookup is play session id whoever it belongs to (requirePlay).
func (ps *plays) lookup(id string) (play, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p := ps.m[id]
	if p == nil || time.Since(p.used) > playTTL {
		return play{}, false
	}
	return *p, true
}

// setHLS records the HLS session of a play session.
func (ps *plays) setHLS(id, sid string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if p := ps.m[id]; p != nil {
		p.hls = sid
	}
}

// lastHLS is the HLS session of the newest play of the item by the
// profile on the device (a new PlaybackInfo there replaces it: a track
// change, a reload); another device's playback is left alone.
func (ps *plays) lastHLS(uid int64, device string, itemID int64) string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	var best *play
	for _, p := range ps.m {
		if p.uid == uid && p.device == device && p.itemID == itemID && p.hls != "" && (best == nil || p.created.After(best.created)) {
			best = p
		}
	}
	if best == nil {
		return ""
	}
	return best.hls
}

// count is the number of play sessions of profile uid.
func (ps *plays) count(uid int64) int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	n := 0
	for _, p := range ps.m {
		if p.uid == uid {
			n++
		}
	}
	return n
}
