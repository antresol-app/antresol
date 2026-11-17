package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/library"
)

// coreHolds is the addon env with a file browser over dl (and a symlink
// out of it), its config wrapped by the hold index; the addon "hd" has
// files.holds.
type coreHolds struct {
	*coreWrite
	holds       *addons.Holds
	dl, outside string
	root        string // the file browser's root as configured (dl, or a link to it)
}

func newCoreHolds(t *testing.T) *coreHolds { return newCoreHoldsOver(t, false) }

// newCoreHoldsOver: linked configures the file browser's root as
// <base>/link, a symlink to dl.
func newCoreHoldsOver(t *testing.T, linked bool) *coreHolds {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &coreHolds{dl: filepath.Join(base, "dl"), outside: filepath.Join(base, "outside")}
	for _, d := range []string{c.dl, c.outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(c.dl, "Pack", "a.mkv"))
	write(t, filepath.Join(c.outside, "x.mkv"))
	if err := os.Symlink(c.outside, filepath.Join(c.dl, "escape")); err != nil {
		t.Fatal(err)
	}
	c.root = c.dl
	if linked {
		c.root = filepath.Join(base, "link")
		if err := os.Symlink(c.dl, c.root); err != nil {
			t.Fatal(err)
		}
	}
	cw := &coreWrite{}
	cw.addonEnv = newAddonEnv(t, func(d *Deps) {
		c.holds = &addons.Holds{Refs: &addons.RefStore{DB: d.DB}, Roots: []string{c.root},
			Installed: func(context.Context) (map[string]string, error) {
				return map[string]string{"hd": "Держатель"}, nil
			}}
		fc := files.ServiceConfig{Sandbox: files.NewSandbox(base, []files.Root{{ID: "dl", Title: "dl", Dir: c.root}}),
			DB: d.DB, Library: library.NewRepo(d.DB)}
		c.holds.Wrap(&fc)
		d.Files, d.AddonHolds = files.NewService(fc), c.holds
	})
	c.coreWrite = cw
	hello := c.fake.installed["hello"]
	c.fake.mu.Lock()
	in := hello
	in.ID, in.Manifest.ID, in.Manifest.Permissions = "hd", "hd", []string{"files.holds"}
	in.Manifest.Hooks = []string{"files.plan", "files.pause", "files.resume", "files.stop"}
	c.fake.installed["hd"], c.fake.tokens["tok-hd"] = in, "hd"
	mute := in // holds files but answers no files hook
	mute.ID, mute.Manifest.ID, mute.Manifest.Hooks = "mute", "mute", []string{"files.plan"}
	c.fake.installed["mute"], c.fake.tokens["tok-mute"] = mute, "mute"
	c.fake.mu.Unlock()
	return c
}

func (c *coreHolds) hold(key, root string, paths ...string) addonkit.Hold {
	h := addonkit.Hold{Key: key, Root: root, Active: true, Label: addonkit.Text{RU: "Используется", EN: "In use"}}
	for _, p := range paths {
		h.Files = append(h.Files, addonkit.HoldFile{Path: p, Selected: true})
	}
	return h
}

func TestAddonCoreHolds_Permission(t *testing.T) {
	c := newCoreHolds(t)
	pack := filepath.Join(c.dl, "Pack")
	body := map[string]any{"holds": []addonkit.Hold{c.hold("k", pack, filepath.Join(pack, "a.mkv"))}}
	if s := c.call("PUT", "tok-hello", "/files/holds", body, 403, nil); !strings.Contains(s, "нет такого разрешения") {
		t.Fatalf("403 body %s", s)
	}
	if _, _, _, ok := c.holds.Lookup(filepath.Join(pack, "a.mkv")); ok {
		t.Fatal("put without the permission")
	}
}

// An addon that cannot be asked before its files move may not hold any.
func TestAddonCoreHolds_NeedsHooks(t *testing.T) {
	c := newCoreHolds(t)
	pack := filepath.Join(c.dl, "Pack")
	body := map[string]any{"holds": []addonkit.Hold{c.hold("k", pack, filepath.Join(pack, "a.mkv"))}}
	if s := c.call("PUT", "tok-mute", "/files/holds", body, 400, nil); !strings.Contains(s, "files.pause") {
		t.Fatalf("400 body %s", s)
	}
	c.call("PUT", "tok-mute", "/files/holds", map[string]any{"holds": []addonkit.Hold{}}, 204, nil) // clearing is fine
	if _, _, _, ok := c.holds.Lookup(filepath.Join(pack, "a.mkv")); ok {
		t.Fatal("held without the hooks")
	}
}

func TestAddonCoreHolds_PutAndList(t *testing.T) {
	c := newCoreHolds(t)
	pack := filepath.Join(c.dl, "Pack")
	a := filepath.Join(pack, "a.mkv")
	later := filepath.Join(c.dl, "Later", "b.mkv") // not on disk yet: fine
	good := []addonkit.Hold{c.hold("k", pack, a), c.hold("later", filepath.Dir(later), later)}
	c.call("PUT", "tok-hd", "/files/holds", map[string]any{"holds": good}, 204, nil)
	if err := os.Symlink(c.dl, filepath.Join(c.dl, "self")); err != nil {
		t.Fatal(err)
	}

	for name, holds := range map[string]any{
		"no holds":          nil,
		"relative root":     []addonkit.Hold{c.hold("x", "dl/Pack")},
		"root outside":      []addonkit.Hold{c.hold("x", c.outside, filepath.Join(c.outside, "x.mkv"))},
		"file outside root": []addonkit.Hold{c.hold("x", pack, filepath.Join(c.dl, "Other.mkv"))},
		"dot dot":           []addonkit.Hold{c.hold("x", pack, pack+"/../Pack/a.mkv")},
		"symlinked root":    []addonkit.Hold{c.hold("x", filepath.Join(c.dl, "escape"), filepath.Join(c.dl, "escape", "x.mkv"))},
		"symlinked file":    []addonkit.Hold{c.hold("x", c.dl, filepath.Join(c.dl, "escape", "x.mkv"))},
		"key twice":         []addonkit.Hold{c.hold("x", pack, a), c.hold("x", pack, a)},
		"empty key":         []addonkit.Hold{c.hold("", pack, a)},
		"listed fb root":    []addonkit.Hold{c.hold("x", c.dl, c.dl)},
		"listed via link":   []addonkit.Hold{c.hold("x", c.dl, filepath.Join(c.dl, "self"))},
	} {
		body := map[string]any{}
		if holds != nil {
			body["holds"] = holds
		}
		c.call("PUT", "tok-hd", "/files/holds", body, 400, nil)
		if id, x, _, ok := c.holds.Lookup(a); !ok || id != "hd" || x.Key != "k" {
			t.Fatalf("%s: the previous set changed: %q %+v %v", name, id, x, ok)
		}
	}
	w := c.do("PUT", "/api/addon-core/v1/files/holds", `{"holds": [`, nil, "Authorization", "Bearer tok-hd", "Content-Type", "application/json")
	if w.Code != 400 {
		t.Fatalf("bad JSON: %d", w.Code)
	}

	// an admin's listing shows the hold
	w = c.do("GET", "/api/files/list?root=dl&path=Pack", "", c.admin)
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var l struct {
		Entries []struct {
			Name string
			Hold *files.HoldRef
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Hold == nil || *l.Entries[0].Hold != (files.HoldRef{Addon: "Держатель", Label: "Используется", Active: true}) {
		t.Fatalf("listing %s", w.Body)
	}
	// kids get no file browser at all
	if w := c.do("GET", "/api/files/list?root=dl&path=Pack", "", c.kid); w.Code != 403 {
		t.Fatalf("kid list: %d", w.Code)
	}

	// an empty set clears the addon's holds
	c.call("PUT", "tok-hd", "/files/holds", map[string]any{"holds": []addonkit.Hold{}}, 204, nil)
	if _, _, _, ok := c.holds.Lookup(a); ok {
		t.Fatal("not cleared")
	}
}

// GET /files/holds is the addon's own set as the server keeps it: [] at
// first, the pushed holds after a PUT, never another addon's.
func TestAddonCoreHolds_Get(t *testing.T) {
	c := newCoreHolds(t)
	var got struct {
		Holds []addonkit.Hold `json:"holds"`
	}
	if s := c.call("GET", "tok-hd", "/files/holds", nil, 200, &got); got.Holds == nil || len(got.Holds) != 0 {
		t.Fatalf("empty: %s", s)
	}
	pack := filepath.Join(c.dl, "Pack")
	c.call("PUT", "tok-hd", "/files/holds", map[string]any{"holds": []addonkit.Hold{c.hold("k", pack, filepath.Join(pack, "a.mkv"))}}, 204, nil)
	got.Holds = nil
	c.call("GET", "tok-hd", "/files/holds", nil, 200, &got)
	if len(got.Holds) != 1 || got.Holds[0].Key != "k" || got.Holds[0].Root != pack {
		t.Fatalf("after PUT: %+v", got.Holds)
	}
	got.Holds = nil
	if s := c.call("GET", "tok-mute", "/files/holds", nil, 200, &got); len(got.Holds) != 0 {
		t.Fatalf("another addon's set: %s", s)
	}
	if s := c.call("GET", "tok-hello", "/files/holds", nil, 403, nil); !strings.Contains(s, "нет такого разрешения") {
		t.Fatalf("permission: %s", s)
	}
}

// A file-browser root missing on disk (a media mount not up yet) loses no
// saved hold: the core reloading its holds keeps the ones under it and GET
// answers them; PUT accepts holds under it (they protect the path once it
// is there); a hold escaping the root by its spelling is still refused.
func TestAddonCoreHolds_MissingRootKept(t *testing.T) {
	c := newCoreHolds(t)
	ctx := context.Background()
	pack := filepath.Join(c.dl, "Pack")
	a := filepath.Join(pack, "a.mkv")
	c.call("PUT", "tok-hd", "/files/holds", map[string]any{"holds": []addonkit.Hold{c.hold("k", pack, a)}}, 204, nil)
	// the mount goes: the root is not on disk, and the core restarts
	if err := os.Rename(c.dl, c.dl+".away"); err != nil {
		t.Fatal(err)
	}
	if err := c.holds.Load(ctx); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Holds []addonkit.Hold `json:"holds"`
	}
	c.call("GET", "tok-hd", "/files/holds", nil, 200, &got)
	if len(got.Holds) != 1 || got.Holds[0].Key != "k" || got.Holds[0].Root != pack || len(got.Holds[0].Files) != 1 {
		t.Fatalf("saved holds after a restart with the root missing: %+v", got.Holds)
	}
	// the addon pushes its whole set while the root is missing: accepted
	later := filepath.Join(c.dl, "Later", "b.mkv")
	c.call("PUT", "tok-hd", "/files/holds", map[string]any{"holds": []addonkit.Hold{c.hold("k", pack, a), c.hold("later", filepath.Dir(later), later)}}, 204, nil)
	for name, x := range map[string]addonkit.Hold{
		"file outside root": c.hold("x", pack, filepath.Join(c.dl, "Other.mkv")),
		"dot dot":           c.hold("x", pack, pack+"/../Pack/a.mkv"),
		"listed fb root":    c.hold("x", c.dl, c.dl),
	} {
		if s := c.call("PUT", "tok-hd", "/files/holds", map[string]any{"holds": []addonkit.Hold{x}}, 400, nil); s == "" {
			t.Fatalf("%s accepted", name)
		}
	}
	// the mount is back: the holds protect the data
	if err := os.Rename(c.dl+".away", c.dl); err != nil {
		t.Fatal(err)
	}
	if err := c.holds.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if id, x, _, ok := c.holds.Lookup(a); !ok || id != "hd" || x.Key != "k" {
		t.Fatalf("after the mount: %q %+v %v", id, x, ok)
	}
	if _, x, _, ok := c.holds.Lookup(later); !ok || x.Key != "later" {
		t.Fatalf("the hold pushed while the root was missing: %+v %v", x, ok)
	}
}

// Without a hold index the route answers 503.
func TestAddonCoreHolds_Off(t *testing.T) {
	c := &coreWrite{}
	c.addonEnv = newAddonEnv(t)
	hello := c.fake.installed["hello"]
	c.fake.mu.Lock()
	hello.ID, hello.Manifest.ID, hello.Manifest.Permissions = "hd", "hd", []string{"files.holds"}
	c.fake.installed["hd"], c.fake.tokens["tok-hd"] = hello, "hd"
	c.fake.mu.Unlock()
	c.call("PUT", "tok-hd", "/files/holds", map[string]any{"holds": []addonkit.Hold{}}, 503, nil)
	c.call("GET", "tok-hd", "/files/holds", nil, 503, nil)
}

// The guard checks a hold resolved, as /info.fileRoots and the contract
// give it: under a file-browser root that is a symlink, a hold spelled by
// the real path (or through an alias outside every root that resolves
// inside one) is accepted and stored in the root's spelling.
func TestAddonCoreHolds_ResolvedSpellingAccepted(t *testing.T) {
	c := newCoreHoldsOver(t, true)
	alias := filepath.Join(filepath.Dir(c.dl), "alias")
	if err := os.Symlink(c.dl, alias); err != nil {
		t.Fatal(err)
	}
	real := c.hold("real", filepath.Join(c.dl, "Pack"), filepath.Join(c.dl, "Pack", "a.mkv"))
	viaAlias := c.hold("alias", filepath.Join(alias, "Pack"), filepath.Join(alias, "Pack", "a.mkv"))
	for name, x := range map[string]addonkit.Hold{"real path": real, "alias": viaAlias} {
		c.call("PUT", "tok-hd", "/files/holds", map[string]any{"holds": []addonkit.Hold{x}}, 204, nil)
		var got struct {
			Holds []addonkit.Hold `json:"holds"`
		}
		c.call("GET", "tok-hd", "/files/holds", nil, 200, &got)
		pack := filepath.Join(c.root, "Pack")
		if len(got.Holds) != 1 || got.Holds[0].Root != pack || got.Holds[0].Files[0].Path != filepath.Join(pack, "a.mkv") {
			t.Fatalf("%s: stored %+v, want the root's spelling %s", name, got.Holds, pack)
		}
		w := c.do("GET", "/api/files/list?root=dl&path=Pack", "", c.admin)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Используется") {
			t.Fatalf("%s: the file browser does not show the hold: %d %s", name, w.Code, w.Body)
		}
	}
	// still refused: outside every root resolved, or a root itself
	for name, x := range map[string]addonkit.Hold{
		"outside":     c.hold("x", c.outside, filepath.Join(c.outside, "x.mkv")),
		"real root":   c.hold("x", c.dl, c.dl),
		"linked root": c.hold("x", c.root, c.root),
		"alias root":  c.hold("x", alias, alias),
	} {
		c.call("PUT", "tok-hd", "/files/holds", map[string]any{"holds": []addonkit.Hold{x}}, 400, nil)
		_ = name
	}
}
