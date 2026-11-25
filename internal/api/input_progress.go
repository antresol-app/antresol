package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
)

// The progress of an addon input: PUT /api/play/input/{id}/progress saves
// the position in the library item the input plays (when it names one) and
// tells the addon through its play.progress hook, at most once per
// inputProgressEvery per input, and always at the end or when the page
// goes away (keepalive). The save never waits for the addon.

// inputHooks calls an addon's declared hooks (*addons.Hooks).
type inputHooks interface {
	Call(ctx context.Context, id, name string, user *addonkit.User, in, out any) error
}

// inputProgressEvery is the least time between two play.progress calls of
// one input while it plays.
const inputProgressEvery = 15 * time.Second

// inputProgressFloor is the least time between two play.progress calls of
// one input that skip the throttle (keepalive or the end).
const inputProgressFloor = time.Second

// inputProgressSlack is how far a position may pass the duration (a
// player's duration is an estimate).
const inputProgressSlack = 5000

const msgBadPosition = "Неверная позиция"

// progress is PUT /api/play/input/{id}/progress {positionMs, durationMs,
// keepalive} → 204.
func (p *inputPlay) progress(w http.ResponseWriter, r *http.Request) {
	in, ok := p.inputFor(w, r)
	if !ok {
		return
	}
	var body struct {
		PositionMs int64 `json:"positionMs"`
		DurationMs int64 `json:"durationMs"`
		Keepalive  bool  `json:"keepalive"`
	}
	if !decode(w, r, &body) {
		return
	}
	pos, dur := body.PositionMs, body.DurationMs
	if pos < 0 || dur < 0 || pos > dur+inputProgressSlack {
		writeErr(w, r, http.StatusBadRequest, msgBadPosition)
		return
	}
	u := userOf(r)
	// the item's kind and visibility were checked when the input was registered
	if in.Req.ItemID > 0 && p.lib != nil && p.Progress != nil {
		if err := p.Progress.PlayedAt(r.Context(), auth.HistoryID(r.Context()), in.Req.ItemID, pos, dur, time.Time{}); err != nil {
			internalErr(w, r, "input progress save", err)
			return
		}
	}
	ended := dur > 0 && pos >= dur
	if p.hooks != nil {
		who := addonkit.User{ID: u.ID, UID: u.UID, Name: u.Name, IsAdmin: u.IsAdmin, IsKid: u.IsKid, History: addonHistory(r.Context())}
		if p.lib != nil && p.DB != nil {
			if _, l, err := p.effectiveUILanguage(r.Context(), u.ID); err == nil {
				who.Lang = l
			}
		}
		ev := addonkit.PlayProgress{InputID: in.ID, UserID: u.ID, PositionSec: float64(pos) / 1000,
			DurationSec: float64(dur) / 1000, Ended: ended}
		ctx := context.WithoutCancel(r.Context())
		p.progressFlights.submit(in.ID, p.clock(), body.Keepalive, ended, func() { p.callProgress(ctx, in, &who, ev) })
	}
	w.WriteHeader(http.StatusNoContent)
}

// callProgress runs in's addon's play.progress hook, when the addon
// declares it; a failure is logged once per input.
func (p *inputPlay) callProgress(ctx context.Context, in addons.Input, who *addonkit.User, ev addonkit.PlayProgress) {
	if p.rt == nil {
		return
	}
	inst, err := p.rt.Get(ctx, in.AddonID)
	if err != nil || !inst.Manifest.Answers("play.progress") {
		return // undeclared hooks are never called
	}
	if err := p.hooks.Call(ctx, in.AddonID, "play.progress", who, ev, nil); err != nil {
		if p.progressLogged.take(in.ID, time.Time{}, 0, false) {
			log.Printf("api: addon %s input %s play.progress: %v", in.AddonID, in.ID, err)
		}
	}
}

// take puts k at now and says true unless k was put within every before
// now (force: always). With every 0, only the first take of k is true.
func (f *keyTimes) take(k string, now time.Time, every time.Duration, force bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.m[k]; ok && !force && (every == 0 || now.Sub(t) < every) {
		return false
	}
	f.room(k, probeCacheMax, nil)
	f.m[k] = now
	return true
}

// progressFlights keeps, per input, when its last play.progress call began
// and whether one is running. Only one call of an input runs at a time: a
// call that comes while one runs waits as the single pending one (the
// latest, but an end is never replaced by a plain position) and starts
// when the running one finishes.
type progressFlights struct {
	boundedCache[string, *progressFlight]
}

type progressFlight struct {
	last      time.Time
	running   bool
	endedSent bool
	pending   func()
	pendEnded bool
}

// submit starts run (in its own goroutine) for input id unless the
// throttle says no: a plain position waits inputProgressEvery since the
// last call; keepalive and the end only inputProgressFloor (the first end
// of an input is exempt from that floor).
func (f *progressFlights) submit(id string, now time.Time, keepalive, ended bool, run func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.m[id]
	if s == nil {
		f.room(id, probeCacheMax, func(s *progressFlight) bool { return s.running }) // a running call is never forgotten
		s = &progressFlight{}
		f.m[id] = s
	}
	if !s.last.IsZero() {
		gap := now.Sub(s.last)
		switch {
		case keepalive || ended:
			if gap < inputProgressFloor && !(ended && !s.endedSent) {
				return
			}
		case gap < inputProgressEvery:
			return
		}
	}
	s.last = now
	if ended {
		s.endedSent = true
	}
	if s.running {
		if ended || !s.pendEnded {
			s.pending, s.pendEnded = run, ended
		}
		return
	}
	s.running = true
	go f.loop(s, run)
}

func (f *progressFlights) loop(s *progressFlight, run func()) {
	for run != nil {
		run()
		f.mu.Lock()
		run, s.pending, s.pendEnded = s.pending, nil, false
		if run == nil {
			s.running = false
		}
		f.mu.Unlock()
	}
}
