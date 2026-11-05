package api

import (
	"context"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"

	"github.com/antresol-app/antresol/internal/audioserve"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/go-chi/chi/v5"
)

// defaultAudioLimit is how many audio transcodes run at once (separate
// from the HLS slots).
const defaultAudioLimit = 4

func mountAudio(r chi.Router, h *lib) {
	r.Get("/api/tracks/{id}/stream", h.trackStream)
	r.Get("/api/audio/{fileId}", h.audioFile)
}

// audioDeps: the remote rule of playbackCaps and «Музыка вне дома».
func (h *lib) audioDeps() audioserve.Deps {
	return audioserve.Deps{
		Transcoder: h.AudioTranscoder,
		IsRemote: func(r *http.Request) bool {
			if h.ClientIsRemote != nil {
				return h.ClientIsRemote(r)
			}
			return !h.Network.IsTrusted(r)
		},
		RemoteKbps: h.remoteMusicKbps,
	}
}

func (h *lib) remoteMusicKbps(ctx context.Context) int {
	if h.DB == nil {
		return AppSettings{}.RemoteMusicKbps()
	}
	s, err := LoadAppSettings(ctx, h.DB, h.SettingsDefaults)
	if err != nil {
		log.Printf("api: settings for remote music: %v", err)
	}
	return s.RemoteMusicKbps()
}

// audioQuery reads accept/maxBitRate/format into req and returns the
// offset t (seconds, float) in ms.
func audioQuery(w http.ResponseWriter, r *http.Request, req *media.AudioRequest) (int64, bool) {
	q := r.URL.Query()
	req.Lang = i18n.FromContext(r.Context()) // the language of AudioPlan.Reason
	if err := audioserve.ParseQuery(q, req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err.Error())
		return 0, false
	}
	var off int64
	if s := q.Get("t"); s != "" {
		t, err := strconv.ParseFloat(s, 64)
		if err != nil || t < 0 || math.IsNaN(t) || math.IsInf(t, 0) || t > 1e7 {
			writeErr(w, r, http.StatusBadRequest, "Неверная позиция")
			return 0, false
		}
		off = int64(math.Round(t * 1000))
	}
	return off, true
}

// trackStream is GET /api/tracks/{id}/stream: a track (a CUE track is cut
// from its image); t moves the start of a transcode within the track.
func (h *lib) trackStream(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	tr, err := h.Library.TrackByID(r.Context(), id)
	if errors.Is(err, library.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	}
	if err != nil {
		internalErr(w, r, "track", err)
		return
	}
	it, ok := h.item(w, r, tr.ID) // the album/artist visibility rules
	if !ok || !h.screenAllowed(w, r, it.Kind) {
		return
	}
	f, err := h.Library.FileByID(r.Context(), tr.FileID)
	if errors.Is(err, library.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Файл не найден")
		return
	}
	if err != nil {
		internalErr(w, r, "track file", err)
		return
	}
	req := audioserve.TrackRequest(tr, f)
	off, ok := audioQuery(w, r, &req)
	if !ok {
		return
	}
	d := h.audioDeps()
	d.Meter = h.audioMeter(r, it)
	audioserve.Serve(w, r, d, f, tr.StartMs+off, tr.EndMs, req)
}

// audioFile is GET /api/audio/{fileId}: an audiobook part or any library
// audio file, from t seconds when transcoded.
func (h *lib) audioFile(w http.ResponseWriter, r *http.Request) {
	f, it, ok := h.file(w, r)
	if !ok || !h.screenAllowed(w, r, it.Kind) {
		return
	}
	req := audioserve.FileRequest(f)
	off, ok := audioQuery(w, r, &req)
	if !ok {
		return
	}
	d := h.audioDeps()
	d.Meter = h.audioMeter(r, it)
	audioserve.Serve(w, r, d, f, off, nil, req)
}
