package channels

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/media"
)

// cue is a WebVTT cue, its times in seconds from the programme's start.
type cue struct {
	start, end float64
	text       string
}

// parseVTT reads the cues of the WebVTT ffmpeg writes for a programme
// (times offset by media.LiveSubOffset); a cue still being written (no
// blank line after it yet, unless final) is left out.
func parseVTT(b []byte, final bool) []cue {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	blocks := strings.Split(s, "\n\n")
	if !final && len(blocks) > 0 {
		blocks = blocks[:len(blocks)-1] // may be half written
	}
	var out []cue
	for _, bl := range blocks {
		lines := strings.Split(strings.Trim(bl, "\n"), "\n")
		for i, l := range lines {
			a, rest, ok := strings.Cut(l, " --> ")
			if !ok {
				continue
			}
			e, _, _ := strings.Cut(strings.TrimSpace(rest), " ")
			st, ok1 := vttSeconds(strings.TrimSpace(a))
			en, ok2 := vttSeconds(e)
			text := strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			if ok1 && ok2 && text != "" {
				out = append(out, cue{start: st - media.LiveSubOffset, end: en - media.LiveSubOffset, text: text})
			}
			break
		}
	}
	return out
}

// vttSeconds parses hh:mm:ss.mmm or mm:ss.mmm.
func vttSeconds(s string) (float64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var v float64
	for i, p := range parts {
		x, err := strconv.ParseFloat(p, 64)
		if err != nil || x < 0 || (i < len(parts)-1 && strings.Contains(p, ".")) {
			return 0, false
		}
		v = v*60 + x
	}
	return v, true
}

func vttTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int64(math.Round(sec * 1000))
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// vttSegment is the WebVTT segment of a video segment: the cues
// overlapping [from, from+dur) (seconds from the programme's start; a cue
// spanning segments is in each of them, unchanged), placed by
// X-TIMESTAMP-MAP at the segment's first PTS.
func vttSegment(cues []cue, from, dur float64, pts int64) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:%d,LOCAL:%s\n", pts%ptsWrap, vttTime(from))
	to := from + dur
	for _, c := range cues {
		if c.end > from && c.start < to {
			fmt.Fprintf(&b, "\n%s --> %s\n%s\n", vttTime(c.start), vttTime(c.end), c.text)
		}
	}
	return b.Bytes()
}
