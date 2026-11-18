package iptv

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func parseFile(t *testing.T, name string) (Playlist, error) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return ParseM3U(f, MaxEntries)
}

func TestParseM3UAttributes(t *testing.T) {
	p, err := parseFile(t, "attrs.m3u")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Entries) != 4 {
		t.Fatalf("entries: %d %+v", len(p.Entries), p.Entries)
	}
	want := []Entry{
		{Name: "News One HD", TvgID: "news.one", TvgName: "News One", Logo: "http://img.example.test/n1.png", Group: "News",
			URL: "http://stream.example.test/live/news1.m3u8", UserAgent: "TestPlayer/1.0", Referrer: "http://ref.example.test/"},
		{Name: "Movie Two, Classic", TvgID: "movie.two", Group: "Movies", URL: "http://stream.example.test/live/movie2.ts"},
		{Name: "Kids Fun", TvgName: "Kids, Fun", Group: "Kids", URL: "http://stream.example.test/live/kids.m3u8",
			UserAgent: "KodiUA/2", Referrer: "http://k.example.test/"},
		{Name: "Plain", Group: "Misc", URL: "http://stream.example.test/plain.m3u8", UserAgent: "PipeUA", Referrer: "http://p.example.test/"},
	}
	for i, w := range want {
		if p.Entries[i] != w {
			t.Errorf("entry %d:\n got %+v\nwant %+v", i, p.Entries[i], w)
		}
	}
}

func TestParseM3UHeaderEPG(t *testing.T) {
	p, err := parseFile(t, "attrs.m3u")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://epg.example.test/a.xml.gz", "http://epg.example.test/b.xml"}
	if fmt.Sprint(p.EPGURLs) != fmt.Sprint(want) {
		t.Fatalf("epg urls: %q", p.EPGURLs)
	}
}

func TestParseM3URejectsHTML(t *testing.T) {
	if _, err := parseFile(t, "login.html"); !errors.Is(err, ErrNotM3U) {
		t.Fatalf("html: %v", err)
	}
	if _, err := ParseM3U(strings.NewReader(`{"error":"auth"}`), MaxEntries); !errors.Is(err, ErrNotM3U) {
		t.Fatalf("json: %v", err)
	}
	if _, err := ParseM3U(strings.NewReader(""), MaxEntries); !errors.Is(err, ErrEmpty) {
		t.Fatalf("0 bytes: %v", err)
	}
}

func TestParseM3UEmpty(t *testing.T) {
	if _, err := parseFile(t, "empty.m3u"); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
}

func TestParseM3UCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for i := 0; i < MaxEntries+1; i++ {
		fmt.Fprintf(&b, "#EXTINF:-1,Ch %d\nhttp://stream.example.test/%d.ts\n", i, i)
	}
	if _, err := ParseM3U(strings.NewReader(b.String()), MaxEntries); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("cap: %v", err)
	}
	p, err := ParseM3U(strings.NewReader(b.String()), MaxEntries+1)
	if err != nil || len(p.Entries) != MaxEntries+1 {
		t.Fatalf("under cap: %d %v", len(p.Entries), err)
	}
}

func TestXtreamURLs(t *testing.T) {
	m3u, xml := XtreamURLs("http://xt.example.test:8080/", "us er", "p&ss")
	if m3u != "http://xt.example.test:8080/get.php?username=us+er&password=p%26ss&type=m3u_plus&output=ts" {
		t.Fatalf("m3u: %s", m3u)
	}
	if xml != "http://xt.example.test:8080/xmltv.php?username=us+er&password=p%26ss" {
		t.Fatalf("xmltv: %s", xml)
	}
	m3u, _ = XtreamURLs("xt.example.test", "u", "p")
	if !strings.HasPrefix(m3u, "http://xt.example.test/get.php?") {
		t.Fatalf("no scheme: %s", m3u)
	}
}

func TestPlaylistTitleAndDefaultName(t *testing.T) {
	p, err := ParseM3U(strings.NewReader("#EXTM3U\n#PLAYLIST: Мой плейлист \n#EXTINF:-1,A\nhttp://x.test/a\n"), 10)
	if err != nil || p.Title != "Мой плейлист" {
		t.Fatalf("title %q %v", p.Title, err)
	}
	for in, want := range map[string]string{
		"http://www.AkciaTV.org/list.m3u?x=1": "akciatv.org",
		"https://iptv.example.test:8080/get":  "iptv.example.test",
		"provider.test:8000":                  "provider.test",
		"":                                    "",
	} {
		if got := DefaultSourceName(in); got != want {
			t.Errorf("DefaultSourceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseM3UExtHTTP(t *testing.T) {
	p, err := ParseM3U(strings.NewReader("#EXTM3U\n#EXTINF:-1,One\n#EXTHTTP:{\"User-Agent\":\"HttpUA/3\",\"Referer\":\"http://r.example.test/\",\"Cookie\":\"x\"}\nhttp://s.example.test/1.m3u8\n"+
		"#EXTINF:-1,Two\n#EXTHTTP:not json\nhttp://s.example.test/2.m3u8\n"), MaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	if e := p.Entries[0]; e.UserAgent != "HttpUA/3" || e.Referrer != "http://r.example.test/" {
		t.Fatalf("one %+v", e)
	}
	if e := p.Entries[1]; e.UserAgent != "" || e.Referrer != "" || e.URL != "http://s.example.test/2.m3u8" {
		t.Fatalf("two %+v", e)
	}
}
