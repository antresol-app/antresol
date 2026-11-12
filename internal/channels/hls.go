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

// renderMaster is a channel's master playlist: the video variant with
// every audio slot (DEFAULT the Russian one) and every subtitle slot.
// Version 6: WebVTT renditions beside MPEG-TS.
func renderMaster(peak, avg int64, w, h int) []byte {
	var b bytes.Buffer
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for i, s := range AudioSlots {
		def, auto := "NO", "NO"
		if i == slotRU {
			def, auto = "YES", "YES"
		}
		if i == slotEN {
			auto = "YES"
		}
		fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=%s,AUTOSELECT=%s,CHANNELS=\"2\",URI=\"audio-%s.m3u8\"\n",
			s.Name, s.Lang, def, auto, s.ID)
	}
	for _, s := range SubSlots {
		fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"sub\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI=\"subs-%s.m3u8\"\n",
			s.Name, s.Lang, s.ID)
	}
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"%s\",AUDIO=\"aud\",SUBTITLES=\"sub\",CLOSED-CAPTIONS=NONE\nvideo.m3u8\n",
		peak, avg, w, h, liveCodecs)
	return b.Bytes()
}

// mediaKind picks a media playlist: the video (zero), audio slot audio-1,
// subtitle slot sub-1.
type mediaKind struct{ audio, sub int }

// renderMedia is a media playlist of the last window segments. Every
// rendition lists the same segments (numbers, discontinuities), so a
// player switching renditions stays where it is; an audio slot's lines
// name the programme's output that fills it.
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
			if s.prog != nil {
				out = s.prog.slots[k.audio-1]
			}
			d := s.dur
			if out < len(s.adur) {
				d = s.adur[out]
			}
			fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s\n", d, media.LiveAudioSegment(out, s.seq))
		case k.sub > 0:
			fmt.Fprintf(&b, "#EXTINF:%.3f,\ns-%s-%s.vtt\n", s.dur, SubSlots[k.sub-1].ID, n)
		default:
			fmt.Fprintf(&b, "#EXTINF:%.3f,\nseg%s.ts\n", s.dur, n)
		}
	}
	return b.Bytes()
}
