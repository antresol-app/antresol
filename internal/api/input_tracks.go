package api

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/extaudio"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// The audio sidecars of an addon input: voice-over files the addon serves
// beside the video, read through the loopback relay (parts 1…A) and muxed
// as ffmpeg's second input. Their language, title and studio come from
// their names (extaudio.FindIn), else from their own tags. Tracks are
// probed detached once the video's own probe is known and waited for
// briefly: a track still probing, or whose probe failed, is not listed
// this time (a later play lists it).

// inputTrackProbes: at most this many probes of one input's sidecars run
// at once; each also holds one of the server's inputProbeSlots.
const inputTrackProbes = 2

// inputTrackProbeTimeout bounds the probe of one sidecar.
var inputTrackProbeTimeout = 20 * time.Second

// inputTrackWait bounds how long one play waits, in all, for the probes of
// the sidecars it lists (one still probing then is not listed; one named
// by the request makes it answer 202 pending).
var inputTrackWait = 4 * time.Second

// inputTrackFailMemory is how long a failed probe of an input's part (its
// main file or a sidecar) is remembered: reported as failed instead of
// probed again by every request.
var inputTrackFailMemory = 2 * time.Minute

// inputTrackPreferWait bounds how long play answers 202 pending for the
// sidecar the profile's language calls for when only sidecars still
// probing have that language, counted from the start of their probe.
var inputTrackPreferWait = 20 * time.Second

// inputAudio is what externalAudio found: the listed tracks, the tracks
// still probing (by src) and the byte seek of each listed raw stream (by
// its path, media.RawAudioByteSeek).
type inputAudio struct {
	ext     []extAudio
	pending map[string]pendingTrack
	seeks   map[string]media.ByteSeek
}

// externalAudio lists the playable audio sidecars of in in request order:
// probed, of the video's cut (durationMs, extaudio.Describe). The i-th
// sidecar is relay part 1+i, its path that part's loopback URL, its src
// extaudio.ID of its name. Probes not known yet start now (detached) and
// are waited for at most inputTrackWait in all, or until ctx ends; pending
// are the sidecars still probing then (not listed).
func (p *inputPlay) externalAudio(ctx context.Context, in addons.Input, durationMs int64) inputAudio {
	var out inputAudio
	audio := in.Req.Sidecars.Audio
	if !externalAudioClient(ctx) || len(audio) == 0 {
		return out
	}
	// the name's language, title and studio, looked up by name (FindIn
	// sorts and caps what it returns: never its order or position)
	names := make([]string, 0, 1+len(audio))
	names = append(names, in.Req.Name)
	for _, a := range audio {
		names = append(names, a.Name)
	}
	byName := map[string]extaudio.Track{}
	for _, t := range extaudio.FindIn(in.Req.Name, names) {
		byName[t.Path] = t
	}
	for i := range audio { // every probe runs while the first is waited for
		p.trackProbe(ctx, in, 1+i, 0)
	}
	deadline := time.Now().Add(inputTrackWait)
	for i, a := range audio {
		n := 1 + i
		src := extaudio.ID(a.Name)
		t, ok := byName[a.Name]
		if !ok {
			t = extaudio.Track{Lang: "und"} // its own tags say
		}
		pr, err := p.trackProbe(ctx, in, n, max(time.Until(deadline), 0))
		if errors.Is(err, errStillWorking) || (err != nil && ctx.Err() != nil) {
			if out.pending == nil {
				out.pending = map[string]pendingTrack{}
			}
			since, ok := p.trackStarts.at(inputPartKey(in, n))
			if !ok { // its job has not begun yet
				since = p.clock()
			}
			out.pending[src] = pendingTrack{lang: t.Lang, since: since}
			continue
		}
		if err != nil {
			continue // failed: not listed (remembered for inputTrackFailMemory)
		}
		url := p.internalURL(in.ID, n)
		t.Path = url
		s, ok := extaudio.Describe(ctx, t, func(context.Context, string) (media.ProbeResult, error) { return pr, nil }, durationMs)
		if !ok {
			continue
		}
		out.ext = append(out.ext, extAudio{path: url, src: src, studio: t.Studio, stream: s})
		if bs := media.RawAudioByteSeek(pr); bs.Format != "" {
			if out.seeks == nil {
				out.seeks = map[string]media.ByteSeek{}
			}
			out.seeks[url] = bs
		}
	}
	return out
}

// inputPartKey is the probe key of part n of in: id, part, size.
func inputPartKey(in addons.Input, n int) string {
	_, _, size, _ := in.Part(n)
	return in.ID + "/" + strconv.Itoa(n) + "/" + strconv.FormatInt(size, 10)
}

// trackProbe is the probe of sidecar part n of in: the kept one, else that
// of its detached job (started if none runs), waited for up to wait
// (errStillWorking: still probing).
func (p *inputPlay) trackProbe(ctx context.Context, in addons.Input, n int, wait time.Duration) (media.ProbeResult, error) {
	key := inputPartKey(in, n)
	if pr, ok := p.trackProbes.get(key); ok {
		return pr, nil
	}
	if p.failedRecently(&p.trackFails, key) && !p.trackJobs.running(key) {
		return media.ProbeResult{}, errTrackRecentlyFailed
	}
	src := p.internalURL(in.ID, n)
	timeout := inputTrackProbeTimeout
	// the job's bound: a turn for the input's and the server's slots, then the probe
	// (a job that ends while waiting for a slot is no failure: the next
	// play starts another; its probe start is stamped once both are held)
	return p.trackJobs.do(ctx, key, wait, 3*timeout, func(ctx context.Context) (media.ProbeResult, error) {
		if err := p.trackSlot(ctx, in.ID); err != nil {
			return media.ProbeResult{}, err
		}
		defer p.trackSlots.release(in.ID)
		var pr media.ProbeResult
		err := p.withProbeSlot(ctx, func() error {
			p.trackStarts.putAt(key, p.clock())
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			var err error
			pr, err = p.runProbe(pctx, src)
			return err
		})
		if errors.Is(err, errSlotWait) {
			return pr, err
		}
		if err == nil && (len(pr.Audio) == 0 || pr.DurationMs <= 0) {
			err = errTrackIncomplete
		}
		if err != nil {
			log.Printf("api: probe input track %s: %s", key, redactToken(err.Error()))
			p.trackFails.putAt(key, p.clock())
			return pr, err
		}
		p.trackProbes.putAny(key, pr)
		return pr, nil
	})
}

// trackSlot waits for one of input id's inputTrackProbes slots.
func (p *inputPlay) trackSlot(ctx context.Context, id string) error {
	for !p.trackSlots.acquire(id) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(trackSlotPoll):
		}
	}
	return nil
}

// failedRecently: key was put in f within inputTrackFailMemory (by p's clock).
func (p *inputPlay) failedRecently(f *keyTimes, key string) bool {
	t, ok := f.at(key)
	return ok && p.clock().Sub(t) < inputTrackFailMemory
}

// seriesVoice is the title of the voice uid last chose in the series of
// in (the library's series_tracks): the series of its title when that is
// a series («series», TMDB id) the library knows, else that of its
// library item; "" when there is none: not in the library, a movie,
// nothing remembered, or the cleaned version of a voice (an input has none).
func (p *inputPlay) seriesVoice(ctx context.Context, uid int64, in addons.Input) string {
	if p.lib == nil || p.Library == nil {
		return ""
	}
	var sid int64
	if t := in.Req.Title; t != nil && t.Kind == library.KindSeries && t.TMDBID > 0 {
		if it, err := p.Library.ItemByTMDB(ctx, library.KindSeries, t.TMDBID); err == nil {
			sid = it.ID
		}
	}
	if sid <= 0 && in.Req.ItemID > 0 { // no title, or one the library does not know
		it, err := p.Library.Item(ctx, in.Req.ItemID)
		if err != nil {
			return ""
		}
		if sid, err = p.seriesOf(ctx, it); err != nil {
			return ""
		}
	}
	if sid <= 0 {
		return ""
	}
	st, ok, err := p.Library.SeriesTracks(ctx, uid, sid)
	if err != nil || !ok || st.Audio == nil || st.Audio.Kind != "" {
		return ""
	}
	return st.Audio.Title
}
