package channels

import (
	"bytes"
	"fmt"
	"math"
	"strconv"

	"github.com/antresol-app/antresol/internal/media"
)

// liveCodecs: H.264 High (level 4.0, 1080p up to 30 fps) and AAC-LC.
const liveCodecs = "avc1.640028,mp4a.40.2"

// renderMaster is a channel run's master playlist: the video variant with
// every audio rendition of set (audio-<n>.m3u8) and every subtitle one
// (subs-<n>.m3u8). Version 6: WebVTT renditions beside MPEG-TS.
func renderMaster(set TrackSet, peak, avg int64, w, h int) []byte {
	var b bytes.Buffer
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	yes := func(v bool) string {
		if v {
			return "YES"
		}
		return "NO"
	}
	for n, r := range set.Audio {
		fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=%s,AUTOSELECT=%s,CHANNELS=\"2\",URI=\"audio-%d.m3u8\"\n",
			r.Name, r.Lang, yes(r.Default), yes(r.Autoselect), n)
	}
	for n, r := range set.Subs {
		fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"sub\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=%s,AUTOSELECT=%s,FORCED=NO,URI=\"subs-%d.m3u8\"\n",
			r.Name, r.Lang, yes(r.Default), yes(r.Autoselect), n)
	}
	subs := ""
	if len(set.Subs) > 0 {
		subs = ",SUBTITLES=\"sub\""
	}
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"%s\",AUDIO=\"aud\"%s,CLOSED-CAPTIONS=NONE\nvideo.m3u8\n",
		peak, avg, w, h, liveCodecs, subs)
	return b.Bytes()
}

// mediaKind picks a media playlist: the video (zero), audio rendition
// audio-1, subtitle rendition sub-1.
type mediaKind struct{ audio, sub int }

// renderMedia is a media playlist of the last window segments. Every
// rendition lists the same segments (numbers, discontinuities), so a
// player switching renditions stays where it is; an audio rendition's
// lines name the programme's output that plays it.
func renderMedia(segs []liveSeg, discGone, window int, k mediaKind) []byte {
	from := max(0, len(segs)-window)
	disc := discGone
	for _, s := range segs[:from] {
		if s.disc {
			disc++
		}
	}
	win := segs[from:]
	// one target duration for all renditions
	target := float64(media.LiveSegmentSeconds)
	for _, s := range win {
		target = math.Max(target, s.dur)
		for _, d := range s.adur {
			target = math.Max(target, d)
		}
	}
	var b bytes.Buffer
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", int(math.Ceil(target)))
	var first int64
	if len(win) > 0 {
		first = win[0].seq
	}
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", first)
	fmt.Fprintf(&b, "#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", disc)
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	for _, s := range win {
		if s.disc {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		n := strconv.FormatInt(s.seq, 10)
		switch {
		case k.audio > 0:
			out := 0
			if s.prog != nil && k.audio-1 < len(s.prog.audio) {
				out = s.prog.audio[k.audio-1]
			}
			d := s.dur
			if out < len(s.adur) {
				d = s.adur[out]
			}
			fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s\n", d, media.LiveAudioSegment(out, s.seq))
		case k.sub > 0:
			fmt.Fprintf(&b, "#EXTINF:%.3f,\ns-%d-%s.vtt\n", s.dur, k.sub-1, n)
		default:
			fmt.Fprintf(&b, "#EXTINF:%.3f,\nseg%s.ts\n", s.dur, n)
		}
	}
	return b.Bytes()
}
