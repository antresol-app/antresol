package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/iptv"
	"github.com/go-chi/chi/v5"
)

// The web's relay of an external stream it cannot play as given (a
// User-Agent or Referer a browser cannot send, http on an https page, a
// provider without CORS): the server fetches the provider's URL with the
// source's headers and passes the body on, same-origin. An HLS playlist
// comes back with every URI (variants, renditions, segments, keys, maps)
// pointing back through the relay by a sealed reference — the URL, the
// viewer, the channel and an expiry, encrypted and authenticated with the
// server's key — so the relay fetches only what the channel's playlist
// links to, and the browser never sees a provider address. Anything else
// (MPEG-TS, progressive) is piped with flushing. Nothing is kept on disk;
// the apps keep the direct URLs (they send the headers themselves).
//
//	GET /api/channels/live/ext/{id}/{n}/relay        stream n of channel id
//	GET /api/channels/live/ext/{id}/{n}/relay/{ref}  a URI of its playlist
//
// Access is the channel's: the viewer's set and rules, checked on every
// request (cached for relayVisibleTTL). A viewer relays at most
// relayPerUser streams at once, the server relayTotal.

const (
	relayPerUser = 3
	relayTotal   = 32
	// relayIdle: a relay without a request for this long has stopped
	// (an HLS player asks at least every target duration).
	relayIdle = 20 * time.Second
	// relayEvictAfter: at the viewer's limit, a relay of theirs idle this
	// long gives its place to a new one (the player moved on).
	relayEvictAfter      = 5 * time.Second
	relayPlaylistMax     = 4 << 20
	relayPlaylistTimeout = 20 * time.Second
	relayStall           = 30 * time.Second
	relayConnect         = 10 * time.Second
	relayHeaders         = 15 * time.Second
	// relayPlaylistTTL: a variant playlist's reference lasts the viewing
	// (players reload it without going back to the master);
	// relayMediaTTL: a segment's, key's or map's, until the next reload.
	relayPlaylistTTL = 12 * time.Hour
	relayMediaTTL    = 10 * time.Minute
	relayVisibleTTL  = 30 * time.Second
)

var (
	errRelayRef   = errors.New("relay: bad reference")
	errRelayLimit = errors.New("relay: too many streams")
	errRelayDial  = errors.New("relay: address not allowed")
)

// relayPath is the web's relay of stream n of external channel id.
func extRelayPath(id int64, n int) string { return extStreamPath(id, n) + "/relay" }

type relayKey struct {
	user, channel int64
	n             int
}

type relaySession struct {
	inflight int
	last     time.Time
	started  time.Time
	// public: the stream's own host is a public address, so every URL it
	// leads to must be one too (no LAN or loopback through a provider).
	public bool
}

type relaySeenKey struct {
	user    int64
	device  string
	channel int64
}

type relaySeen struct {
	x     iptv.ExtChannel
	ok    bool
	until time.Time
}

type extRelay struct {
	db  *sql.DB
	now func() time.Time
	// open: the client for streams on public hosts (only public addresses
	// dialled); local: for a stream on a LAN or loopback host.
	open, local *http.Client
	resolve     func(ctx context.Context, host string) ([]net.IPAddr, error)

	keyOnce sync.Once
	aead    cipher.AEAD
	keyErr  error

	perUser, total int

	mu       sync.Mutex
	sessions map[relayKey]*relaySession
	seen     map[relaySeenKey]relaySeen
	janitor  sync.Once
}

func newExtRelay(db *sql.DB) *extRelay {
	return &extRelay{db: db, now: time.Now, open: relayClient(true), local: relayClient(false), resolve: net.DefaultResolver.LookupIPAddr,
		perUser: relayPerUser, total: relayTotal, sessions: map[relayKey]*relaySession{}, seen: map[relaySeenKey]relaySeen{}}
}

func relayClient(publicOnly bool) *http.Client {
	d := &net.Dialer{Timeout: relayConnect, KeepAlive: 30 * time.Second}
	if publicOnly {
		d.ControlContext = func(_ context.Context, _, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
				return errRelayDial
			}
			return nil
		}
	}
	tr := &http.Transport{
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   relayConnect,
		ResponseHeaderTimeout: relayHeaders,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("relay: too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errRelayRef
		}
		return nil
	}}
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip))
}

// publicHost: every address of rawURL's host is public (a failed lookup
// counts as public: the stricter client).
func (rl *extRelay) publicHost(ctx context.Context, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		return publicIP(ip)
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, relayConnect)
	defer cancel()
	ips, err := rl.resolve(ctx, host)
	if err != nil || len(ips) == 0 {
		return true
	}
	for _, a := range ips {
		if !publicIP(a.IP) {
			return false
		}
	}
	return true
}

// ---- references ----

func (rl *extRelay) cipher() (cipher.AEAD, error) {
	rl.keyOnce.Do(func() {
		v, err := auth.LoadOrCreateSetting(context.Background(), rl.db, "iptv_relay_key", 32)
		if err != nil {
			rl.keyErr = fmt.Errorf("relay key: %w", err)
			return
		}
		key, err := hex.DecodeString(v)
		if err != nil || len(key) != 32 {
			rl.keyErr = errors.New("relay key: bad iptv_relay_key setting")
			return
		}
		b, err := aes.NewCipher(key)
		if err != nil {
			rl.keyErr = err
			return
		}
		rl.aead, rl.keyErr = cipher.NewGCM(b)
	})
	return rl.aead, rl.keyErr
}

type relayRef struct {
	key relayKey
	exp time.Time
	url string
}

const relayRefVersion = 1

// seal makes the reference of ref (opaque: the URL is encrypted).
func (rl *extRelay) seal(ref relayRef) (string, error) {
	g, err := rl.cipher()
	if err != nil {
		return "", err
	}
	p := []byte{relayRefVersion}
	p = binary.BigEndian.AppendUint64(p, uint64(ref.exp.Unix()))
	p = binary.AppendUvarint(p, uint64(ref.key.user))
	p = binary.AppendUvarint(p, uint64(ref.key.channel))
	p = binary.AppendUvarint(p, uint64(ref.key.n))
	p = append(p, ref.url...)
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(g.Seal(nonce, nonce, p, nil)), nil
}

// unseal opens a reference: errRelayRef when it was not made by this
// server, was changed, or has expired.
func (rl *extRelay) unseal(s string) (relayRef, error) {
	g, err := rl.cipher()
	if err != nil {
		return relayRef{}, err
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) < g.NonceSize()+g.Overhead() {
		return relayRef{}, errRelayRef
	}
	p, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], nil)
	if err != nil || len(p) < 9 || p[0] != relayRefVersion {
		return relayRef{}, errRelayRef
	}
	ref := relayRef{exp: time.Unix(int64(binary.BigEndian.Uint64(p[1:9])), 0)}
	p = p[9:]
	var v [3]uint64
	for i := range v {
		x, k := binary.Uvarint(p)
		if k <= 0 {
			return relayRef{}, errRelayRef
		}
		v[i], p = x, p[k:]
	}
	ref.key = relayKey{user: int64(v[0]), channel: int64(v[1]), n: int(v[2])}
	ref.url = string(p)
	if !rl.now().Before(ref.exp) {
		return relayRef{}, errRelayRef
	}
	return ref, nil
}

// ---- sessions ----

// acquire counts a request of relay k (public: its stream's host class,
// asked only when the relay starts); release when the answer is done.
func (rl *extRelay) acquire(ctx context.Context, k relayKey, streamURL string) (release func(), public bool, err error) {
	rl.janitor.Do(func() { go rl.sweepLoop() })
	rl.mu.Lock()
	now := rl.now()
	rl.sweepLocked(now)
	s := rl.sessions[k]
	if s == nil {
		mine := 0
		var evict *relayKey
		for ok, os := range rl.sessions {
			if ok.user != k.user {
				continue
			}
			mine++
			if os.inflight == 0 && now.Sub(os.last) >= relayEvictAfter && (evict == nil || os.last.Before(rl.sessions[*evict].last)) {
				kk := ok
				evict = &kk
			}
		}
		if mine >= rl.perUser && evict != nil {
			rl.stopLocked(*evict, now)
			mine--
		}
		if mine >= rl.perUser || len(rl.sessions) >= rl.total {
			rl.mu.Unlock()
			return nil, false, errRelayLimit
		}
		rl.mu.Unlock()
		pub := rl.publicHost(ctx, streamURL) // outside the lock: a DNS lookup
		rl.mu.Lock()
		if s = rl.sessions[k]; s == nil {
			s = &relaySession{started: now, public: pub}
			rl.sessions[k] = s
			log.Printf("channels: relay start: channel %d stream %d user %d", k.channel, k.n, k.user)
		}
	}
	s.inflight++
	s.last = now
	public = s.public
	rl.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			rl.mu.Lock()
			s.inflight--
			s.last = rl.now()
			rl.mu.Unlock()
		})
	}, public, nil
}

func (rl *extRelay) stopLocked(k relayKey, now time.Time) {
	s := rl.sessions[k]
	delete(rl.sessions, k)
	log.Printf("channels: relay stop: channel %d stream %d user %d after %s", k.channel, k.n, k.user, now.Sub(s.started).Round(time.Second))
}

func (rl *extRelay) sweepLocked(now time.Time) {
	for k, s := range rl.sessions {
		if s.inflight == 0 && now.Sub(s.last) >= relayIdle {
			rl.stopLocked(k, now)
		}
	}
	for k, v := range rl.seen {
		if !now.Before(v.until) {
			delete(rl.seen, k)
		}
	}
}

func (rl *extRelay) sweepLoop() {
	t := time.NewTicker(relayIdle / 2)
	defer t.Stop()
	for range t.C {
		rl.mu.Lock()
		rl.sweepLocked(rl.now())
		rl.mu.Unlock()
	}
}

// visible: the viewer's view of external channel id (cached briefly: an
// HLS player asks every few seconds).
func (rl *extRelay) visible(r *http.Request, u auth.User, id int64, look func() (iptv.ExtChannel, bool)) (iptv.ExtChannel, bool) {
	k := relaySeenKey{user: u.ID, channel: id}
	if s, ok := auth.SessionFrom(r.Context()); ok {
		k.device = s.Device.ID
	}
	rl.mu.Lock()
	v, hit := rl.seen[k]
	rl.mu.Unlock()
	if hit && rl.now().Before(v.until) {
		return v.x, v.ok
	}
	x, ok := look()
	rl.mu.Lock()
	rl.seen[k] = relaySeen{x: x, ok: ok, until: rl.now().Add(relayVisibleTTL)}
	rl.mu.Unlock()
	return x, ok
}

// ---- the handler ----

// GET /api/channels/live/ext/{id}/{n}/relay[/{ref}]
func (a *channelsAPI) extRelay(w http.ResponseWriter, r *http.Request) {
	rl := a.relay
	id, err1 := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	n, err2 := strconv.Atoi(chi.URLParam(r, "n"))
	if err1 != nil || err2 != nil || n < 0 {
		writeErr(w, r, http.StatusNotFound, "Канал не найден")
		return
	}
	u := userOf(r)
	k := relayKey{user: u.ID, channel: id, n: n}
	target := ""
	if s := chi.URLParam(r, "ref"); s != "" {
		ref, err := rl.unseal(s)
		if err != nil || ref.key != k {
			if err != nil && !errors.Is(err, errRelayRef) {
				log.Printf("channels: relay: %v", err)
			}
			writeErr(w, r, http.StatusForbidden, "Ссылка на поток устарела")
			return
		}
		target = ref.url
	}
	x, ok := rl.visible(r, u, id, func() (iptv.ExtChannel, bool) { x, ok := a.extFor(r, u, "")[id]; return x, ok })
	if !ok || n >= len(x.Streams) {
		writeErr(w, r, http.StatusNotFound, "Канал не найден")
		return
	}
	s := x.Streams[n]
	if target == "" {
		target = s.URL
	}
	release, public, err := rl.acquire(r.Context(), k, s.URL)
	if err != nil {
		w.Header().Set("Retry-After", "10")
		writeErr(w, r, http.StatusTooManyRequests, "Слишком много каналов через сервер одновременно")
		return
	}
	defer release()
	rl.serve(w, r, k, s, target, public)
}

var playlistTypes = []string{"mpegurl"} // application/vnd.apple.mpegurl, application/x-mpegurl, audio/mpegurl

func (rl *extRelay) serve(w http.ResponseWriter, r *http.Request, k relayKey, s iptv.Stream, target string, public bool) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		writeErr(w, r, http.StatusBadGateway, "Источник канала не ответил")
		return
	}
	ua := s.UserAgent
	if ua == "" {
		ua = r.UserAgent() // what the browser itself would have sent
	}
	req.Header.Set("User-Agent", ua)
	if s.Referrer != "" {
		req.Header.Set("Referer", s.Referrer)
	}
	req.Header.Set("Accept", "*/*")
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	c := rl.open
	if !public {
		c = rl.local
	}
	resp, err := c.Do(req)
	if err != nil {
		if r.Context().Err() == nil {
			writeErr(w, r, http.StatusBadGateway, "Источник канала не ответил")
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		writeErr(w, r, http.StatusBadGateway, "Источник канала не ответил")
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	br := bufio.NewReaderSize(resp.Body, 32<<10)
	if isPlaylist(resp.Header.Get("Content-Type"), br) {
		t := time.AfterFunc(relayPlaylistTimeout, cancel)
		defer t.Stop()
		body, err := io.ReadAll(io.LimitReader(br, relayPlaylistMax+1))
		if err != nil || len(body) > relayPlaylistMax {
			writeErr(w, r, http.StatusBadGateway, "Источник канала не ответил")
			return
		}
		var sealErr error
		out := relayRewrite(body, resp.Request.URL, func(abs string, playlist bool) string {
			ttl := relayMediaTTL
			if playlist {
				ttl = relayPlaylistTTL
			}
			ref, err := rl.seal(relayRef{key: k, exp: rl.now().Add(ttl), url: abs})
			if err != nil {
				sealErr = err
				return ""
			}
			return extRelayPath(k.channel, k.n) + "/" + ref
		})
		if sealErr != nil {
			internalErr(w, r, "relay", sealErr)
			return
		}
		h.Set("Content-Type", "application/vnd.apple.mpegurl")
		h.Set("Content-Length", strconv.Itoa(len(out)))
		w.Write(out)
		return
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(name); v != "" {
			h.Set(name, v)
		}
	}
	rc := http.NewResponseController(w)
	stall := time.AfterFunc(relayStall, cancel) // an upstream that stops sending
	defer stall.Stop()
	_ = rc.SetWriteDeadline(time.Now().Add(relayStall))
	w.WriteHeader(resp.StatusCode)
	buf := make([]byte, 32<<10)
	for {
		m, err := br.Read(buf)
		if m > 0 {
			stall.Reset(relayStall)
			_ = rc.SetWriteDeadline(time.Now().Add(relayStall))
			if _, werr := w.Write(buf[:m]); werr != nil {
				return
			}
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

// isPlaylist: an HLS playlist by its type or its first line.
func isPlaylist(ctype string, br *bufio.Reader) bool {
	ct := strings.ToLower(ctype)
	for _, t := range playlistTypes {
		if strings.Contains(ct, t) {
			return true
		}
	}
	if strings.HasPrefix(ct, "video/") || strings.HasPrefix(ct, "audio/") {
		return false
	}
	head, _ := br.Peek(16)
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	return bytes.HasPrefix(bytes.TrimLeft(head, " \t\r\n"), []byte("#EXTM3U"))
}

// relayRewrite points every URI of an HLS playlist (resolved against
// base, the playlist's URL after redirects) to link(absolute URL,
// playlist?). A URI that is not http(s) (data:, skd:) stays as it is.
func relayRewrite(body []byte, base *url.URL, link func(abs string, playlist bool) string) []byte {
	master := bytes.Contains(body, []byte("#EXT-X-STREAM-INF")) || bytes.Contains(body, []byte("#EXT-X-MEDIA:")) ||
		bytes.Contains(body, []byte("#EXT-X-I-FRAME-STREAM-INF"))
	resolve := func(ref string) string {
		u, err := base.Parse(strings.TrimSpace(ref))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return ref
		}
		return link(u.String(), master)
	}
	var out bytes.Buffer
	out.Grow(len(body) * 2)
	lines := strings.Split(strings.TrimPrefix(string(body), "\xef\xbb\xbf"), "\n")
	for i, line := range lines {
		l := strings.TrimRight(line, "\r")
		switch {
		case strings.TrimSpace(l) == "":
		case strings.HasPrefix(l, "#"):
			if strings.Contains(l, `URI="`) {
				l = uriAttr.ReplaceAllStringFunc(l, func(m string) string {
					return `URI="` + resolve(m[len(`URI="`):len(m)-1]) + `"`
				})
			}
		default:
			l = resolve(l)
		}
		out.WriteString(l)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.Bytes()
}
