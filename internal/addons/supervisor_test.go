package addons

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	fakeOnce sync.Once
	fakeBin  string
	fakeErr  error
)

// fakeAddon builds testdata/fakeaddon once per test run.
func fakeAddon(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakeaddon-bin")
		if err != nil {
			fakeErr = err
			return
		}
		fakeBin = filepath.Join(dir, "fakeaddon")
		// The fake is stdlib-only; the suite's GOFLAGS (a relative -modfile)
		// would not resolve from the package directory.
		cmd := exec.Command("go", "build", "-o", fakeBin, "./testdata/fakeaddon")
		cmd.Env = append(os.Environ(), "GOFLAGS=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			fakeErr = fmt.Errorf("build fakeaddon: %v\n%s", err, out)
		}
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	return fakeBin
}

func TestMain(m *testing.M) {
	code := m.Run()
	if fakeBin != "" {
		os.RemoveAll(filepath.Dir(fakeBin))
	}
	os.Exit(code)
}

type testSup struct {
	*Supervisor
	mints atomic.Int32
}

func newSup(t *testing.T, mode string) *testSup {
	t.Helper()
	run, err := os.MkdirTemp("/tmp", "ad")
	if err != nil {
		t.Fatal(err)
	}
	ts := &testSup{}
	ts.Supervisor = &Supervisor{
		Dir: t.TempDir(), RunDir: run, CoreURL: "http://127.0.0.1:8420", Locale: "en",
		Log:      log.New(io.Discard, "", 0),
		extraEnv: []string{"FAKE_MODE=" + mode},
	}
	ts.Mint = func(id string) (string, error) {
		return fmt.Sprintf("tok-%s-%d", id, ts.mints.Add(1)), nil
	}
	t.Cleanup(func() {
		ts.StopAll()
		os.RemoveAll(run)
	})
	return ts
}

// install puts the fake addon at Binary(id, version). A non-empty mode
// overrides the supervisor's FAKE_MODE for this version only.
func install(t *testing.T, s *testSup, id, version, mode string) Installed {
	t.Helper()
	bin := s.Binary(id, version)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if mode == "" {
		if err := os.Symlink(fakeAddon(t), bin); err != nil {
			t.Fatal(err)
		}
	} else {
		script := fmt.Sprintf("#!/bin/sh\nFAKE_MODE=%s exec %s --path \"$0\"\n", mode, fakeAddon(t))
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Installed{ID: id, Version: version, Enabled: true,
		Manifest: Manifest{ID: id, Version: version, Contract: ContractVersion}}
}

func sockClient(sock string) *http.Client {
	return &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
}

func get(t *testing.T, sock, path string) (int, []byte) {
	t.Helper()
	resp, err := sockClient(sock).Get("http://addon" + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// alive reports whether any process has pattern in its command line.
func alive(pattern string) bool {
	return exec.Command("pgrep", "-f", pattern).Run() == nil
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStart_Healthy(t *testing.T) {
	s := newSup(t, "ok")
	in := install(t, s, "fakeaddon", "1.0.0", "")
	if got := s.Status("fakeaddon"); got != StatusStopped {
		t.Fatalf("status before start = %q", got)
	}
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if got := s.Status("fakeaddon"); got != StatusRunning {
		t.Fatalf("status = %q", got)
	}
	sock := s.Socket("fakeaddon")
	if want := filepath.Join(s.RunDir, "fakeaddon."); !strings.HasPrefix(sock, want) || !strings.HasSuffix(sock, ".sock") {
		t.Fatalf("socket = %q", sock)
	}
	if code, _ := get(t, sock, "/health"); code != 200 {
		t.Fatalf("health = %d", code)
	}
	if n := s.mints.Load(); n != 1 {
		t.Fatalf("Mint called %d times", n)
	}
	if fi, err := os.Stat(s.DataDir("fakeaddon")); err != nil || !fi.IsDir() {
		t.Fatalf("data dir: %v", err)
	}
}

func TestStart_NeverHealthy_TimesOut(t *testing.T) {
	s := newSup(t, "never")
	s.Health = 500 * time.Millisecond
	in := install(t, s, "fakeaddon", "1.0.0", "")
	start := time.Now()
	if err := s.Start(context.Background(), in); err == nil {
		t.Fatal("started")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v", d)
	}
	if alive(s.Dir) {
		t.Fatal("process still running")
	}
	if s.Socket("fakeaddon") != "" {
		t.Fatal("socket reported for a failed start")
	}
	if left, _ := filepath.Glob(filepath.Join(s.RunDir, "*.sock")); len(left) != 0 {
		t.Fatalf("socket files left: %v", left)
	}
	if got := s.Status("fakeaddon"); got != StatusCrashed {
		t.Fatalf("status = %q", got)
	}
	// A failed first start is not restarted.
	time.Sleep(1500 * time.Millisecond)
	if n := s.mints.Load(); n != 1 {
		t.Fatalf("restarted after a failed first start: %d mints", n)
	}
}

func TestStart_CrashOnFirstStart_NoLoop(t *testing.T) {
	s := newSup(t, "crash")
	s.backoffUnit = 50 * time.Millisecond
	in := install(t, s, "fakeaddon", "1.0.0", "")
	if err := s.Start(context.Background(), in); err == nil {
		t.Fatal("started")
	}
	time.Sleep(500 * time.Millisecond)
	if n := s.mints.Load(); n != 1 {
		t.Fatalf("restarted: %d mints", n)
	}
	if !strings.Contains(strings.Join(s.LogTail("fakeaddon", 0), "\n"), "fake: crashing") {
		t.Fatalf("stderr not captured: %q", s.LogTail("fakeaddon", 0))
	}
}

func TestStart_Incompatible(t *testing.T) {
	s := newSup(t, "ok")
	in := install(t, s, "fakeaddon", "1.0.0", "")
	in.Manifest.Contract = ContractVersion + 1
	if err := s.Start(context.Background(), in); err == nil {
		t.Fatal("started")
	}
	if got := s.Status("fakeaddon"); got != StatusIncompatible {
		t.Fatalf("status = %q", got)
	}
	if s.mints.Load() != 0 || alive(s.Dir) {
		t.Fatal("incompatible addon was launched")
	}
}

func TestCrash_Restarts(t *testing.T) {
	s := newSup(t, "flaky")
	s.backoffUnit = 200 * time.Millisecond
	in := install(t, s, "fakeaddon", "1.0.0", "")
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	first := s.Socket("fakeaddon")
	sawCrashed, sawRecovered := false, false
	end := time.Now().Add(4 * time.Second)
	for time.Now().Before(end) && !(sawRecovered && s.restartCount("fakeaddon") >= 2) {
		switch s.Status("fakeaddon") {
		case StatusCrashed:
			sawCrashed = true
		case StatusRunning:
			if sawCrashed {
				sawRecovered = true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sawCrashed || !sawRecovered {
		t.Fatalf("crashed=%v recovered=%v", sawCrashed, sawRecovered)
	}
	if n := s.restartCount("fakeaddon"); n < 2 {
		t.Fatalf("restarts = %d", n)
	}
	if int(s.mints.Load()) < 3 {
		t.Fatalf("token not minted per restart: %d", s.mints.Load())
	}
	if sock := s.Socket("fakeaddon"); sock == first && sock != "" {
		t.Fatal("restart reused the socket path")
	}
}

func TestNextBackoff(t *testing.T) {
	unit := time.Second
	var d time.Duration
	var got []time.Duration
	for i := 0; i < 9; i++ {
		d = nextBackoff(d, time.Second, unit)
		got = append(got, d)
	}
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("backoff %d = %v, want %vs (all %v)", i, got[i], want[i], got)
		}
	}
	if d := nextBackoff(60*time.Second, 5*time.Minute, unit); d != time.Second {
		t.Fatalf("after 5 min running = %v", d)
	}
}

func TestStop_Graceful(t *testing.T) {
	s := newSup(t, "ok")
	in := install(t, s, "fakeaddon", "1.0.0", "")
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	sock := s.Socket("fakeaddon")
	start := time.Now()
	if err := s.Stop("fakeaddon"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("stop took %v", d)
	}
	if got := s.Status("fakeaddon"); got != StatusStopped {
		t.Fatalf("status = %q", got)
	}
	if s.Socket("fakeaddon") != "" {
		t.Fatal("socket after stop")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket file left: %v", err)
	}
	if alive(s.Dir) {
		t.Fatal("process alive after stop")
	}
	waitFor(t, time.Second, "term line", func() bool {
		return strings.Contains(strings.Join(s.LogTail("fakeaddon", 0), "\n"), "fake: term")
	})
	// Stopped addons are not restarted.
	time.Sleep(300 * time.Millisecond)
	if s.mints.Load() != 1 {
		t.Fatal("restarted after stop")
	}
}

// TestCrash_KillsGrandchildren: an addon that crashes takes the rest of
// its process group with it.
func TestCrash_KillsGrandchildren(t *testing.T) {
	s := newSup(t, "orphan")
	s.backoffUnit = time.Minute // no restart within the test
	in := install(t, s, "fakeaddon", "1.0.0", "")
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	marker := "grandchild " + s.DataDir("fakeaddon")
	waitFor(t, 2*time.Second, "grandchild", func() bool { return alive(marker) })
	waitFor(t, 4*time.Second, "crash", func() bool { return s.Status("fakeaddon") == StatusCrashed })
	waitFor(t, 2*time.Second, "grandchild gone", func() bool { return !alive(marker) })
}

// TestStop_KillsWhatIgnoresSIGTERM: Stop sends SIGKILL after the grace.
func TestStop_KillsWhatIgnoresSIGTERM(t *testing.T) {
	s := newSup(t, "stubborn")
	s.stopGrace = 300 * time.Millisecond
	var logs strings.Builder
	var mu sync.Mutex
	s.Log = log.New(writerFunc(func(b []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return logs.Write(b) }), "", 0)
	in := install(t, s, "fakeaddon", "1.0.0", "")
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	s.Stop("fakeaddon")
	if d := time.Since(start); d < 300*time.Millisecond || d > 3*time.Second {
		t.Fatalf("stop took %v", d)
	}
	if alive(s.Dir) {
		t.Fatal("process alive after stop")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(logs.String(), "ignored SIGTERM") {
		t.Fatalf("no SIGKILL: %s", logs.String())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

func TestStart_UpdateKeepsOldUntilHealthy(t *testing.T) {
	s := newSup(t, "ok")
	s.Health = 500 * time.Millisecond
	v1 := install(t, s, "fakeaddon", "1.0.0", "")
	bad := install(t, s, "fakeaddon", "2.0.0", "never")
	good := install(t, s, "fakeaddon", "3.0.0", "ok")
	if err := s.Start(context.Background(), v1); err != nil {
		t.Fatal(err)
	}
	old := s.Socket("fakeaddon")
	if err := s.Start(context.Background(), bad); err == nil {
		t.Fatal("unhealthy update started")
	}
	if s.Socket("fakeaddon") != old || s.Status("fakeaddon") != StatusRunning {
		t.Fatal("failed update replaced the running version")
	}
	if code, _ := get(t, old, "/health"); code != 200 {
		t.Fatal("old version no longer serving")
	}
	if alive(filepath.Join(s.Dir, "fakeaddon", "2.0.0")) {
		t.Fatal("failed version still running")
	}
	if err := s.Start(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	if s.Socket("fakeaddon") == old {
		t.Fatal("socket not switched")
	}
	if alive(filepath.Join(s.Dir, "fakeaddon", "1.0.0")) {
		t.Fatal("old version still running after a healthy update")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old socket left")
	}
	if s.Status("fakeaddon") != StatusRunning {
		t.Fatalf("status = %q", s.Status("fakeaddon"))
	}
}

func TestStopAll_Parallel(t *testing.T) {
	s := newSup(t, "ok")
	for _, id := range []string{"fakeaddon-a", "fakeaddon-b"} {
		if err := s.Start(context.Background(), install(t, s, id, "1.0.0", "")); err != nil {
			t.Fatal(err)
		}
	}
	s.StopAll()
	for _, id := range []string{"fakeaddon-a", "fakeaddon-b"} {
		if s.Status(id) != StatusStopped {
			t.Fatalf("%s: %q", id, s.Status(id))
		}
	}
	if alive(s.Dir) {
		t.Fatal("processes left")
	}
	if err := s.Start(context.Background(), install(t, s, "fakeaddon-c", "1.0.0", "")); err == nil {
		t.Fatal("started after StopAll")
	}
}

func TestEnv_NoVaultLeak(t *testing.T) {
	t.Setenv("VAULT_TMDB_KEY", "secret")
	t.Setenv("ANTRESOL_ADDON_TOKEN", "spoofed")
	s := newSup(t, "ok")
	in := install(t, s, "fakeaddon", "1.0.0", "")
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	sock := s.Socket("fakeaddon")
	_, body := get(t, sock, "/env")
	var env []string
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "VAULT_") {
			t.Errorf("leaked %s", k)
		}
		if _, dup := got[k]; dup && strings.HasPrefix(k, "ANTRESOL_") {
			t.Errorf("duplicate %s", k)
		}
		got[k] = v
	}
	want := map[string]string{
		"ANTRESOL_ADDON_ID":       "fakeaddon",
		"ANTRESOL_ADDON_DATA_DIR": s.DataDir("fakeaddon"),
		"ANTRESOL_ADDON_SOCKET":   sock,
		"ANTRESOL_CORE_URL":       "http://127.0.0.1:8420",
		"ANTRESOL_ADDON_TOKEN":    "tok-fakeaddon-1",
		"ANTRESOL_CONTRACT":       "1",
		"ANTRESOL_LOCALE":         "en",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if got["PATH"] == "" {
		t.Error("PATH not inherited")
	}
}

func TestLogTail_Ring(t *testing.T) {
	s := newSup(t, "noise")
	in := install(t, s, "fakeaddon", "1.0.0", "")
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "noise lines", func() bool {
		l := s.LogTail("fakeaddon", 0)
		return len(l) > 0 && l[len(l)-1] == "fake: up"
	})
	lines := s.LogTail("fakeaddon", 0)
	if len(lines) != 500 {
		t.Fatalf("len = %d", len(lines))
	}
	if lines[0] != "line 101" || lines[498] != "line 599" {
		t.Fatalf("first %q, before last %q", lines[0], lines[498])
	}
	if tail := s.LogTail("fakeaddon", 2); len(tail) != 2 || tail[0] != "line 599" || tail[1] != "fake: up" {
		t.Fatalf("tail 2 = %q", tail)
	}
	if s.LogTail("nope", 10) != nil {
		t.Fatal("log for unknown id")
	}
}

func TestDefaultRunDir(t *testing.T) {
	short := &Supervisor{Dir: "/srv/data/addons"}
	if got := short.runDir("hello", 1); got != "/srv/data/addons/run" {
		t.Fatalf("short = %q", got)
	}
	long := &Supervisor{Dir: "/" + strings.Repeat("x", 90)}
	got := long.runDir("hello", 1)
	if !strings.HasPrefix(got, os.TempDir()) || !strings.Contains(filepath.Base(got), "antresol-") {
		t.Fatalf("long = %q", got)
	}
	if fixed := (&Supervisor{Dir: long.Dir, RunDir: "/tmp/r"}).runDir("hello", 1); fixed != "/tmp/r" {
		t.Fatalf("explicit = %q", fixed)
	}
}

func TestPaths(t *testing.T) {
	s := &Supervisor{Dir: "/d"}
	if s.Binary("a", "1.0.0") != "/d/a/1.0.0/bin/addon" || s.WebDir("a", "1.0.0") != "/d/a/1.0.0/web" || s.DataDir("a") != "/d/a/data" {
		t.Fatal(s.Binary("a", "1.0.0"), s.WebDir("a", "1.0.0"), s.DataDir("a"))
	}
}

func TestEnvPrefix(t *testing.T) {
	for id, want := range map[string]string{"media-tools": "ANTRESOL_MEDIA_TOOLS_", "my-addon": "ANTRESOL_MY_ADDON_"} {
		if got := EnvPrefix(id); got != want {
			t.Errorf("EnvPrefix(%q) = %q, want %q", id, got, want)
		}
	}
}

// TestEnv_OwnPrefixOnly: the addon gets the server's ANTRESOL_<ID>_*
// variables and nothing else of VAULT_*/ANTRESOL_*; the contract's own
// variables are never taken from the server env.
func TestEnv_OwnPrefixOnly(t *testing.T) {
	t.Setenv("ANTRESOL_FAKE_X", "1")
	t.Setenv("ANTRESOL_OTHER_Y", "2")
	t.Setenv("VAULT_TMDB_KEY", "k")
	t.Setenv("ANTRESOL_FAKE_ADDON_TOKEN", "evil")
	t.Setenv("ANTRESOL_ADDON_TOKEN", "evil")
	s := newSup(t, "ok")
	in := install(t, s, "fake", "1.0.0", "")
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, s.Socket("fake"), "/env")
	var env []string
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = append(got[k], v)
	}
	if v := got["ANTRESOL_FAKE_X"]; len(v) != 1 || v[0] != "1" {
		t.Errorf("ANTRESOL_FAKE_X = %q", v)
	}
	for _, k := range []string{"ANTRESOL_OTHER_Y", "VAULT_TMDB_KEY"} {
		if v, ok := got[k]; ok {
			t.Errorf("leaked %s=%q", k, v)
		}
	}
	if v := got["ANTRESOL_ADDON_TOKEN"]; len(v) != 1 || v[0] != "tok-fake-1" {
		t.Errorf("ANTRESOL_ADDON_TOKEN = %q, want the minted token once", v)
	}
	// its own variable, not the token: it never replaces the contract's
	if v := got["ANTRESOL_FAKE_ADDON_TOKEN"]; len(v) != 1 || v[0] != "evil" {
		t.Errorf("ANTRESOL_FAKE_ADDON_TOKEN = %q", v)
	}
	for k, v := range got {
		if strings.HasPrefix(k, "ANTRESOL_") && len(v) > 1 {
			t.Errorf("duplicate %s: %q", k, v)
		}
	}
}

// TestStart_NotReadyThenReady: an addon answering /health with
// {"ready": false} is started but stays «starting» until it reports ready.
func TestStart_NotReadyThenReady(t *testing.T) {
	s := newSup(t, "slowready")
	s.readyPoll = 100 * time.Millisecond
	in := install(t, s, "fakeaddon", "1.0.0", "")
	begin := time.Now()
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(begin); d > 800*time.Millisecond {
		t.Fatalf("Start waited for readiness: %v", d)
	}
	if got := s.Status("fakeaddon"); got != StatusStarting {
		t.Fatalf("status before ready = %q", got)
	}
	if s.Socket("fakeaddon") == "" {
		t.Fatal("no socket while starting")
	}
	// the proxy's view: not serving yet
	if st, sock := s.Serving("fakeaddon"); st != StatusStarting || sock != "" {
		t.Fatalf("Serving while starting = %q, %q", st, sock)
	}
	waitFor(t, 5*time.Second, "running", func() bool { return s.Status("fakeaddon") == StatusRunning })
	if st, sock := s.Serving("fakeaddon"); st != StatusRunning || sock != s.Socket("fakeaddon") || sock == "" {
		t.Fatalf("Serving when running = %q, %q", st, sock)
	}
	if st, sock := s.Serving("nope"); st != StatusStopped || sock != "" {
		t.Fatalf("Serving of an unknown addon = %q, %q", st, sock)
	}
	if d := time.Since(begin); d < time.Second {
		t.Fatalf("running before the addon was ready: %v", d)
	}
}

func TestReadyPollDefault(t *testing.T) {
	if got := (&Supervisor{}).pollEvery(); got != 2*time.Second {
		t.Fatalf("ready poll = %v", got)
	}
}

func TestHealthReady(t *testing.T) {
	for body, want := range map[string]bool{
		"ok": true, `{"ok":true}`: true, `{"ok":true,"ready":true}`: true, `{"ok":true,"ready":false}`: false,
		`true`: true, ``: true, `{"ready":"no"}`: true,
	} {
		if got := healthReady([]byte(body)); got != want {
			t.Errorf("healthReady(%q) = %v", body, got)
		}
	}
}

// OnStatus hears every status change, in order: start, crash, restart,
// stop; a call that changes nothing is not reported.
func TestOnStatus_EveryChange(t *testing.T) {
	s := newSup(t, "flaky")
	s.backoffUnit = 200 * time.Millisecond
	var mu sync.Mutex
	var seen []string
	s.OnStatus(func(id string, st Status) {
		mu.Lock()
		seen = append(seen, id+" "+string(st))
		mu.Unlock()
	})
	got := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
	has := func(st Status) bool {
		for _, x := range got() {
			if x == "fakeaddon "+string(st) {
				return true
			}
		}
		return false
	}
	in := install(t, s, "fakeaddon", "1.0.0", "")
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if g := got(); len(g) < 2 || g[0] != "fakeaddon starting" || g[1] != "fakeaddon running" {
		t.Fatalf("start: %v", g)
	}
	waitFor(t, 4*time.Second, "crash and restart", func() bool { return has(StatusCrashed) && s.restartCount("fakeaddon") >= 1 })
	s.Stop("fakeaddon")
	g := got()
	if g[len(g)-1] != "fakeaddon stopped" {
		t.Fatalf("stop: %v", g)
	}
	for i := 1; i < len(g); i++ {
		if g[i] == g[i-1] {
			t.Fatalf("unchanged status reported: %v", g)
		}
	}
	n := len(g)
	s.Stop("fakeaddon")
	if len(got()) != n {
		t.Fatalf("a second stop reported: %v", got())
	}
	// an incompatible release is a status change too
	bad := in
	bad.Manifest.Contract = ContractVersion + 1
	if err := s.Start(context.Background(), bad); err == nil {
		t.Fatal("incompatible started")
	}
	if g := got(); g[len(g)-1] != "fakeaddon incompatible" {
		t.Fatalf("incompatible: %v", g)
	}
}

// OnStatus listeners add up: each hears every change, in the order added.
func TestOnStatus_SeveralListeners(t *testing.T) {
	s := newSup(t, "listeners")
	var mu sync.Mutex
	var seen []string
	for _, tag := range []string{"a", "b"} {
		s.OnStatus(func(id string, st Status) {
			mu.Lock()
			seen = append(seen, tag+" "+string(st))
			mu.Unlock()
		})
	}
	in := install(t, s, "fakeaddon", "1.0.0", "")
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	s.Stop("fakeaddon")
	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 6 || seen[0] != "a starting" || seen[1] != "b starting" || seen[2] != "a running" || seen[3] != "b running" ||
		seen[len(seen)-2] != "a stopped" || seen[len(seen)-1] != "b stopped" {
		t.Fatalf("seen %v", seen)
	}
}
