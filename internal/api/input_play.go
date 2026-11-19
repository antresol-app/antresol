package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/go-chi/chi/v5"
)

// Playing addon inputs: the videos addons register (/api/addon-core/v1/
// play/inputs) and the core plays like library files. ffmpeg and ffprobe
// read them through the loopback relay (input_relay.go); the profile's
// player through /api/play/input/{id}/….

// mediaKeyInputStreams is the cap per media key on an input's direct stream.
const mediaKeyInputStreams = 6

// inputRelayHeaderTimeout: how long the addon may take to start answering
// a relay read (the body may then stream as long as it likes).
const inputRelayHeaderTimeout = 60 * time.Second

type inputPlay struct {
	*lib
	inputs      *addons.Inputs
	users       *auth.Store
	base, token string // Deps.InputBase / InputToken; either empty → inputs cannot play
	// client reads the addon's socket: the one in the request's context
	// (inputSockKey), a new connection every time (sockets change per
	// spawn), no gzip asked (Content-Length stays), no redirect followed.
	client     *http.Client
	keyStreams keyedLimit   // media keys reading direct streams
	rt         addonRuntime // the addon's current manifest for the access re-check
	now        func() time.Time
	// the inputs' probes: complete ones kept by id/part/size, one job per
	// key, at most inputProbeSlots running at once (main and sidecars)
	probes     probeCache
	probeJobs  detached[media.ProbeResult]
	probeSlots chan struct{}
	probeFails keyTimes // main probes failed, by key (inputTrackFailMemory)
	// the sidecars' probes (input_tracks.go): kept, jobs, failures and
	// starts by id/part/size; inputTrackProbes slots per input
	trackProbes probeCache
	trackJobs   detached[media.ProbeResult]
	trackFails  keyTimes
	trackStarts keyTimes
	trackSlots  keyedLimit
	// the subtitle sidecars (input_subs.go): failed reads or conversions,
	// and those over maxInputSub, by id/part (conversions run in lib's subJobs);
	// subPruned: when the cache of converted ones was last pruned
	subFails, subLarge, subPruned keyTimes
	// subSlots: the conversions running, at most inputSubSlots server-wide;
	// subWait: how long one waits for a slot (inputSubWait)
	subSlots chan struct{}
	subWait  time.Duration
	// play.progress (input_progress.go): the hook caller (nil: none is
	// called), the calls of each input (one at a time) and the inputs whose failure
	// was logged
	hooks           inputHooks
	progressLogged  keyTimes
	progressFlights progressFlights
}

type inputSockKey struct{}

func newInputPlay(h *lib, deps Deps) *inputPlay {
	p := &inputPlay{lib: h, inputs: deps.AddonInputs, users: deps.Users, base: strings.TrimRight(deps.InputBase, "/"),
		token: deps.InputToken, rt: deps.Addons, keyStreams: keyedLimit{max: mediaKeyInputStreams},
		probeSlots: make(chan struct{}, inputProbeSlots), trackSlots: keyedLimit{max: inputTrackProbes},
		subSlots: make(chan struct{}, inputSubSlots), subWait: inputSubWait}
	if deps.inputSubWait > 0 {
		p.subWait = deps.inputSubWait
	}
	if deps.AddonHooks != nil { // never a typed nil in the interface
		p.hooks = deps.AddonHooks
	}
	p.client = &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives:     true,
			DisableCompression:    true,
			ResponseHeaderTimeout: inputRelayHeaderTimeout,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				sock, _ := ctx.Value(inputSockKey{}).(string)
				if sock == "" {
					return nil, errors.New("api: input relay without a socket")
				}
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", sock)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return p
}

// clock is p.now, else the inputs' clock (addons.Inputs.Now), else time.Now.
func (p *inputPlay) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	if p.inputs != nil && p.inputs.Now != nil {
		return p.inputs.Now()
	}
	return time.Now()
}

// mountInputRelay: the relay ffmpeg and ffprobe read inputs through, with
// the per-process token on loopback (no session).
func mountInputRelay(r chi.Router, p *inputPlay) {
	r.Get("/api/addon-input/{id}/{n}", p.relayRoute)
	r.Head("/api/addon-input/{id}/{n}", p.relayRoute)
}

// mountInputPlay: the profile's routes of an input (inside the session
// guard); kids get none.
func mountInputPlay(r chi.Router, p *inputPlay) {
	r.Group(func(r chi.Router) {
		r.Use(addonsNoKids)
		r.Get("/api/play/input/{id}", p.info)
		r.Post("/api/play/input/{id}", p.play)
		r.Get("/api/play/input/{id}/direct", p.direct)
		r.Head("/api/play/input/{id}/direct", p.direct)
		r.Get("/api/play/input/{id}/subs/{n}.vtt", p.subtitle)
		r.Put("/api/play/input/{id}/progress", p.progress)
	})
}

// msgTooManyStreams: a media key already holds mediaKeyInputStreams reads.
const msgTooManyStreams = "Слишком много одновременных потоков"

// inputInfo is GET /api/play/input/{id}: what the player shows and where it
// starts (never the addon's path or socket).
type inputInfo struct {
	ID       string               `json:"id"`
	AddonID  string               `json:"addonId"`
	Name     string               `json:"name"`
	ItemID   int64                `json:"itemId"`
	Title    *addonkit.InputTitle `json:"title,omitempty"`
	Slow     bool                 `json:"slow"`
	Meta     json.RawMessage      `json:"meta,omitempty"`
	StartSec float64              `json:"startSec"`
}

func (p *inputPlay) info(w http.ResponseWriter, r *http.Request) {
	in, ok := p.inputFor(w, r)
	if !ok {
		return
	}
	out := inputInfo{ID: in.ID, AddonID: in.AddonID, Name: in.Req.Name, ItemID: in.Req.ItemID, Title: in.Req.Title,
		Slow: in.Req.Slow, Meta: in.Req.Meta}
	out.StartSec = p.startSec(r.Context(), auth.HistoryID(r.Context()), in.Req.ItemID)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// startSec is the profile's saved position of itemID unless finished, else 0.
func (p *inputPlay) startSec(ctx context.Context, uid, itemID int64) float64 {
	if itemID <= 0 || p.lib == nil || p.Progress == nil {
		return 0
	}
	e, err := p.Progress.Get(ctx, uid, itemID)
	if err != nil {
		if !errors.Is(err, progress.ErrNotFound) {
			log.Printf("api: input start position of item %d: %v", itemID, err)
		}
		return 0
	}
	if e.Finished || e.Locator != "" || e.PositionMs <= 0 {
		return 0
	}
	return float64(e.PositionMs) / 1000
}

// direct is GET|HEAD /api/play/input/{id}/direct: the input's main file as
// it is. A media key holds at most mediaKeyInputStreams reads at once.
func (p *inputPlay) direct(w http.ResponseWriter, r *http.Request) {
	in, ok := p.inputFor(w, r)
	if !ok {
		return
	}
	if mk, keyed := auth.ViaMediaKey(r.Context()); keyed {
		if !p.keyStreams.acquire(mk) {
			writeErr(w, r, http.StatusTooManyRequests, msgTooManyStreams)
			return
		}
		defer p.keyStreams.release(mk)
	}
	p.relay(w, r, in, 0)
}

// inputFor is the live input of the request's {id} for its profile: one
// it registered for, of an addon still open to it (admins always, others
// only while the addon's access is "adults"). Else 404 «Видео не найдено»
// is written (never confirming another profile's id) and ok is false.
func (p *inputPlay) inputFor(w http.ResponseWriter, r *http.Request) (addons.Input, bool) {
	in, ok := p.inputs.Get(chi.URLParam(r, "id"))
	u := userOf(r)
	if ok && in.Req.UserID == u.ID && !u.IsKid {
		if u.IsAdmin {
			return in, true
		}
		if p.rt != nil {
			if inst, err := p.rt.Get(r.Context(), in.AddonID); err == nil && inst.Manifest.ForAdults() {
				return in, true
			}
		}
	}
	writeErr(w, r, http.StatusNotFound, msgAddonNoInput)
	return addons.Input{}, false
}

// internalURL is the loopback URL of part n of input id, the token in it.
func (p *inputPlay) internalURL(id string, n int) string {
	return p.base + "/api/addon-input/" + id + "/" + strconv.Itoa(n) + "?k=" + p.token
}

// inputProbeWait is how long POST /api/play/input/{id} waits for the
// probe before answering 202 {"pending":true} (under the proxy's 60 s).
var inputProbeWait = 25 * time.Second

// inputProbeTimeout bounds a main probe's job, its wait for a slot included.
var inputProbeTimeout = 10 * time.Minute

// inputProbeSlots bounds the input probes running at once, server-wide
// (main files and sidecars): each reads an addon over its socket.
const inputProbeSlots = 4

// msgInputUnreadable: the probe of an input's main file failed.
const msgInputUnreadable = "Не удалось прочитать видео. Возможно, оно ещё не готово"

// play is POST /api/play/input/{id}: direct play or HLS for the input's
// main file, as /api/play does for library files. ffprobe and ffmpeg read
// it through the relay; the answer never carries the relay URL.
func (p *inputPlay) play(w http.ResponseWriter, r *http.Request) {
	in, ok := p.inputFor(w, r)
	if !ok {
		return
	}
	var body playRequest
	if !decode(w, r, &body) {
		return
	}
	if p.base == "" || p.token == "" {
		writeErr(w, r, http.StatusServiceUnavailable, "Перекодирование недоступно")
		return
	}
	src := p.internalURL(in.ID, 0)
	pr, err := p.probeInput(r.Context(), in.ID+"/0/"+strconv.FormatInt(in.Req.Size, 10), src)
	switch {
	case errors.Is(err, errStillWorking), errors.Is(err, errSlotWait):
		writeJSON(w, http.StatusAccepted, map[string]any{"pending": true})
		return
	case r.Context().Err() != nil:
		return
	case errors.Is(err, errInputRecentlyFailed): // logged when it failed
		writeErr(w, r, http.StatusBadGateway, msgInputUnreadable)
		return
	case err != nil:
		log.Printf("api: probe input %s of %s: %s", in.ID, in.AddonID, redactToken(err.Error()))
		writeErr(w, r, http.StatusBadGateway, msgInputUnreadable)
		return
	}
	caps, q := p.playbackCaps(r, body.Caps, body.Quality)
	uid := userOf(r).ID
	// the audio sidecars, as /api/play has a file's: probed once the video
	// is, and waited for up to inputTrackWait
	ia := p.externalAudio(r.Context(), in, pr.DurationMs)
	if r.Context().Err() != nil {
		return
	}
	ext := ia.ext
	ai := body.audioIndex()
	e, external := extAudio{}, false
	if ai >= externalAudioBase {
		e, external = pickExtAudio(ext, ai-externalAudioBase, body.AudioSrc)
		// the track asked for is still probing: the client asks again; one
		// whose probe failed plays the default track
		_, srcPending := ia.pending[body.AudioSrc]
		if !external && (srcPending || (body.AudioSrc == "" && len(ia.pending) > 0)) {
			writeJSON(w, http.StatusAccepted, map[string]any{"pending": true})
			return
		}
		ai = -1
	} else if body.AudioIndex == nil {
		k, probingSince := p.preferredExternal(r.Context(), uid, pr, ext, ia.pending, func() string { return p.seriesVoice(r.Context(), uid, in) })
		switch {
		case k >= 0:
			e, external = ext[k], true
		case !probingSince.IsZero() && p.clock().Sub(probingSince) < inputTrackPreferWait:
			// the profile's language is only among tracks still probing:
			// wait for them a while rather than start in another language
			writeJSON(w, http.StatusAccepted, map[string]any{"pending": true})
			return
		case len(pr.Audio) == 0 && len(ext) > 0:
			// a video without audio of its own: a voice-over, not silence
			e, external = ext[0], true
		}
	}
	var d media.Decision
	audioIndex := -1
	if external {
		d = media.DecideExternalAudio(pr, caps, e.media(), 0)
		d.AudioInputByteSeek = ia.seeks[e.path]
		audioIndex = externalAudioBase + slices.IndexFunc(ext, func(x extAudio) bool { return x.path == e.path })
	} else {
		d = media.Decide(pr, caps, ai)
		audioIndex = d.AudioIndex
	}
	out := map[string]any{
		"mode": d.Mode, "reason": d.Reason, "startSec": max(body.StartSec, 0), "durationMs": pr.DurationMs,
		"audioIndex": audioIndex, "audio": withExternalAudio(audioTracks(pr), ext), "subtitles": p.subTracks(in), "quality": q,
	}
	if d.Mode == "direct" {
		p.dropReplaced(r, body)
		out["url"] = "/api/play/input/" + in.ID + "/direct"
	} else if !p.startHLS(w, r, out, 0, in.Req.ItemID, src, d, body, pr.DurationMs) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// errInputRecentlyFailed: the probe of an input's main file failed a
// moment ago (inputTrackFailMemory; not started again yet).
var errInputRecentlyFailed = errors.New("api: input probe failed recently")

// probeInput is the probe of src (an input's part through the relay),
// kept under key once complete. It runs detached (a player reload or a
// proxy read timeout does not cancel it, bounded to 10 minutes) and in
// one of the server's inputProbeSlots; errStillWorking: still running
// after inputProbeWait, the client asks again. A failure is remembered
// for inputTrackFailMemory (errInputRecentlyFailed meanwhile), so a probe
// that fails after the wait answers the next poll instead of starting
// another.
func (p *inputPlay) probeInput(ctx context.Context, key, src string) (media.ProbeResult, error) {
	if pr, ok := p.probes.get(key); ok {
		return pr, nil
	}
	if p.failedRecently(&p.probeFails, key) && !p.probeJobs.running(key) {
		return media.ProbeResult{}, errInputRecentlyFailed
	}
	return p.probeJobs.do(ctx, key, inputProbeWait, inputProbeTimeout, func(ctx context.Context) (media.ProbeResult, error) {
		var pr media.ProbeResult
		err := p.withProbeSlot(ctx, func() error {
			var err error
			pr, err = p.runProbe(ctx, src)
			return err
		})
		if errors.Is(err, errSlotWait) {
			return pr, err // it never read the input: no failure
		}
		if err != nil {
			p.probeFails.putAt(key, p.clock())
			return pr, err
		}
		p.probes.put(key, pr) // only a complete one
		return pr, nil
	})
}

// withProbeSlot runs f in one of the server's inputProbeSlots (main files
// and sidecars alike), waiting for it until ctx ends (then an error that
// is errSlotWait and ctx's).
func (p *inputPlay) withProbeSlot(ctx context.Context, f func() error) error {
	select {
	case p.probeSlots <- struct{}{}:
		defer func() { <-p.probeSlots }()
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", errSlotWait, ctx.Err())
	}
	return f()
}

// errSlotWait: a probe's context ended while it waited for a slot (it
// never read the input).
var errSlotWait = errors.New("api: no probe slot")

// runProbe probes src: Deps.Probe when set, else ffprobe.
func (p *inputPlay) runProbe(ctx context.Context, src string) (media.ProbeResult, error) {
	if p.Probe != nil {
		return p.Probe(ctx, src)
	}
	return media.Probe(ctx, p.ffprobe(), src)
}
