package addons

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// relayAddon is an in-process addon socket for the relay's tests: it
// records the paths it was asked for and answers them from routes.
type relayAddon struct {
	sock string
	srv  *http.Server

	mu    sync.Mutex
	asked []*http.Request
}

func (a *relayAddon) paths() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, r := range a.asked {
		out = append(out, r.URL.RequestURI())
	}
	return out
}

func (a *relayAddon) last() *http.Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.asked) == 0 {
		return nil
	}
	return a.asked[len(a.asked)-1]
}

// shortDir is a directory whose socket paths fit the 104-byte limit.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "rl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func newRelayAddon(t *testing.T, h http.Handler) *relayAddon {
	t.Helper()
	a := &relayAddon{sock: filepath.Join(shortDir(t), "a.sock")}
	ln, err := net.Listen("unix", a.sock)
	if err != nil {
		t.Fatal(err)
	}
	a.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.asked = append(a.asked, r.Clone(context.Background()))
		a.mu.Unlock()
		h.ServeHTTP(w, r)
	})}
	go a.srv.Serve(ln)
	t.Cleanup(func() { a.srv.Close() })
	return a
}

// relaySockets is a SocketLookup of fixed sockets per addon (all running).
type relaySockets struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *relaySockets) lookup(id string) (Status, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sock, ok := s.m[id]; ok {
		return StatusRunning, sock
	}
	return StatusStopped, ""
}

func (s *relaySockets) set(id, sock string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sock == "" {
		delete(s.m, id)
		return
	}
	s.m[id] = sock
}

func newTestRelay(t *testing.T, a *relayAddon) (*Relay, *relaySockets) {
	t.Helper()
	socks := &relaySockets{m: map[string]string{"tv": a.sock}}
	return NewRelay(socks.lookup, func() int { return 8420 }), socks
}

// relayGet asks the relay for the path of url (its host dropped) from a
// loopback address.
func relayGet(t *testing.T, r *Relay, method, url string, hdr http.Header) *httptest.ResponseRecorder {
	t.Helper()
	p := strings.TrimPrefix(url, "http://127.0.0.1:8420")
	req := httptest.NewRequest(method, p, nil)
	req.RemoteAddr = "127.0.0.1:50000"
	for k, v := range hdr {
		req.Header[k] = v
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func playlistAddon(t *testing.T) *relayAddon {
	return newRelayAddon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ch/t/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			io.WriteString(w, "#EXTM3U\n#EXTINF:4,\nseg1.ts\n")
		case "/ch/t/seg1.ts", "/ch/t/sub/seg2.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			io.WriteString(w, "TSDATA")
		default:
			http.NotFound(w, r)
		}
	}))
}

// The URL ffmpeg gets: http://127.0.0.1:<port>/api/addon-relay/<k>/<token>/<name>,
// k and the token 32 hex characters; a segment URI relative to the
// playlist keeps both and reaches the addon under the playlist's folder.
func TestRelay_PlaylistRelativeURIsKeepKey(t *testing.T) {
	a := playlistAddon(t)
	r, _ := newTestRelay(t, a)
	u, release, err := r.Open("tv", "/ch/t/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	rest, ok := strings.CutPrefix(u, "http://127.0.0.1:8420/api/addon-relay/")
	parts := strings.Split(rest, "/")
	if !ok || len(parts) != 3 || !hex32(parts[0]) || !hex32(parts[1]) || parts[2] != "index.m3u8" {
		t.Fatalf("url %q", u)
	}
	if w := relayGet(t, r, "GET", u, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "seg1.ts") {
		t.Fatalf("playlist %d %s", w.Code, w.Body)
	}
	// ffmpeg resolves seg1.ts against the playlist's URL
	seg := strings.TrimSuffix(u, "index.m3u8") + "seg1.ts"
	if w := relayGet(t, r, "GET", seg, nil); w.Code != 200 || w.Body.String() != "TSDATA" {
		t.Fatalf("segment %d %s", w.Code, w.Body)
	}
	if w := relayGet(t, r, "HEAD", strings.TrimSuffix(u, "index.m3u8")+"sub/seg2.ts", nil); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD sub segment %d %q", w.Code, w.Body)
	}
	got := a.paths()
	if len(got) != 3 || got[0] != "/ch/t/index.m3u8" || got[1] != "/ch/t/seg1.ts" || got[2] != "/ch/t/sub/seg2.ts" {
		t.Fatalf("addon asked %v", got)
	}
	if a.last().Method != "HEAD" {
		t.Fatalf("method %s", a.last().Method)
	}
}

func hex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func TestRelay_WrongKey404(t *testing.T) {
	a := playlistAddon(t)
	r, _ := newTestRelay(t, a)
	u, release, err := r.Open("tv", "/ch/t/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	rest := strings.TrimPrefix(u, "http://127.0.0.1:8420/api/addon-relay/")
	k, tok, _ := strings.Cut(rest, "/")
	tok, _, _ = strings.Cut(tok, "/")
	other := NewRelay(nil, func() int { return 1 })
	for _, bad := range []string{
		"/api/addon-relay/" + strings.Repeat("0", 32) + "/" + tok + "/index.m3u8",
		"/api/addon-relay/" + other.key + "/" + tok + "/index.m3u8",
		"/api/addon-relay/" + k + "/" + strings.Repeat("0", 32) + "/index.m3u8",
		"/api/addon-relay/" + k + "/index.m3u8",
		"/api/addon-relay/" + k,
		"/api/addon-relay/",
	} {
		if w := relayGet(t, r, "GET", bad, nil); w.Code != 404 {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}
	if len(a.paths()) != 0 {
		t.Fatalf("addon asked %v", a.paths())
	}
	if w := relayGet(t, r, "POST", u, nil); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST %d", w.Code)
	}
}

func TestRelay_NotLoopback403(t *testing.T) {
	a := playlistAddon(t)
	r, _ := newTestRelay(t, a)
	u, release, _ := r.Open("tv", "/ch/t/index.m3u8")
	defer release()
	for _, remote := range []string{"192.0.2.7:4000", "[2001:db8::1]:4000", "garbage"} {
		req := httptest.NewRequest("GET", strings.TrimPrefix(u, "http://127.0.0.1:8420"), nil)
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Errorf("%s: %d", remote, w.Code)
		}
	}
	req := httptest.NewRequest("GET", strings.TrimPrefix(u, "http://127.0.0.1:8420"), nil)
	req.RemoteAddr = "[::1]:4000"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("::1: %d", w.Code)
	}
}

func TestRelay_ReleaseForgets(t *testing.T) {
	a := playlistAddon(t)
	r, _ := newTestRelay(t, a)
	u, release, _ := r.Open("tv", "/ch/t/index.m3u8")
	if w := relayGet(t, r, "GET", u, nil); w.Code != 200 {
		t.Fatalf("before release %d", w.Code)
	}
	release()
	release() // twice is harmless
	if w := relayGet(t, r, "GET", u, nil); w.Code != 404 {
		t.Fatalf("after release %d", w.Code)
	}
	// unused for 6 h: dropped too
	now := time.Now()
	r.now = func() time.Time { return now }
	u2, release2, _ := r.Open("tv", "/ch/t/index.m3u8")
	defer release2()
	now = now.Add(5 * time.Hour)
	if w := relayGet(t, r, "GET", u2, nil); w.Code != 200 {
		t.Fatalf("used within 6 h %d", w.Code)
	}
	now = now.Add(6*time.Hour + time.Second)
	if w := relayGet(t, r, "GET", u2, nil); w.Code != 404 {
		t.Fatalf("unused 6 h %d", w.Code)
	}
}

func TestRelay_DotDotRefused(t *testing.T) {
	a := playlistAddon(t)
	r, _ := newTestRelay(t, a)
	if _, _, err := r.Open("tv", "/ch/../hooks/x"); err == nil {
		t.Fatal("opened a path with ..")
	}
	if _, _, err := r.Open("tv", "/hooks/channels.release"); err == nil {
		t.Fatal("opened a hook")
	}
	if _, _, err := r.Open("tv", "relative.m3u8"); err == nil {
		t.Fatal("opened a relative path")
	}
	u, release, _ := r.Open("tv", "/ch/t/index.m3u8")
	defer release()
	base := strings.TrimSuffix(u, "index.m3u8")
	for _, bad := range []string{"../../hooks/channels.release", "..%2f..%2fhooks%2fx", "%2e%2e/%2e%2e/health", "./seg1.ts", "a//seg1.ts", "a%2Fb.ts", ""} {
		if w := relayGet(t, r, "GET", base+bad, nil); w.Code != 404 {
			t.Errorf("%q: %d", bad, w.Code)
		}
	}
	if len(a.paths()) != 0 {
		t.Fatalf("addon asked %v", a.paths())
	}
}

// Only Range and If-Range reach the addon; only Content-Length,
// Content-Range, Content-Type and Accept-Ranges come back.
func TestRelay_HeaderFilter(t *testing.T) {
	a := newRelayAddon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Set-Cookie", "a=b")
		h.Set("Location", "http://elsewhere/")
		h.Set("X-Secret", "s")
		h.Set("ETag", `"e"`)
		h.Set("Content-Type", "video/mp4")
		h.Set("Accept-Ranges", "bytes")
		h.Set("Content-Range", "bytes 0-3/10")
		h.Set("Content-Length", "4")
		w.WriteHeader(206)
		io.WriteString(w, "abcd")
	}))
	r, _ := newTestRelay(t, a)
	u, release, _ := r.Open("tv", "/f/movie.mp4")
	defer release()
	w := relayGet(t, r, "GET", u, http.Header{"Range": {"bytes=0-3"}, "Cookie": {"s=1"}, "Authorization": {"x"}})
	if w.Code != 206 || w.Body.String() != "abcd" {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
	for k := range w.Header() {
		switch k {
		case "Content-Length", "Content-Range", "Content-Type", "Accept-Ranges", "X-Content-Type-Options", "Content-Security-Policy":
		default:
			t.Errorf("header %s passed", k)
		}
	}
	if w.Header().Get("Content-Type") != "video/mp4" || w.Header().Get("Content-Range") != "bytes 0-3/10" {
		t.Fatalf("headers %v", w.Header())
	}
	got := a.last()
	if got.Header.Get("Range") != "bytes=0-3" || got.Header.Get("Cookie") != "" || got.Header.Get("Authorization") != "" {
		t.Fatalf("asked with %v", got.Header)
	}
	if w := relayGet(t, r, "GET", u, http.Header{"Range": {"bytes=0-1,4-5"}}); w.Code != 416 {
		t.Fatalf("multi-range %d", w.Code)
	}
}

// The addon's process goes (stopped, or a new process on a new socket):
// its relay URLs answer 404 and are forgotten.
func TestRelay_AddonGoneEnds(t *testing.T) {
	a := playlistAddon(t)
	r, socks := newTestRelay(t, a)
	u, release, _ := r.Open("tv", "/ch/t/index.m3u8")
	defer release()
	socks.set("tv", "/tmp/elsewhere.sock") // restarted: a new socket
	if w := relayGet(t, r, "GET", u, nil); w.Code != 404 {
		t.Fatalf("after restart %d", w.Code)
	}
	socks.set("tv", a.sock)
	if w := relayGet(t, r, "GET", u, nil); w.Code != 404 {
		t.Fatalf("forgotten token came back %d", w.Code)
	}
	u2, release2, _ := r.Open("tv", "/ch/t/index.m3u8")
	defer release2()
	r.Forget("tv") // the supervisor says it stopped
	if w := relayGet(t, r, "GET", u2, nil); w.Code != 404 {
		t.Fatalf("after Forget %d", w.Code)
	}
	u3, release3, _ := r.Open("tv", "/ch/t/index.m3u8")
	defer release3()
	a.srv.Close() // the socket is gone
	os.Remove(a.sock)
	if w := relayGet(t, r, "GET", u3, nil); w.Code != 404 {
		t.Fatalf("dead socket %d", w.Code)
	}
	socks.set("tv", "")
	if _, _, err := r.Open("tv", "/ch/t/index.m3u8"); err == nil {
		t.Fatal("opened for a stopped addon")
	}
}
