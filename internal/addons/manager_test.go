package addons

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// writeLocal puts a local release of id/version into dev/id: a platform
// tar.gz whose bin/addon runs the fake addon in mode, and, with web, a
// one-file web bundle.
func writeLocal(t *testing.T, dev, id, version, mode string, web bool) {
	t.Helper()
	writeLocalHooks(t, dev, id, version, mode, web, nil)
}

// writeLocalHooks is writeLocal with the manifest declaring hooks.
func writeLocalHooks(t *testing.T, dev, id, version, mode string, web bool, hooks []string) {
	t.Helper()
	dir := filepath.Join(dev, id)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nFAKE_MODE=%s exec %s\n", mode, fakeAddon(t))
	asset := func(name string, ents []tent) Platform {
		b := makeTGZ(t, ents)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		return Platform{Asset: name, SHA256: hex.EncodeToString(sum[:])}
	}
	m := Manifest{
		ID: id, Name: Text{RU: "Привет", EN: "Hello"}, Version: version, Contract: ContractVersion,
		Platforms: map[string]Platform{runtime.GOOS + "/" + runtime.GOARCH: asset("addon.tar.gz", []tent{
			{name: "bin/", typ: tar.TypeDir, mode: 0o755},
			{name: "bin/addon", typ: tar.TypeReg, mode: 0o755, body: script},
		})},
		Permissions: []string{"events"},
		Hooks:       hooks,
	}
	if web {
		w := asset("web.tar.gz", []tent{{name: "index.js", typ: tar.TypeReg, mode: 0o644, body: "export default 1\n"}})
		m.Web = &w
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, ManifestName), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newManager(t *testing.T) (*Manager, string) {
	t.Helper()
	s := newSup(t, "ok")
	s.Health = 2 * time.Second
	m := &Manager{Store: &Store{DB: openTestDB(t)}, Sup: s.Supervisor, Resolver: &Resolver{}, Dev: t.TempDir()}
	s.Mint = m.Mint
	return m, m.Dev
}

// addonToken reads the token the running process got.
func addonToken(t *testing.T, m *Manager, id string) string {
	t.Helper()
	code, body := get(t, m.Sup.Socket(id), "/env")
	if code != 200 {
		t.Fatalf("/env = %d", code)
	}
	var env []string
	json.Unmarshal(body, &env)
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "ANTRESOL_ADDON_TOKEN="); ok {
			return v
		}
	}
	t.Fatal("no token in env")
	return ""
}

func TestManager_InstallLocal(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", true)
	in, err := m.Install(ctx, "local:hello", "")
	if err != nil {
		t.Fatal(err)
	}
	if in.ID != "hello" || in.Version != "1.0.0" || in.Source != "local:hello" || !in.Enabled {
		t.Fatalf("installed = %+v", in)
	}
	got, err := m.Store.Get(ctx, "hello")
	if err != nil || got.Version != "1.0.0" || got.PrevVersion != "" {
		t.Fatalf("store = %+v, %v", got, err)
	}
	if fi, err := os.Stat(m.Sup.Binary("hello", "1.0.0")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("binary: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(m.Sup.WebDir("hello", "1.0.0"), "index.js")); err != nil || !strings.Contains(string(b), "export") {
		t.Fatalf("web: %v", err)
	}
	if st := m.Status("hello"); st != StatusRunning {
		t.Fatalf("status = %q", st)
	}

	tok := addonToken(t, m, "hello")
	if len(tok) != 64 {
		t.Fatalf("token %q: want 32 bytes hex", tok)
	}
	if id, err := m.IDForToken(ctx, tok); err != nil || id != "hello" {
		t.Fatalf("IDForToken = %q, %v", id, err)
	}
	if _, err := m.IDForToken(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}

	// same version again: refused, nothing changes
	if _, err := m.Install(ctx, "local:hello", ""); err == nil {
		t.Fatal("reinstalled the same version")
	}
	if m.Status("hello") != StatusRunning {
		t.Fatal("reinstall attempt stopped the addon")
	}

	// disabled: stopped and its token refused
	if err := m.SetEnabled(ctx, "hello", false); err != nil {
		t.Fatal(err)
	}
	if st, sock := m.Serving("hello"); st != StatusStopped || sock != "" {
		t.Fatalf("disabled addon status %q", st)
	}
	if _, err := m.IDForToken(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("token of a disabled addon: %v", err)
	}
	if err := m.SetEnabled(ctx, "hello", true); err != nil {
		t.Fatal(err)
	}
	if m.Status("hello") != StatusRunning {
		t.Fatalf("re-enabled status %q", m.Status("hello"))
	}
	if _, err := m.IDForToken(ctx, tok); err == nil {
		t.Fatal("the old process's token still works after a restart")
	}
	if id, err := m.IDForToken(ctx, addonToken(t, m, "hello")); err != nil || id != "hello" {
		t.Fatalf("new token: %q, %v", id, err)
	}
}

func TestManager_UpdateUnhealthyKeepsOld(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	writeLocal(t, dev, "hello", "1.1.0", "never", false)
	if v, ok := m.UpdateAvailable(ctx, "hello"); !ok || v != "1.1.0" {
		t.Fatalf("UpdateAvailable = %q, %v", v, ok)
	}
	if _, err := m.Update(ctx, "hello", ""); err == nil {
		t.Fatal("update to a never-healthy version succeeded")
	}
	got, err := m.Store.Get(ctx, "hello")
	if err != nil || got.Version != "1.0.0" || got.PrevVersion != "" {
		t.Fatalf("store after failed update = %+v, %v", got, err)
	}
	if m.Status("hello") != StatusRunning {
		t.Fatalf("status = %q", m.Status("hello"))
	}
	if _, sock := m.Serving("hello"); sock == "" {
		t.Fatal("old version not running")
	} else if code, _ := get(t, sock, "/health"); code != 200 {
		t.Fatal("old version not serving")
	}
	if _, err := os.Stat(filepath.Join(m.Sup.Dir, "hello", "1.1.0")); !os.IsNotExist(err) {
		t.Fatalf("failed version dir left: %v", err)
	}
	if _, err := os.Stat(m.Sup.Binary("hello", "1.0.0")); err != nil {
		t.Fatalf("old version dir: %v", err)
	}
	// the running process holds a valid token
	if id, err := m.IDForToken(ctx, addonToken(t, m, "hello")); err != nil || id != "hello" {
		t.Fatalf("token after failed update: %q, %v", id, err)
	}

	writeLocal(t, dev, "hello", "1.2.0", "ok", false)
	in, err := m.Update(ctx, "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	if in.Version != "1.2.0" || in.PrevVersion != "" {
		t.Fatalf("updated = %+v", in)
	}
	if got, _ := m.Store.Get(ctx, "hello"); got.Version != "1.2.0" || got.PrevVersion != "" {
		t.Fatalf("store after update = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(m.Sup.Dir, "hello", "1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("previous version dir left: %v", err)
	}
	if _, ok := m.UpdateAvailable(ctx, "hello"); ok {
		t.Fatal("update offered for the installed version")
	}
	// an older release is not an update
	writeLocal(t, dev, "hello", "1.1.5", "ok", false)
	if _, err := m.Update(ctx, "hello", ""); err == nil {
		t.Fatal("downgraded")
	}
}

// TestManager_PinsPreviewedVersion: install and update take the version
// the admin saw and refuse when the latest release is another one.
func TestManager_PinsPreviewedVersion(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if _, err := m.Install(ctx, "local:hello", "0.9.0"); err == nil || !strings.Contains(err.Error(), "Релиз расширения изменился") {
		t.Fatalf("install of another version: %v", err)
	}
	if _, err := m.Store.Get(ctx, "hello"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("installed anyway: %v", err)
	}
	if _, err := m.Install(ctx, "local:hello", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	writeLocal(t, dev, "hello", "1.2.0", "ok", false)
	if _, err := m.Update(ctx, "hello", "1.1.0"); err == nil || !strings.Contains(err.Error(), "Релиз расширения изменился") {
		t.Fatalf("update to another version: %v", err)
	}
	if got, _ := m.Store.Get(ctx, "hello"); got.Version != "1.0.0" {
		t.Fatalf("updated anyway: %+v", got)
	}
	if in, err := m.Update(ctx, "hello", "1.2.0"); err != nil || in.Version != "1.2.0" {
		t.Fatalf("pinned update: %+v %v", in, err)
	}
}

func TestManager_FailedFirstInstallLeavesNothing(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "never", false)
	if _, err := m.Install(ctx, "local:hello", ""); err == nil {
		t.Fatal("installed a never-healthy addon")
	}
	if _, err := m.Store.Get(ctx, "hello"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("store row left: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.Sup.Dir, "hello", "1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("version dir left: %v", err)
	}
}

func TestManager_Remove(t *testing.T) {
	for _, withData := range []bool{false, true} {
		t.Run(fmt.Sprint("data=", withData), func(t *testing.T) {
			m, dev := newManager(t)
			ctx := context.Background()
			writeLocal(t, dev, "hello", "1.0.0", "ok", true)
			if _, err := m.Install(ctx, "local:hello", ""); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(m.Sup.DataDir("hello"), "state"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := m.Remove(ctx, "hello", withData); err != nil {
				t.Fatal(err)
			}
			if st, sock := m.Serving("hello"); st != StatusStopped || sock != "" {
				t.Fatal("still running")
			}
			if _, err := m.Store.Get(ctx, "hello"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("store row: %v", err)
			}
			if _, err := os.Stat(filepath.Join(m.Sup.Dir, "hello", "1.0.0")); !os.IsNotExist(err) {
				t.Fatalf("version dir: %v", err)
			}
			_, err := os.Stat(filepath.Join(m.Sup.DataDir("hello"), "state"))
			if withData && !os.IsNotExist(err) {
				t.Fatalf("data kept: %v", err)
			}
			if !withData && err != nil {
				t.Fatalf("data lost: %v", err)
			}
			if err := m.Remove(ctx, "hello", true); !errors.Is(err, ErrNotFound) {
				t.Fatalf("second remove: %v", err)
			}
		})
	}
}

// A remove that fails half way leaves the row, so that it can be run again
// and finish: the files are not orphaned behind an addon that is gone.
func TestManager_RemoveRetriesAFailedStep(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", true)
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	good := &RefStore{DB: m.Store.DB}
	bad := openTestDB(t)
	bad.Close()
	m.Refs = &RefStore{DB: bad}
	if err := m.Remove(ctx, "hello", false); err == nil {
		t.Fatal("a failed step must be an error")
	}
	if _, err := m.Store.Get(ctx, "hello"); err != nil {
		t.Fatalf("row after the failure: %v", err)
	}
	m.Refs = good
	if err := m.Remove(ctx, "hello", false); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := m.Store.Get(ctx, "hello"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row after the retry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.Sup.Dir, "hello", "1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("version dir: %v", err)
	}
}

func TestManager_LocalSourceChecks(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	outside := t.TempDir()
	writeLocal(t, outside, "evil", "1.0.0", "ok", false)
	if err := os.Symlink(filepath.Join(outside, "evil"), filepath.Join(dev, "evil")); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"local:../hello", "local:Hello", "local:", "local:hello/..", "local:evil", "local:missing"} {
		if _, err := m.Preview(ctx, src); err == nil {
			t.Errorf("%s: accepted", src)
		}
	}
	if man, err := m.Preview(ctx, "local:hello"); err != nil || man.ID != "hello" {
		t.Fatalf("preview = %+v, %v", man, err)
	}
	m.Dev = ""
	if _, err := m.Preview(ctx, "local:hello"); err == nil {
		t.Fatal("local source without a dev dir")
	}
}

func TestManager_InstallIDMismatch(t *testing.T) {
	m, dev := newManager(t)
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if err := os.Rename(filepath.Join(dev, "hello"), filepath.Join(dev, "other")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(context.Background(), "local:other", ""); err == nil {
		t.Fatal("a local dir named other installed addon hello")
	}
}

func TestManager_StartAll(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	writeLocal(t, dev, "other", "1.0.0", "ok", false)
	for _, id := range []string{"hello", "other"} {
		if _, err := m.Install(ctx, "local:"+id, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.SetEnabled(ctx, "other", false); err != nil {
		t.Fatal(err)
	}
	m.Sup.Stop("hello")
	m.StartAll(ctx)
	waitFor(t, 5*time.Second, "hello running", func() bool { return m.Status("hello") == StatusRunning })
	if m.Status("other") != StatusStopped {
		t.Fatal("disabled addon started")
	}
}

// TestManager_StartAllReconcilesInterruptedUpdate: the server died after an
// update recorded the new version but before the old one was deleted.
func TestManager_StartAllReconcilesInterruptedUpdate(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", false)
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	m.Sup.Stop("hello")
	in, err := m.Store.Get(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	// a leftover old version: record 2.0.0 as current, 1.0.0 as previous
	writeLocal(t, dev, "hello", "2.0.0", "ok", false)
	if err := os.CopyFS(filepath.Join(m.Sup.Dir, "hello", "2.0.0"), os.DirFS(filepath.Join(m.Sup.Dir, "hello", "1.0.0"))); err != nil {
		t.Fatal(err)
	}
	in.PrevVersion, in.Version, in.Manifest.Version = "1.0.0", "2.0.0", "2.0.0"
	if err := m.Store.Save(ctx, in); err != nil {
		t.Fatal(err)
	}
	m.StartAll(ctx)
	waitFor(t, 5*time.Second, "hello running", func() bool { return m.Status("hello") == StatusRunning })
	if _, err := os.Stat(filepath.Join(m.Sup.Dir, "hello", "1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("previous version dir still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.Sup.Dir, "hello", "2.0.0")); err != nil {
		t.Fatalf("current version dir: %v", err)
	}
	if got, _ := m.Store.Get(ctx, "hello"); got.PrevVersion != "" || got.Version != "2.0.0" {
		t.Fatalf("record = %+v", got)
	}
}

// TestStore_SaveKeepsToken: an update's record leaves the running
// process's token valid.
func TestStore_SaveKeepsToken(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: openTestDB(t)}
	in := sample("hello")
	if err := s.Put(ctx, in, "tok"); err != nil {
		t.Fatal(err)
	}
	in.Version, in.PrevVersion, in.Manifest.Version = "2.0.0", "1.0.0", "2.0.0"
	if err := s.Save(ctx, in); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, "hello"); got.Version != "2.0.0" || got.PrevVersion != "1.0.0" {
		t.Fatalf("saved = %+v", got)
	}
	if id, err := s.IDForToken(ctx, "tok"); err != nil || id != "hello" {
		t.Fatalf("token after Save: %q, %v", id, err)
	}
	if err := s.Save(ctx, sample("nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Save of a missing addon: %v", err)
	}
}

func TestEventType(t *testing.T) {
	for name, want := range map[string]string{"ping": "addon.hello.ping", "admin.sync-done": "addon.hello.admin.sync-done", "9x": "addon.hello.9x"} {
		if got, err := EventType("hello", name); err != nil || got != want {
			t.Errorf("%q: %q, %v", name, got, err)
		}
	}
	for _, bad := range []string{"", "Ping", ".x", "-x", "a b", strings.Repeat("a", 41), "a/b"} {
		if _, err := EventType("hello", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSemverLess(t *testing.T) {
	for _, c := range [][2]string{{"1.0.0", "1.0.1"}, {"1.9.0", "1.10.0"}, {"1.0.0-rc.1", "1.0.0"}, {"1.0.0-alpha", "1.0.0-beta"},
		{"1.0.0-rc.2", "1.0.0-rc.10"}, {"0.9.9", "1.0.0"}} {
		if !semverLess(c[0], c[1]) || semverLess(c[1], c[0]) {
			t.Errorf("%s < %s wrong", c[0], c[1])
		}
	}
	if semverLess("1.0.0", "1.0.0") || semverLess("1.0.0+a", "1.0.0+b") {
		t.Error("equal versions ordered")
	}
}

// TestInstall_PrefixOverlap: fake (ANTRESOL_FAKE_) and fake-x
// (ANTRESOL_FAKE_X_) cannot be installed together, in either order.
func TestInstall_PrefixOverlap(t *testing.T) {
	for _, order := range [][2]string{{"fake", "fake-x"}, {"fake-x", "fake"}} {
		t.Run(order[0]+"_then_"+order[1], func(t *testing.T) {
			m, dev := newManager(t)
			ctx := context.Background()
			writeLocal(t, dev, order[0], "1.0.0", "ok", false)
			writeLocal(t, dev, order[1], "1.0.0", "ok", false)
			if _, err := m.Install(ctx, "local:"+order[0], ""); err != nil {
				t.Fatal(err)
			}
			_, err := m.Install(ctx, "local:"+order[1], "")
			if !errors.Is(err, ErrPrefixOverlap) {
				t.Fatalf("second install: %v", err)
			}
			msg, ok := UserMessage(err)
			if want := "Расширение «" + order[1] + "» мешает «" + order[0] + "»: их переменные окружения пересекаются"; !ok || msg != want {
				t.Fatalf("message = %q, %v", msg, ok)
			}
			if _, err := m.Store.Get(ctx, order[1]); !errors.Is(err, ErrNotFound) {
				t.Fatalf("store row of the refused addon: %v", err)
			}
			if _, err := os.Stat(filepath.Join(m.Sup.Dir, order[1])); !os.IsNotExist(err) {
				t.Fatalf("files of the refused addon: %v", err)
			}
			// the installed one is untouched, and its update is not an overlap
			if st := m.Status(order[0]); st != StatusRunning {
				t.Fatalf("%s status = %q", order[0], st)
			}
			writeLocal(t, dev, order[0], "1.0.1", "ok", false)
			if _, err := m.Install(ctx, "local:"+order[0], ""); err != nil {
				t.Fatalf("update: %v", err)
			}
			// fakex (ANTRESOL_FAKEX_) is no overlap of fake
			writeLocal(t, dev, "fakex", "1.0.0", "ok", false)
			if _, err := m.Install(ctx, "local:fakex", ""); err != nil {
				t.Fatalf("fakex: %v", err)
			}
		})
	}
}

func TestManager_RemoveCallsOnRemove(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	var got []string
	m.OnRemove = func(_ context.Context, id string) { got = append(got, id) }
	writeLocal(t, dev, "hello", "1.0.0", "ok", true)
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(ctx, "missing", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := m.Remove(ctx, "hello", false); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("OnRemove calls %v", got)
	}
}

// TestInstall_ScanKindCollision (E2): a scan kind another installed addon
// declares, or library, is refused with its message, and the refused addon
// leaves nothing behind.
func TestInstall_ScanKindCollision(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocalHooks(t, dev, "one", "1.0.0", "ok", false, []string{"tracking.scan:alpha"})
	if _, err := m.Install(ctx, "local:one", ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, kind string }{{"two", "alpha"}, {"four", "library"}} {
		writeLocalHooks(t, dev, c.id, "1.0.0", "ok", false, []string{"tracking.scan:beta", "tracking.scan:" + c.kind})
		_, err := m.Install(ctx, "local:"+c.id, "")
		if !errors.Is(err, ErrScanKindTaken) {
			t.Fatalf("%s: err = %v", c.id, err)
		}
		if msg, ok := UserMessage(err); !ok || msg != "Вид источника «"+c.kind+"» уже занят" {
			t.Fatalf("%s: message %q %v", c.id, msg, ok)
		}
		if _, err := m.Store.Get(ctx, c.id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: store row: %v", c.id, err)
		}
		if _, err := os.Stat(filepath.Join(m.Sup.Dir, c.id)); !os.IsNotExist(err) {
			t.Fatalf("%s: files: %v", c.id, err)
		}
	}
	// a free kind installs; the first addon's own update keeps its kind
	writeLocalHooks(t, dev, "five", "1.0.0", "ok", false, []string{"tracking.scan:beta"})
	if _, err := m.Install(ctx, "local:five", ""); err != nil {
		t.Fatalf("five: %v", err)
	}
	writeLocalHooks(t, dev, "one", "1.0.1", "ok", false, []string{"tracking.scan:alpha"})
	if _, err := m.Update(ctx, "one", ""); err != nil {
		t.Fatalf("own update: %v", err)
	}
	// a disabled addon's kind is still taken (its rows stay listed)
	if err := m.SetEnabled(ctx, "one", false); err != nil {
		t.Fatal(err)
	}
	writeLocalHooks(t, dev, "six", "1.0.0", "ok", false, []string{"tracking.scan:alpha"})
	if _, err := m.Install(ctx, "local:six", ""); !errors.Is(err, ErrScanKindTaken) {
		t.Fatalf("six over a disabled addon: %v", err)
	}
}

// TestInstall_OnlineKindAllowedOnce (D12): `online` is no longer the core's;
// the first addon to declare it installs (and keeps it over an update), a
// second one declaring it is refused as for any other kind, on or off.
func TestInstall_OnlineKindAllowedOnce(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocalHooks(t, dev, "one", "1.0.0", "ok", false, []string{"tracking.scan:online"})
	if _, err := m.Install(ctx, "local:one", ""); err != nil {
		t.Fatalf("the first addon with online: %v", err)
	}
	writeLocalHooks(t, dev, "two", "1.0.0", "ok", false, []string{"tracking.scan:beta", "tracking.scan:online"})
	_, err := m.Install(ctx, "local:two", "")
	if !errors.Is(err, ErrScanKindTaken) {
		t.Fatalf("a second addon with online: %v", err)
	}
	if msg, ok := UserMessage(err); !ok || msg != "Вид источника «online» уже занят" {
		t.Fatalf("message %q %v", msg, ok)
	}
	if _, err := m.Store.Get(ctx, "two"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("store row: %v", err)
	}
	// its own update keeps the kind; turned off, the kind is still taken
	writeLocalHooks(t, dev, "one", "1.0.1", "ok", false, []string{"tracking.scan:online"})
	if _, err := m.Update(ctx, "one", ""); err != nil {
		t.Fatalf("own update: %v", err)
	}
	if err := m.SetEnabled(ctx, "one", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(ctx, "local:two", ""); !errors.Is(err, ErrScanKindTaken) {
		t.Fatalf("second over a disabled addon: %v", err)
	}
}

// TestUpdate_ScanKindCollision (E2, R3): an update that newly declares a
// kind another addon has is refused; the running version stays, with its
// files and its record.
func TestUpdate_ScanKindCollision(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocalHooks(t, dev, "one", "1.0.0", "ok", false, []string{"tracking.scan:alpha"})
	writeLocalHooks(t, dev, "two", "1.0.0", "ok", false, []string{"tracking.scan:beta"})
	for _, id := range []string{"one", "two"} {
		if _, err := m.Install(ctx, "local:"+id, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"alpha", "library"} {
		writeLocalHooks(t, dev, "two", "1.1.0", "ok", false, []string{"tracking.scan:beta", "tracking.scan:" + kind})
		_, err := m.Update(ctx, "two", "")
		if !errors.Is(err, ErrScanKindTaken) {
			t.Fatalf("%s: err = %v", kind, err)
		}
		in, err := m.Store.Get(ctx, "two")
		if err != nil || in.Version != "1.0.0" || !slices.Equal(in.Manifest.Hooks, []string{"tracking.scan:beta"}) {
			t.Fatalf("%s: record %+v %v", kind, in, err)
		}
		if st := m.Status("two"); st != StatusRunning {
			t.Fatalf("%s: status %q", kind, st)
		}
		if _, err := os.Stat(filepath.Join(m.Sup.Dir, "two", "1.0.0")); err != nil {
			t.Fatalf("%s: running version's files: %v", kind, err)
		}
		if _, err := os.Stat(filepath.Join(m.Sup.Dir, "two", "1.1.0")); !os.IsNotExist(err) {
			t.Fatalf("%s: refused version's files: %v", kind, err)
		}
	}
}
