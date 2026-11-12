package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/iptv"
)

// tvProvider: a fake IPTV provider (synthetic playlists and guides).
type tvProvider struct {
	mu    sync.Mutex
	files map[string]string
	srv   *httptest.Server
}

func (p *tvProvider) put(path, body string) { p.mu.Lock(); p.files[path] = body; p.mu.Unlock() }

type tvEnv struct {
	*libEnv
	tv  *iptv.Service
	p   *tvProvider
	svc *channels.Service
}

func newTVEnv(t *testing.T) *tvEnv { return newTVEnvLive(t, nil) }

// newTVEnvLive: with a live encoder (nil: none).
func newTVEnvLive(t *testing.T, live *channels.Live) *tvEnv {
	p := &tvProvider{files: map[string]string{}}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		b, ok := p.files[r.URL.Path]
		p.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(b))
	}))
	t.Cleanup(p.srv.Close)
	e := &tvEnv{p: p}
	e.libEnv = newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		e.svc = channels.New(db, live)
		seal, err := iptv.NewSealer(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		e.tv = &iptv.Service{Store: &iptv.Store{DB: db}, Channels: e.svc.Store, Fetch: iptv.NewFetcher(), Seal: seal}
		d.Channels, d.DB, d.IPTV, d.ExtIPTV = e.svc, db, true, e.tv
	})
	return e
}

const tvPlaylist = "#EXTM3U\n" +
	"#EXTINF:-1 tvg-id=\"n1\" tvg-logo=\"http://logo.example.test/n1.png\" group-title=\"Новости\",Новости 1\nhttp://stream.example.test/live/u1/secret9/1.ts\n" +
	"#EXTINF:-1 tvg-id=\"n2\" group-title=\"Новости\",Новости 2\n#EXTVLCOPT:http-user-agent=TestUA/1\nhttp://stream.example.test/live/u1/secret9/2.ts\n" +
	"#EXTINF:-1 group-title=\"Кино\",Кино 1\nhttp://stream.example.test/live/u1/secret9/3.ts\n" +
	"#EXTINF:-1 group-title=\"Кино\",Кино 1 +2\nhttp://stream.example.test/live/u1/secret9/4.ts\n"

const tvGuide = `<?xml version="1.0" encoding="UTF-8"?><tv>
<channel id="n1"><display-name>Новости 1</display-name></channel>
<channel id="k.one"><display-name>Синемир</display-name><display-name>Кино первый</display-name></channel>
</tv>`

// addSource creates a source on the fake provider through the API and refreshes it.
func (e *tvEnv) addSource(name string) sourceJSON {
	e.t.Helper()
	e.p.put("/get.php", tvPlaylist)
	e.p.put("/epg.xml", tvGuide)
	body := `{"name":"` + name + `","url":"` + e.p.srv.URL + `/get.php?username=u1&password=secret9","epg":[{"url":"` + e.p.srv.URL + `/epg.xml?password=secret9"}]}`
	w := e.do("POST", "/api/tv/sources", body, e.admin)
	if w.Code != 201 {
		e.t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var src sourceJSON
	json.Unmarshal(w.Body.Bytes(), &src)
	if _, err := e.tv.RefreshSource(context.Background(), src.ID, false); err != nil {
		e.t.Fatal(err)
	}
	e.tv.RefreshSourceEPGs(context.Background(), src.ID)
	return src
}

func noSecrets(t *testing.T, what, body string) {
	t.Helper()
	for _, s := range []string{"secret9", "u1", "get.php", "epg.xml?", "username"} {
		if strings.Contains(body, s) {
			t.Fatalf("%s carries %q: %s", what, s, body)
		}
	}
}

func TestSourcesAPIMasksSecrets(t *testing.T) {
	e := newTVEnv(t)
	src := e.addSource("Провайдер")
	w := e.do("GET", "/api/tv/sources", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	noSecrets(t, "sources", w.Body.String())
	var l struct {
		Sources       []sourceJSON
		XtreamEnabled bool
	}
	json.Unmarshal(w.Body.Bytes(), &l)
	host := strings.TrimPrefix(e.p.srv.URL, "http://")
	s := l.Sources[0]
	if s.URL != "http://"+host+"/…***" || len(s.EPG) != 1 || s.EPG[0].URL != "http://"+host+"/…***" || s.EPG[0].Matched != 1 ||
		s.Counts != (countsJSON{Total: 4, Shown: 3, Hidden: 1}) || s.LastOK == nil || s.DefaultShown == nil || !*s.DefaultShown || l.XtreamEnabled {
		t.Fatalf("source %+v", s)
	}
	w = e.do("GET", "/api/tv/sources/"+itoa(src.ID)+"/channels", "", e.admin)
	noSecrets(t, "channels", w.Body.String())
	// an update without a URL keeps the stored one
	if w := e.do("PUT", "/api/tv/sources/"+itoa(src.ID), `{"name":"Новое имя","hideUrls":true}`, e.admin); w.Code != 200 {
		t.Fatalf("update %d %s", w.Code, w.Body)
	}
	if _, err := e.tv.RefreshSource(context.Background(), src.ID, false); err != nil {
		t.Fatalf("refresh after an update without URL: %v", err)
	}
	// a refresh failing on the provider: the reason without credentials
	e.p.put("/get.php", "<html>login</html>")
	w = e.do("POST", "/api/tv/sources/"+itoa(src.ID)+"/refresh", "", e.admin)
	noSecrets(t, "refresh", w.Body.String())
	var rr struct {
		OK     bool
		Error  string
		Source sourceJSON
	}
	json.Unmarshal(w.Body.Bytes(), &rr)
	if w.Code != 200 || rr.OK || rr.Error != iptv.ErrTextNotM3U || rr.Source.LastError != iptv.ErrTextNotM3U || rr.Source.Counts.Total != 4 {
		t.Fatalf("failed refresh %d %s", w.Code, w.Body)
	}
	// other profiles: none of it
	ann := e.login(`{"name":"Аня"}`)
	for _, p := range []string{"/api/tv/sources", "/api/tv/sources/" + itoa(src.ID) + "/channels", "/api/tv/devices", "/api/tv/sets"} {
		if w := e.do("GET", p, "", ann); w.Code != 403 {
			t.Fatalf("%s for a non-admin: %d", p, w.Code)
		}
	}
}

func TestSourcesAPIXtreamOffByDefault(t *testing.T) {
	e := newTVEnv(t)
	body := `{"name":"X","kind":"xtream","xtream":{"server":"http://x.example.test:8080","user":"u1","password":"secret9"}}`
	if w := e.do("POST", "/api/tv/sources", body, e.admin); w.Code != 400 {
		t.Fatalf("xtream while off: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/tv/settings", `{"xtreamEnabled":true}`, e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"xtreamEnabled":true`) {
		t.Fatalf("settings %d %s", w.Code, w.Body)
	}
	w := e.do("POST", "/api/tv/sources", body, e.admin)
	if w.Code != 201 {
		t.Fatalf("xtream on: %d %s", w.Code, w.Body)
	}
	noSecrets(t, "xtream source", strings.ReplaceAll(w.Body.String(), `"user":"u1"`, ""))
	if !strings.Contains(w.Body.String(), `"xtream":{"server":"http://x.example.test:8080","user":"u1"}`) {
		t.Fatal(w.Body)
	}
	for _, bad := range []string{`{"name":"Y","url":"ftp://x.example.test/a"}`, `{"name":"","url":"http://x.example.test/a"}`, `{"name":"Z","url":"http://x.example.test/a","refreshHours":5}`} {
		if w := e.do("POST", "/api/tv/sources", bad, e.admin); w.Code != 400 {
			t.Fatalf("%s: %d", bad, w.Code)
		}
	}
}

type tvChannels struct {
	Groups   []groupJSON
	Channels []memberJSON
	Total    int
}

func (e *tvEnv) channels(srcID int64, q string) tvChannels {
	e.t.Helper()
	w := e.do("GET", "/api/tv/sources/"+itoa(srcID)+"/channels"+q, "", e.admin)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out tvChannels
	json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func TestEditorPatchAndWhy(t *testing.T) {
	e := newTVEnv(t)
	src := e.addSource("П")
	c := e.channels(src.ID, "")
	if c.Total != 4 || len(c.Groups) != 2 || c.Groups[0] != (groupJSON{"Новости", 2, 2}) || c.Groups[1] != (groupJSON{"Кино", 2, 1}) {
		t.Fatalf("groups %+v", c)
	}
	by := map[string]memberJSON{}
	for _, m := range c.Channels {
		by[m.ProviderName] = m
	}
	if m := by["Кино 1 +2"]; m.Shown || m.Why != "preset-timeshift" || m.WhyText == "" || m.Number == 0 {
		t.Fatalf("timeshift %+v", m)
	}
	if m := by["Новости 1"]; !m.Shown || m.Why != "source-default" || m.Number != 100 || m.Category != "Новости" || m.EPG.How != "tvg-id" || m.EPG.XMLID != "n1" {
		t.Fatalf("news %+v", m)
	}
	n1 := by["Новости 1"].ChannelID
	w := e.do("PATCH", "/api/tv/channels", `{"channelIds":[`+itoa(n1)+`],"category":"Главное","name":"Первые новости","number":7}`, e.admin)
	if w.Code != 200 {
		t.Fatalf("patch %d %s", w.Code, w.Body)
	}
	c = e.channels(src.ID, "?q=первые")
	if c.Total != 1 || c.Channels[0].Name != "Первые новости" || c.Channels[0].Category != "Главное" || c.Channels[0].Number != 7 || !c.Channels[0].NumberManual || c.Channels[0].Why != "manual" {
		t.Fatalf("patched %+v", c)
	}
	// a taken number
	if w := e.do("PATCH", "/api/tv/channels", `{"channelIds":[`+itoa(by["Новости 2"].ChannelID)+`],"number":7}`, e.admin); w.Code != 409 {
		t.Fatalf("taken %d", w.Code)
	}
	// a whole group hidden, then the state filters
	if w := e.do("PATCH", "/api/tv/groups", `{"sourceId":`+itoa(src.ID)+`,"group":"Новости","shown":false}`, e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"updated":2`) {
		t.Fatalf("group %d %s", w.Code, w.Body)
	}
	if c := e.channels(src.ID, "?state=shown"); c.Total != 1 || c.Channels[0].ProviderName != "Кино 1" {
		t.Fatalf("shown %+v", c)
	}
	if c := e.channels(src.ID, "?state=hidden&group=Новости&limit=1"); c.Total != 2 || len(c.Channels) != 1 {
		t.Fatalf("hidden page %+v", c)
	}
	// reset: back to the rules
	e.do("PATCH", "/api/tv/channels", `{"channelIds":[`+itoa(n1)+`],"reset":true}`, e.admin)
	if c := e.channels(src.ID, "?q=новости+1"); c.Channels[0].Why != "source-default" || !c.Channels[0].Shown || c.Channels[0].Number != 7 || c.Channels[0].NumberManual {
		t.Fatalf("reset %+v", c.Channels)
	}
	// categories: own and external, equal names once
	e.svc.Store.Create(context.Background(), channels.Channel{Name: "Свой", Category: "Кино", Enabled: true})
	w = e.do("GET", "/api/tv/categories", "", e.admin)
	if !strings.Contains(w.Body.String(), `"categories":["Кино","Новости"]`) {
		t.Fatalf("categories %s", w.Body)
	}
	// an external channel is not edited through the own editor
	if w := e.do("PUT", "/api/channels/"+itoa(n1), `{"name":"x","rules":{}}`, e.admin); w.Code != 400 {
		t.Fatalf("own editor on an external row: %d", w.Code)
	}
	if w := e.do("DELETE", "/api/channels/"+itoa(n1), "", e.admin); w.Code != 400 {
		t.Fatalf("own delete on an external row: %d", w.Code)
	}
}

func TestManualEPGBinding(t *testing.T) {
	e := newTVEnv(t)
	src := e.addSource("П")
	w := e.do("GET", "/api/tv/epg/search?q=синемир", "", e.admin)
	var res struct{ Results []xmlChannelJSON }
	json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Results) != 1 || res.Results[0].XMLID != "k.one" || len(res.Results[0].Names) != 2 || res.Results[0].SourceID != src.ID {
		t.Fatalf("search %s", w.Body)
	}
	var kino memberJSON
	for _, m := range e.channels(src.ID, "").Channels {
		if m.ProviderName == "Кино 1" {
			kino = m
		}
	}
	if kino.EPG.XMLID != "" {
		t.Fatalf("auto-bound %+v", kino.EPG)
	}
	body := `{"epgUrlId":` + itoa(res.Results[0].EPGURLID) + `,"xmlId":"k.one"}`
	if w := e.do("PUT", "/api/tv/channels/"+itoa(kino.ChannelID)+"/epg", body, e.admin); w.Code != 204 {
		t.Fatalf("bind %d %s", w.Code, w.Body)
	}
	// survives a guide refresh
	e.tv.RefreshSourceEPGs(context.Background(), src.ID)
	c := e.channels(src.ID, "?q=кино+1")
	if m := c.Channels[0]; m.EPG != (memberEPGJSON{"k.one", res.Results[0].EPGURLID, "manual", true}) {
		t.Fatalf("bound %+v", m.EPG)
	}
	if w := e.do("DELETE", "/api/tv/channels/"+itoa(kino.ChannelID)+"/epg", "", e.admin); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if m := e.channels(src.ID, "?q=кино+1").Channels[0]; m.EPG.Manual || m.EPG.XMLID != "" {
		t.Fatalf("unbound %+v", m.EPG)
	}
}

func TestDevicesAndSets(t *testing.T) {
	e := newTVEnv(t)
	src := e.addSource("П")
	w := e.do("POST", "/api/tv/sets", `{"name":"Спальня","sources":[`+itoa(src.ID)+`],"hiddenGroups":["Кино"]}`, e.admin)
	var set iptv.Set
	json.Unmarshal(w.Body.Bytes(), &set)
	if w.Code != 201 || set.ID == 0 || set.HiddenChannels == nil {
		t.Fatalf("set %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/tv/sets/"+itoa(set.ID), `{"name":"Спальня 2","sources":[`+itoa(src.ID)+`]}`, e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Спальня 2"`) {
		t.Fatalf("put set %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/tv/sets", "", e.admin); !strings.Contains(w.Body.String(), `"sets":[{"id":`+itoa(set.ID)) {
		t.Fatal(w.Body)
	}
	var uid int64
	e.db.QueryRow(`SELECT id FROM users LIMIT 1`).Scan(&uid)
	if _, err := auth.NewSessions(e.db).CreateDevice(context.Background(), uid, auth.DeviceInfo{ID: "dev-1", Name: "Гостиная", Model: "AppleTV14,1", Version: "1.2"}); err != nil {
		t.Fatal(err)
	}
	if w := e.do("PUT", "/api/tv/devices/assign", `{"userId":`+itoa(uid)+`,"deviceId":"dev-1","setId":`+itoa(set.ID)+`}`, e.admin); w.Code != 204 {
		t.Fatalf("assign %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/tv/devices", "", e.admin)
	var d struct{ Devices []deviceJSON }
	json.Unmarshal(w.Body.Bytes(), &d)
	if len(d.Devices) != 1 || d.Devices[0].Client != "tvos" || d.Devices[0].SetID != set.ID || d.Devices[0].Kind != "app" || d.Devices[0].DeviceID != "dev-1" {
		t.Fatalf("devices %s", w.Body)
	}
	if e.tv.SetFor(context.Background(), uid, "dev-1", "") != set.ID {
		t.Fatal("SetFor")
	}
	if w := e.do("PUT", "/api/tv/devices/assign", `{"userId":`+itoa(uid)+`,"deviceId":"dev-1","setId":999}`, e.admin); w.Code != 400 {
		t.Fatalf("unknown set %d", w.Code)
	}
	if w := e.do("DELETE", "/api/tv/sets/"+itoa(set.ID), "", e.admin); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if e.tv.SetFor(context.Background(), uid, "dev-1", "") != 0 {
		t.Fatal("a deleted set still assigned")
	}
}

func TestLinksCreateTokenWithSet(t *testing.T) {
	e := newTVEnv(t)
	src := e.addSource("П")
	w := e.do("POST", "/api/tv/sets", `{"name":"ТВ 5","sources":[`+itoa(src.ID)+`]}`, e.admin)
	var set iptv.Set
	json.Unmarshal(w.Body.Bytes(), &set)
	var uid int64
	e.db.QueryRow(`SELECT id FROM users LIMIT 1`).Scan(&uid)
	w = e.do("POST", "/api/tv/links", `{"userId":`+itoa(uid)+`,"setId":`+itoa(set.ID)+`,"name":"Телевизор 5"}`, e.admin)
	var l struct{ LinkID, Playlist, Guide string }
	json.Unmarshal(w.Body.Bytes(), &l)
	if w.Code != 201 || l.LinkID == "" || !strings.HasSuffix(l.Playlist, "/playlist.m3u") {
		t.Fatalf("link %d %s", w.Code, w.Body)
	}
	tok := strings.TrimSuffix(strings.TrimPrefix(l.Playlist, "http://example.com/iptv/"), "/playlist.m3u")
	if e.tv.SetFor(context.Background(), uid, "", tok) != set.ID {
		t.Fatal("the link's set")
	}
	// a second link does not replace the first
	e.do("POST", "/api/tv/links", `{"userId":`+itoa(uid)+`,"setId":0,"name":"Телевизор 6"}`, e.admin)
	w = e.do("GET", "/api/tv/devices", "", e.admin)
	var d struct{ Devices []deviceJSON }
	json.Unmarshal(w.Body.Bytes(), &d)
	if len(d.Devices) != 2 || d.Devices[0].Kind != "iptv" || d.Devices[0].Client != "iptv" {
		t.Fatalf("devices %s", w.Body)
	}
	var mine deviceJSON
	for _, x := range d.Devices {
		if x.LinkID == l.LinkID {
			mine = x
		}
	}
	if mine.SetID != set.ID || mine.Name != "Телевизор 5" || mine.Playlist != l.Playlist {
		t.Fatalf("link device %+v", mine)
	}
	// reassign by link id
	if w := e.do("PUT", "/api/tv/devices/assign", `{"linkId":"`+l.LinkID+`","setId":0}`, e.admin); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if e.tv.SetFor(context.Background(), uid, "", tok) != 0 {
		t.Fatal("reassigned")
	}
}

func TestPendingDropRejectAndConfirm(t *testing.T) {
	e := newTVEnv(t)
	src := e.addSource("П")
	e.p.put("/get.php", "#EXTM3U\n#EXTINF:-1,Кино 1\nhttp://stream.example.test/x.ts\n")
	w := e.do("POST", "/api/tv/sources/"+itoa(src.ID)+"/refresh", "", e.admin)
	if !strings.Contains(w.Body.String(), `"pendingDrop":4`) {
		t.Fatalf("pending %s", w.Body)
	}
	w = e.do("POST", "/api/tv/sources/"+itoa(src.ID)+"/reject", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"pendingDrop":0`) || !strings.Contains(w.Body.String(), `"total":4`) {
		t.Fatalf("reject %d %s", w.Code, w.Body)
	}
	w = e.do("POST", "/api/tv/sources/"+itoa(src.ID)+"/confirm", "", e.admin)
	if !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"counts":{"total":1`) {
		t.Fatalf("confirm %s", w.Body)
	}
	if w := e.do("DELETE", "/api/tv/sources/"+itoa(src.ID), "", e.admin); w.Code != 204 {
		t.Fatal(w.Code)
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM channels WHERE kind='external'`).Scan(&n)
	if n != 0 {
		t.Fatalf("rows left %d", n)
	}
}

func TestDeleteIPTVLink(t *testing.T) {
	e := newTVEnv(t)
	var uid int64
	e.db.QueryRow(`SELECT id FROM users LIMIT 1`).Scan(&uid)
	w := e.do("POST", "/api/tv/links", `{"userId":`+itoa(uid)+`,"setId":0,"name":"Кухня"}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var l struct{ LinkID, Playlist string }
	json.Unmarshal(w.Body.Bytes(), &l)
	if w := e.do("DELETE", "/api/tv/links/"+l.LinkID, "", e.admin); w.Code != 204 {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
	if w := e.do("DELETE", "/api/tv/links/"+l.LinkID, "", e.admin); w.Code != 404 {
		t.Fatalf("second delete %d", w.Code)
	}
}
