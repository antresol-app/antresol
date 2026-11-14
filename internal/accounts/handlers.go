package accounts

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// OAuth resolves the provider's OAuth client: the environment's client id
// and secret override the stored ones (account_settings); the redirect URI
// is the server's public URL + /api/accounts/shikimori/callback.
type OAuth struct {
	Store                        *Store
	PublicURL                    string
	EnvClientID, EnvClientSecret string
}

const callbackPath = "/api/accounts/shikimori/callback"

// RedirectURI is where the provider sends the browser back.
func (o *OAuth) RedirectURI() string {
	if o.PublicURL == "" {
		return ""
	}
	return strings.TrimRight(o.PublicURL, "/") + callbackPath
}

// Client returns the OAuth client; ok is false while id or secret is
// missing.
func (o *OAuth) Client(ctx context.Context) (OAuthClient, bool, error) {
	c := OAuthClient{ID: o.EnvClientID, Secret: o.EnvClientSecret, RedirectURI: o.RedirectURI()}
	if c.ID == "" || c.Secret == "" {
		id, sec, ok, err := o.Store.ClientSettings(ctx, Shikimori)
		if err != nil {
			return OAuthClient{}, false, err
		}
		if ok {
			if c.ID == "" {
				c.ID = id
			}
			if c.Secret == "" {
				c.Secret = sec
			}
		}
	}
	return c, c.ID != "" && c.Secret != "", nil
}

// CanWrite: write-back can be offered (a client, and an https public URL
// the provider can send the browser back to).
func (o *OAuth) CanWrite(ctx context.Context) bool {
	if !strings.HasPrefix(o.PublicURL, "https://") {
		return false
	}
	_, ok, err := o.Client(ctx)
	return err == nil && ok
}

// RemoteUser is a provider account.
type RemoteUser struct {
	ID       int64
	Nickname string
}

// Account is the provider's user lookup and OAuth sign-in. An unknown
// nickname is ErrRemoteNotFound.
type Account interface {
	UserByNick(ctx context.Context, nick string) (RemoteUser, error)
	AuthorizeURL(c OAuthClient, state string) string
	Exchange(ctx context.Context, c OAuthClient, code string) (Tokens, error)
	Whoami(ctx context.Context, access string) (RemoteUser, error)
}

// Viewer is the signed-in profile of a request.
type Viewer struct {
	ID             int64
	IsKid, IsAdmin bool
}

// API is the accounts HTTP API. Routes are mounted behind the core's
// session check, which also turns kid profiles away; BrowserRoutes (the
// OAuth callback, a browser navigation) are reached without a viewer then
// and answer with a redirect.
type API struct {
	Sync    *Syncer
	Account Account
	// Viewer is the request's signed-in profile (ok false: none).
	Viewer func(r *http.Request) (Viewer, bool)
	// Localize translates a catalog key into the request's language (nil:
	// the key itself).
	Localize func(ctx context.Context, key string) string
	// Background runs work after the response (nil: a goroutine).
	Background func(func())
	Log        *slog.Logger
	// MyShows is the read-only MyShows link (nil: not offered, its routes
	// answer 404 and GET leaves "myshows" out).
	MyShows MyShows

	mu     sync.Mutex
	states map[string]oauthState
}

type oauthState struct {
	uid     int64
	expires time.Time
}

const stateTTL = 10 * time.Minute

// maxStates caps the pending sign-ins kept in memory (the oldest go first).
const maxStates = 1000

// Routes mounts the API:
//
//	GET    /api/accounts
//	  → {"myshows": {"linked": bool, "nickname": str (the MyShows login), "status": "ok"|"private"|"not_found"|"error",
//	     "lastError": str (in the viewer's language), "lastSync": RFC 3339 | null, "unmapped": int}
//	     (only when MyShows is offered; read only: nothing is sent back to MyShows),
//	     "shikimori": {"linked": bool, "nickname": str, "status": "ok"|"private"|"not_found"|"error"|"relogin",
//	     "lastError": str (in the viewer's language), "lastSync": RFC 3339 | null,
//	     "writeState": "unasked"|"asked"|"enabled"|"declined",
//	     "canWrite": bool (an OAuth client is configured and the public URL is https), "unmapped": int}}
//	PUT    /api/accounts/shikimori         {"nickname": str} → the GET body; 404 {"error"} for an unknown nickname
//	DELETE /api/accounts/shikimori         → 204 (link, remote twins and queue removed)
//	POST   /api/accounts/shikimori/write   {"enable": false} → 204 (declined, queue cleared)
//	                                       {"enable": true}  → {"authorizeUrl": str}; 409 when canWrite is false
//	POST   /api/accounts/shikimori/asked   → 204 (the write-back prompt was shown: unasked becomes asked; any other state stays)
//	PUT    /api/accounts/myshows           {"nickname": login} → the GET body; the list is read once to check the login:
//	                                       404 {"error"} unknown login, 422 {"error"} private profile (nothing is linked),
//	                                       502 {"error"} MyShows not answering; 404 when MyShows is not offered
//	DELETE /api/accounts/myshows           → 204 (the link, its twins and seen entries removed; marks stay)
//	GET    /api/accounts/admin/shikimori   (admin) → {"clientId", "hasSecret": bool, "fromEnv": bool, "redirectUri", "scope": "user_rates"}
//	PUT    /api/accounts/admin/shikimori   (admin) {"clientId", "clientSecret"} → 204 (an empty secret keeps the stored one)
func (a *API) Routes(r chi.Router) {
	r.Get("/api/accounts", a.get)
	r.Put("/api/accounts/shikimori", a.putNickname)
	r.Delete("/api/accounts/shikimori", a.unlink)
	r.Post("/api/accounts/shikimori/write", a.write)
	r.Post("/api/accounts/shikimori/asked", a.asked)
	r.Put("/api/accounts/myshows", a.putMyShows)
	r.Delete("/api/accounts/myshows", a.unlinkMyShows)
	r.Get("/api/accounts/admin/shikimori", a.adminGet)
	r.Put("/api/accounts/admin/shikimori", a.adminPut)
}

// BrowserRoutes mounts the routes a browser navigates to:
//
//	GET /api/accounts/shikimori/callback?code&state
//	  → 302 /settings?account=shikimori&ok=1 | &error=session|state|denied|exchange|account
func (a *API) BrowserRoutes(r chi.Router) {
	r.Get(callbackPath, a.callback)
}

func (a *API) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

func (a *API) localize(ctx context.Context, key string) string {
	if a.Localize == nil || key == "" {
		return key
	}
	return a.Localize(ctx, key)
}

func (a *API) background(f func()) {
	if a.Background != nil {
		a.Background(f)
		return
	}
	go f()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// fail answers {"error": key in the request's language}; internal errors
// are logged, not shown.
func (a *API) fail(w http.ResponseWriter, r *http.Request, code int, key string, err error) {
	if err != nil {
		a.log().Warn("accounts api", "path", r.URL.Path, "err", err)
	}
	writeJSON(w, code, map[string]string{"error": a.localize(r.Context(), key)})
}

func (a *API) viewer(w http.ResponseWriter, r *http.Request) (Viewer, bool) {
	v, ok := a.Viewer(r)
	if !ok {
		a.fail(w, r, http.StatusUnauthorized, "Нужно войти", nil)
		return v, false
	}
	if v.IsKid {
		a.fail(w, r, http.StatusForbidden, "Недостаточно прав", nil)
		return v, false
	}
	return v, true
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v)
}

type shikimoriJSON struct {
	Linked     bool    `json:"linked"`
	Nickname   string  `json:"nickname"`
	Status     string  `json:"status"`
	LastError  string  `json:"lastError"`
	LastSync   *string `json:"lastSync"`
	WriteState string  `json:"writeState"`
	CanWrite   bool    `json:"canWrite"`
	Unmapped   int     `json:"unmapped"`
}

func (a *API) state(ctx context.Context, uid int64) (map[string]any, error) {
	out := shikimoriJSON{WriteState: "unasked", Status: StatusOK, CanWrite: a.Sync.OAuth != nil && a.Sync.OAuth.CanWrite(ctx)}
	l, ok, err := a.Sync.Accounts.Link(ctx, uid, Shikimori)
	if err != nil {
		return nil, err
	}
	if ok {
		out.Linked = l.ExternalID != 0
		out.Nickname, out.Status, out.WriteState, out.Unmapped = l.Nickname, l.Status, l.WriteState, l.Unmapped
		out.LastError = a.localize(ctx, l.LastError)
		if !l.LastSync.IsZero() {
			s := l.LastSync.UTC().Format(time.RFC3339)
			out.LastSync = &s
		}
	}
	res := map[string]any{Shikimori: out}
	if a.MyShows != nil {
		ms, err := a.myShowsState(ctx, uid)
		if err != nil {
			return nil, err
		}
		res[myShowsProvider] = ms
	}
	return res, nil
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	v, ok := a.viewer(w, r)
	if !ok {
		return
	}
	st, err := a.state(r.Context(), v.ID)
	if err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось прочитать данные", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// pullThenPush runs a pull (when wanted) and a push for the profile after
// the response.
func (a *API) pullThenPush(uid int64, pull bool) {
	a.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if pull {
			if _, err := a.Sync.PullOne(ctx, uid); err != nil {
				a.log().Info("accounts: pull after link", "user", uid, "err", err)
			}
		}
		if err := a.Sync.PushOne(ctx, uid); err != nil {
			a.log().Info("accounts: push after link", "user", uid, "err", err)
		}
	})
}

func (a *API) putNickname(w http.ResponseWriter, r *http.Request) {
	v, ok := a.viewer(w, r)
	if !ok {
		return
	}
	var body struct {
		Nickname string `json:"nickname"`
	}
	if err := decode(r, &body); err != nil || strings.TrimSpace(body.Nickname) == "" {
		a.fail(w, r, http.StatusBadRequest, "Укажите ник на Shikimori", nil)
		return
	}
	ctx := r.Context()
	u, err := a.Account.UserByNick(ctx, strings.TrimSpace(body.Nickname))
	if errors.Is(err, ErrRemoteNotFound) {
		a.fail(w, r, http.StatusNotFound, "Пользователь Shikimori не найден", nil)
		return
	}
	if err != nil {
		a.fail(w, r, http.StatusBadGateway, "Shikimori не отвечает, попробуйте позже", err)
		return
	}
	l, linked, err := a.Sync.Accounts.Link(ctx, v.ID, Shikimori)
	if err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
		return
	}
	if linked && l.ExternalID != 0 && l.ExternalID != u.ID {
		// another account: the old one's tokens, twins and queue go
		if err := a.Sync.Accounts.Unlink(ctx, v.ID, Shikimori); err != nil {
			a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
			return
		}
	}
	if err := a.Sync.Accounts.SetNickname(ctx, v.ID, Shikimori, u.Nickname, u.ID); err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
		return
	}
	a.pullThenPush(v.ID, true)
	a.get(w, r)
}

func (a *API) unlink(w http.ResponseWriter, r *http.Request) {
	v, ok := a.viewer(w, r)
	if !ok {
		return
	}
	if err := a.Sync.Accounts.Unlink(r.Context(), v.ID, Shikimori); err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) write(w http.ResponseWriter, r *http.Request) {
	v, ok := a.viewer(w, r)
	if !ok {
		return
	}
	var body struct {
		Enable *bool `json:"enable"`
	}
	if err := decode(r, &body); err != nil || body.Enable == nil {
		a.fail(w, r, http.StatusBadRequest, "Неверный запрос", nil)
		return
	}
	ctx := r.Context()
	if !*body.Enable {
		if err := a.Sync.Accounts.SetWriteState(ctx, v.ID, Shikimori, "declined"); err != nil {
			a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
			return
		}
		if err := a.Sync.Accounts.ClearOutbox(ctx, v.ID, Shikimori); err != nil {
			a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
			return
		}
		// no write-back: no reason to keep its sign-in
		if err := a.Sync.Accounts.ClearTokens(ctx, v.ID, Shikimori); err != nil {
			a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if a.Sync.OAuth == nil || !a.Sync.OAuth.CanWrite(ctx) {
		a.fail(w, r, http.StatusConflict, "Запись на Shikimori не настроена на этом сервере", nil)
		return
	}
	c, _, err := a.Sync.OAuth.Client(ctx)
	if err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось прочитать данные", err)
		return
	}
	st, err := a.newState(v.ID)
	if err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorizeUrl": a.Account.AuthorizeURL(c, st)})
}

// asked records that the write-back prompt was shown, so it is shown once
// per profile: only unasked moves on.
func (a *API) asked(w http.ResponseWriter, r *http.Request) {
	v, ok := a.viewer(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	l, linked, err := a.Sync.Accounts.Link(ctx, v.ID, Shikimori)
	if err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
		return
	}
	if linked && l.WriteState == "unasked" {
		if err := a.Sync.Accounts.SetWriteState(ctx, v.ID, Shikimori, "asked"); err != nil {
			a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// newState issues a random OAuth state bound to the profile for 10 min.
func (a *API) newState(uid int64) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	st := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.states == nil {
		a.states = map[string]oauthState{}
	}
	for k, s := range a.states {
		if now.After(s.expires) {
			delete(a.states, k)
		}
	}
	for len(a.states) >= maxStates {
		oldest := ""
		for k, s := range a.states {
			if oldest == "" || s.expires.Before(a.states[oldest].expires) {
				oldest = k
			}
		}
		delete(a.states, oldest)
	}
	a.states[st] = oauthState{uid: uid, expires: now.Add(stateTTL)}
	return st, nil
}

// takeState consumes a state: valid once, for its profile, before expiry.
func (a *API) takeState(st string, uid int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.states[st]
	if !ok {
		return false
	}
	delete(a.states, st)
	return s.uid == uid && time.Now().Before(s.expires)
}

func (a *API) callback(w http.ResponseWriter, r *http.Request) {
	// the URL carries a one-time code: never cached, never sent on
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	done := func(result string) {
		http.Redirect(w, r, "/settings?account=shikimori&"+result, http.StatusFound)
	}
	v, ok := a.Viewer(r)
	if !ok || v.IsKid {
		done("error=session")
		return
	}
	q := r.URL.Query()
	if !a.takeState(q.Get("state"), v.ID) {
		done("error=state")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		done("error=denied")
		return
	}
	ctx := r.Context()
	if a.Sync.OAuth == nil {
		done("error=exchange")
		return
	}
	c, ok, err := a.Sync.OAuth.Client(ctx)
	if err != nil || !ok {
		a.log().Warn("accounts: oauth client", "err", err)
		done("error=exchange")
		return
	}
	tok, err := a.Account.Exchange(ctx, c, q.Get("code"))
	if err != nil {
		a.log().Info("accounts: oauth exchange", "user", v.ID, "err", err)
		done("error=exchange")
		return
	}
	me, err := a.Account.Whoami(ctx, tok.Access)
	if err != nil || me.ID == 0 {
		a.log().Info("accounts: oauth whoami", "user", v.ID, "err", err)
		done("error=exchange")
		return
	}
	l, linked, err := a.Sync.Accounts.Link(ctx, v.ID, Shikimori)
	if err != nil {
		a.log().Warn("accounts: link", "user", v.ID, "err", err)
		done("error=exchange")
		return
	}
	if linked && l.ExternalID != 0 && l.ExternalID != me.ID {
		done("error=account") // signed in as another account than the linked one
		return
	}
	if !linked || l.ExternalID == 0 {
		if err := a.Sync.Accounts.SetNickname(ctx, v.ID, Shikimori, me.Nickname, me.ID); err != nil {
			a.log().Warn("accounts: link", "user", v.ID, "err", err)
			done("error=exchange")
			return
		}
	}
	// under the profile's lock: a push refreshing now cannot store its
	// rotated pair over this one
	lk := a.Sync.userLock(v.ID)
	lk.Lock()
	err = a.Sync.Accounts.SetTokens(ctx, v.ID, Shikimori, tok)
	lk.Unlock()
	if err == nil {
		err = a.Sync.Accounts.SetWriteState(ctx, v.ID, Shikimori, "enabled")
	}
	if err == nil && l.Status == StatusRelogin {
		err = a.Sync.Accounts.SetStatus(ctx, v.ID, Shikimori, StatusOK, "", l.LastSync)
	}
	if err != nil {
		a.log().Warn("accounts: store sign-in", "user", v.ID, "err", err)
		done("error=exchange")
		return
	}
	a.pullThenPush(v.ID, !l.FirstPullDone)
	done("ok=1")
}

func (a *API) admin(w http.ResponseWriter, r *http.Request) bool {
	v, ok := a.viewer(w, r)
	if !ok {
		return false
	}
	if !v.IsAdmin {
		a.fail(w, r, http.StatusForbidden, "Недостаточно прав", nil)
		return false
	}
	return true
}

func (a *API) adminGet(w http.ResponseWriter, r *http.Request) {
	if !a.admin(w, r) {
		return
	}
	o := a.Sync.OAuth
	if o == nil {
		a.fail(w, r, http.StatusConflict, "Запись на Shikimori не настроена на этом сервере", nil)
		return
	}
	c, _, err := o.Client(r.Context())
	if err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось прочитать данные", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clientId": c.ID, "hasSecret": c.Secret != "",
		"fromEnv": o.EnvClientID != "" || o.EnvClientSecret != "", "redirectUri": o.RedirectURI(), "scope": "user_rates"})
}

func (a *API) adminPut(w http.ResponseWriter, r *http.Request) {
	if !a.admin(w, r) {
		return
	}
	var body struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, http.StatusBadRequest, "Неверный запрос", nil)
		return
	}
	ctx := r.Context()
	st := a.Sync.Accounts
	if body.ClientSecret == "" {
		_, sec, _, err := st.ClientSettings(ctx, Shikimori)
		if err != nil {
			a.fail(w, r, http.StatusInternalServerError, "Не удалось прочитать данные", err)
			return
		}
		body.ClientSecret = sec
	}
	if err := st.SetClientSettings(ctx, Shikimori, strings.TrimSpace(body.ClientID), strings.TrimSpace(body.ClientSecret)); err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось сохранить", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
