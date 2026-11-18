package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// hostileUpstream: a provider that answers every path with what a page of
// ours must never serve as its own: HTML and SVG with script, a cookie, a
// download name, its own CSP.
func hostileUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Set-Cookie", "evil=1; Path=/")
		h.Set("Content-Disposition", `attachment; filename="x.html"`)
		h.Set("Content-Security-Policy", "default-src *")
		h.Set("X-Provider", "1")
		switch r.URL.Path {
		case "/html":
			h.Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte("<html><script>fetch('/api/me')</script></html>"))
		case "/mixed":
			h.Set("Content-Type", "Text/HTML")
			w.Write([]byte("<script>1</script>"))
		case "/svg":
			h.Set("Content-Type", "image/svg+xml")
			w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>1</script></svg>`))
		case "/js":
			h.Set("Content-Type", "application/javascript")
			w.Write([]byte("alert(1)"))
		case "/none":
			h["Content-Type"] = nil // no type: Go would sniff the body as HTML
			w.Write([]byte("<!DOCTYPE html><script>1</script>"))
		case "/ts":
			h.Set("Content-Type", "video/MP2T")
			w.Write([]byte{0x47, 0, 0, 0})
		case "/bin":
			h.Set("Content-Type", "application/octet-stream")
			w.Write([]byte("KEY"))
		case "/pl":
			// a playlist that says it is a page
			h.Set("Content-Type", "text/html")
			w.Write([]byte("#EXTM3U\n#EXTINF:4,\nseg.ts\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// relayHeadersSafe: what a relayed answer must carry whatever the provider
// said.
func relayHeadersSafe(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	h := w.Header()
	if csp := h.Get("Content-Security-Policy"); !strings.HasPrefix(csp, "sandbox") || strings.Contains(csp, "default-src *") {
		t.Errorf("%s: CSP %q", what, csp)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("%s: nosniff missing: %v", what, h)
	}
	for _, name := range []string{"Set-Cookie", "Content-Disposition", "X-Provider"} {
		if v := h.Values(name); len(v) > 0 {
			t.Errorf("%s: provider's %s passed: %v", what, name, v)
		}
	}
}

func TestRelayNeverServesProviderTypesOnOurOrigin(t *testing.T) {
	e := newTVEnv(t)
	up := hostileUpstream(t)
	c := e.addRelaySource("#EXTM3U\n#EXTINF:-1,X\n"+up.URL+"/html\n", e.p.srv.URL)["X"]
	if c.Stream == nil || c.Stream.Relay == "" {
		t.Fatalf("stream %+v", c.Stream)
	}
	get := func(path string) *httptest.ResponseRecorder { return e.relayAsk(c, up.URL+path) }
	// the entry itself is HTML
	w := e.do("GET", c.Stream.Relay, "", e.admin)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("entry %d %v", w.Code, w.Header())
	}
	relayHeadersSafe(t, "entry", w)
	for path, wantType := range map[string]string{
		"/html": "application/octet-stream", "/mixed": "application/octet-stream", "/svg": "application/octet-stream",
		"/js": "application/octet-stream", "/none": "application/octet-stream",
		"/ts": "video/mp2t", "/bin": "application/octet-stream",
		"/pl": "application/vnd.apple.mpegurl", // detected by its first line, rewritten, never passed as is
	} {
		w := get(path)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		if got := w.Header().Get("Content-Type"); got != wantType {
			t.Errorf("%s: Content-Type %q, want %q", path, got, wantType)
		}
		relayHeadersSafe(t, path, w)
	}
	// the refusals carry the same headers
	w = e.do("GET", c.Stream.Relay+"/bm9wZQ", "", e.admin)
	if w.Code != 403 {
		t.Fatalf("bad ref %d", w.Code)
	}
	relayHeadersSafe(t, "403", w)
	w = e.do("GET", "/api/channels/live/ext/99999/0/relay", "", e.admin)
	if w.Code != 404 {
		t.Fatalf("no channel %d", w.Code)
	}
	relayHeadersSafe(t, "404", w)
}

// relayAsk: GET of target through the relay of channel c, as a reference a
// playlist of the channel would have linked (sealed with the server's key).
func (e *tvEnv) relayAsk(c channelJSON, target string) *httptest.ResponseRecorder {
	e.t.Helper()
	rl := newExtRelay(e.db)
	s, err := rl.seal(relayRef{key: relayKey{user: e.userID(e.admin), channel: c.ID}, exp: time.Now().Add(time.Minute), url: target})
	if err != nil {
		e.t.Fatal(err)
	}
	return e.do("GET", c.Stream.Relay+"/"+s, "", e.admin)
}

// hostMap sends a made-up public host name to the test provider: the
// admin's playlist address may then look like the internet's.
type hostMap struct {
	from, to string
	rt       http.RoundTripper
}

func (m hostMap) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == m.from {
		r = r.Clone(r.Context())
		r.URL.Host = m.to
	}
	return m.rt.RoundTrip(r)
}

// publicSource: the playlist is typed as http://provider.example.test (an
// internet host) and fetched from the test provider.
func (e *tvEnv) publicSource(pl string) map[string]channelJSON {
	e.t.Helper()
	e.tv.Fetch.Client.Transport = hostMap{from: "provider.example.test", to: e.p.srv.Listener.Addr().String(), rt: e.tv.Fetch.Client.Transport}
	return e.addRelaySource(pl, "http://provider.example.test")
}

// A playlist from the internet cannot make the server fetch its own
// loopback, the LAN or the cloud metadata address: the entry's own host
// decides nothing, the address the admin typed does.
func TestRelayInternetSourceCannotReachLocalAddresses(t *testing.T) {
	e := newTVEnv(t)
	up := newRelayUpstream(t)
	c := e.publicSource("#EXTM3U\n#EXTINF:-1,X\n" + up.srv.URL + "/hls/entry\n")["X"]
	if c.Stream == nil || c.Stream.Relay == "" {
		t.Fatalf("stream %+v", c.Stream)
	}
	// the entry itself, and what its playlist would link to
	if w := e.do("GET", c.Stream.Relay, "", e.admin); w.Code != http.StatusBadGateway {
		t.Errorf("entry on loopback: %d %s", w.Code, w.Body)
	}
	for _, target := range []string{up.srv.URL + "/hls/v/seg1.ts", "http://127.0.0.1:1/", "http://[::1]:1/", "http://[::ffff:127.0.0.1]:1/",
		"http://169.254.169.254/latest/meta-data/", "http://192.168.50.20:8006/", "http://10.0.0.1/", "http://localhost:1/"} {
		if w := e.relayAsk(c, target); w.Code != http.StatusBadGateway {
			t.Errorf("%s: %d %s", target, w.Code, w.Body)
		}
	}
	up.mu.Lock()
	asked := up.asked
	up.mu.Unlock()
	if len(asked) != 0 {
		t.Fatalf("the provider's loopback host was reached: %v", asked)
	}
}

// A playlist on the LAN may stream from the LAN, but never from the
// metadata address or any other link-local, reserved or multicast one, by
// the entry or by a redirect.
func TestRelayLANSourceStillRefusesLinkLocal(t *testing.T) {
	e := newTVEnv(t)
	up := newRelayUpstream(t)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer redir.Close()
	// the provider is on loopback: a LAN source
	c := e.addRelaySource("#EXTM3U\n#EXTINF:-1,X\n#EXTHTTP:{\"User-Agent\":\""+relayUA+"\",\"Referer\":\""+relayReferer+"\"}\n"+up.srv.URL+"/hls/v/seg1.ts\n", e.p.srv.URL)["X"]
	if w := e.relayAsk(c, up.srv.URL+"/hls/v/seg1.ts"); w.Code != 200 || w.Body.String() != "SEG1" {
		t.Fatalf("LAN stream of a LAN source: %d %s", w.Code, w.Body)
	}
	for _, target := range []string{"http://169.254.169.254/latest/meta-data/", redir.URL + "/x", "http://0.0.0.0:1/", "http://[fe80::1]:1/",
		"http://224.0.0.1:1/", "http://255.255.255.255:1/", "http://[64:ff9b::7f00:1]:1/"} {
		start := time.Now()
		if w := e.relayAsk(c, target); w.Code != http.StatusBadGateway {
			t.Errorf("%s: %d %s", target, w.Code, w.Body)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s: refused after %s, a dial was tried", target, d) // refused at the socket, not timed out
		}
	}
}

// The per-viewer limit holds when several streams start at once (the check
// and the count are one step), and the total limit too.
func TestRelayLimitsHoldUnderConcurrentStarts(t *testing.T) {
	rl := newExtRelay(nil)
	rl.perUser, rl.total = 3, 5
	rl.sourceLAN = func(context.Context, int64) bool { time.Sleep(30 * time.Millisecond); return false } // a slow store
	try := func(keys []relayKey) (ok int) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, k := range keys {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, _, err := rl.acquire(context.Background(), k, 1); err == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				} else if !errors.Is(err, errRelayLimit) {
					t.Errorf("acquire: %v", err)
				}
			}()
		}
		wg.Wait()
		return ok
	}
	var mine []relayKey
	for ch := int64(1); ch <= 12; ch++ {
		mine = append(mine, relayKey{user: 1, channel: ch})
	}
	if got := try(mine); got != 3 {
		t.Fatalf("one viewer, 12 streams at once: %d admitted, want 3", got)
	}
	var others []relayKey
	for u := int64(2); u <= 9; u++ {
		others = append(others, relayKey{user: u, channel: 100 + u})
	}
	if got := try(others); got != 2 { // 3 held, total 5
		t.Fatalf("8 viewers at once with 2 places left: %d admitted", got)
	}
}

// One viewer cannot open any number of parallel requests on one stream.
func TestRelayCapsRequestsPerStream(t *testing.T) {
	rl := newExtRelay(nil)
	k := relayKey{user: 1, channel: 1}
	var rels []func()
	for i := 0; i < relayInflight; i++ {
		rel, _, err := rl.acquire(context.Background(), k, 1)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		rels = append(rels, rel)
	}
	if _, _, err := rl.acquire(context.Background(), k, 1); !errors.Is(err, errRelayLimit) {
		t.Fatalf("over the cap: %v", err)
	}
	rels[0]()
	rels[0]() // twice: counted once
	rel, _, err := rl.acquire(context.Background(), k, 1)
	if err != nil {
		t.Fatalf("after a release: %v", err)
	}
	rel()
	if _, _, err := rl.acquire(context.Background(), k, 1); err != nil {
		t.Fatalf("one place was freed twice: %v", err)
	}
	if _, _, err := rl.acquire(context.Background(), k, 1); !errors.Is(err, errRelayLimit) {
		t.Fatalf("a double release freed a second place: %v", err)
	}
}

// A provider that sends headers and then trickles less than the first
// bytes the relay looks at cannot hold the handler for ever.
func TestRelayStalledProviderBeforeFirstBytes(t *testing.T) {
	old := relayStall
	relayStall = 300 * time.Millisecond
	t.Cleanup(func() { relayStall = old })
	e := newTVEnv(t)
	stuck := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ab")) // fewer than the 16 bytes read to tell a playlist
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-stuck:
		}
	}))
	defer srv.Close()
	defer close(stuck)
	c := e.addRelaySource("#EXTM3U\n#EXTINF:-1,X\n"+srv.URL+"/x\n", e.p.srv.URL)["X"]
	cancel, done, _ := e.stream(c.Stream.Relay, e.admin)
	defer cancel()
	select {
	case w := <-done:
		if w.Code != http.StatusBadGateway {
			t.Fatalf("stalled provider: %d %s", w.Code, w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler is still waiting for a provider that sends nothing")
	}
}

// A player reloads the variant playlist it was given for as long as it
// plays and never asks the master again: that reference must outlast a
// channel left on for days, while a segment's, key's or map's stays short.
func TestRelayVariantReferencesOutlastAViewing(t *testing.T) {
	e, _, chans := newExtRelayEnv(t)
	c := chans["HLS"]
	master := e.do("GET", c.Stream.Relay, "", e.admin)
	if master.Code != 200 {
		t.Fatalf("master %d %s", master.Code, master.Body)
	}
	rl := newExtRelay(e.db) // the same server key
	expOf := func(path string) time.Duration {
		t.Helper()
		ref, err := rl.unseal(path[strings.LastIndex(path, "/")+1:])
		if err != nil {
			t.Fatalf("unseal %s: %v", path, err)
		}
		return time.Until(ref.exp)
	}
	uris := relayURIs(t, master.Body.String())
	for _, u := range uris {
		if d := expOf(u); d < 7*24*time.Hour {
			t.Errorf("variant reference lasts %s: a channel left playing would stop", d)
		}
	}
	media := relayURIs(t, e.do("GET", uris[1], "", e.admin).Body.String())
	for _, u := range media {
		if d := expOf(u); d > time.Hour {
			t.Errorf("segment reference lasts %s", d)
		}
	}
}
