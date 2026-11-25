package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"io"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/iptv"
	"github.com/antresol-app/antresol/internal/media"
)

// The relay mends silent channels. An IPTV provider's MPEG-TS often carries
// audio that browsers and apps cannot decode (MP2, AC-3, E-AC-3, DTS): the
// picture plays and there is no sound. The relay looks at the start of each
// stream once, remembers what it found, and for a stream with audio clients
// cannot play it runs the body through ffmpeg: the video is copied as it
// is, the audio is encoded as AAC stereo (internal/media/audiofix.go). A
// stream whose audio is fine is never touched, and costs nothing but the
// look.
//
// ffmpeg never opens an address. The relay reads the stream itself, with the
// same guarded clients as for everything else it fetches (netguard), and
// hands ffmpeg the bytes on its standard input, with the pipe as the only
// protocol and MPEG-TS the only demuxer. An HLS stream is mended segment by
// segment, its master playlist's CODECS made to say AAC.
//
// The decision is per stream (its provider URL), made once, from the first
// transport-stream body the relay serves for it or from a look the server
// takes when a viewer tunes in (warm), and kept relayAudioTTL. A stream
// marked as mended is told to clients (streamJSON.RemuxAudio with the codec
// in AudioCodec, and "web" is then not a platform that plays it directly):
// the web plays it through the relay.

const (
	// relayAudioHead: how much of a stream's start is looked at, and
	// relayAudioWait: how long a slow stream is waited for it (what has come
	// by then is looked at).
	relayAudioHead = 256 << 10
	relayAudioWait = 2 * time.Second
	// relayAudioProbe bounds one ffprobe.
	relayAudioProbe = 15 * time.Second
	// relayAudioTTL: a decision with audio in it; relayAudioRetry: one
	// without (no audio seen, a failed look): looked at again soon.
	relayAudioTTL   = 12 * time.Hour
	relayAudioRetry = 5 * time.Minute
	// relayAudioKeep: streams remembered at most.
	relayAudioKeep = 4096
	// relayWarmTimeout bounds the look a tune-in takes, relayLooks how many
	// go on at once.
	relayWarmTimeout = 25 * time.Second
	relayLooks       = 4
)

// relayMendMax: conversions at once; the rest go out as they are (a
// variable for the tests).
var relayMendMax = 6

type audioMemo struct {
	mend bool
	// codec: with mend, the first audio codec that is converted (ffprobe's
	// name; "" when it gave none).
	codec string
	until time.Time
}

// relayAudio remembers, per stream, whether its audio is mended.
type relayAudio struct {
	mu      sync.Mutex
	memo    map[[32]byte]audioMemo
	looking map[[32]byte]bool
}

func streamKey(s iptv.Stream) [32]byte { return sha256.Sum256([]byte(s.URL)) }

func (c *relayAudio) get(k [32]byte, now time.Time) (mend bool, codec string, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.memo[k]
	return m.mend, m.codec, ok && now.Before(m.until)
}

func (c *relayAudio) set(k [32]byte, mend bool, codec string, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.memo == nil {
		c.memo = map[[32]byte]audioMemo{}
	}
	if len(c.memo) >= relayAudioKeep {
		now := time.Now()
		for k, m := range c.memo {
			if !now.Before(m.until) {
				delete(c.memo, k)
			}
		}
		if len(c.memo) >= relayAudioKeep { // all current: forget them, they are looked at again
			clear(c.memo)
		}
	}
	c.memo[k] = audioMemo{mend, codec, until}
}

// begin takes the look at k, false when one is going on.
func (c *relayAudio) begin(k [32]byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.looking[k] {
		return false
	}
	if c.looking == nil {
		c.looking = map[[32]byte]bool{}
	}
	c.looking[k] = true
	return true
}

func (c *relayAudio) end(k [32]byte) {
	c.mu.Lock()
	delete(c.looking, k)
	c.mu.Unlock()
}

// toolsOK: ffmpeg and ffprobe are there (once).
func (rl *extRelay) toolsOK() bool {
	rl.toolsOnce.Do(func() {
		_, e1 := exec.LookPath(rl.ffmpegPath())
		_, e2 := exec.LookPath(rl.ffprobePath())
		rl.tools = e1 == nil && e2 == nil
		if !rl.tools {
			log.Printf("channels: relay: no ffmpeg or ffprobe, audio of streams is left as it is")
		}
	})
	return rl.tools
}

func (rl *extRelay) ffmpegPath() string {
	if rl.ffmpeg != "" {
		return rl.ffmpeg
	}
	return "ffmpeg"
}

func (rl *extRelay) ffprobePath() string {
	if rl.ffprobe != "" {
		return rl.ffprobe
	}
	return "ffprobe"
}

// mended: stream s is known to have audio that gets converted, and the first
// codec of it that is (see media.ProbeResult.FirstAudioToFix).
func (rl *extRelay) mended(s iptv.Stream) (mend bool, codec string) {
	mend, codec, known := rl.audio.get(streamKey(s), rl.now())
	return known && mend, codec
}

// learn looks at the start of stream s (head) and remembers what it found;
// whether the stream is mended.
func (rl *extRelay) learn(ctx context.Context, s iptv.Stream, head []byte) bool {
	k := streamKey(s)
	ctx, cancel := context.WithTimeout(ctx, relayAudioProbe)
	defer cancel()
	probe := rl.probe
	if probe == nil {
		probe = func(ctx context.Context, head []byte) (media.ProbeResult, error) {
			return media.ProbeTSHead(ctx, rl.ffprobePath(), head)
		}
	}
	p, err := probe(ctx, head)
	if err != nil || len(p.Audio) == 0 {
		if err != nil && ctx.Err() == nil {
			log.Printf("channels: relay: the start of a stream could not be looked at: %v", err)
		}
		rl.audio.set(k, false, "", rl.now().Add(relayAudioRetry))
		return false
	}
	codec, mend := p.FirstAudioToFix()
	rl.audio.set(k, mend, codec, rl.now().Add(relayAudioTTL))
	if mend {
		var names []string
		for _, a := range p.Audio {
			names = append(names, a.Codec)
		}
		log.Printf("channels: relay: audio of a stream (%s) is converted to AAC", strings.Join(names, ", "))
	}
	return mend
}

// audioRoute decides how a body goes out: converted (true) or as it is; src
// is what is left of it to read, stop lets go of it. Only a transport stream
// is looked at or converted: a key, an init segment or a progressive file
// goes as it is.
func (rl *extRelay) audioRoute(ctx context.Context, s iptv.Stream, br *bufio.Reader) (src io.Reader, mend bool, stop func()) {
	src, stop = br, func() {}
	if !rl.toolsOK() {
		return
	}
	k := streamKey(s)
	mend, _, known := rl.audio.get(k, rl.now())
	if known && !mend {
		return
	}
	if head, _ := br.Peek(3 * 188); !media.LooksLikeTS(head) {
		return src, false, stop
	}
	if known {
		return src, true, stop
	}
	h := newHeadReader(br)
	mend = rl.learn(ctx, s, h.fill(relayAudioHead, relayAudioWait))
	return h, mend, h.Close
}

// warm takes the look at stream s in the background, when a viewer tunes in:
// the web and the apps then learn it before they play the stream again.
func (rl *extRelay) warm(s iptv.Stream, userAgent string) {
	k := streamKey(s)
	if !rl.toolsOK() || !rl.audio.begin(k) {
		return
	}
	if _, _, known := rl.audio.get(k, rl.now()); known {
		rl.audio.end(k)
		return
	}
	select {
	case rl.looks <- struct{}{}:
	default: // busy: the next tune-in tries again
		rl.audio.end(k)
		return
	}
	go func() {
		defer func() { <-rl.looks; rl.audio.end(k) }()
		ctx, cancel := context.WithTimeout(context.Background(), relayWarmTimeout)
		defer cancel()
		rl.lookAt(ctx, s, userAgent)
		if _, _, known := rl.audio.get(k, rl.now()); !known { // a look that found nothing is not repeated at once
			rl.audio.set(k, false, "", rl.now().Add(relayAudioRetry))
		}
	}()
}

// lookAt follows stream s to the start of its first transport-stream body (an
// HLS playlist leads to its audio rendition or first variant, or to the last
// segment of a media playlist) and learns from it.
func (rl *extRelay) lookAt(ctx context.Context, s iptv.Stream, userAgent string) {
	lan := rl.sourceLAN != nil && rl.sourceLAN(ctx, s.SourceID)
	target := s.URL
	for hop := 0; hop < 4; hop++ {
		resp, err := rl.fetch(ctx, s, target, userAgent, "", lan)
		if err != nil {
			return
		}
		br := bufio.NewReaderSize(resp.Body, relayAudioHead)
		playlist, err := isPlaylist(resp.Header.Get("Content-Type"), br)
		if err != nil || resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return
		}
		if !playlist {
			head := newHeadReader(br)
			b := head.fill(relayAudioHead, relayAudioWait)
			head.Close()
			resp.Body.Close()
			if media.LooksLikeTS(b) {
				rl.learn(ctx, s, b)
			} else {
				rl.audio.set(streamKey(s), false, "", rl.now().Add(relayAudioRetry))
			}
			return
		}
		body, _ := io.ReadAll(io.LimitReader(br, relayPlaylistMax))
		base := resp.Request.URL
		resp.Body.Close()
		if target = playlistProbeTarget(body, base); target == "" {
			return
		}
	}
}

// playlistProbeTarget is the address in an HLS playlist to look at next: a
// master's first audio rendition, else its first variant; a media
// playlist's last segment (the first may be gone already). "": none.
func playlistProbeTarget(body []byte, base *url.URL) string {
	var variant, last string
	next := false
	for _, line := range strings.Split(string(body), "\n") {
		l := strings.TrimSpace(line)
		switch {
		case l == "":
		case strings.HasPrefix(l, "#EXT-X-MEDIA:") && strings.Contains(l, "TYPE=AUDIO"):
			if m := uriAttr.FindString(l); m != "" {
				return resolveHTTP(base, m[len(`URI="`):len(m)-1])
			}
		case strings.HasPrefix(l, "#EXT-X-STREAM-INF"):
			next = true
		case strings.HasPrefix(l, "#"):
		case next && variant == "":
			variant, next = l, false
		default:
			last = l
		}
	}
	if variant != "" {
		return resolveHTTP(base, variant)
	}
	return resolveHTTP(base, last)
}

func resolveHTTP(base *url.URL, ref string) string {
	u, err := base.Parse(strings.TrimSpace(ref))
	if err != nil || ref == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

// serveRemux answers with the body converted. false: it was not (all the
// conversions are busy), and nothing is written: the body goes out as it is.
func (rl *extRelay) serveRemux(w http.ResponseWriter, r *http.Request, ctx context.Context, src io.Reader, stall *time.Timer) bool {
	select {
	case rl.slots <- struct{}{}:
		defer func() { <-rl.slots }()
	default:
		log.Printf("channels: relay: %d conversions are running, audio goes as it is", cap(rl.slots))
		return false
	}
	out := &remuxWriter{w: w, rc: http.NewResponseController(w), stall: stall}
	err := media.RemuxAudio(ctx, rl.ffmpegPath(), src, out)
	switch {
	case err != nil && ctx.Err() == nil:
		log.Printf("channels: relay: audio conversion: %v", err)
		if !out.started {
			writeErr(w, r, http.StatusBadGateway, "Источник канала не ответил")
		}
	case err == nil && !out.started:
		out.start()
	}
	return true
}

// remuxWriter writes ffmpeg's output to the response, the answer's header
// with the first of it: a conversion that fails before that is still an
// error to answer.
type remuxWriter struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	stall   *time.Timer
	started bool
}

func (f *remuxWriter) start() {
	f.started = true
	f.w.Header().Set("Content-Type", "video/mp2t")
	f.w.WriteHeader(http.StatusOK)
}

func (f *remuxWriter) Write(p []byte) (int, error) {
	if !f.started {
		f.start()
	}
	f.stall.Reset(relayStall)
	_ = f.rc.SetWriteDeadline(time.Now().Add(relayStall))
	n, err := f.w.Write(p)
	_ = f.rc.Flush()
	return n, err
}

// headReader reads a body in a goroutine of its own, so that waiting for its
// start has a limit (a slow stream is not held back for a look at it): fill
// collects the start, and Read then yields it, and the rest.
type headReader struct {
	ch   chan bodyChunk
	stop chan struct{}
	once sync.Once
	buf  []byte
	err  error
}

type bodyChunk struct {
	b   []byte
	err error
}

func newHeadReader(r io.Reader) *headReader {
	h := &headReader{ch: make(chan bodyChunk), stop: make(chan struct{})}
	go func() {
		for {
			b := make([]byte, 32<<10)
			n, err := r.Read(b)
			select {
			case h.ch <- bodyChunk{b[:n], err}:
			case <-h.stop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return h
}

// fill waits until n bytes of the body are held, or d has passed, or it has
// ended, and returns what is held.
func (h *headReader) fill(n int, d time.Duration) []byte {
	t := time.NewTimer(d)
	defer t.Stop()
	for len(h.buf) < n && h.err == nil {
		select {
		case c := <-h.ch:
			h.buf = append(h.buf, c.b...)
			h.err = c.err
		case <-t.C:
			return h.buf
		}
	}
	return h.buf
}

func (h *headReader) Read(p []byte) (int, error) {
	for len(h.buf) == 0 {
		if h.err != nil {
			return 0, h.err
		}
		c := <-h.ch
		h.buf, h.err = c.b, c.err
	}
	n := copy(p, h.buf)
	h.buf = h.buf[n:]
	return n, nil
}

// Close lets go of the goroutine (when the body is not read to its end).
func (h *headReader) Close() { h.once.Do(func() { close(h.stop) }) }

var reCodecs = regexp.MustCompile(`(?i)CODECS="([^"]*)"`)

// unplayableCodecs are the audio codec names of an HLS CODECS attribute
// (RFC 6381) a converted stream no longer has.
var unplayableCodecs = []string{"ac-3", "ec-3", "ac-4", "mp4a.a5", "mp4a.a6", "dtsc", "dtse", "dtsh", "dtsl"}

// relayFixCodecs says AAC where a master playlist's CODECS name an audio
// codec the converted stream no longer has: a player that finds no codec it
// can play in the list does not start.
func relayFixCodecs(pl []byte) []byte {
	return reCodecs.ReplaceAllFunc(pl, func(m []byte) []byte {
		var out []string
		for _, c := range strings.Split(string(m[len(`CODECS="`):len(m)-1]), ",") {
			c = strings.TrimSpace(c)
			if slices.Contains(unplayableCodecs, strings.ToLower(c)) {
				c = "mp4a.40.2"
			}
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
		return []byte(`CODECS="` + strings.Join(out, ",") + `"`)
	})
}
