package iptv

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
)

func (e *svcEnv) user(name string) int64 {
	res, err := e.svc.Store.DB.Exec(`INSERT INTO users (name, avatar) VALUES (?, 'a')`, name)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func names(m map[int64]ExtChannel) string {
	var out []string
	for _, x := range m {
		out = append(out, x.Channel.Name)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func (e *svcEnv) visible(v channels.Viewer, set int64) map[int64]ExtChannel {
	e.t.Helper()
	m, err := e.svc.Visible(context.Background(), v, set)
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

// two sources: A «Новости 1», «Общий», a hidden timeshift copy; B «Общий», «Кино 1»
func (e *svcEnv) twoSources() (Source, Source) {
	e.p.put("/a.m3u", "#EXTM3U\n#EXTINF:-1 tvg-id=\"n1\" tvg-logo=\"http://logo.example.test/n1.png\" group-title=\"Новости\",Новости 1\nhttp://a.example.test/live/u/p/1.ts\n"+
		"#EXTINF:-1 group-title=\"Разное\",Общий\n#EXTVLCOPT:http-user-agent=TestUA/1\nhttp://a.example.test/common.ts\n"+
		"#EXTINF:-1 group-title=\"Новости\",Новости 1 +2\nhttp://a.example.test/n1p2.ts\n")
	e.p.put("/b.m3u", "#EXTM3U\n#EXTINF:-1 group-title=\"Разное\",Общий HD\nhttp://b.example.test/common.m3u8\n#EXTINF:-1 group-title=\"Кино\",Кино 1\nhttp://b.example.test/k1.m3u8\n")
	a := e.source("A", "/a.m3u", nil)
	b := e.source("B", "/b.m3u", func(s *Source) { s.Position = 1 })
	e.refresh(a.ID)
	e.refresh(b.ID)
	return a, b
}

func TestVisibleOrderOfFilters(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a, b := e.twoSources()
	ann, bob := e.user("ann"), e.user("bob")
	// editor: the timeshift copy is hidden
	if got := names(e.visible(channels.Viewer{ID: ann}, 0)); got != "Кино 1,Новости 1,Общий" {
		t.Fatal(got)
	}
	// access: B only for bob
	b.Profiles = []int64{bob}
	e.svc.Store.SaveSource(ctx, b)
	if got := names(e.visible(channels.Viewer{ID: ann}, 0)); got != "Новости 1,Общий" {
		t.Fatal(got)
	}
	// the set cuts what access left: a set of B for ann shows nothing
	setB, _ := e.svc.Store.SaveSet(ctx, Set{Name: "Б", Sources: []int64{b.ID}})
	if got := names(e.visible(channels.Viewer{ID: ann}, setB.ID)); got != "" {
		t.Fatal(got)
	}
	if got := names(e.visible(channels.Viewer{ID: bob}, setB.ID)); got != "Кино 1,Общий" {
		t.Fatal(got)
	}
	// the set never shows what the editor hides
	setA, _ := e.svc.Store.SaveSet(ctx, Set{Name: "А", Sources: []int64{a.ID}})
	if got := names(e.visible(channels.Viewer{ID: bob}, setA.ID)); got != "Новости 1,Общий" {
		t.Fatal(got)
	}
	// a set that went is «all my channels»
	if got := names(e.visible(channels.Viewer{ID: bob}, 999)); got != "Кино 1,Новости 1,Общий" {
		t.Fatal(got)
	}
	x := e.visible(channels.Viewer{ID: bob}, setA.ID)
	for _, c := range x {
		if c.Channel.Name == "Новости 1" && (c.TvgID != "n1" || c.Logo != "http://logo.example.test/n1.png" || c.Streams[0].URL != "http://a.example.test/live/u/p/1.ts") {
			t.Fatalf("%+v", c)
		}
	}
}

func TestDedupePrimaryIsFirstSourceInSet(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	a, b := e.twoSources()
	ann := e.user("ann")
	common := func(set int64) ExtChannel {
		for _, c := range e.visible(channels.Viewer{ID: ann}, set) {
			if c.Channel.Name == "Общий" {
				return c
			}
		}
		t.Fatalf("no common channel in set %d", set)
		return ExtChannel{}
	}
	all := common(0)
	if len(all.Streams) != 2 || all.Streams[0].SourceID != a.ID || all.Streams[0].UserAgent != "TestUA/1" || all.Streams[1].URL != "http://b.example.test/common.m3u8" {
		t.Fatalf("all %+v", all.Streams)
	}
	setB, _ := e.svc.Store.SaveSet(ctx, Set{Name: "Б", Sources: []int64{b.ID}})
	if x := common(setB.ID); len(x.Streams) != 1 || x.Streams[0].SourceID != b.ID || x.Channel.ID != all.Channel.ID {
		t.Fatalf("set B %+v", x)
	}
	// moving B above A makes it primary everywhere
	b.Position, a.Position = 0, 1
	e.svc.Store.SaveSource(ctx, b)
	e.svc.Store.SaveSource(ctx, a)
	if x := common(0); x.Streams[0].SourceID != b.ID {
		t.Fatalf("reordered %+v", x.Streams)
	}
}

func TestLimitedProfilesSeeNoExternal(t *testing.T) {
	e := newSvcEnv(t)
	e.twoSources()
	kid := e.user("kid")
	if got := names(e.visible(channels.Viewer{ID: kid, MaxRating: "18+"}, 0)); got != "" {
		t.Fatalf("age-limited %s", got)
	}
	e.svc.ScreenLimited = func(_ context.Context, uid int64) bool { return uid == kid }
	if got := names(e.visible(channels.Viewer{ID: kid}, 0)); got != "" {
		t.Fatalf("screen-limited %s", got)
	}
	if got := names(e.visible(channels.Viewer{ID: kid + 1}, 0)); got == "" {
		t.Fatal("unlimited sees none")
	}
}

func TestSetHiddenGroups(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	e.twoSources()
	ann := e.user("ann")
	all := e.visible(channels.Viewer{ID: ann}, 0)
	var kino int64
	for id, c := range all {
		if c.Channel.Name == "Кино 1" {
			kino = id
		}
	}
	set, _ := e.svc.Store.SaveSet(ctx, Set{Name: "Без новостей", HiddenGroups: []string{"новости"}, HiddenChannels: []int64{kino}})
	if got := names(e.visible(channels.Viewer{ID: ann}, set.ID)); got != "" {
		t.Fatalf("a set without sources shows %s", got) // a set names its sources
	}
	srcs, _ := e.svc.Store.Sources(ctx)
	for _, s := range srcs {
		set.Sources = append(set.Sources, s.ID)
	}
	e.svc.Store.SaveSet(ctx, set)
	if got := names(e.visible(channels.Viewer{ID: ann}, set.ID)); got != "Общий" {
		t.Fatal(got)
	}
}

func TestSetForTokenThenDeviceThenDefault(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	ann := e.user("ann")
	st := e.svc.Store
	s1, _ := st.SaveSet(ctx, Set{Name: "1"})
	s2, _ := st.SaveSet(ctx, Set{Name: "2"})
	s3, _ := st.SaveSet(ctx, Set{Name: "3"})
	st.DB.Exec(`INSERT INTO app_tokens(token, user_id, proto, device_id, created_at, last_seen) VALUES ('tok1', ?, 'iptv', 'tv1', '', '')`, ann)
	check := func(dev, tok string, want int64) {
		t.Helper()
		if got := e.svc.SetFor(ctx, ann, dev, tok); got != want {
			t.Fatalf("SetFor(%q,%q) = %d, want %d", dev, tok, got, want)
		}
	}
	check("phone", "tok1", 0)
	st.AssignSet(ctx, ann, "", s3.ID)
	check("phone", "", s3.ID)
	st.AssignSet(ctx, ann, "phone", s2.ID)
	check("phone", "", s2.ID)
	check("tablet", "", s3.ID)
	st.SetTokenSet(ctx, "tok1", s1.ID)
	check("phone", "tok1", s1.ID)
	// another profile's token is not this one's
	check("phone", "tok-unknown", s2.ID)
	st.DeleteSet(ctx, s2.ID)
	check("phone", "", s3.ID)
}
