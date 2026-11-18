package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/go-chi/chi/v5"
)

// Sign-in on a TV by QR code (auth.Pairings). The TV starts a request and
// polls it without a session; a signed-in profile looks the code up and
// approves or denies it (the phone app with its Bearer token, or the web
// page /pair?code=… the phone's camera opens). The TV's token is the native
// token of POST /api/auth/token, for the approved profile.
//
// Who may approve for whom: every profile signs a TV in to itself (a kid
// too: it only gets the kid profile on the TV); an admin may sign it in to
// any profile (setting up a kid's TV). A profile without a password is
// refused for a TV that asked from outside the home network: its native
// token would not work there anyway (auth.Guard).
type pairingAPI struct {
	pairs    *auth.Pairings
	users    *auth.Store
	net      *auth.Network
	throttle *auth.Throttle
	serverID string
	me       *authAPI // the /api/me shape of the TV's answer
}

// pairPollInterval is how often the TV is asked to poll, in seconds.
const pairPollInterval = 2

// pairBusyRetry: Retry-After of a start refused for too many open requests.
const pairBusyRetry = 30 * time.Second

const (
	msgPairGone    = "Код не найден или устарел"
	msgPairBusy    = "Слишком много открытых запросов на вход. Попробуйте через минуту"
	msgPairDenied  = "Вход отклонён"
	msgPairExpired = "Код устарел"
	msgPairKid     = "Детский профиль может подтвердить вход только в свой профиль"
	msgPairOther   = "Подтвердить вход в другой профиль может только администратор"
)

// mountPairing mounts /api/auth/pair/*: start and poll are open, lookup,
// approve and deny need a session (guard).
func mountPairing(r chi.Router, deps Deps, guard *auth.Guard, requestsOpen func(context.Context) bool) {
	th := deps.Throttle
	if th == nil {
		th = auth.NewThrottle()
	}
	p := &pairingAPI{
		pairs: auth.NewPairings(deps.DB, deps.Sessions), users: deps.Users, net: deps.Network, throttle: th, serverID: deps.ServerID,
		me: &authAPI{users: deps.Users, sessions: deps.Sessions, net: deps.Network,
			libraryOnly: deps.LibraryOnly || libraryEdition, requestsOpen: requestsOpen},
	}
	r.Post("/api/auth/pair/start", p.start)
	r.Post("/api/auth/pair/poll", p.poll)
	r.Group(func(r chi.Router) {
		r.Use(guard.Require)
		r.Post("/api/auth/pair/lookup", p.lookup)
		r.Post("/api/auth/pair/approve", p.approve)
		r.Post("/api/auth/pair/deny", p.deny)
	})
}

func (p *pairingAPI) start(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Device struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Model   string `json:"model"`
			Version string `json:"version"`
		} `json:"device"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !deviceIDRe.MatchString(in.Device.ID) {
		writeErr(w, r, http.StatusBadRequest, "Не указано устройство")
		return
	}
	d := auth.DeviceInfo{ID: in.Device.ID, Name: clip(in.Device.Name, 100), Model: clip(in.Device.Model, 64), Version: clip(in.Device.Version, 64)}
	st, err := p.pairs.Start(r.Context(), d, p.net.ClientKey(r), !p.net.IsTrusted(r))
	if errors.Is(err, auth.ErrPairingBusy) {
		w.Header().Set("Retry-After", strconv.Itoa(int(pairBusyRetry/time.Second)))
		writeErr(w, r, http.StatusTooManyRequests, msgPairBusy)
		return
	}
	if err != nil {
		internalErr(w, r, "pair start", err)
		return
	}
	q := url.Values{"code": {st.Code}}
	if p.serverID != "" {
		q.Set("sid", p.serverID)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"code":      st.Code,
		"secret":    st.Secret,
		"expiresAt": st.ExpiresAt.UTC().Format(time.RFC3339),
		"interval":  pairPollInterval,
		"url":       p.net.Origin(r) + "/pair?" + q.Encode(),
		"serverId":  p.serverID,
	})
}

func (p *pairingAPI) poll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Secret string `json:"secret"`
	}
	if !decode(w, r, &in) {
		return
	}
	key := "pair:poll:" + p.net.ClientKey(r)
	if wait := p.throttle.Wait(key); wait > 0 {
		tooMany(w, r, wait)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	res, err := p.pairs.Poll(r.Context(), in.Secret)
	if errors.Is(err, auth.ErrPairingGone) {
		p.throttle.Fail(key) // a guessed secret
		writeJSON(w, http.StatusGone, map[string]string{"status": auth.PairExpired, "error": i18n.Localize(r.Context(), msgPairExpired)})
		return
	}
	if err != nil {
		internalErr(w, r, "pair poll", err)
		return
	}
	exp := res.ExpiresAt.UTC().Format(time.RFC3339)
	switch res.Status {
	case auth.PairPending:
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending", "expiresAt": exp, "interval": pairPollInterval})
	case auth.PairDone:
		log.Printf("auth: a device signed in to profile %d by QR from %s", res.User.ID, p.net.ClientKey(r))
		writeJSON(w, http.StatusOK, map[string]any{"status": "approved", "token": res.Token, "user": p.me.meOf(r, res.User)})
	case auth.PairDenied:
		writeJSON(w, http.StatusGone, map[string]string{"status": "denied", "error": i18n.Localize(r.Context(), msgPairDenied)})
	default:
		writeJSON(w, http.StatusGone, map[string]string{"status": auth.PairExpired, "error": i18n.Localize(r.Context(), msgPairExpired)})
	}
}

// codeKeys are the throttle keys of a profile trying codes: the address
// and the profile (codes are short, guesses must stay slow).
func (p *pairingAPI) codeKeys(r *http.Request, u auth.User) []string {
	return []string{"pair:code:" + p.net.ClientKey(r), fmt.Sprintf("pair:code:user:%d", u.ID)}
}

// open looks the code of a signed-in request up, answering for it when it
// is throttled or not open (ok false).
func (p *pairingAPI) open(w http.ResponseWriter, r *http.Request, code string) (auth.PairingInfo, bool) {
	keys := p.codeKeys(r, userOf(r))
	if wait := p.throttle.Wait(keys...); wait > 0 {
		tooMany(w, r, wait)
		return auth.PairingInfo{}, false
	}
	info, err := p.pairs.Lookup(r.Context(), code)
	if errors.Is(err, auth.ErrPairingGone) {
		p.throttle.Fail(keys...)
		writeErr(w, r, http.StatusNotFound, msgPairGone)
		return auth.PairingInfo{}, false
	}
	if err != nil {
		internalErr(w, r, "pair lookup", err)
		return auth.PairingInfo{}, false
	}
	return info, true
}

type pairCode struct {
	Code   string `json:"code"`
	UserID int64  `json:"userId"` // approve: the profile to sign in (0: the caller's)
}

func (p *pairingAPI) lookup(w http.ResponseWriter, r *http.Request) {
	var in pairCode
	if !decode(w, r, &in) {
		return
	}
	info, ok := p.open(w, r, in.Code)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"code":      auth.NormalizePairingCode(in.Code),
		"device":    map[string]string{"name": info.Device.Name, "model": info.Device.Model, "version": info.Device.Version},
		"remote":    info.Remote,
		"createdAt": info.CreatedAt.UTC().Format(time.RFC3339),
		"expiresAt": info.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (p *pairingAPI) approve(w http.ResponseWriter, r *http.Request) {
	var in pairCode
	if !decode(w, r, &in) {
		return
	}
	u := userOf(r)
	target := u
	if in.UserID != 0 && in.UserID != u.ID {
		switch {
		case u.IsKid:
			writeErr(w, r, http.StatusForbidden, msgPairKid)
			return
		case !u.IsAdmin:
			writeErr(w, r, http.StatusForbidden, msgPairOther)
			return
		}
		t, err := p.users.Get(r.Context(), in.UserID)
		if err != nil {
			storeErr(w, r, err)
			return
		}
		target = t
	}
	info, ok := p.open(w, r, in.Code)
	if !ok {
		return
	}
	if info.Remote && !target.HasPassword {
		w.Header().Set("X-Vault-Error", "remote-passwordless")
		writeErr(w, r, http.StatusForbidden, auth.MsgRemotePasswordless)
		return
	}
	err := p.pairs.Approve(r.Context(), in.Code, target.ID, u.ID)
	if errors.Is(err, auth.ErrPairingGone) { // decided or expired a moment ago
		writeErr(w, r, http.StatusNotFound, msgPairGone)
		return
	}
	if err != nil {
		internalErr(w, r, "pair approve", err)
		return
	}
	log.Printf("auth: profile %d approved a QR sign-in for profile %d", u.ID, target.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "userId": target.ID})
}

func (p *pairingAPI) deny(w http.ResponseWriter, r *http.Request) {
	var in pairCode
	if !decode(w, r, &in) {
		return
	}
	if _, ok := p.open(w, r, in.Code); !ok {
		return
	}
	err := p.pairs.Deny(r.Context(), in.Code, userOf(r).ID)
	if errors.Is(err, auth.ErrPairingGone) {
		writeErr(w, r, http.StatusNotFound, msgPairGone)
		return
	}
	if err != nil {
		internalErr(w, r, "pair deny", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
