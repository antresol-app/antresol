package addons

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestParsePins(t *testing.T) {
	for s, want := range map[string][]Pin{
		"":                                 nil,
		"   ":                              nil,
		"hello@1.0.0":                      {{ID: "hello", Version: "1.0.0"}},
		" hello@1.0.0 , other@2.1.0-rc.1 ": {{ID: "hello", Version: "1.0.0"}, {ID: "other", Version: "2.1.0-rc.1"}},
	} {
		got, err := ParsePins(s)
		if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("ParsePins(%q) = %v, %v; want %v", s, got, err, want)
		}
	}
	for _, s := range []string{
		"hello",                    // no @
		"hello@",                   // no version
		"@1.0.0",                   // no id
		"hello@1.0.0,",             // an empty part
		",hello@1.0.0",             // an empty part
		"hello@1.0.0,,other@1.0.0", // an empty part
		"hello@1.0.0,hello@1.1.0",  // a duplicate id
		"Hello@1.0.0",              // not an addon id
		"../x@1.0.0",               // not an addon id
		"hello@1.0.0@2",            // two @
		"hello@1 0",                // a space in the version
	} {
		if got, err := ParsePins(s); err == nil {
			t.Errorf("ParsePins(%q) = %v, want an error", s, got)
		}
	}
}

func pins(s string) []Pin {
	p, err := ParsePins(s)
	if err != nil {
		panic(err)
	}
	return p
}

func ensure(t *testing.T, m *Manager, s string) []error {
	t.Helper()
	return m.EnsurePinned(context.Background(), pins(s))
}

// writeData puts a file into the addon's data directory.
func writeData(t *testing.T, m *Manager, id string) string {
	t.Helper()
	p := filepath.Join(m.Sup.DataDir(id), "keep.txt")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func dataKept(t *testing.T, p string) {
	t.Helper()
	if b, err := os.ReadFile(p); err != nil || string(b) != "kept" {
		t.Fatalf("data: %q, %v", b, err)
	}
}

func wantPinned(t *testing.T, m *Manager, id, version string) Installed {
	t.Helper()
	in, err := m.Store.Get(context.Background(), id)
	if err != nil || in.Version != version || in.Source != LocalPrefix+id || !in.Enabled || in.PrevVersion != "" {
		t.Fatalf("store = %+v, %v; want %s@%s local, enabled", in, err, id, version)
	}
	waitFor(t, 5*time.Second, id+" running", func() bool { return m.Status(id) == StatusRunning })
	if !m.Pinned(id) {
		t.Fatalf("%s not pinned", id)
	}
	return in
}

func TestEnsurePinned_FreshInstall(t *testing.T) {
	m, dev := newManager(t)
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if errs := ensure(t, m, "hello@1.0.0"); len(errs) != 0 {
		t.Fatal(errs)
	}
	wantPinned(t, m, "hello", "1.0.0")
}

func TestEnsurePinned_SameVersionNoop(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	before, _ := m.Store.Get(ctx, "hello")
	m.Sup.Stop("hello") // a boot: nothing runs yet
	if errs := ensure(t, m, "hello@1.0.0"); len(errs) != 0 {
		t.Fatal(errs)
	}
	in := wantPinned(t, m, "hello", "1.0.0")
	if !in.UpdatedAt.Equal(before.UpdatedAt) || !in.InstalledAt.Equal(before.InstalledAt) {
		t.Fatalf("same version reinstalled: %+v, was %+v", in, before)
	}
}

func TestEnsurePinned_Upgrade(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	data := writeData(t, m, "hello")
	m.Sup.Stop("hello")
	writeLocal(t, dev, "hello", "1.2.0", "ok", false)
	if errs := ensure(t, m, "hello@1.2.0"); len(errs) != 0 {
		t.Fatal(errs)
	}
	wantPinned(t, m, "hello", "1.2.0")
	dataKept(t, data)
	if _, err := os.Stat(filepath.Join(m.Sup.Dir, "hello", "1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("old version dir left: %v", err)
	}
}

// TestEnsurePinned_Downgrade: the pin is the version that runs, also when
// the installed one is newer.
func TestEnsurePinned_Downgrade(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.2.0", "ok", false)
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	data := writeData(t, m, "hello")
	m.Sup.Stop("hello")
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if errs := ensure(t, m, "hello@1.0.0"); len(errs) != 0 {
		t.Fatal(errs)
	}
	wantPinned(t, m, "hello", "1.0.0")
	dataKept(t, data)
}

func TestEnsurePinned_ReenablesDisabled(t *testing.T) {
	for _, v := range []string{"1.0.0", "1.1.0"} { // the same version, and an update
		t.Run(v, func(t *testing.T) {
			m, dev := newManager(t)
			ctx := context.Background()
			writeLocal(t, dev, "hello", "1.0.0", "ok", false)
			if _, err := m.Install(ctx, "local:hello", ""); err != nil {
				t.Fatal(err)
			}
			if err := m.SetEnabled(ctx, "hello", false); err != nil {
				t.Fatal(err)
			}
			writeLocal(t, dev, "hello", v, "ok", false)
			if errs := ensure(t, m, "hello@"+v); len(errs) != 0 {
				t.Fatal(errs)
			}
			wantPinned(t, m, "hello", v)
		})
	}
}

func TestEnsurePinned_OtherSource(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	in, err := m.Install(ctx, "local:hello", "")
	if err != nil {
		t.Fatal(err)
	}
	in.Source = "github.com/o/hello" // installed from GitHub
	if err := m.Store.Save(ctx, in); err != nil {
		t.Fatal(err)
	}
	data := writeData(t, m, "hello")
	m.Sup.Stop("hello")
	var dropped []string
	m.OnRemove = func(_ context.Context, id string) {
		// the new install is in place, not yet started
		if got, err := m.Store.Get(context.Background(), id); err != nil || got.Source != "local:hello" || m.Status(id) == StatusRunning {
			t.Errorf("OnRemove before the new install: %+v, %v, %q", got, err, m.Status(id))
		}
		dropped = append(dropped, id)
	}
	writeLocal(t, dev, "hello", "1.0.0", "ok", false) // even the same version
	if errs := ensure(t, m, "hello@1.0.0"); len(errs) != 0 {
		t.Fatal(errs)
	}
	wantPinned(t, m, "hello", "1.0.0")
	dataKept(t, data)
	if fmt.Sprint(dropped) != "[hello]" {
		t.Fatalf("OnRemove calls %v", dropped)
	}
}

// TestEnsurePinned_OtherSourceFailedKeepsState: the release does not
// unpack; the old addon's notifications and holds stay.
func TestEnsurePinned_OtherSourceFailedKeepsState(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	in, err := m.Install(ctx, "local:hello", "")
	if err != nil {
		t.Fatal(err)
	}
	in.Source = "github.com/o/hello"
	if err := m.Store.Save(ctx, in); err != nil {
		t.Fatal(err)
	}
	data := writeData(t, m, "hello")
	m.Sup.Stop("hello")
	m.OnRemove = func(_ context.Context, id string) { t.Errorf("OnRemove(%s) after a failed pin", id) }
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if err := os.WriteFile(filepath.Join(dev, "hello", "addon.tar.gz"), []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if errs := ensure(t, m, "hello@1.0.0"); len(errs) != 1 {
		t.Fatalf("errors = %v", errs)
	}
	dataKept(t, data)
	if m.Status("hello") != StatusStopped {
		t.Fatal("started")
	}
}

// TestEnsurePinned_SameVersionOtherBytes: an install of the pinned version
// from other files is installed again from the release; one that cannot
// be is not started.
func TestEnsurePinned_SameVersionOtherBytes(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	old, _ := m.Store.Get(ctx, "hello")
	data := writeData(t, m, "hello")
	m.Sup.Stop("hello")
	writeLocal(t, dev, "hello", "1.0.0", "slowready", true) // other bytes, a web bundle
	rel, err := LocalRelease(filepath.Join(dev, "hello"))
	if err != nil || sameRelease(old.Manifest, rel.Manifest) {
		t.Fatalf("the fixture has the same files: %v", err)
	}
	if errs := ensure(t, m, "hello@1.0.0"); len(errs) != 0 {
		t.Fatal(errs)
	}
	in := wantPinned(t, m, "hello", "1.0.0")
	if !sameRelease(in.Manifest, rel.Manifest) {
		t.Fatal("not installed from the release")
	}
	if _, err := os.Stat(filepath.Join(m.Sup.WebDir("hello", "1.0.0"), "index.js")); err != nil {
		t.Fatalf("the release's web bundle: %v", err)
	}
	dataKept(t, data)

	// other bytes again, and they never get healthy: not started
	m.Sup.Stop("hello")
	writeLocal(t, dev, "hello", "1.0.0", "never", false)
	m2 := &Manager{Store: m.Store, Sup: m.Sup, Resolver: m.Resolver, Dev: dev} // a new boot
	if errs := m2.EnsurePinned(ctx, pins("hello@1.0.0")); len(errs) != 1 {
		t.Fatalf("errors = %v", errs)
	}
	if st := m2.Status("hello"); st != StatusStopped {
		t.Fatalf("status %q", st)
	}
	if err := m2.SetEnabled(ctx, "hello", true); !errors.Is(err, ErrPinNotApplied) {
		t.Fatalf("enable: %v", err)
	}
	dataKept(t, data)
}

// TestPinned_EnableBeforeEnsure: at boot, before EnsurePinned applied the
// pin, the switch starts nothing pinned.
func TestPinned_EnableBeforeEnsure(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(ctx, "hello", false); err != nil {
		t.Fatal(err)
	}
	m.SetPins(pins("hello@1.0.0"))
	if err := m.SetEnabled(ctx, "hello", true); !errors.Is(err, ErrPinNotApplied) {
		t.Fatalf("enable before the pin: %v", err)
	}
	if m.Status("hello") != StatusStopped {
		t.Fatal("started before the pin")
	}
	if msg, ok := UserMessage(ErrPinNotApplied); !ok || msg == "" {
		t.Fatal("no user message")
	}
	if errs := ensure(t, m, "hello@1.0.0"); len(errs) != 0 {
		t.Fatal(errs)
	}
	wantPinned(t, m, "hello", "1.0.0")
	if err := m.SetEnabled(ctx, "hello", false); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(ctx, "hello", true); err != nil {
		t.Fatalf("enable after the pin: %v", err)
	}
}

// TestBoot_PinsBeforeStartAll: Boot applies the pins before it starts the
// other addons, so a stale install of a pinned id never runs, even for a
// moment.
func TestBoot_PinsBeforeStartAll(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	writeLocal(t, dev, "other", "1.0.0", "ok", false)
	for _, id := range []string{"hello", "other"} {
		if _, err := m.Install(ctx, "local:"+id, ""); err != nil {
			t.Fatal(err)
		}
		m.Sup.Stop(id)
	}
	writeLocal(t, dev, "hello", "2.0.0", "never", false) // the pin fails
	var mu sync.Mutex
	var seen []string
	m.Sup.OnStatus(func(id string, st Status) {
		mu.Lock()
		seen = append(seen, id+" "+string(st))
		mu.Unlock()
	})
	if errs := m.Boot(ctx, pins("hello@2.0.0")); len(errs) != 1 {
		t.Fatalf("errors = %v", errs)
	}
	waitFor(t, 5*time.Second, "other running", func() bool { return m.Status("other") == StatusRunning })
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	for _, e := range seen {
		if e == "hello running" {
			t.Fatalf("a stale pinned build ran: %v", seen)
		}
	}
}

func TestEnsurePinned_MissingInDev(t *testing.T) {
	m, dev := newManager(t)
	writeLocal(t, dev, "other", "1.0.0", "ok", false)
	writeLocal(t, dev, "third", "2.0.0", "ok", false)
	// "hello" is not in Dev; "third" is there in another version
	errs := ensure(t, m, "hello@1.0.0,other@1.0.0,third@1.0.0")
	if len(errs) != 2 {
		t.Fatalf("errors = %v, want two", errs)
	}
	wantPinned(t, m, "other", "1.0.0")
	for _, id := range []string{"hello", "third"} {
		if _, err := m.Store.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s installed: %v", id, err)
		}
		if !m.Pinned(id) {
			t.Fatalf("%s: a failed pin is still pinned", id)
		}
	}
}

func TestEnsurePinned_NoDev(t *testing.T) {
	m, _ := newManager(t)
	m.Dev = ""
	if errs := ensure(t, m, "hello@1.0.0"); len(errs) != 1 {
		t.Fatalf("errors = %v", errs)
	}
}

// TestEnsurePinned_FailedPinNotStarted: a stale install of a pinned id is
// never started in its pin's place.
func TestEnsurePinned_FailedPinNotStarted(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T, dev string){
		"missing":   func(t *testing.T, dev string) { os.RemoveAll(filepath.Join(dev, "hello")) },
		"unhealthy": func(t *testing.T, dev string) { writeLocal(t, dev, "hello", "2.0.0", "never", false) },
	} {
		t.Run(name, func(t *testing.T) {
			m, dev := newManager(t)
			ctx := context.Background()
			writeLocal(t, dev, "hello", "1.0.0", "ok", false)
			writeLocal(t, dev, "other", "1.0.0", "ok", false)
			for _, id := range []string{"hello", "other"} {
				if _, err := m.Install(ctx, "local:"+id, ""); err != nil {
					t.Fatal(err)
				}
				m.Sup.Stop(id)
			}
			prepare(t, dev)
			if errs := ensure(t, m, "hello@2.0.0"); len(errs) != 1 {
				t.Fatalf("errors = %v", errs)
			}
			m.StartAll(ctx)
			waitFor(t, 5*time.Second, "other running", func() bool { return m.Status("other") == StatusRunning })
			time.Sleep(300 * time.Millisecond)
			if st := m.Status("hello"); st != StatusStopped {
				t.Fatalf("a failed pin's old version: %q", st)
			}
			if got, _ := m.Store.Get(ctx, "hello"); got.Version != "1.0.0" {
				t.Fatalf("store = %+v", got)
			}
			// the admin's switch does not start the stale build either
			if err := m.SetEnabled(ctx, "hello", true); !errors.Is(err, ErrPinNotApplied) {
				t.Fatalf("enable after a failed pin: %v", err)
			}
			if st := m.Status("hello"); st != StatusStopped {
				t.Fatalf("enabled a failed pin: %q", st)
			}
			if err := m.SetEnabled(ctx, "hello", false); err != nil {
				t.Fatalf("disable of a failed pin: %v", err)
			}
			if !m.PinFailed("hello") || fmt.Sprint(m.FailedPins()) != "[hello@2.0.0]" {
				t.Fatalf("failed pins %v", m.FailedPins())
			}
		})
	}
}

// TestEnsurePinned_StartAllLeavesPinsRunning: StartAll does not start a
// pinned addon a second time.
func TestEnsurePinned_StartAllLeavesPinsRunning(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if errs := ensure(t, m, "hello@1.0.0"); len(errs) != 0 {
		t.Fatal(errs)
	}
	wantPinned(t, m, "hello", "1.0.0")
	sock := m.Sup.Socket("hello")
	m.StartAll(ctx)
	time.Sleep(300 * time.Millisecond)
	if m.Sup.Socket("hello") != sock {
		t.Fatal("StartAll restarted a pinned addon")
	}
}

func TestPinnedLocked(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if errs := ensure(t, m, "hello@1.0.0"); len(errs) != 0 {
		t.Fatal(errs)
	}
	writeLocal(t, dev, "hello", "1.1.0", "ok", false)
	if _, err := m.Install(ctx, "local:hello", ""); !errors.Is(err, ErrPinned) {
		t.Fatalf("install: %v", err)
	}
	if _, err := m.Update(ctx, "hello", ""); !errors.Is(err, ErrPinned) {
		t.Fatalf("update: %v", err)
	}
	if err := m.Remove(ctx, "hello", false); !errors.Is(err, ErrPinned) {
		t.Fatalf("remove: %v", err)
	}
	if err := m.Remove(ctx, "hello", true); !errors.Is(err, ErrPinned) {
		t.Fatalf("remove with data: %v", err)
	}
	if msg, ok := UserMessage(ErrPinned); !ok || msg != "Расширение закреплено при развёртывании" {
		t.Fatalf("message %q, %v", msg, ok)
	}
	if _, ok := m.UpdateAvailable(ctx, "hello"); ok {
		t.Fatal("an update offered for a pinned addon")
	}
	wantPinned(t, m, "hello", "1.0.0")
	if err := m.SetEnabled(ctx, "hello", false); err != nil {
		t.Fatal(err)
	}
	if st := m.Status("hello"); st != StatusStopped {
		t.Fatalf("disabled: %q", st)
	}
	if err := m.SetEnabled(ctx, "hello", true); err != nil {
		t.Fatal(err)
	}
	wantPinned(t, m, "hello", "1.0.0")
	// a pinned id that is not installed (its pin failed) is locked too
	if errs := ensure(t, m, "nope@1.0.0"); len(errs) != 1 {
		t.Fatalf("errors = %v", errs)
	}
	writeLocal(t, dev, "nope", "1.0.0", "ok", false)
	if _, err := m.Install(ctx, "local:nope", ""); !errors.Is(err, ErrPinned) {
		t.Fatalf("install of a failed pin: %v", err)
	}
}
