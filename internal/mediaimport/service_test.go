package mediaimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
)

// fakeSource stands in for a source the sources build registers.
type fakeSource struct {
	id    string
	order int
	path  string
	block chan struct{} // Import waits for it to close
}

func (f *fakeSource) ID() string                     { return f.id }
func (f *fakeSource) Name() string                   { return strings.ToUpper(f.id) }
func (f *fakeSource) Order() int                     { return f.order }
func (f *fakeSource) Path() string                   { return f.path }
func (f *fakeSource) IsDir() bool                    { return false }
func (f *fakeSource) Problem(context.Context) string { return "" }
func (f *fakeSource) Detail(context.Context, string) (string, error) {
	return "fake", nil
}
func (f *fakeSource) Import(ctx context.Context, r Run) error {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.Report.Add(Line{Source: f.id, Kind: "source", Name: f.Name(), Status: StatusPlanned})
	return nil
}

type svcEnv struct {
	t       *testing.T
	s       *Service
	dir     string
	reports string
	admin   auth.User
}

func newSvcEnv(t *testing.T) *svcEnv {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	users := auth.NewStore(d)
	admin, err := users.CreateFirstAdmin(context.Background(), "Александр", "семья-2026")
	if err != nil {
		t.Fatal(err)
	}
	e := &svcEnv{t: t, dir: t.TempDir(), reports: filepath.Join(t.TempDir(), "migrate"), admin: admin}
	jf, kv, abs := DefaultPaths(e.dir)
	ctx, cancel := context.WithCancel(context.Background())
	e.s = NewService(&Service{Profiles: users, Library: library.NewRepo(d), Progress: progress.New(d),
		Jellyfin: jf, Kavita: kv, ABS: abs, ReportDir: e.reports, TmpDir: filepath.Join(t.TempDir(), "tmp"), BaseCtx: ctx})
	t.Cleanup(func() { cancel(); e.s.Wait() })
	return e
}

func ids(srcs []Source) []string {
	var out []string
	for _, s := range srcs {
		out = append(out, s.ID())
	}
	return out
}

func TestCoreSourcesOnly(t *testing.T) {
	e := newSvcEnv(t)
	if got := strings.Join(ids(e.s.list()), " "); got != "jellyfin kavita audiobookshelf" {
		t.Fatalf("sources %q", got)
	}
	st := e.s.Discover(context.Background())
	if len(st) != 3 || st[0].Name != "Jellyfin" || st[1].Name != "Kavita" || st[2].Name != "Audiobookshelf" {
		t.Fatalf("discover %+v", st)
	}
}

func TestRegisterOrdersSources(t *testing.T) {
	e := newSvcEnv(t)
	e.s.Register(&fakeSource{id: "fake90", order: 90})
	e.s.Register(&fakeSource{id: "fake10", order: 10})
	if got := strings.Join(ids(e.s.list()), " "); got != "fake10 jellyfin kavita audiobookshelf fake90" {
		t.Fatalf("sources %q", got)
	}
	// a source registered again replaces the earlier one
	e.s.Register(&fakeSource{id: "fake10", order: 95})
	if got := strings.Join(ids(e.s.list()), " "); got != "jellyfin kavita audiobookshelf fake90 fake10" {
		t.Fatalf("re-registered %q", got)
	}
}

func (e *svcEnv) finish(id string) *Report {
	e.t.Helper()
	e.s.Wait()
	rep, err := LoadReport(e.reports, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return rep
}

func TestRunNeedsDryRun(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	req := RunRequest{Sources: []string{"jellyfin"}, AdminID: e.admin.ID}
	if _, err := e.s.Start(ctx, req); !errors.Is(err, ErrNeedsDryRun) {
		t.Fatalf("no plan: %v", err)
	}
	req.DryRun = true
	id, err := e.s.Start(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	plan := e.finish(id)
	if !plan.DryRun || plan.FinishedAt.IsZero() || plan.PlanHash == "" {
		t.Fatalf("plan %+v", plan)
	}
	// a plan of other sources does not allow it
	if _, err := e.s.Start(ctx, RunRequest{Sources: []string{"kavita"}, PlanID: plan.ID, AdminID: e.admin.ID}); !errors.Is(err, ErrNeedsDryRun) {
		t.Fatalf("other sources: %v", err)
	}
	id, err = e.s.Start(ctx, RunRequest{Sources: []string{"jellyfin"}, PlanID: plan.ID, AdminID: e.admin.ID})
	if err != nil {
		t.Fatalf("with the plan: %v", err)
	}
	if real := e.finish(id); real.DryRun || real.PlanID != plan.ID || real.FinishedAt.IsZero() {
		t.Fatalf("real %+v", real)
	}
}

func TestBeginBusy(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	end, err := e.s.Begin("x")
	if err != nil {
		t.Fatal(err)
	}
	if e.s.Running() != "x" {
		t.Fatalf("running %q", e.s.Running())
	}
	req := RunRequest{Sources: []string{"jellyfin"}, DryRun: true, AdminID: e.admin.ID}
	if _, err := e.s.Start(ctx, req); !errors.Is(err, ErrBusy) {
		t.Fatalf("while held: %v", err)
	}
	if _, err := e.s.Begin(""); !errors.Is(err, ErrBusy) {
		t.Fatalf("a switch while held: %v", err)
	}
	end()
	if e.s.Running() != "" {
		t.Fatalf("running %q after end", e.s.Running())
	}
	id, err := e.s.Start(ctx, req)
	if err != nil {
		t.Fatalf("after end: %v", err)
	}
	e.finish(id)
	// a switch holds it without showing a report
	end, err = e.s.Begin("")
	if err != nil || e.s.Running() != "" {
		t.Fatalf("switch: %v %q", err, e.s.Running())
	}
	if _, err := e.s.Start(ctx, req); !errors.Is(err, ErrBusy) {
		t.Fatalf("while switching: %v", err)
	}
	end()
}

func TestUnknownSourceInCore(t *testing.T) {
	e := newSvcEnv(t)
	for _, src := range [][]string{{"nosuch"}, {"jellyfin", "../x"}} {
		if _, err := e.s.Start(context.Background(), RunRequest{Sources: src, DryRun: true, AdminID: e.admin.ID}); !errors.Is(err, ErrUnknownSource) {
			t.Fatalf("%v: %v", src, err)
		}
	}
	if _, err := e.s.Start(context.Background(), RunRequest{DryRun: true, AdminID: e.admin.ID}); !errors.Is(err, ErrNoSources) {
		t.Fatalf("none: %v", err)
	}
}

func TestRunsRegisteredSourcesInOrder(t *testing.T) {
	e := newSvcEnv(t)
	last := &fakeSource{id: "last", order: 90}
	e.s.Register(last)
	e.s.Register(&fakeSource{id: "first", order: 10})
	id, err := e.s.Start(context.Background(), RunRequest{Sources: []string{"last", "jellyfin", "first"}, DryRun: true, AdminID: e.admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	rep := e.finish(id)
	if strings.Join(rep.Sources, " ") != "first jellyfin last" {
		t.Fatalf("sources %v", rep.Sources)
	}
	var got []string
	for _, l := range rep.Lines {
		got = append(got, l.Source)
	}
	if !strings.Contains(strings.Join(got, " "), "first") || !strings.Contains(strings.Join(got, " "), "last") {
		t.Fatalf("lines %+v", rep.Lines)
	}
}

func TestLeftoversJellyfinChannels(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	writeFile(t, e.s.Jellyfin, "")
	if got := e.s.Leftovers(ctx); len(got) != 0 {
		t.Fatalf("empty file: %+v", got)
	}
	src := jellyfinDB(t, nil, []jfItem{{ID: "c1", Type: jfChannel, Name: "Первый"}, {ID: "c2", Type: jfChannel, Name: "Второй"}}, nil)
	b := mustRead(t, src)
	writeFile(t, e.s.Jellyfin, string(b))
	got := e.s.Leftovers(ctx)
	if len(got) != 1 || got[0].Reason != "Jellyfin: ТВ-каналы не переносятся (2 канала)" || got[0].Status != StatusWarning {
		t.Fatalf("leftovers %+v", got)
	}
	// the sources build's lines come first; the importer writes the channels
	// line itself when Jellyfin runs
	e.s.ExtraLeftovers = func(_ context.Context, _ string, running []string) []Line {
		return []Line{{Source: "librarr", Kind: "leftover", Status: StatusWarning, Reason: strings.Join(running, ",")}}
	}
	tmp := t.TempDir()
	if got := e.s.leftovers(ctx, tmp, nil); len(got) != 2 || got[0].Source != "librarr" || got[1].Source != SourceJellyfin {
		t.Fatalf("with extra %+v", got)
	}
	if got := e.s.leftovers(ctx, tmp, []string{SourceJellyfin}); len(got) != 1 || got[0].Reason != SourceJellyfin {
		t.Fatalf("jellyfin running %+v", got)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
