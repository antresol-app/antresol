package addons

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/playlistimport"
)

// importHooks are the import.* hooks the fakes declare.
var importHooks = []string{"import.services", "import.stop", "import.resolve", "import.search"}

func serviceIDs(ss []playlistimport.ServiceInfo) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}

// The tiles of every addon, in addon-id order; the first addon to list a
// service owns it, and an id the server does not know is dropped.
func TestImportServices_Merge(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "zeta", `{"import.services": {"body": {"services": [
		{"id": "apple", "configured": false, "connect": "paste"},
		{"id": "file", "configured": true, "connect": "file", "files": ["csv"]}]}}}`, importHooks, true)
	hookAddon(t, tg, "alpha", `{"import.services": {"body": {"services": [
		{"id": "apple", "configured": true, "connect": "redirect", "setup": ["шаг"]},
		{"id": "nosuch", "configured": true, "connect": "redirect"}]}}}`, importHooks, true)
	got := h.ImportServices().Services(context.Background())
	if !slices.Equal(serviceIDs(got), []string{"apple", "file"}) {
		t.Fatalf("services %+v", got)
	}
	if a := got[0]; !a.Configured || a.Connect != "redirect" || !slices.Equal(a.Setup, []string{"шаг"}) || a.Files == nil {
		t.Fatalf("apple %+v (alpha's tile, lists never nil)", a)
	}
	if f := got[1]; !slices.Equal(f.Files, []string{"csv"}) || f.Setup == nil {
		t.Fatalf("file %+v", f)
	}
	for svc, want := range map[string]string{"apple": "alpha", "file": "zeta"} {
		if id, ok := h.ServiceOwner(svc); !ok || id != want {
			t.Errorf("ServiceOwner(%q) = %q %v, want %q", svc, id, ok, want)
		}
	}
	for _, svc := range []string{"nosuch", "", "../x"} {
		if id, ok := h.ServiceOwner(svc); ok {
			t.Errorf("ServiceOwner(%q) = %q", svc, id)
		}
	}
	// called for the household
	if cs := calls(t, tg, "alpha"); len(cs) == 0 || cs[0].Name != "import.services" || cs[0].User != "" {
		t.Fatalf("calls %+v", cs)
	}
}

// With no addon answering, there are no tiles (not nil: the page shows the
// file import) and no owner.
func TestImportServices_Down(t *testing.T) {
	h, tg := newHooks(t)
	h.timeouts = map[string]time.Duration{"import.services": 200 * time.Millisecond}
	if got := h.ImportServices().Services(context.Background()); got == nil || len(got) != 0 {
		t.Fatalf("no addons: %#v", got)
	}
	hookAddon(t, tg, "stopped", `{"import.services": {"body": {"services": [{"id": "apple"}]}}}`, importHooks, false)
	hookAddon(t, tg, "broken", `{"import.services": {"status": 500}}`, importHooks, true)
	hookAddon(t, tg, "garbled", `{"import.services": {"body": {"services": "x"}}}`, importHooks, true)
	hookAddon(t, tg, "slow", `{"import.services": {"delayMs": 5000, "body": {"services": [{"id": "apple"}]}}}`, importHooks, true)
	if got := h.ImportServices().Services(context.Background()); got == nil || len(got) != 0 {
		t.Fatalf("down: %#v", got)
	}
	if id, ok := h.ServiceOwner("apple"); ok {
		t.Fatalf("owner %q", id)
	}
}

// A Takeout's ids an addon cannot name (down, failing) stay unknown: no
// error, nothing named. Named ones are taken only for the ids asked, in
// batches.
func TestImportResolve_DownKeepsUnknown(t *testing.T) {
	h, tg := newHooks(t)
	r := h.ImportResolver()
	named, err := r.Resolve(context.Background(), []string{"v1"})
	if err != nil || len(named) != 0 {
		t.Fatalf("no addons: %v %v", named, err)
	}
	hookAddon(t, tg, "down", `{"import.resolve": {"body": {"tracks": [{"key": "v1", "title": "X"}]}}}`, importHooks, false)
	hookAddon(t, tg, "broken", `{"import.resolve": {"status": 500}}`, importHooks, true)
	named, err = r.Resolve(context.Background(), []string{"v1", "v2"})
	if err != nil || len(named) != 0 {
		t.Fatalf("down: %v %v", named, err)
	}

	hookAddon(t, tg, "alpha", `{"import.resolve": {"body": {"tracks": [
		{"key": "v1", "artist": "Кино", "title": "Кукушка", "album": "Чёрный альбом", "durationMs": 1000},
		{"key": "v2", "artist": "Кино", "title": ""},
		{"key": "other", "artist": "Кино", "title": "Не спрашивали"}]}}}`, importHooks, true)
	hookAddon(t, tg, "beta", `{"import.resolve": {"body": {"tracks": [
		{"key": "v1", "artist": "Б", "title": "Второй"},
		{"key": "v3", "artist": "Б", "title": "Звезда"}]}}}`, importHooks, true)
	old := importResolveBatch
	importResolveBatch = 2
	t.Cleanup(func() { importResolveBatch = old })
	named, err = r.Resolve(context.Background(), []string{"v1", "v2", "v3", "v4", "v5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(named) != 2 || named["v1"].Title != "Кукушка" || named["v1"].Artist != "Кино" || named["v1"].Album != "Чёрный альбом" ||
		named["v1"].Key != "v1" || named["v1"].DurationMs != 1000 || named["v3"].Title != "Звезда" {
		t.Fatalf("named %+v (alpha first, no empty title, only the ids asked)", named)
	}
	var bodies []string
	for _, c := range calls(t, tg, "alpha") {
		bodies = append(bodies, string(c.Body))
	}
	if want := []string{`{"ids":["v1","v2"]}`, `{"ids":["v3","v4"]}`, `{"ids":["v5"]}`}; !slices.Equal(bodies, want) {
		t.Fatalf("batches %v, want %v", bodies, want)
	}
}

// The review's search: every addon's candidates, in addon-id order; down
// ones add none and are no error.
func TestImportSearch_DownEmpty(t *testing.T) {
	h, tg := newHooks(t)
	c := h.ImportCatalog()
	// nobody declares it: as with no catalog at all
	if _, err := c.DiscoverMedia(context.Background(), "music", "кино"); !errors.Is(err, playlistimport.ErrCatalogOffline) {
		t.Fatalf("none declares: err = %v", err)
	}
	hookAddon(t, tg, "down", `{"import.search": {"body": {"candidates": [{"title": "X"}]}}}`, importHooks, false)
	hookAddon(t, tg, "broken", `{"import.search": {"status": 500}}`, importHooks, true)
	cs, err := c.DiscoverMedia(context.Background(), "music", "кино")
	if err != nil || cs == nil || len(cs) != 0 {
		t.Fatalf("down: %#v %v", cs, err)
	}
	hookAddon(t, tg, "alpha", `{"import.search": {"body": {"candidates": [
		{"kind": "music", "extId": "rg-1", "title": "Группа крови", "subtitle": "Кино", "year": 1988, "inLibrary": true,
		 "libraryItemId": 5, "aliases": ["Gruppa krovi"], "matchedTrack": "Кукушка", "byArtist": "Кино"}]}}}`, importHooks, true)
	cs, err = c.DiscoverMedia(context.Background(), "music", "кино")
	if err != nil || len(cs) != 1 {
		t.Fatalf("search: %+v %v", cs, err)
	}
	if x := cs[0]; x.ExtID != "rg-1" || x.Title != "Группа крови" || x.Year != 1988 || !x.InLibrary || x.LibraryItemID == nil ||
		*x.LibraryItemID != 5 || !slices.Equal(x.Aliases, []string{"Gruppa krovi"}) || x.MatchedTrack != "Кукушка" || x.ByArtist != "Кино" {
		t.Fatalf("candidate %+v", x)
	}
	cl := calls(t, tg, "alpha")
	if len(cl) != 1 || string(cl[0].Body) != `{"kind":"music","q":"кино"}` {
		t.Fatalf("calls %+v", cl)
	}
}

// The addon's catalog is offline: its refusal comes back with the text the
// API maps to 503.
func TestImportSearch_CatalogOffline(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "alpha", `{"import.search": {"status": 409, "body": {"error": "`+playlistimport.CatalogOffline+`"}}}`, importHooks, true)
	_, err := h.ImportCatalog().DiscoverMedia(context.Background(), "music", "кино")
	var ref *HookRefusal
	if !errors.As(err, &ref) || ref.Message != playlistimport.CatalogOffline || ref.Addon != "alpha" {
		t.Fatalf("err = %v", err)
	}
	// another addon's answer wins over the refusal
	hookAddon(t, tg, "beta", `{"import.search": {"body": {"candidates": [{"title": "Т"}]}}}`, importHooks, true)
	cs, err := h.ImportCatalog().DiscoverMedia(context.Background(), "music", "кино")
	if err != nil || len(cs) != 1 {
		t.Fatalf("with beta: %+v %v", cs, err)
	}
}

// import.stop is fired and forgotten: Stop never waits for the addon.
func TestImportStop_Async(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "alpha", `{"import.stop": {"delayMs": 1500, "status": 204}}`, importHooks, true)
	t0 := time.Now()
	h.ImportStopper().Stop(42)
	if d := time.Since(t0); d > 200*time.Millisecond {
		t.Fatalf("Stop took %v", d)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		cs := calls(t, tg, "alpha")
		if len(cs) == 1 {
			if cs[0].Name != "import.stop" || string(cs[0].Body) != `{"jobId":42}` {
				t.Fatalf("call %+v", cs[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no import.stop call: %+v", cs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// An import hook that times out counts as down by itself: the addon's other
// hooks (an import one included) are still called at once.
func TestImportTimeout_KeepsOtherHooks(t *testing.T) {
	h, tg := newHooks(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	h.timeouts = map[string]time.Duration{"import.services": 200 * time.Millisecond}
	hookAddon(t, tg, "alpha", `{"import.services": {"delayMs": 5000, "body": {"services": []}},
		"disk.usage": {"body": {"n": "alpha"}},
		"import.search": {"body": {"candidates": [{"title": "Т"}]}}}`, append([]string{"disk.usage"}, importHooks...), true)
	if err := h.Call(context.Background(), "alpha", "import.services", nil, nil, nil); !errors.Is(err, ErrHookTimeout) {
		t.Fatalf("first call: err = %v", err)
	}
	// the hook itself: down at once for the window
	t0 := time.Now()
	if got := h.ImportServices().Services(context.Background()); len(got) != 0 {
		t.Fatalf("services %+v", got)
	}
	if err := h.Call(context.Background(), "alpha", "import.services", nil, nil, nil); !errors.Is(err, ErrHookDown) {
		t.Fatalf("in the window: err = %v", err)
	}
	if d := time.Since(t0); d > 100*time.Millisecond {
		t.Fatalf("calls in the window took %v", d)
	}
	// the others are not
	var got []string
	h.Each(context.Background(), "disk.usage", nil, nil, func(id string, _ json.RawMessage) { got = append(got, id) })
	if !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("disk.usage = %v", got)
	}
	if cs, err := h.ImportCatalog().DiscoverMedia(context.Background(), "music", "q"); err != nil || len(cs) != 1 {
		t.Fatalf("import.search: %+v %v", cs, err)
	}
	// after the window the hook is called again
	now = now.Add(hookDownFor + time.Second)
	n := 0
	for _, c := range calls(t, tg, "alpha") {
		if c.Name == "import.services" {
			n++
		}
	}
	h.ImportServices().Services(context.Background()) // times out again, but it is called
	m := 0
	for _, c := range calls(t, tg, "alpha") {
		if c.Name == "import.services" {
			m++
		}
	}
	if n != 1 || m != 2 {
		t.Fatalf("import.services calls %d then %d", n, m)
	}
}

// Another hook's down period (disk.usage hung) does not cover the import
// hooks: they are still called and answer.
func TestImportHooks_NotCooledByAnotherHook(t *testing.T) {
	h, tg := newHooks(t)
	h.timeouts = map[string]time.Duration{"disk.usage": 200 * time.Millisecond}
	hookAddon(t, tg, "alpha", `{"disk.usage": {"delayMs": 5000, "body": {}},
		"import.services": {"body": {"services": [{"id": "apple"}]}}}`, append([]string{"disk.usage"}, importHooks...), true)
	if err := h.Call(context.Background(), "alpha", "disk.usage", nil, nil, nil); !errors.Is(err, ErrHookTimeout) {
		t.Fatalf("err = %v", err)
	}
	if got := h.ImportServices().Services(context.Background()); len(got) != 1 || got[0].ID != "apple" {
		t.Fatalf("services %+v", got)
	}
}

// A sign-in's callback is not lost while import.services is slow: the
// owner of the last good answer still owns the service through a timeout
// and the hook's down period; an answer without the service ends that.
func TestServiceOwner_SurvivesCooling(t *testing.T) {
	h, tg := newHooks(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	hookAddon(t, tg, "alpha", `{"import.services": {"delayMs": 300, "body": {"services": [{"id": "apple"}]}}}`, importHooks, true)
	if id, ok := h.ServiceOwner("apple"); !ok || id != "alpha" {
		t.Fatalf("good answer: %q %v", id, ok)
	}
	// the answer comes too late now: a timeout, then the down period
	h.timeouts = map[string]time.Duration{"import.services": 100 * time.Millisecond}
	if id, ok := h.ServiceOwner("apple"); !ok || id != "alpha" {
		t.Fatalf("timed out: %q %v", id, ok)
	}
	t0 := time.Now()
	if id, ok := h.ServiceOwner("apple"); !ok || id != "alpha" {
		t.Fatalf("down period: %q %v", id, ok)
	}
	if d := time.Since(t0); d > 100*time.Millisecond {
		t.Fatalf("the down period waited %v", d)
	}
	if id, ok := h.ServiceOwner("file"); ok {
		t.Fatalf("never listed: %q", id)
	}
	// a fresh answer without the service: no owner
	if err := tg.sup.Stop("alpha"); err != nil {
		t.Fatal(err)
	}
	hookAddon(t, tg, "alpha", `{"import.services": {"body": {"services": [{"id": "file"}]}}}`, importHooks, true)
	now = now.Add(hookDownFor + time.Second)
	h.timeouts = nil
	if id, ok := h.ServiceOwner("apple"); ok {
		t.Fatalf("no longer listed: %q", id)
	}
	if id, ok := h.ServiceOwner("file"); !ok || id != "alpha" {
		t.Fatalf("file: %q %v", id, ok)
	}
	// a stopped addon owns nothing, whatever it listed
	if err := tg.sup.Stop("alpha"); err != nil {
		t.Fatal(err)
	}
	if id, ok := h.ServiceOwner("file"); ok {
		t.Fatalf("stopped: %q", id)
	}
}
