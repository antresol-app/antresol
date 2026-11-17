package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http/httptest"
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
)

// withHooks makes "pl" declare play.progress (unless declare is false) and
// rebuilds e's router with a hook caller over the fake addons.
func (e *relayEnv) withHooks(declare bool) {
	e.t.Helper()
	e.fake.mu.Lock()
	pl := e.fake.installed["pl"]
	if declare {
		pl.Manifest.Hooks = []string{"play.progress"}
	}
	e.fake.installed["pl"] = pl
	e.fake.mu.Unlock()
	e.deps.AddonHooks = &addons.Hooks{T: e.fake}
	e.h = NewRouter(e.deps)
}

// hookCalls are the play.progress calls the fake addon got, in order.
func (e *relayEnv) hookCalls() []addonkit.PlayProgress {
	e.t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []addonkit.PlayProgress
	for _, s := range e.seen {
		if s.uri != "/hooks/play.progress" {
			continue
		}
		var p addonkit.PlayProgress
		if err := json.Unmarshal(s.body, &p); err != nil {
			e.t.Fatalf("hook body %q: %v", s.body, err)
		}
		out = append(out, p)
	}
	return out
}

// waitHooks waits until the fake addon got n play.progress calls.
func (e *relayEnv) waitHooks(n int) []addonkit.PlayProgress {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := e.hookCalls()
		if len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// putProgress saves a position of input id as the session tok.
func (e *relayEnv) putProgress(tok, id, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("PUT", "/api/play/input/"+id+"/progress", strings.NewReader(body))
	r.RemoteAddr = loopAddr
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func progBody(pos, dur int64, keepalive bool) string {
	return `{"positionMs":` + strconv.FormatInt(pos, 10) + `,"durationMs":` + strconv.FormatInt(dur, 10) +
		`,"keepalive":` + strconv.FormatBool(keepalive) + `}`
}

// movie is a library movie the profiles may see.
func (e *relayEnv) movie() int64 {
	e.t.Helper()
	fp := filepath.Join(e.t.TempDir(), "Брат.mkv")
	if err := os.WriteFile(fp, []byte("x"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	item, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "Брат", Year: 1997,
		File: library.FileInput{Path: fp, Size: 1}})
	if err != nil {
		e.t.Fatal(err)
	}
	return item
}

func TestInputProgress_SavesCoreProgress(t *testing.T) {
	e := newRelayEnv(t)
	e.withHooks(true)
	tok := e.session(e.uid)
	item := e.movie()
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{ItemID: item})
	if w := e.putProgress(tok, id, progBody(42500, 6000000, false)); w.Code != 204 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	w := e.as(tok, "GET", "/api/progress/"+strconv.FormatInt(item, 10))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"positionMs":42500`) || !strings.Contains(w.Body.String(), `"durationMs":6000000`) {
		t.Fatalf("core progress: %d %s", w.Code, w.Body)
	}
	got := e.waitHooks(1)
	if len(got) != 1 || got[0] != (addonkit.PlayProgress{InputID: id, UserID: e.uid, PositionSec: 42.5, DurationSec: 6000}) {
		t.Fatalf("hook %+v", got)
	}
	// as the profile
	e.mu.Lock()
	var hdr string
	for _, s := range e.seen {
		if s.uri == "/hooks/play.progress" {
			hdr = s.header.Get("X-Antresol-User")
		}
	}
	e.mu.Unlock()
	if !strings.Contains(hdr, `"id":`+strconv.FormatInt(e.uid, 10)) || !strings.Contains(hdr, `"uid":"`) {
		t.Errorf("X-Antresol-User %q", hdr)
	}
}

func TestInputProgress_NoItem(t *testing.T) {
	e := newRelayEnv(t)
	e.withHooks(true)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	if w := e.putProgress(tok, id, progBody(1000, 2000, false)); w.Code != 204 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if got := e.waitHooks(1); len(got) != 1 || got[0].PositionSec != 1 || got[0].DurationSec != 2 {
		t.Fatalf("hook %+v", got)
	}
	var n int
	if err := e.deps.DB.QueryRow(`SELECT COUNT(*) FROM progress`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("core progress rows: %d %v", n, err)
	}
}

// An addon that does not declare play.progress is never called.
func TestInputProgress_Undeclared(t *testing.T) {
	e := newRelayEnv(t)
	e.withHooks(false)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	var logs syncBuf
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	if w := e.putProgress(tok, id, progBody(1000, 2000, true)); w.Code != 204 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	time.Sleep(100 * time.Millisecond)
	if got := e.hookCalls(); len(got) != 0 {
		t.Fatalf("called: %+v", got)
	}
	if strings.Contains(logs.String(), "play.progress") {
		t.Errorf("logged: %q", logs.String())
	}
}

func TestInputProgress_Throttle(t *testing.T) {
	e := newRelayEnv(t)
	clock := e.withClock()
	e.withHooks(true)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	save := func(pos int64, keepalive bool, want int) {
		t.Helper()
		if w := e.putProgress(tok, id, progBody(pos, 600000, keepalive)); w.Code != 204 {
			t.Fatalf("save at %d: %d %s", pos, w.Code, w.Body)
		}
		if got := e.waitHooks(want); len(got) != want {
			t.Fatalf("after %d: %d calls, want %d", pos, len(got), want)
		}
	}
	save(0, false, 1) // 0 s
	clock.add(10 * time.Second)
	save(10000, false, 1)
	clock.add(6 * time.Second) // 16 s
	save(16000, false, 2)
	clock.add(time.Second) // 17 s: the page goes away
	save(17000, true, 3)
	clock.add(time.Second)
	save(600000, false, 4) // the end
	time.Sleep(100 * time.Millisecond)
	got := e.hookCalls()
	if len(got) != 4 {
		t.Fatalf("%d calls: %+v", len(got), got)
	}
	for i, want := range []float64{0, 16, 17, 600} {
		if got[i].PositionSec != want {
			t.Errorf("call %d at %v, want %v", i, got[i].PositionSec, want)
		}
	}
	if got[2].Ended || !got[3].Ended {
		t.Errorf("ended flags %+v", got)
	}
	// another input has its own clock
	id2 := e.addFor(e.uid, "/media/2", addonkit.InputRequest{})
	if w := e.putProgress(tok, id2, progBody(1000, 600000, false)); w.Code != 204 {
		t.Fatalf("other input: %d", w.Code)
	}
	if got := e.waitHooks(5); len(got) != 5 || got[4].InputID != id2 {
		t.Fatalf("other input not called: %+v", got)
	}
}

// The save never waits for the addon.
func TestInputProgress_NotBlocking(t *testing.T) {
	e := newRelayEnv(t)
	e.withHooks(true)
	gate := make(chan struct{})
	e.mu.Lock()
	e.hookGate = gate
	e.mu.Unlock()
	t.Cleanup(func() { close(gate) })
	tok := e.session(e.uid)
	item := e.movie()
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{ItemID: item})
	start := time.Now()
	if w := e.putProgress(tok, id, progBody(5000, 600000, false)); w.Code != 204 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("the save took %v", d)
	}
	if got := e.waitHooks(1); len(got) != 1 {
		t.Fatal("the hook was not called")
	}
	if w := e.putProgress(tok, id, progBody(6000, 600000, true)); w.Code != 204 || time.Since(start) > time.Second {
		t.Fatalf("second save: %d after %v", w.Code, time.Since(start))
	}
}

func TestInputProgress_Bad(t *testing.T) {
	e := newRelayEnv(t)
	e.withHooks(true)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{ItemID: e.movie()})
	for _, b := range []string{
		`{"positionMs":-1,"durationMs":1000}`,
		`{"positionMs":0,"durationMs":-1}`,
		progBody(1000+5001, 1000, false),
	} {
		if w := e.putProgress(tok, id, b); w.Code != 400 || !strings.Contains(w.Body.String(), "Неверная позиция") {
			t.Errorf("%s: %d %s", b, w.Code, w.Body)
		}
	}
	if w := e.putProgress(tok, id, `{"positionMs":`); w.Code != 400 {
		t.Errorf("broken JSON: %d", w.Code)
	}
	// up to 5 s past the duration is accepted
	if w := e.putProgress(tok, id, progBody(1000+5000, 1000, false)); w.Code != 204 {
		t.Errorf("at the bound: %d %s", w.Code, w.Body)
	}
	time.Sleep(50 * time.Millisecond)
	if got := e.hookCalls(); len(got) != 1 {
		t.Errorf("bad saves reached the addon: %+v", got)
	}
}

func TestInputProgress_OtherProfile404(t *testing.T) {
	e := newRelayEnv(t)
	e.withHooks(true)
	item := e.movie()
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{ItemID: item})
	_, other := e.profile(auth.NewUser{Name: "Петя", IsAdmin: true, Password: "secret-pass-1"})
	if w := e.putProgress(other, id, progBody(1000, 2000, false)); w.Code != 404 || !strings.Contains(w.Body.String(), msgAddonNoInput) {
		t.Fatalf("another profile: %d %s", w.Code, w.Body)
	}
	_, kid := e.profile(auth.NewUser{Name: "Ваня", IsKid: true})
	if w := e.putProgress(kid, id, progBody(1000, 2000, false)); w.Code != 403 {
		t.Fatalf("kid: %d %s", w.Code, w.Body)
	}
	// a non-admin once the addon is no longer for adults
	uid2, tok2 := e.profile(auth.NewUser{Name: "Оля"})
	id2 := e.addFor(uid2, "/media/2", addonkit.InputRequest{})
	e.dropAccess()
	if w := e.putProgress(tok2, id2, progBody(1000, 2000, false)); w.Code != 404 {
		t.Fatalf("access dropped: %d %s", w.Code, w.Body)
	}
	if w := e.putProgress("bogus", id, progBody(1000, 2000, false)); w.Code != 401 {
		t.Fatalf("no session: %d", w.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if got := e.hookCalls(); len(got) != 0 {
		t.Fatalf("hook called: %+v", got)
	}
	if w := e.as(e.session(e.uid), "GET", "/api/progress/"+strconv.FormatInt(item, 10)); !strings.Contains(w.Body.String(), `"positionMs":0`) {
		t.Fatalf("progress written: %s", w.Body)
	}
}

// A hook that fails is logged once per input, not on every save.
func TestInputProgress_FailureLoggedOnce(t *testing.T) {
	e := newRelayEnv(t)
	clock := e.withClock()
	e.withHooks(true)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	var logs syncBuf
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	e.srv.Close() // the addon's socket answers no more; the input stays registered
	for i := range 3 {
		if w := e.putProgress(tok, id, progBody(int64(i)*1000, 600000, true)); w.Code != 204 && w.Code != 404 {
			t.Fatalf("save %d: %d %s", i, w.Code, w.Body)
		}
		clock.add(time.Second)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "play.progress") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := strings.Count(logs.String(), "api: addon pl input"); n != 1 {
		t.Fatalf("logged %d times: %q", n, logs.String())
	}
}

// syncBuf is a log output safe for the hook's goroutines.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Keepalive and end saves are bounded: one hook call in flight per input,
// a 1 s floor, and the end is still delivered.
func TestInputProgress_KeepaliveBounded(t *testing.T) {
	e := newRelayEnv(t)
	clock := e.withClock()
	e.withHooks(true)
	gate := make(chan struct{})
	e.mu.Lock()
	e.hookGate = gate
	e.mu.Unlock()
	released := false
	release := func() {
		if !released {
			released = true
			close(gate)
		}
	}
	t.Cleanup(release)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	put := func(pos int64, keepalive bool) {
		t.Helper()
		if w := e.putProgress(tok, id, progBody(pos, 600000, keepalive)); w.Code != 204 {
			t.Fatalf("save at %d: %d", pos, w.Code)
		}
	}
	put(1000, true)
	if got := e.waitHooks(1); len(got) != 1 {
		t.Fatalf("first call missing: %d", len(got))
	}
	for i := 0; i < 20; i++ { // rapid keepalives while the first is held
		clock.add(2 * time.Second)
		put(int64(2000+i), true)
	}
	put(600000, false) // the end
	time.Sleep(100 * time.Millisecond)
	if got := e.hookCalls(); len(got) != 1 {
		t.Fatalf("%d calls in flight, want 1", len(got))
	}
	release()
	got := e.waitHooks(2)
	time.Sleep(100 * time.Millisecond)
	got = e.hookCalls()
	if len(got) != 2 || !got[1].Ended {
		t.Fatalf("want the held call then the end, got %+v", got)
	}
}

// Without a call in flight, keepalives are held to the 1 s floor.
func TestInputProgress_KeepaliveFloor(t *testing.T) {
	e := newRelayEnv(t)
	clock := e.withClock()
	e.withHooks(true)
	tok := e.session(e.uid)
	id := e.addFor(e.uid, "/media/1", addonkit.InputRequest{})
	for i := 0; i < 10; i++ {
		if w := e.putProgress(tok, id, progBody(int64(i), 600000, true)); w.Code != 204 {
			t.Fatal(w.Code)
		}
		clock.add(100 * time.Millisecond)
	}
	e.waitHooks(1)
	time.Sleep(100 * time.Millisecond)
	if got := e.hookCalls(); len(got) != 1 {
		t.Fatalf("%d calls within 1 s, want 1", len(got))
	}
}
