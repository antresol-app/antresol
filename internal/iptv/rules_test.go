package iptv

import (
	"context"
	"testing"
)

func TestIdentPrefersTvgIDThenURLThenName(t *testing.T) {
	for _, c := range []struct {
		e    Entry
		want string
	}{
		{Entry{TvgID: "demo.one", Name: "Demo One", URL: "http://example.test/live/u/p/1.ts"}, "tvg:demo.one"},
		{Entry{Name: "Demo One", URL: "http://u:p@example.test/live/u/p/1.ts?token=t"}, "url:http://example.test/live/1.ts"},
		{Entry{Name: "Demo One HD"}, "name:demo one"},
		{Entry{Name: "Demo One HD", URL: "not a url"}, "name:demo one"},
	} {
		if got := Ident(c.e); got != c.want {
			t.Errorf("Ident(%+v) = %q, want %q", c.e, got, c.want)
		}
	}
}

func shownOf(d Decision) string {
	switch {
	case d.Shown == nil:
		return "nil"
	case *d.Shown:
		return "shown"
	}
	return "hidden"
}

func TestPresetsHideTimeshiftAndAdult(t *testing.T) {
	src := Source{PresetTimeshift: true, PresetAdult: true}
	for _, c := range []struct {
		e   Entry
		why string
	}{
		{Entry{Name: "Тестовый +2"}, WhyTimeshift},
		{Entry{Name: "Кино +4", Group: "Кино"}, WhyTimeshift},
		{Entry{Name: "Пример +12 HD"}, WhyTimeshift},
		{Entry{Name: "Пример Орбита"}, WhyTimeshift},
		{Entry{Name: "Пример (Дубль)"}, WhyTimeshift},
		{Entry{Name: "Demo -2h"}, WhyTimeshift},
		{Entry{Name: "Ночной", Group: "18+"}, WhyAdult},
		{Entry{Name: "Demo XXX"}, WhyAdult},
		{Entry{Name: "Кино", Group: "Для взрослых"}, WhyAdult},
		{Entry{Name: "Эротика ТВ"}, WhyAdult},
	} {
		d := Defaults(src, c.e, 10)
		if shownOf(d) != "hidden" || d.Why != c.why {
			t.Errorf("%+v: %s %q, want hidden %q", c.e, shownOf(d), d.Why, c.why)
		}
	}
	// Not copies: «+» without a number, numbers that are names, «Канал 24».
	for _, name := range []string{"Пример+", "Канал 24", "Demo 2", "Demo +20", "Плюс Кино", "Sample+ HD"} {
		if d := Defaults(src, Entry{Name: name}, 10); shownOf(d) != "shown" || d.Why != WhySourceDefault {
			t.Errorf("%q: %s %q", name, shownOf(d), d.Why)
		}
	}
	// Presets off: shown by default.
	off := Source{}
	if d := Defaults(off, Entry{Name: "Тестовый +2", Group: "18+"}, 10); shownOf(d) != "shown" {
		t.Errorf("presets off: %s", shownOf(d))
	}
}

func TestDefaultsBySize(t *testing.T) {
	e := Entry{Name: "Demo", Group: "News"}
	if d := Defaults(Source{}, e, 299); shownOf(d) != "shown" || d.Group != "News" || d.Name != "Demo" || d.Why != WhySourceDefault {
		t.Errorf("small: %+v", d)
	}
	if d := Defaults(Source{}, e, 300); shownOf(d) != "hidden" || d.Why != WhySourceDefault {
		t.Errorf("big: %s", shownOf(d))
	}
	yes, no := true, false
	if d := Defaults(Source{DefaultShown: &yes}, e, 5000); shownOf(d) != "shown" {
		t.Errorf("big, shown by choice: %s", shownOf(d))
	}
	if d := Defaults(Source{DefaultShown: &no}, e, 5); shownOf(d) != "hidden" {
		t.Errorf("small, hidden by choice: %s", shownOf(d))
	}
}

func TestApplyManual(t *testing.T) {
	yes, no := true, false
	def := Decision{Shown: &yes, Group: "News", Name: "Demo", Why: WhySourceDefault}
	if got := ApplyManual(def, nil); got.Why != WhySourceDefault || shownOf(got) != "shown" {
		t.Fatalf("no manual: %+v", got)
	}
	got := ApplyManual(def, &Decision{Shown: &no, Number: 150})
	if shownOf(got) != "hidden" || got.Number != 150 || got.Group != "News" || got.Name != "Demo" || got.Why != WhyManual {
		t.Fatalf("manual: %+v", got)
	}
	got = ApplyManual(def, &Decision{Group: "Кино", Name: "Своё имя"})
	if shownOf(got) != "shown" || got.Group != "Кино" || got.Name != "Своё имя" || got.Why != WhyManual {
		t.Fatalf("manual group/name: %+v", got)
	}
	if got := ApplyManual(def, &Decision{}); got.Why != WhySourceDefault {
		t.Fatalf("empty manual: %+v", got)
	}
}

// Adult groups by whole-word stems («Взрослые» is one); a name stays
// strict («Adult Swim» is a cartoon channel); the admin's word on a group
// wins either way, and is kept with the source.
func TestAdultGroups(t *testing.T) {
	for _, g := range []string{"Взрослые", "ВЗРОСЛЫЕ HD", "RU | Взрослое", "Adult", "Adults only", "Porn", "PORNO", "Порно", "Эротические", "Erotic", "NSFW", "XXX", "18+"} {
		if !IsAdultGroup(g) {
			t.Errorf("group %q is not adult", g)
		}
	}
	for _, g := range []string{"Кино", "Детские", "Sport", "Новости 18", "Superporno", "Неэротичное"} {
		if IsAdultGroup(g) {
			t.Errorf("group %q is adult", g)
		}
	}
	if IsAdult("Adult Swim", "Мультфильмы") || IsAdult("Porn Hub Fake", "") {
		t.Error("a name with a group-only stem is adult")
	}
	src := Source{PresetAdult: true, AdultGroups: map[string]bool{"Премиум": true, "Взрослые": false}}
	if d := Defaults(src, Entry{Name: "Ночь", Group: "Премиум"}, 10); d.Why != WhyAdult || shownOf(d) != "hidden" {
		t.Errorf("marked group: %s %q", shownOf(d), d.Why)
	}
	if d := Defaults(src, Entry{Name: "Ночь", Group: "Взрослые"}, 10); d.Why != WhySourceDefault {
		t.Errorf("unmarked group: %q", d.Why)
	}
	if d := Defaults(Source{PresetAdult: true}, Entry{Name: "Ночь", Group: "Взрослые"}, 10); d.Why != WhyAdult {
		t.Errorf("взрослые: %q", d.Why)
	}
	if !src.EntryAdult("Ночь", "Премиум") || src.EntryAdult("Ночь", "Взрослые") || !src.EntryAdult("Demo XXX", "Взрослые") {
		t.Error("EntryAdult")
	}

	ctx := context.Background()
	s := newTestStore(t)
	saved, err := s.SaveSource(ctx, Source{Name: "Synthetic", Kind: KindM3U})
	if err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	if err := s.SetAdultGroup(ctx, saved.ID, "Премиум", &yes); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAdultGroup(ctx, saved.ID, "Взрослые", &no); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAdultGroup(ctx, saved.ID, "Взрослые", nil); err != nil { // back to automatic
		t.Fatal(err)
	}
	got, err := s.Source(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.AdultGroups) != 1 || !got.AdultGroups["Премиум"] {
		t.Fatalf("stored overrides %v", got.AdultGroups)
	}
	if _, err := s.SaveSource(ctx, got); err != nil { // a save keeps them
		t.Fatal(err)
	}
	if got, _ = s.Source(ctx, saved.ID); !got.AdultGroups["Премиум"] {
		t.Fatalf("lost on save: %v", got.AdultGroups)
	}
	if err := s.DeleteSource(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDedupeKey(t *testing.T) {
	if DedupeKey("Demo One HD") != "ext:demo one" || DedupeKey("Demo One") != DedupeKey("demo one FHD") {
		t.Fatal(DedupeKey("Demo One HD"))
	}
	if DedupeKey("Demo +2") == DedupeKey("Demo") {
		t.Fatal("a +2 copy dedupes with the channel")
	}
}

// Review Focus 3: the provider renames a channel; with the same tvg-id it
// is the same member, and the admin's decision stays.
func TestManualSurvivesRename(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	src, err := s.SaveSource(ctx, Source{Name: "Synthetic", Kind: KindM3U})
	if err != nil {
		t.Fatal(err)
	}
	before := Entry{TvgID: "demo.first", Name: "Тестовый канал HD", Group: "Эфир", URL: "http://example.test/a.m3u8"}
	m := Member{Ident: Ident(before), TvgID: before.TvgID, Name: before.Name, Group: before.Group}
	if err := s.ReplaceMembers(ctx, src.ID, []Member{m}); err != nil {
		t.Fatal(err)
	}
	ms, _ := s.Members(ctx, src.ID)
	no := false
	manual := &Decision{Shown: &no, Number: 150, Group: "Избранное"}
	if err := s.SetManual(ctx, ms[0].ID, manual); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMemberEPG(ctx, ms[0].ID, 0, "guide.first", "manual", true); err != nil {
		t.Fatal(err)
	}

	after := Entry{TvgID: "demo.first", Name: "Тестовый HD", Group: "Эфир", URL: "http://example.test/b.m3u8"}
	if Ident(after) != Ident(before) {
		t.Fatalf("ident changed: %q → %q", Ident(before), Ident(after))
	}
	if err := s.ReplaceMembers(ctx, src.ID, []Member{{Ident: Ident(after), TvgID: after.TvgID, Name: after.Name, Group: after.Group}}); err != nil {
		t.Fatal(err)
	}
	// An automatic match does not replace the manual binding.
	ms, _ = s.Members(ctx, src.ID)
	if err := s.SetMemberEPG(ctx, ms[0].ID, 0, "guide.other", "norm-name", false); err != nil {
		t.Fatal(err)
	}
	ms, _ = s.Members(ctx, src.ID)
	if len(ms) != 1 {
		t.Fatalf("members: %+v", ms)
	}
	got := ms[0]
	if got.Name != "Тестовый HD" || got.Manual == nil || got.EPGID != "guide.first" || !got.EPGManual {
		t.Fatalf("after rename: %+v", got)
	}
	d := ApplyManual(Defaults(src, after, 1), got.Manual)
	if shownOf(d) != "hidden" || d.Number != 150 || d.Group != "Избранное" || d.Name != "Тестовый HD" || d.Why != WhyManual {
		t.Fatalf("decision after rename: %+v", d)
	}
}
