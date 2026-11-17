package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// profile creates a profile and returns its id and a session token.
func (e *relayEnv) profile(u auth.NewUser) (int64, string) {
	e.t.Helper()
	nu, err := e.users.CreateUser(context.Background(), u)
	if err != nil {
		e.t.Fatal(err)
	}
	return nu.ID, e.session(nu.ID)
}

func (e *relayEnv) session(uid int64) string {
	e.t.Helper()
	tok, err := e.ss.Create(context.Background(), uid)
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

// mediaKey issues a media key for the session tok.
func (e *relayEnv) mediaKey(tok string) string {
	e.t.Helper()
	_, s, err := e.ss.LookupSession(context.Background(), tok)
	if err != nil {
		e.t.Fatal(err)
	}
	k, _ := e.keys.Issue(s, tok, time.Hour)
	return k
}

// addFor registers an input of uid (main part p, the 1 MiB file) with req's
// optional fields.
func (e *relayEnv) addFor(uid int64, p string, req addonkit.InputRequest) string {
	e.t.Helper()
	req.UserID, req.Path, req.Size = uid, p, int64(len(e.data))
	if req.Name == "" {
		req.Name = "Film/x.mkv"
	}
	in, err := e.inputs.Add("pl", req)
	if err != nil {
		e.t.Fatal(err)
	}
	return in.ID
}

// as asks the router with the session tok.
func (e *relayEnv) as(tok, method, target string, hdr ...string) *httptest.ResponseRecorder {
	return e.get(method, target, loopAddr, append([]string{"Authorization", "Bearer " + tok}, hdr...)...)
}

func (e *relayEnv) dropAccess() {
	e.fake.mu.Lock()
	pl := e.fake.installed["pl"]
	pl.Manifest.Access = ""
	e.fake.installed["pl"] = pl
	e.fake.mu.Unlock()
}

func TestInputInfo(t *testing.T) {
	e := newRelayEnv(t)
	tok := e.session(e.uid)
	media := t.TempDir()
	fp := filepath.Join(media, "Брат.mkv")
	if err := os.WriteFile(fp, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	item, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "Брат", Year: 1997, Rating: "16+",
		File: library.FileInput{Path: fp, Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.prog.Save(context.Background(), e.uid, item, 42500, 6000000); err != nil {
		t.Fatal(err)
	}
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{Name: "Films/Брат.mkv", ItemID: item, Slow: true,
		Title: &addonkit.InputTitle{Kind: "movie", TMDBID: 20992}, Meta: json.RawMessage(`{"a":1}`)})

	w := e.as(tok, "GET", "/api/play/input/"+id)
	if w.Code != 200 {
		t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q", cc)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": id, "addonId": "pl", "name": "Films/Брат.mkv", "itemId": float64(item),
		"title": map[string]any{"kind": "movie", "tmdbId": float64(20992), "season": float64(0), "episode": float64(0)},
		"slow":  true, "meta": map[string]any{"a": float64(1)}, "startSec": 42.5}
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if !bytes.Equal(gb, wb) {
		t.Errorf("info\n got %s\nwant %s", gb, wb)
	}
	if bytes.Contains(w.Body.Bytes(), []byte(relayToken)) || bytes.Contains(w.Body.Bytes(), []byte("/media/1")) {
		t.Errorf("info leaks the token or the addon's path: %s", w.Body)
	}

	// finished: start from 0
	if err := e.prog.MarkWatched(context.Background(), e.uid, item, true); err != nil {
		t.Fatal(err)
	}
	if w := e.as(tok, "GET", "/api/play/input/"+id); !strings.Contains(w.Body.String(), `"startSec":0`) {
		t.Errorf("finished: %s", w.Body)
	}

	// no itemId: startSec 0, optional fields absent
	id2 := e.addFor(e.uid, "/media/2", addonkit.InputRequest{})
	w = e.as(tok, "GET", "/api/play/input/"+id2)
	if w.Code != 200 {
		t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	got = nil
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["startSec"] != float64(0) || got["itemId"] != float64(0) || got["slow"] != false {
		t.Errorf("no item: %s", w.Body)
	}
	for _, k := range []string{"title", "meta"} {
		if _, ok := got[k]; ok {
			t.Errorf("no item: %s present: %s", k, w.Body)
		}
	}
}

func TestInputDirect_Range(t *testing.T) {
	e := newRelayEnv(t)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	w := e.as(tok, "GET", "/api/play/input/"+id+"/direct", "Range", "bytes=100-199")
	if w.Code != 206 {
		t.Fatalf("direct: %d %s", w.Code, w.Body)
	}
	if !bytes.Equal(w.Body.Bytes(), e.data[100:200]) {
		t.Fatalf("body: %d bytes, not the file's 100-199", w.Body.Len())
	}
	h := w.Header()
	if h.Get("Content-Range") != "bytes 100-199/1048576" || h.Get("Content-Type") != "video/x-matroska" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("headers %v", h)
	}
	if u := e.last().uri; u != "/media/1" {
		t.Errorf("addon asked %s", u)
	}
	// HEAD
	w = e.as(tok, "HEAD", "/api/play/input/"+id+"/direct")
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "1048576" {
		t.Errorf("HEAD: %d, %d bytes, %v", w.Code, w.Body.Len(), w.Header())
	}
	// without a session: 401, the addon not asked
	n := e.seenCount()
	if w := e.get("GET", "/api/play/input/"+id+"/direct", loopAddr); w.Code != 401 {
		t.Errorf("no session: %d", w.Code)
	}
	if e.seenCount() != n {
		t.Error("the addon was asked without a session")
	}
}

func TestInputDirect_MediaKeyCap(t *testing.T) {
	e := newRelayEnv(t)
	tok := e.session(e.uid)
	key := e.mediaKey(tok)
	id := e.addFor(e.uid, "/slow", addonkit.InputRequest{})
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	open := func(key string) *http.Response {
		t.Helper()
		resp, err := http.Get(srv.URL + "/api/play/input/" + id + "/direct?mk=" + url.QueryEscape(key))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	var held []*http.Response
	defer func() {
		for _, r := range held {
			r.Body.Close()
		}
	}()
	for i := 0; i < mediaKeyInputStreams; i++ {
		resp := open(key)
		held = append(held, resp)
		if resp.StatusCode != 200 {
			t.Fatalf("stream %d: %d", i+1, resp.StatusCode)
		}
		if _, err := io.ReadFull(resp.Body, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
	}
	resp := open(key)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 429 || !strings.Contains(string(b), "Слишком много одновременных потоков") {
		t.Fatalf("7th stream: %d %s", resp.StatusCode, b)
	}
	// another key (another session) has its own slots
	other := open(e.mediaKey(e.session(e.uid)))
	other.Body.Close()
	if other.StatusCode != 200 {
		t.Errorf("another key: %d", other.StatusCode)
	}
	// a session is not capped
	r, _ := http.NewRequest("GET", srv.URL+"/api/play/input/"+id+"/direct", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	sr, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	sr.Body.Close()
	if sr.StatusCode != 200 {
		t.Errorf("session past the key's cap: %d", sr.StatusCode)
	}
	// a slot frees when its read ends
	held[0].Body.Close()
	held = held[1:]
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp := open(key)
		resp.Body.Close()
		if resp.StatusCode == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot not freed: %d", resp.StatusCode)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestInputPlay_OtherProfile404(t *testing.T) {
	e := newRelayEnv(t)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	_, adult := e.profile(auth.NewUser{Name: "Петя"})
	_, admin := e.profile(auth.NewUser{Name: "Админ", IsAdmin: true, Password: "secret-pass-1"})
	kidID, kid := e.profile(auth.NewUser{Name: "Вася", IsKid: true})
	kidInput := e.addFor(kidID, "/media/1", addonkit.InputRequest{}) // never registered by the API; still refused
	var body string
	for _, tok := range []string{adult, admin} {
		for _, p := range []string{"/api/play/input/" + id, "/api/play/input/" + id + "/direct"} {
			w := e.as(tok, "GET", p, "Range", "bytes=0-1")
			if w.Code != 404 || !strings.Contains(w.Body.String(), "Видео не найдено") {
				t.Errorf("%s as another profile: %d %s", p, w.Code, w.Body)
			}
			if body == "" {
				body = w.Body.String()
			} else if w.Body.String() != body {
				t.Errorf("%s: answer %q differs from %q", p, w.Body, body)
			}
		}
	}
	for _, tok := range []string{adult, admin} {
		if w := e.post(tok, "/api/play/input/"+id, `{}`); w.Code != 404 || w.Body.String() != body {
			t.Errorf("POST as another profile: %d %s", w.Code, w.Body)
		}
	}
	for _, i := range []string{id, kidInput} {
		if w := e.post(kid, "/api/play/input/"+i, `{}`); w.Code != 403 {
			t.Errorf("POST as a kid: %d %s", w.Code, w.Body)
		}
	}
	// an unknown id: the same answer
	if w := e.as(adult, "GET", "/api/play/input/"+strings.Repeat("a", 32)); w.Code != 404 || w.Body.String() != body {
		t.Errorf("unknown id: %d %s", w.Code, w.Body)
	}
	for _, i := range []string{id, kidInput} {
		for _, p := range []string{"/api/play/input/" + i, "/api/play/input/" + i + "/direct"} {
			if w := e.as(kid, "GET", p); w.Code != 403 {
				t.Errorf("%s as a kid: %d %s", p, w.Code, w.Body)
			}
		}
	}
	// a media key of another profile
	if w := e.get("GET", "/api/play/input/"+id+"/direct?mk="+url.QueryEscape(e.mediaKey(adult)), loopAddr); w.Code != 404 {
		t.Errorf("another profile's media key: %d %s", w.Code, w.Body)
	}
	if n := e.seenCount(); n != 0 {
		t.Fatalf("the addon was asked %d times", n)
	}
}

func TestInputPlay_AccessDropped404(t *testing.T) {
	e := newRelayEnv(t)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	adminID, admin := e.profile(auth.NewUser{Name: "Админ", IsAdmin: true, Password: "secret-pass-1"})
	aid := e.addFor(adminID, "/media/1", addonkit.InputRequest{})
	if w := e.as(tok, "GET", "/api/play/input/"+id); w.Code != 200 {
		t.Fatalf("before: %d %s", w.Code, w.Body)
	}
	e.dropAccess()
	for _, p := range []string{"/api/play/input/" + id, "/api/play/input/" + id + "/direct"} {
		if w := e.as(tok, "GET", p, "Range", "bytes=0-1"); w.Code != 404 || !strings.Contains(w.Body.String(), "Видео не найдено") {
			t.Errorf("%s after access dropped: %d %s", p, w.Code, w.Body)
		}
	}
	if n := e.seenCount(); n != 0 {
		t.Fatalf("the addon was asked %d times", n)
	}
	if w := e.as(admin, "GET", "/api/play/input/"+aid); w.Code != 200 {
		t.Errorf("admin's info: %d %s", w.Code, w.Body)
	}
	if w := e.as(admin, "GET", "/api/play/input/"+aid+"/direct", "Range", "bytes=0-9"); w.Code != 206 || !bytes.Equal(w.Body.Bytes(), e.data[:10]) {
		t.Errorf("admin's direct: %d %d bytes", w.Code, w.Body.Len())
	}
}

func TestInputPlay_DeadInput404(t *testing.T) {
	e := newRelayEnv(t)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	if w := e.as(tok, "GET", "/api/play/input/"+id+"/direct", "Range", "bytes=0-1"); w.Code != 206 {
		t.Fatalf("alive: %d", w.Code)
	}
	n := e.seenCount()
	// a restart: a new socket
	e.fake.mu.Lock()
	e.fake.sock["pl"] = e.sock + "2"
	e.fake.mu.Unlock()
	for _, p := range []string{"/api/play/input/" + id, "/api/play/input/" + id + "/direct"} {
		if w := e.as(tok, "GET", p, "Range", "bytes=0-1"); w.Code != 404 || !strings.Contains(w.Body.String(), "Видео не найдено") {
			t.Errorf("%s after restart: %d %s", p, w.Code, w.Body)
		}
	}
	// the process stopped
	e.fake.mu.Lock()
	e.fake.sock["pl"] = e.sock
	e.fake.status["pl"] = addons.StatusCrashed
	e.fake.mu.Unlock()
	for _, p := range []string{"/api/play/input/" + id, "/api/play/input/" + id + "/direct"} {
		if w := e.as(tok, "GET", p); w.Code != 404 {
			t.Errorf("%s after a crash: %d %s", p, w.Code, w.Body)
		}
	}
	if e.seenCount() != n {
		t.Error("the addon was asked for a dead input")
	}
}

// post asks the router with the session tok, POST with body.
func (e *relayEnv) post(tok, target, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", target, strings.NewReader(body))
	r.RemoteAddr = loopAddr
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json") // as the web client sends it
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

// probeStub answers Deps.Probe by URL, counting calls; a URL in block
// waits for its channel to close (or the probe's context to end).
type probeStub struct {
	mu      sync.Mutex
	results map[string]media.ProbeResult
	errs    map[string]error
	block   map[string]chan struct{}
	calls   map[string]int
	running int
	maxRun  int
}

func (s *probeStub) probe(ctx context.Context, in string) (media.ProbeResult, error) {
	s.mu.Lock()
	s.calls[in]++
	s.running++
	s.maxRun = max(s.maxRun, s.running)
	ch := s.block[in]
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running--
		s.mu.Unlock()
	}()
	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return media.ProbeResult{}, ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.errs[in]; err != nil {
		return media.ProbeResult{}, err
	}
	if p, ok := s.results[in]; ok {
		return p, nil
	}
	return media.ProbeResult{Container: "matroska", DurationMs: 6000000, Video: &media.VideoStream{Codec: "hevc", Width: 1920, Height: 1080, BitDepth: 8},
		Audio: []media.AudioStream{{Index: 1, Codec: "ac3", Channels: 6, Default: true}}}, nil
}

func (s *probeStub) count(in string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[in]
}

func (s *probeStub) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		n += c
	}
	return n
}

// withPlay rebuilds e's router with the probe stub and a fake HLS manager
// (opt changes the deps further).
func (e *relayEnv) withPlay(opt func(*Deps)) (*probeStub, *fakeHLS) {
	e.t.Helper()
	ps := &probeStub{results: map[string]media.ProbeResult{}, errs: map[string]error{}, block: map[string]chan struct{}{},
		calls: map[string]int{}}
	hls := &fakeHLS{dir: e.t.TempDir()}
	d := e.deps
	d.Probe, d.HLS, d.DataDir = ps.probe, hls, e.t.TempDir()
	if opt != nil {
		opt(&d)
	}
	e.deps = d
	e.h = NewRouter(d)
	return ps, hls
}

// inputURL is the relay URL ffmpeg and ffprobe read part n of input id at.
func inputURL(id string, n int) string {
	return "http://127.0.0.1:1/api/addon-input/" + id + "/" + strconv.Itoa(n) + "?k=" + relayToken
}

const h264Caps = `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]}}`

func shortProbeWait(t *testing.T, d time.Duration) {
	old := inputProbeWait
	inputProbeWait = d
	t.Cleanup(func() { inputProbeWait = old })
}

func TestInputPlay_Direct(t *testing.T) {
	e := newRelayEnv(t)
	ps, hls := e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{Name: "Film/x.mp4"})
	ps.results[inputURL(id, 0)] = media.ProbeResult{Container: "mp4", DurationMs: 5000,
		Video: &media.VideoStream{Codec: "h264", Height: 720, BitDepth: 8}, Audio: []media.AudioStream{{Index: 1, Codec: "aac", Default: true}}}
	w := e.post(tok, "/api/play/input/"+id, `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]},"startSec":12}`)
	if w.Code != 200 {
		t.Fatalf("play: %d %s", w.Code, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["mode"] != "direct" || out["url"] != "/api/play/input/"+id+"/direct" || out["durationMs"] != float64(5000) ||
		out["startSec"] != float64(12) {
		t.Errorf("answer %s", w.Body)
	}
	for _, k := range []string{"reason", "audioIndex", "audio", "subtitles", "quality"} {
		if _, ok := out[k]; !ok {
			t.Errorf("no %s: %s", k, w.Body)
		}
	}
	if strings.Contains(w.Body.String(), "k=") || strings.Contains(w.Body.String(), relayToken) || strings.Contains(w.Body.String(), "/media/1") {
		t.Errorf("answer leaks the relay URL: %s", w.Body)
	}
	if len(hls.opts) != 0 {
		t.Errorf("an HLS session for a direct play: %v", hls.opts)
	}
	if ps.count(inputURL(id, 0)) != 1 {
		t.Errorf("probes %v", ps.calls)
	}
}

func TestInputPlay_HLSThroughRelay(t *testing.T) {
	e := newRelayEnv(t)
	ps, hls := e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	w := e.post(tok, "/api/play/input/"+id, h264Caps)
	if w.Code != 200 {
		t.Fatalf("play: %d %s", w.Code, w.Body)
	}
	var out struct {
		Mode, URL   string
		HLSComplete *bool `json:"hlsComplete"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Mode == "direct" || out.URL != "/api/hls/sess1/index.m3u8" || out.HLSComplete == nil {
		t.Fatalf("answer %s", w.Body)
	}
	if len(hls.opts) != 1 || hls.opts[0].Input != inputURL(id, 0) || hls.opts[0].UserID != e.uid || hls.opts[0].FileID != 0 {
		t.Fatalf("sessions %+v", hls.opts)
	}
	if !media.IsLoopbackURL(hls.opts[0].Input) {
		t.Fatal("the relay URL is not accepted by the HLS input whitelist")
	}
	if strings.Contains(w.Body.String(), relayToken) {
		t.Errorf("answer leaks the token: %s", w.Body)
	}
	// the second play uses the kept probe
	if w := e.post(tok, "/api/play/input/"+id, h264Caps); w.Code != 200 {
		t.Fatalf("again: %d %s", w.Code, w.Body)
	}
	if n := ps.count(inputURL(id, 0)); n != 1 {
		t.Errorf("probed %d times", n)
	}
}

func TestInputPlay_PendingThenReady(t *testing.T) {
	shortProbeWait(t, 50*time.Millisecond)
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	release := make(chan struct{})
	ps.block[inputURL(id, 0)] = release
	w := e.post(tok, "/api/play/input/"+id, h264Caps)
	if w.Code != 202 || strings.TrimSpace(w.Body.String()) != `{"pending":true}` {
		t.Fatalf("while probing: %d %s", w.Code, w.Body)
	}
	// still probing: still pending, no second probe
	if w := e.post(tok, "/api/play/input/"+id, h264Caps); w.Code != 202 {
		t.Fatalf("still probing: %d %s", w.Code, w.Body)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		w = e.post(tok, "/api/play/input/"+id, h264Caps)
		if w.Code != 202 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"url":"/api/hls/sess1/index.m3u8"`) {
		t.Fatalf("after the probe: %d %s", w.Code, w.Body)
	}
	if n := ps.count(inputURL(id, 0)); n != 1 {
		t.Errorf("probed %d times", n)
	}
}

func TestInputPlay_ProbeFailRedacted(t *testing.T) {
	e := newRelayEnv(t)
	clock := e.withClock()
	ps, hls := e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	u := inputURL(id, 0)
	ps.errs[u] = errors.New("ffprobe " + u + ": exit status 1: " + u + ": Server returned 404 Not Found")
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	w := e.post(tok, "/api/play/input/"+id, h264Caps)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "Не удалось прочитать видео. Возможно, оно ещё не готово") {
		t.Fatalf("probe failed: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), relayToken) || strings.Contains(w.Body.String(), "addon-input") {
		t.Errorf("answer leaks the relay URL: %s", w.Body)
	}
	if !strings.Contains(logs.String(), "k=…") || strings.Contains(logs.String(), relayToken) {
		t.Errorf("log not redacted: %q", logs.String())
	}
	if len(hls.opts) != 0 {
		t.Error("an HLS session after a failed probe")
	}
	// a failure is remembered for inputTrackFailMemory: 502 again, no new probe
	delete(ps.errs, u)
	clock.add(inputTrackFailMemory - time.Second)
	if w := e.post(tok, "/api/play/input/"+id, h264Caps); w.Code != 502 {
		t.Fatalf("within the memory: %d %s", w.Code, w.Body)
	}
	if n := ps.count(u); n != 1 {
		t.Errorf("probed %d times within the memory", n)
	}
	// then the next play probes again
	clock.add(2 * time.Second)
	if w := e.post(tok, "/api/play/input/"+id, h264Caps); w.Code != 200 {
		t.Fatalf("after the memory: %d %s", w.Code, w.Body)
	}
	if n := ps.count(u); n != 2 {
		t.Errorf("probed %d times", n)
	}
	// English
	id2 := e.addFor(e.uid, "/media/2", addonkit.InputRequest{})
	ps.errs[inputURL(id2, 0)] = errors.New("ffprobe " + inputURL(id2, 0) + ": boom")
	r := httptest.NewRequest("POST", "/api/play/input/"+id2, strings.NewReader(h264Caps))
	r.RemoteAddr = loopAddr
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Accept-Language", "en")
	r.Header.Set("Content-Type", "application/json")
	ew := httptest.NewRecorder()
	e.h.ServeHTTP(ew, r)
	if ew.Code != 502 || !strings.Contains(ew.Body.String(), "Couldn’t read the video. It may not be ready yet") {
		t.Errorf("English: %d %s", ew.Code, ew.Body)
	}
}

func TestInputPlay_NoLoopback503BeforeProbe(t *testing.T) {
	for _, c := range []struct{ name, base, token string }{{"no base", "", relayToken}, {"no token", "http://127.0.0.1:1", ""}} {
		t.Run(c.name, func(t *testing.T) {
			e := newRelayEnv(t)
			ps, hls := e.withPlay(func(d *Deps) { d.InputBase, d.InputToken = c.base, c.token })
			tok := e.session(e.uid)
			id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
			w := e.post(tok, "/api/play/input/"+id, h264Caps)
			if w.Code != 503 || !strings.Contains(w.Body.String(), "Перекодирование недоступно") {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if n := ps.total(); n != 0 {
				t.Errorf("probed %d times", n)
			}
			if len(hls.opts) != 0 || e.seenCount() != 0 {
				t.Error("played without the relay")
			}
		})
	}
}

func TestInputPlay_ProbeSlots(t *testing.T) {
	shortProbeWait(t, 20*time.Millisecond)
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	release := make(chan struct{})
	var ids []string
	for i := 0; i < inputProbeSlots+1; i++ {
		id := e.addFor(e.uid, "/media/"+strconv.Itoa(i), addonkit.InputRequest{})
		ids = append(ids, id)
		ps.block[inputURL(id, 0)] = release
	}
	for _, id := range ids {
		if w := e.post(tok, "/api/play/input/"+id, h264Caps); w.Code != 202 {
			t.Fatalf("%s: %d %s", id, w.Code, w.Body)
		}
	}
	running := func() int {
		ps.mu.Lock()
		defer ps.mu.Unlock()
		return ps.running
	}
	deadline := time.Now().Add(5 * time.Second)
	for running() < inputProbeSlots && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // a fifth would have started by now
	if n := running(); n != inputProbeSlots {
		t.Fatalf("%d probes running, want %d", n, inputProbeSlots)
	}
	close(release)
	// the fifth gets its slot and every input plays
	for _, id := range ids {
		deadline := time.Now().Add(5 * time.Second)
		for {
			w := e.post(tok, "/api/play/input/"+id, h264Caps)
			if w.Code == 200 {
				break
			}
			if w.Code != 202 || time.Now().After(deadline) {
				t.Fatalf("%s: %d %s", id, w.Code, w.Body)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.maxRun > inputProbeSlots {
		t.Errorf("%d probes ran at once", ps.maxRun)
	}
	for _, id := range ids {
		if ps.calls[inputURL(id, 0)] != 1 {
			t.Errorf("%s probed %d times", id, ps.calls[inputURL(id, 0)])
		}
	}
}

func TestInputPlay_KeyframeErrRedacted(t *testing.T) {
	e := newRelayEnv(t)
	_, hls := e.withPlay(func(d *Deps) {
		d.Keyframe = func(_ context.Context, in string, _ float64) (float64, error) {
			return 0, errors.New("ffprobe " + in + ": exit status 1")
		}
	})
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	// hevc for a client that plays hevc in mp4: a remux, the video copied
	w := e.post(tok, "/api/play/input/"+id, `{"caps":{"videoCodecs":["h264","hevc"],"audioCodecs":["aac","ac3"],"containers":["mp4"]},"startSec":30}`)
	if w.Code != 200 || len(hls.started) != 1 || !hls.started[0].VideoCopy {
		t.Fatalf("play: %d %s %+v", w.Code, w.Body, hls.started)
	}
	if !strings.Contains(logs.String(), "keyframe") || strings.Contains(logs.String(), relayToken) {
		t.Errorf("keyframe log not redacted: %q", logs.String())
	}
}

// A probe slower than inputProbeWait that then fails: the next poll
// answers 502 instead of starting another probe (and another 202).
func TestInputPlay_SlowProbeFailureAnswered(t *testing.T) {
	shortProbeWait(t, 20*time.Millisecond)
	e := newRelayEnv(t)
	e.withClock()
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	u := inputURL(id, 0)
	release := make(chan struct{})
	ps.block[u] = release
	if w := e.post(tok, "/api/play/input/"+id, h264Caps); w.Code != 202 {
		t.Fatalf("while probing: %d %s", w.Code, w.Body)
	}
	ps.mu.Lock()
	ps.errs[u] = errors.New("ffprobe " + u + ": end of file")
	ps.mu.Unlock()
	close(release)
	// polled until the job has stored its failure (202 meanwhile)
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := e.post(tok, "/api/play/input/"+id, h264Caps)
		if w.Code == 502 {
			break
		}
		if w.Code != 202 || time.Now().After(deadline) {
			t.Fatalf("waiting for the failure: %d %s", w.Code, w.Body)
		}
		time.Sleep(5 * time.Millisecond)
	}
	for range 3 {
		if w := e.post(tok, "/api/play/input/"+id, h264Caps); w.Code != 502 || !strings.Contains(w.Body.String(), "Не удалось прочитать видео") {
			t.Fatalf("after the failure: %d %s", w.Code, w.Body)
		}
	}
	if n := ps.count(u); n != 1 {
		t.Errorf("probed %d times", n)
	}
}

// startHLS's internal error is logged with the relay token hidden.
func TestInputPlay_HLSStartErrRedacted(t *testing.T) {
	e := newRelayEnv(t)
	_, hls := e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	hls.err = errors.New("ffmpeg " + inputURL(id, 0) + ": exit status 1")
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	w := e.post(tok, "/api/play/input/"+id, h264Caps)
	if w.Code != 500 || strings.Contains(w.Body.String(), relayToken) {
		t.Fatalf("hls start failed: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(logs.String(), "hls start") || !strings.Contains(logs.String(), "k=…") || strings.Contains(logs.String(), relayToken) {
		t.Errorf("hls start log not redacted: %q", logs.String())
	}
}

// A main probe whose job ends while it waits for a slot is no failure:
// the next ask probes it instead of answering 502.
func TestInputPlay_SlotWaitNotAFailure(t *testing.T) {
	shortProbeWait(t, 20*time.Millisecond)
	e := newRelayEnv(t)
	e.withClock()
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	release := make(chan struct{})
	e.holdProbeSlots(ps, tok, release) // under the full bound
	old := inputProbeTimeout
	inputProbeTimeout = 60 * time.Millisecond // the next job's bound
	t.Cleanup(func() { inputProbeTimeout = old })
	id := e.addFor(e.uid, "/media/w", addonkit.InputRequest{})
	if w := e.post(tok, "/api/play/input/"+id, h264Caps); w.Code != 202 {
		t.Fatalf("waiting for a slot: %d %s", w.Code, w.Body)
	}
	time.Sleep(200 * time.Millisecond) // its job's bound passes while it waits
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := e.post(tok, "/api/play/input/"+id, h264Caps)
		if w.Code == 200 {
			break
		}
		if w.Code != 202 || time.Now().After(deadline) {
			t.Fatalf("after the slots freed: %d %s", w.Code, w.Body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := ps.count(inputURL(id, 0)); n != 1 {
		t.Errorf("probed %d times", n)
	}
}
