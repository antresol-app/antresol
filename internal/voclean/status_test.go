package voclean

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// «Обработать всю медиатеку»: the backlog runs while something plays,
// like a fresh file (its read does not yield to playback either).
func TestWorkerBackfillRunsDuringPlayback(t *testing.T) {
	fx := newWFix(t)
	fx.w.Busy = func() bool { return true }
	on := false
	fx.w.Backfill = func(context.Context) bool { return on }
	fx.file("old", fx.itemAt("movie", 0, "Old", fx.now.Add(-72*time.Hour), 0, 0), probeOf(lfEng, "eng:"))
	fx.pass()
	if c := fx.takeCalls(); len(c) != 0 || !fx.w.heldForPlayback {
		t.Fatalf("backfill off: %v, held %v", c, fx.w.heldForPlayback)
	}
	if st, err := fx.w.Status(context.Background()); err != nil || st.Queued != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	on = true
	fx.pass()
	if c := fx.takeCalls(); !reflect.DeepEqual(c, []string{"measure old/1", "clean old/1"}) {
		t.Fatalf("backfill on: %v", c)
	}
	if fx.gated["old"] || fx.w.heldForPlayback {
		t.Fatalf("backfill gated: %v, held %v", fx.gated, fx.w.heldForPlayback)
	}
}

// Status counts by state; Recheck queues the not eligible and the given
// up tracks again (and only those).
func TestWorkerStatusAndRecheck(t *testing.T) {
	fx := newWFix(t)
	ctx := context.Background()
	fx.file("ok", fx.item("movie", 0, "OK", 9, 0, 0), probeOf(lfEng, "eng:"))
	fx.file("no", fx.item("movie", 0, "No", 8, 0, 0), probeOf(lfEng, "eng:"))
	fx.file("bad", fx.item("movie", 0, "Bad", 7, 0, 0), probeOf(lfEng, "eng:"))
	fx.file("retry", fx.item("movie", 0, "Retry", 6, 0, 0), probeOf(lfEng, "eng:"))
	fx.notEligible["no/1"] = true
	// before any pass: the rows alone (none yet)
	if st, err := fx.w.Status(ctx); err != nil || st != (Status{}) {
		t.Fatalf("%+v %v", st, err)
	}
	fx.pass()
	fx.takeCalls()
	// bad: given up after maxFails; retry: failed once, retried later
	fx.exec(`UPDATE cleaned_audio SET state='failed', fails=5, retry_at=NULL WHERE file_id=?`, fx.files["bad"])
	fx.exec(`UPDATE cleaned_audio SET state='failed', fails=1, retry_at='2099-01-01T00:00:00Z' WHERE file_id=?`, fx.files["retry"])
	st, err := fx.w.Status(ctx)
	if err != nil || st != (Status{Done: 1, Failed: 2, NotEligible: 1, Queued: 0}) {
		t.Fatalf("%+v %v", st, err)
	}
	n, err := fx.w.Recheck(ctx)
	if err != nil || n != 2 {
		t.Fatalf("recheck %d %v", n, err)
	}
	if s, f, _ := fx.state("no", 1); s != "queued" || f != 0 {
		t.Fatalf("no: %s %d", s, f)
	}
	if s, f, _ := fx.state("bad", 1); s != "queued" || f != 0 {
		t.Fatalf("bad: %s %d", s, f)
	}
	if s, _, _ := fx.state("retry", 1); s != "failed" {
		t.Fatalf("retry: %s", s)
	}
	if st, _ := fx.w.Status(ctx); st.Queued != 2 || st.NotEligible != 0 || st.Failed != 1 {
		t.Fatalf("after recheck %+v", st)
	}
	select {
	case <-fx.w.kick:
	default:
		t.Fatal("recheck did not kick a pass")
	}
	delete(fx.notEligible, "no/1")
	fx.pass()
	if st, _ := fx.w.Status(ctx); st.Done != 3 || st.Queued != 0 || st.Failed != 1 {
		t.Fatalf("after the pass %+v", st)
	}
}
