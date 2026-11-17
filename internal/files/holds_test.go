package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// batchEngine implements Holders with the optional HoldersCtx and
// BatchHolders: what an addon's holds look like to Ops. plan maps a path
// to the keys an operation on it stops.
type batchEngine struct {
	plan      map[string][]string
	pauseErr  error
	pauseWait time.Duration // PauseAll takes this long
	plans     []string
	pauses    [][]string
	resumes   [][]string
	stops     [][]string
	deadlines []time.Duration // left on each call's ctx (-1: none)
	perKey    int             // calls of Pause, Resume or Stop
	noCtx     int             // calls of PlanRelocate
}

func (b *batchEngine) left(ctx context.Context) {
	d, ok := ctx.Deadline()
	if !ok {
		b.deadlines = append(b.deadlines, -1)
		return
	}
	b.deadlines = append(b.deadlines, time.Until(d))
}

func (b *batchEngine) PlanRelocate(from, to string) ([]string, []string) {
	b.noCtx++
	b.plans = append(b.plans, from)
	return nil, b.plan[from]
}

func (b *batchEngine) PlanRelocateCtx(ctx context.Context, from, to string) ([]string, []string) {
	b.left(ctx)
	b.plans = append(b.plans, from)
	return nil, b.plan[from]
}

func (b *batchEngine) Relocate(ctx context.Context, from, to string, rename func() error) error {
	return rename()
}

func (b *batchEngine) Stop(context.Context, string) error   { b.perKey++; return nil }
func (b *batchEngine) Pause(context.Context, string) error  { b.perKey++; return nil }
func (b *batchEngine) Resume(context.Context, string) error { b.perKey++; return nil }

func (b *batchEngine) PauseAll(ctx context.Context, keys []string) error {
	b.left(ctx)
	time.Sleep(b.pauseWait)
	b.pauses = append(b.pauses, sorted(keys))
	return b.pauseErr
}

func (b *batchEngine) ResumeAll(ctx context.Context, keys []string) error {
	b.left(ctx)
	b.resumes = append(b.resumes, sorted(keys))
	return nil
}

func (b *batchEngine) StopAll(ctx context.Context, keys []string) error {
	b.left(ctx)
	b.stops = append(b.stops, sorted(keys))
	return nil
}

func sorted(keys []string) []string {
	out := append([]string{}, keys...)
	sort.Strings(out)
	return out
}

func (b *batchEngine) checkDeadlines(t *testing.T) {
	t.Helper()
	if len(b.deadlines) == 0 || b.noCtx > 0 {
		t.Fatalf("calls with a ctx %d, plans without one %d", len(b.deadlines), b.noCtx)
	}
	for _, d := range b.deadlines {
		if d < 0 || d > holdOpTimeout {
			t.Fatalf("a call's deadline: %v left (want ≤ %v)", d, holdOpTimeout)
		}
	}
}

// fakeTexts answers HoldTexts from maps.
type fakeTexts struct {
	holds map[string]HoldRef
	move  string // "" → the build's fallback
	kept  string
	asked []string
}

func (f *fakeTexts) HoldOf(_ context.Context, p string) (HoldRef, bool) {
	f.asked = append(f.asked, p)
	h, ok := f.holds[p]
	return h, ok
}

func (f *fakeTexts) MoveWarning(_ context.Context, p, name string) string {
	f.asked = append(f.asked, p)
	if f.move == "" {
		return ""
	}
	return f.move + " " + name
}

func (f *fakeTexts) KeptWarning(_ context.Context, key, name string) string {
	if f.kept == "" {
		return ""
	}
	return f.kept + " " + key + " " + name
}

// Trashing three held items: one PauseAll with every key, one StopAll,
// no per-key call, all under one deadline of at most holdOpTimeout.
func TestBatchTrashOnePauseOneStop(t *testing.T) {
	o, mr, _ := testOps(t)
	var rels []string
	plan := map[string][]string{}
	for _, n := range []string{"a", "b", "c"} {
		p := filepath.Join(mr, "media/extra", n)
		write(t, filepath.Join(p, "x.mkv"), 3)
		rels = append(rels, "extra/"+n)
		plan[p] = []string{"x:" + n, "x:shared"}
	}
	be := &batchEngine{plan: plan}
	o.Holders = be
	res, err := o.Trash(context.Background(), "library", rels, true, 1)
	if err != nil || len(res.Trashed) != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	want := []string{"x:a", "x:b", "x:c", "x:shared"}
	if len(be.pauses) != 1 || strings.Join(be.pauses[0], ",") != strings.Join(want, ",") {
		t.Fatalf("pauses %v", be.pauses)
	}
	if len(be.stops) != 1 || strings.Join(be.stops[0], ",") != strings.Join(want, ",") {
		t.Fatalf("stops %v", be.stops)
	}
	if len(be.resumes) != 0 || be.perKey != 0 || len(be.plans) != 3 {
		t.Fatalf("resumes %v, per-key calls %d, plans %v", be.resumes, be.perKey, be.plans)
	}
	be.checkDeadlines(t)
}

// planAllEngine also plans a whole trash in one call (BatchPlan).
type planAllEngine struct {
	*batchEngine
	calls [][]string
}

func (p *planAllEngine) PlanTrashCtx(ctx context.Context, froms []string) [][]string {
	p.left(ctx)
	p.calls = append(p.calls, append([]string{}, froms...))
	out := make([][]string, len(froms))
	for i, f := range froms {
		out[i] = p.plan[f]
	}
	return out
}

// With BatchPlan a three-item trash makes one plan call, one PauseAll and
// one StopAll.
func TestBatchTrashOnePlanCall(t *testing.T) {
	o, mr, _ := testOps(t)
	var rels []string
	plan := map[string][]string{}
	for _, n := range []string{"a", "b", "c"} {
		p := filepath.Join(mr, "media/extra", n)
		write(t, filepath.Join(p, "x.mkv"), 3)
		rels = append(rels, "extra/"+n)
		plan[p] = []string{"x:" + n}
	}
	pe := &planAllEngine{batchEngine: &batchEngine{plan: plan}}
	o.Holders = pe
	res, err := o.Trash(context.Background(), "library", rels, true, 1)
	if err != nil || len(res.Trashed) != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(pe.calls) != 1 || len(pe.calls[0]) != 3 || len(pe.plans) != 0 {
		t.Fatalf("batch plans %v, per-item plans %v", pe.calls, pe.plans)
	}
	if len(pe.pauses) != 1 || strings.Join(pe.pauses[0], ",") != "x:a,x:b,x:c" || len(pe.stops) != 1 || pe.perKey != 0 {
		t.Fatalf("pauses %v stops %v per-key %d", pe.pauses, pe.stops, pe.perKey)
	}
	pe.checkDeadlines(t)
}

// A batch plan with the wrong number of answers moves nothing.
func TestBatchTrashPlanMismatchMovesNothing(t *testing.T) {
	o, mr, _ := testOps(t)
	p := filepath.Join(mr, "media/extra/a")
	write(t, filepath.Join(p, "x.mkv"), 3)
	o.Holders = &shortPlanEngine{&planAllEngine{batchEngine: &batchEngine{}}}
	res, err := o.Trash(context.Background(), "library", []string{"extra/a"}, true, 1)
	if !errors.Is(err, ErrHeld) || len(res.Trashed) != 0 || !exists(p) {
		t.Fatalf("%+v %v", res, err)
	}
}

type shortPlanEngine struct{ *planAllEngine }

func (s *shortPlanEngine) PlanTrashCtx(context.Context, []string) [][]string { return nil }

// A PauseAll that fails moves nothing, releases what it may have held
// and comes back as ErrHeld.
func TestBatchTrashPauseFailsMovesNothing(t *testing.T) {
	o, mr, _ := testOps(t)
	p := filepath.Join(mr, "media/extra/a")
	write(t, filepath.Join(p, "x.mkv"), 3)
	be := &batchEngine{plan: map[string][]string{p: {"x:a"}}, pauseErr: errors.New("down")}
	o.Holders = be
	res, err := o.Trash(context.Background(), "library", []string{"extra/a"}, true, 1)
	if !errors.Is(err, ErrHeld) || len(res.Trashed) != 0 || !exists(p) {
		t.Fatalf("%+v %v (data there: %v)", res, err, exists(p))
	}
	if len(be.resumes) != 1 || len(be.stops) != 0 {
		t.Fatalf("resumes %v stops %v", be.resumes, be.stops)
	}
}

// A failed rename in the middle of a batch: the keys of what moved are
// stopped, those only of what stayed are resumed, once each.
func TestBatchTrashPartialFailure(t *testing.T) {
	o, mr, _ := testOps(t)
	a, b := filepath.Join(mr, "media/extra/a"), filepath.Join(mr, "media/extra/b")
	write(t, filepath.Join(a, "x.mkv"), 3)
	write(t, filepath.Join(b, "x.mkv"), 3)
	be := &batchEngine{plan: map[string][]string{a: {"x:a", "x:s"}, b: {"x:b", "x:s"}}}
	// b vanishes once everything is paused, so its rename fails
	o.Holders = &removingEngine{batchEngine: be, remove: b}
	res, err := o.Trash(context.Background(), "library", []string{"extra/a", "extra/b"}, true, 1)
	if err == nil || len(res.Trashed) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(be.stops) != 1 || strings.Join(be.stops[0], ",") != "x:a,x:s" {
		t.Fatalf("stops %v", be.stops)
	}
	if len(be.resumes) != 1 || strings.Join(be.resumes[0], ",") != "x:b" {
		t.Fatalf("resumes %v", be.resumes)
	}
}

// removingEngine removes one path once everything is paused, so its
// rename fails.
type removingEngine struct {
	*batchEngine
	remove string
}

func (r *removingEngine) PauseAll(ctx context.Context, keys []string) error {
	os.RemoveAll(r.remove)
	return r.batchEngine.PauseAll(ctx, keys)
}

// The engine of the other build (no optional interfaces) keeps its
// per-key calls.
func TestHoldPerKeyEngineUnchanged(t *testing.T) {
	o, mr, _ := testOps(t)
	for _, n := range []string{"a", "b"} {
		write(t, filepath.Join(mr, "media/extra", n, "x.mkv"), 3)
	}
	ft := &fakeHolders{stop: []string{"k1", "k2"}}
	o.Holders = ft
	res, err := o.Trash(context.Background(), "library", []string{"extra/a", "extra/b"}, true, 1)
	if err != nil || len(res.Trashed) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(ft.plans) != 2 || len(ft.paused) != 4 || len(ft.stopped) != 4 {
		t.Fatalf("plans %v paused %v stopped %v", ft.plans, ft.paused, ft.stopped)
	}
}

// A move of held data pauses once and stops once, under the deadline.
func TestBatchMoveDeadline(t *testing.T) {
	o, mr, _ := testOps(t)
	p := filepath.Join(mr, "media/extra/a")
	write(t, filepath.Join(p, "x.mkv"), 3)
	os.MkdirAll(filepath.Join(mr, "media/movies"), 0o755)
	be := &batchEngine{plan: map[string][]string{p: {"x:a", "x:b"}}}
	o.Holders = be
	if err := o.Move(context.Background(), "library", "extra/a", "movies", "", true); err != nil {
		t.Fatal(err)
	}
	if len(be.plans) != 1 || len(be.pauses) != 1 || len(be.stops) != 1 || be.perKey != 0 {
		t.Fatalf("plans %v pauses %v stops %v per-key %d", be.plans, be.pauses, be.stops, be.perKey)
	}
	be.checkDeadlines(t)
}

// A rename of held data without confirm: the warning is the addon's
// text with the name; an empty one falls back to the neutral text.
func TestHoldMoveWarning(t *testing.T) {
	o, mr, _ := testOps(t)
	p := filepath.Join(mr, "media/extra/Pack")
	write(t, filepath.Join(p, "x.mkv"), 3)
	o.Held = fakeHeld{p: "x:k"}
	tx := &fakeTexts{move: "Расширение держит"}
	o.Texts = tx
	err := o.Rename(context.Background(), "library", "extra/Pack", "Pack2", false)
	var ce *ConfirmError
	if !errors.As(err, &ce) || len(ce.Warnings) != 1 || ce.Warnings[0] != "Расширение держит Pack" {
		t.Fatalf("%v", err)
	}
	if len(tx.asked) == 0 || tx.asked[0] != p {
		t.Fatalf("asked %v", tx.asked)
	}
	tx.move = ""
	err = o.Rename(context.Background(), "library", "extra/Pack", "Pack2", false)
	if !errors.As(err, &ce) || len(ce.Warnings) != 1 || ce.Warnings[0] != "Эти данные используются" {
		t.Fatalf("fallback: %v", err)
	}
	if !exists(p) {
		t.Fatal("moved without confirm")
	}
	// held by an addon that gives no text: the neutral one, with the addon
	tx.holds = map[string]HoldRef{p: {Addon: "x"}}
	err = o.Rename(context.Background(), "library", "extra/Pack", "Pack2", false)
	if !errors.As(err, &ce) || len(ce.Warnings) != 1 || ce.Warnings[0] != "Эти данные использует расширение «x»" {
		t.Fatalf("neutral: %v", err)
	}
}

// The listing shows the hold of an entry.
func TestHoldListingFillsEntry(t *testing.T) {
	s, mr := testSandbox(t)
	p := filepath.Join(mr, "media/extra/Pack")
	write(t, filepath.Join(p, "x.mkv"), 3)
	write(t, filepath.Join(mr, "media/extra/free.mkv"), 3)
	b := &Browser{Sandbox: s, Texts: &fakeTexts{holds: map[string]HoldRef{p: {Addon: "x", Label: "Нужно расширению", Active: true}}}}
	l, err := b.List(context.Background(), "library", "extra", ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*HoldRef{}
	for _, e := range l.Entries {
		got[e.Name] = e.Hold
	}
	if h := got["Pack"]; h == nil || h.Addon != "x" || h.Label != "Нужно расширению" || !h.Active {
		t.Fatalf("Pack hold %+v", h)
	}
	if got["free.mkv"] != nil {
		t.Fatalf("free.mkv hold %+v", got["free.mkv"])
	}
}

// A symlink leading out of the roots is never asked about.
func TestHoldLookupOnlyInsideRoots(t *testing.T) {
	s, mr := testSandbox(t)
	out := t.TempDir()
	write(t, filepath.Join(out, "x.mkv"), 3)
	if err := os.Symlink(out, filepath.Join(mr, "media/out")); err != nil {
		t.Fatal(err)
	}
	if s.holdable(filepath.Join(mr, "media/out")) || s.holdable(filepath.Join(mr, "media/out/x.mkv")) {
		t.Fatal("escaping path holdable")
	}
	if s.holdable(filepath.Join(mr, ".vault-trash/x")) || s.holdable("media/x") || s.holdable(out) {
		t.Fatal("trash, relative or outside path holdable")
	}
	if !s.holdable(filepath.Join(mr, "media/movies")) {
		t.Fatal("root folder not holdable")
	}
}

// heldEnv: a movie made from a held pack of three files; the addon
// selected two of them.
func heldEnv(t *testing.T) (e *itemEnv, it library.Item, pack, a, b, c string) {
	e = newItemEnv(t)
	m := filepath.Join(e.mv, "Брат (1997)", "Брат (1997).mkv")
	write(t, m, 4)
	pack = filepath.Join(e.mr, "media/extra/Brat.Pack")
	a, b, c = filepath.Join(pack, "Brat.mkv"), filepath.Join(pack, "Brat.ru.srt"), filepath.Join(pack, "Other.mkv")
	write(t, a, 4)
	write(t, b, 1)
	write(t, c, 7)
	e.rescan(t)
	it = e.itemOf(t, m)
	return
}

// Item delete trashes the listed (selected) files of a hold, never its
// root nor another file under it.
func TestHoldLinkedPathsTrashesListedOnly(t *testing.T) {
	e, it, pack, a, b, c := heldEnv(t)
	ctx := context.Background()
	set, _ := e.d.Library.DeleteSet(ctx, it.ID)
	e.d.LinkFiles = func(_ context.Context, key string) ([]library.File, error) {
		if key == "x:k" {
			return set.Files, nil
		}
		return nil, nil
	}
	be := &batchEngine{plan: map[string][]string{a: {"x:k"}, b: {"x:k"}}}
	e.d.Ops.Holders = be
	e.d.Linked = func(context.Context, library.DeleteSet) []Linked {
		return []Linked{{Path: pack, Key: "x:k", Paths: []string{a, b}}}
	}
	res, err := e.d.Delete(ctx, it.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if exists(a) || exists(b) || !exists(c) || !exists(pack) {
		t.Fatalf("a %v b %v c %v pack %v", exists(a), exists(b), exists(c), exists(pack))
	}
	if len(res.Trashed) != 3 {
		t.Fatalf("trashed %+v", res.Trashed)
	}
	if len(be.pauses) != 1 || len(be.stops) != 1 {
		t.Fatalf("pauses %v stops %v", be.pauses, be.stops)
	}
}

// Listed paths that are the root itself, outside it or lead out of the
// roots through a symlink are left alone.
func TestHoldLinkedPathsRefusesEscapes(t *testing.T) {
	e, it, pack, a, _, c := heldEnv(t)
	ctx := context.Background()
	set, _ := e.d.Library.DeleteSet(ctx, it.ID)
	e.d.LinkFiles = func(context.Context, string) ([]library.File, error) { return set.Files, nil }
	out := t.TempDir()
	write(t, filepath.Join(out, "x.mkv"), 1)
	link := filepath.Join(pack, "link")
	if err := os.Symlink(out, link); err != nil {
		t.Fatal(err)
	}
	beside := filepath.Join(e.mr, "media/extra/beside.mkv")
	write(t, beside, 1)
	e.d.Linked = func(context.Context, library.DeleteSet) []Linked {
		return []Linked{{Path: pack, Key: "x:k", Paths: []string{pack, a, filepath.Join(link, "x.mkv"), beside, "rel.mkv"}}}
	}
	if _, err := e.d.Delete(ctx, it.ID, 1); err != nil {
		t.Fatal(err)
	}
	if exists(a) || !exists(c) || !exists(pack) || !exists(beside) || !exists(filepath.Join(out, "x.mkv")) {
		t.Fatalf("a %v c %v pack %v beside %v out %v", exists(a), exists(c), exists(pack), exists(beside), exists(filepath.Join(out, "x.mkv")))
	}
}

// A listed folder is not trashed by item delete: it would take the
// unselected files in it along.
func TestHoldLinkedPathsRefusesFolders(t *testing.T) {
	e, it, pack, a, _, _ := heldEnv(t)
	ctx := context.Background()
	set, _ := e.d.Library.DeleteSet(ctx, it.ID)
	e.d.LinkFiles = func(context.Context, string) ([]library.File, error) { return set.Files, nil }
	sub := filepath.Join(pack, "Extras")
	write(t, filepath.Join(sub, "unselected.mkv"), 2)
	e.d.Linked = func(context.Context, library.DeleteSet) []Linked {
		return []Linked{{Path: pack, Key: "x:k", Paths: []string{sub, a}}}
	}
	if _, err := e.d.Delete(ctx, it.ID, 1); err != nil {
		t.Fatal(err)
	}
	if exists(a) || !exists(filepath.Join(sub, "unselected.mkv")) {
		t.Fatalf("a %v sub %v", exists(a), exists(filepath.Join(sub, "unselected.mkv")))
	}
}

// A Listed link trashes only its Paths: with none usable, nothing of it
// (never its Path); an unlisted one with no Paths keeps the old fallback.
func TestHoldListedNeverFallsBackToPath(t *testing.T) {
	for _, tc := range []struct {
		name  string
		l     func(pack, sub string) Linked
		packs bool // the pack stays
	}{
		{"listed, no paths", func(pack, _ string) Linked { return Linked{Path: pack, Key: "x:k", Listed: true} }, true},
		{"listed, all refused", func(pack, sub string) Linked {
			return Linked{Path: pack, Key: "x:k", Listed: true, Paths: []string{pack, sub}}
		}, true},
		{"unlisted, no paths", func(pack, _ string) Linked { return Linked{Path: pack, Key: "x:k"} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, it, pack, a, _, c := heldEnv(t)
			ctx := context.Background()
			set, _ := e.d.Library.DeleteSet(ctx, it.ID)
			e.d.LinkFiles = func(context.Context, string) ([]library.File, error) { return set.Files, nil }
			sub := filepath.Join(pack, "Extras")
			write(t, filepath.Join(sub, "x.mkv"), 2)
			e.d.Linked = func(context.Context, library.DeleteSet) []Linked { return []Linked{tc.l(pack, sub)} }
			if _, err := e.d.Delete(ctx, it.ID, 1); err != nil {
				t.Fatal(err)
			}
			if exists(pack) != tc.packs || tc.packs && (!exists(a) || !exists(c)) {
				t.Fatalf("pack %v a %v c %v", exists(pack), exists(a), exists(c))
			}
		})
	}
}

// coversLinked finds what was made from the data by its key through
// LinkFiles, with no item id.
func TestHoldLinkedCoversByKeyItemZero(t *testing.T) {
	e, it, pack, _, _, _ := heldEnv(t)
	ctx := context.Background()
	set, _ := e.d.Library.DeleteSet(ctx, it.ID)
	l := Linked{Path: pack, Key: "x:k"}
	if e.d.coversLinked(ctx, set, l) {
		t.Fatal("covered without LinkFiles (nothing carries the key)")
	}
	var asked []string
	e.d.LinkFiles = func(_ context.Context, key string) ([]library.File, error) {
		asked = append(asked, key)
		return set.Files, nil
	}
	if !e.d.coversLinked(ctx, set, l) || len(asked) != 1 || asked[0] != "x:k" {
		t.Fatalf("not covered by LinkFiles (asked %v)", asked)
	}
	e.d.LinkFiles = func(context.Context, string) ([]library.File, error) {
		return append(append([]library.File{}, set.Files...), library.File{ID: -5}), nil
	}
	if e.d.coversLinked(ctx, set, l) {
		t.Fatal("covered while another file was made from it")
	}
	e.d.LinkFiles = func(context.Context, string) ([]library.File, error) { return nil, errors.New("db") }
	if e.d.coversLinked(ctx, set, l) {
		t.Fatal("covered on an error")
	}
}

// The notes of a delete use the addon's texts, falling back to the build's.
func TestHoldNotesTexts(t *testing.T) {
	e, it, pack, _, _, _ := heldEnv(t)
	ctx := context.Background()
	set, _ := e.d.Library.DeleteSet(ctx, it.ID)
	e.d.Linked = func(context.Context, library.DeleteSet) []Linked { return []Linked{{Path: pack, Key: "x:k"}} }
	e.d.Ops.Texts = &fakeTexts{move: "уйдёт", kept: "останется"}
	if n := e.d.Notes(ctx, set); len(n) != 1 || n[0] != "останется x:k Brat.Pack" {
		t.Fatalf("kept: %v", n)
	}
	e.d.LinkFiles = func(context.Context, string) ([]library.File, error) { return set.Files, nil }
	if n := e.d.Notes(ctx, set); len(n) != 1 || n[0] != "уйдёт Brat.Pack" {
		t.Fatalf("goes: %v", n)
	}
	e.d.Ops.Texts = &fakeTexts{holds: map[string]HoldRef{pack: {Addon: "x"}}}
	if n := e.d.Notes(ctx, set); len(n) != 1 || n[0] != "Эти данные использует расширение «x»" {
		t.Fatalf("neutral: %v", n)
	}
	e.d.Ops.Texts = &fakeTexts{}
	n := e.d.Notes(ctx, set)
	if len(n) != 1 || n[0] != "Эти данные используются" {
		t.Fatalf("fallback: %v", n)
	}
}

// The hold deadline covers asking and holding only: sizes and the trash's
// rows use the request's ctx, and settling after the move gets a fresh
// deadline of its own.
func TestHoldDeadlineSparesTheWork(t *testing.T) {
	old := holdOpTimeout
	holdOpTimeout = 150 * time.Millisecond
	t.Cleanup(func() { holdOpTimeout = old })
	o, mr, _ := testOps(t)
	p := filepath.Join(mr, "media/extra/a")
	write(t, filepath.Join(p, "x.mkv"), 3)
	be := &batchEngine{plan: map[string][]string{p: {"x:a"}}, pauseWait: 200 * time.Millisecond}
	o.Holders = be
	res, err := o.Trash(context.Background(), "library", []string{"extra/a"}, true, 1)
	if err != nil || len(res.Trashed) != 1 || res.Trashed[0].Size != 3 {
		t.Fatalf("trash %+v %v", res, err)
	}
	if items, err := o.ListTrash(context.Background()); err != nil || len(items) != 1 {
		t.Fatalf("trash rows %+v %v", items, err)
	}
	if len(be.stops) != 1 {
		t.Fatalf("stops %v", be.stops)
	}
	if d := be.deadlines[len(be.deadlines)-1]; d <= 0 || d > holdOpTimeout {
		t.Fatalf("StopAll's deadline: %v left", d)
	}
}

// Nothing uses the data: the move is a plain rename, no holding.
func TestHoldNothingPlannedPlainRename(t *testing.T) {
	o, mr, _ := testOps(t)
	write(t, filepath.Join(mr, "media/extra/a/x.mkv"), 3)
	be := &batchEngine{}
	o.Holders = be
	if err := o.Rename(context.Background(), "library", "extra/a", "b", true); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(mr, "media/extra/b/x.mkv")) || len(be.pauses)+len(be.stops)+len(be.resumes) != 0 {
		t.Fatalf("pauses %v stops %v resumes %v", be.pauses, be.stops, be.resumes)
	}
}
