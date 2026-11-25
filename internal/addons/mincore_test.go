package addons

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseManifest_MinCore(t *testing.T) {
	rep := func(extra string) []byte {
		return []byte(strings.Replace(validJSON(), `"contract":1,`, `"contract":1,`+extra, 1))
	}
	m, err := ParseManifest(rep(`"minCore":"0.8.28",`))
	if err != nil || m.MinCore != "0.8.28" {
		t.Fatalf("%+v %v", m, err)
	}
	if m, err = ParseManifest([]byte(validJSON())); err != nil || m.MinCore != "" {
		t.Fatalf("without the field: %+v %v", m, err)
	}
	for _, bad := range []string{"", "0.8", "v0.8.28", "0.8.28-rc.1", "0.8.28+x", "01.2.3", "latest", "1.2.3.4", " 0.8.28"} {
		_, err := ParseManifest(rep(`"minCore":"` + bad + `",`))
		if bad == "" { // an empty one is the same as none
			if err != nil {
				t.Errorf("empty minCore refused: %v", err)
			}
			continue
		}
		if msg, _ := UserMessage(err); !strings.Contains(msg, "неверная минимальная версия сервера") {
			t.Errorf("minCore %q: %v", bad, err)
		}
	}
	if _, err := ParseManifest(rep(`"minCore":8,`)); err == nil {
		t.Error("a number taken as minCore")
	}
}

func TestManifest_NeedsCore(t *testing.T) {
	for _, c := range []struct{ min, core, want string }{
		{"0.8.28", "0.8.27", "0.8.28"},
		{"0.8.28", "0.8.28", ""},
		{"0.8.28", "0.8.29", ""},
		{"0.8.28", "0.9.0", ""},
		{"0.8.28", "1.0.0", ""},
		{"0.8.28", "0.8.9", "0.8.28"},
		{"0.10.0", "0.9.99", "0.10.0"},
		{"0.8.28", "v0.8.28", ""},
		{"0.8.28", "v0.8.27", "0.8.28"},
		{"0.8.28", "0.8.28-rc.1", ""}, // a pre-release of a version counts as it
		{"0.8.28", "0.8.27-3-gabc1234", "0.8.28"},
		{"0.8.28", "0.8.28+build5", ""},
		{"0.8.28", "dev", ""}, // a development build is never older
		{"0.8.28", "", ""},
		{"0.8.28", "abc1234", ""},
		{"", "0.1.0", ""}, // no minCore
	} {
		m := Manifest{MinCore: c.min}
		if got := m.NeedsCore(c.core); got != c.want {
			t.Errorf("minCore %q on %q: %q, want %q", c.min, c.core, got, c.want)
		}
	}
}

func TestStart_NeedsNewerCore(t *testing.T) {
	s := newSup(t, "ok")
	in := install(t, s, "fakeaddon", "1.0.0", "")
	in.Manifest.MinCore = "0.8.28"
	s.Core = "0.8.27"
	if err := s.Start(context.Background(), in); err == nil {
		t.Fatal("started on an older server")
	}
	if got := s.Status("fakeaddon"); got != StatusIncompatible {
		t.Fatalf("status = %q", got)
	}
	if s.mints.Load() != 0 || alive(s.Dir) {
		t.Fatal("an addon needing a newer server was launched")
	}
	// the server it asks for, and one newer, run it
	s.Core = "0.8.28"
	if err := s.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if got := s.Status("fakeaddon"); got != StatusRunning {
		t.Fatalf("status on the minimum = %q", got)
	}
}

// setMinCore rewrites a local release's manifest with minCore.
func setMinCore(t *testing.T, dev, id, min string) {
	t.Helper()
	p := filepath.Join(dev, id, ManifestName)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m.MinCore = min
	if b, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

const needs0828 = "Нужна версия сервера 0.8.28 или новее — обновите сервер"

func TestManager_RefusesAReleaseNeedingANewerCore(t *testing.T) {
	m, dev := newManager(t)
	m.Sup.Core = "0.8.27"
	ctx := context.Background()

	writeLocal(t, dev, "hello", "1.0.0", "ok", true)
	setMinCore(t, dev, "hello", "0.8.28")

	// the preview and the install say what to update to, and leave nothing
	_, err := m.Preview(ctx, "local:hello")
	var nc NeedsCoreError
	if !errors.As(err, &nc) || nc.MinCore != "0.8.28" || nc.Version != "1.0.0" {
		t.Fatalf("preview: %v", err)
	}
	if msg, ok := UserMessage(err); !ok || msg != needs0828 {
		t.Fatalf("preview message %q", msg)
	}
	if _, err := m.Install(ctx, "local:hello", ""); !errors.As(err, &nc) {
		t.Fatalf("install: %v", err)
	}
	if _, err := m.Store.Get(ctx, "hello"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a row was left: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.Sup.Dir, "hello")); !os.IsNotExist(err) {
		t.Fatalf("files were left: %v", err)
	}

	// on a server that fits it installs, and the next release needing more is an update refused
	m.Sup.Core = "0.8.28"
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	writeLocal(t, dev, "hello", "1.1.0", "ok", true)
	setMinCore(t, dev, "hello", "0.9.0")
	_, err = m.Update(ctx, "hello", "")
	if !errors.As(err, &nc) || nc.MinCore != "0.9.0" {
		t.Fatalf("update: %v", err)
	}
	if got, _ := m.Store.Get(ctx, "hello"); got.Version != "1.0.0" || m.Status("hello") != StatusRunning {
		t.Fatalf("the running version was touched: %+v %q", got, m.Status("hello"))
	}
	// the update check says it too, in place of an «Обновить»
	c := m.CheckUpdate(ctx, "hello", false)
	if !c.NeedsCore || c.Version != "" || !errors.As(c.Err, &nc) || nc.MinCore != "0.9.0" {
		t.Fatalf("check: %+v", c)
	}
	// a server that is new enough finds it a plain update
	m.Sup.Core = "0.9.0"
	if c := m.CheckUpdate(ctx, "hello", false); c.NeedsCore || c.Version != "1.1.0" {
		t.Fatalf("check on a fitting server: %+v", c)
	}
}

// A server older than the release it runs (a rolled-back image) does not
// start the addon: it is incompatible, its token is refused, and the row
// stays so that the newer server runs it again.
func TestManager_StartAllWithAnAddonNeedingANewerCore(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	m.Sup.Core = "0.8.28"
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	setMinCore(t, dev, "hello", "0.8.28")
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	tok := addonToken(t, m, "hello")
	m.Sup.Stop("hello") // the server goes down...

	m.Sup.Core = "0.8.27" // ...and comes back as an older one
	m.StartAll(ctx)
	waitStatus(t, m, "hello", StatusIncompatible)
	if _, err := m.IDForToken(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("token of an incompatible addon: %v", err)
	}
	if got, err := m.Store.Get(ctx, "hello"); err != nil || got.Manifest.MinCore != "0.8.28" || !got.Enabled {
		t.Fatalf("row: %+v %v", got, err)
	}

	m.Sup.Core = "0.8.28"
	m.StartAll(ctx)
	waitStatus(t, m, "hello", StatusRunning)
}

// waitStatus waits for StartAll's background start to reach want.
func waitStatus(t *testing.T, m *Manager, id string, want Status) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if m.Status(id) == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("status of %s = %q, want %q", id, m.Status(id), want)
}

// A newer release on GitHub that needs a newer server is a «needsCore»
// check naming the version, until the server is that new.
func TestManager_CheckUpdateReleaseNeedsCore(t *testing.T) {
	m, _ := newManager(t)
	ctx := context.Background()
	rel := checkManifest("1.1.0")
	rel.MinCore = "0.8.28"
	f := newRealGH(t, rel)
	m.Resolver = &Resolver{API: f.srv.URL, wait: time.Millisecond}
	in := Installed{ID: "demo", Source: "github.com/o/r", Version: "1.0.0", Manifest: checkManifest("1.0.0"), Enabled: true,
		InstalledAt: time.Now(), UpdatedAt: time.Now()}
	if err := m.Store.Put(ctx, in, "tok"); err != nil {
		t.Fatal(err)
	}
	m.Sup.Core = "0.8.27"
	c := m.CheckUpdate(ctx, "demo", false)
	if !c.NeedsCore || c.Version != "" {
		t.Fatalf("check: %+v", c)
	}
	if msg, _ := UserMessage(c.Err); msg != needs0828 {
		t.Fatalf("message %q", msg)
	}
	m.Sup.Core = "0.8.28"
	if c := m.CheckUpdate(ctx, "demo", false); c.NeedsCore || c.Version != "1.1.0" || c.Err != nil {
		t.Fatalf("check on a fitting server: %+v", c)
	}
}

// A pinned release that needs a newer server is a failed pin: logged, and
// the addon stays off.
func TestEnsurePinned_ReleaseNeedsANewerCore(t *testing.T) {
	m, dev := newManager(t)
	m.Sup.Core = "0.8.27"
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	setMinCore(t, dev, "hello", "0.8.28")
	errs := ensure(t, m, "hello@1.0.0")
	var nc NeedsCoreError
	if len(errs) != 1 || !errors.As(errs[0], &nc) || nc.MinCore != "0.8.28" {
		t.Fatalf("errors: %v", errs)
	}
	if !m.PinFailed("hello") || m.Status("hello") == StatusRunning {
		t.Fatalf("failed %v, status %q", m.PinFailed("hello"), m.Status("hello"))
	}
	m.Sup.Core = "0.8.28"
	if errs := ensure(t, m, "hello@1.0.0"); len(errs) != 0 || m.Status("hello") != StatusRunning {
		t.Fatalf("on a fitting server: %v, %q", errs, m.Status("hello"))
	}
}
