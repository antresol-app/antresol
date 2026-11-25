package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/go-chi/chi/v5"
)

// The loopback relay of addon inputs: GET|HEAD /api/addon-input/{id}/{n}
// reads part n of an input from the socket of the process that registered
// it. Only ffmpeg and ffprobe ask it (internalURL): the per-process token
// and a loopback client, else 404 like an unknown input (no hint which check
// failed). A reverse proxy on this host makes every request loopback, so
// the token is what keeps the relay closed.

// inputRelayHeaders are the addon's answer headers the relay passes on:
// the relays' own (addons.RelayHeaders) and the validators If-Range
// needs; Content-Type comes from the part's name (inputType).
var inputRelayHeaders = append(slices.Clone(addons.RelayHeaders), "Last-Modified", "ETag")

// internalRequest: the token (both non-empty, compared in constant time)
// from a loopback address (RemoteAddr only, never X-Forwarded-For).
func (p *inputPlay) internalRequest(r *http.Request) bool {
	k := r.URL.Query().Get("k")
	if p.token == "" || k == "" || subtle.ConstantTimeCompare([]byte(k), []byte(p.token)) != 1 {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (p *inputPlay) relayRoute(w http.ResponseWriter, r *http.Request) {
	if !p.internalRequest(r) {
		writeErr(w, r, http.StatusNotFound, msgAddonNoInput)
		return
	}
	ns := chi.URLParam(r, "n")
	n, err := strconv.Atoi(ns)
	if err != nil || n < 0 || strconv.Itoa(n) != ns {
		writeErr(w, r, http.StatusNotFound, msgAddonNoInput)
		return
	}
	in, ok := p.inputs.Get(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, r, http.StatusNotFound, msgAddonNoInput)
		return
	}
	p.relay(w, r, in, n)
}

// inputType is the Content-Type of a part named name: its extension's
// video or audio type, else application/octet-stream (what the addon says
// is never used).
func inputType(name string) string {
	if t := fileTypes[strings.ToLower(path.Ext(name))]; strings.HasPrefix(t, "video/") || strings.HasPrefix(t, "audio/") {
		return t
	}
	return "application/octet-stream"
}

// relay reads part n of in from the addon for r (same method; only Range
// and If-Range pass, plus who asks) and answers with its status (200, 206
// or 416; anything else 502) and the whitelisted headers. An input whose
// process is gone is 404: ffmpeg would retry a 5xx for ever.
func (p *inputPlay) relay(w http.ResponseWriter, r *http.Request, in addons.Input, n int) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	target, name, size, ok := in.Part(n)
	if !ok {
		writeErr(w, r, http.StatusNotFound, msgAddonNoInput)
		return
	}
	hdr, ok := addons.RelayRequestHeaders(r)
	if !ok {
		// a multipart answer would pass as the part's own type
		if size > 0 {
			w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		}
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	method := http.MethodGet
	if r.Method == http.MethodHead {
		method = http.MethodHead
	}
	resp, err := p.askPart(r.Context(), in, target, method, hdr)
	if errors.Is(err, errInputNoProfile) {
		writeErr(w, r, http.StatusNotFound, msgAddonNoInput)
		return
	}
	if err != nil {
		if r.Context().Err() != nil {
			return // ffmpeg went away
		}
		if p.gone(in, err) {
			writeErr(w, r, http.StatusNotFound, msgAddonNoInput)
			return
		}
		log.Printf("api: addon %s input %s part %d: %v", in.AddonID, in.ID, n, err)
		writeErr(w, r, http.StatusBadGateway, msgAddonNoAnswer)
		return
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent, http.StatusRequestedRangeNotSatisfiable:
	default:
		log.Printf("api: addon %s input %s part %d: status %d", in.AddonID, in.ID, n, resp.StatusCode)
		writeErr(w, r, http.StatusBadGateway, msgAddonNoAnswer)
		return
	}
	h := w.Header()
	for _, k := range inputRelayHeaders {
		if v := resp.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	h.Set("Content-Type", inputType(name))
	w.WriteHeader(resp.StatusCode)
	if method == http.MethodHead || resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		return
	}
	io.Copy(w, resp.Body) // ends when ffmpeg goes or the addon's socket does
}

// errInputNoProfile: the input's profile is gone (or its part cannot be
// asked for): answered like an unknown input.
var errInputNoProfile = errors.New("api: input without its profile")

// askPart asks the addon of in for target (a part's path) with method and
// hdr (Range, If-Range), as the input's profile, over the socket of the
// process that registered it (p.client: no redirect followed).
func (p *inputPlay) askPart(ctx context.Context, in addons.Input, target, method string, hdr http.Header) (*http.Response, error) {
	who, ok := p.inputUser(ctx, in.Req.UserID)
	if !ok {
		return nil, errInputNoProfile
	}
	// target is in the form ValidateInput checked (canonical escaping): it
	// parses back to itself, so the addon is asked exactly that
	out, err := http.NewRequestWithContext(context.WithValue(ctx, inputSockKey{}, in.Socket), method, "http://addon"+target, nil)
	if err != nil {
		return nil, errInputNoProfile
	}
	for k, v := range hdr {
		out.Header[k] = v
	}
	out.Header.Set("X-Antresol-Input", in.ID)
	addons.SetUser(out.Header, in.Socket, who)
	return p.client.Do(out)
}

// gone: the read failed because the input's process is gone (it could not
// be dialled, or the addon no longer serves on that socket).
func (p *inputPlay) gone(in addons.Input, err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	if p.rt == nil {
		return true
	}
	st, sock := p.rt.Serving(in.AddonID)
	return st != addons.StatusRunning || sock != in.Socket
}

// inputUser is the X-Antresol-User header of an input's profile (false:
// the profile is gone).
func (p *inputPlay) inputUser(ctx context.Context, uid int64) (string, bool) {
	who := addonkit.User{ID: uid}
	if p.users != nil {
		u, err := p.users.Get(ctx, uid)
		if errors.Is(err, auth.ErrNotFound) {
			return "", false
		}
		if err == nil {
			who = addonkit.User{ID: u.ID, UID: u.UID, Name: u.Name, IsAdmin: u.IsAdmin, IsKid: u.IsKid}
		}
	}
	if p.lib != nil && p.DB != nil {
		if _, l, err := p.effectiveUILanguage(ctx, uid); err == nil {
			who.Lang = l
		}
	}
	b, _ := json.Marshal(who)
	return addons.ASCIIJSON(b), true
}
