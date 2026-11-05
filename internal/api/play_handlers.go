package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/audioserve"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/screentime"
	"github.com/antresol-app/antresol/internal/segments"
	"github.com/antresol-app/antresol/internal/subtitles"
	"github.com/go-chi/chi/v5"
)

// HLS is the part of *media.HLSManager the API uses.
type HLS interface {
	Start(ctx context.Context, o media.StartOptions) (string, error)
	Playlist(id string) (string, error)
	// CompletePlaylist is a transcoded session's VOD playlist of the whole
	// film (false: the session keeps ffmpeg's growing EVENT playlist).
	CompletePlaylist(id string) (string, bool)
	Segment(id, name string) (string, error)
	Touch(id string) (media.SessionStatus, error)
	Stop(id string)
	// IOBusy: a session's ffmpeg has just started (a play or a seek);
	// background reads of the media disk wait meanwhile.
	IOBusy() bool
}

// externalSubBase is the first subtitle index of files lying next to the video.
const externalSubBase = 1000

func mountPlay(r chi.Router, h *lib) {
	r.Post("/api/play/{fileId}", h.play)
	r.Get("/api/stream/{fileId}", h.stream(false))
	r.Get("/api/download/{fileId}", h.stream(true))
	r.Get("/api/hls/{session}/{name}", h.hlsFile)
	r.Put("/api/hls/{session}", h.hlsTouch)
	r.Delete("/api/hls/{session}", h.hlsStop)
	r.Get("/api/subs/{fileId}/{index}.vtt", h.subtitle)
}

func baseName(p string) string { return filepath.Base(p) }

func cachedProbe(f library.File) (media.ProbeResult, bool) {
	var p media.ProbeResult
	if f.ProbeJSON == "" {
		return p, false
	}
	return p, json.Unmarshal([]byte(f.ProbeJSON), &p) == nil
}

// probe returns the cached probe of f, running ffprobe (and caching) on first use.
func (h *lib) probe(ctx context.Context, f library.File) (media.ProbeResult, error) {
	if p, ok := cachedProbe(f); ok {
		return p, nil
	}
	var p media.ProbeResult
	var err error
	if h.Probe != nil {
		p, err = h.Probe(ctx, f.Path)
	} else {
		p, err = media.Probe(ctx, h.ffprobe(), f.Path)
	}
	if err != nil {
		return p, err
	}
	if b, err := json.Marshal(p); err == nil {
		if err := h.Library.SetProbe(ctx, f.ID, string(b), p.DurationMs); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (h *lib) ffprobe() string {
	if h.FFprobe != "" {
		return h.FFprobe
	}
	return "ffprobe"
}

func (h *lib) ffmpeg() string {
	if h.FFmpeg != "" {
		return h.FFmpeg
	}
	return "ffmpeg"
}

func (h *lib) play(w http.ResponseWriter, r *http.Request) {
	f, it, ok := h.file(w, r)
	if !ok || !h.screenAllowed(w, r, it.Kind) {
		return
	}
	h.screenOpened(r, it)
	var in playRequest
	if !decode(w, r, &in) {
		return
	}
	if _, err := os.Stat(f.Path); errors.Is(err, os.ErrNotExist) {
		writeErr(w, r, http.StatusNotFound, "Файл пропал с диска")
		return
	}
	p, err := h.probe(r.Context(), f)
	if err != nil {
		internalErr(w, r, "probe "+f.Path, err)
		return
	}
	caps, q := h.playbackCaps(r, in.Caps, in.Quality)
	ext := h.externalAudio(r.Context(), f, p.DurationMs, p.StartMs)
	burned := h.burnable(p, in.BurnSubtitle)
	var d media.Decision
	audioIndex := -1
	ai := in.audioIndex()
	e, external := extAudio{}, false
	if ai >= externalAudioBase {
		// an audio file beside the video (ffmpeg's second input); one gone
		// meanwhile plays the default track, like an unknown embedded index
		e, external = pickExtAudio(ext, ai-externalAudioBase, in.AudioSrc)
		ai = -1
	} else if in.AudioIndex == nil {
		// the server's own choice: the cleaned version of the default track
		// for a profile that prefers it
		if k := h.preferredCleaned(r.Context(), userOf(r).ID, media.Decide(p, caps, -1).AudioIndex, ext); k >= 0 {
			e, external = ext[k], true
		}
	}
	if external {
		d = media.DecideExternalAudio(p, caps, e.media(), burned)
		for i := range ext {
			if ext[i].path == e.path {
				audioIndex = externalAudioBase + i
			}
		}
	} else {
		d = media.Decide(p, caps, ai)
		if burned > 0 {
			d = media.DecideBurn(p, caps, ai, burned)
		}
		audioIndex = d.AudioIndex
	}
	out := map[string]any{
		"mode": d.Mode, "reason": d.Reason, "startSec": max(in.StartSec, 0), "durationMs": p.DurationMs,
		"audioIndex": audioIndex, "audio": withExternalAudio(audioTracks(p), ext), "subtitles": subTracks(f, p, h.DataDir), "quality": q,
	}
	if burned > 0 {
		out["burnedSubtitle"] = burned
	}
	out["segments"] = h.segmentsOf(r.Context(), f, it, p.DurationMs)
	// its scrub previews next, if not made yet
	if h.Trickplay != nil && (it.Kind == library.KindMovie || it.Kind == library.KindEpisode) {
		if _, ok := h.Trickplay.Index(f.Path, f.Size, f.Mtime); !ok {
			h.Trickplay.Want(f.ID)
		}
	}
	h.prefetchSubs(f, p, max(in.StartSec, 0))
	if d.Mode == "direct" {
		h.dropReplaced(r, in)
		out["url"] = "/api/stream/" + strconv.FormatInt(f.ID, 10)
	} else if !h.startHLS(w, r, out, f.ID, it.ID, f.Path, d, in, p.DurationMs) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// segmentsOf is the recap, intro and credits of a library file for the
// player (what is stored; a file never looked at is queued): always a list.
func (h *lib) segmentsOf(ctx context.Context, f library.File, it library.Item, durationMs int64) []segments.Play {
	if h.Segments == nil || (it.Kind != library.KindMovie && it.Kind != library.KindEpisode) {
		return []segments.Play{}
	}
	return h.Segments.ForPlay(ctx, f.ID, durationMs, it.Kind == library.KindMovie)
}

// playRequest is the body of POST /api/play and /api/torrent-play.
type playRequest struct {
	Caps       media.ClientCaps `json:"caps"`
	AudioIndex *int             `json:"audioIndex"`
	// AudioSrc is the id of the external audio track AudioIndex names
	// (AudioTrack.Src): authoritative when given, as a file written
	// meanwhile shifts the positions.
	AudioSrc string  `json:"audioSrc"`
	StartSec float64 `json:"startSec"`
	// Quality is the player's quality menu: auto | original | 1080 | 720 | 480.
	Quality string `json:"quality"`
	// ReplaceSession is the HLS session this one replaces (seek, track or
	// quality change): it is stopped first.
	ReplaceSession string `json:"replaceSession"`
	// BurnSubtitle is the ffprobe index of an image subtitle (PGS, VobSub)
	// to burn into the picture: the video is transcoded with it.
	BurnSubtitle *int `json:"burnSubtitle"`
}

// burnable is the index of the image subtitle stream wanted for burn-in, or 0.
func (h *lib) burnable(p media.ProbeResult, want *int) int {
	if want == nil {
		return 0
	}
	for _, s := range p.Subs {
		if s.Index == *want && s.Index > 0 && !textSubCodecs[s.Codec] {
			return s.Index
		}
	}
	return 0
}

func (in playRequest) audioIndex() int {
	if in.AudioIndex != nil {
		return *in.AudioIndex
	}
	return -1
}

// startHLS starts the session for an HLS decision and fills out's url and
// startSec; false = the error response is written.
func (h *lib) startHLS(w http.ResponseWriter, r *http.Request, out map[string]any, fileID, itemID int64, input string, d media.Decision,
	in playRequest, durationMs int64) bool {
	sid, start, err := h.startSession(r, fileID, itemID, input, d, max(in.StartSec, 0), in.ReplaceSession, durationMs)
	switch {
	case errors.Is(err, errNoTranscoder):
		writeErr(w, r, http.StatusServiceUnavailable, "Перекодирование недоступно")
		return false
	case errors.Is(err, media.ErrUserLimit):
		writeErr(w, r, http.StatusTooManyRequests, "Этот профиль уже смотрит видео на другом устройстве. Закройте один из просмотров и попробуйте снова.")
		return false
	case errors.Is(err, media.ErrBusy):
		writeErr(w, r, http.StatusServiceUnavailable, "Сервер сейчас занят другими просмотрами. Попробуйте чуть позже.")
		return false
	case errors.Is(err, media.ErrNoSpace):
		writeErr(w, r, http.StatusInsufficientStorage, "На диске не хватает места, чтобы подготовить видео.")
		return false
	case err != nil:
		internalErr(w, r, "hls start", err)
		return false
	}
	out["url"] = "/api/hls/" + sid + "/index.m3u8"
	out["startSec"] = start
	// the keyed playlist (the app, AirPlay) lists the whole film from the start
	_, complete := h.HLS.CompletePlaylist(sid)
	out["hlsComplete"] = complete
	return true
}

// errNoTranscoder: the server has no HLS manager.
var errNoTranscoder = errors.New("api: no hls manager")

// startSession starts an HLS session of the request's profile for a
// decision, from start seconds (a copied video: the keyframe before it,
// returned as the start), replacing the profile's session replace. The
// session is the profile's own (hlsOwners), of library item itemID (0:
// none, a live stream).
func (h *lib) startSession(r *http.Request, fileID, itemID int64, input string, d media.Decision, start float64, replace string,
	durationMs int64) (string, float64, error) {
	if h.HLS == nil {
		return "", 0, errNoTranscoder
	}
	uid := userOf(r).ID
	ffStart := start
	if d.VideoCopy && start > 0 {
		// A copied video starts on the keyframe before the position: report
		// that as the start, or the player's clock runs ahead of the picture.
		// The file's keyframe index knows it without reading the media (and
		// a remux's complete playlist needs a start the index lists).
		indexed := false
		if ix, ok := h.HLS.(interface {
			IndexedKeyframe(input string, t float64) (float64, bool)
		}); ok {
			if k, ok := ix.IndexedKeyframe(input, start); ok && start-k < 60 {
				start, ffStart, indexed = k, k+keyframeLead, true
			}
		}
		if indexed {
			// found in the index
		} else if k, err := h.keyframe(r.Context(), input, start); err == nil && k <= start && start-k < 60 {
			start = k
			// ffmpeg seeks 3/23 s before -ss when the video has B-frames
			// (its DTS heuristic): aim a little after the keyframe so it
			// lands on it, not on the GOP before (checked with ffmpeg 7.1).
			ffStart = k + keyframeLead
		} else if err != nil && r.Context().Err() == nil {
			log.Printf("api: keyframe before %.1fs: %v", start, err)
		}
	}
	if replace != "" && !h.sessions.owns(replace, uid) {
		replace = ""
	}
	sid, err := h.HLS.Start(r.Context(), media.StartOptions{UserID: uid, FileID: fileID, Input: input, Decision: d,
		AudioIndex: -1, StartSec: ffStart, StreamStart: start, Replace: replace, DurationSec: float64(durationMs) / 1000})
	if replace != "" {
		h.sessions.remove(replace)
	}
	if err != nil {
		return "", 0, err
	}
	h.sessions.add(sid, uid, itemID)
	return sid, start, nil
}

// dropReplaced stops the session a direct-play reload replaces (the next
// part of a movie may play direct): it would hold a slot until it idles out.
func (h *lib) dropReplaced(r *http.Request, in playRequest) {
	if h.HLS != nil && in.ReplaceSession != "" && h.sessions.owns(in.ReplaceSession, userOf(r).ID) {
		h.HLS.Stop(in.ReplaceSession)
		h.sessions.remove(in.ReplaceSession)
	}
}

// keyframeLead is how far after the keyframe ffmpeg's -ss aims (> 3/23 s).
const keyframeLead = 0.2

// keyframe finds the keyframe at or before t (Deps.Keyframe in tests).
func (h *lib) keyframe(ctx context.Context, input string, t float64) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if h.Keyframe != nil {
		return h.Keyframe(ctx, input, t)
	}
	return media.KeyframeBefore(ctx, h.ffprobe(), input, t)
}

// fileTypes are the Content-Types of /api/stream (nosniff makes them
// mandatory): video, audio (audioserve.AudioTypes), books and comics.
var fileTypes = func() map[string]string {
	m := map[string]string{
		".mp4": "video/mp4", ".m4v": "video/mp4", ".mkv": "video/x-matroska", ".webm": "video/webm",
		".mov": "video/quicktime", ".ts": "video/mp2t", ".m2ts": "video/mp2t", ".avi": "video/x-msvideo",
		".epub": "application/epub+zip", ".fb2": "application/x-fictionbook+xml", ".zip": "application/zip",
		".pdf": "application/pdf", ".mobi": "application/x-mobipocket-ebook", ".azw3": "application/vnd.amazon.ebook",
		".cbz": "application/vnd.comicbook+zip", ".cbr": "application/vnd.comicbook-rar", ".cb7": "application/x-7z-compressed",
	}
	for ext, t := range audioserve.AudioTypes {
		m[ext] = t
	}
	return m
}()

// mediaKeyStreamKind: the kinds whose files a media key may stream (what
// AVPlayer and AirPlay fetch: video and audio). Books and comics need the
// Bearer token.
func mediaKeyStreamKind(kind string) bool {
	switch kind {
	case library.KindMovie, library.KindEpisode, library.KindTrack, library.KindAudiobook:
		return true
	}
	return false
}

// stream serves the file with Range support; download adds Content-Disposition.
func (h *lib) stream(download bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, it, ok := h.file(w, r)
		if !ok {
			return
		}
		if _, keyed := auth.ViaMediaKey(r.Context()); keyed && !mediaKeyStreamKind(it.Kind) {
			writeErr(w, r, http.StatusUnauthorized, "Нужно войти")
			return
		}
		if !h.screenAllowed(w, r, it.Kind) || (download && !h.screenDownloadAllowed(w, r, it.Kind)) {
			return
		}
		// a download of a kid without rules counts and stops like a stream
		serveFile(w, r, f, download, h.screenWrap(r, it))
	}
}

// serveFile serves a library file with Range support (the caller checked
// access); download adds Content-Disposition; wrap (optional) wraps the
// file the response reads (screenWrap).
func serveFile(w http.ResponseWriter, r *http.Request, f library.File, download bool, wrap func(io.ReadSeeker) io.ReadSeeker) {
	fh, err := os.Open(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, r, http.StatusNotFound, "Файл пропал с диска")
		return
	}
	if err != nil {
		internalErr(w, r, "open", err)
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		internalErr(w, r, "stat", err)
		return
	}
	if st.IsDir() { // a comic volume that is a folder of images
		writeErr(w, r, http.StatusNotFound, "Это папка, откройте том в читалке")
		return
	}
	name := baseName(f.Path)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// opened directly (a book's HTML/SVG/XML) it must not run in our origin
	w.Header().Set("Content-Security-Policy", "sandbox")
	if t, ok := fileTypes[strings.ToLower(filepath.Ext(name))]; ok {
		w.Header().Set("Content-Type", t)
	}
	if download {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	var content io.ReadSeeker = fh
	if wrap != nil {
		content = wrap(fh)
	}
	http.ServeContent(w, r, name, st.ModTime(), content)
}

func (h *lib) hlsFile(w http.ResponseWriter, r *http.Request) {
	id, name := chi.URLParam(r, "session"), chi.URLParam(r, "name")
	var uri func(string) string
	if key, ok := auth.ViaMediaKey(r.Context()); ok {
		// AVPlayer and AirPlay fetch the URIs inside without the app's
		// headers: each one carries the key.
		q := "mk=" + url.QueryEscape(key)
		uri = func(u string) string { return withQuery(u, q) }
	}
	if err := h.serveHLS(w, r, id, name, uri); err != nil {
		writeErr(w, r, http.StatusNotFound, "Сеанс не найден")
	}
}

// withQuery appends the query q to the URI u.
func withQuery(u, q string) string {
	if strings.Contains(u, "?") {
		return u + "&" + q
	}
	return u + "?" + q
}

// serveHLS serves the playlist (index.m3u8) or a segment of the request
// profile's session id. uri, when set, rewrites every URI of the playlist
// (a keyed playlist: AVPlayer and mpv fetch them without headers), and the
// playlist is then the complete VOD one when the session has it, so
// AVPlayer and Picture in Picture treat a transcode as a film, not as live;
// the web (nil) keeps ffmpeg's EVENT playlist. media.ErrNoSession: the
// session is not the profile's, or is gone; nothing is written then.
func (h *lib) serveHLS(w http.ResponseWriter, r *http.Request, id, name string, uri func(string) string) error {
	if h.HLS == nil || !h.sessions.owns(id, userOf(r).ID) {
		return media.ErrNoSession
	}
	// a session started before the limit (or the end of the hours) stops at it
	if ref := h.ScreenTime.Allow(r.Context(), userOf(r), screentime.Video); ref != nil {
		h.HLS.Stop(id)
		h.sessions.remove(id)
		writeScreenTime(w, ref)
		return nil
	}
	if name != "index.m3u8" {
		if item := h.sessions.item(id); item > 0 {
			h.screenSignal(r, library.Item{ID: item, Kind: library.KindMovie}, false)
		}
	}
	var p string
	var err error
	if name == "index.m3u8" {
		p, err = h.HLS.Playlist(id)
	} else {
		p, err = h.HLS.Segment(id, name)
	}
	switch {
	case errors.Is(err, media.ErrNoSession):
		h.sessions.remove(id) // ended by idle timeout
		return err
	case errors.Is(err, media.ErrNoSegment):
		writeErr(w, r, http.StatusNotFound, "Сеанс не найден")
		return nil
	case errors.Is(err, media.ErrNotReady):
		// ffmpeg is still on the first segment (a slow source, a 4K
		// transcode): the player asks again.
		w.Header().Set("Retry-After", "2")
		writeErr(w, r, http.StatusServiceUnavailable, "Видео ещё готовится")
		return nil
	case errors.Is(err, media.ErrSegmentGone):
		// Far behind the player: deleted to save disk; the player restarts there.
		writeErr(w, r, http.StatusGone, "Этот фрагмент уже удалён")
		return nil
	case err != nil:
		internalErr(w, r, "hls", err)
		return nil
	}
	if uri != nil && name == "index.m3u8" {
		playlist, complete := h.HLS.CompletePlaylist(id)
		if !complete {
			b, err := os.ReadFile(p)
			if err != nil {
				writeErr(w, r, http.StatusNotFound, "Сеанс не найден")
				return nil
			}
			playlist = string(b)
		}
		setHLSType(w, name)
		http.ServeContent(w, r, name, time.Time{}, strings.NewReader(rewritePlaylistURIs(playlist, uri)))
		return nil
	}
	fh, err := os.Open(p)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Сеанс не найден")
		return nil
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		internalErr(w, r, "hls stat", err)
		return nil
	}
	setHLSType(w, name)
	http.ServeContent(w, r, name, st.ModTime(), fh)
	return nil
}

// setHLSType sets the Content-Type of an HLS file (and no-cache on a playlist).
func setHLSType(w http.ResponseWriter, name string) {
	switch {
	case name == "index.m3u8":
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
	case filepath.Ext(name) == ".ts":
		w.Header().Set("Content-Type", "video/mp2t")
	default:
		w.Header().Set("Content-Type", "video/mp4")
	}
}

// playlistStart makes AVPlayer start a growing (EVENT) playlist at its
// beginning, not at the live edge.
const playlistStart = "#EXT-X-START:TIME-OFFSET=0,PRECISE=YES"

// uriAttr is a URI="…" attribute of an HLS tag (EXT-X-MAP's init.mp4).
var uriAttr = regexp.MustCompile(`URI="([^"]*)"`)

// rewritePlaylist adds the media key to every URI of an HLS playlist
// (rewritePlaylistURIs).
func rewritePlaylist(playlist, key string) string {
	q := "mk=" + url.QueryEscape(key)
	return rewritePlaylistURIs(playlist, func(u string) string { return withQuery(u, q) })
}

// rewritePlaylistURIs passes every URI of an HLS playlist (the segment
// lines and URI="…" attributes of tags) through withKey and adds an
// EXT-X-START after #EXTM3U unless the playlist has one. Comments and line
// endings are kept.
func rewritePlaylistURIs(playlist string, withKey func(string) string) string {
	hasStart := strings.Contains(playlist, "#EXT-X-START:")
	lines := strings.SplitAfter(playlist, "\n")
	var b strings.Builder
	b.Grow(len(playlist) + len(lines)*64 + len(playlistStart) + 2)
	for _, line := range lines {
		body := strings.TrimRight(line, "\r\n")
		eol := line[len(body):]
		switch {
		case strings.TrimSpace(body) == "":
		case strings.HasPrefix(body, "#EXT"):
			body = uriAttr.ReplaceAllStringFunc(body, func(m string) string {
				return `URI="` + withKey(m[len(`URI="`):len(m)-1]) + `"`
			})
		case strings.HasPrefix(body, "#"):
		default:
			body = withKey(body)
		}
		b.WriteString(body)
		b.WriteString(eol)
		if !hasStart && body == "#EXTM3U" {
			hasStart = true
			if eol == "" {
				eol = "\n"
				b.WriteString(eol)
				b.WriteString(playlistStart)
			} else {
				b.WriteString(playlistStart)
				b.WriteString(eol)
			}
		}
	}
	return b.String()
}

// hlsTouch is the player's heartbeat (every 20 s while the player is open):
// a paused player keeps its session, and learns how it is doing.
func (h *lib) hlsTouch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "session")
	if h.HLS == nil || !h.sessions.owns(id, userOf(r).ID) {
		writeErr(w, r, http.StatusNotFound, "Сеанс не найден")
		return
	}
	st, err := h.HLS.Touch(id)
	if err != nil {
		h.sessions.remove(id)
		writeErr(w, r, http.StatusNotFound, "Сеанс не найден")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *lib) hlsStop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "session")
	if h.HLS == nil || !h.sessions.owns(id, userOf(r).ID) {
		writeErr(w, r, http.StatusNotFound, "Сеанс не найден")
		return
	}
	h.HLS.Stop(id)
	h.sessions.remove(id)
	w.WriteHeader(http.StatusNoContent)
}

// hlsOwners binds HLS sessions to the profile that started them.
type hlsOwners struct {
	mu sync.Mutex
	m  map[string]hlsOwner
}

type hlsOwner struct {
	uid     int64
	item    int64 // the library item played (0: none)
	created time.Time
}

// ownerTTL bounds the map: sessions idle out in the manager long before this.
const ownerTTL = 24 * time.Hour

func (o *hlsOwners) add(id string, uid, item int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m == nil {
		o.m = map[string]hlsOwner{}
	}
	now := time.Now()
	for k, v := range o.m {
		if now.Sub(v.created) > ownerTTL {
			delete(o.m, k)
		}
	}
	o.m[id] = hlsOwner{uid, item, now}
}

// item is the library item session id plays (0: none, or unknown).
func (o *hlsOwners) item(id string) int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.m[id].item
}

func (o *hlsOwners) owns(id string, uid int64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	v, ok := o.m[id]
	return ok && v.uid == uid
}

func (o *hlsOwners) remove(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.m, id)
}

// --- subtitles ---

var textSubCodecs = media.TextSubCodecs

type extSub struct {
	path, lang, title, codec string
	forced                   bool
}

// externalSubs lists the subtitle files of f (subtitles.FindExternal: next
// to the video, Subs/ folders, Auto's DataDir/subs/ext), in a stable order
// so that indices hold between /api/play and /api/subs.
func externalSubs(f library.File, dataDir string) []extSub {
	var out []extSub
	for _, e := range subtitles.FindExternal(f.Path, dataDir, f.ID) {
		out = append(out, extSub{path: e.Path, lang: e.Lang, title: e.Title, codec: e.Codec, forced: e.Forced})
	}
	return out
}

// extSubID names an external subtitle file in its URL (?src=). Indices are
// positions in externalSubs, and a file written meanwhile (Auto puts
// <stem>.<lang>.srt next to the video, ahead of Subs/) shifts them; the id
// keeps a URL a player already holds pointing at the same file.
func extSubID(path string) string {
	h := fnv.New32a()
	h.Write([]byte(path))
	return fmt.Sprintf("%08x", h.Sum32())
}

// pickExtSub finds the external subtitle of a URL: by its id when the URL
// has one (the file at position i first, then any; gone: not found), else
// by position i (older URLs).
func pickExtSub(ext []extSub, i int, id string) (extSub, bool) {
	if id == "" {
		if i < len(ext) {
			return ext[i], true
		}
		return extSub{}, false
	}
	if i < len(ext) && extSubID(ext[i].path) == id {
		return ext[i], true
	}
	for _, s := range ext {
		if extSubID(s.path) == id {
			return s, true
		}
	}
	return extSub{}, false
}

// subInput prepares ffmpeg input options for an external text subtitle file.
// UTF-8 (with or without BOM) is read as is. UTF-16 (BOM) is decoded here and
// piped in as UTF-8, because ffmpeg's srt demuxer splits lines before
// recoding. Anything else is taken as CP1251 (old Russian .srt files).
// The file comes from outside: ffmpeg reads only it (file protocol) with
// the demuxer of its extension, so a playlist inside cannot fetch URLs.
func subInput(s extSub, b []byte) (args []string, stdin []byte) {
	demux := []string{}
	if f := media.SubDemuxer[s.codec]; f != "" {
		demux = []string{"-f", f}
	}
	file := append(append([]string{"-protocol_whitelist", "file"}, demux...), "-i", "file:"+s.path)
	switch subEncodingOf(b) {
	case subUTF16:
		return append(append([]string{"-protocol_whitelist", "pipe"}, demux...), "-i", "pipe:0"), []byte(subtitles.Decode(b))
	case subUTF8:
		return file, nil
	}
	return append([]string{"-sub_charenc", "CP1251"}, file...), nil
}

type subEncoding int

const (
	subUTF8   subEncoding = iota // with or without BOM: read as is
	subUTF16                     // BOM: decoded by subtitles.Decode before ffmpeg
	subCP1251                    // anything else (old Russian .srt files)
)

// subEncodingOf classifies an external subtitle file by its content.
func subEncodingOf(b []byte) subEncoding {
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}) || bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return subUTF16
	case utf8.Valid(bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})):
		return subUTF8
	}
	return subCP1251
}

// embeddedSubArgs are the ffmpeg input options extracting stream idx of a
// local video (file protocol only, see media.InputArgs).
func embeddedSubArgs(src string, idx int) []string {
	opts, in, err := media.InputArgs(src)
	if err != nil { // not a local path: never happens for library files
		opts, in = []string{"-protocol_whitelist", "file"}, "file:"+src
	}
	return append(opts, "-i", in, "-map", "0:"+strconv.Itoa(idx))
}

// subTracks lists embedded and external subtitles of f.
func subTracks(f library.File, p media.ProbeResult, dataDir string) []SubTrack {
	base := "/api/subs/" + strconv.FormatInt(f.ID, 10) + "/"
	out := []SubTrack{}
	for _, s := range p.Subs {
		t := SubTrack{Index: s.Index, Codec: s.Codec, Lang: s.Lang, Title: s.Title, Forced: s.Forced, Default: s.Default}
		if textSubCodecs[s.Codec] {
			t.URL = base + strconv.Itoa(s.Index) + ".vtt"
		} else {
			t.BurnIn = true // image-based (PGS, VobSub): would need burning into the video
		}
		out = append(out, t)
	}
	for i, s := range externalSubs(f, dataDir) {
		idx := externalSubBase + i
		out = append(out, SubTrack{Index: idx, Codec: s.codec, Lang: s.lang, Title: s.title, Forced: s.forced, External: true,
			URL: base + strconv.Itoa(idx) + ".vtt?src=" + extSubID(s.path)})
	}
	return out
}

func (h *lib) subtitle(w http.ResponseWriter, r *http.Request) {
	f, _, ok := h.file(w, r)
	if !ok {
		return
	}
	idx, err := strconv.Atoi(chi.URLParam(r, "index"))
	if err != nil || idx < 0 {
		writeErr(w, r, http.StatusBadRequest, "Неверный номер субтитров")
		return
	}
	h.serveSubtitle(w, r, f, idx, r.URL.Query().Get("src"))
}

// serveSubtitle answers with the WebVTT of subtitle stream idx of f (the
// caller checked access): embedded (the ffprobe index) or external
// (externalSubBase + its position; srcID: extSubID of the file, "" = by
// position).
func (h *lib) serveSubtitle(w http.ResponseWriter, r *http.Request, f library.File, idx int, srcID string) {
	var src string
	var args []string
	var stdin []byte
	if idx >= externalSubBase {
		es, ok := pickExtSub(externalSubs(f, h.DataDir), idx-externalSubBase, srcID)
		if !ok {
			writeErr(w, r, http.StatusNotFound, "Субтитры не найдены")
			return
		}
		src = es.path
		b, err := os.ReadFile(src)
		if err != nil {
			writeErr(w, r, http.StatusNotFound, "Субтитры не найдены")
			return
		}
		args, stdin = subInput(es, b)
		args = append(args, "-map", "0:s:0")
	} else {
		p, err := h.probe(r.Context(), f)
		if err != nil {
			internalErr(w, r, "probe "+f.Path, err)
			return
		}
		var found *media.SubStream
		for i := range p.Subs {
			if p.Subs[i].Index == idx {
				found = &p.Subs[i]
			}
		}
		if found == nil {
			writeErr(w, r, http.StatusNotFound, "Субтитры не найдены")
			return
		}
		if !textSubCodecs[found.Codec] {
			writeErr(w, r, http.StatusUnsupportedMediaType, "Эти субтитры — картинки, их можно только вшить в видео")
			return
		}
		src = f.Path
		args = embeddedSubArgs(src, idx)
	}
	st, err := os.Stat(src)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Субтитры не найдены")
		return
	}
	dir := filepath.Join(h.DataDir, "subs")
	out := subCachePath(h.DataDir, f.ID, idx, src, st)
	if _, err := os.Stat(out); err != nil {
		// Extracting an embedded track reads the whole file (minutes for a
		// remux on the USB disk). It runs detached: a player reload or a
		// proxy timeout does not throw the work away; the client asks again.
		// a prefetch of the file's tracks may be reading it already: join it
		if h.subJobs.running(prefetchKey(f.ID)) {
			_, err := h.subJobs.do(r.Context(), prefetchKey(f.ID), subtitleWait, 45*time.Minute, func(context.Context) (struct{}, error) { return struct{}{}, nil })
			if err == nil {
				if _, serr := os.Stat(out); serr == nil {
					w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
					http.ServeFile(w, r, out)
					return
				}
			} else if errors.Is(err, errStillWorking) {
				if servePartialSub(w, out) {
					return
				}
				w.Header().Set("Retry-After", "5")
				writeErr(w, r, http.StatusServiceUnavailable, "Субтитры ещё готовятся")
				return
			} else if r.Context().Err() != nil {
				return
			}
		}
		_, err := h.subJobs.do(r.Context(), out, subtitleWait, 30*time.Minute, func(ctx context.Context) (struct{}, error) {
			if _, err := os.Stat(out); err == nil {
				return struct{}{}, nil
			}
			return struct{}{}, h.convertSub(ctx, args, stdin, dir, out)
		})
		switch {
		case errors.Is(err, errStillWorking):
			if servePartialSub(w, out) {
				return
			}
			w.Header().Set("Retry-After", "3")
			writeErr(w, r, http.StatusServiceUnavailable, "Субтитры ещё готовятся")
			return
		case r.Context().Err() != nil:
			return
		case err != nil:
			internalErr(w, r, "subtitle "+src, err)
			return
		}
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	http.ServeFile(w, r, out)
}

// subtitleWait is how long a subtitle request waits for the extraction
// before answering 503 (under the reverse proxy's 60 s read timeout).
var subtitleWait = 40 * time.Second

// convertSub runs ffmpeg to produce out (WebVTT) atomically; stdin feeds "pipe:0" inputs.
func (h *lib) convertSub(ctx context.Context, args []string, stdin []byte, dir, out string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := out + ".tmp"
	full := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)
	// every cue reaches the disk at once: a request meanwhile serves what is there
	full = append(full, "-c:s", "webvtt", "-flush_packets", "1", "-f", "webvtt", tmp)
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, h.ffmpeg(), full...)
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return os.Rename(tmp, out)
}

// subCachePath is where the WebVTT of stream idx of src is kept. The
// source's path is in the name: external indices shift when a subtitle file
// is added.
func subCachePath(dataDir string, fileID int64, idx int, src string, st os.FileInfo) string {
	sum := fnv.New32a()
	sum.Write([]byte(src))
	return filepath.Join(dataDir, "subs", fmt.Sprintf("%d-%d-%08x-%d-%d.vtt", fileID, idx, sum.Sum32(), st.Size(), st.ModTime().Unix()))
}

func prefetchKey(fileID int64) string { return "all:" + strconv.FormatInt(fileID, 10) }

// prefetchSubs extracts every embedded text track of f that is not cached
// in ONE pass over the file: a remux holds the tracks interleaved, so each
// extraction reads all of it (minutes on the USB disk), and one read per
// track would multiply that. It runs detached; /api/subs joins it and,
// meanwhile, serves the cues written so far (servePartialSub).
//
// Playback from startSec (a resume) would wait for the pass to read up to
// there: a short pass from just before startSec goes first (<out>.from),
// and the partial answer takes its cues past where the full pass is.
func (h *lib) prefetchSubs(f library.File, p media.ProbeResult, startSec float64) {
	st, err := os.Stat(f.Path)
	if err != nil {
		return
	}
	type job struct {
		idx int
		out string
	}
	var todo []job
	for _, s := range p.Subs {
		if !textSubCodecs[s.Codec] {
			continue
		}
		out := subCachePath(h.DataDir, f.ID, s.Index, f.Path, st)
		if _, err := os.Stat(out); err != nil {
			todo = append(todo, job{s.Index, out})
		}
	}
	if len(todo) == 0 || h.subJobs.running(prefetchKey(f.ID)) {
		return
	}
	h.subJobs.do(context.Background(), prefetchKey(f.ID), 0, 45*time.Minute, func(ctx context.Context) (struct{}, error) {
		if err := os.MkdirAll(filepath.Join(h.DataDir, "subs"), 0o755); err != nil {
			return struct{}{}, err
		}
		in := embeddedSubArgs(f.Path, todo[0].idx)
		in = in[:len(in)-2] // without -map
		pass := func(seek []string, suffix string) error {
			args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, seek...)
			args = append(args, in...)
			for _, j := range todo {
				args = append(args, "-map", "0:"+strconv.Itoa(j.idx), "-c:s", "webvtt", "-flush_packets", "1", "-f", "webvtt", j.out+suffix+".tmp")
			}
			var stderr bytes.Buffer
			cmd := exec.CommandContext(ctx, h.ffmpeg(), args...)
			cmd.Stderr = &stderr
			if err := runYielding(cmd, h.ioBusy, yieldEvery); err != nil {
				for _, j := range todo {
					os.Remove(j.out + suffix + ".tmp")
				}
				log.Printf("api: subtitle prefetch %s: %v: %s", f.Path, err, strings.TrimSpace(stderr.String()))
				return err
			}
			for _, j := range todo {
				os.Rename(j.out+suffix+".tmp", j.out+suffix)
			}
			return nil
		}
		if from := startSec - subLeadIn; from > subLeadIn {
			// the original timestamps (from the file's start), as the full pass writes them
			// only the next quarter hour: the full pass brings the rest
			pass([]string{"-ss", strconv.FormatFloat(from, 'f', 3, 64), "-t", strconv.Itoa(subFromWindow), "-copyts", "-start_at_zero"}, subFromSuffix)
		}
		err := pass(nil, "")
		for _, j := range todo {
			os.Remove(j.out + subFromSuffix)
		}
		return struct{}{}, err
	})
}

// subLeadIn is how far before the playback start the quick pass begins.
const subLeadIn = 30.0

// subFromWindow: the seconds the quick pass reads (it read to the end of the
// file, tens of gigabytes, before the full pass read them again).
const subFromWindow = 900

// yieldEvery: how often a subtitle extraction checks whether playback
// needs the disk (runYielding).
const yieldEvery = 250 * time.Millisecond

func (h *lib) ioBusy() bool { return h.HLS != nil && h.HLS.IOBusy() }

// runYielding runs cmd, holding it stopped while playback needs the media
// disk (media.RunYielding).
var runYielding = media.RunYielding

// subFromSuffix names the quick pass's file (<out>.from, its own .tmp while written).
const subFromSuffix = ".from"

// servePartialSub answers with the cues extracted so far for out (the full
// pass's .tmp, then the quick pass's cues after them), marked partial: the
// client asks again until the answer is whole. False: nothing yet.
func servePartialSub(w http.ResponseWriter, out string) bool {
	body := partialSub(out)
	if body == nil {
		return false
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Vault-Partial", "1")
	w.Header().Set("Retry-After", "10")
	w.Write(body)
	return true
}

// partialSub joins the complete cues of the passes still being written
// (nil when there are none).
func partialSub(out string) []byte {
	head, _ := os.ReadFile(out + ".tmp")
	cues := vttCues(head)
	last := -1.0
	if n := len(cues); n > 0 {
		last = cues[n-1].start
	}
	from, err := os.ReadFile(out + subFromSuffix)
	if err != nil {
		from, _ = os.ReadFile(out + subFromSuffix + ".tmp")
	}
	for _, c := range vttCues(from) {
		if c.start > last {
			cues = append(cues, c)
		}
	}
	if len(cues) == 0 {
		return nil
	}
	var b bytes.Buffer
	b.WriteString("WEBVTT\n\n")
	for _, c := range cues {
		b.WriteString(c.block)
		b.WriteString("\n\n")
	}
	return b.Bytes()
}

type vttCue struct {
	start float64
	block string // the timing line and the text
}

// vttCues are the complete cues of ffmpeg's WebVTT output (a cue is
// complete once the blank line after it is written).
func vttCues(b []byte) []vttCue {
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	end := strings.LastIndex(text, "\n\n")
	if end < 0 {
		return nil
	}
	var out []vttCue
	for _, block := range strings.Split(text[:end], "\n\n") {
		block = strings.Trim(block, "\n")
		timing, _, _ := strings.Cut(block, "\n")
		at, _, ok := strings.Cut(timing, " --> ")
		if !ok {
			continue // the WEBVTT header
		}
		if start, ok := vttTime(strings.TrimSpace(at)); ok {
			out = append(out, vttCue{start, block})
		}
	}
	return out
}

// vttTime reads [hh:]mm:ss.ttt.
func vttTime(s string) (float64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	sec := 0.0
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0, false
		}
		sec = sec*60 + v
	}
	return sec, true
}
