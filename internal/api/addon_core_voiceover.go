package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/extaudio"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/vofit"
	"github.com/go-chi/chi/v5"
)

// The core API's voice-over fitting (voiceover): an addon that places a
// voice-over track beside a library file queues its fit (the players
// leave the track out until it is fitted) and reads how it went.

const (
	msgAddonNoFit     = "Подгонка озвучек недоступна"
	msgAddonFitNoFile = "Файл не найден в медиатеке"
	msgAddonFitNone   = "Озвучка не найдена"
	addonFitBody      = 64 << 10
)

// fitLang is an ISO 639 code (the title's original language).
var fitLang = regexp.MustCompile(`^[a-z]{2,3}$`)

func mountAddonCoreVoiceover(r chi.Router, a *addonsAPI) {
	r.Post("/voiceover/fit", a.coreQueueFit)
	r.Get("/voiceover/fit", a.coreFitState)
}

// fitCaller: the calling addon has the voiceover permission (403 first)
// and the server runs the fit worker (503).
func (a *addonsAPI) fitCaller(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := a.callingAddon(w, r, "voiceover"); !ok {
		return false
	}
	if a.fitKick == nil || a.db == nil {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonNoFit)
		return false
	}
	return true
}

// besideFile: track is an audio file (an extension extaudio knows) in
// the very folder of the library file video, written absolute and clean
// (filepath.Clean leaves it as it is: no ".." or "." element, no "//"),
// and not the video itself.
func besideFile(track, video string) bool {
	return track != "" && filepath.IsAbs(track) && filepath.Clean(track) == track && track != video &&
		filepath.Dir(track) == filepath.Dir(video) && extaudio.IsAudio(track)
}

// coreQueueFit: POST {path, fileId, origLang} → 202: the track at path,
// placed (or about to be) beside the library file fileId, is queued for
// its fit against the file's track in origLang ("" unknown: the
// voiceover.origLang hook is asked) and the worker kicked. Anything but
// an audio track beside a library file is 400 «Файл не найден в
// медиатеке».
func (a *addonsAPI) coreQueueFit(w http.ResponseWriter, r *http.Request) {
	if !a.fitCaller(w, r) {
		return
	}
	var in struct {
		Path     string `json:"path"`
		FileID   int64  `json:"fileId"`
		OrigLang string `json:"origLang"`
	}
	if !decodeMax(w, r, &in, addonFitBody) {
		return
	}
	lang := strings.ToLower(strings.TrimSpace(in.OrigLang))
	if lang != "" && !fitLang.MatchString(lang) {
		badRequest(w, r)
		return
	}
	if in.FileID <= 0 || a.lib == nil {
		writeErr(w, r, http.StatusBadRequest, msgAddonFitNoFile)
		return
	}
	f, err := a.lib.FileByID(r.Context(), in.FileID)
	if errors.Is(err, library.ErrNotFound) || err == nil && !besideFile(in.Path, f.Path) {
		writeErr(w, r, http.StatusBadRequest, msgAddonFitNoFile)
		return
	}
	if err != nil {
		internalErr(w, r, "addon voiceover file", err)
		return
	}
	if err := vofit.Queue(r.Context(), a.db, in.Path, f.ID, lang); err != nil {
		internalErr(w, r, "addon voiceover queue", err)
		return
	}
	a.fitKick()
	w.WriteHeader(http.StatusAccepted)
}

// coreFitState: GET ?path= → the queued track's FitState; 404 when none.
func (a *addonsAPI) coreFitState(w http.ResponseWriter, r *http.Request) {
	if !a.fitCaller(w, r) {
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		badRequest(w, r)
		return
	}
	f, ok, err := vofit.Lookup(r.Context(), a.db, p)
	if err != nil {
		internalErr(w, r, "addon voiceover fit", err)
		return
	}
	if !ok {
		writeErr(w, r, http.StatusNotFound, msgAddonFitNone)
		return
	}
	writeJSON(w, http.StatusOK, fitState(f))
}

// fitState is a queued track's state: fitted (with its offset and
// tempo), misfit, unmeasured (no track to measure against, or a failed
// fit given up), else pending (queued, a stale fit, a failure retried
// later).
func fitState(f vofit.Stored) addonkit.FitState {
	switch {
	case !f.Current || f.State == vofit.StatePending || f.State == vofit.StateFailed && !f.GaveUp:
		return addonkit.FitState{State: addonkit.FitPending}
	case f.State == vofit.StateOK:
		return addonkit.FitState{State: addonkit.FitOK, OffsetMs: f.OffsetMs, Tempo: f.Tempo}
	case f.State == vofit.StateMisfit:
		return addonkit.FitState{State: addonkit.FitMisfit}
	}
	return addonkit.FitState{State: addonkit.FitUnmeasured}
}

// AddonOrigLang is the fit worker's OrigLang over the voiceover.origLang
// hook (E8): the language the first addon (by id) answers for the
// library file, within 10 s, for the household. "" when none knows it,
// or every addon is down, slow or fails: the fit then runs without it.
func AddonOrigLang(h *addons.Hooks) func(ctx context.Context, fileID int64) string {
	return func(ctx context.Context, fileID int64) string {
		if h == nil {
			return ""
		}
		lang := ""
		h.Each(ctx, "voiceover.origLang", nil, addonkit.OrigLangRequest{FileID: fileID}, func(_ string, raw json.RawMessage) {
			var a addonkit.OrigLangAnswer
			if lang != "" || json.Unmarshal(raw, &a) != nil {
				return
			}
			if l := strings.ToLower(strings.TrimSpace(a.Lang)); fitLang.MatchString(l) {
				lang = l
			}
		})
		return lang
	}
}
