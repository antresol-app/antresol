// Package audioserve sends a library audio file to a player: the original
// with Range (at home, or when the client takes it) or an ffmpeg transcode
// (outside the home network by «Музыка вне дома», on an explicit limit or
// format, and for CUE tracks cut from an image). It is shared by the web
// API and Subsonic, so it depends on neither.
package audioserve

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// Deps are what Serve needs from its caller.
type Deps struct {
	Transcoder *media.AudioTranscoder
	// IsRemote reports a client outside the home network (nil: at home).
	IsRemote func(*http.Request) bool
	// RemoteKbps is «Музыка вне дома» (0 = original); nil: no limit.
	RemoteKbps func(ctx context.Context) int
	// WriteIdle is how long a transcode waits for a client that stopped
	// reading (a paused player with a full buffer) before it gives its
	// slot back; 0: DefaultWriteIdle, negative: no limit.
	WriteIdle time.Duration
	// Meter (optional) wraps the response given the stream's byte rate (a
	// kid's screen time counts what is sent and ends it at the limit);
	// done runs when the response ends. A nil writer: w as is.
	Meter func(w http.ResponseWriter, bytesPerSec float64) (mw http.ResponseWriter, done func())
}

// fallbackKbps is the rate assumed when nothing says (128 kbit/s).
const fallbackKbps = 128

// meter wraps w by d.Meter at kbps (none: w, a no-op).
func (d Deps) meter(w http.ResponseWriter, kbps int) (http.ResponseWriter, func()) {
	if d.Meter == nil {
		return w, func() {}
	}
	if kbps <= 0 {
		kbps = fallbackKbps
	}
	mw, done := d.Meter(w, float64(kbps)*125)
	if mw == nil {
		return w, func() {}
	}
	return mw, done
}

// DefaultWriteIdle frees a transcode slot a minute after the client stops
// reading: the slots are few (4) and shared by every player.
const DefaultWriteIdle = 60 * time.Second

// AudioTypes are the Content-Types of audio files served as is (nosniff
// makes them mandatory).
var AudioTypes = map[string]string{
	".mp3": "audio/mpeg", ".flac": "audio/flac",
	".m4a": "audio/mp4", ".m4b": "audio/mp4", ".aac": "audio/aac", // bare .aac is ADTS
	".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg",
	".wav": "audio/wav",
}

// extCodecs: the codec of an audiobook (or other untagged) file by extension.
var extCodecs = map[string]string{
	".m4b": "aac", ".m4a": "aac", ".mp3": "mp3", ".flac": "flac",
	".ogg": "vorbis", ".oga": "vorbis", ".opus": "opus",
	".wav": "wav", ".aiff": "aiff", ".aif": "aiff",
}

var losslessCodecs = map[string]bool{"flac": true, "alac": true, "wav": true, "aiff": true, "ape": true, "wv": true}

// bitrateKbps is the source bitrate: the cached probe's, else size over
// duration, else 0.
func bitrateKbps(f library.File) int {
	var p media.ProbeResult
	if f.ProbeJSON != "" && json.Unmarshal([]byte(f.ProbeJSON), &p) == nil && len(p.Audio) > 0 && p.Audio[0].BitRate > 0 {
		return int(p.Audio[0].BitRate / 1000)
	}
	if f.Size > 0 && f.DurationMs > 0 {
		return int(f.Size * 8 / f.DurationMs) // bit/ms = kbit/s
	}
	return 0
}

// FileRequest describes an audio file (an audiobook part, any library
// audio file) by its extension and cached probe.
func FileRequest(f library.File) media.AudioRequest {
	codec := extCodecs[strings.ToLower(filepath.Ext(f.Path))]
	if codec == "" {
		var p media.ProbeResult
		if f.ProbeJSON != "" && json.Unmarshal([]byte(f.ProbeJSON), &p) == nil && len(p.Audio) > 0 {
			codec = strings.ToLower(p.Audio[0].Codec)
		}
	}
	lossless := losslessCodecs[codec] || strings.HasPrefix(codec, "pcm_")
	return media.AudioRequest{Codec: codec, Lossless: lossless, BitrateKbps: bitrateKbps(f)}
}

// TrackRequest describes a music track: codec and lossless from the
// tracks row; a CUE track (a part of its file) is a segment.
func TrackRequest(tr library.Track, f library.File) media.AudioRequest {
	return media.AudioRequest{Codec: tr.Codec, Lossless: tr.Lossless, BitrateKbps: bitrateKbps(f),
		Segment: tr.StartMs > 0 || tr.EndMs != nil}
}

// ParseQuery reads accept (comma list), maxBitRate (kbit/s, 0 = no limit)
// and format (raw|aac|mp3|opus) into req. Errors are in Russian (catalog
// keys: the API's writeErr localizes them).
func ParseQuery(q url.Values, req *media.AudioRequest) error {
	if a := q.Get("accept"); a != "" {
		req.Accept = nil
		for _, c := range strings.Split(a, ",") {
			if c = strings.ToLower(strings.TrimSpace(c)); c != "" {
				req.Accept = append(req.Accept, c)
			}
		}
	}
	if s := q.Get("maxBitRate"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return errors.New("Неверный maxBitRate")
		}
		req.MaxKbps, req.MaxKbpsSet = n, true
	}
	switch f := strings.ToLower(q.Get("format")); f {
	case "", "raw", "aac", "mp3", "opus":
		req.Format = f
	default:
		return errors.New("Неизвестный формат")
	}
	return nil
}

// writeErr answers {"error": msg}, msg (a Russian catalog key) in the
// request language.
func writeErr(w http.ResponseWriter, r *http.Request, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": i18n.T(r.Context(), msg)})
}

// started records whether anything reached the client, and moves the
// write deadline forward before every write: a write blocked longer than
// idle fails, ffmpeg stops and the slot frees.
type started struct {
	http.ResponseWriter
	n    int64
	rc   *http.ResponseController
	idle time.Duration
}

func (s *started) Write(p []byte) (int, error) {
	if s.rc != nil && s.idle > 0 {
		// ErrNotSupported (a writer without deadlines): no watchdog
		_ = s.rc.SetWriteDeadline(time.Now().Add(s.idle))
	}
	n, err := s.ResponseWriter.Write(p)
	s.n += int64(n)
	return n, err
}

// transcodeHeaders are the headers of a transcode; dropped when it fails
// before the first byte.
var transcodeHeaders = []string{"Content-Type", "X-Content-Duration", "Accept-Ranges", "Cache-Control", "X-Vault-Transcode"}

// Serve sends f from startMs (to endMs when set) as req allows. Without an
// explicit limit (req.MaxKbpsSet) a remote client gets «Музыка вне дома». Direct play
// is the whole file with Range; a transcode streams without Range and says
// its length in X-Content-Duration. startMs is ignored by direct play.
func Serve(w http.ResponseWriter, r *http.Request, d Deps, f library.File, startMs int64, endMs *int64, req media.AudioRequest) {
	if !req.MaxKbpsSet && d.IsRemote != nil && d.RemoteKbps != nil && d.IsRemote(r) {
		req.MaxKbps = d.RemoteKbps(r.Context())
	}
	if req.Lang == "" {
		req.Lang = i18n.FromContext(r.Context()) // the plan's Reason
	}
	plan := media.AudioDecide(req)
	if plan.Direct {
		mw, done := d.meter(w, bitrateKbps(f))
		defer done()
		serveFile(mw, r, f)
		return
	}
	startMs = max(startMs, 0)
	end := f.DurationMs
	if endMs != nil {
		end = *endMs
	}
	if (endMs != nil || end > 0) && startMs >= end {
		writeErr(w, r, http.StatusRequestedRangeNotSatisfiable, "Позиция за концом трека")
		return
	}
	if _, err := os.Stat(f.Path); errors.Is(err, os.ErrNotExist) {
		writeErr(w, r, http.StatusNotFound, "Файл пропал с диска")
		return
	}
	if d.Transcoder == nil {
		log.Printf("audioserve: no transcoder for file %d", f.ID)
		writeErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
		return
	}
	h := w.Header()
	h.Set("Content-Type", plan.ContentType)
	if end > startMs {
		h.Set("X-Content-Duration", strconv.FormatFloat(float64(end-startMs)/1000, 'f', -1, 64))
	}
	h.Set("Accept-Ranges", "none")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Vault-Transcode", "1")
	idle := d.WriteIdle
	if idle == 0 {
		idle = DefaultWriteIdle
	}
	kbps := plan.Kbps
	if kbps <= 0 { // flac, copy: the source's rate
		kbps = bitrateKbps(f)
	}
	mw, done := d.meter(w, kbps)
	defer done()
	rc := http.NewResponseController(mw)
	out := &started{ResponseWriter: mw, rc: rc, idle: idle}
	err := d.Transcoder.Stream(r.Context(), out, f.Path, plan, startMs, endMs)
	if idle > 0 {
		_ = rc.SetWriteDeadline(time.Time{}) // the error reply below must not inherit it
	}
	if err == nil || out.n > 0 || r.Context().Err() != nil {
		if err != nil && r.Context().Err() == nil {
			log.Printf("audioserve: file %d: %v", f.ID, err)
			// Bytes are out, so no error reply is possible. Break the
			// chunked body instead of ending it: a transcode has no length,
			// and a client saving it would keep a truncated file.
			panic(http.ErrAbortHandler)
		}
		return
	}
	for _, k := range transcodeHeaders {
		h.Del(k)
	}
	if errors.Is(err, media.ErrAudioBusy) {
		h.Set("Retry-After", "2")
		writeErr(w, r, http.StatusServiceUnavailable, "Сервер занят, попробуйте через пару секунд")
		return
	}
	log.Printf("audioserve: file %d: %v", f.ID, err)
	writeErr(w, r, http.StatusInternalServerError, "Не удалось перекодировать аудио")
}

// serveFile sends the original with Range (Safari and iOS seek with it).
func serveFile(w http.ResponseWriter, r *http.Request, f library.File) {
	fh, err := os.Open(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, r, http.StatusNotFound, "Файл пропал с диска")
		return
	}
	if err != nil {
		log.Printf("audioserve: open file %d: %v", f.ID, err)
		writeErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil || st.IsDir() {
		writeErr(w, r, http.StatusNotFound, "Файл не найден")
		return
	}
	name := filepath.Base(f.Path)
	ct, ok := AudioTypes[strings.ToLower(filepath.Ext(name))]
	if !ok {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, name, st.ModTime(), fh)
}
