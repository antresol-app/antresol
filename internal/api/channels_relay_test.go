package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	relayUA      = "RelayUA/1"
	relayReferer = "http://ref.example.test/page"
)

// relayUpstream: a provider that answers only with the channel's
// User-Agent and Referer; HLS under /hls (an entry that redirects to the
// master), endless MPEG-TS under /ts/{k}.
type relayUpstream struct {
	srv     *httptest.Server
	mu      sync.Mutex
	started map[string]chan struct{}
	gone    map[string]chan struct{}
	asked   []string
}

func newRelayUpstream(t *testing.T) *relayUpstream {
	up := &relayUpstream{started: map[string]chan struct{}{}, gone: map[string]chan struct{}{}}
	files := map[string]string{
		"/hls/master.m3u8": "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",NAME=\"en\",URI=\"audio/en.m3u8\"\n" +
			"#EXT-X-STREAM-INF:BANDWIDTH=800000,AUDIO=\"a\"\r\nv/index.m3u8\r\n",
		"/hls/v/index.m3u8": "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-KEY:METHOD=AES-128,URI=\"../key.bin\",IV=0x01\n#EXT-X-MAP:URI=\"init.mp4\"\n" +
			"#EXTINF:6,\nseg1.ts\n#EXTINF:6,\n{{abs}}/hls/v/seg2.ts\n#EXTINF:6,\n/hls/v/seg3.ts\n",
		"/hls/audio/en.m3u8": "#EXTM3U\n#EXTINF:6,\na1.aac\n",
		"/hls/key.bin":       "KEY",
		"/hls/v/init.mp4":    "INIT",
		"/hls/v/seg1.ts":     "SEG1",
		"/hls/v/seg2.ts":     "SEG2",
		"/hls/v/seg3.ts":     "SEG3",
		"/hls/audio/a1.aac":  "AAC1",
	}
	up.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.mu.Lock()
		up.asked = append(up.asked, r.URL.Path)
		up.mu.Unlock()
		if r.UserAgent() != relayUA || r.Referer() != relayReferer {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/hls/entry" {
			http.Redirect(w, r, "/hls/master.m3u8", http.StatusFound)
			return
		}
		if k, ok := strings.CutPrefix(r.URL.Path, "/ts/"); ok {
			up.mu.Lock()
			st, gone := up.started[k], up.gone[k]
			up.mu.Unlock()
			w.Header().Set("Content-Type", "video/mp2t")
			w.WriteHeader(200)
			chunk := make([]byte, 188)
			chunk[0] = 0x47
			first := true
			for {
				if _, err := w.Write(chunk); err != nil {
					break
				}
				w.(http.Flusher).Flush()
				if first {
					close(st)
					first = false
				}
				select {
				case <-r.Context().Done():
					close(gone)
					return
				case <-time.After(20 * time.Millisecond):
				}
			}
			<-r.Context().Done()
			close(gone)
			return
		}
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		w.Write([]byte(strings.ReplaceAll(b, "{{abs}}", "http://"+r.Host)))
	}))
	t.Cleanup(up.srv.Close)
	return up
}

func (up *relayUpstream) expect(k string) (started, gone chan struct{}) {
	up.mu.Lock()
	defer up.mu.Unlock()
	up.started[k], up.gone[k] = make(chan struct{}), make(chan struct{})
	return up.started[k], up.gone[k]
}

// relayEnv: a source with an HLS channel and four MPEG-TS ones, all
// asking for a User-Agent and a Referer (so "web" is unsupported).
func newExtRelayEnv(t *testing.T) (*tvEnv, *relayUpstream, map[string]channelJSON) {
	e := newTVEnv(t)
	up := newRelayUpstream(t)
	pl := "#EXTM3U\n#EXTINF:-1 tvg-id=\"h\",HLS\n#EXTVLCOPT:http-user-agent=" + relayUA + "\n#EXTVLCOPT:http-referrer=" + relayReferer + "\n" + up.srv.URL + "/hls/entry\n"
	for i := 1; i <= 4; i++ {
		pl += "#EXTINF:-1,TS" + strconv.Itoa(i) + "\n#EXTHTTP:{\"User-Agent\":\"" + relayUA + "\",\"Referer\":\"" + relayReferer + "\"}\n" + up.srv.URL + "/ts/" + strconv.Itoa(i) + "\n"
	}
	e.p.put("/get.php", pl)
	w := e.do("POST", "/api/tv/sources", `{"name":"Relay","url":"`+e.p.srv.URL+`/get.php?username=u1&password=secret9"}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var src sourceJSON
	json.Unmarshal(w.Body.Bytes(), &src)
	if _, err := e.tv.RefreshSource(context.Background(), src.ID, false); err != nil {
		t.Fatal(err)
	}
	byName := map[string]channelJSON{}
	for _, c := range e.list(e.admin).Channels {
		byName[c.Name] = c
	}
	return e, up, byName
}

var relayURI = regexp.MustCompile(`URI="([^"]*)"`)

// relayURIs: every URI of a rewritten playlist; each must be a relay path.
func relayURIs(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimRight(l, "\r")
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "#") {
			for _, m := range relayURI.FindAllStringSubmatch(l, -1) {
				out = append(out, m[1])
			}
			continue
		}
		out = append(out, l)
	}
	for _, u := range out {
		if !strings.HasPrefix(u, "/api/channels/live/ext/") || !strings.Contains(u, "/relay/") {
			t.Fatalf("not relayed: %q in\n%s", u, body)
		}
	}
	return out
}

func TestRelayHLSRewritesAndPassesHeaders(t *testing.T) {
	e, up, chans := newExtRelayEnv(t)
	c := chans["HLS"]
	id := itoa(c.ID)
	if c.Stream == nil || c.Stream.Relay != "/api/channels/live/ext/"+id+"/0/relay" || c.Stream.UserAgent != relayUA || len(c.Stream.Unsupported) != 1 {
		t.Fatalf("stream %+v", c.Stream)
	}
	host := strings.TrimPrefix(up.srv.URL, "http://")
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := e.do("GET", path, "", e.admin)
		if w.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), host) {
			t.Fatalf("provider address leaked: %s", w.Body)
		}
		return w
	}
	// the entry follows the provider's redirect; the master's URIs resolve against the master
	w := get(c.Stream.Relay)
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", w.Header())
	}
	master := relayURIs(t, w.Body.String())
	if len(master) != 2 {
		t.Fatalf("master %v", master)
	}
	audio := relayURIs(t, get(master[0]).Body.String())
	if b := get(audio[0]).Body.String(); b != "AAC1" {
		t.Fatalf("audio segment %q", b)
	}
	media := relayURIs(t, get(master[1]).Body.String())
	var got []string
	for _, u := range media {
		got = append(got, get(u).Body.String())
	}
	if strings.Join(got, ",") != "KEY,INIT,SEG1,SEG2,SEG3" {
		t.Fatalf("media %v", got)
	}
	up.mu.Lock()
	asked := strings.Join(up.asked, " ")
	up.mu.Unlock()
	if !strings.Contains(asked, "/hls/entry /hls/master.m3u8") || !strings.Contains(asked, "/hls/key.bin") {
		t.Fatalf("upstream saw %s", asked)
	}

	// a changed reference, one of another channel or stream: refused
	ref := media[2]
	i := strings.LastIndex(ref, "/") + 5
	swap := "A"
	if ref[i] == 'A' {
		swap = "B"
	}
	bad := ref[:i] + swap + ref[i+1:]
	if w := e.do("GET", bad, "", e.admin); w.Code != 403 {
		t.Fatalf("tampered %d", w.Code)
	}
	other := itoa(chans["TS1"].ID)
	if w := e.do("GET", strings.Replace(ref, "/ext/"+id+"/", "/ext/"+other+"/", 1), "", e.admin); w.Code != 403 {
		t.Fatalf("other channel %d", w.Code)
	}
	if w := e.do("GET", strings.Replace(ref, "/0/relay/", "/1/relay/", 1), "", e.admin); w.Code != 403 && w.Code != 404 {
		t.Fatalf("other stream %d", w.Code)
	}
	if w := e.do("GET", "/api/channels/live/ext/"+id+"/0/relay/aGVsbG8", "", e.admin); w.Code != 403 {
		t.Fatalf("made-up %d", w.Code)
	}
	// a URL the server never linked, an expired reference, another viewer's: refused
	rl := newExtRelay(e.db) // the same server key (settings)
	uid := e.userID(e.admin)
	mint := func(k relayKey, exp time.Time, u string) string {
		s, err := rl.seal(relayRef{key: k, exp: exp, url: u})
		if err != nil {
			t.Fatal(err)
		}
		return c.Stream.Relay + "/" + s
	}
	good := relayKey{user: uid, channel: c.ID}
	if w := e.do("GET", mint(good, time.Now().Add(time.Minute), up.srv.URL+"/hls/v/seg1.ts"), "", e.admin); w.Code != 200 || w.Body.String() != "SEG1" {
		t.Fatalf("minted good %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", mint(good, time.Now().Add(-time.Second), up.srv.URL+"/hls/v/seg1.ts"), "", e.admin); w.Code != 403 {
		t.Fatalf("expired %d", w.Code)
	}
	if w := e.do("GET", mint(relayKey{user: uid + 1, channel: c.ID}, time.Now().Add(time.Minute), up.srv.URL+"/hls/v/seg1.ts"), "", e.admin); w.Code != 403 {
		t.Fatalf("other viewer %d", w.Code)
	}
	// a viewer who does not see the channel: none of it
	kid := e.kid("")
	if w := e.do("GET", c.Stream.Relay, "", kid); w.Code != 404 {
		t.Fatalf("kid entry %d", w.Code)
	}
	if w := e.do("GET", ref, "", kid); w.Code != 403 {
		t.Fatalf("kid ref %d", w.Code)
	}
}

func (e *tvEnv) userID(c *http.Cookie) int64 {
	e.t.Helper()
	w := e.do("GET", "/api/me", "", c)
	var me struct {
		ID   int64
		User struct{ ID int64 }
	}
	json.Unmarshal(w.Body.Bytes(), &me)
	if me.ID == 0 {
		me.ID = me.User.ID
	}
	if me.ID == 0 {
		e.t.Fatalf("me %d %s", w.Code, w.Body)
	}
	return me.ID
}

// stream: a relay request that runs until cancel (the recorder is read
// only after done).
func (e *tvEnv) stream(path string, c *http.Cookie) (cancel func(), done chan *httptest.ResponseRecorder, wrote chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", path, nil).WithContext(ctx)
	r.RemoteAddr = "192.168.1.20:50000"
	r.AddCookie(c)
	done = make(chan *httptest.ResponseRecorder, 1)
	w := &signalRecorder{ResponseRecorder: httptest.NewRecorder(), wrote: make(chan struct{})}
	go func() {
		e.h.ServeHTTP(w, r)
		done <- w.ResponseRecorder
	}()
	return cancel, done, w.wrote
}

// signalRecorder closes wrote on the first body bytes.
type signalRecorder struct {
	*httptest.ResponseRecorder
	once  sync.Once
	wrote chan struct{}
}

func (w *signalRecorder) Write(b []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(b)
	w.once.Do(func() { close(w.wrote) })
	return n, err
}

func waitClosed(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out: %s", what)
	}
}

func TestRelayTSStopsUpstreamWhenClientGoes(t *testing.T) {
	e, up, chans := newExtRelayEnv(t)
	c := chans["TS1"]
	if c.Stream == nil || c.Stream.UserAgent != relayUA || c.Stream.Relay == "" {
		t.Fatalf("EXTHTTP headers %+v", c.Stream)
	}
	started, gone := up.expect("1")
	cancel, done, wrote := e.stream(c.Stream.Relay, e.admin)
	waitClosed(t, started, "upstream start")
	waitClosed(t, wrote, "bytes to the client")
	cancel()
	waitClosed(t, gone, "upstream closed after the client left")
	select {
	case w := <-done:
		if w.Code != 200 || w.Header().Get("Content-Type") != "video/mp2t" || w.Body.Len() == 0 || w.Body.Bytes()[0] != 0x47 {
			t.Fatalf("ts %d %v %d", w.Code, w.Header(), w.Body.Len())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler still running")
	}
}

func TestRelayPerViewerLimit(t *testing.T) {
	e, up, chans := newExtRelayEnv(t)
	var cancels []func()
	var dones []chan *httptest.ResponseRecorder
	defer func() {
		for _, c := range cancels {
			c()
		}
		for _, d := range dones {
			<-d
		}
	}()
	for i := 1; i <= relayPerUser; i++ {
		started, _ := up.expect(strconv.Itoa(i))
		cancel, done, _ := e.stream(chans["TS"+strconv.Itoa(i)].Stream.Relay, e.admin)
		cancels, dones = append(cancels, cancel), append(dones, done)
		waitClosed(t, started, "stream "+strconv.Itoa(i))
	}
	w := e.do("GET", chans["TS4"].Stream.Relay, "", e.admin)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit %d %s", w.Code, w.Body)
	}
	// the same stream again (another tab, a reload) is not a new relay
	started, _ := up.expect("1")
	cancel, done, _ := e.stream(chans["TS1"].Stream.Relay, e.admin)
	cancels, dones = append(cancels, cancel), append(dones, done)
	waitClosed(t, started, "the same stream again")
}

func TestRelaySessionsEvictIdleAndExpire(t *testing.T) {
	e := newTVEnv(t)
	rl := newExtRelay(e.db)
	rl.perUser = 2
	now := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return now }
	ctx := context.Background()
	acq := func(user, ch int64) (func(), error) {
		rel, _, err := rl.acquire(ctx, relayKey{user: user, channel: ch}, "http://127.0.0.1/x")
		return rel, err
	}
	r1, _ := acq(1, 1)
	r2, _ := acq(1, 2)
	if _, err := acq(1, 3); !errors.Is(err, errRelayLimit) {
		t.Fatalf("third in flight: %v", err)
	}
	if r, err := acq(2, 3); err != nil {
		t.Fatalf("another viewer: %v", err)
	} else {
		r()
	}
	r1()
	now = now.Add(relayEvictAfter)
	r3, err := acq(1, 3) // stream 1 is idle: it gives its place
	if err != nil {
		t.Fatalf("evict idle: %v", err)
	}
	r2()
	r3()
	now = now.Add(relayIdle)
	rl.mu.Lock()
	rl.sweepLocked(now)
	n := len(rl.sessions)
	rl.mu.Unlock()
	if n != 0 {
		t.Fatalf("sessions after idle: %d", n)
	}
	// references expire on the relay's clock
	ref, _ := rl.seal(relayRef{key: relayKey{user: 1, channel: 1}, exp: now.Add(time.Minute), url: "http://x.example.test/a"})
	if got, err := rl.unseal(ref); err != nil || got.url != "http://x.example.test/a" {
		t.Fatalf("unseal %+v %v", got, err)
	}
	now = now.Add(time.Minute)
	if _, err := rl.unseal(ref); !errors.Is(err, errRelayRef) {
		t.Fatalf("expired: %v", err)
	}
}

func TestRelayPublicClientRefusesLocalAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if _, err := relayClient(true).Get(srv.URL); err == nil || !errors.Is(err, errRelayDial) {
		t.Fatalf("public-only client reached loopback: %v", err)
	}
	if resp, err := relayClient(false).Get(srv.URL); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	rl := &extRelay{resolve: func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.IPv4(10, 0, 0, 5)}}, nil
	}}
	for raw, want := range map[string]bool{"http://127.0.0.1/a": false, "http://192.168.1.4/a": false, "http://8.8.8.8/a": true, "http://lan.example.test/a": false, "http://localhost:1/a": false} {
		if got := rl.publicHost(context.Background(), raw); got != want {
			t.Errorf("publicHost(%s) = %v", raw, got)
		}
	}
}

func TestRelayRewriteKeepsNonHTTP(t *testing.T) {
	base, _ := url.Parse("https://cdn.example.test/live/a/index.m3u8?tok=1")
	out := string(relayRewrite([]byte("#EXTM3U\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"skd://k1\"\n#EXTINF:4,\n../b/s1.ts?x=2\n"), base,
		func(abs string, playlist bool) string { return "R(" + abs + "," + strconv.FormatBool(playlist) + ")" }))
	if out != "#EXTM3U\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"skd://k1\"\n#EXTINF:4,\nR(https://cdn.example.test/live/b/s1.ts?x=2,false)\n" {
		t.Fatalf("%q", out)
	}
}
