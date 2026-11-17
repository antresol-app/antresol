package api

import (
	"context"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

func TestRedactToken(t *testing.T) {
	in := `http://127.0.0.1:8420/api/addon-input/ab/1?k=sekret: Invalid data; http://127.0.0.1:1/x?a=1&k=s2"`
	got := redactToken(in)
	if strings.Contains(got, "sekret") || strings.Contains(got, "s2") || !strings.Contains(got, "?k=…: Invalid data") || !strings.Contains(got, "a=1&k=…") {
		t.Fatal(got)
	}
}

func TestPreferredExternal_LanguageOrder(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	if err := e.repo.SetPlaybackPrefs(ctx, 1, library.PlaybackPrefs{AudioLangs: []string{"ru"}, SubMode: "off", SubSize: "m", SubBackground: "translucent"}); err != nil {
		t.Fatal(err)
	}
	h := &lib{Deps: Deps{Library: e.repo, Progress: e.prog, DB: e.db}}
	en := media.ProbeResult{Audio: []media.AudioStream{{Lang: "en"}}}
	ext := []extAudio{
		{path: "a", stream: media.AudioStream{Lang: "en"}},
		{path: "b", studio: "Студия А", stream: media.AudioStream{Lang: "ru"}},
		{path: "c", studio: "Студия Б", stream: media.AudioStream{Lang: "ru"}},
	}
	if k, _ := h.preferredExternal(ctx, 1, en, ext, nil, func() string { return "Студия Б" }); k != 2 {
		t.Fatalf("series voice: k = %d, want 2", k)
	}
	if k, _ := h.preferredExternal(ctx, 1, en, ext, nil, func() string { return "" }); k != 1 {
		t.Fatalf("no series voice: k = %d, want 1", k)
	}
	ru := media.ProbeResult{Audio: []media.AudioStream{{Lang: "ru"}}}
	if k, _ := h.preferredExternal(ctx, 1, ru, ext, nil, func() string { return "Студия Б" }); k != -1 {
		t.Fatalf("video has ru: k = %d, want -1", k)
	}
}

func TestTrackLang(t *testing.T) {
	for _, in := range []string{"rus", "ru-RU"} {
		if got := trackLang(in); got != "ru" {
			t.Fatalf("trackLang(%q) = %q, want ru", in, got)
		}
	}
}
