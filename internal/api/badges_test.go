package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

func fileWithProbe(t *testing.T, path string, size int64, p media.ProbeResult) library.File {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return library.File{Path: path, Size: size, ProbeJSON: string(b)}
}

func TestMusicBadges(t *testing.T) {
	cases := []struct {
		name string
		a    media.AudioStream
		want []string
	}{
		{"hires flac", media.AudioStream{Codec: "flac", BitDepth: 24, SampleRate: 96000}, []string{"FLAC 24/96"}},
		{"cd flac", media.AudioStream{Codec: "flac", BitDepth: 16, SampleRate: 44100}, []string{"FLAC 16/44.1"}},
		{"mp3", media.AudioStream{Codec: "mp3", BitRate: 320000, SampleRate: 44100}, []string{"MP3 320"}},
		{"aac", media.AudioStream{Codec: "aac", BitRate: 255800}, []string{"AAC 256"}},
		{"alac", media.AudioStream{Codec: "alac", BitDepth: 24, SampleRate: 48000}, []string{"ALAC 24/48"}},
		{"flac without depth", media.AudioStream{Codec: "flac"}, []string{"FLAC"}},
	}
	for _, c := range cases {
		f := fileWithProbe(t, "/m/a.x", 1, media.ProbeResult{Audio: []media.AudioStream{c.a}})
		if got := musicBadges(f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if got := musicBadges(library.File{}); got != nil {
		t.Errorf("no probe: %v", got)
	}
}

func TestVideoBadges(t *testing.T) {
	p := media.ProbeResult{
		Video: &media.VideoStream{Height: 2160, HDR: true},
		Audio: []media.AudioStream{{Lang: "rus"}, {Lang: "eng"}, {Lang: "rus"}},
		Subs:  []media.SubStream{{Lang: "eng"}},
	}
	got := videoBadges(i18n.RU, []library.File{fileWithProbe(t, "/m/f.mkv", 1, p)}, nil)
	want := []string{"4K", "HDR", "Звук RU EN", "Субтитры EN"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	p.Video = &media.VideoStream{Height: 1080, HDR: true, CodecTag: "dvh1"}
	p.Subs = nil
	got = videoBadges(i18n.RU, []library.File{fileWithProbe(t, "/m/f.mkv", 1, p)}, nil)
	if want := []string{"1080p", "Dolby Vision", "Звук RU EN"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	if videoBadges(i18n.RU, nil, nil) != nil {
		t.Error("no files must give no badges")
	}
	// only «Мои языки»: a Ukrainian track and Japanese subtitles get no badge
	p.Audio = []media.AudioStream{{Lang: "ukr"}, {Lang: "rus"}, {Lang: "eng"}}
	p.Subs = []media.SubStream{{Lang: "jpn"}}
	got = videoBadges(i18n.RU, []library.File{fileWithProbe(t, "/m/f.mkv", 1, p)}, []string{"ru", "en"})
	if want := []string{"1080p", "Dolby Vision", "Звук RU EN"}; !reflect.DeepEqual(got, want) {
		t.Errorf("mine: got %v want %v", got, want)
	}
}

// The tier counts the width too: scope films are not 720p.
func TestResolutionTiers(t *testing.T) {
	for _, c := range []struct {
		w, h int
		want string
	}{
		{3840, 2160, "4K"}, {3840, 1600, "4K"}, {4096, 1716, "4K"},
		{2560, 1440, "1440p"}, {2560, 1072, "1440p"},
		{1920, 1080, "1080p"}, {1920, 800, "1080p"}, {2048, 858, "1080p"}, {1080, 1920, "1080p"},
		{1280, 720, "720p"}, {1280, 536, "720p"},
		{720, 576, "576p"}, {720, 480, "SD"}, {0, 0, ""},
	} {
		if got := resolution(c.w, c.h); got != c.want {
			t.Errorf("%d×%d: %q, want %q", c.w, c.h, got, c.want)
		}
	}
}

func TestBookBadges(t *testing.T) {
	files := []library.File{{Path: "/b/x.epub", Size: 100}}
	if got, want := bookBadges(bookMeta{Lang: "ru"}, files), []string{"RU", "EPUB"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	if got, want := bookBadges(bookMeta{}, []library.File{{Path: "/b/x.fb2.zip"}}), []string{"FB2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	ab := []library.File{{Size: 700 << 20}, {Size: 800 << 20}}
	if got, want := audiobookBadges(i18n.RU, bookMeta{Lang: "rus"}, ab), []string{"RU", "1,5 ГБ"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	if got := sizeBadge(i18n.RU, 842<<20); got != "842 МБ" {
		t.Errorf("size %q", got)
	}
}

// showCache starts over past showCacheMax rather than growing for good; an
// update of a cached show never drops the rest.
func TestShowCacheBounded(t *testing.T) {
	var c showCache
	for id := int64(0); id < showCacheMax; id++ {
		c.put(id, showEntry{key: "k"})
	}
	c.put(0, showEntry{key: "k2"})
	if len(c.m) != showCacheMax {
		t.Fatalf("an update must not reset: %d", len(c.m))
	}
	c.put(showCacheMax, showEntry{key: "k"})
	if len(c.m) != 1 {
		t.Fatalf("cache not reset past its bound: %d", len(c.m))
	}
	if e, ok := c.get(showCacheMax); !ok || e.key != "k" {
		t.Fatalf("new entry lost: %v %v", e, ok)
	}
}
