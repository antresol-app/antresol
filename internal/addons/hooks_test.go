package addons

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/mediaimport/runkit"
)

// hookTarget serves Installed from a map and Serving from a supervisor.
type hookTarget struct {
	sup *testSup
	ins map[string]Installed
}

func (h *hookTarget) Get(_ context.Context, id string) (Installed, error) {
	in, ok := h.ins[id]
	if !ok {
		return Installed{}, ErrNotFound
	}
	return in, nil
}

func (h *hookTarget) List(context.Context) ([]Installed, error) {
	var out []Installed
	for _, in := range h.ins {
		out = append(out, in)
	}
	return out, nil
}

func (h *hookTarget) Serving(id string) (Status, string) { return h.sup.Serving(id) }

// stubTarget reports one addon with a fixed status and no process.
type stubTarget struct {
	in     Installed
	status Status
}

func (s stubTarget) Get(context.Context, string) (Installed, error) { return s.in, nil }
func (s stubTarget) List(context.Context) ([]Installed, error)      { return []Installed{s.in}, nil }
func (s stubTarget) Serving(string) (Status, string)                { return s.status, "" }

// hookAddon installs the fake in hooks mode with script, declaring hooks,
// and starts it unless start is false.
func hookAddon(t *testing.T, tg *hookTarget, id, script string, hooks []string, start bool) {
	t.Helper()
	bin := tg.sup.Binary(id, "1.0.0")
	if err := os.MkdirAll(bin[:strings.LastIndex(bin, "/")], 0o755); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "'") {
		t.Fatal("script must not contain single quotes")
	}
	sh := fmt.Sprintf("#!/bin/sh\nFAKE_MODE=hooks ANTRESOL_FAKE_HOOKS='%s' exec %s --path \"$0\"\n", script, fakeAddon(t))
	if err := os.WriteFile(bin, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	in := Installed{ID: id, Version: "1.0.0", Enabled: true,
		Manifest: Manifest{ID: id, Version: "1.0.0", Contract: ContractVersion, Hooks: hooks}}
	tg.ins[id] = in
	if start {
		if err := tg.sup.Start(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
}

func newHooks(t *testing.T) (*Hooks, *hookTarget) {
	t.Helper()
	tg := &hookTarget{sup: newSup(t, "hooks"), ins: map[string]Installed{}}
	return &Hooks{T: tg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, tg
}

func calls(t *testing.T, tg *hookTarget, id string) []hookCallRec {
	t.Helper()
	_, sock := tg.sup.Serving(id)
	code, b := get(t, sock, "/calls")
	if code != 200 {
		t.Fatalf("/calls = %d", code)
	}
	var out []hookCallRec
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

type hookCallRec struct {
	Name, Header, Type, User string
	Signed                   bool // the addon's addonkit.UserFrom took User
	Body                     json.RawMessage
}

func TestCall_OK(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "fake", `{"files.plan": {"body": {"follow": ["a"], "stop": ["b"]}}}`, []string{"files.plan"}, true)
	var out struct{ Follow, Stop []string }
	u := &addonkit.User{ID: 3, Name: "Ёжик", IsAdmin: true, Lang: "ru"}
	if err := h.Call(context.Background(), "fake", "files.plan", u, map[string]string{"from": "/x", "to": "/y"}, &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Follow, []string{"a"}) || !slices.Equal(out.Stop, []string{"b"}) {
		t.Fatalf("out = %+v", out)
	}
	cs := calls(t, tg, "fake")
	if len(cs) != 1 {
		t.Fatalf("calls = %+v", cs)
	}
	c := cs[0]
	if c.Name != "files.plan" || c.Header != "files.plan" || c.Type != "application/json" {
		t.Fatalf("call = %+v", c)
	}
	var body map[string]string
	if json.Unmarshal(c.Body, &body) != nil || body["from"] != "/x" || body["to"] != "/y" {
		t.Fatalf("body = %s", c.Body)
	}
	for _, r := range c.User {
		if r >= 0x80 {
			t.Fatalf("non-ASCII user header %q", c.User)
		}
	}
	var gotU addonkit.User
	if err := json.Unmarshal([]byte(c.User), &gotU); err != nil || !reflect.DeepEqual(gotU, *u) {
		t.Fatalf("user header %q → %+v (%v)", c.User, gotU, err)
	}
	if !c.Signed {
		t.Fatal("the addon does not take the user header: its signature does not verify with the key the addon was started with")
	}

	// no user → no header; nil out discards the answer
	if err := h.Call(context.Background(), "fake", "files.plan", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	cs = calls(t, tg, "fake")
	if len(cs) != 2 || cs[1].User != "" || string(cs[1].Body) != "{}" {
		t.Fatalf("second call = %+v", cs)
	}
}

func TestCall_NotDeclared(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "fake", `{"files.stop": {"status": 204}}`, []string{"files.plan"}, true)
	if err := h.Call(context.Background(), "fake", "files.stop", nil, nil, nil); !errors.Is(err, ErrHookDown) {
		t.Fatalf("err = %v", err)
	}
	if err := h.Call(context.Background(), "nope", "files.stop", nil, nil, nil); !errors.Is(err, ErrHookDown) {
		t.Fatalf("unknown addon: err = %v", err)
	}
	if cs := calls(t, tg, "fake"); len(cs) != 0 {
		t.Fatalf("undeclared hook reached the addon: %+v", cs)
	}
}

func TestCall_Stopped(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "fake", `{"files.plan": {"status": 204}}`, []string{"files.plan"}, false)
	if err := h.Call(context.Background(), "fake", "files.plan", nil, nil, nil); !errors.Is(err, ErrHookDown) {
		t.Fatalf("stopped: err = %v", err)
	}
	in := Installed{ID: "s", Manifest: Manifest{ID: "s", Hooks: []string{"files.plan"}}, Enabled: true}
	for _, st := range []Status{StatusStarting, StatusCrashed, StatusIncompatible} {
		h2 := &Hooks{T: stubTarget{in: in, status: st}}
		if err := h2.Call(context.Background(), "s", "files.plan", nil, nil, nil); !errors.Is(err, ErrHookDown) {
			t.Fatalf("%s: err = %v", st, err)
		}
	}
	// running but no socket (raced a stop) is down, too
	h3 := &Hooks{T: stubTarget{in: in, status: StatusRunning}}
	if err := h3.Call(context.Background(), "s", "files.plan", nil, nil, nil); !errors.Is(err, ErrHookDown) {
		t.Fatalf("running without socket: err = %v", err)
	}
}

func TestCall_204And404(t *testing.T) {
	h, tg := newHooks(t)
	var logs bytes.Buffer
	h.Log = slog.New(slog.NewTextHandler(&logs, nil))
	hookAddon(t, tg, "fake", `{"files.pause": {"status": 204}}`, []string{"files.pause", "files.stop", "disk.usage"}, true)
	out := map[string]any{"keep": true}
	if err := h.Call(context.Background(), "fake", "files.pause", nil, map[string][]string{"keys": {"a", "b"}}, &out); err != nil {
		t.Fatalf("204: %v", err)
	}
	if out["keep"] != true {
		t.Fatalf("204 touched out: %v", out)
	}
	for i := 0; i < 2; i++ { // disk.usage is not scripted → 404
		if err := h.Call(context.Background(), "fake", "disk.usage", nil, nil, nil); err != nil {
			t.Fatalf("404: %v", err)
		}
	}
	// a declared files.* hook never takes 404 for a yes
	if err := h.Call(context.Background(), "fake", "files.stop", nil, nil, nil); !errors.Is(err, ErrHookFailed) {
		t.Fatalf("files.stop 404: %v", err)
	}
	if n := strings.Count(logs.String(), "not implemented"); n != 1 {
		t.Fatalf("404 logged %d times:\n%s", n, logs.String())
	}
}

func TestCall_Failed(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "fake", `{"files.plan": {"status": 500}, "files.stop": {"raw": "not json"}, "files.pause": {"status": 409, "body": {}}}`,
		[]string{"files.plan", "files.stop", "files.pause"}, true)
	var out struct{ Follow []string }
	for _, name := range []string{"files.plan", "files.stop", "files.pause"} {
		if err := h.Call(context.Background(), "fake", name, nil, nil, &out); !errors.Is(err, ErrHookFailed) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestCall_Refusal(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "fake", `{"files.pause": {"status": 409, "body": {"error": "Файлы заняты"}}}`, []string{"files.pause"}, true)
	err := h.Call(context.Background(), "fake", "files.pause", nil, nil, nil)
	var ref *HookRefusal
	if !errors.As(err, &ref) {
		t.Fatalf("err = %v", err)
	}
	if ref.Addon != "fake" || ref.Message != "Файлы заняты" || err.Error() != "Файлы заняты" {
		t.Fatalf("refusal = %+v (%q)", ref, err.Error())
	}
	if errors.Is(err, ErrHookDown) || errors.Is(err, ErrHookFailed) {
		t.Fatalf("refusal counts as down: %v", err)
	}
}

func TestCall_Timeout(t *testing.T) {
	h, tg := newHooks(t)
	h.timeouts = map[string]time.Duration{"files.plan": 200 * time.Millisecond}
	hookAddon(t, tg, "fake", `{"files.plan": {"delayMs": 5000, "status": 204}}`, []string{"files.plan"}, true)
	t0 := time.Now()
	err := h.Call(context.Background(), "fake", "files.plan", nil, nil, nil)
	if !errors.Is(err, ErrHookTimeout) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(t0); d > time.Second {
		t.Fatalf("took %v", d)
	}
	// a shorter caller deadline wins over the hook's own
	h.timeouts = nil
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	t0 = time.Now()
	if err := h.Call(ctx, "fake", "files.plan", nil, nil, nil); !errors.Is(err, ErrHookTimeout) {
		t.Fatalf("ctx deadline: err = %v", err)
	}
	if d := time.Since(t0); d > time.Second {
		t.Fatalf("ctx deadline took %v", d)
	}
}

func TestHookTimeout(t *testing.T) {
	for name, want := range map[string]time.Duration{
		"files.plan": 10 * time.Second, "files.pause": 30 * time.Second, "library.pending": 2 * time.Second,
		"tracking.scan:pinned": 120 * time.Second, "tracking.watched": 3 * time.Second, "import.search": 30 * time.Second,
		"requests.open": 2 * time.Second, "requests.title": 10 * time.Second, "requests.search": 10 * time.Second,
	} {
		if got := HookTimeout(name); got != want {
			t.Errorf("HookTimeout(%q) = %v, want %v", name, got, want)
		}
	}
	// import.public is no hook any more (its link import is the addon's own)
	if _, ok := hookTimeouts["import.public"]; ok {
		t.Error("import.public still has a timeout")
	}
	// files.* (but plan) take the operation's deadline when it has one
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if d := hookDeadline(ctx, "files.stop", nil); d < 80*time.Second {
		t.Fatalf("files.stop under a 90 s operation = %v", d)
	}
	if d := hookDeadline(ctx, "files.plan", nil); d > 10*time.Second {
		t.Fatalf("files.plan under a 90 s operation = %v", d)
	}
}

func TestEach_OrderAndSkip(t *testing.T) {
	h, tg := newHooks(t)
	h.timeouts = map[string]time.Duration{"disk.usage": 300 * time.Millisecond}
	hookAddon(t, tg, "zed", `{"disk.usage": {"body": {"n": "zed"}}}`, []string{"disk.usage"}, true)
	hookAddon(t, tg, "alpha", `{"disk.usage": {"body": {"n": "alpha"}}}`, []string{"disk.usage"}, true)
	hookAddon(t, tg, "mid", `{"disk.usage": {"body": {"n": "mid"}}}`, []string{"disk.usage"}, true)
	hookAddon(t, tg, "slow1", `{"disk.usage": {"delayMs": 5000, "body": {}}}`, []string{"disk.usage"}, true)
	hookAddon(t, tg, "slow2", `{"disk.usage": {"delayMs": 5000, "body": {}}}`, []string{"disk.usage"}, true)
	hookAddon(t, tg, "empty", `{"disk.usage": {"status": 204}}`, []string{"disk.usage"}, true)
	hookAddon(t, tg, "broken", `{"disk.usage": {"status": 500}}`, []string{"disk.usage"}, true)
	hookAddon(t, tg, "undecl", `{"disk.usage": {"body": {"n": "undecl"}}}`, nil, true)
	hookAddon(t, tg, "down", `{"disk.usage": {"body": {"n": "down"}}}`, []string{"disk.usage"}, false)
	hookAddon(t, tg, "off", `{"disk.usage": {"body": {"n": "off"}}}`, []string{"disk.usage"}, true)
	off := tg.ins["off"]
	off.Enabled = false
	tg.ins["off"] = off

	var got []string
	t0 := time.Now()
	h.Each(context.Background(), "disk.usage", &addonkit.User{ID: 1}, map[string]any{}, func(id string, raw json.RawMessage) {
		var b struct{ N string }
		if err := json.Unmarshal(raw, &b); err != nil || b.N != id {
			t.Errorf("%s: raw %s", id, raw)
		}
		got = append(got, id)
	})
	if d := time.Since(t0); d > time.Second {
		t.Fatalf("Each took %v: slow addons were not run under one deadline", d)
	}
	if want := []string{"alpha", "mid", "zed"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if cs := calls(t, tg, "undecl"); len(cs) != 0 {
		t.Fatalf("undeclared addon was called: %+v", cs)
	}
	if cs := calls(t, tg, "off"); len(cs) != 0 {
		t.Fatalf("disabled addon was called: %+v", cs)
	}
	if cs := calls(t, tg, "alpha"); len(cs) != 1 || cs[0].User == "" {
		t.Fatalf("alpha calls = %+v", cs)
	}
}

func TestCall_NoRedirects(t *testing.T) {
	h, tg := newHooks(t)
	// followed, the redirect would turn into GET /calls: a 200 JSON answer
	hookAddon(t, tg, "fake", `{"files.plan": {"status": 302, "location": "/calls"}}`, []string{"files.plan"}, true)
	var out any
	if err := h.Call(context.Background(), "fake", "files.plan", nil, nil, &out); !errors.Is(err, ErrHookFailed) {
		t.Fatalf("err = %v (out %v)", err, out)
	}
	if out != nil {
		t.Fatalf("redirect was followed: %v", out)
	}
}

func TestCall_RefusalTrimmed(t *testing.T) {
	h, tg := newHooks(t)
	long := strings.Repeat("я", 600)
	hookAddon(t, tg, "fake", `{"files.pause": {"status": 409, "body": {"error": "Нельзя\u0007\n\u001b[31mсейчас`+long+`"}}}`, []string{"files.pause"}, true)
	err := h.Call(context.Background(), "fake", "files.pause", nil, nil, nil)
	var ref *HookRefusal
	if !errors.As(err, &ref) {
		t.Fatalf("err = %v", err)
	}
	if n := len([]rune(ref.Message)); n != 500 {
		t.Fatalf("message is %d runes", n)
	}
	if !strings.HasPrefix(ref.Message, "Нельзя[31mсейчас") {
		t.Fatalf("control characters kept: %q", ref.Message[:40])
	}
}

// A hung addon (accepts, never answers) costs one deadline per hook, not
// one per call: after a timeout that hook fails at once for the down
// period, and Each leaves it out without waiting.
func TestCall_HungAddonSkippedFast(t *testing.T) {
	h, tg := newHooks(t)
	h.timeouts = map[string]time.Duration{"notify.interest": 200 * time.Millisecond, "disk.usage": 200 * time.Millisecond}
	hookAddon(t, tg, "hung", `{"notify.interest": {"delayMs": 5000, "body": {}}, "disk.usage": {"delayMs": 5000, "body": {}}}`,
		[]string{"notify.interest", "disk.usage"}, true)
	hookAddon(t, tg, "ok", `{"disk.usage": {"body": {"n": "ok"}}}`, []string{"disk.usage"}, true)
	if err := h.Call(context.Background(), "hung", "notify.interest", nil, nil, nil); !errors.Is(err, ErrHookTimeout) {
		t.Fatalf("first call: err = %v", err)
	}
	// disk.usage is down by itself: the first Each waits for it once
	var got []string
	h.Each(context.Background(), "disk.usage", nil, nil, func(id string, _ json.RawMessage) { got = append(got, id) })
	if !slices.Equal(got, []string{"ok"}) {
		t.Fatalf("first Each = %v", got)
	}
	t0 := time.Now()
	for range 5 {
		if err := h.Call(context.Background(), "hung", "notify.interest", nil, nil, nil); !errors.Is(err, ErrHookDown) {
			t.Fatalf("down period: err = %v", err)
		}
	}
	got = nil
	h.Each(context.Background(), "disk.usage", nil, nil, func(id string, _ json.RawMessage) { got = append(got, id) })
	if d := time.Since(t0); d > 100*time.Millisecond {
		t.Fatalf("calls in the down period took %v", d)
	}
	if !slices.Equal(got, []string{"ok"}) {
		t.Fatalf("Each = %v", got)
	}
	if n := len(calls(t, tg, "hung")); n != 2 {
		t.Fatalf("hung addon got %d calls, want 2 (one per hook)", n)
	}
}

// The down period ends: after it the addon is called again.
func TestCall_HungAddonRecovers(t *testing.T) {
	h, tg := newHooks(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	h.timeouts = map[string]time.Duration{"notify.interest": 100 * time.Millisecond}
	hookAddon(t, tg, "slow", `{"notify.interest": {"delayMs": 400, "body": {"ids": [1]}}}`, []string{"notify.interest"}, true)
	if err := h.Call(context.Background(), "slow", "notify.interest", nil, nil, nil); !errors.Is(err, ErrHookTimeout) {
		t.Fatalf("first call: err = %v", err)
	}
	h.timeouts = nil // 5 s: the addon answers in time now
	now = now.Add(59 * time.Second)
	if err := h.Call(context.Background(), "slow", "notify.interest", nil, nil, nil); !errors.Is(err, ErrHookDown) {
		t.Fatalf("at 59 s: err = %v", err)
	}
	now = now.Add(2 * time.Second)
	var out struct{ IDs []int }
	if err := h.Call(context.Background(), "slow", "notify.interest", nil, nil, &out); err != nil || !slices.Equal(out.IDs, []int{1}) {
		t.Fatalf("after the window: err = %v, out = %+v", err, out)
	}
}

// A caller's own shorter deadline is not the addon's fault: no down period.
func TestCall_CallerDeadlineNoDownPeriod(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "slow", `{"notify.interest": {"delayMs": 400, "body": {}}}`, []string{"notify.interest"}, true)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := h.Call(ctx, "slow", "notify.interest", nil, nil, nil); !errors.Is(err, ErrHookTimeout) {
		t.Fatalf("err = %v", err)
	}
	if err := h.Call(context.Background(), "slow", "notify.interest", nil, nil, nil); err != nil {
		t.Fatalf("after a caller timeout: err = %v", err)
	}
}

// leftoverHooks are the hooks the leftovers fakes declare.
var leftoverHooks = []string{"migrate.leftovers"}

func leftoverReasons(lines []runkit.Line) []string {
	var out []string
	for _, l := range lines {
		out = append(out, l.Source+": "+l.Reason)
	}
	return out
}

// The addons' leftover lines, in addon-id order, each a "leftover" line;
// asked for the household in the screen's language.
func TestLeftovers_FromAddon(t *testing.T) {
	h, tg := newHooks(t)
	left := h.MigrateLeftovers()
	if got := left(context.Background()); len(got) != 0 {
		t.Fatalf("no addons: %+v", got)
	}
	hookAddon(t, tg, "zeta", `{"migrate.leftovers": {"body": {"lines": [
		{"source": "gamma", "kind": "download", "name": "Правила", "status": "warning", "reason": "Не переносится: правила (2)"}]}}}`,
		leftoverHooks, true)
	hookAddon(t, tg, "alpha", `{"migrate.leftovers": {"body": {"lines": [
		{"source": "books", "name": "Books", "status": "warning", "reason": "Не переносится: книги (3)"},
		{"source": "asks", "name": "Asks", "status": "failed", "reason": "Не удалось проверить Asks"},
		{"source": "odd", "name": "Odd", "status": "planned", "reason": "o"}]}}}`,
		leftoverHooks, true)
	hookAddon(t, tg, "nothing", `{"migrate.leftovers": {"status": 204}}`, leftoverHooks, true)
	ctx := i18n.WithLanguage(context.Background(), i18n.EN)
	got := left(ctx)
	want := []string{"books: Не переносится: книги (3)", "asks: Не удалось проверить Asks", "odd: o", "gamma: Не переносится: правила (2)"}
	if !slices.Equal(leftoverReasons(got), want) {
		t.Fatalf("lines %v", leftoverReasons(got))
	}
	for _, l := range got {
		if l.Kind != "leftover" {
			t.Fatalf("kind %+v", l)
		}
	}
	if got[0].Name != "Books" || got[0].Status != runkit.StatusWarning || got[1].Status != runkit.StatusFailed ||
		got[2].Status != runkit.StatusWarning {
		t.Fatalf("lines %+v", got)
	}
	cs := calls(t, tg, "alpha")
	if len(cs) != 1 || cs[0].Name != "migrate.leftovers" || cs[0].User != "" || string(cs[0].Body) != `{"lang":"en"}` {
		t.Fatalf("calls %+v", cs)
	}
}

// An answer while an addon is down (stopped, failing, slow or garbled) is
// not kept: the next look asks again, and gets the addon's lines once it is
// back.
func TestLeftovers_NotCachedWhenDown(t *testing.T) {
	h, tg := newHooks(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	left := h.MigrateLeftovers()
	ctx := context.Background()
	script := `{"migrate.leftovers": {"body": {"lines": [{"source": "books", "name": "Books", "status": "warning", "reason": "r"}]}}}`
	hookAddon(t, tg, "alpha", script, leftoverHooks, false)
	if got := left(ctx); len(got) != 0 {
		t.Fatalf("stopped: %+v", got)
	}
	if err := tg.sup.Start(ctx, tg.ins["alpha"]); err != nil {
		t.Fatal(err)
	}
	if got := leftoverReasons(left(ctx)); !slices.Equal(got, []string{"books: r"}) {
		t.Fatalf("back: %v", got)
	}

	// one addon failing: the others' lines are shown, and not kept
	hookAddon(t, tg, "broken", `{"migrate.leftovers": {"status": 500}}`, leftoverHooks, true)
	hookAddon(t, tg, "garbled", `{"migrate.leftovers": {"body": {"lines": "x"}}}`, leftoverHooks, true)
	now = now.Add(61 * time.Second)
	for range 2 {
		if got := leftoverReasons(left(ctx)); !slices.Equal(got, []string{"books: r"}) {
			t.Fatalf("with a failing addon: %v", got)
		}
	}
	if n := len(calls(t, tg, "broken")); n != 2 {
		t.Fatalf("broken asked %d times, want 2 (not kept)", n)
	}
	if n := len(calls(t, tg, "garbled")); n != 2 {
		t.Fatalf("garbled asked %d times, want 2 (not kept)", n)
	}
	delete(tg.ins, "broken")
	delete(tg.ins, "garbled")

	// a timeout: nothing, and the down period after it is not kept either
	h2, tg2 := newHooks(t)
	h2.now = h.now
	h2.timeouts = map[string]time.Duration{"migrate.leftovers": 100 * time.Millisecond}
	left2 := h2.MigrateLeftovers()
	hookAddon(t, tg2, "slow", `{"migrate.leftovers": {"delayMs": 400, "body": {"lines": [{"source": "asks", "name": "Asks", "status": "warning", "reason": "s"}]}}}`,
		leftoverHooks, true)
	if got := left2(ctx); len(got) != 0 {
		t.Fatalf("timed out: %+v", got)
	}
	if got := left2(ctx); len(got) != 0 {
		t.Fatalf("down period: %+v", got)
	}
	h2.timeouts = nil
	now = now.Add(61 * time.Second)
	if got := leftoverReasons(left2(ctx)); !slices.Equal(got, []string{"asks: s"}) {
		t.Fatalf("after the down period: %v", got)
	}
}

// A full answer is kept 60 s per language.
func TestLeftovers_Cached60s(t *testing.T) {
	h, tg := newHooks(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	left := h.MigrateLeftovers()
	hookAddon(t, tg, "alpha", `{"migrate.leftovers": {"body": {"lines": [{"source": "books", "name": "Books", "status": "warning", "reason": "r"}]}}}`,
		leftoverHooks, true)
	hookAddon(t, tg, "none", `{"migrate.leftovers": {"status": 404}}`, leftoverHooks, true)
	ctx := context.Background()
	first := left(ctx)
	if got := leftoverReasons(first); !slices.Equal(got, []string{"books: r"}) {
		t.Fatalf("first: %v", got)
	}
	first[0].Reason = "changed by the caller"
	now = now.Add(59 * time.Second)
	if got := leftoverReasons(left(ctx)); !slices.Equal(got, []string{"books: r"}) {
		t.Fatalf("at 59 s: %v", got)
	}
	if n := len(calls(t, tg, "alpha")); n != 1 {
		t.Fatalf("asked %d times in 60 s, want 1", n)
	}
	// another language is its own answer
	left(i18n.WithLanguage(ctx, i18n.EN))
	if n := len(calls(t, tg, "alpha")); n != 2 {
		t.Fatalf("asked %d times after an English look, want 2", n)
	}
	now = now.Add(2 * time.Second)
	if got := leftoverReasons(left(ctx)); !slices.Equal(got, []string{"books: r"}) {
		t.Fatalf("after 60 s: %v", got)
	}
	if n := len(calls(t, tg, "alpha")); n != 3 {
		t.Fatalf("asked %d times after 60 s, want 3", n)
	}
}

// listFails makes List fail while *fail is set.
type listFails struct {
	*hookTarget
	fail *bool
}

func (l listFails) List(ctx context.Context) ([]Installed, error) {
	if *l.fail {
		return nil, errors.New("store closed")
	}
	return l.hookTarget.List(ctx)
}

// The addons not listed is no answer: nothing from them, not kept; the next
// look with the list back gives their lines.
func TestLeftovers_ListFailsNotCached(t *testing.T) {
	h, tg := newHooks(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	fail := true
	h.T = listFails{tg, &fail}
	left := h.MigrateLeftovers()
	hookAddon(t, tg, "alpha", `{"migrate.leftovers": {"body": {"lines": [{"source": "books", "name": "Books", "status": "warning", "reason": "r"}]}}}`,
		leftoverHooks, true)
	ctx := context.Background()
	if got := left(ctx); len(got) != 0 {
		t.Fatalf("list fails: %+v", got)
	}
	fail = false
	if got := leftoverReasons(left(ctx)); !slices.Equal(got, []string{"books: r"}) {
		t.Fatalf("list back: %v", got)
	}
}

// F11: a kept answer outlives an addon that stops, until its 60 s end.
func TestLeftovers_StoppedKeepsUntil60s(t *testing.T) {
	h, tg := newHooks(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	left := h.MigrateLeftovers()
	hookAddon(t, tg, "alpha", `{"migrate.leftovers": {"body": {"lines": [{"source": "books", "name": "Books", "status": "warning", "reason": "r"}]}}}`,
		leftoverHooks, true)
	ctx := context.Background()
	if got := leftoverReasons(left(ctx)); !slices.Equal(got, []string{"books: r"}) {
		t.Fatalf("first: %v", got)
	}
	if err := tg.sup.Stop("alpha"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for st, _ := tg.sup.Serving("alpha"); st == StatusRunning; st, _ = tg.sup.Serving("alpha") {
		if time.Now().After(deadline) {
			t.Fatal("alpha did not stop")
		}
		time.Sleep(20 * time.Millisecond)
	}
	now = now.Add(59 * time.Second)
	if got := leftoverReasons(left(ctx)); !slices.Equal(got, []string{"books: r"}) {
		t.Fatalf("stopped, at 59 s: %v", got)
	}
	now = now.Add(2 * time.Second)
	if got := left(ctx); len(got) != 0 {
		t.Fatalf("stopped, after 60 s: %+v", got)
	}
}

// A hook that times out is down by itself: the addon's other hooks are
// still called and answer, and the slow hook fails at once until its down
// period ends.
func TestTimeout_CoolsOnlyThatHook(t *testing.T) {
	h, tg := newHooks(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	h.timeouts = map[string]time.Duration{"disk.usage": 200 * time.Millisecond}
	hookAddon(t, tg, "alpha", `{"disk.usage": {"delayMs": 5000, "body": {}}, "notify.interest": {"body": {"ids": [7]}}}`,
		[]string{"disk.usage", "notify.interest"}, true)
	if err := h.Call(context.Background(), "alpha", "disk.usage", nil, nil, nil); !errors.Is(err, ErrHookTimeout) {
		t.Fatalf("first call: err = %v", err)
	}
	var out struct{ IDs []int }
	if err := h.Call(context.Background(), "alpha", "notify.interest", nil, nil, &out); err != nil || !slices.Equal(out.IDs, []int{7}) {
		t.Fatalf("other hook: err = %v, out = %+v", err, out)
	}
	t0 := time.Now()
	err := h.Call(context.Background(), "alpha", "disk.usage", nil, nil, nil)
	if !errors.Is(err, ErrHookDown) || !errors.Is(err, errCooling) {
		t.Fatalf("slow hook in its down period: err = %v", err)
	}
	if d := time.Since(t0); d > 100*time.Millisecond {
		t.Fatalf("the cooled call took %v", d)
	}
	now = now.Add(hookDownFor + time.Second)
	if err := h.Call(context.Background(), "alpha", "disk.usage", nil, nil, nil); !errors.Is(err, ErrHookTimeout) {
		t.Fatalf("after the period the hook is called again: err = %v", err)
	}
	n := 0
	for _, c := range calls(t, tg, "alpha") {
		if c.Name == "disk.usage" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("disk.usage called %d times, want 2", n)
	}
}

// A status change of an addon (here a restart) ends all its down periods at
// once, and no other addon's.
func TestStatusChange_ClearsDownPeriods(t *testing.T) {
	h, tg := newHooks(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	tg.sup.OnStatus(h.StatusChanged)
	h.timeouts = map[string]time.Duration{"notify.interest": 100 * time.Millisecond, "disk.usage": 100 * time.Millisecond}
	script := `{"notify.interest": {"delayMs": 400, "body": {"ids": [1]}}, "disk.usage": {"delayMs": 400, "body": {}}}`
	hookAddon(t, tg, "alpha", script, []string{"notify.interest", "disk.usage"}, true)
	hookAddon(t, tg, "alphabet", script, []string{"notify.interest"}, true)
	for _, c := range []struct{ id, name string }{{"alpha", "notify.interest"}, {"alpha", "disk.usage"}, {"alphabet", "notify.interest"}} {
		if err := h.Call(context.Background(), c.id, c.name, nil, nil, nil); !errors.Is(err, ErrHookTimeout) {
			t.Fatalf("%s %s: err = %v", c.id, c.name, err)
		}
	}
	if err := tg.sup.Stop("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := tg.sup.Start(context.Background(), tg.ins["alpha"]); err != nil {
		t.Fatal(err)
	}
	h.timeouts = nil // 5 s: the answers come in time now
	var out struct{ IDs []int }
	if err := h.Call(context.Background(), "alpha", "notify.interest", nil, nil, &out); err != nil || !slices.Equal(out.IDs, []int{1}) {
		t.Fatalf("after the restart: err = %v, out = %+v", err, out)
	}
	if err := h.Call(context.Background(), "alpha", "disk.usage", nil, nil, nil); err != nil {
		t.Fatalf("after the restart, disk.usage: err = %v", err)
	}
	if err := h.Call(context.Background(), "alphabet", "notify.interest", nil, nil, nil); !errors.Is(err, errCooling) {
		t.Fatalf("another addon's down period ended: err = %v", err)
	}
}

// A caller that goes away (its ctx cancelled) is not the addon's fault: no
// down period.
func TestCall_CallerCancelNoDownPeriod(t *testing.T) {
	h, tg := newHooks(t)
	h.timeouts = map[string]time.Duration{"notify.interest": 300 * time.Millisecond}
	hookAddon(t, tg, "slow", `{"notify.interest": {"delayMs": 400, "body": {}}}`, []string{"notify.interest"}, true)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if err := h.Call(ctx, "slow", "notify.interest", nil, nil, nil); err == nil {
		t.Fatal("a cancelled call answered")
	}
	if h.isDown(downKey("slow", "notify.interest")) {
		t.Fatal("a cancelled caller started a down period")
	}
	h.timeouts = nil
	if err := h.Call(context.Background(), "slow", "notify.interest", nil, nil, nil); err != nil {
		t.Fatalf("after a cancelled call: err = %v", err)
	}
}
