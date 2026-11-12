package iptv

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// provider is a fake IPTV provider: paths answer what the test puts there.
type provider struct {
	mu    sync.Mutex
	files map[string]string
	srv   *httptest.Server
}

func newProvider(t *testing.T) *provider {
	p := &provider{files: map[string]string{}}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		body, ok := p.files[r.URL.Path]
		p.mu.Unlock()
		switch {
		case !ok:
			http.NotFound(w, r)
		case strings.HasPrefix(body, "TRUNCATE:"):
			// promises more than it sends: the client sees a cut body
			b := strings.TrimPrefix(body, "TRUNCATE:")
			w.Header().Set("Content-Length", fmt.Sprint(len(b)+5000))
			w.Write([]byte(b))
		default:
			w.Write([]byte(body))
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *provider) put(path, body string) {
	p.mu.Lock()
	p.files[path] = body
	p.mu.Unlock()
}

func (p *provider) url(path string) string { return p.srv.URL + path }

// m3u makes a playlist of n channels named prefix 1…n (tvg-id prefix.k).
func m3u(prefix string, from, n int, group string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for k := from; k < from+n; k++ {
		fmt.Fprintf(&b, "#EXTINF:-1 tvg-id=\"%s.%d\" group-title=\"%s\",%s %d\nhttp://stream.example.test/live/u/p/%s%d.ts\n", prefix, k, group, prefix, k, prefix, k)
	}
	return b.String()
}

type svcEnv struct {
	t   *testing.T
	svc *Service
	now time.Time
	p   *provider
}

func newSvcEnv(t *testing.T) *svcEnv {
	t.Helper()
	d := openTestDB(t)
	e := &svcEnv{t: t, now: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC), p: newProvider(t)}
	seal, err := NewSealer(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	nowFn := func() time.Time { return e.now }
	e.svc = &Service{Store: &Store{DB: d, Now: nowFn}, Fetch: NewFetcher(), Seal: seal, Now: nowFn}
	return e
}

func (e *svcEnv) source(name, path string, mod func(*Source)) Source {
	e.t.Helper()
	enc, err := e.svc.Seal.Seal(e.p.url(path))
	if err != nil {
		e.t.Fatal(err)
	}
	src := Source{Name: name, Kind: KindM3U, URLEnc: enc, PresetAdult: true, PresetTimeshift: true, RefreshHours: 24}
	if mod != nil {
		mod(&src)
	}
	src, err = e.svc.Store.SaveSource(context.Background(), src)
	if err != nil {
		e.t.Fatal(err)
	}
	return src
}

func (e *svcEnv) refresh(id int64) Result {
	e.t.Helper()
	res, err := e.svc.RefreshSource(context.Background(), id, false)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

type rowState struct {
	ID       int64
	Number   int
	Name     string
	Category string
	Enabled  bool
	Manual   bool
	Why      string
}

func (e *svcEnv) rows() map[string]rowState {
	e.t.Helper()
	rs, err := e.svc.Store.DB.Query(`SELECT id, number, name, category, enabled, number_manual, why FROM channels WHERE kind='external'`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rs.Close()
	out := map[string]rowState{}
	for rs.Next() {
		var r rowState
		rs.Scan(&r.ID, &r.Number, &r.Name, &r.Category, &r.Enabled, &r.Manual, &r.Why)
		out[r.Name] = r
	}
	return out
}

func (e *svcEnv) snapshot(srcID int64) string {
	ms, err := e.svc.Store.Members(context.Background(), srcID)
	if err != nil {
		e.t.Fatal(err)
	}
	var b strings.Builder
	for _, m := range ms {
		fmt.Fprintf(&b, "%s|%s|%v|%v|%d\n", m.Ident, m.Name, m.Present, m.Shown, m.ChannelID)
	}
	fmt.Fprint(&b, e.rows())
	return b.String()
}

func TestRefreshKeepsLastGoodOnHTML(t *testing.T) {
	e := newSvcEnv(t)
	e.p.put("/pl.m3u", m3u("Тест", 1, 5, "Новости"))
	src := e.source("A", "/pl.m3u", nil)
	if res := e.refresh(src.ID); res.Total != 5 || res.New != 5 || res.Shown != 5 {
		t.Fatalf("first %+v", res)
	}
	before := e.snapshot(src.ID)
	e.p.put("/pl.m3u", "<!DOCTYPE html><html><body>Login</body></html>")
	e.now = e.now.Add(time.Hour)
	if _, err := e.svc.RefreshSource(context.Background(), src.ID, false); err == nil {
		t.Fatal("html accepted")
	}
	if after := e.snapshot(src.ID); after != before {
		t.Fatalf("html changed the list:\n%s\n%s", before, after)
	}
	got, _ := e.svc.Store.Source(context.Background(), src.ID)
	if got.LastErr != ErrTextNotM3U || !got.LastTry.Equal(e.now) || got.LastOK.Equal(e.now) {
		t.Fatalf("source after html %+v", got)
	}
}

func TestRefreshKeepsLastGoodOnEmptyAndTruncated(t *testing.T) {
	e := newSvcEnv(t)
	good := m3u("Тест", 1, 5, "Новости")
	e.p.put("/pl.m3u", good)
	src := e.source("A", "/pl.m3u", nil)
	e.refresh(src.ID)
	before := e.snapshot(src.ID)
	for _, c := range []struct{ body, why string }{
		{"", ErrTextEmpty},
		{"#EXTM3U\n", ErrTextEmpty},
		{"TRUNCATE:" + good[:len(good)/2], ErrTextTruncated},
	} {
		e.p.put("/pl.m3u", c.body)
		if _, err := e.svc.RefreshSource(context.Background(), src.ID, false); err == nil {
			t.Fatalf("%q accepted", c.body)
		}
		if after := e.snapshot(src.ID); after != before {
			t.Fatalf("%q changed the list", c.body)
		}
		if got, _ := e.svc.Store.Source(context.Background(), src.ID); got.LastErr != c.why {
			t.Fatalf("%q: LastErr %q", c.body, got.LastErr)
		}
	}
	// an HTTP error, with the credentials of the URL never in the reason
	enc, _ := e.svc.Seal.Seal(e.p.url("/get.php?username=u1&password=secret9"))
	src.URLEnc = enc
	e.svc.Store.SaveSource(context.Background(), src)
	_, err := e.svc.RefreshSource(context.Background(), src.ID, false)
	got, _ := e.svc.Store.Source(context.Background(), src.ID)
	if err == nil || got.LastErr != "HTTP 404" || strings.Contains(err.Error(), "secret9") {
		t.Fatalf("404: %v %q", err, got.LastErr)
	}
}

func TestRefreshDropOver30PercentWaitsForConfirm(t *testing.T) {
	e := newSvcEnv(t)
	e.p.put("/pl.m3u", m3u("Тест", 1, 10, "Новости"))
	src := e.source("A", "/pl.m3u", nil)
	e.refresh(src.ID)
	before := e.snapshot(src.ID)
	e.p.put("/pl.m3u", m3u("Тест", 1, 6, "Новости")) // 4 of 10 lost
	res := e.refresh(src.ID)
	if res.PendingDrop != 4 || res.Lost != 4 {
		t.Fatalf("drop %+v", res)
	}
	if after := e.snapshot(src.ID); after != before {
		t.Fatal("a pending drop stored the playlist")
	}
	if got, _ := e.svc.Store.Source(context.Background(), src.ID); got.PendingDrop != 4 {
		t.Fatalf("pending %+v", got)
	}
	if _, err := e.svc.ConfirmDrop(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := e.svc.Store.Source(context.Background(), src.ID)
	rows := e.rows()
	if got.PendingDrop != 0 || !rows["Тест 6"].Enabled || rows["Тест 7"].Enabled || rows["Тест 7"].Number == 0 {
		t.Fatalf("after confirm %+v %+v", got, rows)
	}
	// losing 3 of 10 (not more than 30 %) goes through by itself
	e.p.put("/pl.m3u", m3u("Тест", 1, 10, "Новости"))
	e.refresh(src.ID)
	e.p.put("/pl.m3u", m3u("Тест", 1, 7, "Новости"))
	if res := e.refresh(src.ID); res.PendingDrop != 0 || res.Lost != 3 {
		t.Fatalf("30 %% %+v", res)
	}
}

func TestRefreshCreatesChannelRowsNumberedFrom100(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	for n := 1; n <= 12; n++ {
		if _, err := e.svc.Store.DB.Exec(`INSERT INTO channels (number, name) VALUES (?, ?)`, n, fmt.Sprintf("Свой %d", n)); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.Store.DB.Exec(`INSERT INTO channels (number, name) VALUES (101, 'Свой 101')`)
	e.p.put("/pl.m3u", m3u("Тест", 1, 4, "Новости")+
		"#EXTINF:-1 group-title=\"Кино\",Кино +2\nhttp://stream.example.test/k2.ts\n"+
		"#EXTINF:-1 group-title=\"18+\",Ночной\nhttp://stream.example.test/n.ts\n")
	src := e.source("A", "/pl.m3u", nil)
	e.refresh(src.ID)
	rows := e.rows()
	nums := map[string]int{}
	for n, r := range rows {
		nums[n] = r.Number
	}
	want := map[string]int{"Тест 1": 100, "Тест 2": 102, "Тест 3": 103, "Тест 4": 104, "Кино +2": 105, "Ночной": 106}
	if fmt.Sprint(nums) != fmt.Sprint(want) {
		t.Fatalf("numbers %v", nums)
	}
	if rows["Кино +2"].Enabled || rows["Кино +2"].Why != WhyTimeshift || rows["Ночной"].Enabled || rows["Ночной"].Why != WhyAdult ||
		!rows["Тест 1"].Enabled || rows["Тест 1"].Category != "Новости" || rows["Тест 1"].Why != WhySourceDefault {
		t.Fatalf("rows %+v", rows)
	}
	// own channels untouched
	var own int
	e.svc.Store.DB.QueryRow(`SELECT count(*) FROM channels WHERE kind='antresol' AND (number <= 12 OR number = 101)`).Scan(&own)
	if own != 13 {
		t.Fatal(own)
	}
	// a manual number and a manual hide survive a refresh; the hidden keeps its number
	ms, _ := e.svc.Store.Members(ctx, src.ID)
	hide := false
	for _, m := range ms {
		switch m.Name {
		case "Тест 2":
			e.svc.Store.SetManual(ctx, m.ID, &Decision{Number: 150})
		case "Тест 3":
			e.svc.Store.SetManual(ctx, m.ID, &Decision{Shown: &hide})
		}
	}
	e.now = e.now.Add(24 * time.Hour)
	e.p.put("/pl.m3u", m3u("Тест", 1, 5, "Новости"))
	if res := e.refresh(src.ID); res.PendingDrop != 2 { // 2 of 6 gone
		t.Fatalf("%+v", res)
	}
	e.svc.ConfirmDrop(ctx, src.ID)
	e.refresh(src.ID)
	rows = e.rows()
	if r := rows["Тест 2"]; r.Number != 150 || !r.Manual || r.Why != WhyManual {
		t.Fatalf("manual number %+v", r)
	}
	if r := rows["Тест 3"]; r.Number != 103 || r.Enabled || r.Why != WhyManual {
		t.Fatalf("manual hide %+v", r)
	}
	if r := rows["Тест 5"]; r.Number != 107 || !r.Enabled {
		t.Fatalf("new channel takes the next free number %+v", r)
	}
	// gone from the playlist: disabled, its number kept
	if r := rows["Кино +2"]; r.Number != 105 || r.Enabled {
		t.Fatalf("gone %+v", r)
	}
}

func TestRefreshBigPlaylistHiddenByDefault(t *testing.T) {
	e := newSvcEnv(t)
	e.p.put("/big.m3u", m3u("Большой", 1, BigPlaylist, "Разное"))
	big := e.source("Big", "/big.m3u", nil)
	if res := e.refresh(big.ID); res.Shown != 0 || res.Hidden != BigPlaylist {
		t.Fatalf("big %+v", res)
	}
	got, _ := e.svc.Store.Source(context.Background(), big.ID)
	if got.DefaultShown == nil || *got.DefaultShown {
		t.Fatalf("default fixed at the first refresh %+v", got.DefaultShown)
	}
	// an explicit default wins the size rule
	on := true
	e.p.put("/big2.m3u", m3u("Второй", 1, BigPlaylist, "Разное"))
	b2 := e.source("Big2", "/big2.m3u", func(s *Source) { s.DefaultShown = &on })
	if res := e.refresh(b2.ID); res.Shown != BigPlaylist {
		t.Fatalf("explicit %+v", res)
	}
}

func TestRefreshDedupesAcrossSources(t *testing.T) {
	e := newSvcEnv(t)
	e.p.put("/a.m3u", "#EXTM3U\n#EXTINF:-1,Пример HD\nhttp://a.example.test/1.ts\n#EXTINF:-1,Россия\nhttp://a.example.test/2.ts\n#EXTINF:-1,Россия 4K\nhttp://a.example.test/3.ts\n")
	e.p.put("/b.m3u", "#EXTM3U\n#EXTINF:-1,Пример\nhttp://b.example.test/1.ts\n")
	a := e.source("A", "/a.m3u", nil)
	b := e.source("B", "/b.m3u", func(s *Source) { s.Position = 1 })
	e.refresh(a.ID)
	e.refresh(b.ID)
	rows := e.rows()
	if len(rows) != 3 {
		t.Fatalf("rows %+v", rows)
	}
	ma, _ := e.svc.Store.Members(context.Background(), a.ID)
	mb, _ := e.svc.Store.Members(context.Background(), b.ID)
	if ma[0].ChannelID != mb[0].ChannelID || ma[1].ChannelID == ma[2].ChannelID {
		t.Fatalf("dedupe a=%+v b=%+v", ma, mb)
	}
	// deleting A keeps the row B still feeds, drops the others
	if err := e.svc.DeleteSource(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	rows = e.rows()
	if len(rows) != 1 || rows["Пример"].ID != mb[0].ChannelID || !rows["Пример"].Enabled {
		t.Fatalf("after delete %+v", rows)
	}
}

func TestRefreshXtreamOffByDefault(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	pass, _ := e.svc.Seal.Seal("pw")
	e.p.put("/get.php", m3u("Икс", 1, 2, "X"))
	src, _ := e.svc.Store.SaveSource(ctx, Source{Name: "X", Kind: KindXtream, XtreamServer: e.p.srv.URL, XtreamUser: "u", XtreamPassEnc: pass})
	if _, err := e.svc.RefreshSource(ctx, src.ID, false); err == nil {
		t.Fatal("xtream refreshed while off")
	}
	if got, _ := e.svc.Store.Source(ctx, src.ID); got.LastErr != ErrTextXtreamOff {
		t.Fatal(got.LastErr)
	}
	e.svc.Store.SetXtreamEnabled(ctx, true)
	if res := e.refresh(src.ID); res.Total != 2 {
		t.Fatalf("%+v", res)
	}
}

const guideXML = `<?xml version="1.0" encoding="UTF-8"?>
<tv>
<channel id="g.one"><display-name>Тест 1</display-name></channel>
<channel id="g.two"><display-name>Тест второй</display-name><display-name>Тест 2</display-name></channel>
<channel id="g.other"><display-name>Чужой</display-name></channel>
<programme start="20261111100000 +0000" stop="20261111110000 +0000" channel="g.one"><title>Давно</title></programme>
<programme start="20261112110000 +0000" stop="20261112130000 +0000" channel="g.one"><title lang="ru">Сейчас</title><desc>Описание</desc><category>Новости</category></programme>
<programme start="20261112130000 +0000" channel="g.one"><title>Без конца</title></programme>
<programme start="20261112140000 +0000" stop="20261112150000 +0000" channel="g.one"><title>Потом</title></programme>
<programme start="20261112110000 +0000" stop="20261112120000 +0000" channel="g.two"><title>Второй</title></programme>
<programme start="20261112110000 +0000" stop="20261112120000 +0000" channel="g.other"><title>Чужое</title></programme>
<programme start="20261125110000 +0000" stop="20261125120000 +0000" channel="g.one"><title>Далеко</title></programme>
</tv>`

func TestEPGStoresOnlyMatchedWindow(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	e.p.put("/pl.m3u", "#EXTM3U url-tvg=\""+e.p.url("/guide.xml")+"\"\n"+
		"#EXTINF:-1 tvg-id=\"g.one\",Первый тест\nhttp://s.example.test/1.ts\n#EXTINF:-1,Тест 2\nhttp://s.example.test/2.ts\n#EXTINF:-1,Без программы\nhttp://s.example.test/3.ts\n")
	e.p.put("/guide.xml", guideXML)
	src := e.source("A", "/pl.m3u", nil)
	e.refresh(src.ID)
	es, _ := e.svc.Store.EPGURLs(ctx, src.ID)
	if len(es) != 1 || !es[0].Auto {
		t.Fatalf("auto guide %+v", es)
	}
	if err := e.svc.RefreshEPG(ctx, es[0].ID); err != nil {
		t.Fatal(err)
	}
	rs, _ := e.svc.Store.DB.Query(`SELECT xml_channel, title, end_ms FROM iptv_programmes ORDER BY xml_channel, start_ms`)
	var got []string
	for rs.Next() {
		var ch, title string
		var end int64
		rs.Scan(&ch, &title, &end)
		got = append(got, ch+":"+title+"@"+time.UnixMilli(end).UTC().Format("15"))
	}
	rs.Close()
	if fmt.Sprint(got) != "[g.one:Сейчас@13 g.one:Без конца@14 g.one:Потом@15 g.two:Второй@12]" {
		t.Fatalf("programmes %v", got)
	}
	ms, _ := e.svc.Store.Members(ctx, src.ID)
	if ms[0].EPGID != "g.one" || ms[0].EPGHow != "tvg-id" || ms[0].EPGURLID != es[0].ID || ms[1].EPGID != "g.two" || ms[1].EPGHow != "name" || ms[2].EPGID != "" {
		t.Fatalf("bindings %+v", ms)
	}
	var xmlChans int
	e.svc.Store.DB.QueryRow(`SELECT count(*) FROM iptv_xml_channels WHERE epg_url_id=?`, es[0].ID).Scan(&xmlChans)
	if xmlChans != 3 {
		t.Fatal(xmlChans)
	}
	// a cut guide keeps what is stored
	e.p.put("/guide.xml", "TRUNCATE:"+guideXML[:len(guideXML)/2])
	if err := e.svc.RefreshEPG(ctx, es[0].ID); err == nil {
		t.Fatal("cut guide accepted")
	}
	var n int
	e.svc.Store.DB.QueryRow(`SELECT count(*) FROM iptv_programmes`).Scan(&n)
	got2, _ := e.svc.Store.EPGURL(ctx, es[0].ID)
	if n != 4 || got2.LastErr == "" {
		t.Fatalf("after cut: %d %q", n, got2.LastErr)
	}
	// a manual binding survives and its programmes are kept
	e.p.put("/guide.xml", guideXML)
	e.svc.Store.SetMemberEPG(ctx, ms[2].ID, es[0].ID, "g.other", "manual", true)
	e.svc.RefreshEPG(ctx, es[0].ID)
	e.svc.Store.DB.QueryRow(`SELECT count(*) FROM iptv_programmes WHERE xml_channel='g.other'`).Scan(&n)
	ms, _ = e.svc.Store.Members(ctx, src.ID)
	if n != 1 || ms[2].EPGID != "g.other" || !ms[2].EPGManual {
		t.Fatalf("manual %d %+v", n, ms[2])
	}
}

func TestOnOpenRefreshesOldOnly(t *testing.T) {
	e := newSvcEnv(t)
	e.p.put("/pl.m3u", m3u("Тест", 1, 2, "Новости"))
	src := e.source("A", "/pl.m3u", func(s *Source) { s.RefreshOnOpen = true })
	e.svc.OnOpen(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := e.svc.Store.Source(context.Background(), src.ID)
		if !got.LastOK.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("OnOpen did not refresh")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
