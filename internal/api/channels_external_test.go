package api

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/channels"
)

func (e *tvEnv) firstExternal() int64 {
	var id int64
	e.db.QueryRow(`SELECT id FROM channels WHERE kind='external' AND enabled=1 ORDER BY number LIMIT 1`).Scan(&id)
	if id == 0 {
		e.t.Fatal("no external channel")
	}
	return id
}

// liveGuide: programmes of n1 around now.
func liveGuide(now time.Time) string {
	f := func(t time.Time) string { return t.UTC().Format("20060102150405 +0000") }
	h := now.Truncate(time.Hour)
	return `<?xml version="1.0" encoding="UTF-8"?><tv>
<channel id="n1"><display-name>Новости 1</display-name></channel>
<programme start="` + f(h) + `" stop="` + f(h.Add(time.Hour)) + `" channel="n1"><title>Выпуск</title><sub-title>Утро</sub-title><desc>Главное за день</desc><category>Новости</category><icon src="http://img.example.test/a.jpg"/></programme>
<programme start="` + f(h.Add(time.Hour)) + `" stop="` + f(h.Add(2*time.Hour)) + `" channel="n1"><title>Погода</title></programme>
</tv>`
}

type listAnswer struct {
	Channels    []channelJSON
	LastChannel *int64
}

func (e *tvEnv) list(c *http.Cookie) listAnswer {
	e.t.Helper()
	w := e.do("GET", "/api/channels", "", c)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	var l listAnswer
	json.Unmarshal(w.Body.Bytes(), &l)
	return l
}

func TestListMergesExternalWithNumbersAndStream(t *testing.T) {
	e := newTVEnv(t)
	m1, _ := e.movie("Один", "12+", "a")
	e.withDuration()
	e.do("POST", "/api/channels", `{"name":"Свой","rules":{"titles":[`+itoa(m1)+`]}}`, e.admin)
	e.addSource("П")
	l := e.list(e.admin)
	var got []string
	for _, c := range l.Channels {
		got = append(got, fmt.Sprintf("%d:%s:%v", c.Number, c.Name, c.External))
	}
	if strings.Join(got, ",") != "1:Свой:false,100:Новости 1:true,101:Новости 2:true,102:Кино 1:true" {
		t.Fatalf("list %v", got)
	}
	n1, n2 := l.Channels[1], l.Channels[2]
	if n1.Stream == nil || n1.Stream.URL != "http://stream.example.test/live/u1/secret9/1.ts" || len(n1.Stream.Unsupported) != 0 ||
		n1.LogoURL != "http://logo.example.test/n1.png" || n1.CanEdit || n1.Rules != nil || n1.Personal || n1.Auto {
		t.Fatalf("external %+v %+v", n1, n1.Stream)
	}
	if n2.Stream.UserAgent != "TestUA/1" || fmt.Sprint(n2.Stream.Unsupported) != "[web]" || n2.Stream.Fallbacks == nil {
		t.Fatalf("headers %+v", n2.Stream)
	}
	if l.Channels[0].Stream != nil || l.Channels[0].External {
		t.Fatal("own channel with a stream")
	}
	// favourite and last channel work for an external row
	id := itoa(n1.ID)
	if w := e.do("PUT", "/api/channels/"+id+"/favorite", "", e.admin); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := e.do("PUT", "/api/channels/last", `{"channelId":`+id+`}`, e.admin); w.Code != 204 {
		t.Fatal(w.Code)
	}
	l = e.list(e.admin)
	if l.LastChannel == nil || *l.LastChannel != n1.ID || !l.Channels[1].Favorite {
		t.Fatalf("fav/last %+v", l)
	}
	// manage: own channels only; the live encoder never serves it
	w := e.do("GET", "/api/channels?manage=1", "", e.admin)
	if strings.Contains(w.Body.String(), "Новости") {
		t.Fatalf("manage lists externals %s", w.Body)
	}
	if w := e.do("GET", "/api/channels/live/"+id+"/index.m3u8", "", e.admin); w.Code == 200 {
		t.Fatal("live of an external row")
	}
	// kids and other limited profiles: none
	kid := e.kid("")
	for _, c := range e.list(kid).Channels {
		if c.External {
			t.Fatalf("kid sees %+v", c)
		}
	}
	if w := e.do("PUT", "/api/channels/"+id+"/favorite", "", kid); w.Code != 404 {
		t.Fatalf("kid favourite %d", w.Code)
	}
	// hidden addresses: an Antresol redirect, the stream URL nowhere
	src := e.firstSourceID()
	e.do("PUT", "/api/tv/sources/"+itoa(src), `{"hideUrls":true}`, e.admin)
	w = e.do("GET", "/api/channels", "", e.admin)
	if strings.Contains(w.Body.String(), "stream.example.test") || !strings.Contains(w.Body.String(), `"url":"/api/channels/live/ext/`+id+`/0"`) {
		t.Fatalf("hidden %s", w.Body)
	}
	r := e.do("GET", "/api/channels/live/ext/"+id+"/0", "", e.admin)
	if r.Code != 302 || r.Header().Get("Location") != "http://stream.example.test/live/u1/secret9/1.ts" {
		t.Fatalf("redirect %d %v", r.Code, r.Header())
	}
	if r := e.do("GET", "/api/channels/live/ext/"+id+"/0", "", kid); r.Code != 404 {
		t.Fatalf("kid redirect %d", r.Code)
	}
}

func (e *tvEnv) firstSourceID() int64 {
	var id int64
	e.db.QueryRow(`SELECT id FROM iptv_sources ORDER BY id LIMIT 1`).Scan(&id)
	return id
}

func TestGuideMergesExternalProgrammes(t *testing.T) {
	e := newTVEnv(t)
	src := e.addSource("П")
	e.p.put("/epg.xml", liveGuide(time.Now()))
	e.tv.RefreshSourceEPGs(context.Background(), src.ID)
	id := e.firstExternal()
	l := e.list(e.admin)
	if p := l.Channels[0].Now; p == nil || p.Title != "Выпуск" || p.Kind != "external" || p.ItemID != 0 || p.Subtitle != "Утро" ||
		p.Overview != "Главное за день" || fmt.Sprint(p.Genres) != "[Новости]" || p.Poster != "http://img.example.test/a.jpg" ||
		!strings.HasPrefix(p.ID, "x") || l.Channels[0].Next == nil || l.Channels[0].Next.Title != "Погода" {
		t.Fatalf("now %+v next %+v", p, l.Channels[0].Next)
	}
	w := e.do("GET", "/api/channels/"+itoa(id)+"/guide?hours=6", "", e.admin)
	var g struct {
		Channel  channelJSON
		Programs []programJSON
	}
	json.Unmarshal(w.Body.Bytes(), &g)
	if w.Code != 200 || len(g.Programs) != 2 || !g.Channel.External || g.Channel.Stream == nil {
		t.Fatalf("guide %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/channels/epg?hours=6", "", e.admin)
	var grid struct{ Channels []channelEPGJSON }
	json.Unmarshal(w.Body.Bytes(), &grid)
	if len(grid.Channels) != 3 || !grid.Channels[0].External || len(grid.Channels[0].Programs) != 2 || len(grid.Channels[1].Programs) != 0 {
		t.Fatalf("grid %s", w.Body)
	}
}

func (e *tvEnv) iptvLink(set int64) string {
	e.t.Helper()
	var uid int64
	e.db.QueryRow(`SELECT id FROM users LIMIT 1`).Scan(&uid)
	w := e.do("POST", "/api/tv/links", `{"userId":`+itoa(uid)+`,"setId":`+itoa(set)+`,"name":"ТВ"}`, e.admin)
	var l struct{ Playlist string }
	json.Unmarshal(w.Body.Bytes(), &l)
	return strings.TrimSuffix(strings.TrimPrefix(l.Playlist, "http://example.com"), "/playlist.m3u")
}

func TestIPTVM3UIncludesSetExternalsWithHeaders(t *testing.T) {
	e := newTVEnv(t)
	m1, _ := e.movie("Один", "12+", "a")
	e.withDuration()
	e.do("POST", "/api/channels", `{"name":"Свой","rules":{"titles":[`+itoa(m1)+`]}}`, e.admin)
	src := e.addSource("П")
	base := e.iptvLink(0)
	m := e.do("GET", base+"/playlist.m3u", "", nil).Body.String()
	want := "#EXTINF:-1 tvg-id=\"n2\" tvg-chno=\"101\" tvg-name=\"Новости 2\" tvg-logo=\"http://example.com" + base + "/logo/" +
		itoa(e.firstExternal()+1) + ".png\" group-title=\"Новости\",Новости 2\n#EXTVLCOPT:http-user-agent=TestUA/1\nhttp://stream.example.test/live/u1/secret9/2.ts\n"
	if !strings.Contains(m, want) || !strings.Contains(m, `tvg-logo="http://logo.example.test/n1.png"`) || !strings.Contains(m, "/live/1/index.m3u8") ||
		strings.Index(m, "Свой") > strings.Index(m, "Новости 1") {
		t.Fatalf("m3u:\n%s", m)
	}
	if strings.Contains(m, "Кино 1 +2") {
		t.Fatal("hidden channel in the m3u")
	}
	// a set without the source: own channels only
	w := e.do("POST", "/api/tv/sets", `{"name":"Свои","sources":[]}`, e.admin)
	var set struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &set)
	m = e.do("GET", e.iptvLink(set.ID)+"/playlist.m3u", "", nil).Body.String()
	if strings.Contains(m, "Новости") || !strings.Contains(m, "Свой") {
		t.Fatalf("set m3u:\n%s", m)
	}
	_ = src
}

func TestIPTVHideURLsRedirects(t *testing.T) {
	e := newTVEnv(t)
	src := e.addSource("П")
	e.do("PUT", "/api/tv/sources/"+itoa(src.ID), `{"hideUrls":true}`, e.admin)
	base := e.iptvLink(0)
	m := e.do("GET", base+"/playlist.m3u", "", nil).Body.String()
	id := e.firstExternal()
	if strings.Contains(m, "stream.example.test") || !strings.Contains(m, "http://example.com"+base+"/ext/"+itoa(id)+"\n") {
		t.Fatalf("m3u:\n%s", m)
	}
	r := e.do("GET", base+"/ext/"+itoa(id), "", nil)
	if r.Code != 302 || r.Header().Get("Location") != "http://stream.example.test/live/u1/secret9/1.ts" {
		t.Fatalf("redirect %d %v", r.Code, r.Header())
	}
	// a channel the link's set does not have: 403
	w := e.do("POST", "/api/tv/sets", `{"name":"Пусто","sources":[]}`, e.admin)
	var set struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &set)
	if r := e.do("GET", e.iptvLink(set.ID)+"/ext/"+itoa(id), "", nil); r.Code != 403 {
		t.Fatalf("outside the set %d", r.Code)
	}
}

func TestIPTVXMLTVIncludesExternal(t *testing.T) {
	e := newTVEnv(t)
	src := e.addSource("П")
	e.p.put("/epg.xml", liveGuide(time.Now()))
	e.tv.RefreshSourceEPGs(context.Background(), src.ID)
	base := e.iptvLink(0)
	w := e.do("GET", base+"/epg.xml", "", nil)
	var doc struct {
		Channels []struct {
			ID   string `xml:"id,attr"`
			Name string `xml:"display-name"`
		} `xml:"channel"`
		Programmes []struct {
			Channel string `xml:"channel,attr"`
			Title   string `xml:"title"`
			Desc    string `xml:"desc"`
		} `xml:"programme"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err, w.Body)
	}
	ids := map[string]string{}
	for _, c := range doc.Channels {
		ids[c.ID] = c.Name
	}
	kino := "ext-" + itoa(e.firstExternal()+2)
	if ids["n1"] != "Новости 1" || ids["n2"] != "Новости 2" || ids[kino] != "Кино 1" {
		t.Fatalf("channels %v", ids)
	}
	if len(doc.Programmes) != 2 || doc.Programmes[0].Channel != "n1" || doc.Programmes[0].Title != "Выпуск" || doc.Programmes[0].Desc != "Главное за день" {
		t.Fatalf("programmes %+v", doc.Programmes)
	}
	// the logo of an external channel through the link
	if r := e.do("GET", base+"/logo/"+itoa(e.firstExternal())+".png", "", nil); r.Code != 200 {
		t.Fatalf("logo %d", r.Code)
	}
	_ = channels.ExternalTvgID
}
