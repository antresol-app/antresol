package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// A big IPTV setup (thousands of playlist channels in tens of groups, a
// guide for each): the viewer's list stays light enough to load at once.

// bigPlaylist: n channels in groups groups, each with a tvg-id, a logo and
// a stream.
func bigPlaylist(n, groups int) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "#EXTINF:-1 tvg-id=\"ch%d\" tvg-logo=\"http://logo.example.test/ch%d.png\" group-title=\"Группа %d\",Канал номер %d\nhttp://stream.example.test/live/u1/secret9/%d.m3u8\n", i, i, i%groups+1, i, i)
	}
	return b.String()
}

// bigGuide: for n channels, programmes of 30–60 minutes from 12 h ago to
// 24 h ahead, each with a description and an icon.
func bigGuide(n int, now time.Time) string {
	f := func(t time.Time) string { return t.UTC().Format("20060102150405 +0000") }
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><tv>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<channel id="ch%d"><display-name>Канал номер %d</display-name></channel>`, i, i)
	}
	desc := strings.Repeat("Подробное описание передачи для телегида. ", 8)
	for i := 1; i <= n; i++ {
		t := now.Add(-12 * time.Hour).Truncate(30 * time.Minute)
		for k := 0; t.Before(now.Add(24 * time.Hour)); k++ {
			d := time.Duration(30+(i*7+k*13)%31) * time.Minute
			fmt.Fprintf(&b, `<programme start="%s" stop="%s" channel="ch%d"><title>Передача %d</title><sub-title>Выпуск %d</sub-title><desc>%s</desc><category>Разное</category><icon src="http://img.example.test/p%d.jpg"/></programme>`,
				f(t), f(t.Add(d)), i, k, k, desc, k)
			t = t.Add(d)
		}
	}
	b.WriteString(`</tv>`)
	return b.String()
}

// addBigSource: a source of n channels with its guide, refreshed.
func (e *tvEnv) addBigSource(n, groups int) {
	e.t.Helper()
	e.p.put("/get.php", bigPlaylist(n, groups))
	e.p.put("/epg.xml", bigGuide(n, time.Now()))
	body := `{"name":"Большой","defaultShown":true,"url":"` + e.p.srv.URL + `/get.php?username=u1&password=secret9","epg":[{"url":"` + e.p.srv.URL + `/epg.xml?password=secret9"}]}`
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
}

// TestBigPlaylistList: ANTRESOL_BIG_TV=<n> measures GET /api/channels with
// n playlist channels (bytes, time); the default 600 checks the answer
// stays light per channel.
func TestBigPlaylistList(t *testing.T) {
	n := 600
	if s := os.Getenv("ANTRESOL_BIG_TV"); s != "" {
		fmt.Sscan(s, &n)
	}
	e := newTVEnv(t)
	e.addBigSource(n, 40)
	e.list(e.admin) // warm
	sizes := map[string]int{}
	for _, path := range []string{"/api/channels", "/api/channels?brief=1"} {
		start := time.Now()
		w := e.do("GET", path, "", e.admin)
		took := time.Since(start)
		if w.Code != 200 {
			t.Fatalf("%s %d %s", path, w.Code, w.Body)
		}
		var l struct {
			Channels   []channelJSON
			Categories []categoryJSON
		}
		json.Unmarshal(w.Body.Bytes(), &l)
		per := w.Body.Len() / max(1, len(l.Channels))
		t.Logf("%s: %d channels, %d categories, %d bytes (%d per channel), %v", path, len(l.Channels), len(l.Categories), w.Body.Len(), per, took)
		if len(l.Channels) != n || len(l.Categories) != MaxTags || l.Channels[0].Now == nil || l.Channels[0].Next == nil {
			t.Fatalf("%d channels %d categories", len(l.Channels), len(l.Categories))
		}
		sizes[path] = per
	}
	// the web's list: no descriptions, under half the full answer
	if b := sizes["/api/channels?brief=1"]; b*2 > sizes["/api/channels"] || b > 1000 {
		t.Fatalf("brief %d bytes per channel, full %d", b, sizes["/api/channels"])
	}
}
