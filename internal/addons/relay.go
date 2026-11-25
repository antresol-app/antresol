package addons

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The core's loopback relay of addon streams: the server's own ffmpeg
// reads a stream an addon serves on its socket (a «ТВ» channel's title
// from outside the library) as
//
//	http://127.0.0.1:<port>/api/addon-relay/<k>/<token>/<rest>
//
// k is the relay's key, one per process, in the path so that the relative
// URIs of an HLS playlist keep it; the token is one opened stream. Both
// are 32 hex characters, never in an answer a client sees. rest is read
// from the addon under the folder of the opened path. Loopback only,
// GET and HEAD, Range passed on, the answer headers filtered as for
// inputs. A token goes on release, after RelayTTL unused, and when its
// addon's process goes (another socket, or not running).

// RelayPrefix is the relay's route.
const RelayPrefix = "/api/addon-relay/"

// RelayTTL: a stream unused this long is forgotten.
const RelayTTL = 6 * time.Hour

const (
	maxRelayStreams = 200 // the next one evicts the least recently used
	// relayHeaderTimeout bounds the wait for an addon's answer headers.
	relayHeaderTimeout = 60 * time.Second
)

// RelayHeaders are the addon's answer headers the relays (this one and
// the inputs') pass on; Content-Type is each relay's own.
var RelayHeaders = []string{"Content-Length", "Content-Range", "Accept-Ranges"}

// RelayRequestHeaders are the headers of r a relay sends to the addon
// (Range, If-Range); ok is false for a multi-range request, which the
// relay answers 416 (a multipart answer would pass as the part's type).
func RelayRequestHeaders(r *http.Request) (h http.Header, ok bool) {
	h = http.Header{}
	rng := r.Header.Get("Range")
	if strings.Contains(rng, ",") {
		return nil, false
	}
	if rng != "" {
		h.Set("Range", rng)
	}
	if ir := r.Header.Get("If-Range"); ir != "" {
		h.Set("If-Range", ir)
	}
	return h, true
}

// SocketLookup is an addon's status and socket (*Manager.Serving).
type SocketLookup func(id string) (Status, string)

// Relay is the loopback relay of addon streams (see RelayPrefix).
type Relay struct {
	sockets SocketLookup
	port    func() int
	key     string
	client  *http.Client
	now     func() time.Time // test clock (nil: time.Now)
	Log     *slog.Logger

	mu      sync.Mutex
	streams map[string]*relayStream // token → stream
}

type relayStream struct {
	addon, socket string
	dir           string // the opened path's folder, "/…/" (escaped)
	headers       map[string]string
	used          time.Time
}

type relaySockKey struct{}

// NewRelay is the relay over the addons' sockets; port is the server's
// loopback port (0: not reachable, Open fails).
func NewRelay(sockets SocketLookup, port func() int) *Relay {
	r := &Relay{sockets: sockets, port: port, key: hex32Random(), streams: map[string]*relayStream{}}
	r.client = &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives:     true, // one socket per process: never a pooled connection to an old one
			DisableCompression:    true,
			ResponseHeaderTimeout: relayHeaderTimeout,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				sock, _ := ctx.Value(relaySockKey{}).(string)
				if sock == "" {
					return nil, errors.New("addons: relay without a socket")
				}
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", sock)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return r
}

// hex32Random is 128 random bits as 32 hex characters.
func hex32Random() string {
	var b [16]byte
	rand.Read(b[:]) // never fails (crypto/rand)
	return hex.EncodeToString(b[:])
}

func (r *Relay) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *Relay) log() *slog.Logger { return cmp.Or(r.Log, slog.Default()) }

// Open opens path (a route on addon addonID's socket: an HLS playlist or
// a file) for ffmpeg: its loopback URL, and release, which forgets it.
func (r *Relay) Open(addonID, path string) (url string, release func(), err error) {
	u, _, rel, err := r.open(addonID, path, nil)
	return u, rel, err
}

// open is Open sending headers with every request to the addon; token is
// the stream's.
func (r *Relay) open(addonID, p string, headers map[string]string) (u, token string, release func(), err error) {
	if !validInputPath(p) || strings.HasSuffix(strings.SplitN(p, "?", 2)[0], "/") {
		return "", "", nil, fmt.Errorf("addons: relay: bad path %q", p)
	}
	port := 0
	if r.port != nil {
		port = r.port()
	}
	if port <= 0 {
		return "", "", nil, errors.New("addons: relay: the server is not reachable on loopback")
	}
	st, sock := r.sockets(addonID)
	if st != StatusRunning || sock == "" {
		return "", "", nil, fmt.Errorf("addons: relay: addon %s is %s", addonID, st)
	}
	i := strings.LastIndex(strings.SplitN(p, "?", 2)[0], "/")
	dir, name := p[:i+1], p[i+1:]
	token = hex32Random()
	now := r.clock()
	r.mu.Lock()
	r.sweepLocked(now)
	r.streams[token] = &relayStream{addon: addonID, socket: sock, dir: dir, headers: headers, used: now}
	r.mu.Unlock()
	u = fmt.Sprintf("http://127.0.0.1:%d%s%s/%s/%s", port, RelayPrefix, r.key, token, name)
	return u, token, func() { r.drop(token) }, nil
}

// sweepLocked forgets the streams unused for RelayTTL and, over
// maxRelayStreams, the least recently used.
func (r *Relay) sweepLocked(now time.Time) {
	var oldest string
	for t, s := range r.streams {
		if now.Sub(s.used) > RelayTTL {
			delete(r.streams, t)
			continue
		}
		if oldest == "" || s.used.Before(r.streams[oldest].used) {
			oldest = t
		}
	}
	if len(r.streams) >= maxRelayStreams && oldest != "" {
		delete(r.streams, oldest)
	}
}

func (r *Relay) drop(token string) {
	r.mu.Lock()
	delete(r.streams, token)
	r.mu.Unlock()
}

// Forget drops every stream of addon id (its process stopped). It only
// takes the relay's lock: a Supervisor.OnStatus listener may call it.
func (r *Relay) Forget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for t, s := range r.streams {
		if s.addon == id {
			delete(r.streams, t)
		}
	}
}

// ServeHTTP answers GET|HEAD /api/addon-relay/<k>/<token>/<rest> from a
// loopback address (RemoteAddr only): 403 from elsewhere, 404 for a wrong
// key, an unknown or gone stream, or a rest that leaves its folder.
func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !loopbackRemote(req) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rest, _ := strings.CutPrefix(req.URL.EscapedPath(), RelayPrefix)
	k, rest, _ := strings.Cut(rest, "/")
	token, rest, ok := strings.Cut(rest, "/")
	if !ok || subtle.ConstantTimeCompare([]byte(k), []byte(r.key)) != 1 {
		http.NotFound(w, req)
		return
	}
	now := r.clock()
	r.mu.Lock()
	s := r.streams[token]
	if s != nil && now.Sub(s.used) > RelayTTL {
		delete(r.streams, token)
		s = nil
	}
	var st relayStream
	if s != nil {
		st = *s
	}
	r.mu.Unlock()
	if s == nil {
		http.NotFound(w, req)
		return
	}
	if cur, sock := r.sockets(st.addon); cur != StatusRunning || sock != st.socket {
		r.drop(token) // the process that served it is gone
		http.NotFound(w, req)
		return
	}
	target, ok := relayTarget(st.dir, rest, req.URL.RawQuery)
	if !ok {
		http.NotFound(w, req)
		return
	}
	hdr, ok := RelayRequestHeaders(req)
	if !ok {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	r.mu.Lock()
	if s := r.streams[token]; s != nil {
		s.used = now
	}
	r.mu.Unlock()
	r.relay(w, req, st, token, target, hdr)
}

// relayTarget is the addon route of rest under dir (with query), false
// when a segment is empty, "." or "..", holds an escaped slash or the
// whole is not a route an addon may serve (validInputPath).
func relayTarget(dir, rest, query string) (string, bool) {
	if rest == "" {
		return "", false
	}
	for _, seg := range strings.Split(rest, "/") {
		dec, err := url.PathUnescape(seg)
		if err != nil || dec == "" || dec == "." || dec == ".." || strings.ContainsAny(dec, "/\\") {
			return "", false
		}
	}
	t := dir + rest
	if query != "" {
		t += "?" + query
	}
	return t, validInputPath(t)
}

// loopbackRemote: the request comes from a loopback address (never
// X-Forwarded-For).
func loopbackRemote(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// relayTypes are the answer types passed on as the addon gives them;
// any other is application/octet-stream.
func relayType(t string) string {
	t = strings.ToLower(strings.TrimSpace(strings.SplitN(t, ";", 2)[0]))
	switch {
	case t == "application/vnd.apple.mpegurl", t == "application/x-mpegurl", t == "audio/mpegurl", t == "audio/x-mpegurl",
		strings.HasPrefix(t, "video/"), strings.HasPrefix(t, "audio/"):
		return t
	}
	return "application/octet-stream"
}

// relay asks the addon for target over the stream's socket (same method,
// its headers, Range and If-Range, X-Antresol-Relay: the token) and
// answers with its status (200, 206 or 416; 404 and 410 as 404; anything
// else 502) and the filtered headers. A stream whose process is gone is
// 404: ffmpeg would retry a 5xx for ever.
func (r *Relay) relay(w http.ResponseWriter, req *http.Request, st relayStream, token, target string, hdr http.Header) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	method := http.MethodGet
	if req.Method == http.MethodHead {
		method = http.MethodHead
	}
	out, err := http.NewRequestWithContext(context.WithValue(req.Context(), relaySockKey{}, st.socket), method, "http://addon"+target, nil)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	for k, v := range st.headers {
		out.Header.Set(k, v)
	}
	for k, v := range hdr {
		out.Header[k] = v
	}
	out.Header.Set("X-Antresol-Relay", token)
	resp, err := r.client.Do(out)
	if err != nil {
		if req.Context().Err() != nil {
			return // ffmpeg went away
		}
		var op *net.OpError
		cur, sock := r.sockets(st.addon)
		if (errors.As(err, &op) && op.Op == "dial") || cur != StatusRunning || sock != st.socket {
			r.drop(token)
			http.NotFound(w, req)
			return
		}
		r.log().Warn("addon relay", "addon", st.addon, "err", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent, http.StatusRequestedRangeNotSatisfiable:
	case http.StatusNotFound, http.StatusGone:
		http.NotFound(w, req)
		return
	default:
		r.log().Warn("addon relay", "addon", st.addon, "status", resp.StatusCode)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	h := w.Header()
	for _, k := range RelayHeaders {
		if v := resp.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	h.Set("Content-Type", relayType(resp.Header.Get("Content-Type")))
	w.WriteHeader(resp.StatusCode)
	if method == http.MethodHead || resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		return
	}
	io.Copy(w, resp.Body) // ends when ffmpeg goes or the addon's socket does
}
