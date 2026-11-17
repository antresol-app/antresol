package mediaimport

import (
	"testing"

	"github.com/antresol-app/antresol/internal/mediaimport/runkit"
)

// TestAliasesSame: the leaf package's types are the migration's own
// (assignment both ways compiles), and its errors keep their identity.
func TestAliasesSame(t *testing.T) {
	var r *Report = &runkit.Report{ID: "x"}
	var rr *runkit.Report = r
	var _ runkit.Line = Line{}
	var _ runkit.Run = Run{Report: r}
	var _ runkit.RunRequest = RunRequest{}
	var _ runkit.SourceStatus = SourceStatus{}
	var _ runkit.Progress = Progress{}
	var _ runkit.TableCount = TableCount{}
	var _ runkit.PathMap = PathMap{}
	var _ runkit.Source = Source(nil)
	var _ error = &runkit.UserError{}
	var _ *UserError = &runkit.UserError{}
	if rr.ID != "x" {
		t.Fatal("report not carried")
	}
	for _, e := range []struct{ old, leaf error }{
		{ErrNeedsDryRun, runkit.ErrNeedsDryRun}, {ErrNotFound, runkit.ErrNotFound},
		{ErrBusy, runkit.ErrBusy}, {ErrUnknownSource, runkit.ErrUnknownSource},
		{ErrUnknownProfile, runkit.ErrUnknownProfile}, {ErrNotReady, runkit.ErrNotReady},
		{ErrNoSources, runkit.ErrNoSources},
	} {
		if e.old != e.leaf {
			t.Errorf("%v: not the leaf package's error", e.old)
		}
	}
	if StatusPlanned != runkit.StatusPlanned || PlanHash([]string{"a"}, nil) != runkit.PlanHash([]string{"a"}, nil) {
		t.Error("values not re-exported")
	}
}
