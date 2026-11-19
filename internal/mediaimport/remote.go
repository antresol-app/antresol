package mediaimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Remote is how to reach Jellyfin, Kavita or Audiobookshelf over its API
// instead of its mounted database: the address the admin typed and a key
// (Jellyfin: an API key, or a user name and a password; Kavita: one or
// more users' API keys; Audiobookshelf: an API token).
type Remote struct {
	URL      string
	APIKey   string
	Username string
	Password string
	// token is Jellyfin's access token got with the login at «Подключить»
	// (the password is not sent again on every look).
	token string
}

// Kinds of connection forms (RemoteView.Kind).
const (
	RemoteAPIKey        = "apikey"        // an address and an API key
	RemoteAPIKeyOrLogin = "apikeyOrLogin" // Jellyfin: an API key, or a user name and a password
)

// RemoteKind is the connection form of the core source id ("" = none).
func RemoteKind(id string) string {
	switch id {
	case SourceJellyfin:
		return RemoteAPIKeyOrLogin
	case SourceKavita, SourceABS:
		return RemoteAPIKey
	}
	return ""
}

// RemoteView is a connection as the page sees it: never the key or the
// password, only whether one is kept. Mounted: the database file is there
// too (it is used while no connection is kept).
type RemoteView struct {
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Username  string `json:"username,omitempty"`
	HasSecret bool   `json:"hasSecret"`
	Connected bool   `json:"connected"`
	Mounted   bool   `json:"mounted"`
}

// remotes keeps the connections in memory only: never written to disk,
// logged or answered; a restart forgets them (they are entered again).
type remotes struct {
	mu  sync.RWMutex
	m   map[string]Remote
	gen int // changes with every set and forget (the screen's cache key)
}

func (r *remotes) get(id string) (Remote, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.m[id]
	return v, ok
}

func (r *remotes) set(id string, v Remote) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]Remote{}
	}
	r.m[id] = v
	r.gen++
}

func (r *remotes) forget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, id)
	r.gen++
}

func (r *remotes) all() (map[string]Remote, int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Remote, len(r.m))
	for k, v := range r.m {
		out[k] = v
	}
	return out, r.gen
}

// ErrUnknownRemote: the source has no API connection form.
var ErrUnknownRemote = errors.New("migrate: the source has no API connection")

// RemoteError is a connection problem to show as it is (Russian, a key of
// the catalog: Localize translates it).
type RemoteError struct {
	Msg string
	Err error
}

func (e *RemoteError) Error() string { return e.Msg }
func (e *RemoteError) Unwrap() error { return e.Err }

func remoteErr(err error, format string, a ...any) *RemoteError {
	return &RemoteError{Msg: fmt.Sprintf(format, a...), Err: err}
}

// cleanRemote trims what was typed.
func cleanRemote(r Remote) Remote {
	r.URL = strings.TrimRight(strings.TrimSpace(r.URL), "/")
	r.APIKey = strings.TrimSpace(r.APIKey)
	r.Username = strings.TrimSpace(r.Username)
	return r
}

// checkRemoteURL validates an address typed for app: http(s) with a host,
// and nothing else — no login or password in it (they would be kept,
// answered and logged) and no query or fragment (a key pasted as
// ?apiKey=...). Those go in their own fields.
func checkRemoteURL(app, base string) *RemoteError {
	if base == "" {
		return remoteErr(nil, "Укажите адрес %s", app)
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return remoteErr(err, "Адрес %s должен начинаться с http:// или https://", app)
	}
	if u.User != nil || strings.ContainsAny(base, "?#") {
		return remoteErr(nil, "Адрес %s не должен содержать логин, пароль или параметры — их вводят в отдельных полях", app)
	}
	return nil
}

// redactURL is the address as it may be shown or logged: scheme, host and
// path only.
func redactURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/")
}

// errCrossHostRedirect: the service redirected to another address; the
// key is never sent there.
var errCrossHostRedirect = errors.New("migrate: redirect to another host")

// errBlockedAddress: the address is one the migration never calls.
var errBlockedAddress = errors.New("migrate: address not allowed")

// sameHostOnly is CheckRedirect: a redirect stays on the host the request
// went to and never goes from https to http.
func sameHostOnly(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	first := via[0].URL
	if req.URL.Host != first.Host || (first.Scheme == "https" && req.URL.Scheme != "https") {
		return errCrossHostRedirect
	}
	return nil
}

// dialControl refuses what an admin's typed address never needs to reach:
// link-local addresses (a cloud's metadata service at 169.254.169.254),
// multicast and the unspecified address. The local network and this host
// are allowed: the old servers live there.
func dialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return errBlockedAddress
	}
	return nil
}

// remoteHTTP is the client the API sources use: hc (a default one when
// nil) following same-host redirects only, and its default transport
// dialing through dialControl.
func remoteHTTP(hc *http.Client) *http.Client {
	if hc == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: dialControl}).DialContext
		hc = &http.Client{Timeout: 60 * time.Second, Transport: tr}
	}
	c := *hc
	c.CheckRedirect = sameHostOnly
	return &c
}

// apiBodyLimit bounds one answer (a big library's page of items).
const apiBodyLimit = 64 << 20

// apiClient reads one old service over its JSON API.
type apiClient struct {
	app     string // Jellyfin | Kavita | Audiobookshelf
	base    string // without a trailing slash
	http    *http.Client
	headers map[string]string
	// authHint is said when the service refuses the key (where to find it).
	authHint string
}

func newAPIClient(app, base string, hc *http.Client, authHint string) *apiClient {
	return &apiClient{app: app, base: strings.TrimRight(base, "/"), http: remoteHTTP(hc), headers: map[string]string{}, authHint: authHint}
}

func (c *apiClient) shown() string { return redactURL(c.base) }

// do sends method path (with query) and reads the JSON answer into v (nil:
// ignored). Its errors are *RemoteError or ctx's; no key goes into one.
func (c *apiClient) do(ctx context.Context, method, path string, query url.Values, body any, v any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return remoteErr(err, "Неверный адрес %s", c.app)
	}
	for k, val := range c.headers {
		req.Header.Set(k, val)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		switch {
		case errors.Is(err, errCrossHostRedirect):
			return remoteErr(err, "%s перенаправляет на другой адрес — укажите адрес, который открывается без перенаправления", c.app)
		case errors.Is(err, errBlockedAddress):
			return remoteErr(err, "К адресу %s подключаться нельзя — укажите адрес %s в локальной сети", c.shown(), c.app)
		}
		return remoteErr(err, "%s не отвечает по адресу %s", c.app, c.shown())
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return &RemoteError{Msg: c.authHint, Err: httpStatus{resp.StatusCode}}
	case resp.StatusCode == http.StatusNotFound:
		return remoteErr(httpStatus{resp.StatusCode}, "По адресу %s нет API %s — проверьте адрес", c.shown(), c.app)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return remoteErr(nil, "%s ответил %d на %s", c.app, resp.StatusCode, path)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, apiBodyLimit+1))
	if err != nil {
		return remoteErr(err, "%s не отвечает по адресу %s", c.app, c.shown())
	}
	if len(b) > apiBodyLimit {
		return remoteErr(nil, "Ответ %s слишком большой (больше %d МиБ)", c.app, apiBodyLimit>>20)
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(b, v); err != nil {
		return remoteErr(err, "По адресу %s отвечает не %s", c.shown(), c.app)
	}
	return nil
}

func (c *apiClient) get(ctx context.Context, path string, query url.Values, v any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, v)
}

// httpStatus is the status of a refused key (401, 403) or of a missing
// path (404).
type httpStatus struct{ code int }

func (e httpStatus) Error() string { return fmt.Sprintf("HTTP %d", e.code) }

func statusOf(err error) int {
	var h httpStatus
	if errors.As(err, &h) {
		return h.code
	}
	return 0
}

func isAuthErr(err error) bool {
	c := statusOf(err)
	return c == http.StatusUnauthorized || c == http.StatusForbidden
}

func isNotFound(err error) bool { return statusOf(err) == http.StatusNotFound }

// --- the service's side ---

// Remote is the connection form of the source id as the page sees it;
// false when id has none.
func (s *Service) Remote(id string) (RemoteView, bool) {
	kind := RemoteKind(id)
	if kind == "" {
		return RemoteView{}, false
	}
	v := RemoteView{Kind: kind, Mounted: present(s.dbPath(id), false)}
	if r, ok := s.remotes.get(id); ok {
		v.URL, v.Username, v.HasSecret, v.Connected = r.URL, r.Username, r.APIKey != "" || r.Password != "" || r.token != "", true
	}
	return v, true
}

func (s *Service) dbPath(id string) string {
	switch id {
	case SourceJellyfin:
		return s.Jellyfin
	case SourceKavita:
		return s.Kavita
	case SourceABS:
		return s.ABS
	}
	return ""
}

// Connect checks r against the service id (the address answers, the key
// is taken) and keeps it for the migration: from now on the service is read
// over its API, its database file (if mounted) is not. detail is what it
// found. A blank key or password keeps the one kept for the same address
// (and user name). The error is a *RemoteError to show, ErrUnknownRemote,
// ErrBusy or ctx's.
func (s *Service) Connect(ctx context.Context, id string, r Remote) (string, error) {
	if RemoteKind(id) == "" {
		return "", ErrUnknownRemote
	}
	if s.Running() != "" {
		return "", ErrBusy // a run reads the connection it started with
	}
	r = cleanRemote(r)
	r.token = ""
	if old, ok := s.remotes.get(id); ok && old.URL == r.URL {
		switch {
		case r.APIKey == "" && r.Username == "" && old.Username == "":
			r.APIKey = old.APIKey
		case r.APIKey == "" && r.Password == "" && r.Username == old.Username:
			r.Password, r.token = old.Password, old.token
		}
	}
	name := map[string]string{SourceJellyfin: "Jellyfin", SourceKavita: "Kavita", SourceABS: "Audiobookshelf"}[id]
	if err := checkRemoteURL(name, r.URL); err != nil {
		return "", err
	}
	var detail string
	switch id {
	case SourceJellyfin:
		c, tok, err := jellyfinClient(ctx, r, s.HTTP)
		if err != nil {
			return "", err
		}
		r.token = tok
		data, err := readJellyfinAPI(ctx, c)
		if err != nil {
			return "", err
		}
		detail = jellyfinDetail(ctx, data)
	case SourceKavita:
		list, err := readKavitaAPI(ctx, r, s.HTTP)
		if err != nil {
			return "", err
		}
		detail = kavitaDetail(ctx, list)
	case SourceABS:
		list, _, err := readABSAPI(ctx, r, s.HTTP)
		if err != nil {
			return "", err
		}
		detail = absDetail(ctx, list)
	}
	s.remotes.set(id, r)
	s.cache.forget()
	return detail, nil
}

// Forget drops the connection kept for id: the database file is used again.
func (s *Service) Forget(id string) error {
	if RemoteKind(id) == "" {
		return ErrUnknownRemote
	}
	if s.Running() != "" {
		return ErrBusy
	}
	s.remotes.forget(id)
	s.cache.forget()
	return nil
}
