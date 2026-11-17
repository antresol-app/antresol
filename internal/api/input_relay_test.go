package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/go-chi/chi/v5"
)

const relayToken = "0123456789abcdef0123456789abcdef" // gitleaks:allow (a test value)

// seenReq is what the fake addon was asked.
type seenReq struct {
	method, uri string
	header      http.Header
	body        []byte // of a hook call (/hooks/…)
}

// relayEnv is a router with the input relay and a fake addon "pl" that
// serves a 1 MiB file over a Unix socket.
type relayEnv struct {
	t      *testing.T
	h      http.Handler
	inputs *addons.Inputs
	fake   *fakeAddons
	srv    *http.Server
	sock   string
	data   []byte
	uid    int64
	users  *auth.Store
	ss     *auth.Sessions
	keys   *auth.MediaKeys
	repo   *library.Repo
	prog   *progress.Store
	hits   atomic.Int64 // requests to /target (a redirect's target)
	deps   Deps         // what h was made of (withDeps rebuilds it)

	mu    sync.Mutex
	seen  []seenReq
	files map[string][]byte // served at their path instead of data (serveFile)
	// hookGate, when set, holds every hook call until it closes (or the
	// call's context ends); hooks answer 204
	hookGate chan struct{}
}

// serveFile makes the fake addon serve b at path p (Content-Length, no ETag).
func (e *relayEnv) serveFile(p string, b []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.files == nil {
		e.files = map[string][]byte{}
	}
	e.files[p] = b
}

func newRelayEnv(t *testing.T) *relayEnv {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &relayEnv{t: t, data: make([]byte, 1<<20)}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range e.data {
		e.data[i] = byte(rng.Uint32())
	}
	dir, err := os.MkdirTemp("/tmp", "ad")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	e.sock = filepath.Join(dir, "pl.1.sock")
	ln, err := net.Listen("unix", e.sock)
	if err != nil {
		t.Fatal(err)
	}
	mod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	e.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if strings.HasPrefix(r.URL.Path, "/hooks/") {
			body, _ = io.ReadAll(r.Body)
		}
		e.mu.Lock()
		e.seen = append(e.seen, seenReq{r.Method, r.RequestURI, r.Header.Clone(), body})
		f, isFile := e.files[r.URL.Path]
		gate := e.hookGate
		e.mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/hooks/") {
			if gate != nil {
				select {
				case <-gate:
				case <-r.Context().Done():
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if isFile {
			http.ServeContent(w, r, "f.bin", mod, bytes.NewReader(f))
			return
		}
		switch r.URL.Path {
		case "/evil":
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Location", "/elsewhere")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Content-Security-Policy", "default-src *")
			http.SetCookie(w, &http.Cookie{Name: "evil", Value: "1"})
			http.ServeContent(w, r, "x.html", mod, bytes.NewReader(e.data))
		case "/redir":
			http.Redirect(w, r, "/target", http.StatusFound)
		case "/target":
			e.hits.Add(1)
			w.Write([]byte("target"))
		case "/slow":
			// the head of a long file, then nothing until the process goes
			w.Header().Set("Content-Length", strconv.Itoa(len(e.data)))
			w.Write(e.data[:1024])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/big-chunked":
			// a subtitle file over maxInputSub without a Content-Length
			if r.Method == http.MethodGet {
				w.(http.Flusher).Flush()
				chunk := bytes.Repeat([]byte("x"), 1<<20)
				for range maxInputSub>>20 + 1 {
					if _, err := w.Write(chunk); err != nil {
						return
					}
				}
			}
		case "/fail":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.ServeContent(w, r, "x.bin", mod, bytes.NewReader(e.data))
		}
	})}
	go e.srv.Serve(ln)
	t.Cleanup(func() { e.srv.Close() })

	st, ss := auth.NewStore(d), auth.NewSessions(d)
	u, err := st.CreateUser(context.Background(), auth.NewUser{Name: "Маша"})
	if err != nil {
		t.Fatal(err)
	}
	e.uid = u.ID
	pl := addons.Installed{ID: "pl", Source: "github.com/o/pl", Version: "1.0.0", Enabled: true,
		Manifest: addons.Manifest{ID: "pl", Name: addons.Text{RU: "Плеер", EN: "Player"}, Version: "1.0.0",
			Contract: addons.ContractVersion, Permissions: []string{"play"}, Access: "adults"}}
	e.fake = &fakeAddons{
		installed: map[string]addons.Installed{"pl": pl},
		status:    map[string]addons.Status{"pl": addons.StatusRunning},
		sock:      map[string]string{"pl": e.sock},
		tokens:    map[string]string{},
		webRoot:   t.TempDir(),
	}
	e.inputs = &addons.Inputs{Serving: e.fake.Serving}
	e.users, e.ss, e.repo, e.prog = st, ss, library.NewRepo(d), progress.New(d)
	e.keys = auth.NewMediaKeys(bytes.Repeat([]byte{1}, 32), ss)
	e.deps = Deps{Users: st, Sessions: ss, MediaKeys: e.keys, Library: e.repo, Progress: e.prog, DB: d,
		Addons: e.fake, AddonInputs: e.inputs, InputBase: "http://127.0.0.1:1", InputToken: relayToken}
	e.h = NewRouter(e.deps)
	return e
}

// add registers an input whose main part is p named name, with one audio
// and one subtitle sidecar.
func (e *relayEnv) add(p, name string) string {
	e.t.Helper()
	in, err := e.inputs.Add("pl", addonkit.InputRequest{UserID: e.uid, Path: p, Name: name, Size: int64(len(e.data)),
		Sidecars: addonkit.InputSidecars{
			Audio:     []addonkit.AudioSidecar{{Path: "/media/a", Name: "Film/a.mka", Size: 10}},
			Subtitles: []addonkit.SubtitleSidecar{{Path: "/media/s", Name: "Film/s.srt"}},
		}})
	if err != nil {
		e.t.Fatal(err)
	}
	return in.ID
}

// get asks the relay from addr (host:port) with headers hdr (pairs).
func (e *relayEnv) get(method, target, addr string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = addr
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func relayPath(id string, n int) string {
	return "/api/addon-input/" + id + "/" + strconv.Itoa(n) + "?k=" + relayToken
}

const loopAddr = "127.0.0.1:50000"

func (e *relayEnv) seenCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.seen)
}

func (e *relayEnv) last() seenReq {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.seen) == 0 {
		e.t.Fatal("the addon was not asked")
	}
	return e.seen[len(e.seen)-1]
}

func TestInputRelay_Range(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/media/1", "Film/x.mkv")
	w := e.get("GET", relayPath(id, 0), loopAddr, "Range", "bytes=100-199")
	if w.Code != 206 {
		t.Fatalf("range: %d %s", w.Code, w.Body)
	}
	if !bytes.Equal(w.Body.Bytes(), e.data[100:200]) {
		t.Fatalf("body: %d bytes, not the file's 100-199", w.Body.Len())
	}
	if cr := w.Header().Get("Content-Range"); cr != "bytes 100-199/1048576" {
		t.Errorf("Content-Range %q", cr)
	}
	if ct := w.Header().Get("Content-Type"); ct != "video/x-matroska" {
		t.Errorf("Content-Type %q", ct)
	}
	got := e.last()
	if got.uri != "/media/1" || got.method != "GET" {
		t.Errorf("addon asked %s %s", got.method, got.uri)
	}
	if got.header.Get("X-Antresol-Input") != id {
		t.Errorf("X-Antresol-Input %q", got.header.Get("X-Antresol-Input"))
	}
	var who addonkit.User
	if err := json.Unmarshal([]byte(got.header.Get("X-Antresol-User")), &who); err != nil || who.ID != e.uid || who.Name != "Маша" || len(who.UID) != 32 {
		t.Errorf("X-Antresol-User %q: %+v %v", got.header.Get("X-Antresol-User"), who, err)
	}
	if strings.Contains(got.header.Get("X-Antresol-User"), "Маша") {
		t.Error("X-Antresol-User is not ASCII")
	}
	for k := range got.header {
		switch k {
		case "Range", "X-Antresol-Input", "X-Antresol-User", "User-Agent":
		case "Connection": // close: a new connection every time
			if v := got.header.Get(k); v != "close" {
				t.Errorf("Connection %q", v)
			}
		default:
			t.Errorf("header %s reached the addon", k)
		}
	}
	// the sidecars are parts 1 and 2
	e.get("GET", relayPath(id, 1), loopAddr, "Range", "bytes=0-9")
	if u := e.last().uri; u != "/media/a" {
		t.Errorf("part 1 asked %s", u)
	}
	e.get("GET", relayPath(id, 2), loopAddr)
	if u := e.last().uri; u != "/media/s" {
		t.Errorf("part 2 asked %s", u)
	}
}

// The path reaches the addon exactly as validated (escapes and query).
func TestInputRelay_PathAsValidated(t *testing.T) {
	e := newRelayEnv(t)
	for _, p := range []string{"/media/%C3%A9%20x", "/media/x?part=2&q=a%2Fb", "/media/a+b"} {
		id := e.add(p, "Film/x.mp4")
		if w := e.get("GET", relayPath(id, 0), loopAddr, "Range", "bytes=0-0"); w.Code != 206 {
			t.Fatalf("%s: %d", p, w.Code)
		}
		if u := e.last().uri; u != p {
			t.Errorf("asked %q, want %q", u, p)
		}
	}
}

func TestInputRelay_HEAD(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/media/1", "Film/x.mp4")
	w := e.get("HEAD", relayPath(id, 0), loopAddr)
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD: %d, %d bytes", w.Code, w.Body.Len())
	}
	if w.Header().Get("Content-Length") != "1048576" || w.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("headers %v", w.Header())
	}
	if m := e.last().method; m != "HEAD" {
		t.Errorf("addon asked with %s", m)
	}
}

func TestInputRelay_Auth(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/media/1", "Film/x.mp4")
	base := "/api/addon-input/" + id + "/0"
	cases := []struct {
		name, target, addr string
		hdr                []string
	}{
		{"no k", base, loopAddr, nil},
		{"empty k", base + "?k=", loopAddr, nil},
		{"wrong k", base + "?k=" + strings.Repeat("0", 32), loopAddr, nil},
		{"k prefix", base + "?k=" + relayToken[:31], loopAddr, nil},
		{"right k, remote", relayPath(id, 0), "10.0.0.20:5000", nil},
		{"right k, forwarded", relayPath(id, 0), "10.0.0.20:5000", []string{"X-Forwarded-For", "127.0.0.1", "X-Real-IP", "127.0.0.1"}},
		{"right k, no port", relayPath(id, 0), "127.0.0.1", nil},
	}
	var body string
	for _, c := range cases {
		w := e.get("GET", c.target, c.addr, append([]string{"Range", "bytes=0-1"}, c.hdr...)...)
		if w.Code != 404 {
			t.Errorf("%s: %d", c.name, w.Code)
		}
		if body == "" {
			body = w.Body.String()
		} else if w.Body.String() != body {
			t.Errorf("%s: answer %q differs from %q (a hint which check failed)", c.name, w.Body, body)
		}
		if strings.Contains(w.Body.String(), relayToken) {
			t.Errorf("%s: token in the answer", c.name)
		}
	}
	if n := e.seenCount(); n != 0 {
		t.Fatalf("the addon was asked %d times", n)
	}
	if !strings.Contains(body, "Видео не найдено") {
		t.Errorf("refusal %q", body)
	}
	for _, addr := range []string{loopAddr, "[::1]:50000"} {
		if w := e.get("GET", relayPath(id, 0), addr, "Range", "bytes=0-1"); w.Code != 206 {
			t.Errorf("right k from %s: %d %s", addr, w.Code, w.Body)
		}
	}
	// an unknown id with the right k is the same 404
	if w := e.get("GET", relayPath(strings.Repeat("a", 32), 0), loopAddr); w.Code != 404 || w.Body.String() != body {
		t.Errorf("unknown id: %d %s", w.Code, w.Body)
	}
}

// Without a token the relay admits nobody.
func TestInputRelay_NoToken(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/media/1", "Film/x.mp4")
	p := newInputPlay(nil, Deps{AddonInputs: e.inputs, Addons: e.fake, InputBase: "http://127.0.0.1:1"})
	r := httptest.NewRequest("GET", "/api/addon-input/"+id+"/0?k=", nil)
	r.RemoteAddr = loopAddr
	if p.internalRequest(r) {
		t.Fatal("empty token admitted an empty k")
	}
}

func TestInputRelay_HeaderWhitelist(t *testing.T) {
	e := newRelayEnv(t)
	for name, want := range map[string]string{"x/video.mp4": "video/mp4", "x/video.epub": "application/octet-stream",
		"x/video.html": "application/octet-stream", "x/sound.m4a": "audio/mp4", "x/sound.MKV": "video/x-matroska",
		"x/sub.srt": "application/octet-stream", "x/book.pdf": "application/octet-stream"} {
		id := e.add("/evil", name)
		w := e.get("GET", relayPath(id, 0), loopAddr, "Range", "bytes=0-9")
		if w.Code != 206 {
			t.Fatalf("%s: %d", name, w.Code)
		}
		h := w.Header()
		if ct := h.Get("Content-Type"); ct != want {
			t.Errorf("%s: Content-Type %q, want %q", name, ct, want)
		}
		for _, k := range []string{"Set-Cookie", "Location", "Access-Control-Allow-Origin"} {
			if h.Get(k) != "" {
				t.Errorf("%s: %s passed: %q", name, k, h.Get(k))
			}
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "sandbox" {
			t.Errorf("%s: nosniff %q, CSP %q", name, h.Get("X-Content-Type-Options"), h.Get("Content-Security-Policy"))
		}
		for k := range h {
			switch k {
			case "Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "Etag",
				"X-Content-Type-Options", "Content-Security-Policy":
			case "Vary": // the core's own (i18n)
				if v := h.Get(k); v != "Accept-Language" {
					t.Errorf("%s: Vary %q", name, v)
				}
			default:
				t.Errorf("%s: header %s passed", name, k)
			}
		}
		if h.Get("Last-Modified") == "" || h.Get("Content-Range") == "" || h.Get("Content-Length") != "10" {
			t.Errorf("%s: whitelisted headers missing: %v", name, h)
		}
	}
}

func TestInputRelay_NoRedirect(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/redir", "Film/x.mp4")
	w := e.get("GET", relayPath(id, 0), loopAddr)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "Расширение не ответило") {
		t.Fatalf("redirect: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Location") != "" {
		t.Error("Location passed")
	}
	if e.hits.Load() != 0 {
		t.Fatal("the redirect was followed")
	}
	// any other status is 502 as well
	id = e.add("/fail", "Film/y.mp4")
	if w := e.get("GET", relayPath(id, 0), loopAddr); w.Code != 502 || strings.Contains(w.Body.String(), "boom") {
		t.Fatalf("500: %d %s", w.Code, w.Body)
	}
}

func TestInputRelay_416Copied(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/media/1", "Film/x.mp4")
	w := e.get("GET", relayPath(id, 0), loopAddr, "Range", "bytes=2000000-")
	if w.Code != 416 {
		t.Fatalf("unsatisfiable range: %d", w.Code)
	}
	if w.Header().Get("Content-Range") != "bytes */1048576" {
		t.Errorf("Content-Range %q", w.Header().Get("Content-Range"))
	}
}

func TestInputRelay_DeadIs404(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/media/1", "Film/x.mp4")
	if w := e.get("GET", relayPath(id, 0), loopAddr, "Range", "bytes=0-1"); w.Code != 206 {
		t.Fatalf("alive: %d", w.Code)
	}
	// the process is gone (its socket closed) while the registry still has it
	e.srv.Close()
	os.Remove(e.sock)
	if w := e.get("GET", relayPath(id, 0), loopAddr, "Range", "bytes=0-1"); w.Code != 404 {
		t.Fatalf("closed socket: %d %s", w.Code, w.Body)
	}
	// a restart: a new socket; the old id never reaches the new process
	ln, err := net.Listen("unix", e.sock+"2")
	if err != nil {
		t.Fatal(err)
	}
	var asked atomic.Int64
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asked.Add(1) })}
	go srv.Serve(ln)
	defer srv.Close()
	id2 := e.add("/media/1", "Film/x.mp4") // registered before the restart too
	e.fake.mu.Lock()
	e.fake.sock["pl"] = e.sock + "2"
	e.fake.mu.Unlock()
	for _, i := range []string{id, id2} {
		if w := e.get("GET", relayPath(i, 0), loopAddr); w.Code != 404 {
			t.Errorf("after restart: %d %s", w.Code, w.Body)
		}
	}
	if asked.Load() != 0 {
		t.Fatal("the new process was asked for an old input")
	}
}

// A process that dies while a read is in flight: the read ends.
func TestInputRelay_DiesMidRead(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/slow", "Film/x.mp4")
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + relayPath(id, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatal(err)
	}
	e.srv.Close()
	done := make(chan int64)
	go func() { n, _ := io.Copy(io.Discard, resp.Body); done <- n }()
	select {
	case n := <-done:
		if n >= int64(len(e.data)) {
			t.Fatalf("read %d bytes of a file never sent whole", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read did not end when the process went")
	}
}

func TestInputRelay_BadPart(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/media/1", "Film/x.mp4")
	for _, n := range []string{"7", "3", "-1", "+0", "00", "1e0", "x", ""} {
		w := e.get("GET", "/api/addon-input/"+id+"/"+n+"?k="+relayToken, loopAddr)
		if w.Code != 404 {
			t.Errorf("part %q: %d", n, w.Code)
		}
	}
	if n := e.seenCount(); n != 0 {
		t.Fatalf("the addon was asked %d times", n)
	}
}

func TestInputRelay_NoGzip(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/media/1", "Film/x.mp4")
	w := e.get("GET", relayPath(id, 0), loopAddr, "Accept-Encoding", "gzip")
	if w.Code != 200 || w.Body.Len() != len(e.data) {
		t.Fatalf("GET: %d, %d bytes", w.Code, w.Body.Len())
	}
	if w.Header().Get("Content-Length") != "1048576" || w.Header().Get("Content-Encoding") != "" {
		t.Errorf("headers %v", w.Header())
	}
	if ae := e.last().header.Get("Accept-Encoding"); ae != "" {
		t.Errorf("addon asked with Accept-Encoding %q", ae)
	}
}

func TestInputRelay_MultiRange(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/media/1", "Film/x.mp4")
	w := e.get("GET", relayPath(id, 0), loopAddr, "Range", "bytes=0-1,5-6")
	if w.Code != 416 {
		t.Fatalf("multi-range: %d", w.Code)
	}
	if cr := w.Header().Get("Content-Range"); cr != "bytes */1048576" {
		t.Errorf("Content-Range %q", cr)
	}
	// a subtitle sidecar's size is unknown: no Content-Range
	w = e.get("GET", relayPath(id, 2), loopAddr, "Range", "bytes=0-1,5-6")
	if w.Code != 416 || w.Header().Get("Content-Range") != "" {
		t.Errorf("multi-range of a part of unknown size: %d, Content-Range %q", w.Code, w.Header().Get("Content-Range"))
	}
	if n := e.seenCount(); n != 0 {
		t.Fatalf("the addon was asked %d times", n)
	}
}

func TestInputRelay_IfRange(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/media/1", "Film/x.mp4")
	lm := "Fri, 02 Jan 2026 03:04:05 GMT"
	w := e.get("GET", relayPath(id, 0), loopAddr, "Range", "bytes=0-9", "If-Range", lm, "Cookie", "s=1", "Authorization", "Bearer x")
	if w.Code != 206 {
		t.Fatalf("If-Range: %d", w.Code)
	}
	h := e.last().header
	if h.Get("If-Range") != lm || h.Get("Cookie") != "" || h.Get("Authorization") != "" {
		t.Errorf("addon headers %v", h)
	}
}

func TestInputPlay_InternalURL(t *testing.T) {
	p := newInputPlay(nil, Deps{InputBase: "http://127.0.0.1:8420/", InputToken: "t0k"})
	if u := p.internalURL("ab", 3); u != "http://127.0.0.1:8420/api/addon-input/ab/3?k=t0k" {
		t.Fatalf("internalURL %q", u)
	}
	if s := redactToken("ffprobe: " + p.internalURL("ab", 3) + ": 404"); strings.Contains(s, "t0k") {
		t.Fatalf("redacted %q", s)
	}
}

func TestMediaRoute_Inputs(t *testing.T) {
	for p, want := range map[string]bool{
		"/api/play/input/x/direct":   true,
		"/api/addon-input/x/0":       true,
		"/api/play/input/x":          false,
		"/api/play/input/x/progress": false,
		"/api/play/input/x/info":     false,
	} {
		if got := mediaRoute(p); got != want {
			t.Errorf("%s: %v, want %v", p, got, want)
		}
	}
}

func TestInputPlay_InputFor(t *testing.T) {
	e := newRelayEnv(t)
	id := e.add("/media/1", "Film/x.mp4")
	p := newInputPlay(nil, Deps{AddonInputs: e.inputs, Addons: e.fake})
	ask := func(u auth.User, id string) int {
		r := httptest.NewRequest("GET", "/api/play/input/"+id, nil)
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", id)
		r = r.WithContext(context.WithValue(auth.WithUser(r.Context(), u), chi.RouteCtxKey, rc))
		w := httptest.NewRecorder()
		if in, ok := p.inputFor(w, r); ok {
			if in.ID != id {
				t.Fatalf("input %q for %q", in.ID, id)
			}
			return 200
		}
		if !strings.Contains(w.Body.String(), "Видео не найдено") {
			t.Errorf("refusal %q", w.Body)
		}
		return w.Code
	}
	owner := auth.User{ID: e.uid, Name: "Маша"}
	if c := ask(owner, id); c != 200 {
		t.Fatalf("owner: %d", c)
	}
	if c := ask(auth.User{ID: e.uid + 1}, id); c != 404 {
		t.Errorf("another profile: %d", c)
	}
	if c := ask(auth.User{ID: e.uid + 1, IsAdmin: true}, id); c != 404 {
		t.Errorf("another profile, admin: %d", c)
	}
	if c := ask(owner, strings.Repeat("b", 32)); c != 404 {
		t.Errorf("unknown id: %d", c)
	}
	// the addon is no longer open to adults: only an admin owner keeps it
	e.fake.mu.Lock()
	pl := e.fake.installed["pl"]
	pl.Manifest.Access = ""
	e.fake.installed["pl"] = pl
	e.fake.mu.Unlock()
	if c := ask(owner, id); c != 404 {
		t.Errorf("access dropped: %d", c)
	}
	in, err := e.inputs.Add("pl", addonkit.InputRequest{UserID: 99, Path: "/media/1", Name: "a.mp4", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c := ask(auth.User{ID: 99, IsAdmin: true}, in.ID); c != 200 {
		t.Errorf("admin owner after access dropped: %d", c)
	}
	// a dead process: 404
	e.fake.mu.Lock()
	e.fake.status["pl"] = addons.StatusCrashed
	e.fake.mu.Unlock()
	if c := ask(auth.User{ID: 99, IsAdmin: true}, in.ID); c != 404 {
		t.Errorf("dead process: %d", c)
	}
}
