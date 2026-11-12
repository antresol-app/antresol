package channels

import (
	"strings"
	"testing"
)

func TestParseVTTOfFFmpeg(t *testing.T) {
	b := []byte("WEBVTT\n\n16:38.000 --> 16:40.500\nПривет\n\n16:44.000 --> 16:50.000 align:start\nДве\nстроки\n\n01:00:00.000 --> 01:00:01.000\nполовина")
	got := parseVTT(b, false)
	if len(got) != 2 || got[0].start != -2 || got[0].end != 0.5 || got[1].start != 4 || got[1].end != 10 || got[1].text != "Две\nстроки" {
		t.Fatalf("%+v", got)
	}
	if got := parseVTT(b, true); len(got) != 3 || got[2].start != 2600 {
		t.Fatalf("final %+v", got)
	}
}

func TestVTTSegmentSplitsCuesAndMapsTime(t *testing.T) {
	cues := []cue{{-2, 0.5, "a"}, {4, 10, "b"}, {12, 13, "c"}}
	s0 := string(vttSegment(cues, 0, 6, 126000))
	s1 := string(vttSegment(cues, 6, 6, 666000))
	s2 := string(vttSegment(cues, 12, 6, ptsWrap+5))
	if !strings.HasPrefix(s0, "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000\n") ||
		!strings.Contains(s0, "00:00:00.000 --> 00:00:00.500\na\n") || !strings.Contains(s0, "00:00:04.000 --> 00:00:10.000\nb\n") {
		t.Fatalf("s0 %q", s0)
	}
	// the cue across the boundary is in both segments, unchanged
	if !strings.HasPrefix(s1, "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:666000,LOCAL:00:00:06.000\n") || !strings.Contains(s1, "00:00:04.000 --> 00:00:10.000\nb\n") ||
		strings.Contains(s1, "\na\n") || strings.Contains(s1, "\nc\n") {
		t.Fatalf("s1 %q", s1)
	}
	if !strings.Contains(s2, "MPEGTS:5,LOCAL:00:00:12.000") || !strings.Contains(s2, "\nc\n") || strings.Contains(s2, "\nb\n") {
		t.Fatalf("s2 %q", s2)
	}
	// none: an empty but valid segment
	if e := string(vttSegment(nil, 6, 6, 1)); e != "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:1,LOCAL:00:00:06.000\n" {
		t.Fatalf("empty %q", e)
	}
}
