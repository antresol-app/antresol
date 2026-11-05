package api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/screentime"
	"github.com/antresol-app/antresol/internal/subtitles"
	"github.com/go-chi/chi/v5"
)

// Device copies: MP4 files the native app downloads (with its Bearer
// token) to play offline. The quality resolves like /api/play: «Авто» at
// home is the original, outside it is «Качество вне дома».
func mountDeviceCopies(r chi.Router, h *lib) {
	r.Post("/api/device-copies", h.deviceCopyRequest)
	r.Get("/api/device-copies/{id}", h.deviceCopyStatus)
	r.Get("/api/device-copies/{id}/file", h.deviceCopyFile)
}

type deviceCopyRequest struct {
	FileID     int64  `json:"fileId"`
	Quality    string `json:"quality"` // auto | original | 1080 | 720 | 480
	AudioIndex *int   `json:"audioIndex"`
	// AudioSrc: the id of the external audio track AudioIndex (2000+) names.
	AudioSrc string `json:"audioSrc"`
}

func (h *lib) deviceCopyRequest(w http.ResponseWriter, r *http.Request) {
	if h.DeviceCopies == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "Перекодирование недоступно")
		return
	}
	var in deviceCopyRequest
	if !decode(w, r, &in) {
		return
	}
	// playbackCaps takes anything unknown for «Авто»: a copy is checked first
	if _, ok := qualityCaps[in.Quality]; !ok && in.Quality != qualityAuto && in.Quality != qualityOriginal {
		writeErr(w, r, http.StatusBadRequest, "Неизвестное качество")
		return
	}
	f, it, ok := h.fileByID(w, r, in.FileID)
	if !ok {
		return
	}
	// a copy plays offline, out of the clock's reach: not for a kid with rules
	if c, ok := screentime.CategoryOf(it.Kind); ok && h.ScreenTime.Restricted(r.Context(), userOf(r), c) {
		writeScreenTime(w, &screentime.Refusal{Reason: "download", Message: i18n.T(r.Context(), copyOff)})
		return
	}
	st, err := os.Stat(f.Path)
	if err != nil || st.IsDir() {
		writeErr(w, r, http.StatusNotFound, "Файл пропал с диска")
		return
	}
	p, err := h.probe(r.Context(), f)
	if err != nil {
		internalErr(w, r, "probe "+f.Path, err)
		return
	}
	caps, q := h.playbackCaps(r, media.ClientCaps{}, in.Quality)
	audio := -1
	if in.AudioIndex != nil {
		audio = *in.AudioIndex
	}
	var extA *extAudio
	if audio >= externalAudioBase {
		// one gone meanwhile: the copy of the default track
		if e, ok := pickExtAudio(h.externalAudio(r.Context(), f, p.DurationMs, p.StartMs), audio-externalAudioBase, in.AudioSrc); ok {
			extA = &e
		} else {
			audio = -1
		}
	} else if in.AudioIndex == nil {
		// the copy's own choice: the cleaned version of the default track
		// for a profile that prefers it
		ext := h.externalAudio(r.Context(), f, p.DurationMs, p.StartMs)
		if k := h.preferredCleaned(r.Context(), userOf(r).ID, media.PlanDeviceCopy(p, nil, 0, 0, -1).AudioIndex, ext); k >= 0 {
			extA, audio = &ext[k], externalAudioBase+k
		}
	}
	var paths []string
	var subs []extSub
	for _, s := range externalSubs(f, h.DataDir) {
		if media.SubDemuxer[s.codec] != "" {
			subs = append(subs, s)
			paths = append(paths, s.path)
		}
	}
	key := media.DeviceCopyKey{FileID: f.ID, Size: st.Size(), ModTime: st.ModTime(), MaxHeight: caps.MaxHeight,
		MaxBitrate: caps.MaxBitrate, AudioIndex: audio, ExtSubsHash: media.ExternalSubsHash(paths)}
	if extA != nil {
		key.ExtAudioHash = extA.copyHash()
	}
	ext, err := h.copySubs(key.ID(), subs)
	if err != nil {
		internalErr(w, r, "device copy subtitles", err)
		return
	}
	pl := media.PlanDeviceCopy(p, ext, caps.MaxHeight, caps.MaxBitrate, audio)
	if extA != nil {
		pl = media.PlanDeviceCopyExternalAudio(p, ext, caps.MaxHeight, caps.MaxBitrate, extA.media(), i18n.FromContext(r.Context()))
	}
	cs, err := h.DeviceCopies.Request(r.Context(), userOf(r).ID, key, f.Path, pl)
	if err != nil || cs.State == media.CopyReady {
		// no job reads the decoded subtitles (a running one wrote the same files)
		for _, e := range ext {
			if filepath.Dir(e.Path) == h.DeviceCopies.SubsDir() {
				os.Remove(e.Path)
			}
		}
	}
	switch {
	case errors.Is(err, media.ErrTooMany):
		writeErr(w, r, http.StatusTooManyRequests, "Слишком много копий в очереди. Дождитесь готовых.")
		return
	case errors.Is(err, media.ErrNoSpace):
		writeErr(w, r, http.StatusInsufficientStorage, "На сервере не хватает места для копии")
		return
	case err != nil:
		internalErr(w, r, "device copy", err)
		return
	}
	cs.Quality = media.QualityInfo{Selected: q.Selected, Applied: q.Applied, Remote: q.Remote}
	code := http.StatusAccepted
	if cs.State == media.CopyReady {
		code = http.StatusOK
	}
	writeJSON(w, code, localizedCopy(r, cs))
}

// copySubs turns the external subtitles into copy inputs. A UTF-16 file
// is decoded into a UTF-8 <SubsDir>/<id>-<k><ext> (ffmpeg's demuxers split
// lines before recoding, and only one input could use stdin); a file that
// is not UTF-8 is read as CP1251.
func (h *lib) copySubs(id string, subs []extSub) ([]media.ExternalSub, error) {
	var out []media.ExternalSub
	for k, s := range subs {
		b, err := os.ReadFile(s.path)
		if err != nil {
			continue // gone meanwhile: the copy goes without it
		}
		e := media.ExternalSub{Path: s.path, Lang: s.lang, Codec: s.codec}
		switch subEncodingOf(b) {
		case subUTF16:
			dir := h.DeviceCopies.SubsDir()
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
			p := filepath.Join(dir, id+"-"+strconv.Itoa(k)+filepath.Ext(s.path))
			if err := os.WriteFile(p+".tmp", []byte(subtitles.Decode(b)), 0o644); err != nil {
				return nil, err
			}
			if err := os.Rename(p+".tmp", p); err != nil {
				return nil, err
			}
			e.Path = p
		case subCP1251:
			e.CP1251 = true
		}
		out = append(out, e)
	}
	return out, nil
}

// copyVisible loads the status of copy {id} if the user sees the item of
// the file it was made from; otherwise it writes 404 and returns false.
func (h *lib) copyVisible(w http.ResponseWriter, r *http.Request) (media.CopyStatus, bool) {
	if h.DeviceCopies == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "Перекодирование недоступно")
		return media.CopyStatus{}, false
	}
	id := chi.URLParam(r, "id")
	if !media.IsCopyID(id) {
		writeErr(w, r, http.StatusNotFound, "Копия не найдена")
		return media.CopyStatus{}, false
	}
	cs, err := h.DeviceCopies.Status(id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Копия не найдена")
		return cs, false
	}
	f, err := h.Library.FileByID(r.Context(), cs.FileID)
	if errors.Is(err, library.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Копия не найдена")
		return cs, false
	}
	if err != nil {
		internalErr(w, r, "device copy file", err)
		return cs, false
	}
	it, err := h.Library.Item(r.Context(), f.ItemID)
	if err == nil {
		var ok bool
		if ok, err = h.allowed(r.Context(), userOf(r), it); err == nil && !ok {
			err = library.ErrNotFound
		}
	}
	if errors.Is(err, library.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Копия не найдена") // do not reveal hidden items
		return cs, false
	}
	if err != nil {
		internalErr(w, r, "device copy item", err)
		return cs, false
	}
	return cs, true
}

func (h *lib) deviceCopyStatus(w http.ResponseWriter, r *http.Request) {
	cs, ok := h.copyVisible(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, localizedCopy(r, cs))
}

func (h *lib) deviceCopyFile(w http.ResponseWriter, r *http.Request) {
	cs, ok := h.copyVisible(w, r)
	if !ok {
		return
	}
	// copies are shared by file (copyVisible checked the profile may see
	// it): a kid's fetch is refused like a new copy, else counted like a stream
	var it library.Item
	if f, err := h.Library.FileByID(r.Context(), cs.FileID); err == nil {
		it, _ = h.Library.Item(r.Context(), f.ItemID)
	}
	if c, ok := screentime.CategoryOf(it.Kind); ok && h.ScreenTime.Restricted(r.Context(), userOf(r), c) {
		writeScreenTime(w, &screentime.Refusal{Reason: "download", Message: i18n.T(r.Context(), copyOff)})
		return
	}
	if !h.screenAllowed(w, r, it.Kind) {
		return
	}
	p, _, err := h.DeviceCopies.Path(cs.ID)
	switch {
	case errors.Is(err, media.ErrNotReady):
		writeErr(w, r, http.StatusConflict, "Копия ещё готовится")
		return
	case err != nil:
		writeErr(w, r, http.StatusNotFound, "Копия не найдена")
		return
	}
	fh, err := os.Open(p)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Копия не найдена")
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		internalErr(w, r, "device copy stat", err)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	var content io.ReadSeeker = fh
	if wrap := h.screenWrap(r, it); wrap != nil {
		content = wrap(fh)
	}
	http.ServeContent(w, r, cs.ID+".mp4", st.ModTime(), content)
}

// localizedCopy: a failed copy's error, made in Russian by the copy job,
// in the request language.
func localizedCopy(r *http.Request, cs media.CopyStatus) media.CopyStatus {
	cs.Error = i18n.Localize(r.Context(), cs.Error)
	return cs
}
