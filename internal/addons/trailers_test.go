package addons

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/trailers"
)

// sockTarget is one running addon "tr" declaring trailers.fetch, served by
// h on a socket of its own.
type sockTarget struct {
	mu   sync.Mutex
	sock string
	in   Installed
}

func (s *sockTarget) Get(context.Context, string) (Installed, error) { return s.in, nil }
func (s *sockTarget) List(context.Context) ([]Installed, error)      { return []Installed{s.in}, nil }
func (s *sockTarget) Serving(string) (Status, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StatusRunning, s.sock
}

func newTrailerHooks(t *testing.T, h http.HandlerFunc) (*Trailers, *Hooks, *sockTarget) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "th")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close(); os.RemoveAll(dir) })
	tg := &sockTarget{sock: sock, in: Installed{ID: "tr", Version: "1.0.0", Enabled: true,
		Manifest: Manifest{ID: "tr", Version: "1.0.0", Contract: ContractVersion, Hooks: []string{TrailersHook}}}}
	hk := &Hooks{T: tg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return &Trailers{H: hk, Log: hk.Log}, hk, tg
}

// answering answers trailers.fetch with code and body.
func answering(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

func TestRetryAfterClamp(t *testing.T) {
	for ms, want := range map[int64]time.Duration{
		0: trailerPollDefault, -5: trailerPollDefault, 1: trailerPollMin, 249: trailerPollMin, 700: 700 * time.Millisecond,
		2000: trailerPollMax, 2001: trailerPollMax, math.MaxInt64: trailerPollMax, math.MaxInt64 / 1000: trailerPollMax,
	} {
		if got := retryAfter(ms); got != want {
			t.Errorf("retryAfter(%d) = %v, want %v", ms, got, want)
		}
	}
	// through the hook: a huge retryAfterMs waits the most, never negative
	tr, _, _ := newTrailerHooks(t, answering(202, `{"retryAfterMs":9223372036854775807}`))
	at, d, err := tr.ask(context.Background(), "tr", addonkit.TrailerFetch{Key: "k", URL: "u"})
	if err != nil || d != trailerPollMax || at.file.Path != "" {
		t.Fatalf("%+v %v %v", at, d, err)
	}
}

func TestAskBadAnswersAreFailures(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"202 empty":    answering(202, ""),
		"202 not json": answering(202, "pending"),
		"200 empty":    answering(200, ""),
		"409 no text":  answering(409, `{"error":"  "}`),
		"409 no json":  answering(409, ""),
		"302":          func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) },
		"500":          answering(500, `{"error":"boom"}`),
	} {
		t.Run(name, func(t *testing.T) {
			tr, hk, _ := newTrailerHooks(t, h)
			if _, err := hk.Ask(context.Background(), "tr", TrailersHook, nil, nil); !errors.Is(err, ErrHookFailed) {
				t.Fatalf("Ask: %v", err)
			}
			if _, err := tr.Play(context.Background(), "k", "u"); !errors.Is(err, trailers.ErrFetch) {
				t.Fatalf("Play: %v", err)
			}
			if hk.isDown(downKey("tr", TrailersHook)) {
				t.Fatal("a failure counted the addon as down")
			}
		})
	}
	// a 409 with a text is a refusal, cut and cleaned
	tr, _, _ := newTrailerHooks(t, answering(409, `{"error":"Нет\u0007 видео"}`))
	_, err := tr.Play(context.Background(), "k", "u")
	var ref *trailers.Refusal
	if !errors.As(err, &ref) || ref.Message != "Нет видео" {
		t.Fatalf("refusal: %v", err)
	}
	// 204 and 404: not this addon, without a body
	for _, code := range []int{204, 404} {
		_, hk, tg := newTrailerHooks(t, answering(code, ""))
		a, err := hk.Ask(context.Background(), "tr", TrailersHook, nil, nil)
		if err != nil || a.Status != code || a.Body != nil || a.Socket != tg.sock {
			t.Fatalf("%d: %+v %v", code, a, err)
		}
	}
}

func TestPlayCancelledDuringTheHold(t *testing.T) {
	var asks atomic.Int32
	tr, hk, _ := newTrailerHooks(t, func(w http.ResponseWriter, r *http.Request) {
		asks.Add(1)
		answering(202, `{"retryAfterMs":250}`)(w, r)
	})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(400*time.Millisecond, cancel)
	_, err := tr.Play(ctx, "k", "u")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if asks.Load() < 2 {
		t.Fatalf("asked %d times", asks.Load())
	}
	if hk.isDown(downKey("tr", TrailersHook)) {
		t.Fatal("a caller going away counted the addon as down")
	}
	// the hold running out is ErrPending, and does not count it down either
	dctx, dcancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer dcancel()
	if _, err := tr.Play(dctx, "k", "u"); !errors.Is(err, trailers.ErrPending) {
		t.Fatalf("hold: %v", err)
	}
	if hk.isDown(downKey("tr", TrailersHook)) {
		t.Fatal("the hold counted the addon as down")
	}
}

func TestKeepBindsTheSocketThatAnswered(t *testing.T) {
	var tg *sockTarget
	tr, _, tg := newTrailerHooks(t, func(w http.ResponseWriter, r *http.Request) {
		// the addon restarts while it answers: the 200 belongs to this socket
		tg.mu.Lock()
		tg.sock = "/tmp/a-new-process.sock"
		tg.mu.Unlock()
		answering(200, `{"path":"/media/k.mp4","size":10,"durationMs":1,"contentType":"video/mp4"}`)(w, r)
	})
	old := tg.sock
	if _, err := tr.Play(context.Background(), "k", "u"); err != nil {
		t.Fatal(err)
	}
	tr.mu.Lock()
	at := tr.files["k"]
	tr.mu.Unlock()
	if at.socket != old {
		t.Fatalf("kept %q, the answer came from %q", at.socket, old)
	}
	// and kept() drops it, since that process is gone
	if _, ok := tr.kept("k"); ok {
		t.Fatal("kept a file of a gone process")
	}
}

func TestForgetKeepsANewerAnswer(t *testing.T) {
	tr := &Trailers{H: &Hooks{T: &sockTarget{}}}
	a := trailerAt{addon: "tr", socket: "/s1", file: addonkit.TrailerFile{Path: "/media/a.mp4"}}
	b := trailerAt{addon: "tr", socket: "/s2", file: addonkit.TrailerFile{Path: "/media/a.mp4"}}
	tr.keep("k", b)
	tr.forget("k", a) // a stale read's 404
	if got := tr.files["k"]; got != b {
		t.Fatalf("forgot the newer answer: %+v", got)
	}
	tr.forget("k", b)
	if _, ok := tr.files["k"]; ok {
		t.Fatal("not forgotten")
	}
}

func TestPrefetchRetriedAfterAFailure(t *testing.T) {
	var code atomic.Int32
	code.Store(500)
	var asks atomic.Int32
	tr, _, _ := newTrailerHooks(t, func(w http.ResponseWriter, r *http.Request) {
		asks.Add(1)
		answering(int(code.Load()), `{"retryAfterMs":1000}`)(w, r)
	})
	tr.Prefetch("k", "u")
	tr.Wait()
	// the failure does not hold the key back for a day
	code.Store(202)
	tr.Prefetch("k", "u")
	tr.Wait()
	if asks.Load() != 2 {
		t.Fatalf("asked %d times", asks.Load())
	}
	// a 202 does: once a day
	tr.Prefetch("k", "u")
	tr.Wait()
	if asks.Load() != 2 {
		t.Fatalf("asked %d times after a 202", asks.Load())
	}
}
