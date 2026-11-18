package addons

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// home.continue is a known hook whose deadline is a hard 500 ms: Home
// never waits longer for an addon's cards.
func TestHookTimeouts_HomeContinue500ms(t *testing.T) {
	if got := HookTimeout("home.continue"); got != 500*time.Millisecond {
		t.Fatalf("HookTimeout = %v", got)
	}
	if d := hookDeadline(context.Background(), "home.continue", nil); d != 500*time.Millisecond {
		t.Fatalf("hookDeadline = %v", d)
	}
	if !knownHook("home.continue") {
		t.Fatal("home.continue is not a known hook")
	}
}

// OpenTo is the one access rule (the bundles' list): admins get every
// addon, other profiles only those with access "adults".
func TestManifest_OpenTo(t *testing.T) {
	for _, c := range []struct {
		access string
		admin  bool
		want   bool
	}{{"", true, true}, {"admins", true, true}, {"adults", true, true}, {"", false, false}, {"admins", false, false}, {"adults", false, true}} {
		if got := (Manifest{Access: c.access}).OpenTo(c.admin); got != c.want {
			t.Errorf("access %q admin %v: %v", c.access, c.admin, got)
		}
	}
}

// EachFor asks only the addons admit lets through.
func TestHooks_EachForAdmits(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "a", `{"home.continue": {"body": {"cards": []}}}`, []string{"home.continue"}, true)
	hookAddon(t, tg, "b", `{"home.continue": {"body": {"cards": []}}}`, []string{"home.continue"}, true)
	var got []string
	h.EachFor(context.Background(), "home.continue", nil, func(in Installed) bool { return in.ID == "b" }, nil,
		func(id string, _ json.RawMessage) { got = append(got, id) })
	if len(got) != 1 || got[0] != "b" || len(calls(t, tg, "a")) != 0 {
		t.Fatalf("answered %v, a called %d times", got, len(calls(t, tg, "a")))
	}
}
