package iptv

import (
	"context"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
)

func TestEntryLang(t *testing.T) {
	for _, c := range []struct{ name, group, tvg, ai, want string }{
		{"Новости", "UA | Новини", "", "", "uk"},
		{"Новости", "Украина", "", "", "uk"},
		{"Sport 1", "Germany", "", "", "de"},
		{"[DE] Sport 1", "", "", "", "de"},
		{"UA | Кино", "", "", "", "uk"},
		{"Russia Today", "News", "", "", ""},    // a name is no mark
		{"Kino", "Movies in HD", "", "", ""},    // «in» is not India here
		{"BBC One", "", "bbc.one.uk", "", "en"}, // the tvg-id's country
		{"Канал", "", "kanal.kz", "", ""},       // two languages there: nothing said
		{"Новини 24", "", "", "", ""},
		{"Перший національний", "", "", "", "uk"}, // Ukrainian letters
		{"Something", "", "", "fr", "fr"},         // the AI's word, last
		{"Первый канал", "RU | Общие", "", "en", "ru"},
	} {
		if got := EntryLang(c.name, c.group, c.tvg, c.ai); got != c.want {
			t.Errorf("EntryLang(%q, %q, %q, %q) = %q, want %q", c.name, c.group, c.tvg, c.ai, got, c.want)
		}
	}
}

func TestSourceLanguageFilter(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	e.p.put("/a.m3u", "#EXTM3U\n#EXTINF:-1 group-title=\"RU | Новости\",Новости\nhttp://a.example.test/1.m3u8\n"+
		"#EXTINF:-1 group-title=\"UA | Новини\",Новини\nhttp://a.example.test/2.m3u8\n"+
		"#EXTINF:-1 group-title=\"Разное\",Неизвестный\nhttp://a.example.test/3.m3u8\n"+
		"#EXTINF:-1 group-title=\"UA | Кино\",Кино UA\nhttp://a.example.test/4.m3u8\n")
	src := e.source("A", "/a.m3u", func(s *Source) { s.Langs = []string{"ru"} })
	e.refresh(src.ID)
	if got := names(e.visible(channels.Viewer{ID: e.user("v")}, 0)); got != "Неизвестный,Новости" {
		t.Fatalf("visible %s", got)
	}
	// the admin's decision wins over the filter
	ms, _ := e.svc.Store.Members(ctx, src.ID)
	shown := true
	for _, m := range ms {
		if m.Name == "Кино UA" {
			if m.Why != WhyLanguage {
				t.Errorf("why %q", m.Why)
			}
			e.svc.Store.SetManual(ctx, m.ID, &Decision{Shown: &shown})
		}
	}
	e.svc.Sync(ctx)
	if got := names(e.visible(channels.Viewer{ID: e.user("w")}, 0)); got != "Кино UA,Неизвестный,Новости" {
		t.Fatalf("after manual %s", got)
	}
	// off: everything back
	src, _ = e.svc.Store.Source(ctx, src.ID)
	src.Langs = nil
	e.svc.Store.SaveSource(ctx, src)
	e.svc.Sync(ctx)
	if got := names(e.visible(channels.Viewer{ID: e.user("x")}, 0)); got != "Кино UA,Неизвестный,Новини,Новости" {
		t.Fatalf("off %s", got)
	}
}
