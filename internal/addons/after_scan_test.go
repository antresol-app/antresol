package addons

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// afterScanHooks: the hooks of tg's addons with a log the test reads.
func afterScanHooks(t *testing.T) (*Hooks, *hookTarget, *syncBuf) {
	t.Helper()
	h, tg := newHooks(t)
	buf := &syncBuf{}
	h.Log = slog.New(slog.NewTextHandler(buf, nil))
	return h, tg, buf
}

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

// A scan hands the addons library.afterScan without waiting for them: an
// addon that takes 5 s to answer does not hold the scan's result back, and
// every scan sends one.
func TestAfterScanHook_CalledAsyncAfterScan(t *testing.T) {
	h, tg, _ := afterScanHooks(t)
	hookAddon(t, tg, "seed", `{"library.afterScan": {"delayMs": 5000, "status": 204}}`, []string{"library.afterScan"}, true)
	jobs := &library.Jobs{Scanner: &library.Scanner{Repo: library.NewRepo(openTestDB(t))}, AfterScan: h.AfterScan}

	t0 := time.Now()
	jobs.RunOnce(context.Background())
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("the scan waited %v for the hook", d)
	}
	if st := jobs.Status(); st.Running || st.FinishedAt.IsZero() {
		t.Fatalf("the scan's result is not in: %+v", st)
	}
	waitFor(t, 3*time.Second, "the hook call", func() bool { return len(calls(t, tg, "seed")) == 1 })
	c := calls(t, tg, "seed")[0]
	if c.Name != "library.afterScan" || c.Header != "library.afterScan" || c.Type != "application/json" || string(c.Body) != "{}" || c.User != "" {
		t.Fatalf("call %+v (body %s)", c, c.Body)
	}

	// the next scan sends its own, though the last call is still running
	jobs.RunOnce(context.Background())
	waitFor(t, 3*time.Second, "the second call", func() bool { return len(calls(t, tg, "seed")) == 2 })
}

// An addon that is down, stopped or does not declare the hook is skipped:
// the others are still told, and nothing is logged for the down one.
func TestAfterScanHook_DownSkipped(t *testing.T) {
	h, tg, log := afterScanHooks(t)
	hook := []string{"library.afterScan"}
	hookAddon(t, tg, "up", `{"library.afterScan": {"status": 204}}`, hook, true)
	hookAddon(t, tg, "down", `{"library.afterScan": {"status": 204}}`, hook, false)
	hookAddon(t, tg, "undeclared", `{"library.afterScan": {"status": 204}}`, nil, true)
	hookAddon(t, tg, "broken", `{"library.afterScan": {"status": 500}}`, hook, true)
	hookAddon(t, tg, "zz", `{"library.afterScan": {"body": {"ok": true}}}`, hook, true)

	h.afterScan(context.Background()) // the call AfterScan makes on its own goroutine

	for _, id := range []string{"up", "broken", "zz"} {
		if n := len(calls(t, tg, id)); n != 1 {
			t.Errorf("%s: %d calls, want 1", id, n)
		}
	}
	if n := len(calls(t, tg, "undeclared")); n != 0 {
		t.Errorf("an addon without the hook was called %d times", n)
	}
	if out := log.String(); strings.Contains(out, "addon=down") {
		t.Errorf("the down addon was logged: %s", out)
	}
	if out := log.String(); !strings.Contains(out, "addon=broken") {
		t.Errorf("the failing addon was not logged: %s", out)
	}

	// nobody declares it: nothing happens, however it is called
	h2, tg2, _ := afterScanHooks(t)
	hookAddon(t, tg2, "quiet", `{"disk.usage": {"status": 204}}`, []string{"disk.usage"}, true)
	h2.AfterScan(context.Background())
	h2.afterScan(context.Background())
	if n := len(calls(t, tg2, "quiet")); n != 0 {
		t.Errorf("%d calls of an addon that does not declare it", n)
	}
}

// The hook's own deadline is 60 s.
func TestAfterScanHook_Deadline(t *testing.T) {
	if got := HookTimeout("library.afterScan"); got != 60*time.Second {
		t.Fatalf("HookTimeout = %v", got)
	}
	if !knownHook("library.afterScan") {
		t.Fatal("library.afterScan is not a known hook")
	}
}
