package iptv

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

var guideNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

func parseGuide(t *testing.T, src string, keep func(string) bool) ([]XMLChannel, []XMLProgramme, error) {
	t.Helper()
	var chans []XMLChannel
	var progs []XMLProgramme
	err := ParseXMLTV(strings.NewReader(src), keep, guideNow.Add(-24*time.Hour), guideNow.Add(7*24*time.Hour),
		func(c XMLChannel) { chans = append(chans, c) },
		func(p XMLProgramme) { progs = append(progs, p) })
	return chans, progs, err
}

func readGuide(t *testing.T) string {
	b, err := os.ReadFile("testdata/guide.xml")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseXMLTVChannelsAndWindow(t *testing.T) {
	chans, progs, err := parseGuide(t, readGuide(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(chans) != 3 {
		t.Fatalf("channels: %+v", chans)
	}
	if c := chans[0]; c.ID != "ch.one" || fmt.Sprint(c.Names) != "[Тестовый Один HD Test One]" || c.Icon != "http://img.example.test/one.png" {
		t.Fatalf("channel one: %+v", c)
	}
	var titles []string
	for _, p := range progs {
		titles = append(titles, p.Title)
	}
	want := "[Yesterday Show Вечерние новости Утренний фильм Cats & Dogs Late Show]"
	if fmt.Sprint(titles) != want {
		t.Fatalf("titles: %v, want %s", titles, want)
	}
	two := progs[2]
	if two.Channel != "ch.two" || two.SubTitle != "Part One" || two.Desc != "Русское описание." || two.Category != "Кино" ||
		two.Icon != "http://img.example.test/film.jpg" || !two.Start.Equal(time.Date(2026, 11, 12, 13, 0, 0, 0, time.UTC)) ||
		!two.Stop.Equal(time.Date(2026, 11, 12, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("ch.two programme: %+v", two)
	}
	if progs[3].Desc != "Plain desc" {
		t.Fatalf("skipped credits broke desc: %+v", progs[3])
	}
}

func TestParseXMLTVKeepFilter(t *testing.T) {
	asked := map[string]int{}
	chans, progs, err := parseGuide(t, readGuide(t), func(id string) bool { asked[id]++; return id == "ch.three" })
	if err != nil {
		t.Fatal(err)
	}
	if len(chans) != 3 {
		t.Fatalf("channels are all delivered: %d", len(chans))
	}
	if len(progs) != 2 || progs[0].Channel != "ch.three" || progs[1].Channel != "ch.three" {
		t.Fatalf("kept: %+v", progs)
	}
	if asked["ch.one"] == 0 {
		t.Fatal("keep not asked")
	}
}

func TestParseXMLTVTruncated(t *testing.T) {
	g := readGuide(t)
	cut := g[:strings.Index(g, "<title>Cats")+8]
	_, progs, err := parseGuide(t, cut, nil)
	if err == nil {
		t.Fatal("truncated guide: no error")
	}
	if len(progs) != 3 {
		t.Fatalf("delivered before the cut: %d", len(progs))
	}
	if _, _, err := parseGuide(t, "<!DOCTYPE html><html><body>login</body></html>", nil); err == nil {
		t.Fatal("html: no error")
	}
}

func TestParseXMLTVTimezones(t *testing.T) {
	for in, want := range map[string]time.Time{
		"20261112180000 +0300": time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC),
		"20261112180000 -0130": time.Date(2026, 11, 12, 19, 30, 0, 0, time.UTC),
		"20261112180000":       time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC),
		"202611121800 +0000":   time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC),
		"20261112180000 Z":     time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC),
		"20261112180000+0100":  time.Date(2026, 11, 12, 17, 0, 0, 0, time.UTC),
	} {
		got, err := ParseXMLTVTime(in)
		if err != nil || !got.Equal(want) {
			t.Errorf("%q: %v %v, want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "2026", "2026111218000x +0300", "20261112180000 +03"} {
		if _, err := ParseXMLTVTime(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// TestParseXMLTVRealSample parses a real guide (local only; the file is
// never in the repository): IPTV_EPG_SAMPLE=<path to .xml or .xml.gz>.
// It keeps the programmes of the first 300 channels, as a big playlist
// matched against a big guide would.
func TestParseXMLTVRealSample(t *testing.T) {
	path := os.Getenv("IPTV_EPG_SAMPLE")
	if path == "" {
		t.Skip("IPTV_EPG_SAMPLE not set")
	}
	start := time.Now()
	chans, progs, kept, err := parseFileForBench(path, 300)
	if err != nil {
		t.Fatal(err)
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	t.Logf("channels %d, programmes %d, kept %d, %v, heap sys %d MB, total alloc %d MB",
		chans, progs, kept, time.Since(start).Round(time.Millisecond), m.HeapSys>>20, m.TotalAlloc>>20)
	if chans == 0 || progs == 0 || kept == 0 {
		t.Fatal("nothing parsed")
	}
}
