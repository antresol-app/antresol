package iptv

import (
	"context"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/channels"
)

// A playlist without logos whose guide has them: the guide channel's
// <icon> is the channel's logo; a tvg-logo still wins. Programme icons are
// kept for the guide and the player.
func TestGuideIconIsTheLogoFallback(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	e.p.put("/pl.m3u", "#EXTM3U url-tvg=\""+e.p.url("/guide.xml")+"\"\n"+
		"#EXTINF:-1 tvg-id=\"g.one\",Первый\nhttp://s.example.test/1.ts\n"+
		"#EXTINF:-1 tvg-id=\"g.two\" tvg-logo=\"http://pl.example.test/two.png\",Второй\nhttp://s.example.test/2.ts\n")
	e.p.put("/guide.xml", `<?xml version="1.0" encoding="UTF-8"?><tv>
<channel id="g.one"><display-name>Первый</display-name><icon src="http://epg.example.test/one.png"/></channel>
<channel id="g.two"><display-name>Второй</display-name><icon src="http://epg.example.test/two.png"/></channel>
<programme start="20261112110000 +0000" stop="20261112130000 +0000" channel="g.one"><title>Сейчас</title><icon src="http://epg.example.test/now.jpg"/></programme>
</tv>`)
	src := e.source("A", "/pl.m3u", nil)
	e.refresh(src.ID)
	es, _ := e.svc.Store.EPGURLs(ctx, src.ID)
	if err := e.svc.RefreshEPG(ctx, es[0].ID); err != nil {
		t.Fatal(err)
	}
	vis := e.visible(channels.Viewer{ID: e.user("v")}, 0)
	logos := map[string]string{}
	keys := map[int64][]EPGKey{}
	for id, x := range vis {
		logos[x.Channel.Name] = x.Logo
		keys[id] = x.EPGKeys
	}
	if logos["Первый"] != "http://epg.example.test/one.png" || logos["Второй"] != "http://pl.example.test/two.png" {
		t.Fatalf("logos %v", logos)
	}
	ps, err := e.svc.Programmes(ctx, keys, e.now.Add(-time.Hour), e.now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, list := range ps {
		for _, p := range list {
			found = found || p.Icon == "http://epg.example.test/now.jpg"
		}
	}
	if !found {
		t.Errorf("programme icon lost: %+v", ps)
	}
}
