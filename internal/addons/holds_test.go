package addons

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/files"
)

func holdsFixture(t *testing.T, installed ...string) *Holds {
	t.Helper()
	return &Holds{
		Refs:  &RefStore{DB: openTestDB(t)},
		Roots: []string{"/lib", "/dl/"},
		Installed: func(context.Context) (map[string]string, error) {
			out := map[string]string{}
			for _, id := range installed {
				out[id] = "Addon " + id
			}
			return out, nil
		},
	}
}

// show: a hold of two files below /dl/show, the second unselected.
func showHold() addonkit.Hold {
	return addonkit.Hold{Key: "show", Root: "/dl/show", Active: true, Files: []addonkit.HoldFile{
		{Path: "/dl/show/s1/e1.mkv", Size: 1, Selected: true},
		{Path: "/dl/show/s1/e2.mkv", Size: 1},
	}}
}

func TestHolds_Lookup(t *testing.T) {
	h := holdsFixture(t, "a")
	ctx := context.Background()
	if err := h.Put(ctx, "a", []addonkit.Hold{showHold()}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path     string
		selected bool
		ok       bool
	}{
		{"/dl/show/s1/e1.mkv", true, true},
		{"/dl/show/s1/e2.mkv", false, true}, // unselected: owner only
		{"/dl/show/s1", true, true},
		{"/dl/show/s1/", true, true}, // cleaned
		{"/dl/show", true, true},     // the root
		{"/dl", false, false},        // above the root
		{"/dl/show/other.mkv", false, false},
	} {
		id, hold, sel, ok := h.Lookup(c.path)
		if ok != c.ok || sel != c.selected {
			t.Fatalf("%s: ok %v sel %v", c.path, ok, sel)
		}
		if ok && (id != "a" || hold == nil || hold.Key != "show") {
			t.Fatalf("%s: %q %+v", c.path, id, hold)
		}
		if !ok && (id != "" || hold != nil) {
			t.Fatalf("%s: miss carries %q %+v", c.path, id, hold)
		}
	}
}

func TestHolds_LookupBetter(t *testing.T) {
	h := holdsFixture(t, "a", "b")
	ctx := context.Background()
	// a: the file unselected, active; b: the same file selected, inactive
	if err := h.Put(ctx, "a", []addonkit.Hold{{Key: "x", Root: "/lib/m", Active: true,
		Files: []addonkit.HoldFile{{Path: "/lib/m/f.mkv"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := h.Put(ctx, "b", []addonkit.Hold{{Key: "y", Root: "/lib/m",
		Files: []addonkit.HoldFile{{Path: "/lib/m/f.mkv", Selected: true}}}}); err != nil {
		t.Fatal(err)
	}
	if id, hold, sel, ok := h.Lookup("/lib/m/f.mkv"); !ok || id != "b" || hold.Key != "y" || !sel {
		t.Fatalf("selected should win: %q %+v %v %v", id, hold, sel, ok)
	}
	// both selected: the active one wins
	if err := h.Put(ctx, "a", []addonkit.Hold{{Key: "x", Root: "/lib/m", Active: true,
		Files: []addonkit.HoldFile{{Path: "/lib/m/f.mkv", Selected: true}}}}); err != nil {
		t.Fatal(err)
	}
	if id, _, _, _ := h.Lookup("/lib/m/f.mkv"); id != "a" {
		t.Fatalf("active should win: %q", id)
	}
	if id, _, _, _ := h.Lookup("/lib/m"); id != "a" {
		t.Fatalf("active should win on the folder: %q", id)
	}
}

func TestHolds_Refusals(t *testing.T) {
	h := holdsFixture(t, "a")
	ctx := context.Background()
	if err := h.Put(ctx, "a", []addonkit.Hold{showHold()}); err != nil {
		t.Fatal(err)
	}
	one := func(f func(*addonkit.Hold)) []addonkit.Hold {
		x := showHold()
		x.Files = append([]addonkit.HoldFile(nil), x.Files...)
		f(&x)
		return []addonkit.Hold{x}
	}
	many := make([]addonkit.Hold, MaxHolds+1)
	for i := range many {
		many[i] = addonkit.Hold{Key: fmt.Sprint("k", i), Root: "/lib/x"}
	}
	files := make([]addonkit.HoldFile, MaxHoldFiles+1)
	for i := range files {
		files[i] = addonkit.HoldFile{Path: fmt.Sprintf("/lib/x/%d", i)}
	}
	for name, set := range map[string][]addonkit.Hold{
		"relative root":   one(func(x *addonkit.Hold) { x.Root = "dl/show" }),
		"unclean root":    one(func(x *addonkit.Hold) { x.Root = "/dl/show/../show" }),
		"root outside":    one(func(x *addonkit.Hold) { x.Root = "/etc" }),
		"root slash":      one(func(x *addonkit.Hold) { x.Root = "/" }),
		"root prefix":     one(func(x *addonkit.Hold) { x.Root = "/library" }),
		"file outside":    one(func(x *addonkit.Hold) { x.Files[1].Path = "/dl/other/e2.mkv" }),
		"file relative":   one(func(x *addonkit.Hold) { x.Files[1].Path = "e2.mkv" }),
		"file unclean":    one(func(x *addonkit.Hold) { x.Files[1].Path = "/dl/show/s1/../../x" }),
		"empty key":       one(func(x *addonkit.Hold) { x.Key = "" }),
		"long key":        one(func(x *addonkit.Hold) { x.Key = strings.Repeat("a", 129) }),
		"unprintable key": one(func(x *addonkit.Hold) { x.Key = "a\nb" }),
		"duplicate key":   append(one(func(*addonkit.Hold) {}), showHold()),
		"too many holds":  many,
		"too many files":  {{Key: "x", Root: "/lib/x", Files: files}},
		"listed root":     {{Key: "r", Root: "/lib", Files: []addonkit.HoldFile{{Path: "/lib"}}}},
		"listed root /":   {{Key: "r", Root: "/dl", Files: []addonkit.HoldFile{{Path: "/dl"}}}},
	} {
		err := h.Put(ctx, "a", set)
		if !errors.Is(err, ErrBadHold) {
			t.Fatalf("%s: err %v", name, err)
		}
		if _, hold, _, ok := h.Lookup("/dl/show/s1/e1.mkv"); !ok || hold.Key != "show" {
			t.Fatalf("%s: the previous set is gone", name)
		}
		// nor is it replaced in the database
		h2 := holdsFixture(t, "a")
		h2.Refs = h.Refs
		if err := h2.Load(ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, _, ok := h2.Lookup("/dl/show/s1/e2.mkv"); !ok {
			t.Fatalf("%s: the saved set changed", name)
		}
	}
	if err := h.Put(ctx, "", []addonkit.Hold{showHold()}); !errors.Is(err, ErrNoAddon) {
		t.Fatalf("no addon: %v", err)
	}
	// a 128-character key, a file-browser root as a hold's root (its listed
	// paths below it) and a file equal to its root are fine
	if err := h.Put(ctx, "a", []addonkit.Hold{{Key: strings.Repeat("a", 128), Root: "/lib/k"}, {Key: "r", Root: "/lib",
		Files: []addonkit.HoldFile{{Path: "/lib/r.mkv"}}}, {Key: "one", Root: "/dl/one.mkv",
		Files: []addonkit.HoldFile{{Path: "/dl/one.mkv", Selected: true}}}}); err != nil {
		t.Fatal(err)
	}
	if _, hold, sel, ok := h.Lookup("/dl/one.mkv"); !ok || !sel || hold.Key != "one" {
		t.Fatalf("single-file hold: %+v %v %v", hold, sel, ok)
	}
}

func TestHolds_Touching(t *testing.T) {
	h := holdsFixture(t, "a", "b")
	ctx := context.Background()
	if err := h.Put(ctx, "a", []addonkit.Hold{showHold(), {Key: "film", Root: "/dl/films/f",
		Files: []addonkit.HoldFile{{Path: "/dl/films/f/f.mkv", Selected: true}}}}); err != nil {
		t.Fatal(err)
	}
	if err := h.Put(ctx, "b", []addonkit.Hold{{Key: "z", Root: "/dl/films/g",
		Files: []addonkit.HoldFile{{Path: "/dl/films/g/g.mkv"}}}}); err != nil {
		t.Fatal(err)
	}
	for from, want := range map[string]map[string][]string{
		"/dl/films":          {"a": {"film"}, "b": {"z"}}, // containing two holds of two addons
		"/dl":                {"a": {"film", "show"}, "b": {"z"}},
		"/dl/show":           {"a": {"show"}}, // at the root
		"/dl/show/s1/e2.mkv": {"a": {"show"}}, // inside, unselected
		"/dl/show/s1/":       {"a": {"show"}},
		"/dl/show/notes.txt": {}, // an unlisted file below the root
		"/lib":               {},
		"/dl/films/g/g.mkv":  {"b": {"z"}},
	} {
		got := h.Touching(from)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v, want %v", from, got, want)
		}
	}
}

func TestHolds_LoadAndDrop(t *testing.T) {
	h := holdsFixture(t, "a", "b")
	ctx := context.Background()
	if err := h.Put(ctx, "a", []addonkit.Hold{showHold()}); err != nil {
		t.Fatal(err)
	}
	if err := h.Put(ctx, "b", []addonkit.Hold{{Key: "z", Root: "/lib/z",
		Files: []addonkit.HoldFile{{Path: "/lib/z/z.mkv", Selected: true}}}}); err != nil {
		t.Fatal(err)
	}
	// a new Holds on the same database
	h2 := holdsFixture(t, "a", "b")
	h2.Refs = h.Refs
	if err := h2.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if id, hold, sel, ok := h2.Lookup("/dl/show/s1/e1.mkv"); !ok || id != "a" || !sel || hold.Key != "show" || len(hold.Files) != 2 {
		t.Fatalf("loaded: %q %+v %v %v", id, hold, sel, ok)
	}
	if id, _, _, ok := h2.Lookup("/lib/z"); !ok || id != "b" {
		t.Fatalf("loaded b: %q %v", id, ok)
	}
	// b no longer installed: its holds are ignored
	h3 := holdsFixture(t, "a")
	h3.Refs = h.Refs
	if err := h3.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := h3.Lookup("/lib/z/z.mkv"); ok {
		t.Fatal("holds of an addon not installed are loaded")
	}
	if _, _, _, ok := h3.Lookup("/dl/show"); !ok {
		t.Fatal("a's holds are not loaded")
	}
	// Installed failing fails Load and keeps the index
	h3.Installed = func(context.Context) (map[string]string, error) { return nil, errors.New("boom") }
	if err := h3.Load(ctx); err == nil {
		t.Fatal("Load ignored the Installed error")
	}
	if _, _, _, ok := h3.Lookup("/dl/show"); !ok {
		t.Fatal("a failed Load cleared the index")
	}
	// Drop forgets the addon's holds, in memory and in the database
	if err := h2.Drop(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := h2.Lookup("/dl/show/s1/e1.mkv"); ok {
		t.Fatal("dropped hold still found")
	}
	if len(h2.Touching("/dl")) != 0 {
		t.Fatal("dropped hold still touching")
	}
	if _, _, _, ok := h2.Lookup("/lib/z"); !ok {
		t.Fatal("Drop took another addon's holds")
	}
	h4 := holdsFixture(t, "a", "b")
	h4.Refs = h.Refs
	if err := h4.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := h4.Lookup("/dl/show"); ok {
		t.Fatal("dropped holds are still saved")
	}
	// an empty set clears too
	if err := h4.Put(ctx, "b", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := h4.Lookup("/lib/z"); ok {
		t.Fatal("an empty set kept the holds")
	}
}

// The index keeps its own copy: the caller's slices may change after Put.
func TestHolds_PutCopies(t *testing.T) {
	h := holdsFixture(t, "a")
	set := []addonkit.Hold{showHold()}
	if err := h.Put(context.Background(), "a", set); err != nil {
		t.Fatal(err)
	}
	set[0].Key = "changed"
	set[0].Files[0].Path = "/elsewhere"
	if _, hold, _, ok := h.Lookup("/dl/show/s1/e1.mkv"); !ok || hold.Key != "show" || hold.Files[0].Path != "/dl/show/s1/e1.mkv" {
		t.Fatalf("aliased: %+v %v", hold, ok)
	}
}

// Load keeps the valid holds of a saved set and skips (and logs) only the
// bad ones: a root that left the file browser, an unreadable entry, a key
// twice.
func TestHolds_LoadSkipsBadHolds(t *testing.T) {
	h := holdsFixture(t, "a", "b")
	ctx := context.Background()
	raw := `[{"key":"show","root":"/dl/show","files":[{"path":"/dl/show/e.mkv","selected":true}]},
	  {"key":"gone","root":"/old/x","files":[{"path":"/old/x/f.mkv"}]},
	  "not a hold",
	  {"key":"show","root":"/lib/dup","files":[{"path":"/lib/dup/f.mkv"}]},
	  {"key":"film","root":"/lib/f","files":[{"path":"/lib/f/f.mkv"}]}]`
	if err := h.Refs.SaveHolds(ctx, "a", []byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := h.Refs.SaveHolds(ctx, "b", []byte(`{broken`)); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	h.Log = slog.New(slog.NewTextHandler(&buf, nil))
	if err := h.Load(ctx); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/dl/show/e.mkv", "/lib/f/f.mkv"} {
		if _, _, _, ok := h.Lookup(p); !ok {
			t.Fatalf("valid hold at %s dropped", p)
		}
	}
	for _, p := range []string{"/old/x/f.mkv", "/lib/dup/f.mkv"} {
		if _, _, _, ok := h.Lookup(p); ok {
			t.Fatalf("bad hold at %s loaded", p)
		}
	}
	log := buf.String()
	for _, want := range []string{"gone", "addon=a", "addon=b"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log misses %q:\n%s", want, log)
		}
	}
	if strings.Count(log, "addon=a") != 3 {
		t.Fatalf("want three skipped holds of a:\n%s", log)
	}
}

func TestHolds_LoadNeedsInstalled(t *testing.T) {
	h := holdsFixture(t)
	h.Installed = nil
	if err := h.Load(context.Background()); err == nil {
		t.Fatal("Load without Installed succeeded")
	}
}

// A listed path that is a folder covers everything below it.
func TestHolds_ListedFolderCoversBelow(t *testing.T) {
	h := holdsFixture(t, "a", "b")
	ctx := context.Background()
	if err := h.Put(ctx, "a", []addonkit.Hold{{Key: "pack", Root: "/dl/pack", Active: true,
		Files: []addonkit.HoldFile{{Path: "/dl/pack/disc", Selected: true}, {Path: "/dl/pack/extra"}}}}); err != nil {
		t.Fatal(err)
	}
	if id, hold, sel, ok := h.Lookup("/dl/pack/disc/v/f.vob"); !ok || id != "a" || hold.Key != "pack" || !sel {
		t.Fatalf("below a listed folder: %q %+v %v %v", id, hold, sel, ok)
	}
	if _, _, sel, ok := h.Lookup("/dl/pack/extra/x.txt"); !ok || sel {
		t.Fatalf("below an unselected listed folder: %v %v", sel, ok)
	}
	if _, _, _, ok := h.Lookup("/dl/pack/other"); ok {
		t.Fatal("an unlisted sibling is held")
	}
	if _, _, _, ok := h.Lookup("/dl/packed/disc/f"); ok {
		t.Fatal("a name prefix is held")
	}
	if got := h.Touching("/dl/pack/disc/v"); !reflect.DeepEqual(got, map[string][]string{"a": {"pack"}}) {
		t.Fatalf("touching below a listed folder: %v", got)
	}
	if got := h.Touching("/dl/pack/other"); len(got) != 0 {
		t.Fatalf("touching an unlisted sibling: %v", got)
	}
	// a selected file of another hold below the folder still wins over the unselected folder
	if err := h.Put(ctx, "b", []addonkit.Hold{{Key: "x", Root: "/dl/pack/extra",
		Files: []addonkit.HoldFile{{Path: "/dl/pack/extra/x.txt", Selected: true}}}}); err != nil {
		t.Fatal(err)
	}
	if id, _, sel, _ := h.Lookup("/dl/pack/extra/x.txt"); id != "b" || !sel {
		t.Fatalf("better entry lost: %q %v", id, sel)
	}
}

// aliasEnv: a file browser root R with a real folder R/Pack (a.mkv) and a
// link R/dl/link → R/Pack, the root itself reached through a symlink.
func aliasEnv(t *testing.T) (h *Holds, real, link string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := filepath.Join(base, "R")
	for _, d := range []string{filepath.Join(r, "Pack"), filepath.Join(r, "dl")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r, "Pack", "a.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(r, "Pack"), filepath.Join(r, "dl", "link")); err != nil {
		t.Fatal(err)
	}
	h = holdsFixture(t, "a")
	h.Roots = []string{r}
	return h, filepath.Join(r, "Pack"), filepath.Join(r, "dl", "link")
}

func TestHolds_ListedThroughAlias(t *testing.T) {
	h, real, link := aliasEnv(t)
	err := h.Put(context.Background(), "a", []addonkit.Hold{{Key: "k", Root: link,
		Files: []addonkit.HoldFile{{Path: filepath.Join(link, "a.mkv"), Selected: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, hold, _, ok := h.Lookup(filepath.Join(real, "a.mkv")); !ok || hold.Key != "k" {
		t.Fatalf("real path not held: %v", ok)
	}
	if got := h.Touching(real); !reflect.DeepEqual(got, map[string][]string{"a": {"k"}}) {
		t.Fatalf("a move of the real folder touches %v", got)
	}
	if hold, _ := h.Get("a", "k"); hold.Root != real || hold.Files[0].Path != filepath.Join(real, "a.mkv") {
		t.Fatalf("stored %+v, want the real paths", hold)
	}
}

// A hold listed by its real path stays refused when reached by the link
// (the file layer resolves the link first).
func TestHolds_ListedRealMovedThroughAlias(t *testing.T) {
	e := newFHEnv(t)
	hookAddon(t, e.tg, "ad", `{`+allFileHooks+`}`, fileHookNames, false)
	root, a, _, h := e.pack()
	e.put("ad", h)
	if err := os.Symlink(root, filepath.Join(e.dl, "link")); err != nil {
		t.Fatal(err)
	}
	err := e.svc.Ops.Rename(context.Background(), "dl", "link/a.mkv", "z.mkv", true)
	if !errors.Is(err, files.ErrHeldAlias) || !there(a) {
		t.Fatalf("%v", err)
	}
}

func TestHolds_AliasOutOfRootsRefused(t *testing.T) {
	h, _, link := aliasEnv(t)
	out, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(filepath.Dir(link), "out")); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(filepath.Dir(link), "out", "x.mkv")
	root := filepath.Dir(link)
	err = h.Put(context.Background(), "a", []addonkit.Hold{{Key: "k", Root: root,
		Files: []addonkit.HoldFile{{Path: p}}}})
	if !errors.Is(err, ErrBadHold) {
		t.Fatalf("%v, want ErrBadHold", err)
	}
	// a listed path that is a root once resolved
	if err := os.Symlink(h.Roots[0], filepath.Join(root, "self")); err != nil {
		t.Fatal(err)
	}
	err = h.Put(context.Background(), "a", []addonkit.Hold{{Key: "k", Root: root,
		Files: []addonkit.HoldFile{{Path: filepath.Join(root, "self")}}}})
	if !errors.Is(err, ErrBadHold) {
		t.Fatalf("root through a link: %v", err)
	}
}

// Under a symlinked file-browser root the hold is stored in the root's own
// spelling, the one the file layer looks up, and a real move, rename or
// trash through the file layer is refused (the addon stopped).
func TestHolds_SymlinkedRootKeepsProtection(t *testing.T) {
	e := newFHEnvRoot(t, true)
	hookAddon(t, e.tg, "ad", `{`+allFileHooks+`}`, fileHookNames, false)
	_, a, _, h := e.pack() // paths under the root as configured (the link)
	e.put("ad", h)
	if _, _, _, ok := e.db.Lookup(a); !ok {
		t.Fatal("hold not found under the root as configured")
	}
	if got, _ := e.db.Get("ad", "k"); got.Root != filepath.Dir(a) || got.Files[0].Path != a {
		t.Fatalf("stored %+v", got)
	}
	ctx := context.Background()
	if err := e.svc.Ops.Rename(ctx, "dl", "Pack/a.mkv", "z.mkv", false); err == nil || !there(a) {
		t.Fatalf("rename of held file: %v", err)
	}
	if err := e.svc.Ops.Move(ctx, "dl", "Pack", "dl", "To", false); err == nil || !there(a) {
		t.Fatalf("move of held folder: %v", err)
	}
	if res, err := e.svc.Ops.Trash(ctx, "dl", []string{"Pack"}, false, 1); (err == nil && len(res.Warnings) == 0) || !there(a) {
		t.Fatalf("trash of held folder: %v %+v", err, res)
	}
	// listed by the real spelling of the root: stored the same
	real, err := filepath.EvalSymlinks(e.dl)
	if err != nil {
		t.Fatal(err)
	}
	e.put("ad", addonkit.Hold{Key: "r", Root: filepath.Join(real, "Pack"),
		Files: []addonkit.HoldFile{{Path: filepath.Join(real, "Pack", "a.mkv")}}})
	if _, hold, _, ok := e.db.Lookup(a); !ok || hold.Key != "r" {
		t.Fatalf("real spelling not mapped to the root's: %v", ok)
	}
}

func TestHolds_SymlinkedRootAccepted(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realR := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(realR, "Pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := filepath.Join(base, "R")
	if err := os.Symlink(realR, r); err != nil {
		t.Fatal(err)
	}
	h := holdsFixture(t, "a")
	h.Roots = []string{r}
	err = h.Put(context.Background(), "a", []addonkit.Hold{{Key: "k", Root: filepath.Join(r, "Pack"),
		Files: []addonkit.HoldFile{{Path: filepath.Join(r, "Pack", "a.mkv")}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := h.Lookup(filepath.Join(r, "Pack", "a.mkv")); !ok {
		t.Fatal("not held under the root as configured")
	}
}

// Listed through an alias folder, the real folder's move, rename and
// trash are refused by the file layer (not only found by Lookup).
func TestHolds_ListedThroughAliasRealOpRefused(t *testing.T) {
	e := newFHEnv(t)
	hookAddon(t, e.tg, "ad", `{`+allFileHooks+`}`, fileHookNames, false)
	root, a, _, h := e.pack()
	link := filepath.Join(e.dl, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	h.Root = link
	h.Files = []addonkit.HoldFile{{Path: filepath.Join(link, "a.mkv"), Size: 4, Selected: true}}
	e.put("ad", h)
	ctx := context.Background()
	if err := e.svc.Ops.Rename(ctx, "dl", "Pack/a.mkv", "z.mkv", false); err == nil || !there(a) {
		t.Fatalf("rename: %v", err)
	}
	if err := e.svc.Ops.Move(ctx, "dl", "Pack", "dl", "To", false); err == nil || !there(a) {
		t.Fatalf("move: %v", err)
	}
	if res, err := e.svc.Ops.Trash(ctx, "dl", []string{"Pack"}, false, 1); (err == nil && len(res.Warnings) == 0) || !there(a) {
		t.Fatalf("trash: %v %+v", err, res)
	}
}

// A root that is a symlink to a mount not up yet (dangling) at the core's
// start keeps its saved holds: Load drops nothing for a missing root, and
// they protect the data once the mount is back.
func TestHolds_LoadKeepsHoldsUnderMissingRoot(t *testing.T) {
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mount := filepath.Join(base, "mnt")
	link := filepath.Join(base, "dl")
	if err := os.MkdirAll(filepath.Join(mount, "Pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mount, link); err != nil {
		t.Fatal(err)
	}
	h := holdsFixture(t, "a")
	h.Roots = []string{link}
	x := addonkit.Hold{Key: "k", Root: filepath.Join(link, "Pack"), Files: []addonkit.HoldFile{{Path: filepath.Join(link, "Pack", "a.mkv"), Selected: true}}}
	if err := h.Put(ctx, "a", []addonkit.Hold{x}); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []func() error{
		func() error { return os.Rename(mount, mount+".away") }, // dangling link
		func() error { return os.Remove(link) },                 // no root at all
	} {
		if err := gone(); err != nil {
			t.Fatal(err)
		}
		h2 := holdsFixture(t, "a")
		h2.Refs, h2.Roots = h.Refs, h.Roots
		if err := h2.Load(ctx); err != nil {
			t.Fatal(err)
		}
		if s := h2.Sets()["a"]; len(s) != 1 || s[0].Key != "k" || s[0].Root != x.Root {
			t.Fatalf("saved holds under a missing root: %+v", s)
		}
	}
	if err := os.Symlink(mount, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(mount+".away", mount); err != nil {
		t.Fatal(err)
	}
	h3 := holdsFixture(t, "a")
	h3.Refs, h3.Roots = h.Refs, h.Roots
	if err := h3.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if _, got, _, ok := h3.Lookup(filepath.Join(link, "Pack", "a.mkv")); !ok || got.Key != "k" {
		t.Fatalf("after the mount: %+v %v", got, ok)
	}
}

// lockedEnv: a file-browser root with a folder the server may not read
// (mode 0) below it. Skipped as root, who reads it anyway.
func lockedEnv(t *testing.T) (root, locked string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-0 folder")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(base, "dl")
	locked = filepath.Join(root, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "Pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	return root, locked
}

// A path that cannot be resolved for another reason than not being there
// (EACCES) is refused by Put, never resolved through a parent: a link
// below the locked folder could point anywhere.
func TestHolds_UnreadablePathRefused(t *testing.T) {
	root, locked := lockedEnv(t)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	h := holdsFixture(t, "a")
	h.Roots = []string{root}
	x := addonkit.Hold{Key: "k", Root: filepath.Join(locked, "Pack"), Files: []addonkit.HoldFile{{Path: filepath.Join(locked, "Pack", "a.mkv"), Selected: true}}}
	if err := h.Put(context.Background(), "a", []addonkit.Hold{x}); !errors.Is(err, ErrBadHold) {
		t.Fatalf("Put = %v, want ErrBadHold", err)
	}
}

// Load keeps a saved hold whose paths it cannot resolve now (EACCES), as
// saved, and logs it; a root it cannot resolve is kept as written, so its
// holds are not lost while the addon is down.
func TestHolds_LoadKeepsUnresolvable(t *testing.T) {
	ctx := context.Background()
	for _, lockRoot := range []bool{false, true} {
		root, locked := lockedEnv(t)
		h := holdsFixture(t, "a")
		h.Roots = []string{root}
		x := addonkit.Hold{Key: "k", Root: filepath.Join(locked, "Pack"), Files: []addonkit.HoldFile{{Path: filepath.Join(locked, "Pack", "a.mkv"), Selected: true}}}
		if err := h.Put(ctx, "a", []addonkit.Hold{x}); err != nil {
			t.Fatal(err)
		}
		dir := locked
		if lockRoot {
			// the root's own parent is unreadable: the root cannot be resolved
			dir = filepath.Dir(root)
			t.Cleanup(func() { os.Chmod(dir, 0o755) })
		}
		if err := os.Chmod(dir, 0); err != nil {
			t.Fatal(err)
		}
		var logged bytes.Buffer
		h2 := holdsFixture(t, "a")
		h2.Refs, h2.Roots, h2.Log = h.Refs, h.Roots, slog.New(slog.NewTextHandler(&logged, nil))
		err := h2.Load(ctx)
		os.Chmod(dir, 0o755)
		if err != nil {
			t.Fatal(err)
		}
		if s := h2.Sets()["a"]; len(s) != 1 || s[0].Root != x.Root || s[0].Files[0].Path != x.Files[0].Path {
			t.Fatalf("lockRoot=%v: saved hold after Load: %+v (log %s)", lockRoot, s, logged.String())
		}
		if !lockRoot && !strings.Contains(logged.String(), "kept as saved") {
			t.Fatalf("not logged: %s", logged.String())
		}
	}
}
