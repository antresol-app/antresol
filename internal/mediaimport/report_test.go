package mediaimport

import (
	"reflect"
	"regexp"
	"testing"
	"time"
)

func TestReportSaveLoad(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 25, 19, 30, 0, 0, time.UTC)
	old := &Report{ID: NewReportID(t0), Sources: []string{"jellyfin"}, DryRun: true, StartedAt: t0, FinishedAt: t0.Add(time.Minute)}
	old.Add(Line{Source: "jellyfin", Kind: "movie", Name: "A", Status: StatusPlanned, Size: 10})
	if err := SaveReport(dir, old); err != nil {
		t.Fatal(err)
	}
	t1 := t0.Add(time.Hour)
	r := &Report{ID: NewReportID(t1), Sources: []string{"jellyfin"}, PlanID: old.ID, PlanHash: "h", StartedAt: t1, FinishedAt: t1.Add(time.Minute)}
	r.Add(Line{Source: "jellyfin", Kind: "movie", Name: "Базарный день", Status: StatusImported, Ref: "ab", Size: 5})
	r.Add(Line{Source: "jellyfin", Kind: "movie", Name: "B", Status: StatusImported, Ref: "cd", Size: 6})
	r.Add(Line{Source: "jellyfin", Kind: "movie", Name: "C", Status: StatusFailed, Reason: "Нет папки /x"})
	if err := SaveReport(dir, r); err != nil {
		t.Fatal(err)
	}
	got, err := LoadReport(dir, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != r.ID || got.PlanID != r.PlanID || got.PlanHash != r.PlanHash || !got.StartedAt.Equal(r.StartedAt) ||
		!reflect.DeepEqual(got.Lines, r.Lines) || !reflect.DeepEqual(got.Counts, r.Counts) || !reflect.DeepEqual(got.Sources, r.Sources) {
		t.Fatalf("loaded %+v, saved %+v", got, r)
	}
	if got.Counts["imported"] != 2 || got.Counts["failed"] != 1 {
		t.Fatalf("counts %v", got.Counts)
	}
	list, err := ListReports(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != r.ID || list[1].ID != old.ID {
		t.Fatalf("list order %v", list)
	}
	if list[0].Lines != nil || list[0].Counts["imported"] != 2 || list[1].Counts["planned"] != 1 {
		t.Fatalf("list entry %+v", list[0])
	}
	if _, err := LoadReport(dir, "../x"); err == nil {
		t.Fatal("bad id accepted")
	}
}

func TestReportID(t *testing.T) {
	id := NewReportID(time.Date(2026, 10, 25, 19, 30, 0, 0, time.FixedZone("x", 3600)))
	if !regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{6}$`).MatchString(id) {
		t.Fatalf("id %q", id)
	}
	if id[:16] != "20261025T183000Z" {
		t.Fatalf("id %q is not UTC", id)
	}
}
