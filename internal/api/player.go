package api

import (
	"context"
	"net/http"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/screentime"
)

// Player is the web player's machinery for another API of the same server
// (the Jellyfin API for Infuse and Swiftfin): the probe cache, the quality
// caps, the HLS sessions and the subtitle extractions are the web's own, so
// one extraction or one session limit serves every client. The caller has
// checked that the request's profile (auth.WithUser) may see the file.
type Player struct{ h *lib }

// ErrNoTranscoder: the server has no HLS manager (StartHLS).
var ErrNoTranscoder = errNoTranscoder

// NewRouterWithPlayer is NewRouter and the Player over its library
// handlers (nil when the library is not mounted).
func NewRouterWithPlayer(deps Deps) (http.Handler, *Player) {
	var p *Player
	r := newRouter(deps, func(h *lib) { p = &Player{h} })
	return r, p
}

// Probe is the file's cached probe, running ffprobe on first use.
func (p *Player) Probe(ctx context.Context, f library.File) (media.ProbeResult, error) {
	return p.h.probe(ctx, f)
}

// Caps applies the player's «Авто» quality to a client's caps: the
// original at home, «Качество вне дома» from outside; c.Lang is set from
// the request.
func (p *Player) Caps(r *http.Request, c media.ClientCaps) media.ClientCaps {
	c, _ = p.h.playbackCaps(r, c, qualityAuto)
	return c
}

// StartHLS starts an HLS session of the request's profile from the start
// of file f; replace is the profile's session it replaces (""). The web's
// rule that a new session of the profile on the same file ends the old one
// (an orphan of a reloaded page) does not apply: the caller replaces by
// device, so two TVs of one profile do not stop each other. Errors:
// ErrNoTranscoder, media.ErrUserLimit, media.ErrBusy, media.ErrNoSpace.
func (p *Player) StartHLS(r *http.Request, f library.File, d media.Decision, durationMs int64, replace string) (string, error) {
	// FileID 0: no same-file replacement in media.HLSManager.Start
	sid, _, err := p.h.startSession(r, 0, f.ItemID, f.Path, d, 0, replace, durationMs)
	return sid, err
}

// ServeHLS serves "index.m3u8" or a segment of the profile's session sid,
// the playlist's URIs passed through uri (the complete VOD playlist when
// the session has one). media.ErrNoSession (nothing written): not the
// profile's session, or gone.
func (p *Player) ServeHLS(w http.ResponseWriter, r *http.Request, sid, name string, uri func(string) string) error {
	return p.h.serveHLS(w, r, sid, name, uri)
}

// TouchHLS is the heartbeat of the profile's session sid (false: gone).
func (p *Player) TouchHLS(r *http.Request, sid string) bool {
	if p.h.HLS == nil || !p.h.sessions.owns(sid, userOf(r).ID) {
		return false
	}
	if _, err := p.h.HLS.Touch(sid); err != nil {
		p.h.sessions.remove(sid)
		return false
	}
	return true
}

// StopHLS ends the profile's session sid.
func (p *Player) StopHLS(r *http.Request, sid string) {
	if p.h.HLS != nil && p.h.sessions.owns(sid, userOf(r).ID) {
		p.h.HLS.Stop(sid)
		p.h.sessions.remove(sid)
	}
}

// TimelineOffset is the timestamp (seconds) the stream's start has in the
// segments of session sid (media.HLSManager.TimelineOffset; false:
// unknown).
func (p *Player) TimelineOffset(sid string) (float64, bool) {
	t, ok := p.h.HLS.(interface {
		TimelineOffset(id string) (float64, bool)
	})
	if !ok {
		return 0, false
	}
	return t.TimelineOffset(sid)
}

// ExternalAudio lists the playable audio files beside f (extaudio), in
// the order of /api/play: track i is index 2000+i there. durationMs is the
// video's (a file of another cut is left out).
func (p *Player) ExternalAudio(ctx context.Context, f library.File, durationMs int64, startMs *float64) []media.ExternalAudio {
	var out []media.ExternalAudio
	for _, e := range p.h.externalAudio(ctx, f, durationMs, startMs) {
		out = append(out, e.media())
	}
	return out
}

// ServeFile serves the video f with Range support (the web's
// /api/stream): a kid profile's screen time is checked first and counted
// while it is read.
func (p *Player) ServeFile(w http.ResponseWriter, r *http.Request, f library.File) {
	it := library.Item{ID: f.ItemID, Kind: library.KindMovie}
	if !p.h.screenAllowed(w, r, it.Kind) {
		return
	}
	serveFile(w, r, f, false, p.h.screenWrap(r, it))
}

// ScreenTime is nil when the request's profile may watch now, else why
// not (screentime.Service.Allow for video).
func (p *Player) ScreenTime(r *http.Request) *screentime.Refusal {
	return p.h.ScreenTime.Allow(r.Context(), userOf(r), screentime.Video)
}

// ScreenOpened marks the title of f opened for playback (its player's
// reports count from now on).
func (p *Player) ScreenOpened(r *http.Request, f library.File) {
	p.h.screenOpened(r, library.Item{ID: f.ItemID, Kind: library.KindMovie})
}

// ServeSubtitle answers with the WebVTT of subtitle stream idx of f (the
// web's /api/subs: embedded by ffprobe index, external from 1000 by
// position). Still extracting: 503 + Retry-After, or the cues so far.
func (p *Player) ServeSubtitle(w http.ResponseWriter, r *http.Request, f library.File, idx int) {
	p.h.serveSubtitle(w, r, f, idx, "")
}
