package api

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/subtitles"
)

// Playback helpers shared by every source of video that is not a library
// file (one read over a loopback URL): probe results kept, external tracks
// still probing, the profile's preferred voice among them, subtitles piped
// in, and the internal token hidden in a probe's error.

// probeCache keeps ffprobe results of remote inputs (probing over the
// network is slow). Bounded; only complete-looking probes are kept.
type probeCache struct {
	mu sync.Mutex
	m  map[string]media.ProbeResult
}

const probeCacheMax = 256

func (c *probeCache) get(k string) (media.ProbeResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.m[k]
	return p, ok
}

func (c *probeCache) put(k string, p media.ProbeResult) {
	if p.DurationMs <= 0 || p.Video == nil {
		return // probed an input not readable far enough yet: try again next time
	}
	c.putAny(k, p)
}

// putAny keeps p as it is (the caller judged it complete).
func (c *probeCache) putAny(k string, p media.ProbeResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= probeCacheMax {
		c.m = map[string]media.ProbeResult{}
	}
	c.m[k] = p
}

// keyTimes remembers when something happened to keys (a probe failed or
// started), bounded.
type keyTimes struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (f *keyTimes) put(k string) { f.putAt(k, time.Now()) }

// putAt remembers that something happened to k at t (a caller's clock).
func (f *keyTimes) putAt(k string, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil || len(f.m) >= probeCacheMax {
		f.m = map[string]time.Time{}
	}
	f.m[k] = t
}

// recent says whether k was put within the last d.
func (f *keyTimes) recent(k string, d time.Duration) bool {
	t, ok := f.at(k)
	return ok && time.Since(t) < d
}

// at is when k was put.
func (f *keyTimes) at(k string) (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.m[k]
	return t, ok
}

// errTrackIncomplete: the track's probe found no audio or no duration
// (its head is not readable yet).
var errTrackIncomplete = errors.New("api: track probe incomplete")

// errTrackRecentlyFailed: the track's probe failed a moment ago (not
// started again yet).
var errTrackRecentlyFailed = errors.New("api: track probe failed recently")

// pendingTrack is a track still probing: its language as its name says
// (extaudio.Track.Lang) and when its probe started.
type pendingTrack struct {
	lang  string
	since time.Time
}

// trackSlotPoll is how often a track probe waiting for a slot looks again.
const trackSlotPoll = 20 * time.Millisecond

// preferredExternal is the position in ext of the track the profile's
// audio languages call for when the video has none of them: the first of
// its languages that the video or an external track has decides (the
// video's: its own default stays). Among several tracks of that language
// the voice remembered for the series (seriesVoice) wins, else the first.
// -1: none. probingSince: that language is had only by tracks still
// probing (pending), whose probes began then (the earliest); zero: none.
func (h *lib) preferredExternal(ctx context.Context, uid int64, p media.ProbeResult, ext []extAudio, pending map[string]pendingTrack, seriesVoice func() string) (k int, probingSince time.Time) {
	if len(ext) == 0 && len(pending) == 0 {
		return -1, time.Time{}
	}
	prefs, err := h.prefsFor(ctx, uid)
	if err != nil {
		return -1, time.Time{}
	}
	for _, l := range prefs.AudioLangs {
		l = trackLang(l)
		if slices.ContainsFunc(p.Audio, func(a media.AudioStream) bool { return trackLang(a.Lang) == l }) {
			return -1, time.Time{}
		}
		same := func(e extAudio) bool { return trackLang(e.stream.Lang) == l }
		if k := slices.IndexFunc(ext, same); k >= 0 {
			v := ""
			if slices.ContainsFunc(ext[k+1:], same) { // several: the series' voice, if any
				v = strings.TrimSpace(seriesVoice())
			}
			if v != "" {
				if m := slices.IndexFunc(ext, func(e extAudio) bool {
					return same(e) && (strings.EqualFold(strings.TrimSpace(e.stream.Title), v) || strings.EqualFold(strings.TrimSpace(e.studio), v))
				}); m >= 0 {
					return m, time.Time{}
				}
			}
			return k, time.Time{}
		}
		for _, pt := range pending {
			if trackLang(pt.lang) == l && (probingSince.IsZero() || pt.since.Before(probingSince)) {
				probingSince = pt.since
			}
		}
		if !probingSince.IsZero() {
			return -1, probingSince
		}
	}
	return -1, time.Time{}
}

// redactToken hides the internal token of a loopback URL (?k=… or &k=…)
// in s, a probe's error: ffprobe's message starts with its input.
func redactToken(s string) string {
	return tokenParam.ReplaceAllString(s, "${1}k=…")
}

var tokenParam = regexp.MustCompile(`([?&])k=[^&\s"':]*`)

// trackLang is a stream's or preference's language as an ISO 639-1 code
// when known ("rus", "ru-RU" → "ru").
func trackLang(tag string) string {
	if c := subtitles.NormLang(tag); c != "" {
		return c
	}
	return primaryLang(tag)
}

// subPipeInput is subInput for a subtitle that is not a local file (read
// from elsewhere): decoded to UTF-8 here (UTF-16, UTF-8 with or without a
// BOM, else CP1251) and piped in, with the demuxer of its codec.
func subPipeInput(codec string, b []byte) (args []string, stdin []byte) {
	args = []string{"-protocol_whitelist", "pipe"}
	if f := media.SubDemuxer[codec]; f != "" {
		args = append(args, "-f", f)
	}
	return append(args, "-i", "pipe:0"), []byte(subtitles.Decode(b))
}
