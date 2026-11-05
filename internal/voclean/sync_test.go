package voclean

import (
	"context"
	"encoding/binary"
	"math"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/media"
)

// The whole path of a cleaned track to the player, on real ffmpeg: a file
// whose timeline starts at 1.5 s (a format start_time, as MPEG-TS and some
// Matroska files have), its voice-over cleaned by Clean, the offset the
// play API computes from cleaned_audio.start_ms and the probe, and the
// HLS session media.BuildArgs starts with the cleaned file as its second
// input: the clicks on the front left (copied from the voice-over) come
// out where the voice-over's own clicks do, from the start and after a
// seek. Shifting the input by start_ms itself (-itsoffset 1.5) puts them
// 1.5 s late: the offset is start_ms minus where the file begins now.
//
// The tolerance holds what ffmpeg 7.1 adds: there the cleaned track measures
// 5.3 ms late, the 256 samples at 48 kHz of E-AC-3's encoder delay (ffmpeg 9:
// no difference). Nobody hears that; the offset this test guards is seconds.
func TestCleanedTrackPlaysInSync(t *testing.T) {
	const tolerance = 0.006
	ff := ffmpegPath(t)
	probe := strings.TrimSuffix(ff, "ffmpeg") + "ffprobe"
	dir := t.TempDir()
	path := filepath.Join(dir, "in.mkv")
	// stream 0: the voice-over, clicks every 1.37 s on FL; stream 1: the original
	click := `if(lt(mod(t\,1.37)\,0.004)\,0.9\,0)`
	tone := `0.005*sin(2*PI*440*t)`
	vo := strings.Join([]string{click, tone, "0.4*" + tone, tone, tone, tone}, "|")
	orig := strings.Join([]string{tone, tone, tone, tone, tone, tone}, "|")
	runFF(t, ff, "-f", "lavfi", "-i", "aevalsrc="+vo+":s=48000:d=14:c=5.1", "-f", "lavfi", "-i", "aevalsrc="+orig+":s=48000:d=14:c=5.1",
		"-f", "lavfi", "-i", "testsrc=size=64x64:rate=25:duration=14",
		"-map", "2:v", "-map", "0:a", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast", "-g", "25", "-c:a", "pcm_s16le", "-metadata:s:a:0", "language=rus", "-metadata:s:a:1", "language=eng",
		"-output_ts_offset", "1.5", path)
	start, err := StartOffsetMs(context.Background(), probe, path)
	if err != nil || start != 1500 {
		t.Fatalf("fixture start %v %v", start, err)
	}
	p, err := media.Probe(context.Background(), probe, path)
	if err != nil || p.StartMs == nil || len(p.Audio) != 2 || p.Video == nil {
		t.Fatalf("%v %+v", err, p)
	}
	out := filepath.Join(dir, "1-0.mka")
	in := Input{Path: path, DurationMs: 14_000}
	for _, err := range Clean(context.Background(), ff, in, []Job{{Pair: Pair{Vo: p.Audio[0], Orig: p.Audio[1]}, Out: out}}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	off := Cleaned{StartMs: start}.OffsetMs(p.StartMs)
	if off != 0 {
		t.Fatalf("offset %v", off)
	}
	for _, ss := range []float64{0, 5} {
		own := hlsClicks(t, ff, path, media.Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: p.Audio[0].Index, AudioCodec: "pcm_s16le"}, ss)
		ext := hlsClicks(t, ff, path, media.Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 0, AudioCodec: "eac3",
			AudioInput: out, AudioInputCodec: "eac3", AudioInputOffsetMs: off}, ss)
		t.Logf("seek %v: own %v, cleaned %v", ss, own, ext)
		if len(own) < 3 || len(ext) < 3 {
			t.Fatalf("seek %v: clicks %v %v", ss, own, ext)
		}
		for i := range 3 {
			if d := math.Abs(own[i] - ext[i]); d > tolerance {
				t.Fatalf("seek %v: click %d off by %.4f s (%v vs %v)", ss, i, d, own, ext)
			}
		}
		if ss > 0 {
			continue
		}
		late := hlsClicks(t, ff, path, media.Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 0, AudioCodec: "eac3",
			AudioInput: out, AudioInputCodec: "eac3", AudioInputOffsetMs: start}, ss)
		t.Logf("seek %v, wrong offset: %v", ss, late)
		if !slices.ContainsFunc(late, func(x float64) bool { return math.Abs(x-own[1]-1.5) < tolerance }) {
			t.Fatalf("-itsoffset 1.5: %v vs %v", late, own)
		}
	}
}

func runFF(t *testing.T, ff string, args ...string) {
	t.Helper()
	if out, err := exec.Command(ff, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v %s", args, err, out)
	}
}

// hlsClicks runs the HLS session of d from ss as the server would and
// returns where the clicks of the first channel fall in its audio
// against its video (s from the session's start, every channel downmixed
// as clients hear them). The gap between the two is read from the first
// segment and the samples decoded as they are: ffmpeg 7.1, reading the
// playlist for its audio alone, counts from the audio's own start.
func hlsClicks(t *testing.T, ff, input string, d media.Decision, ss float64) []float64 {
	t.Helper()
	dir := t.TempDir()
	args := media.BuildArgs("none", input, d, ss, dir)
	if out, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Fatalf("hls %v: %v %s", args, err, out)
	}
	gap := audioLead(t, strings.TrimSuffix(ff, "ffmpeg")+"ffprobe", filepath.Join(dir, "seg00000.ts"))
	b, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,hls,mpegts",
		"-i", filepath.Join(dir, "index.m3u8"), "-map", "0:a:0",
		"-ac", "1", "-ar", "8000", "-f", "s16le", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	var at []float64
	last := -1 << 30
	for i := 0; i+1 < len(b); i += 2 {
		v := int16(binary.LittleEndian.Uint16(b[i:]))
		if n := i / 2; (v > 2500 || v < -2500) && n-last > 4000 {
			at = append(at, gap+float64(n)/8000)
			last = n
		}
	}
	return at
}

// audioLead is how long after the video a segment's audio starts (s).
func audioLead(t *testing.T, ffprobe, seg string) float64 {
	t.Helper()
	start := func(stream string) float64 {
		out, err := exec.Command(ffprobe, "-hide_banner", "-loglevel", "error", "-select_streams", stream,
			"-show_entries", "stream=start_time", "-of", "csv=p=0", seg).Output()
		if err != nil {
			t.Fatalf("ffprobe %s: %v", seg, err)
		}
		// the stream is listed twice, with its program
		f := strings.Fields(string(out))
		if len(f) == 0 {
			t.Fatalf("ffprobe %s: no start_time", seg)
		}
		v, err := strconv.ParseFloat(strings.Trim(f[0], ","), 64)
		if err != nil {
			t.Fatalf("ffprobe %s: %q: %v", seg, out, err)
		}
		return v
	}
	return start("a:0") - start("v:0")
}
