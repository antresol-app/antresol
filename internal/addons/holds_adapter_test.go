package addons

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
)

// innerEngine is the build's own file hooks: per-key calls, recorded.
type innerEngine struct {
	mu     sync.Mutex
	owned  map[string]string   // path → key (HeldInfo and Owner)
	follow map[string][]string // from → keys
	stop   map[string][]string // from → keys
	calls  []string
}

func newInner() *innerEngine {
	return &innerEngine{owned: map[string]string{}, follow: map[string][]string{}, stop: map[string][]string{}}
}

func (e *innerEngine) rec(s string) {
	e.mu.Lock()
	e.calls = append(e.calls, s)
	e.mu.Unlock()
}

func (e *innerEngine) got() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

func (e *innerEngine) HeldInfo(p string) (string, bool, bool) {
	k, ok := e.owned[p]
	return k, true, ok
}

func (e *innerEngine) PlanRelocate(from, to string) ([]string, []string) {
	e.rec("plan " + from)
	return e.follow[from], e.stop[from]
}

func (e *innerEngine) Relocate(_ context.Context, from, to string, rename func() error) error {
	e.rec("relocate " + from)
	return rename()
}

func (e *innerEngine) Stop(_ context.Context, k string) error   { e.rec("stop " + k); return nil }
func (e *innerEngine) Pause(_ context.Context, k string) error  { e.rec("pause " + k); return nil }
func (e *innerEngine) Resume(_ context.Context, k string) error { e.rec("resume " + k); return nil }

// fhEnv: a file browser over base/dl and base/media/movies, its config
// wrapped by a Holds whose hooks reach the fake addon.
type fhEnv struct {
	t      *testing.T
	base   string
	dl     string
	movies string
	db     *Holds
	hooks  *Hooks
	tg     *hookTarget
	inner  *innerEngine
	repo   *library.Repo
	svc    *files.Service
	fh     *FileHooks
}

const allFileHooks = `"files.plan": {"status": 204}, "files.pause": {"status": 204}, "files.resume": {"status": 204}, "files.stop": {"status": 204}, "files.detach": {"status": 204}, "files.attach": {"status": 204}`

var fileHookNames = []string{"files.plan", "files.pause", "files.resume", "files.stop", "files.detach", "files.attach"}

func newFHEnv(t *testing.T) *fhEnv { return newFHEnvRoot(t, false) }

// newFHEnvRoot: with linked, the browser's dl root is a symlink to a real
// folder next to it.
func newFHEnvRoot(t *testing.T, linked bool) *fhEnv {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &fhEnv{t: t, base: base, dl: filepath.Join(base, "dl"), movies: filepath.Join(base, "media", "movies"), inner: newInner()}
	for _, d := range []string{e.dl, e.movies} {
		if linked && d == e.dl {
			d = filepath.Join(base, "dlreal")
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if linked {
		if err := os.Symlink(filepath.Join(base, "dlreal"), e.dl); err != nil {
			t.Fatal(err)
		}
	}
	e.hooks, e.tg = newHooks(t)
	d := openTestDB(t)
	e.repo = library.NewRepo(d)
	e.db = &Holds{Refs: &RefStore{DB: d}, Hooks: e.hooks, Roots: []string{e.dl, e.movies},
		Installed: func(ctx context.Context) (map[string]string, error) {
			out := map[string]string{}
			for id := range e.tg.ins {
				out[id] = "Addon " + id
				if i18n.FromContext(ctx) == i18n.EN {
					out[id] = "Addon EN " + id
				}
			}
			return out, nil
		}}
	fc := files.ServiceConfig{
		Sandbox: files.NewSandbox(base, []files.Root{
			{ID: "dl", Title: "dl", Dir: e.dl},
			{ID: "movies", Title: "movies", Dir: e.movies},
			{ID: "disk", Title: "disk", Dir: base, AdminOnly: true},
		}),
		DB: d, Library: e.repo, LibraryRoots: []string{e.movies},
		Held: e.inner, Holders: e.inner,
	}
	e.db.Wrap(&fc)
	e.fh = fc.Holders.(*FileHooks)
	e.svc = files.NewService(fc)
	return e
}

func (e *fhEnv) file(rel string) string {
	e.t.Helper()
	p := filepath.Join(e.base, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *fhEnv) put(id string, holds ...addonkit.Hold) {
	e.t.Helper()
	if err := e.db.Put(context.Background(), id, holds); err != nil {
		e.t.Fatal(err)
	}
}

func there(p string) bool { _, err := os.Lstat(p); return err == nil }

// names of the hook calls the addon got, in order.
func hookNames(t *testing.T, tg *hookTarget, id string) []string {
	t.Helper()
	var out []string
	for _, c := range calls(t, tg, id) {
		out = append(out, c.Name)
	}
	return out
}

func lastBody(t *testing.T, tg *hookTarget, id, name string, v any) {
	t.Helper()
	cs := calls(t, tg, id)
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i].Name == name {
			if err := json.Unmarshal(cs[i].Body, v); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no %s call", name)
}

// pack: dl/Pack with a.mkv selected, b.mkv unselected; one hold "k".
func (e *fhEnv) pack() (root, a, b string, h addonkit.Hold) {
	a, b = e.file("dl/Pack/a.mkv"), e.file("dl/Pack/b.mkv")
	root = filepath.Dir(a)
	h = addonkit.Hold{Key: "k", Root: root, Active: true, Files: []addonkit.HoldFile{{Path: a, Size: 4, Selected: true}, {Path: b, Size: 4}},
		Label: addonkit.Text{RU: "Используется", EN: "In use"}, MoveWarning: addonkit.Text{RU: "«{name}» уйдёт", EN: "{name} goes"},
		KeptWarning: addonkit.Text{RU: "«{name}» останется", EN: "{name} stays"}}
	return
}

// A plan touching nothing held asks the build's own hooks only.
func TestFileHooks_PlanNothingHeld(t *testing.T) {
	e := newFHEnv(t)
	hookAddon(t, e.tg, "ad", `{`+allFileHooks+`}`, fileHookNames, true)
	_, _, _, h := e.pack()
	e.put("ad", h)
	other := e.file("dl/Other/x.mkv")
	e.inner.stop[filepath.Dir(other)] = []string{"ih1"}
	e.inner.follow[filepath.Dir(other)] = []string{"ih2"}
	follow, stop := e.fh.PlanRelocateCtx(context.Background(), filepath.Dir(other), filepath.Join(e.dl, "Other2"))
	if !slices.Equal(follow, []string{"ih2"}) || !slices.Equal(stop, []string{"ih1"}) {
		t.Fatalf("follow %v stop %v", follow, stop)
	}
	if n := hookNames(t, e.tg, "ad"); len(n) != 0 {
		t.Fatalf("hook calls %v", n)
	}
}

// A folder with an addon's holds and the build's own data: one files.plan,
// the answers merged, the addon's keys namespaced; a held key the answer
// does not let follow is stopped.
func TestFileHooks_PlanMixed(t *testing.T) {
	e := newFHEnv(t)
	hookAddon(t, e.tg, "ad", `{"files.plan": {"body": {"follow": ["k", "nope"], "stop": []}}}`, fileHookNames, true)
	_, _, _, h := e.pack()
	c := e.file("dl/Pack2/c.mkv")
	e.put("ad", h, addonkit.Hold{Key: "k2", Root: filepath.Dir(c), Files: []addonkit.HoldFile{{Path: c, Selected: true}}})
	e.inner.stop[e.dl] = []string{"ih1"}
	to := filepath.Join(e.base, "media", "dl")
	follow, stop := e.fh.PlanRelocateCtx(context.Background(), e.dl, to)
	if !slices.Equal(follow, []string{"ad:k"}) || !slices.Equal(stop, []string{"ih1", "ad:k2"}) {
		t.Fatalf("follow %v stop %v", follow, stop)
	}
	if n := hookNames(t, e.tg, "ad"); !slices.Equal(n, []string{"files.plan"}) {
		t.Fatalf("hook calls %v", n)
	}
	var body struct {
		Moves []struct{ From, To string }
		Keys  []string
	}
	lastBody(t, e.tg, "ad", "files.plan", &body)
	if len(body.Moves) != 1 || body.Moves[0].From != e.dl || body.Moves[0].To != to || !slices.Equal(body.Keys, []string{"k", "k2"}) {
		t.Fatalf("plan body %+v", body)
	}
}

// A down addon (not started, starting, answering 5xx) never lets its data
// move: its keys are stopped, holding them fails, nothing moves.
func TestFileHooks_AddonDown(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(e *fhEnv)
	}{
		{"stopped", func(e *fhEnv) { hookAddon(t, e.tg, "ad", `{`+allFileHooks+`}`, fileHookNames, false) }},
		{"failing", func(e *fhEnv) {
			hookAddon(t, e.tg, "ad", `{"files.plan": {"status": 500}, "files.pause": {"status": 500}, "files.resume": {"status": 204}}`, fileHookNames, true)
		}},
		{"starting", func(e *fhEnv) {
			in := Installed{ID: "ad", Enabled: true, Manifest: Manifest{ID: "ad", Hooks: fileHookNames}}
			e.tg.ins["ad"] = in
			e.hooks.T = stubTarget{in: in, status: StatusStarting}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFHEnv(t)
			tc.setup(e)
			root, a, _, h := e.pack()
			e.put("ad", h)
			ctx := context.Background()
			follow, stop := e.fh.PlanRelocateCtx(ctx, root, filepath.Join(e.dl, "Moved"))
			if len(follow) != 0 || !slices.Equal(stop, []string{"ad:k"}) {
				t.Fatalf("follow %v stop %v", follow, stop)
			}
			err := e.fh.PauseAll(ctx, []string{"ad:k"})
			if !errors.Is(err, files.ErrHeld) {
				t.Fatalf("PauseAll: %v", err)
			}
			var he *HoldError
			if !errors.As(err, &he) || !strings.Contains(he.Message, "«Addon ad» не ответило") {
				t.Fatalf("message: %v", err)
			}
			if err := e.svc.Ops.Rename(ctx, "dl", "Pack", "Moved", true); !errors.Is(err, files.ErrHeld) {
				t.Fatalf("move: %v", err)
			}
			if err := e.svc.Ops.Rename(ctx, "dl", "Pack/a.mkv", "z.mkv", true); !errors.Is(err, files.ErrHeld) {
				t.Fatalf("rename: %v", err)
			}
			if _, err := e.svc.Ops.Trash(ctx, "dl", []string{"Pack"}, true, 1); !errors.Is(err, files.ErrHeld) {
				t.Fatalf("trash: %v", err)
			}
			if !there(a) || there(filepath.Join(e.dl, "Moved")) {
				t.Fatal("held data moved")
			}
		})
	}
}

// Moving a folder of 40 holds makes one files.plan, one files.pause with
// every key and one files.stop.
func TestFileHooks_FortyKeysOneCall(t *testing.T) {
	e := newFHEnv(t)
	hookAddon(t, e.tg, "ad", `{`+allFileHooks+`}`, fileHookNames, true)
	var hs []addonkit.Hold
	for i := range 40 {
		p := e.file(filepath.Join("dl/Big", "h"+string(rune('A'+i/26))+string(rune('a'+i%26)), "f.mkv"))
		hs = append(hs, addonkit.Hold{Key: "k" + filepath.Base(filepath.Dir(p)), Root: filepath.Dir(p), Files: []addonkit.HoldFile{{Path: p, Selected: true}}})
	}
	e.put("ad", hs...)
	ctx := context.Background()
	if err := e.svc.Ops.Rename(ctx, "dl", "Big", "Big2", false); !errors.Is(err, files.ErrNeedsConfirm) {
		t.Fatalf("unconfirmed move: %v", err)
	}
	if err := e.svc.Ops.Rename(ctx, "dl", "Big", "Big2", true); err != nil {
		t.Fatal(err)
	}
	if !there(filepath.Join(e.dl, "Big2")) {
		t.Fatal("not moved")
	}
	if n := hookNames(t, e.tg, "ad"); !slices.Equal(n, []string{"files.plan", "files.pause", "files.stop"}) {
		t.Fatalf("hook calls %v", n)
	}
	var body struct{ Keys []string }
	lastBody(t, e.tg, "ad", "files.pause", &body)
	if len(body.Keys) != 40 || strings.Contains(body.Keys[0], ":") {
		t.Fatalf("pause keys %v", body.Keys)
	}
}

// A hold that follows: files.detach, the rename, files.attach{renamed}.
func TestFileHooks_Relocate(t *testing.T) {
	e := newFHEnv(t)
	hookAddon(t, e.tg, "ad", `{"files.plan": {"body": {"follow": ["k"]}}, "files.detach": {"status": 204}, "files.attach": {"status": 204}}`, fileHookNames, true)
	root, _, _, h := e.pack()
	e.put("ad", h)
	ctx := context.Background()
	if err := e.svc.Ops.Rename(ctx, "dl", "Pack", "Pack2", true); err != nil {
		t.Fatal(err)
	}
	if !there(filepath.Join(e.dl, "Pack2", "a.mkv")) || there(root) {
		t.Fatal("not moved")
	}
	if n := hookNames(t, e.tg, "ad"); !slices.Equal(n, []string{"files.plan", "files.detach", "files.attach"}) {
		t.Fatalf("hook calls %v", n)
	}
	var att struct {
		From, To string
		Keys     []string
		Renamed  bool
	}
	lastBody(t, e.tg, "ad", "files.attach", &att)
	if att.From != root || att.To != filepath.Join(e.dl, "Pack2") || !att.Renamed || !slices.Equal(att.Keys, []string{"k"}) {
		t.Fatalf("attach %+v", att)
	}
	if got := e.inner.got(); slices.Contains(got, "relocate "+root) {
		t.Fatalf("inner relocated without following: %v", got)
	}

	// a failed rename: attach{renamed: false} and the rename's error
	other := filepath.Join(e.dl, "Pack2")
	e.put("ad", addonkit.Hold{Key: "k", Root: other, Files: []addonkit.HoldFile{{Path: filepath.Join(other, "a.mkv"), Selected: true}}})
	to := filepath.Join(e.dl, "Pack3")
	if f, _ := e.fh.PlanRelocateCtx(ctx, other, to); !slices.Equal(f, []string{"ad:k"}) {
		t.Fatalf("follow %v", f)
	}
	boom := errors.New("boom")
	if err := e.fh.Relocate(ctx, other, to, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("relocate: %v", err)
	}
	lastBody(t, e.tg, "ad", "files.attach", &att)
	if att.Renamed {
		t.Fatal("attach says renamed")
	}
}

// A detach that fails: nothing is renamed, the addon is not attached.
func TestFileHooks_DetachFails(t *testing.T) {
	e := newFHEnv(t)
	hookAddon(t, e.tg, "ad", `{"files.plan": {"body": {"follow": ["k"]}}, "files.detach": {"status": 500}, "files.attach": {"status": 204}}`, fileHookNames, true)
	root, _, _, h := e.pack()
	e.put("ad", h)
	if err := e.svc.Ops.Rename(context.Background(), "dl", "Pack", "Pack2", true); !errors.Is(err, files.ErrHeld) {
		t.Fatalf("move: %v", err)
	}
	if !there(root) {
		t.Fatal("moved")
	}
	if n := hookNames(t, e.tg, "ad"); !slices.Equal(n, []string{"files.plan", "files.detach"}) {
		t.Fatalf("hook calls %v", n)
	}
}

// Linked finds holds by item and by the ref a deleted file carries; Paths
// are exactly the selected regular files.
func TestFileHooks_Linked(t *testing.T) {
	e := newFHEnv(t)
	root, a, b, h := e.pack()
	sub := filepath.Join(root, "Extras")
	e.file("dl/Pack/Extras/x.mkv")
	h.ItemID = 7
	h.Busy = true
	h.Files = append(h.Files, addonkit.HoldFile{Path: sub, Selected: true})
	c := e.file("dl/ByRef/c.mkv")
	byRef := addonkit.Hold{Key: "r1", Root: filepath.Dir(c), Files: []addonkit.HoldFile{{Path: c, Selected: true}}}
	e.put("ad", h, byRef, addonkit.Hold{Key: "none", Root: filepath.Join(e.dl, "None")})
	ctx := context.Background()
	m := e.file("media/movies/M (2000)/M.mkv")
	if _, err := e.repo.UpsertMovie(ctx, library.MovieInput{Title: "M", Year: 2000, File: library.FileInput{Path: m, Size: 4}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Refs.Bind(ctx, "ad", "r1", []string{m}); err != nil {
		t.Fatal(err)
	}
	f, _ := e.repo.FileByPath(ctx, m)

	ls := e.fh.Linked(ctx, library.DeleteSet{Items: []int64{7}})
	if len(ls) != 1 {
		t.Fatalf("by item: %+v", ls)
	}
	l := ls[0]
	if l.Key != "ad:k" || l.Path != root || !l.Listed || !l.Busy || l.ItemID != 7 || !slices.Equal(l.Paths, []string{a}) || len(l.Files) != 3 {
		t.Fatalf("by item: %+v (b %s)", l, b)
	}
	ls = e.fh.Linked(ctx, library.DeleteSet{Items: []int64{f.ItemID}, Files: []library.File{f}})
	if len(ls) != 1 || ls[0].Key != "ad:r1" || ls[0].ItemID != 0 || !slices.Equal(ls[0].Paths, []string{c}) || !ls[0].Listed {
		t.Fatalf("by ref: %+v", ls)
	}
	// LinkFiles: an addon's key → its ref's files
	fs, err := e.fh.LinkFiles(ctx, "ad:r1")
	if err != nil || len(fs) != 1 || fs[0].ID != f.ID {
		t.Fatalf("LinkFiles a:r1: %+v %v", fs, err)
	}
	if fs, err := e.fh.LinkFiles(ctx, "bb:r1"); err != nil || len(fs) != 0 {
		t.Fatalf("LinkFiles b:r1: %+v %v", fs, err)
	}
}

// Item delete through the adapter: a hold with no selected file leaves its
// root and files; one with a selected file loses only that file.
func TestFileHooks_ItemDeleteListedOnly(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "none selected", true: "one selected"}[selected], func(t *testing.T) {
			e := newFHEnv(t)
			hookAddon(t, e.tg, "ad", `{`+allFileHooks+`}`, fileHookNames, true)
			root, a, b, h := e.pack()
			h.Files[0].Selected = selected
			e.put("ad", h)
			ctx := context.Background()
			m := e.file("media/movies/M (2000)/M.mkv")
			if _, err := e.repo.UpsertMovie(ctx, library.MovieInput{Title: "M", Year: 2000, File: library.FileInput{Path: m, Size: 4}}); err != nil {
				t.Fatal(err)
			}
			if _, err := e.db.Refs.Bind(ctx, "ad", "k", []string{m}); err != nil {
				t.Fatal(err)
			}
			f, _ := e.repo.FileByPath(ctx, m)
			if _, err := e.svc.Items.Delete(ctx, f.ItemID, 1); err != nil {
				t.Fatal(err)
			}
			if there(m) {
				t.Fatal("the library file stayed")
			}
			if !there(root) || !there(b) || there(a) != !selected {
				t.Fatalf("root %v a %v b %v", there(root), there(a), there(b))
			}
			names := hookNames(t, e.tg, "ad")
			if selected && !slices.Equal(names, []string{"files.plan", "files.pause", "files.stop"}) || !selected && len(names) != 0 {
				t.Fatalf("hook calls %v", names)
			}
		})
	}
}

// Texts and listings come from the index: no hook call per lookup.
func TestFileHooks_Texts(t *testing.T) {
	e := newFHEnv(t)
	hookAddon(t, e.tg, "ad", `{`+allFileHooks+`}`, fileHookNames, true)
	root, a, b, h := e.pack()
	e.put("ad", h)
	ctx := context.Background()
	for range 5000 {
		e.fh.HoldOf(ctx, a)
	}
	if r, ok := e.fh.HoldOf(ctx, a); !ok || r != (files.HoldRef{Addon: "Addon ad", Label: "Используется", Active: true}) {
		t.Fatalf("HoldOf a: %+v %v", r, ok)
	}
	en := i18n.WithLanguage(ctx, i18n.EN)
	if r, ok := e.fh.HoldOf(en, b); !ok || r != (files.HoldRef{Addon: "Addon EN ad", Label: "In use", Active: false}) {
		t.Fatalf("HoldOf b (en): %+v %v", r, ok)
	}
	if w := e.fh.MoveWarning(ctx, root, "Pack"); w != "«Pack» уйдёт" {
		t.Fatalf("MoveWarning: %q", w)
	}
	if w := e.fh.MoveWarning(ctx, e.dl, "dl"); w != "«dl» уйдёт" { // a folder holding the hold
		t.Fatalf("MoveWarning above: %q", w)
	}
	if w := e.fh.KeptWarning(en, "ad:k", "Pack"); w != "Pack stays" {
		t.Fatalf("KeptWarning: %q", w)
	}
	if w := e.fh.KeptWarning(ctx, "ih", "Pack"); w != "" {
		t.Fatalf("KeptWarning inner: %q", w)
	}
	if n := hookNames(t, e.tg, "ad"); len(n) != 0 {
		t.Fatalf("hook calls %v", n)
	}
	l, err := e.svc.Browser.List(ctx, "dl", "Pack", files.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*files.HoldRef{}
	for _, x := range l.Entries {
		got[x.Name] = x.Hold
	}
	if got["a.mkv"] == nil || got["b.mkv"] == nil || got["a.mkv"].Label != "Используется" {
		t.Fatalf("listing holds %+v", got)
	}
}

// Hold texts are shown as refusal texts are: control characters dropped,
// spaces trimmed, cut to 500 characters.
func TestText_Cleaned(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("я", 600)
	for in, want := range map[string]string{
		"  В\x00 работе\n\t": "В работе",
		"\x1b[31mкрасный":    "[31mкрасный",
		long:                 strings.Repeat("я", 500),
	} {
		if got := text(ctx, addonkit.Text{RU: in}); got != want {
			t.Errorf("text(%q) = %q, want %q", in, got, want)
		}
	}
}

// Wrapping keeps the build's own hooks for its own keys and paths.
func TestWrap_KeepsInner(t *testing.T) {
	e := newFHEnv(t)
	ctx := context.Background()
	x := e.file("dl/Own/x.mkv")
	e.inner.owned[x] = "ih"
	e.inner.stop[x] = []string{"ih"}
	if ih, _, ok := e.fh.HeldInfo(x); !ok || ih != "ih" {
		t.Fatalf("HeldInfo %q %v", ih, ok)
	}
	if k, ok := e.fh.Owner(x); !ok || k != "ih" {
		t.Fatalf("Owner %q %v", k, ok)
	}
	if _, stop := e.fh.PlanRelocate(x, ""); !slices.Equal(stop, []string{"ih"}) {
		t.Fatalf("stop %v", stop)
	}
	if err := e.fh.PauseAll(ctx, []string{"ih"}); err != nil {
		t.Fatal(err)
	}
	if err := e.fh.StopAll(ctx, []string{"ih"}); err != nil {
		t.Fatal(err)
	}
	if err := e.fh.ResumeAll(ctx, []string{"ih"}); err != nil {
		t.Fatal(err)
	}
	got := e.inner.got()
	for _, want := range []string{"pause ih", "stop ih", "resume ih"} {
		if !slices.Contains(got, want) {
			t.Fatalf("inner calls %v, no %q", got, want)
		}
	}
	// the build's linked data and link files stay
	fc := files.ServiceConfig{Library: e.repo,
		Linked: func(context.Context, library.DeleteSet) []files.Linked {
			return []files.Linked{{Path: "/inner", Key: "ih"}}
		},
		LinkFiles: func(_ context.Context, key string) ([]library.File, error) {
			return []library.File{{ID: 99, Path: key}}, nil
		}}
	e.db.Wrap(&fc)
	if ls := fc.Linked(ctx, library.DeleteSet{}); len(ls) != 1 || ls[0].Key != "ih" {
		t.Fatalf("linked %+v", ls)
	}
	if fs, err := fc.LinkFiles(ctx, "ih"); err != nil || len(fs) != 1 || fs[0].ID != 99 {
		t.Fatalf("link files %+v %v", fs, err)
	}
	if fc.Texts == nil || fc.Held == nil || fc.Holders == nil {
		t.Fatal("not wrapped")
	}
	// the core build: nothing to wrap
	var core files.ServiceConfig
	e.db.Wrap(&core)
	if _, _, ok := core.Held.HeldInfo(x); ok {
		t.Fatal("core build reports a download")
	}
	if f, s := core.Holders.PlanRelocate(x, ""); len(f)+len(s) != 0 {
		t.Fatalf("core build plan %v %v", f, s)
	}
	if ls := core.Linked(ctx, library.DeleteSet{}); len(ls) != 0 {
		t.Fatalf("core build linked %+v", ls)
	}
}

// Removing an addon drops its holds from the index too.
func TestManager_RemoveDropsHoldIndex(t *testing.T) {
	m, dev := newManager(t)
	ctx := context.Background()
	writeLocal(t, dev, "hello", "1.0.0", "ok", true)
	if _, err := m.Install(ctx, "local:hello", ""); err != nil {
		t.Fatal(err)
	}
	m.Refs = &RefStore{DB: m.Store.DB}
	m.Holds = &Holds{Refs: m.Refs, Roots: []string{"/dl"}}
	if err := m.Holds.Put(ctx, "hello", []addonkit.Hold{{Key: "k", Root: "/dl/x", Files: []addonkit.HoldFile{{Path: "/dl/x/f", Selected: true}}}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(ctx, "hello", false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := m.Holds.Lookup("/dl/x/f"); ok {
		t.Fatal("hold still indexed")
	}
	if raw, err := m.Refs.LoadHolds(ctx); err != nil || len(raw) != 0 {
		t.Fatalf("saved holds %v %v", raw, err)
	}
}

// A files.plan that fails, times out or refuses refuses the operation:
// nothing moves and files.pause is never asked; a refusal shows its text.
func TestFileHooks_PlanFailureRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, plan, text string
		timeout          bool
	}{
		{"failure", `{"status": 500}`, "«Addon ad» не ответило", false},
		{"timeout", `{"status": 204, "delayMs": 2000}`, "«Addon ad» не ответило", true},
		{"not implemented", `{"status": 404}`, "«Addon ad» не ответило", false},
		{"refusal", `{"status": 409, "body": {"error": "Идёт проверка «Pack»"}}`, "Идёт проверка «Pack»", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFHEnv(t)
			script := `{"files.plan": ` + tc.plan + `, "files.pause": {"status": 204}, "files.resume": {"status": 204}, "files.stop": {"status": 204}}`
			if tc.plan == `{"status": 404}` {
				script = `{"files.pause": {"status": 204}, "files.resume": {"status": 204}, "files.stop": {"status": 204}}`
			}
			hookAddon(t, e.tg, "ad", script, fileHookNames, true)
			if tc.timeout {
				e.hooks.timeouts = map[string]time.Duration{"files.plan": 200 * time.Millisecond}
			}
			root, a, _, h := e.pack()
			e.put("ad", h)
			ctx := context.Background()
			for _, op := range []func() error{
				func() error { return e.svc.Ops.Rename(ctx, "dl", "Pack", "Moved", true) },
				func() error { _, err := e.svc.Ops.Trash(ctx, "dl", []string{"Pack"}, true, 1); return err },
			} {
				err := op()
				var he *HoldError
				if !errors.Is(err, files.ErrHeld) || !errors.As(err, &he) || !strings.Contains(he.Message, tc.text) {
					t.Fatalf("op: %v", err)
				}
			}
			if !there(a) || !there(root) {
				t.Fatal("held data moved")
			}
			if n := hookNames(t, e.tg, "ad"); slices.Contains(n, "files.pause") || slices.Contains(n, "files.stop") {
				t.Fatalf("hook calls %v", n)
			}
		})
	}
}

// A refused hold releases its keys: the plan it left is not used by a
// later relocation of the same paths.
func TestFileHooks_RetryAfterRefusedPause(t *testing.T) {
	e := newFHEnv(t)
	hookAddon(t, e.tg, "ad", `{"files.plan": {"body": {"follow": ["k"]}}, "files.pause": {"status": 500}, "files.resume": {"status": 204}, "files.detach": {"status": 204}, "files.attach": {"status": 204}}`, fileHookNames, true)
	a := e.file("dl/Group/P1/a.mkv")
	b := e.file("dl/Group/P2/b.mkv")
	e.put("ad", addonkit.Hold{Key: "k", Root: filepath.Dir(a), Files: []addonkit.HoldFile{{Path: a, Selected: true}}},
		addonkit.Hold{Key: "k2", Root: filepath.Dir(b), Files: []addonkit.HoldFile{{Path: b, Selected: true}}})
	ctx := context.Background()
	if err := e.svc.Ops.Rename(ctx, "dl", "Group", "Group2", true); !errors.Is(err, files.ErrHeld) {
		t.Fatalf("rename: %v", err)
	}
	from, to := filepath.Join(e.dl, "Group"), filepath.Join(e.dl, "Group2")
	renamed := false
	// no plan of its own for held data: refused, never renamed blind
	if err := e.fh.Relocate(ctx, from, to, func() error { renamed = true; return nil }); !errors.Is(err, files.ErrHeld) || renamed {
		t.Fatalf("relocate without a plan: %v (renamed %v)", err, renamed)
	}
	if n := hookNames(t, e.tg, "ad"); slices.Contains(n, "files.detach") {
		t.Fatalf("a stale plan was used: %v", n)
	}
	// planning again replaces the plan: one detach for one Relocate
	ctx = files.WithOp(ctx)
	e.fh.PlanRelocateCtx(ctx, from, to)
	e.fh.PlanRelocateCtx(ctx, from, to)
	e.fh.Relocate(ctx, from, to, func() error { return nil })
	e.fh.Relocate(ctx, from, to, func() error { return nil })
	detach := 0
	for _, n := range hookNames(t, e.tg, "ad") {
		if n == "files.detach" {
			detach++
		}
	}
	if detach != 1 {
		t.Fatalf("detach calls %d", detach)
	}
}

// Two operations on the same held data: one releasing its keys never
// takes the other's plan away; that one still detaches and attaches.
func TestFileHooks_PlansBelongToTheirOperation(t *testing.T) {
	e := newFHEnv(t)
	hookAddon(t, e.tg, "ad", `{"files.plan": {"body": {"follow": ["k"]}}, "files.pause": {"status": 204}, "files.resume": {"status": 204}, "files.detach": {"status": 204}, "files.attach": {"status": 204}}`, fileHookNames, true)
	a := e.file("dl/Group/P1/a.mkv")
	b := e.file("dl/Group/P2/b.mkv")
	e.put("ad", addonkit.Hold{Key: "k", Root: filepath.Dir(a), Files: []addonkit.HoldFile{{Path: a, Selected: true}}},
		addonkit.Hold{Key: "k2", Root: filepath.Dir(b), Files: []addonkit.HoldFile{{Path: b, Selected: true}}})
	from, to := filepath.Join(e.dl, "Group"), filepath.Join(e.dl, "Group2")
	x, y := files.WithOp(context.Background()), files.WithOp(context.Background())
	if files.OpID(x) == files.OpID(y) || files.OpID(files.WithOp(x)) != files.OpID(x) {
		t.Fatal("operation ids")
	}
	e.fh.PlanRelocateCtx(x, from, to)
	if err := e.fh.PauseAll(x, []string{"ad:k2"}); err != nil {
		t.Fatal(err)
	}
	e.fh.PlanRelocateCtx(y, from, to)
	if err := e.fh.ResumeAll(y, []string{"ad:k", "ad:k2"}); err != nil { // y gives up
		t.Fatal(err)
	}
	renamed := false
	if err := e.fh.Relocate(x, from, to, func() error { renamed = true; return nil }); err != nil || !renamed {
		t.Fatalf("x: %v (renamed %v)", err, renamed)
	}
	n := hookNames(t, e.tg, "ad")
	if !slices.Contains(n, "files.detach") || !slices.Contains(n, "files.attach") {
		t.Fatalf("x relocated without its addon: %v", n)
	}
	// a failed plan of one operation does not refuse another's hold
	e.fh.failPlan(y, "ad", []string{"k2"}, errors.New("down"))
	if err := e.fh.PauseAll(x, []string{"ad:k2"}); err != nil {
		t.Fatalf("x refused by y's plan: %v", err)
	}
	if err := e.fh.PauseAll(y, []string{"ad:k2"}); !errors.Is(err, files.ErrHeld) {
		t.Fatalf("y not refused: %v", err)
	}
}

// Held data reached through a symlinked folder inside the roots is never
// moved or trashed by that other path, the addon running or stopped: the
// operation is refused before any hook is asked. The link itself, and
// data no addon holds behind a link, still move.
func TestFileHooks_SymlinkAliasRefused(t *testing.T) {
	for _, running := range []bool{true, false} {
		t.Run(map[bool]string{true: "running", false: "stopped"}[running], func(t *testing.T) {
			e := newFHEnv(t)
			hookAddon(t, e.tg, "ad", `{`+allFileHooks+`}`, fileHookNames, running)
			root, a, _, h := e.pack()
			e.put("ad", h)
			ctx := context.Background()
			if err := os.Symlink(root, filepath.Join(e.dl, "link")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(e.dl, filepath.Join(e.dl, "up")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(e.dl, "To"), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, c := range []struct {
				name string
				op   func() error
			}{
				{"rename file", func() error { return e.svc.Ops.Rename(ctx, "dl", "link/a.mkv", "z.mkv", true) }},
				{"rename folder", func() error { return e.svc.Ops.Rename(ctx, "dl", "up/Pack", "Moved", true) }},
				{"move file", func() error { return e.svc.Ops.Move(ctx, "dl", "link/a.mkv", "dl", "To", true) }},
				{"move folder", func() error { return e.svc.Ops.Move(ctx, "dl", "up/Pack", "dl", "To", true) }},
				{"other root", func() error { return e.svc.Ops.Rename(ctx, "disk", "dl/link/a.mkv", "z.mkv", true) }},
				{"trash file", func() error { _, err := e.svc.Ops.Trash(ctx, "dl", []string{"link/a.mkv"}, true, 1); return err }},
				{"trash folder", func() error { _, err := e.svc.Ops.Trash(ctx, "dl", []string{"up/Pack"}, true, 1); return err }},
				{"unconfirmed", func() error { return e.svc.Ops.Rename(ctx, "dl", "link/a.mkv", "z.mkv", false) }},
				{"trash unconfrm", func() error { _, err := e.svc.Ops.Trash(ctx, "dl", []string{"link/a.mkv"}, false, 1); return err }},
			} {
				if err := c.op(); !errors.Is(err, files.ErrHeldAlias) {
					t.Errorf("%s: %v, want ErrHeldAlias", c.name, err)
				}
			}
			if !there(a) || there(filepath.Join(root, "z.mkv")) || there(filepath.Join(e.dl, "Moved")) || there(filepath.Join(e.dl, "To", "a.mkv")) || there(filepath.Join(e.dl, "To", "Pack")) {
				t.Fatal("held data moved through a link")
			}
			if running { // a stopped addon is asked nothing anyway
				if n := hookNames(t, e.tg, "ad"); len(n) != 0 {
					t.Fatalf("hook calls %v", n)
				}
			}
			// data no addon holds behind a link moves; so does the link itself
			x := e.file("dl/Other/x.mkv")
			if err := os.Symlink(filepath.Dir(x), filepath.Join(e.dl, "olink")); err != nil {
				t.Fatal(err)
			}
			if err := e.svc.Ops.Rename(ctx, "dl", "olink/x.mkv", "y.mkv", false); err != nil || !there(filepath.Join(e.dl, "Other", "y.mkv")) {
				t.Fatalf("unheld rename through a link: %v", err)
			}
			if err := e.svc.Ops.Rename(ctx, "dl", "link", "link2", false); err != nil || !there(a) {
				t.Fatalf("renaming the link itself: %v", err)
			}
			if _, err := e.svc.Ops.Trash(ctx, "dl", []string{"link2"}, false, 1); err != nil || !there(a) {
				t.Fatalf("trashing the link itself: %v", err)
			}
		})
	}
}
