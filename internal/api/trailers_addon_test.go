package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

const (
	trKey1 = "trailer0001"
	trKey2 = "trailer0002"
	trKey3 = "trailer0003"
)

type trailerAnswer struct {
	URL        string `json:"url"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error"`
}

// mkOf is the media key of a play URL.
func mkOf(t *testing.T, u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Query().Get("mk") == "" {
		t.Fatalf("no media key in %q", u)
	}
	return p.Query().Get("mk")
}

// fakeTrailerAddon answers trailers.fetch with fetch (nil: 404) and serves
// /media/<key>.mp4 from files, recording every request.
type fakeTrailerAddon struct {
	mu    sync.Mutex
	fetch func(req addonkit.TrailerFetch) (int, any)
	files map[string][]byte // path → bytes
	calls []string          // "fetch <key>", "GET /media/…", "HEAD …"
	hdrs  []http.Header     // of the media reads
	// media answers a media read instead of the files (nil: the files)
	media http.HandlerFunc
}

func (a *fakeTrailerAddon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	fetch := a.fetch
	a.mu.Unlock()
	if r.URL.Path == "/hooks/"+addons.TrailersHook {
		var req addonkit.TrailerFetch
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		a.mu.Lock()
		a.calls = append(a.calls, "fetch "+req.Key)
		a.mu.Unlock()
		if fetch == nil {
			http.NotFound(w, r)
			return
		}
		code, body := fetch(req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(body)
		return
	}
	a.mu.Lock()
	a.calls = append(a.calls, r.Method+" "+r.URL.Path)
	a.hdrs = append(a.hdrs, r.Header.Clone())
	b, ok := a.files[r.URL.Path]
	media := a.media
	a.mu.Unlock()
	if media != nil {
		media(w, r)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html") // never passed on
	w.Header().Set("Set-Cookie", "x=1")
	http.ServeContent(w, r, "t.mp4", time.Unix(1700000000, 0), bytes.NewReader(b))
}

func (a *fakeTrailerAddon) setFetch(f func(addonkit.TrailerFetch) (int, any)) {
	a.mu.Lock()
	a.fetch = f
	a.mu.Unlock()
}

func (a *fakeTrailerAddon) callList() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.calls...)
}

func (a *fakeTrailerAddon) fetches() int {
	n := 0
	for _, c := range a.callList() {
		if strings.HasPrefix(c, "fetch ") {
			n++
		}
	}
	return n
}

// readyFile is a 200 answer naming /media/<key>.mp4.
func readyFile(key string, size int) (int, any) {
	return 200, addonkit.TrailerFile{Path: "/media/" + key + ".mp4", Size: int64(size), DurationMs: 2000, ContentType: "video/mp4"}
}

type addonTrailerEnv struct {
	*libEnv
	fake  *fakeAddons
	tr    *addons.Trailers
	movie int64
	data  []byte
}

// newAddonTrailerEnv: a movie rated 18+ whose trailers are trKey1..3, the
// addon trailer provider over fake addons (none installed yet) and media
// keys.
func newAddonTrailerEnv(t *testing.T) *addonTrailerEnv {
	oldWait := trailerPlayWait
	trailerPlayWait = 1500 * time.Millisecond
	t.Cleanup(func() { trailerPlayWait = oldWait })
	e := &addonTrailerEnv{fake: &fakeAddons{installed: map[string]addons.Installed{}, status: map[string]addons.Status{},
		sock: map[string]string{}, tokens: map[string]string{}, webRoot: t.TempDir()}}
	e.data = make([]byte, 64<<10)
	for i := range e.data {
		e.data[i] = byte(i * 7)
	}
	e.tr = &addons.Trailers{H: &addons.Hooks{T: e.fake}}
	src := &fakeTrailers{vs: []tmdb.Video{
		{Name: "Main", Site: "YouTube", Key: trKey1, Type: "Trailer", Lang: "ru", Official: true},
		{Name: "Second", Site: "YouTube", Key: trKey2, Type: "Trailer", Lang: "ru"},
		{Name: "Teaser", Site: "YouTube", Key: trKey3, Type: "Teaser", Lang: "ru"},
	}}
	e.libEnv = newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		withMediaKeys(d, db)
		d.Trailers = src
		d.Addons = e.fake
		d.TrailerAddons = e.tr
	})
	e.movie, _ = e.libEnv.movie("Фильм", "18+", "0123456789abcdef")
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=603 WHERE id=?`, e.movie); err != nil {
		t.Fatal(err)
	}
	return e
}

// addon starts a fake addon id on a socket of its own; declare: it lists
// trailers.fetch in its manifest.
func (e *addonTrailerEnv) addon(id string, declare bool) *fakeTrailerAddon {
	e.t.Helper()
	a := &fakeTrailerAddon{files: map[string][]byte{}}
	for _, k := range []string{trKey1, trKey2, trKey3} {
		a.files["/media/"+k+".mp4"] = e.data
	}
	dir, err := os.MkdirTemp("/tmp", "tr")
	if err != nil {
		e.t.Fatal(err)
	}
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		e.t.Fatal(err)
	}
	srv := &http.Server{Handler: a}
	go srv.Serve(ln)
	e.t.Cleanup(func() { srv.Close(); os.RemoveAll(dir) })
	var hooks []string
	if declare {
		hooks = []string{addons.TrailersHook}
	}
	e.fake.mu.Lock()
	e.fake.installed[id] = addons.Installed{ID: id, Version: "1.0.0", Enabled: true,
		Manifest: addons.Manifest{ID: id, Name: addons.Text{RU: id, EN: id}, Version: "1.0.0", Contract: addons.ContractVersion, Hooks: hooks}}
	e.fake.status[id], e.fake.sock[id] = addons.StatusRunning, sock
	e.fake.mu.Unlock()
	return a
}

func (e *addonTrailerEnv) setStatus(id string, st addons.Status) {
	e.fake.mu.Lock()
	e.fake.status[id] = st
	e.fake.mu.Unlock()
}

func (e *addonTrailerEnv) play(key string, c *http.Cookie, hdr ...string) (int, trailerAnswer, string) {
	e.t.Helper()
	w := e.do("POST", "/api/trailers/"+key+"/play", `{}`, c, hdr...)
	var a trailerAnswer
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	return w.Code, a, w.Header().Get("Retry-After")
}

// list answers the movie's trailers and waits for the prefetch.
func (e *addonTrailerEnv) list() []Trailer {
	ts := e.trailersOf(e.movie)
	e.tr.Wait()
	return ts
}

func TestAddonTrailersPlayOnlyWithADeclaringRunningAddon(t *testing.T) {
	e := newAddonTrailerEnv(t)
	noPlay := func(why string) {
		t.Helper()
		for _, tr := range e.list() {
			if tr.Play != "" {
				t.Fatalf("%s: play %+v", why, tr)
			}
		}
		if code, _, _ := e.play(trKey1, e.admin); code != 404 {
			t.Fatalf("%s: play %d", why, code)
		}
		if w := e.do("GET", "/api/trailers/"+trKey1+".mp4", "", e.admin); w.Code != 404 {
			t.Fatalf("%s: file %d", why, w.Code)
		}
	}
	noPlay("no addon")
	other := e.addon("aaa", false) // runs, does not declare the hook
	other.setFetch(func(r addonkit.TrailerFetch) (int, any) { return readyFile(r.Key, 10) })
	noPlay("an addon without the hook")
	a := e.addon("zzz", true)
	a.setFetch(func(r addonkit.TrailerFetch) (int, any) { return readyFile(r.Key, len(e.data)) })
	e.setStatus("zzz", addons.StatusStarting)
	noPlay("a starting addon")
	e.setStatus("zzz", addons.StatusRunning)
	for _, tr := range e.list() {
		if tr.Play != "/api/trailers/"+tr.Key+"/play" {
			t.Fatalf("play %+v", tr)
		}
	}
	if other.fetches() != 0 {
		t.Fatalf("asked an addon that does not declare the hook: %v", other.callList())
	}
	e.setStatus("zzz", addons.StatusCrashed)
	noPlay("a crashed addon")
}

func TestAddonTrailersPlayAndRangeFromTheSocket(t *testing.T) {
	e := newAddonTrailerEnv(t)
	a := e.addon("trailers", true)
	var mu sync.Mutex
	var bodies []addonkit.TrailerFetch
	a.setFetch(func(r addonkit.TrailerFetch) (int, any) {
		mu.Lock()
		bodies = append(bodies, r)
		mu.Unlock()
		return readyFile(r.Key, len(e.data))
	})
	// never listed: 404 without asking
	if code, _, _ := e.play(trKey1, e.admin); code != 404 || a.fetches() != 0 {
		t.Fatalf("unlisted: %d %v", code, a.callList())
	}
	for _, bad := range []string{"short", "trailer0001x", "trail%2F0001", "..%2F..%2Fetc"} {
		if code, _, _ := e.play(bad, e.admin); code != 404 {
			t.Fatalf("bad key %q: %d", bad, code)
		}
	}
	// the list prefetches the first one, once a day
	e.list()
	e.list()
	if got := a.callList(); len(got) != 1 || got[0] != "fetch "+trKey1 {
		t.Fatalf("prefetch %v", got)
	}
	mu.Lock()
	if b := bodies[0]; b.Key != trKey1 || b.URL != trailerURL(trKey1) || b.MaxHeight != 0 {
		t.Fatalf("body %+v", b)
	}
	mu.Unlock()
	code, ans, _ := e.play(trKey1, e.admin)
	if code != 200 || ans.URL != "/api/trailers/"+trKey1+".mp4?mk="+url.QueryEscape(mkOf(t, ans.URL)) || ans.DurationMs != 2000 {
		t.Fatalf("%d %+v", code, ans)
	}
	// with the URL alone (its media key): Range read from the addon's socket
	w := e.do("GET", ans.URL, "", nil, "Range", "bytes=100-199", "Cookie", "x=y", "X-Antresol-User", "{}")
	if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), e.data[100:200]) || w.Header().Get("Content-Type") != "video/mp4" ||
		w.Header().Get("Content-Range") != "bytes 100-199/65536" || w.Header().Get("X-Content-Type-Options") != "nosniff" ||
		w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	a.mu.Lock()
	h := a.hdrs[len(a.hdrs)-1]
	a.mu.Unlock()
	if h.Get("Range") != "bytes=100-199" || h.Get("Cookie") != "" || h.Get("X-Antresol-User") != "" || h.Get("Authorization") != "" ||
		h.Get("Accept-Encoding") != "" {
		t.Fatalf("headers sent to the addon: %v", h)
	}
	// HEAD, a whole read with a session; no auth or a forged key: 401
	if w := e.do("HEAD", "/api/trailers/"+trKey1+".mp4", "", e.admin); w.Code != 200 || w.Header().Get("Content-Length") != "65536" || w.Body.Len() != 0 {
		t.Fatalf("HEAD %d %v", w.Code, w.Header())
	}
	if w := e.do("GET", "/api/trailers/"+trKey1+".mp4", "", e.admin); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), e.data) {
		t.Fatalf("session %d", w.Code)
	}
	if w := e.do("GET", "/api/trailers/"+trKey1+".mp4", "", nil); w.Code != 401 {
		t.Fatalf("anonymous %d", w.Code)
	}
	if w := e.do("GET", "/api/trailers/"+trKey1+".mp4?mk=v1.forged", "", nil); w.Code != 401 {
		t.Fatalf("forged key %d", w.Code)
	}
	if w := e.do("POST", "/api/trailers/"+trKey1+"/play?mk="+url.QueryEscape(mkOf(t, ans.URL)), `{}`, nil); w.Code != 401 {
		t.Fatalf("keyed POST %d", w.Code)
	}
	// a multi-range read never reaches the addon
	n := len(a.callList())
	if w := e.do("GET", "/api/trailers/"+trKey1+".mp4", "", e.admin, "Range", "bytes=0-1,5-6"); w.Code != 416 || len(a.callList()) != n {
		t.Fatalf("multi-range %d", w.Code)
	}
	if w := e.do("GET", "/api/trailers/"+trKey1+".webm", "", e.admin); w.Code != 404 {
		t.Fatalf("not .mp4 %d", w.Code)
	}
	// a key with no 200 kept: one fetch first, then the read
	if w := e.do("GET", "/api/trailers/"+trKey3+".mp4", "", e.admin, "Range", "bytes=0-9"); w.Code != 206 || !bytes.Equal(w.Body.Bytes(), e.data[:10]) {
		t.Fatalf("unkept %d", w.Code)
	}
	// the file is gone on the addon: 404
	a.mu.Lock()
	delete(a.files, "/media/"+trKey3+".mp4")
	a.mu.Unlock()
	if w := e.do("GET", "/api/trailers/"+trKey3+".mp4", "", e.admin); w.Code != 404 {
		t.Fatalf("gone %d", w.Code)
	}
	// still fetching on a read with none kept: 404, no waiting
	a.setFetch(func(addonkit.TrailerFetch) (int, any) { return 202, addonkit.TrailerPending{RetryAfterMs: 250} })
	if w := e.do("GET", "/api/trailers/"+trKey2+".mp4", "", e.admin); w.Code != 404 {
		t.Fatalf("pending read %d", w.Code)
	}
}

func TestAddonTrailersRestartAsksAgain(t *testing.T) {
	e := newAddonTrailerEnv(t)
	a := e.addon("trailers", true)
	a.setFetch(func(r addonkit.TrailerFetch) (int, any) { return readyFile(r.Key, len(e.data)) })
	e.list()
	if code, _, _ := e.play(trKey1, e.admin); code != 200 {
		t.Fatal(code)
	}
	// the addon restarts on another socket: the kept 200 is forgotten
	b := e.addon("trailers", true)
	b.setFetch(func(r addonkit.TrailerFetch) (int, any) { return readyFile(r.Key, len(e.data)) })
	if w := e.do("GET", "/api/trailers/"+trKey1+".mp4", "", e.admin, "Range", "bytes=0-9"); w.Code != 206 {
		t.Fatalf("after restart %d", w.Code)
	}
	if b.fetches() != 1 {
		t.Fatalf("the new process: %v", b.callList())
	}
}

func TestAddonTrailersPendingHoldThen503(t *testing.T) {
	e := newAddonTrailerEnv(t)
	a := e.addon("trailers", true)
	a.setFetch(func(addonkit.TrailerFetch) (int, any) { return 202, addonkit.TrailerPending{RetryAfterMs: 1} })
	e.list()
	start := time.Now()
	code, ans, ra := e.play(trKey1, e.admin)
	if code != 503 || ra != "2" || ans.Error == "" {
		t.Fatalf("still fetching %d %q %+v", code, ra, ans)
	}
	if d := time.Since(start); d < trailerPlayWait {
		t.Fatalf("answered after %v, before the hold", d)
	}
	// asked again while it held (retryAfterMs taken as at least 250 ms)
	if n := a.fetches(); n < 4 || n > 8 {
		t.Fatalf("asked %d times in %v", n, trailerPlayWait)
	}
	// ready during the hold: 200
	var mu sync.Mutex
	asks := 0
	a.setFetch(func(r addonkit.TrailerFetch) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		if asks++; asks < 3 {
			return 202, addonkit.TrailerPending{RetryAfterMs: 300}
		}
		return readyFile(r.Key, len(e.data))
	})
	if code, ans, _ := e.play(trKey2, e.admin); code != 200 || ans.DurationMs != 2000 {
		t.Fatalf("ready in the hold %d %+v", code, ans)
	}
}

func TestAddonTrailersRefusalNextAndDown(t *testing.T) {
	e := newAddonTrailerEnv(t)
	first := e.addon("aaa", true)  // can't: 404
	second := e.addon("bbb", true) // refuses
	third := e.addon("ccc", true)  // never reached while bbb answers
	second.setFetch(func(addonkit.TrailerFetch) (int, any) {
		return 409, map[string]string{"error": "Видео недоступно в вашей стране"}
	})
	third.setFetch(func(r addonkit.TrailerFetch) (int, any) { return readyFile(r.Key, len(e.data)) })
	e.list()
	code, ans, _ := e.play(trKey1, e.admin)
	if code != 502 || ans.Error != "Видео недоступно в вашей стране" {
		t.Fatalf("refusal %d %+v", code, ans)
	}
	if first.fetches() == 0 || third.fetches() != 0 {
		t.Fatalf("order: %v %v", first.callList(), third.callList())
	}
	// bbb can't either: the next one plays it
	second.setFetch(nil)
	if code, _, _ := e.play(trKey1, e.admin); code != 200 {
		t.Fatalf("404 → next %d", code)
	}
	// bbb is down (its socket refuses) or fails: skipped
	e.fake.mu.Lock()
	e.fake.sock["bbb"] = "/tmp/no-such-socket"
	e.fake.mu.Unlock()
	if code, _, _ := e.play(trKey2, e.admin); code != 200 || third.fetches() < 2 {
		t.Fatalf("down → next %d", code)
	}
	second2 := e.addon("bbb", true)
	second2.setFetch(func(addonkit.TrailerFetch) (int, any) { return 500, map[string]string{"error": "boom"} })
	if code, _, _ := e.play(trKey3, e.admin); code != 200 {
		t.Fatalf("failure → next %d", code)
	}
	// a 200 breaking a rule is a failure; with nobody else: 502, translated
	for _, bad := range []addonkit.TrailerFile{
		{Path: "/elsewhere/x.mp4", Size: 1, ContentType: "video/mp4"},
		{Path: "/media/../hooks/x", Size: 1, ContentType: "video/mp4"},
		{Path: "/media/x.mp4", Size: 0, ContentType: "video/mp4"},
		{Path: "/media/x.mp4", Size: 1, ContentType: "text/html"},
		{Path: "/media/x.mp4", Size: 1, DurationMs: -1, ContentType: "video/mp4"},
	} {
		third.setFetch(func(addonkit.TrailerFetch) (int, any) { return 200, bad })
		code, ans, _ := e.play(trKey1, e.admin, "Accept-Language", "en")
		if code != 502 || ans.Error != "Couldn’t load the trailer" {
			t.Fatalf("bad 200 %+v: %d %+v", bad, code, ans)
		}
	}
	// every addon can't: 404
	third.setFetch(nil)
	second2.setFetch(nil)
	if code, _, _ := e.play(trKey1, e.admin); code != 404 {
		t.Fatalf("nobody can %d", code)
	}
}

func TestAddonTrailersKidGatedBeforeTheHook(t *testing.T) {
	e := newAddonTrailerEnv(t)
	a := e.addon("trailers", true)
	a.setFetch(func(r addonkit.TrailerFetch) (int, any) { return readyFile(r.Key, len(e.data)) })
	e.list()
	n := a.fetches()
	kid := e.kid("12+")
	if code, _, _ := e.play(trKey2, kid); code != 404 {
		t.Fatalf("kid play %d", code)
	}
	if w := e.do("GET", "/api/trailers/"+trKey2+".mp4", "", kid); w.Code != 404 {
		t.Fatalf("kid file %d", w.Code)
	}
	if a.fetches() != n || len(a.callList()) != n {
		t.Fatalf("the addon was asked for a kid: %v", a.callList())
	}
}

// TestAddonTrailersRelayOverTheWire reads through a real server, so a
// relayed header that does not match the body breaks the client.
func TestAddonTrailersRelayOverTheWire(t *testing.T) {
	e := newAddonTrailerEnv(t)
	a := e.addon("trailers", true)
	a.setFetch(func(r addonkit.TrailerFetch) (int, any) { return readyFile(r.Key, len(e.data)) })
	e.list()
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	get := func(hdr ...string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+"/api/trailers/"+trKey1+".mp4", nil)
		req.AddCookie(e.admin)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("%v: body: %v", hdr, err)
		}
		return resp, b
	}
	lastHdr := func() http.Header {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.hdrs[len(a.hdrs)-1]
	}
	// a range past the end: the addon's 416, its Content-Range, no length
	resp, b := get("Range", "bytes=999999-")
	if resp.StatusCode != 416 || len(b) != 0 || resp.Header.Get("Content-Range") != "bytes */65536" ||
		resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("416: %d %v %q", resp.StatusCode, resp.Header, b)
	}
	// If-Range reaches the addon: a matching one keeps the range, a stale one is the whole file
	lm := time.Unix(1700000000, 0).UTC().Format(http.TimeFormat)
	resp, b = get("Range", "bytes=0-9", "If-Range", lm)
	if resp.StatusCode != 206 || !bytes.Equal(b, e.data[:10]) || lastHdr().Get("If-Range") != lm || lastHdr().Get("Range") != "bytes=0-9" {
		t.Fatalf("if-range: %d %v", resp.StatusCode, lastHdr())
	}
	stale := time.Unix(1600000000, 0).UTC().Format(http.TimeFormat)
	if resp, b = get("Range", "bytes=0-9", "If-Range", stale); resp.StatusCode != 200 || !bytes.Equal(b, e.data) {
		t.Fatalf("stale if-range: %d", resp.StatusCode)
	}
	// a multi-range read: 416 at once (no-store, nosniff); with If-Range the whole file
	n := len(a.callList())
	resp, b = get("Range", "bytes=0-1,5-6")
	if resp.StatusCode != 416 || len(b) != 0 || len(a.callList()) != n || resp.Header.Get("Cache-Control") != "no-store" ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Content-Range") != "bytes */65536" {
		t.Fatalf("multi-range: %d %v", resp.StatusCode, resp.Header)
	}
	resp, b = get("Range", "bytes=0-1,5-6", "If-Range", lm)
	if resp.StatusCode != 200 || !bytes.Equal(b, e.data) || lastHdr().Get("Range") != "" || lastHdr().Get("If-Range") != "" {
		t.Fatalf("multi-range with If-Range: %d %v", resp.StatusCode, lastHdr())
	}
	// the addon fails the read or redirects it: 502, never followed
	var followed atomic.Bool
	for name, h := range map[string]http.HandlerFunc{
		"500": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) },
		"302": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/elsewhere" {
				followed.Store(true)
			}
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		},
	} {
		a.mu.Lock()
		a.media = h
		a.mu.Unlock()
		resp, b := get("Range", "bytes=0-9")
		var ans trailerAnswer
		if resp.StatusCode != 502 || json.Unmarshal(b, &ans) != nil || ans.Error == "" || resp.Header.Get("Location") != "" {
			t.Fatalf("%s: %d %v %s", name, resp.StatusCode, resp.Header, b)
		}
	}
	if followed.Load() {
		t.Fatal("followed the addon's redirect")
	}
}

// noWrite records whether the handler wrote anything.
type noWrite struct {
	h     http.Header
	wrote atomic.Bool
}

func (n *noWrite) Header() http.Header         { return n.h }
func (n *noWrite) Write(b []byte) (int, error) { n.wrote.Store(true); return len(b), nil }
func (n *noWrite) WriteHeader(int)             { n.wrote.Store(true) }

func TestAddonTrailersCallerGoneDuringTheHold(t *testing.T) {
	e := newAddonTrailerEnv(t)
	a := e.addon("trailers", true)
	a.setFetch(func(addonkit.TrailerFetch) (int, any) { return 202, addonkit.TrailerPending{RetryAfterMs: 250} })
	e.list()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequestWithContext(ctx, "POST", "/api/trailers/"+trKey1+"/play", strings.NewReader(`{}`))
	r.RemoteAddr = "192.168.1.20:50000"
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(e.admin)
	w := &noWrite{h: http.Header{}}
	time.AfterFunc(600*time.Millisecond, cancel)
	start := time.Now()
	e.h.ServeHTTP(w, r)
	if w.wrote.Load() {
		t.Fatal("answered a caller that went away")
	}
	if d := time.Since(start); d >= trailerPlayWait {
		t.Fatalf("held for %v after the caller went", d)
	}
	// the addon was not counted as down: the next play asks it again
	n := a.fetches()
	if code, _, _ := e.play(trKey1, e.admin); code != 503 || a.fetches() <= n {
		t.Fatalf("after: %d, asks %d → %d", code, n, a.fetches())
	}
}
