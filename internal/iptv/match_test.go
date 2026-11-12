package iptv

import "testing"

func TestNormName(t *testing.T) {
	for in, want := range map[string]string{
		"Тестовый Один HD":       "тестовый один",
		"  Ёлка   ТВ  (FHD) ":    "елка тв",
		"Demo.Three-SD":          "demo three",
		"Sample Two 4K HDR":      "sample two",
		"Sample Two UHD 50fps":   "sample two",
		"Stadium Sport 1 FHD XX": "stadium sport 1 fhd xx",
		"HD Classics":            "hd classics",
		"Канал +2":               "канал +2",
		"Канал orig":             "канал",
		"HD":                     "hd",
		"Movie Box | HD":         "movie box",
		"News 24/7":              "news 24 7",
	} {
		if got := NormName(in); got != want {
			t.Errorf("NormName(%q) = %q, want %q", in, got, want)
		}
	}
}

var testGuide = []XMLChannel{
	{ID: "ch.one", Names: []string{"Тестовый Один HD", "Test One"}},
	{ID: "stadium.1", Names: []string{"Stadium Sport 1 FHD XX", "Stadium Sport 1 HD XX", "Stadium Sport 1 XX"}},
	{ID: "land", Names: []string{"Земля"}},
	{ID: "land.4k", Names: []string{"Земля 4K"}},
	{ID: "Demo.Three", Names: []string{"Demo Three"}},
}

func TestMatchTvgID(t *testing.T) {
	m := NewMatcher(testGuide)
	if id, how := m.Match(Member{TvgID: "stadium.1", Name: "Whatever"}, nil); id != "stadium.1" || how != "tvg-id" {
		t.Fatalf("exact id: %q %q", id, how)
	}
	if id, how := m.Match(Member{TvgID: "demo.three", Name: "x"}, nil); id != "Demo.Three" || how != "tvg-id" {
		t.Fatalf("id ignoring case: %q %q", id, how)
	}
	if id, how := m.Match(Member{TvgID: "nope", Name: "Unknown"}, nil); id != "" || how != "" {
		t.Fatalf("no match: %q %q", id, how)
	}
}

func TestMatchDisplayNameVariants(t *testing.T) {
	m := NewMatcher(testGuide)
	for _, name := range []string{"Stadium Sport 1 FHD XX", "Stadium Sport 1 HD XX", "stadium sport 1 xx"} {
		if id, how := m.Match(Member{Name: name}, nil); id != "stadium.1" || how != "name" {
			t.Errorf("%q: %q %q", name, id, how)
		}
	}
	// tvg-name before the entry's name.
	if id, how := m.Match(Member{TvgName: "Test One", Name: "Земля"}, nil); id != "ch.one" || how != "name" {
		t.Fatalf("tvg-name first: %q %q", id, how)
	}
	// Normalised when there is no exact name.
	if id, how := m.Match(Member{Name: "Тестовый Один FHD"}, nil); id != "ch.one" || how != "norm-name" {
		t.Fatalf("normalised: %q %q", id, how)
	}
}

func TestMatchNormalisedOnlyWithoutCollision(t *testing.T) {
	m := NewMatcher(testGuide)
	// Exact names always match, collision or not.
	if id, how := m.Match(Member{Name: "Земля 4K"}, []string{"Земля HD"}); id != "land.4k" || how != "name" {
		t.Fatalf("exact 4K: %q %q", id, how)
	}
	// «Земля HD» alone in the playlist: normalised to the plain channel.
	if id, how := m.Match(Member{Name: "Земля HD"}, []string{"Другое"}); id != "land" || how != "norm-name" {
		t.Fatalf("alone: %q %q", id, how)
	}
	// «Земля HD» and «Земля UHD» both in the playlist: neither is normalised.
	if id, how := m.Match(Member{Name: "Земля HD"}, []string{"Земля UHD"}); id != "" || how != "" {
		t.Fatalf("collision: %q %q", id, how)
	}
	// The same name twice is not a collision.
	if id, _ := m.Match(Member{Name: "Земля HD"}, []string{"Земля HD"}); id != "land" {
		t.Fatalf("duplicate entry: %q", id)
	}
	// MatchAll agrees with Match over a playlist.
	ms := []Member{{Name: "Земля HD"}, {Name: "Земля UHD"}, {Name: "Тестовый Один FHD"}, {TvgID: "stadium.1"}}
	got := m.MatchAll(ms)
	want := []MatchResult{{}, {}, {XMLID: "ch.one", How: "norm-name"}, {XMLID: "stadium.1", How: "tvg-id"}}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("MatchAll[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
