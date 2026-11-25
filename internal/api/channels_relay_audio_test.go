package api

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/iptv"
	"github.com/antresol-app/antresol/internal/media"
)

// genTS is 3 s of MPEG-TS: a test picture (H.264) and a tone in codec.
func genTS(t *testing.T, codec string) []byte {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out := filepath.Join(t.TempDir(), "in.ts")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "3", "-c:v", "libx264", "-preset", "ultrafast", "-g", "25",
		"-pix_fmt", "yuv420p", "-c:a", codec, "-b:a", "128k", "-ac", "2", "-f", "mpegts", out}
	if b, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Skipf("this ffmpeg cannot make %s audio: %v\n%s", codec, err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// tsInfo is what the relay's answer is made of.
func tsInfo(t *testing.T, b []byte) media.ProbeResult {
	t.Helper()
	p, err := media.ProbeTSHead(context.Background(), "ffprobe", b)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func videoPackets(t *testing.T, b []byte) string {
	t.Helper()
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-protocol_whitelist", "pipe", "-f", "mpegts",
		"-show_entries", "packet=size,pts,dts", "-of", "csv", "-i", "pipe:0")
	cmd.Stdin = bytes.NewReader(b)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// audioUpstream is a provider of transport streams: one file per codec as a
// raw stream (/ts/<codec>), as an HLS stream (/hls/<codec>/...), and as a
// stream that never ends (/hold/<codec>: the file, then silence).
type audioUpstream struct {
	srv   *httptest.Server
	files map[string][]byte
	mu    sync.Mutex
	hits  map[string]int
}

func newAudioUpstream(t *testing.T, codecs ...string) *audioUpstream {
	up := &audioUpstream{files: map[string][]byte{}, hits: map[string]int{}}
	for _, c := range codecs {
		ts := genTS(t, c)
		up.files["/ts/"+c] = ts
		up.files["/hold/"+c] = ts
		up.files["/hls/"+c+"/seg1.ts"] = ts
		up.files["/hls/"+c+"/master.m3u8"] = []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000,CODECS=\"avc1.42c01e,ac-3\"\nv.m3u8\n")
		up.files["/hls/"+c+"/v.m3u8"] = []byte("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:3,\nseg1.ts\n")
	}
	up.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.mu.Lock()
		up.hits[r.URL.Path]++
		up.mu.Unlock()
		b, ok := up.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		} else {
			w.Header().Set("Content-Type", "video/mp2t")
		}
		if !strings.HasPrefix(r.URL.Path, "/hold/") {
			w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		}
		w.Write(b)
		if strings.HasPrefix(r.URL.Path, "/hold/") {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	t.Cleanup(up.srv.Close)
	return up
}

// audioEnv: a source with, per codec, a raw channel ("TS mp2"), an HLS one
// ("HLS mp2") and one that never ends ("HOLD mp2").
func newAudioEnv(t *testing.T, opt func(*Deps), codecs ...string) (*tvEnv, *audioUpstream, map[string]channelJSON) {
	up := newAudioUpstream(t, codecs...)
	e := newTVEnvOpt(t, opt)
	pl := "#EXTM3U\n#EXTINF:-1,MISSING\n" + up.srv.URL + "/missing/x\n"
	for _, c := range codecs {
		pl += "#EXTINF:-1,TS " + c + "\n" + up.srv.URL + "/ts/" + c + "\n" +
			"#EXTINF:-1,HLS " + c + "\n" + up.srv.URL + "/hls/" + c + "/master.m3u8\n" +
			"#EXTINF:-1,HOLD " + c + "\n" + up.srv.URL + "/hold/" + c + "\n"
	}
	return e, up, e.addRelaySource(pl, e.p.srv.URL)
}

// newTVEnvOpt is newTVEnv with the router's Deps adjusted.
func newTVEnvOpt(t *testing.T, opt func(*Deps)) *tvEnv {
	if opt == nil {
		return newTVEnv(t)
	}
	return newTVEnvWith(t, nil, func(d *Deps, _ *sql.DB) { opt(d) })
}

func (up *audioUpstream) asked(path string) int {
	up.mu.Lock()
	defer up.mu.Unlock()
	return up.hits[path]
}

func (e *tvEnv) relayGet(path string) *httptest.ResponseRecorder {
	e.t.Helper()
	w := e.do("GET", path, "", e.admin)
	if w.Code != 200 {
		e.t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
	}
	return w
}

// A stream whose audio clients cannot decode comes out of the relay with the
// video as it was and the audio as AAC stereo.
func TestRelayConvertsAudioClientsCannotPlay(t *testing.T) {
	e, up, chans := newAudioEnv(t, nil, "mp2", "ac3")
	for _, codec := range []string{"mp2", "ac3"} {
		t.Run(codec, func(t *testing.T) {
			w := e.relayGet(chans["TS "+codec].Stream.Relay)
			src := up.files["/ts/"+codec]
			got := tsInfo(t, w.Body.Bytes())
			if got.Video == nil || got.Video.Codec != "h264" || len(got.Audio) != 1 || got.Audio[0].Codec != "aac" || got.Audio[0].Channels != 2 {
				t.Fatalf("answer: video %+v audio %+v", got.Video, got.Audio)
			}
			if a, b := videoPackets(t, src), videoPackets(t, w.Body.Bytes()); a != b {
				t.Errorf("the video was changed:\nsource %s\nanswer %s", a, b)
			}
			h := w.Header()
			if h.Get("Content-Type") != "video/mp2t" || h.Get("Content-Length") != "" || h.Get("Content-Security-Policy") == "" || h.Get("Cache-Control") != "no-store" {
				t.Errorf("headers %v", h)
			}
		})
	}
}

// AAC and MP3 are not touched: the bytes are the provider's.
func TestRelayLeavesPlayableAudioAlone(t *testing.T) {
	e, up, chans := newAudioEnv(t, nil, "aac", "libmp3lame")
	for _, codec := range []string{"aac", "libmp3lame"} {
		w := e.relayGet(chans["TS "+codec].Stream.Relay)
		if !bytes.Equal(w.Body.Bytes(), up.files["/ts/"+codec]) || w.Header().Get("Content-Length") != strconv.Itoa(len(up.files["/ts/"+codec])) {
			t.Errorf("%s: %d bytes came out of %d", codec, w.Body.Len(), len(up.files["/ts/"+codec]))
		}
	}
	if l := e.list(e.admin); len(l.Channels) == 0 {
		t.Fatal("no channels")
	} else {
		for _, c := range l.Channels {
			if c.Stream != nil && (c.Stream.RemuxAudio || len(c.Stream.Unsupported) != 0) {
				t.Errorf("%s is told to need mending: %+v", c.Name, c.Stream)
			}
		}
	}
}

// ffprobe is run once per stream, not once per request (a wrapper of ffprobe
// that counts its runs).
func TestRelayLooksAtAStreamOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script")
	}
	real, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	count := filepath.Join(t.TempDir(), "runs")
	wrapper := filepath.Join(t.TempDir(), "ffprobe")
	script := "#!/bin/sh\necho run >> '" + count + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e, _, chans := newAudioEnv(t, func(d *Deps) { d.FFprobe = wrapper }, "mp2", "aac")
	runs := func() int {
		b, _ := os.ReadFile(count)
		return strings.Count(string(b), "run")
	}
	for i := 0; i < 3; i++ {
		e.relayGet(chans["TS mp2"].Stream.Relay)
	}
	if n := runs(); n != 1 {
		t.Errorf("ffprobe ran %d times for 3 requests of one stream", n)
	}
	for i := 0; i < 3; i++ {
		e.relayGet(chans["TS aac"].Stream.Relay)
	}
	if n := runs(); n != 2 {
		t.Errorf("ffprobe ran %d times for two streams", n)
	}
}

// What the relay found is told to the clients: the web plays such a stream
// through the relay, an app may too.
func TestStreamListSaysWhichAudioIsMended(t *testing.T) {
	e, _, chans := newAudioEnv(t, nil, "mp2", "aac")
	state := func(name string) (bool, []string) {
		t.Helper()
		for _, c := range e.list(e.admin).Channels {
			if c.Name == name {
				return c.Stream.RemuxAudio, c.Stream.Unsupported
			}
		}
		t.Fatalf("no channel %s", name)
		return false, nil
	}
	if m, u := state("TS mp2"); m || len(u) != 0 {
		t.Fatalf("told before anyone looked: %v %v", m, u)
	}
	e.relayGet(chans["TS mp2"].Stream.Relay)
	e.relayGet(chans["TS aac"].Stream.Relay)
	if m, u := state("TS mp2"); !m || len(u) != 1 || u[0] != "web" {
		t.Errorf("mp2: %v %v", m, u)
	}
	if m, u := state("TS aac"); m || len(u) != 0 {
		t.Errorf("aac: %v %v", m, u)
	}
}

// The list also says which codec the audio is, so that an app that decodes
// some of the codecs itself (AC-3) plays such a stream as it is.
func TestStreamListSaysTheAudioCodec(t *testing.T) {
	e, _, chans := newAudioEnv(t, nil, "mp2", "ac3", "aac")
	for _, n := range []string{"TS mp2", "TS ac3", "TS aac"} {
		e.relayGet(chans[n].Stream.Relay)
	}
	want := map[string]string{"TS mp2": "mp2", "TS ac3": "ac3", "TS aac": ""}
	for _, c := range e.list(e.admin).Channels {
		if w, ok := want[c.Name]; ok && c.Stream.AudioCodec != w {
			t.Errorf("%s: audioCodec %q, want %q (remuxAudio %v)", c.Name, c.Stream.AudioCodec, w, c.Stream.RemuxAudio)
		}
	}
}

// What a stream is said to be: the first audio codec the relay converts
// ("" when the probe could not name it: the flag alone is then the word).
func TestTheCodecToldIsTheFirstOneConverted(t *testing.T) {
	tracks := func(codecs ...string) func(context.Context, []byte) (media.ProbeResult, error) {
		return func(context.Context, []byte) (media.ProbeResult, error) {
			var p media.ProbeResult
			for _, c := range codecs {
				p.Audio = append(p.Audio, media.AudioStream{Codec: c})
			}
			return p, nil
		}
	}
	for _, c := range []struct {
		name  string
		probe func(context.Context, []byte) (media.ProbeResult, error)
		mend  bool
		codec string
	}{
		{"one track", tracks("ac3"), true, "ac3"},
		{"playable first", tracks("aac", "dts"), true, "dts"},
		{"two converted", tracks("eac3", "mp2"), true, "eac3"},
		{"all playable", tracks("aac", "mp3"), false, ""},
		{"unnamed", tracks(""), true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			rl := newExtRelay(nil)
			rl.probe = c.probe
			s := iptv.Stream{URL: "http://p.example.test/a"}
			rl.learn(context.Background(), s, nil)
			mend, codec := rl.mended(s)
			if mend != c.mend || codec != c.codec {
				t.Errorf("mended = %v %q, want %v %q", mend, codec, c.mend, c.codec)
			}
		})
	}
}

// Tuning in takes a look at the channel's audio in the background, so that
// the list says so before the next play: of a raw stream and of an HLS one
// (down its master and media playlists to a segment).
func TestTuningInLooksAtTheAudio(t *testing.T) {
	e, up, chans := newAudioEnv(t, nil, "mp2")
	for _, name := range []string{"TS mp2", "HLS mp2"} {
		c := chans[name]
		if w := e.do("PUT", "/api/channels/last", `{"channelId":`+itoa(c.ID)+`}`, e.admin); w.Code != 204 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			var got bool
			for _, x := range e.list(e.admin).Channels {
				if x.ID == c.ID {
					got = x.Stream.RemuxAudio
				}
			}
			if got {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: not told after the look", name)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if up.asked("/hls/mp2/master.m3u8") != 1 || up.asked("/hls/mp2/v.m3u8") != 1 || up.asked("/hls/mp2/seg1.ts") != 1 {
		t.Errorf("the look of the HLS channel: %d %d %d requests", up.asked("/hls/mp2/master.m3u8"), up.asked("/hls/mp2/v.m3u8"), up.asked("/hls/mp2/seg1.ts"))
	}
}

// A look that found nothing (the provider does not answer) is not taken
// again with the next tune-in.
func TestALookThatFoundNothingIsNotRepeatedAtOnce(t *testing.T) {
	e, up, chans := newAudioEnv(t, nil, "mp2")
	tune := func() {
		if w := e.do("PUT", "/api/channels/last", `{"channelId":`+itoa(chans["MISSING"].ID)+`}`, e.admin); w.Code != 204 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	tune()
	deadline := time.Now().Add(10 * time.Second)
	for up.asked("/missing/x") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no look")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	tune()
	time.Sleep(300 * time.Millisecond)
	if n := up.asked("/missing/x"); n != 1 {
		t.Errorf("the provider was asked %d times", n)
	}
}

// An HLS stream is converted segment by segment, and its master playlist
// stops naming the codec that is gone.
func TestRelayConvertsHLSSegmentsAndFixesCodecs(t *testing.T) {
	e, _, chans := newAudioEnv(t, nil, "mp2")
	c := chans["HLS mp2"]
	e.do("PUT", "/api/channels/last", `{"channelId":`+itoa(c.ID)+`}`, e.admin)
	deadline := time.Now().Add(10 * time.Second)
	for !e.relayKnows(c.ID) {
		if time.Now().After(deadline) {
			t.Fatal("not looked at")
		}
		time.Sleep(50 * time.Millisecond)
	}
	master := e.relayGet(c.Stream.Relay).Body.String()
	if !strings.Contains(master, `CODECS="avc1.42c01e,mp4a.40.2"`) || strings.Contains(master, "ac-3") {
		t.Fatalf("master:\n%s", master)
	}
	media := relayURIs(t, master)
	segs := relayURIs(t, e.relayGet(media[0]).Body.String())
	w := e.relayGet(segs[0])
	if a := tsInfo(t, w.Body.Bytes()).Audio; len(a) != 1 || a[0].Codec != "aac" || a[0].Channels != 2 {
		t.Errorf("segment audio %+v", a)
	}
}

func (e *tvEnv) relayKnows(id int64) bool {
	for _, x := range e.list(e.admin).Channels {
		if x.ID == id {
			return x.Stream.RemuxAudio
		}
	}
	return false
}

// At the limit of conversions the next stream still plays: as the provider
// sends it.
func TestRelayConversionsAreLimited(t *testing.T) {
	old := relayMendMax
	relayMendMax = 1
	t.Cleanup(func() { relayMendMax = old })
	e, up, chans := newAudioEnv(t, nil, "mp2")
	cancel, done, wrote := e.stream(chans["HOLD mp2"].Stream.Relay, e.admin)
	defer func() { cancel(); <-done }()
	select {
	case <-wrote:
	case <-time.After(15 * time.Second):
		t.Fatal("the first stream did not start")
	}
	w := e.relayGet(chans["TS mp2"].Stream.Relay)
	if !bytes.Equal(w.Body.Bytes(), up.files["/ts/mp2"]) {
		t.Errorf("the second stream: %d bytes came out of %d", w.Body.Len(), len(up.files["/ts/mp2"]))
	}
}

// No ffmpeg: the stream goes as it is.
func TestRelayWithoutFFmpegLeavesTheAudio(t *testing.T) {
	e, up, chans := newAudioEnv(t, func(d *Deps) { d.FFmpeg = "/nonexistent/ffmpeg" }, "mp2")
	w := e.relayGet(chans["TS mp2"].Stream.Relay)
	if !bytes.Equal(w.Body.Bytes(), up.files["/ts/mp2"]) {
		t.Errorf("%d bytes came out of %d", w.Body.Len(), len(up.files["/ts/mp2"]))
	}
}

func TestRelayFixCodecs(t *testing.T) {
	for in, want := range map[string]string{
		`#EXT-X-STREAM-INF:BANDWIDTH=1,CODECS="avc1.64001f,ac-3"`:                  `#EXT-X-STREAM-INF:BANDWIDTH=1,CODECS="avc1.64001f,mp4a.40.2"`,
		`#EXT-X-STREAM-INF:CODECS="hvc1.2.4.L93.B0, EC-3",RESOLUTION=1x1`:          `#EXT-X-STREAM-INF:CODECS="hvc1.2.4.L93.B0,mp4a.40.2",RESOLUTION=1x1`,
		`#EXT-X-STREAM-INF:CODECS="avc1.64001f,ac-3,mp4a.40.2"`:                    `#EXT-X-STREAM-INF:CODECS="avc1.64001f,mp4a.40.2"`,
		`#EXT-X-STREAM-INF:CODECS="avc1.64001f,mp4a.40.2"`:                         `#EXT-X-STREAM-INF:CODECS="avc1.64001f,mp4a.40.2"`,
		"#EXT-X-STREAM-INF:BANDWIDTH=1\nv.m3u8\n#EXT-X-STREAM-INF:CODECS=\"dtsc\"": "#EXT-X-STREAM-INF:BANDWIDTH=1\nv.m3u8\n#EXT-X-STREAM-INF:CODECS=\"mp4a.40.2\"",
	} {
		if got := string(relayFixCodecs([]byte(in))); got != want {
			t.Errorf("%s\n got  %s\n want %s", in, got, want)
		}
	}
}

func TestPlaylistProbeTarget(t *testing.T) {
	base, _ := url.Parse("http://p.example.test/live/index.m3u8")
	for name, tc := range map[string]struct{ body, want string }{
		"master: the audio rendition first": {"#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",URI=\"aud/en.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv/a.m3u8\n", "http://p.example.test/live/aud/en.m3u8"},
		"master: the first variant":         {"#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv/a.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2\nv/b.m3u8\n", "http://p.example.test/live/v/a.m3u8"},
		"media: the last segment":           {"#EXTM3U\n#EXTINF:4,\ns1.ts\n#EXTINF:4,\n/abs/s2.ts\n", "http://p.example.test/abs/s2.ts"},
		"not an address of the web":         {"#EXTM3U\n#EXTINF:4,\ndata:text/plain,x\n", ""},
		"nothing":                           {"#EXTM3U\n", ""},
	} {
		if got := playlistProbeTarget([]byte(tc.body), base); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

// The start of a body is waited for no longer than asked, whatever the
// body does, and nothing of it is lost.
func TestHeadReaderWaitsALimitedTimeAndLosesNothing(t *testing.T) {
	pr, pw := io.Pipe()
	h := newHeadReader(pr)
	defer h.Close()
	go func() {
		pw.Write([]byte("abc"))
		time.Sleep(400 * time.Millisecond)
		pw.Write([]byte("def"))
		time.Sleep(50 * time.Millisecond)
		pw.Close()
	}()
	start := time.Now()
	head := h.fill(100, 150*time.Millisecond)
	if string(head) != "abc" || time.Since(start) > 350*time.Millisecond {
		t.Fatalf("head %q after %s", head, time.Since(start))
	}
	rest, err := io.ReadAll(h)
	if err != nil || string(rest) != "abcdef" {
		t.Fatalf("%q %v", rest, err)
	}
	// a body that ends before the wish is over: what there is
	h2 := newHeadReader(strings.NewReader("short"))
	defer h2.Close()
	if got := h2.fill(100, 5*time.Second); string(got) != "short" {
		t.Errorf("%q", got)
	}
	if got, err := io.ReadAll(h2); string(got) != "short" || err != nil {
		t.Errorf("%q %v", got, err)
	}
}
