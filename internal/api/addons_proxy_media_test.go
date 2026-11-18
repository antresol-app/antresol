package api

import (
	"compress/gzip"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/addons"
)

// mediaSrv is a fake addon process serving media on a Unix socket; it
// counts the connections the core opens to it and those closed again.
type mediaSrv struct {
	sock           string
	srv            *httptest.Server
	opened, closed atomic.Int64
	requests       atomic.Int64
}

// mediaAddon answers /api/seg.ts (video/mp2t), /api/index.m3u8 (an HLS
// playlist), /api/gone 410, /api/missing 404, /api/range 416 and
// /api/hold (waits until release is closed, for parallel requests).
func mediaAddon(t *testing.T, release chan struct{}) *mediaSrv {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "am")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "m.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	m := &mediaSrv{sock: sock}
	m.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.requests.Add(1)
		body := strings.Repeat("media ", 200) // worth compressing, if anyone tried
		switch r.URL.Path {
		case "/api/seg.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			fmt.Fprint(w, body)
		case "/api/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#"+body+"\n")
		case "/api/gone":
			http.Error(w, "session expired", http.StatusGone)
		case "/api/missing":
			http.Error(w, "no such segment", http.StatusNotFound)
		case "/api/range":
			w.Header().Set("Content-Range", "bytes */1000")
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
		case "/api/hold":
			<-release
			fmt.Fprint(w, "ok")
		default:
			http.NotFound(w, r)
		}
	}))
	m.srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		switch st {
		case http.StateNew:
			m.opened.Add(1)
		case http.StateClosed, http.StateHijacked:
			m.closed.Add(1)
		}
	}
	m.srv.Listener.Close()
	m.srv.Listener = ln
	m.srv.Start()
	t.Cleanup(func() {
		m.srv.Close()
		os.RemoveAll(dir)
	})
	return m
}

// serveAs makes id a running addon on m's socket (a new process
// generation when it had another socket before).
func (e *addonEnv) serveAs(id string, m *mediaSrv) {
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if _, ok := e.fake.installed[id]; !ok {
		in := e.fake.installed["hello"]
		in.ID, in.Manifest.ID = id, id
		e.fake.installed[id] = in
	}
	e.fake.status[id] = addons.StatusRunning
	e.fake.sock[id] = m.sock
}

// waitConns polls ok for up to 5 s (connection states arrive from the
// server's own goroutines).
func waitConns(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// TestProxy_PassesStreamStatuses: an expired stream session, a missing
// segment and an unsatisfiable range reach the player as the addon sent
// them (the player re-resolves on 410), never as a 502.
func TestProxy_PassesStreamStatuses(t *testing.T) {
	e := newAddonEnv(t)
	e.serveAs("hello", mediaAddon(t, nil))
	for _, c := range []struct {
		path, body string
		code       int
	}{{"gone", "session expired", 410}, {"missing", "no such segment", 404}, {"range", "bad range", 416}} {
		w := e.do("GET", "/api/addons/hello/x/"+c.path, "", e.admin, "Range", "bytes=5000-")
		if w.Code != c.code || strings.TrimSpace(w.Body.String()) != c.body {
			t.Errorf("%s: %d %q, want %d %q", c.path, w.Code, w.Body, c.code, c.body)
		}
		if c.code == 416 && w.Header().Get("Content-Range") != "bytes */1000" {
			t.Errorf("416 lost its Content-Range: %v", w.Header())
		}
	}
}

// TestProxy_MediaNotCompressed: segments and playlists pass unwrapped
// (sendfile-friendly, byte ranges intact). Non-JSON answers carry
// Content-Security-Policy: sandbox, which hls.js fetches ignore.
func TestProxy_MediaNotCompressed(t *testing.T) {
	e := newAddonEnv(t)
	e.serveAs("hello", mediaAddon(t, nil))
	for _, p := range []string{"seg.ts", "index.m3u8"} {
		w := e.do("GET", "/api/addons/hello/x/"+p, "", e.admin, "Accept-Encoding", "gzip")
		if w.Code != 200 || w.Header().Get("Content-Encoding") != "" {
			t.Fatalf("%s: %d, Content-Encoding %q", p, w.Code, w.Header().Get("Content-Encoding"))
		}
		if _, err := gzip.NewReader(strings.NewReader(w.Body.String())); err == nil {
			t.Fatalf("%s: body is gzip", p)
		}
		if !strings.Contains(w.Body.String(), "media media") {
			t.Fatalf("%s: body %q", p, w.Body.String()[:min(40, w.Body.Len())])
		}
		if w.Header().Get("Content-Security-Policy") != "sandbox" {
			t.Errorf("%s: CSP %q", p, w.Header().Get("Content-Security-Policy"))
		}
	}
}

// TestProxy_ReusesSocketConnections: segment after segment rides kept
// connections, and at least 16 parallel requests stay pooled.
func TestProxy_ReusesSocketConnections(t *testing.T) {
	e := newAddonEnv(t)
	release := make(chan struct{})
	m := mediaAddon(t, release)
	e.serveAs("hello", m)
	for i := 0; i < 20; i++ {
		if w := e.do("GET", "/api/addons/hello/x/seg.ts", "", e.admin); w.Code != 200 {
			t.Fatalf("segment %d: %d", i, w.Code)
		}
	}
	if n := m.opened.Load(); n != 1 {
		t.Fatalf("20 sequential segments opened %d connections, want 1", n)
	}
	// 16 at once: 16 connections, all kept idle afterwards
	const par = 16
	var wg sync.WaitGroup
	for i := 0; i < par; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := e.do("GET", "/api/addons/hello/x/hold", "", e.admin); w.Code != 200 {
				t.Errorf("hold: %d", w.Code)
			}
		}()
	}
	waitConns(t, "16 requests in flight", func() bool { return m.requests.Load() == 20+par })
	close(release)
	wg.Wait()
	opened := m.opened.Load()
	if opened > par {
		t.Fatalf("%d parallel requests opened %d connections", par, opened)
	}
	for i := 0; i < par; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.do("GET", "/api/addons/hello/x/seg.ts", "", e.admin)
		}()
	}
	wg.Wait()
	if n := m.opened.Load(); n != opened {
		t.Fatalf("a second burst of %d opened %d new connections: idle ones were not kept", par, n-opened)
	}
	if n := m.closed.Load(); n != 0 {
		t.Fatalf("%d connections closed while the addon runs", n)
	}
}

// TestProxy_NoConnReuseAcrossAddons: a request to B never rides A's idle
// connection.
func TestProxy_NoConnReuseAcrossAddons(t *testing.T) {
	e := newAddonEnv(t)
	a, b := mediaAddon(t, nil), mediaAddon(t, nil)
	e.serveAs("hello", a)
	e.serveAs("other", b)
	for i := 0; i < 3; i++ {
		if w := e.do("GET", "/api/addons/hello/x/seg.ts", "", e.admin); w.Code != 200 {
			t.Fatalf("A: %d", w.Code)
		}
		if w := e.do("GET", "/api/addons/other/x/seg.ts", "", e.admin); w.Code != 200 {
			t.Fatalf("B: %d", w.Code)
		}
	}
	if a.requests.Load() != 3 || b.requests.Load() != 3 {
		t.Fatalf("A got %d requests, B got %d; want 3 each", a.requests.Load(), b.requests.Load())
	}
	if a.opened.Load() != 1 || b.opened.Load() != 1 {
		t.Fatalf("connections: A %d, B %d; want 1 each", a.opened.Load(), b.opened.Load())
	}
}

// TestProxy_IdleConnsClosedOnRestart: after a restart (a new process on a
// new socket) no request uses a connection to the old process, and the
// old process's idle connections are closed; a stopped addon's too.
func TestProxy_IdleConnsClosedOnRestart(t *testing.T) {
	e := newAddonEnv(t)
	old, cur := mediaAddon(t, nil), mediaAddon(t, nil)
	e.serveAs("hello", old)
	for i := 0; i < 3; i++ {
		e.do("GET", "/api/addons/hello/x/seg.ts", "", e.admin)
	}
	if old.opened.Load() != 1 || old.closed.Load() != 0 {
		t.Fatalf("before the restart: opened %d, closed %d", old.opened.Load(), old.closed.Load())
	}
	e.serveAs("hello", cur) // the restart: the old process keeps running a moment
	for i := 0; i < 3; i++ {
		if w := e.do("GET", "/api/addons/hello/x/seg.ts", "", e.admin); w.Code != 200 {
			t.Fatalf("after the restart: %d", w.Code)
		}
	}
	if old.requests.Load() != 3 || cur.requests.Load() != 3 {
		t.Fatalf("requests: old %d, new %d; want 3 each", old.requests.Load(), cur.requests.Load())
	}
	waitConns(t, "the old process's idle connection closed", func() bool { return old.closed.Load() == old.opened.Load() })

	// stopped: the request is refused and the idle connection closed
	e.fake.mu.Lock()
	e.fake.status["hello"] = addons.StatusStopped
	e.fake.mu.Unlock()
	if w := e.do("GET", "/api/addons/hello/x/seg.ts", "", e.admin); w.Code != 503 {
		t.Fatalf("stopped: %d", w.Code)
	}
	waitConns(t, "the stopped addon's idle connection closed", func() bool { return cur.closed.Load() == cur.opened.Load() })
}

// TestProxy_PoolDroppedByStatusFeed: the runtime's status feed closes an
// addon's pool as soon as the addon leaves running, without waiting for
// the next request.
func TestProxy_PoolDroppedByStatusFeed(t *testing.T) {
	e := newAddonEnv(t)
	m := mediaAddon(t, nil)
	e.serveAs("hello", m)
	a := newAddonsAPI(Deps{Addons: e.fake})
	tr := a.pools.get("hello", m.sock)
	if a.pools.get("hello", m.sock) != tr {
		t.Fatal("the same process got a second pool")
	}
	a.pools.statusChanged("hello", addons.StatusRunning)
	if a.pools.get("hello", m.sock) != tr {
		t.Fatal("running dropped the pool")
	}
	for _, st := range []addons.Status{addons.StatusStopped, addons.StatusCrashed, addons.StatusStarting} {
		a.pools.statusChanged("hello", st)
		next := a.pools.get("hello", m.sock)
		if next == tr {
			t.Fatalf("%s kept the pool", st)
		}
		tr = next
	}
}
