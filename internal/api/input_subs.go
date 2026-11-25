package api

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/subtitles"
	"github.com/go-chi/chi/v5"
)

// The subtitle sidecars of an addon input: subtitle files the addon serves
// beside the video (relay parts A+1…A+S), listed in the play answer and
// served as WebVTT by GET /api/play/input/{id}/subs/{n}.vtt, converted by
// the library's ffmpeg pipeline. Subtitle streams inside the main file are
// not listed (stage 2b).

// maxInputSub bounds a subtitle sidecar read from an addon.
const maxInputSub = 8 << 20

// inputSubFailMemory is how long a failed read or conversion of a subtitle
// sidecar is remembered: fetches meanwhile fail at once instead of asking
// the addon and running ffmpeg again.
var inputSubFailMemory = 2 * time.Minute

// inputSubSlots bounds the input subtitle conversions running at once,
// server-wide (each is an ffmpeg); inputSubWait is how long one waits for
// a slot before the fetch is 503 msgSubBusy. A cached WebVTT takes none.
const (
	inputSubSlots = 2
	inputSubWait  = 10 * time.Second
)

// inputSubHeadTimeout bounds the addon's answer to the HEAD of a sidecar.
const inputSubHeadTimeout = 15 * time.Second

// msgSubTooLarge and msgSubUnreadable answer a sidecar over maxInputSub and
// one that could not be read or converted; msgSubBusy, a conversion that
// found no free slot within the wait.
const (
	msgSubTooLarge   = "Файл субтитров слишком большой"
	msgSubUnreadable = "Не удалось прочитать субтитры"
	msgSubBusy       = "Сервер занят, попробуйте ещё раз"
)

var (
	errInputSubTooLarge = errors.New("api: subtitle sidecar larger than allowed")
	errInputGone        = errors.New("api: the input's process is gone")
	errInputSubBusy     = errors.New("api: no free slot for a subtitle conversion")
)

// subTracks lists the subtitle sidecars of in, one per sidecar in request
// order: index externalSubBase+n, the codec of its name's extension, its
// language the sidecar's lang when that is a language tag, else the one
// its name says (subtitles.FindExternalIn, looked up by name, which also
// gives the title and the forced flag).
func (p *inputPlay) subTracks(in addons.Input) []SubTrack {
	subs := in.Req.Sidecars.Subtitles
	out := []SubTrack{}
	if len(subs) == 0 {
		return out
	}
	names := make([]string, 0, 1+len(subs))
	names = append(names, in.Req.Name)
	for _, s := range subs {
		names = append(names, s.Name)
	}
	byName := map[string]subtitles.External{}
	for _, e := range subtitles.FindExternalIn(in.Req.Name, names) {
		byName[e.Path] = e
	}
	for n, s := range subs {
		e := byName[s.Name]
		lang := inputSubLang(s.Lang)
		if lang == "" {
			lang = e.Lang
		}
		out = append(out, SubTrack{Index: externalSubBase + n, Codec: subtitles.Codecs[strings.ToLower(path.Ext(s.Name))],
			Lang: lang, Title: e.Title, Forced: e.Forced, External: true,
			URL: "/api/play/input/" + in.ID + "/subs/" + strconv.Itoa(n) + ".vtt"})
	}
	return out
}

// inputSubLang is an addon's subtitle lang as a language code ("en",
// "rus" → "ru", "pt-BR" → "pt"), "" when it is no language tag (the addon's
// text is never shown as it is).
func inputSubLang(l string) string {
	if c := subtitles.ExternalLang(l); c != "" {
		return c
	}
	return subtitles.ExternalLang(primaryLang(strings.TrimSpace(l)))
}

// subtitle is GET /api/play/input/{id}/subs/{n}.vtt: the WebVTT of subtitle
// sidecar n. The addon is asked HEAD for its length and ETag (a file over
// maxInputSub is 413), then the conversion is kept under DataDir/subs by
// the addon, input, path, name, length and ETag; it runs detached (503
// while not ready, as /api/subs) in one of the server's inputSubSlots (503
// msgSubBusy when none was free within the wait). A failure is remembered for
// inputSubFailMemory: 502 meanwhile, without asking the addon.
func (p *inputPlay) subtitle(w http.ResponseWriter, r *http.Request) {
	in, ok := p.inputFor(w, r)
	if !ok {
		return
	}
	subs := in.Req.Sidecars.Subtitles
	ns := chi.URLParam(r, "n")
	n, err := strconv.Atoi(ns)
	if err != nil || n < 0 || n >= len(subs) || strconv.Itoa(n) != ns {
		writeErr(w, r, http.StatusNotFound, "Субтитры не найдены")
		return
	}
	part := 1 + len(in.Req.Sidecars.Audio) + n
	target, name, _, _ := in.Part(part)
	codec := subtitles.Codecs[strings.ToLower(path.Ext(name))] // ValidateInput checked it
	key := in.ID + "/" + strconv.Itoa(part)
	if p.subFailed(&p.subLarge, key) {
		writeErr(w, r, http.StatusRequestEntityTooLarge, msgSubTooLarge)
		return
	}
	if p.subFailed(&p.subFails, key) {
		writeErr(w, r, http.StatusBadGateway, msgSubUnreadable)
		return
	}
	hctx, cancel := context.WithTimeout(r.Context(), inputSubHeadTimeout)
	defer cancel()
	resp, err := p.askPart(hctx, in, target, http.MethodHead, nil)
	if err != nil {
		p.subAskFailed(w, r, in, key, err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("api: addon %s input %s subtitles %d: status %d", in.AddonID, in.ID, n, resp.StatusCode)
		p.subFails.putAt(key, p.clock())
		writeErr(w, r, http.StatusBadGateway, msgSubUnreadable)
		return
	}
	length, etag := resp.Header.Get("Content-Length"), resp.Header.Get("ETag")
	if l, err := strconv.ParseInt(length, 10, 64); err == nil && l > maxInputSub {
		p.subLarge.putAt(key, p.clock())
		writeErr(w, r, http.StatusRequestEntityTooLarge, msgSubTooLarge)
		return
	}
	sum := sha1.Sum([]byte(in.AddonID + "|" + in.ID + "|" + target + "|" + name + "|" + length + "|" + etag))
	out := filepath.Join(p.DataDir, "subs", "i-"+hex.EncodeToString(sum[:])+".vtt")
	if _, err := os.Stat(out); err != nil {
		_, err := p.subJobs.do(r.Context(), out, subtitleWait, 10*time.Minute, func(ctx context.Context) (struct{}, error) {
			if _, err := os.Stat(out); err == nil {
				return struct{}{}, nil
			}
			release, ok := p.takeSubSlot(ctx)
			if !ok {
				return struct{}{}, errInputSubBusy // it never read the sidecar: no failure
			}
			defer release()
			p.pruneInputSubs(filepath.Dir(out))
			err := p.convertInputSub(ctx, in, target, codec, out)
			switch {
			case errors.Is(err, errInputSubTooLarge):
				p.subLarge.putAt(key, p.clock())
			case errors.Is(err, errInputGone), errors.Is(err, errInputNoProfile):
			case err != nil:
				log.Printf("api: addon %s input %s subtitles %d: %s", in.AddonID, in.ID, n, redactToken(err.Error()))
				p.subFails.putAt(key, p.clock())
			}
			return struct{}{}, err
		})
		switch {
		case errors.Is(err, errStillWorking):
			w.Header().Set("Retry-After", "3")
			writeErr(w, r, http.StatusServiceUnavailable, "Субтитры ещё готовятся")
			return
		case r.Context().Err() != nil:
			return
		case errors.Is(err, errInputSubBusy):
			w.Header().Set("Retry-After", "3")
			writeErr(w, r, http.StatusServiceUnavailable, msgSubBusy)
			return
		case errors.Is(err, errInputSubTooLarge):
			writeErr(w, r, http.StatusRequestEntityTooLarge, msgSubTooLarge)
			return
		case errors.Is(err, errInputGone), errors.Is(err, errInputNoProfile):
			writeErr(w, r, http.StatusNotFound, msgAddonNoInput)
			return
		case err != nil:
			writeErr(w, r, http.StatusBadGateway, msgSubUnreadable)
			return
		}
	}
	if st, err := os.Stat(out); err == nil && time.Since(st.ModTime()) > time.Hour {
		now := time.Now()
		os.Chtimes(out, now, now) // in use: not pruned
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	http.ServeFile(w, r, out)
}

// takeSubSlot takes one of the server's inputSubSlots, waiting up to
// p.subWait or until ctx ends (false: none was free).
func (p *inputPlay) takeSubSlot(ctx context.Context) (release func(), ok bool) {
	t := time.NewTimer(p.subWait)
	defer t.Stop()
	select {
	case p.subSlots <- struct{}{}:
		return func() { <-p.subSlots }, true
	case <-t.C:
	case <-ctx.Done():
	}
	return nil, false
}

// subAskFailed answers a HEAD of a subtitle sidecar that got no answer: an
// input whose process is gone is 404, anything else 502 (remembered).
func (p *inputPlay) subAskFailed(w http.ResponseWriter, r *http.Request, in addons.Input, key string, err error) {
	switch {
	case r.Context().Err() != nil:
	case errors.Is(err, errInputNoProfile) || p.gone(in, err):
		writeErr(w, r, http.StatusNotFound, msgAddonNoInput)
	default:
		log.Printf("api: addon %s input %s subtitles: %s", in.AddonID, in.ID, redactToken(err.Error()))
		p.subFails.putAt(key, p.clock())
		writeErr(w, r, http.StatusBadGateway, msgSubUnreadable)
	}
}

// convertInputSub reads the sidecar at target from the addon (at most
// maxInputSub bytes) and converts it from codec into out.
func (p *inputPlay) convertInputSub(ctx context.Context, in addons.Input, target, codec, out string) error {
	resp, err := p.askPart(ctx, in, target, http.MethodGet, nil)
	if err != nil {
		if !errors.Is(err, errInputNoProfile) && p.gone(in, err) {
			return errInputGone
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxInputSub+1))
	if err != nil {
		return err
	}
	if len(b) > maxInputSub {
		return errInputSubTooLarge
	}
	args, stdin := subPipeInput(codec, b)
	args = append(args, "-map", "0:s:0")
	return p.convertSub(ctx, args, stdin, filepath.Dir(out), out)
}

// subFailed: key was put in f within inputSubFailMemory (by p's clock).
func (p *inputPlay) subFailed(f *keyTimes, key string) bool {
	t, ok := f.get(key)
	return ok && p.clock().Sub(t) < inputSubFailMemory
}

// inputSubPruneEvery: how often the converted input subtitles are pruned.
const inputSubPruneEvery = time.Hour

// pruneInputSubs removes the converted input subtitles (i-*.vtt in dir)
// not used for addons.InputTTL (their inputs have lapsed), at most once
// per inputSubPruneEvery.
func (p *inputPlay) pruneInputSubs(dir string) {
	if !p.subPruned.take("", p.clock(), inputSubPruneEvery, false) {
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "i-") || !strings.HasSuffix(n, ".vtt") {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > addons.InputTTL {
			os.Remove(filepath.Join(dir, n))
		}
	}
}
