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
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/iptv"
	"github.com/antresol-app/antresol/internal/netguard"
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
// relayPerUser streams at once, the server relayTotal, and a stream answers
// at most relayInflight requests at a time.

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
	relayConnect         = 10 * time.Second
	relayHeaders         = 15 * time.Second
	// relayPlaylistTTL: a variant playlist's reference lasts the viewing: a
	// player reloads it for as long as it plays and never asks the master
	// again, and the server cannot hand it a new one. Nothing is lost by
	// it being long: the reference opens only for the viewer it was made for,
	// on the channel it was made for, while that channel is still theirs to
	// see (checked on every request), and only for the URL the playlist
	// linked. A new master reload makes new ones.
	// relayMediaTTL: a segment's, key's or map's, until the next reload.
	relayPlaylistTTL = 30 * 24 * time.Hour
	relayMediaTTL    = 10 * time.Minute
	relayVisibleTTL  = 30 * time.Second
)

// relayStall: a provider that sends nothing for this long is cut off, and a
// client that takes nothing for it (a variable for the tests).
var relayStall = 30 * time.Second

// relayInflight: requests on one stream at once (a player asks for a
// playlist, its renditions, a segment and a key in parallel, not dozens).
const relayInflight = 8

var (
	errRelayRef   = errors.New("relay: bad reference")
	errRelayLimit = errors.New("relay: too many streams")
	errRelayDial  = netguard.ErrDial
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
	// lan: the stream's source is on the LAN (its playlist address is), so
	// its streams may dial the LAN's addresses too; otherwise public ones
	// only (internal/netguard). Set before ready is closed.
	lan   bool
	ready chan struct{}
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
	// open: the client for streams of a source on the internet (public
	// addresses only); local: for those of a source on the LAN.
	open, local *http.Client
	// sourceLAN: whether the playlist address the admin typed for source id
	// is the LAN's (nil: no source is).
	sourceLAN func(ctx context.Context, sourceID int64) bool

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
	return &extRelay{db: db, now: time.Now, open: relayClient(false), local: relayClient(true),
		perUser: relayPerUser, total: relayTotal, sessions: map[relayKey]*relaySession{}, seen: map[relaySeenKey]relaySeen{}}
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

// acquire counts a request of relay k (lan: whether its stream's source is
// on the LAN, asked only when the relay starts); release when the answer
// is done. A new relay takes its place under the lock before anything is
// asked outside it, so starts at once cannot pass the limits together; a
// request that finds it still starting waits for the answer.
func (rl *extRelay) acquire(ctx context.Context, k relayKey, sourceID int64) (release func(), lan bool, err error) {
	rl.janitor.Do(func() { go rl.sweepLoop() })
	rl.mu.Lock()
	now := rl.now()
	rl.sweepLocked(now)
	s := rl.sessions[k]
	fresh := s == nil
	if fresh {
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
		// at the viewer's limit an idle relay of theirs gives its place, but
		// only when the new one is admitted
		freed := 0
		if mine >= rl.perUser {
			if evict == nil {
				rl.mu.Unlock()
				return nil, false, errRelayLimit
			}
			freed = 1
		}
		if len(rl.sessions)-freed >= rl.total {
			rl.mu.Unlock()
			return nil, false, errRelayLimit
		}
		if freed > 0 {
			rl.stopLocked(*evict, now)
		}
		s = &relaySession{started: now, ready: make(chan struct{})}
		rl.sessions[k] = s
		log.Printf("channels: relay start: channel %d stream %d user %d", k.channel, k.n, k.user)
	}
	if s.inflight >= relayInflight {
		rl.mu.Unlock()
		return nil, false, errRelayLimit
	}
	s.inflight++
	s.last = now
	rl.mu.Unlock()
	var once sync.Once
	release = func() {
		once.Do(func() {
			rl.mu.Lock()
			s.inflight--
			s.last = rl.now()
			rl.mu.Unlock()
		})
	}
	if fresh {
		func() {
			defer close(s.ready)
			s.lan = rl.sourceLAN != nil && rl.sourceLAN(ctx, sourceID) // outside the lock: the store
		}()
	} else {
		select {
		case <-s.ready:
		case <-ctx.Done():
			release()
			return nil, false, ctx.Err()
		}
	}
	return release, s.lan, nil
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

// sourceOnLAN: the playlist address the admin typed for source id is the
// LAN's by its spelling (internal/netguard). Anything unknown is not.
func (a *channelsAPI) sourceOnLAN(ctx context.Context, id int64) bool {
	if a.ext == nil || a.ext.Store == nil || id == 0 {
		return false
	}
	src, err := a.ext.Store.Source(ctx, id)
	if err != nil {
		return false
	}
	raw, err := a.ext.PlaylistURL(ctx, src)
	if err != nil {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && netguard.LANSpelling(u.Hostname())
}

// ---- the handler ----

// GET /api/channels/live/ext/{id}/{n}/relay[/{ref}]
func (a *channelsAPI) extRelay(w http.ResponseWriter, r *http.Request) {
	relayHardenHeaders(w)
	rl := a.relay
	id, valid := urlID(r, "id")
	n, err := strconv.Atoi(chi.URLParam(r, "n"))
	if !valid || err != nil || n < 0 {
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
	release, lan, err := rl.acquire(r.Context(), k, s.SourceID)
	if err != nil {
		if r.Context().Err() != nil {
			return // the viewer went away while the relay started
		}
		w.Header().Set("Retry-After", "10")
		writeErr(w, r, http.StatusTooManyRequests, "Слишком много каналов через сервер одновременно")
		return
	}
	defer release()
	rl.serve(w, r, k, s, target, lan)
}

// relayCSP: nothing a relayed body says may run as a page of ours (a
// provider's HTML or SVG opened as a link, same-origin with the session).
// The sibling relays (input_relay.go, addons/relay.go) do the same.
const relayCSP = "sandbox; default-src 'none'"

// relayHardenHeaders: on every answer of the relay, errors included.
func relayHardenHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", relayCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
}

// relayType is the Content-Type a piped body goes out with: its media type
// when that is video or audio (MPEG-TS, MP4, AAC, ...) or a plain binary
// (keys, init segments), else application/octet-stream. Parameters
// (charset) are dropped; the provider's word never makes a page.
func relayType(ctype string) string {
	mt, _, err := mime.ParseMediaType(ctype)
	if err == nil && (strings.HasPrefix(mt, "video/") || strings.HasPrefix(mt, "audio/") ||
		mt == "application/octet-stream" || mt == "application/mp4") {
		return mt
	}
	return "application/octet-stream"
}

var playlistTypes = []string{"mpegurl"} // application/vnd.apple.mpegurl, application/x-mpegurl, audio/mpegurl

func (rl *extRelay) serve(w http.ResponseWriter, r *http.Request, k relayKey, s iptv.Stream, target string, lan bool) {
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
	if lan {
		c = rl.local
	}
	resp, err := c.Do(req)
	if err != nil {
		if errors.Is(err, errRelayDial) {
			// the admin can tell a source on the internet that streams from the LAN
			log.Printf("channels: relay: channel %d stream %d (source %d): address not allowed", k.channel, k.n, s.SourceID)
		}
		if r.Context().Err() == nil {
			writeErr(w, r, http.StatusBadGateway, "Источник канала не ответил")
		}
		return
	}
	defer resp.Body.Close()
	// a provider that stops sending is cut off, from the headers on (the
	// first read below included: reset with every chunk, set to the
	// playlist's own limit for a playlist)
	stall := time.AfterFunc(relayStall, cancel)
	defer stall.Stop()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		writeErr(w, r, http.StatusBadGateway, "Источник канала не ответил")
		return
	}
	h := w.Header()
	br := bufio.NewReaderSize(resp.Body, 32<<10)
	playlist, err := isPlaylist(resp.Header.Get("Content-Type"), br)
	if err != nil {
		if r.Context().Err() == nil {
			writeErr(w, r, http.StatusBadGateway, "Источник канала не ответил")
		}
		return
	}
	if playlist {
		stall.Reset(relayPlaylistTimeout)
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
	// only these of the provider's headers pass (no cookie, no CSP, no
	// download name); its type only as a media type (relayType)
	for _, name := range []string{"Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(name); v != "" {
			h.Set(name, v)
		}
	}
	h.Set("Content-Type", relayType(resp.Header.Get("Content-Type")))
	rc := http.NewResponseController(w)
	stall.Reset(relayStall)
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

// isPlaylist: an HLS playlist by its type or its first line. The error is
// a read that failed before that line was seen (not a short body).
func isPlaylist(ctype string, br *bufio.Reader) (bool, error) {
	ct := strings.ToLower(ctype)
	for _, t := range playlistTypes {
		if strings.Contains(ct, t) {
			return true, nil
		}
	}
	if strings.HasPrefix(ct, "video/") || strings.HasPrefix(ct, "audio/") {
		return false, nil
	}
	head, err := br.Peek(16)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	return bytes.HasPrefix(bytes.TrimLeft(head, " \t\r\n"), []byte("#EXTM3U")), nil
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
