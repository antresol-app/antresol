package iptv

import (
	"context"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
)

func TestVariantNameBaseAndRank(t *testing.T) {
	for _, c := range []struct{ name, base string }{
		{"Пример 1", "пример 1"},
		{"Пример 1 HD", "пример 1"},
		{"Пример 1 FHD", "пример 1"},
		{"Пример 1 UHD", "пример 1"},
		{"Пример 1 HD 50", "пример 1"},
		{"Пример 1 HD orig", "пример 1"},
		{"Пример 1 orig", "пример 1"},
		{"Пример 1 +0 (Регион)", "пример 1"},
		{"Пример 1 (HD)", "пример 1"},
		{"Пример 1 [FHD]", "пример 1"},
		{"Канал 24", "канал 24"},   // a number that is a name
		{"Канал 50", "канал 50"},   // «50» alone is not a frame rate
		{"HD", "hd"},               // never an empty base
		{"Пример (2)", "пример 2"}, // digits in brackets are not a region
	} {
		if got, _ := VariantName(c.name); got != c.base {
			t.Errorf("VariantName(%q) base = %q, want %q", c.name, got, c.base)
		}
	}
	rank := func(n string) int { _, r := VariantName(n); return r }
	order := []string{"Пример 1 FHD", "Пример 1 HD 50", "Пример 1 HD orig", "Пример 1 HD", "Пример 1 UHD", "Пример 1 orig", "Пример 1", "Пример 1 +0 (Регион)"}
	for i := 1; i < len(order); i++ {
		if rank(order[i-1]) <= rank(order[i]) {
			t.Errorf("rank %q (%d) should beat %q (%d)", order[i-1], rank(order[i-1]), order[i], rank(order[i]))
		}
	}
}

func TestVariantGroups(t *testing.T) {
	var ms []Member
	add := func(name, tvg string) {
		ms = append(ms, Member{ID: int64(len(ms) + 1), Name: name, TvgID: tvg, Present: true, Position: len(ms) + 1})
	}
	add("Пример 1 +0 (Регион А)", "")
	add("Пример 1 +0 (Регион Б)", "")
	add("Пример 1 +2", "") // timeshift: not a variant
	add("Пример 1 HD", "")
	add("Пример 1 FHD", "")
	add("Пример 24", "")     // another channel
	add("Другой", "same.id") // tvg-id joins different names
	add("Другой канал", "same.id")
	add("Кино XXX", "") // adult: never grouped
	add("Кино XXX HD", "")
	gs := VariantGroups(ms)
	var got []string
	for _, g := range gs {
		var ns []string
		for _, m := range g {
			ns = append(ns, m.Name)
		}
		got = append(got, strings.Join(ns, "|"))
	}
	want := []string{"Пример 1 FHD|Пример 1 HD|Пример 1 +0 (Регион А)|Пример 1 +0 (Регион Б)", "Другой|Другой канал"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("groups:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestVariantsAreOneChannelWithFallbacks(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	pl := "#EXTM3U\n"
	for _, n := range []string{"Пример +0 (Регион)", "Пример HD", "Пример FHD", "Пример UHD", "Пример +2", "Другой"} {
		pl += "#EXTINF:-1 group-title=\"Общие\"," + n + "\nhttp://a.example.test/" + strings.NewReplacer(" ", "_", "+", "p", "(", "", ")", "").Replace(n) + ".m3u8\n"
	}
	e.p.put("/a.m3u", pl)
	src := e.source("A", "/a.m3u", func(s *Source) { s.PresetVariants = true })
	e.refresh(src.ID)
	ms, err := e.svc.Store.Members(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	var lead int64
	why := map[string]string{}
	ch := map[string]int64{}
	for _, m := range ms {
		why[m.Name], ch[m.Name] = m.Why, m.ChannelID
		if m.Name == "Пример FHD" {
			lead = m.ChannelID
			if !m.Shown {
				t.Errorf("the lead is hidden")
			}
		}
	}
	for _, n := range []string{"Пример +0 (Регион)", "Пример HD", "Пример UHD"} {
		if why[n] != WhyVariant || ch[n] != lead {
			t.Errorf("%q: why %q row %d, want a variant on row %d", n, why[n], ch[n], lead)
		}
	}
	if why["Пример +2"] != WhyTimeshift || ch["Пример +2"] == lead {
		t.Errorf("timeshift copy: %q row %d", why["Пример +2"], ch["Пример +2"])
	}
	vis := e.visible(channels.Viewer{ID: e.user("v")}, 0)
	if names(vis) != "Другой,Пример FHD" {
		t.Fatalf("visible: %s", names(vis))
	}
	x := vis[lead]
	var urls []string
	for _, s := range x.Streams {
		urls = append(urls, s.URL[strings.LastIndex(s.URL, "/")+1:])
	}
	if got := strings.Join(urls, ","); got != "Пример_FHD.m3u8,Пример_HD.m3u8,Пример_UHD.m3u8,Пример_p0_Регион.m3u8" {
		t.Errorf("streams: %s", got)
	}
	// hiding the channel hides every variant; showing it brings one back
	if err := e.svc.Store.SetManual(ctx, ms[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	hidden := false
	for _, m := range ms {
		if m.ChannelID == lead {
			if err := e.svc.Store.SetManual(ctx, m.ID, &Decision{Shown: &hidden}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := e.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if names(e.visible(channels.Viewer{ID: e.user("w")}, 0)) != "Другой" {
		t.Errorf("hidden channel still visible")
	}
}

// A playlist refreshed before variants existed: each quality had its own
// row. The group takes the oldest row (its number stays) and the other
// rows wait, disabled.
func TestVariantsJoinTheOldestRow(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	e.p.put("/a.m3u", "#EXTM3U\n#EXTINF:-1,Пример\nhttp://a.example.test/1.m3u8\n#EXTINF:-1,Пример HD\nhttp://a.example.test/2.m3u8\n")
	src := e.source("A", "/a.m3u", nil)
	e.refresh(src.ID)
	before := e.rows()
	if len(before) != 2 {
		t.Fatalf("rows before: %v", before)
	}
	src.PresetVariants = true
	if _, err := e.svc.Store.SaveSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var name string
	var number, enabled, other int
	if err := e.svc.Store.DB.QueryRow(`SELECT name, number, enabled FROM channels WHERE id=?`, before["Пример"].ID).Scan(&name, &number, &enabled); err != nil {
		t.Fatal(err)
	}
	if name != "Пример HD" || enabled != 1 || number != before["Пример"].Number {
		t.Errorf("the oldest row: %q #%d enabled %d", name, number, enabled)
	}
	if err := e.svc.Store.DB.QueryRow(`SELECT enabled FROM channels WHERE id=?`, before["Пример HD"].ID).Scan(&other); err != nil || other != 0 {
		t.Errorf("the other row: enabled %d (%v)", other, err)
	}
}
