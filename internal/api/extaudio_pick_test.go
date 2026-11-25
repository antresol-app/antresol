package api

import (
	"testing"

	"github.com/antresol-app/antresol/internal/media"
)

func TestAskedAudio(t *testing.T) {
	ext := []extAudio{{src: "aa"}, {src: "bb"}, {src: "cc"}}
	at := func(i int) *int { return &i }
	for _, c := range []struct {
		name  string
		asked *int
		src   string
		want  audioPick
	}{
		{"nothing named", nil, "", audioPick{pos: -1, own: -1}},
		{"own track", at(3), "", audioPick{pos: -1, own: 3, named: true}},
		{"external by position", at(externalAudioBase + 1), "", audioPick{pos: 1, own: -1, named: true}},
		{"external by src at its position", at(externalAudioBase + 2), "cc", audioPick{pos: 2, own: -1, named: true}},
		{"src beats a shifted position", at(externalAudioBase), "cc", audioPick{pos: 2, own: -1, named: true}},
		{"src gone", at(externalAudioBase), "zz", audioPick{pos: -1, own: -1, named: true, gone: true}},
		{"position out of range", at(externalAudioBase + 9), "", audioPick{pos: -1, own: -1, named: true, gone: true}},
	} {
		if got := askedAudio(ext, c.asked, c.src); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestDecideAudio_ExternalTrackIsNumberedAfterTheBase(t *testing.T) {
	p := media.ProbeResult{DurationMs: 60_000, Container: "mp4",
		Video: &media.VideoStream{Codec: "h264", Width: 1280, Height: 720},
		Audio: []media.AudioStream{{Index: 1, Codec: "aac", Channels: 2}}}
	caps := media.ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, Containers: []string{"mp4"}}
	ext := []extAudio{{path: "/a.mka", stream: media.AudioStream{Codec: "aac", Channels: 2}}, {path: "/b.mka", stream: media.AudioStream{Codec: "aac", Channels: 2}}}
	if _, idx := decideAudio(p, caps, ext, audioPick{pos: 1, own: -1}, 0); idx != externalAudioBase+1 {
		t.Fatalf("external: audioIndex = %d", idx)
	}
	d, idx := decideAudio(p, caps, ext, audioPick{pos: -1, own: -1}, 0)
	if idx != d.AudioIndex || idx != 1 {
		t.Fatalf("own default: audioIndex = %d, decision %d", idx, d.AudioIndex)
	}
}
