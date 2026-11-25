package api

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/extaudio"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/voclean"
	"github.com/antresol-app/antresol/internal/vofit"
)

// externalAudioBase is the first audio index of the files lying next to
// the video (extaudio): track i of externalAudio is externalAudioBase+i.
const externalAudioBase = 2000

// extAudio is an external audio track of a file, described for playback.
type extAudio struct {
	path, src, studio string
	stream            media.AudioStream // Index 0 of its file
	// a cleaned voice-over (voclean): kind media.KindCleaned, how it was
	// made, the stream it was made from; offsetMs shifts it on the
	// video's timeline (Decision.AudioInputOffsetMs)
	kind, method string
	cleanedOf    int
	offsetMs     float64
	// tempo: a fitted voice-over's (vofit; 0 or 1: none); fitted: its
	// offset and tempo were measured (the device copy's key says so)
	tempo  float64
	fitted bool
}

// media is e as the media package takes it.
func (e extAudio) media() media.ExternalAudio {
	return media.ExternalAudio{Path: e.path, Studio: e.studio, Stream: e.stream, Kind: e.kind, CleanedOf: e.cleanedOf, OffsetMs: e.offsetMs, Tempo: e.tempo}
}

// copyHash is the external track's part of a device copy's key: its file,
// and for a fitted one the fit (a new fit makes a new copy).
func (e extAudio) copyHash() string {
	h := media.ExternalAudioHash(e.path)
	if e.fitted {
		h += "|" + strconv.FormatFloat(e.offsetMs, 'f', 1, 64) + "|" + strconv.FormatFloat(e.tempo, 'f', 6, 64)
	}
	return h
}

// extAudioCache keeps extaudio.Describe of each external file by path,
// valid while its size and mtime (and the video's duration) stay: the item
// page and every /api/play list the tracks, and a probe per request would
// read the media disk each time.
type extAudioCache = boundedCache[string, extAudioEntry]

type extAudioEntry struct {
	size, mtime, videoMs int64
	a                    media.AudioStream
	ok                   bool
}

// maxExtAudioCache bounds the cache.
const maxExtAudioCache = 4096

// fresh: e was made for a file of st's size and mtime and a video of videoMs.
func (e extAudioEntry) fresh(st os.FileInfo, videoMs int64) bool {
	return e.size == st.Size() && e.mtime == st.ModTime().UnixNano() && e.videoMs == videoMs
}

// externalProbesAtOnce: how many of a file's external tracks are described at once.
const externalProbesAtOnce = 4

// extAudioProbeTimeout bounds one probe of an external file.
const extAudioProbeTimeout = 15 * time.Second

// externalAudio lists the playable external audio tracks of f in a stable
// order: the files beside it (sorted paths, extaudio.Find), then its
// cleaned voice-overs (voclean, by stream); a file that cannot be read,
// or of another cut than the video (durationMs, 0 = unknown), is left out.
// startMs is where the video's timeline begins (its probe's StartMs; nil:
// unknown), what the cleaned tracks are placed against.
func (h *lib) externalAudio(ctx context.Context, f library.File, durationMs int64, startMs *float64) []extAudio {
	if !externalAudioClient(ctx) {
		return nil
	}
	fits := h.fitsOf(ctx, f)
	var tracks []extaudio.Track
	for _, t := range extaudio.Find(f.Path) {
		if fit, fitted := fits[t.Path]; !fitted || fit.Offered() { // else: waiting for its fit, or of another cut (vofit)
			tracks = append(tracks, t)
		}
	}
	// each description may be a probe, a read of the media disk: together,
	// not one after another (the page of a movie with several voices waits for them)
	streams := make([]media.AudioStream, len(tracks))
	described := make([]bool, len(tracks))
	var g errgroup.Group
	g.SetLimit(externalProbesAtOnce)
	for i, t := range tracks {
		g.Go(func() error {
			streams[i], described[i] = h.describeAudio(ctx, f, t, durationMs)
			return nil
		})
	}
	_ = g.Wait()
	var out []extAudio
	for i, t := range tracks {
		if !described[i] {
			continue
		}
		e := extAudio{path: t.Path, src: extaudio.ID(t.Path), studio: t.Studio, stream: streams[i]}
		if fit, fitted := fits[t.Path]; fitted && fit.Applies() {
			e.offsetMs, e.tempo, e.fitted = fit.OffsetMs, fit.Tempo, true
		}
		out = append(out, e)
	}
	if ctx.Err() != nil {
		return out
	}
	return append(out, h.cleanedAudio(ctx, f, durationMs, startMs, out)...)
}

// fitsOf are the fits of f's external tracks (vofit) by path; nil
// without the database or when they cannot be read (the tracks are then
// listed as stage A lists them).
func (h *lib) fitsOf(ctx context.Context, f library.File) map[string]vofit.Stored {
	if h.DB == nil {
		return nil
	}
	m, err := vofit.ForFile(ctx, h.DB, f.ID)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("api: external audio fits of file %d: %v", f.ID, err)
		}
		return nil
	}
	return m
}

// cleanedAudio are f's cleaned voice-overs (voclean.Tracks), titled as
// the studio's track (the players add the badge, Jellyfin a suffix), on
// the video's timeline: offsetMs is where the track starts on the file's
// timestamps (cleaned_audio.start_ms) minus where the file's timeline
// begins now (start, its probe's; nil: where the track was made, 0).
//
// A cleaned version of an external voice-over (cleaned_audio.vo_path) is
// listed only while that track is among found (the tracks beside the
// video as listed: offered, not of another cut), made from its index.
func (h *lib) cleanedAudio(ctx context.Context, f library.File, durationMs int64, start *float64, found []extAudio) []extAudio {
	if h.DB == nil {
		return nil
	}
	tracks, err := voclean.Tracks(ctx, h.DB, f.ID)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("api: cleaned tracks of file %d: %v", f.ID, err)
		}
		return nil
	}
	var out []extAudio
	for _, c := range tracks {
		from := c.VoIndex
		if c.VoPath != "" {
			k := slices.IndexFunc(found, func(e extAudio) bool { return e.path == c.VoPath })
			if k < 0 {
				continue
			}
			from = externalAudioBase + k
		}
		title := c.Title
		if title == "" {
			title = c.Studio
		}
		a, ok := h.describeAudio(ctx, f, extaudio.Track{Path: c.Path, Lang: c.Lang, Title: title, Studio: c.Studio}, durationMs)
		if !ok {
			continue
		}
		out = append(out, extAudio{path: c.Path, src: extaudio.ID(c.Path), studio: c.Studio, stream: a,
			kind: media.KindCleaned, method: c.Method, cleanedOf: from, offsetMs: c.OffsetMs(start)})
	}
	return out
}

// describeAudio is extaudio.Describe of t, cached by path, size and mtime.
func (h *lib) describeAudio(ctx context.Context, f library.File, t extaudio.Track, durationMs int64) (media.AudioStream, bool) {
	{
		st, err := os.Stat(t.Path)
		if err != nil || !st.Mode().IsRegular() {
			return media.AudioStream{}, false
		}
		e, ok := h.extAudio.get(t.Path)
		if !ok || !e.fresh(st, durationMs) {
			pctx, cancel := context.WithTimeout(ctx, extAudioProbeTimeout)
			e = extAudioEntry{size: st.Size(), mtime: st.ModTime().UnixNano(), videoMs: durationMs}
			e.a, e.ok = extaudio.Describe(pctx, t, h.probeAudioFile, durationMs)
			timedOut := pctx.Err() != nil
			cancel()
			if ctx.Err() != nil {
				return media.AudioStream{}, false // the request is gone: nothing learnt
			}
			if timedOut { // a busy disk: left out now, asked again next time
				log.Printf("api: external audio %s of file %d: probe timed out", filepath.Base(t.Path), f.ID)
				return media.AudioStream{}, false
			}
			if !e.ok {
				log.Printf("api: external audio %s of file %d left out: unreadable, silent or of another cut", filepath.Base(t.Path), f.ID)
			}
			h.extAudio.put(t.Path, e, maxExtAudioCache)
		}
		return e.a, e.ok
	}
}

// probeAudioFile probes an external audio file (Deps.Probe in tests).
func (h *lib) probeAudioFile(ctx context.Context, path string) (media.ProbeResult, error) {
	if h.Probe != nil {
		return h.Probe(ctx, path)
	}
	return media.ProbeAudioFile(ctx, h.ffprobe(), path)
}

// withExternalAudio appends the external tracks to a file's audio list.
func withExternalAudio(tracks []AudioTrack, ext []extAudio) []AudioTrack {
	for i, e := range ext {
		a := e.stream
		t := AudioTrack{Index: externalAudioBase + i, Codec: a.Codec, Lang: a.Lang, Title: a.Title,
			Channels: a.Channels, External: true, Src: e.src, Studio: e.studio, Kind: e.kind, Method: e.method}
		if e.kind == media.KindCleaned {
			t.CleanedFrom = &e.cleanedOf
		}
		tracks = append(tracks, t)
	}
	return tracks
}

// preferredCleaned is the cleaned version of the file's stream idx among
// ext when the profile prefers cleaned voice-overs («Выбирать очищенную
// озвучку»): the server's own choice of a track (none asked for) swaps
// to it. Its position in ext; -1: none.
func (h *lib) preferredCleaned(ctx context.Context, uid int64, idx int, ext []extAudio) int {
	if idx < 0 {
		return -1
	}
	k := slices.IndexFunc(ext, func(e extAudio) bool { return e.kind == media.KindCleaned && e.cleanedOf == idx })
	if k < 0 {
		return -1
	}
	if p, err := h.prefsFor(ctx, uid); err != nil || !p.PreferCleaned {
		return -1
	}
	return k
}

// extAudioAt is the position in ext of the external track of a request:
// by its src id when given (the track at position i first, then any; -1:
// gone), else by position i (-1: out of range).
func extAudioAt(ext []extAudio, i int, src string) int {
	if src == "" {
		if i >= 0 && i < len(ext) {
			return i
		}
		return -1
	}
	if i >= 0 && i < len(ext) && ext[i].src == src {
		return i
	}
	return slices.IndexFunc(ext, func(e extAudio) bool { return e.src == src })
}

// audioPick is the audio track a request plays: the external track
// ext[pos] (pos >= 0), else the file's own track `own` (-1: its default).
// named: the request asked for a track; gone: it asked for an external one
// that is no longer there (the default plays).
type audioPick struct {
	pos, own    int
	named, gone bool
}

// askedAudio is the track a request names (asked: its audioIndex, src: its
// audioSrc; externalAudioBase and up are the external tracks). Nothing
// named: the caller makes the server's own choice.
func askedAudio(ext []extAudio, asked *int, src string) audioPick {
	pick := audioPick{pos: -1, own: -1, named: asked != nil}
	switch {
	case asked == nil:
	case *asked >= externalAudioBase:
		pick.pos = extAudioAt(ext, *asked-externalAudioBase, src)
		pick.gone = pick.pos < 0
	default:
		pick.own = *asked
	}
	return pick
}

// decideAudio is the playback decision for a pick (burned: the image
// subtitle burned in, 0: none) and the audioIndex the player is told.
func decideAudio(p media.ProbeResult, caps media.ClientCaps, ext []extAudio, pick audioPick, burned int) (media.Decision, int) {
	if pick.pos >= 0 {
		return media.DecideExternalAudio(p, caps, ext[pick.pos].media(), burned), externalAudioBase + pick.pos
	}
	d := media.Decide(p, caps, pick.own)
	if burned > 0 {
		d = media.DecideBurn(p, caps, pick.own, burned)
	}
	return d, d.AudioIndex
}

// capsHeader is how the native app says what it understands beyond the
// first App Store build: "external-audio" — the audio files beside a
// video (index 2000+ with their studio and src, «Очищено»). That build
// (1.0, 202610301925) knows none of it: it would pick such a track by its
// language alone, label a studio and its cleaned copy both «Русский» and
// send back a bare position, so it is never shown them.
const capsHeader = "X-Vault-Caps"

type extAudioClientKey struct{}

// clientCaps notes in the request's context whether its client lists
// external audio (the header, read before the session is known).
func clientCaps(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slices.Contains(strings.Split(r.Header.Get(capsHeader), ","), "external-audio") {
			r = r.WithContext(context.WithValue(r.Context(), extAudioClientKey{}, true))
		}
		next.ServeHTTP(w, r)
	})
}

// externalAudioClient: a browser, any caller without a Vault session (the
// Jellyfin API, background work), or a native app that said so.
func externalAudioClient(ctx context.Context) bool {
	if s, ok := auth.SessionFrom(ctx); !ok || s.Kind != auth.KindNative {
		return true
	}
	on, _ := ctx.Value(extAudioClientKey{}).(bool)
	return on
}
