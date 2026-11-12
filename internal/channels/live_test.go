package channels

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/subtitles"
)

func TestRenderPlaylistWindowAndDiscontinuitySequence(t *testing.T) {
	var segs []liveSeg
	for i := 0; i < 8; i++ {
		segs = append(segs, liveSeg{seq: int64(100 + i), dur: 6, disc: i == 2 || i == 6})
	}
	segs[5].dur = 3.2
	got := string(renderMedia(segs, 4, 4, mediaKind{}))
	want := "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:104\n#EXT-X-DISCONTINUITY-SEQUENCE:5\n" +
		"#EXT-X-INDEPENDENT-SEGMENTS\n#EXTINF:6.000,\nseg104.ts\n#EXTINF:3.200,\nseg105.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:6.000,\nseg106.ts\n#EXTINF:6.000,\nseg107.ts\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestRenderMasterAndRenditions(t *testing.T) {
	m := string(renderMaster(8_192_000, 6_192_000, 1920, 1080))
	want := "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-INDEPENDENT-SEGMENTS\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Русский",LANGUAGE="ru",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2",URI="audio-ru.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Русский 2",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=NO,CHANNELS="2",URI="audio-ru2.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,CHANNELS="2",URI="audio-en.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Оригинал",LANGUAGE="mul",DEFAULT=NO,AUTOSELECT=NO,CHANNELS="2",URI="audio-orig.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="sub",NAME="Русские",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="subs-ru.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="sub",NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="subs-en.m3u8"` + "\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=8192000,AVERAGE-BANDWIDTH=6192000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="aud",SUBTITLES="sub",CLOSED-CAPTIONS=NONE` + "\nvideo.m3u8\n"
	if m != want {
		t.Fatalf("got\n%s\nwant\n%s", m, want)
	}
	// programme A has two audio outputs (ru, en), B one (everything plays it)
	pa := &liveProg{outs: 2, slots: [4]int{0, 0, 1, 1}}
	pb := &liveProg{outs: 1}
	segs := []liveSeg{{seq: 10, dur: 6, prog: pa, adur: []float64{6.016, 6.01}}, {seq: 11, dur: 4, prog: pa, adur: []float64{4.1, 4}},
		{seq: 12, dur: 6, disc: true, prog: pb, adur: []float64{5.99}}}
	got := string(renderMedia(segs, 1, 10, mediaKind{audio: slotEN + 1}))
	want = "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:7\n#EXT-X-MEDIA-SEQUENCE:10\n#EXT-X-DISCONTINUITY-SEQUENCE:1\n#EXT-X-INDEPENDENT-SEGMENTS\n" +
		"#EXTINF:6.010,\na1-10.ts\n#EXTINF:4.000,\na1-11.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:5.990,\na0-12.ts\n"
	if got != want {
		t.Fatalf("audio en\n%s", got)
	}
	got = string(renderMedia(segs, 1, 2, mediaKind{sub: subEN + 1}))
	want = "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:11\n#EXT-X-DISCONTINUITY-SEQUENCE:1\n#EXT-X-INDEPENDENT-SEGMENTS\n" +
		"#EXTINF:4.000,\ns-en-11.vtt\n#EXT-X-DISCONTINUITY\n#EXTINF:6.000,\ns-en-12.vtt\n"
	if got != want {
		t.Fatalf("subs en\n%s", got)
	}
}

func TestRunPlaylistTakesCompletePairsOnly(t *testing.T) {
	b := []byte("#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6.000000,\nseg7.ts\n#EXTINF:5.5,\nseg8.ts\n#EXTINF:6.0,\n")
	got := runPlaylist(b)
	if len(got) != 2 || got[0].seq != 7 || got[1].dur != 5.5 {
		t.Fatalf("%+v", got)
	}
}

func TestLiveArgsQSVUsesTheGPUWhenItCan(t *testing.T) {
	in := media.LiveInput{Input: "/m/a.mkv", Audio: []media.LiveAudio{{Index: 1}}, VideoCodec: "hevc", PixFmt: "yuv420p10le", SrcWidth: 3840, SrcHeight: 1600,
		StartSec: 12.5, DurationSec: 100, StartNumber: 77, TsOffset: 30, OutDir: "/o", Playlist: "run.m3u8"}
	a := strings.Join(media.LiveArgs("qsv", in), " ")
	for _, want := range []string{"-hwaccel qsv -hwaccel_output_format qsv", "-ss 12.500", "-t 100.000 -i file:/m/a.mkv", "-map 0:v:0 -an", "-map 0:1 -vn",
		"vpp_qsv=w=1920:h=800:format=nv12,hwdownload,format=nv12,pad=1920:1080", "h264_qsv -low_power 1",
		"-output_ts_offset 30.000", "-start_number 77", "/o/seg%d.ts /o/run.m3u8", "-af apad -t 101.000", "/o/a0-%d.ts /o/run-a0.m3u8"} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in %s", want, a)
		}
	}
	// an external voice-over, a subtitle stream and a subtitle file
	in.Audio = append(in.Audio, media.LiveAudio{Input: "/m/a.LostFilm.mka", OffsetMs: 40, Tempo: 1.04})
	in.Subs = []media.LiveSub{{Index: 4, Out: "s.vtt"}, {Input: "/o/x.srt", Codec: "subrip", Out: "t.vtt"}}
	a = strings.Join(media.LiveArgs("qsv", in), " ")
	for _, want := range []string{"-itsoffset 0.04 -ss 13.000 -protocol_whitelist file -format_whitelist", "-i file:/m/a.LostFilm.mka",
		"-ss 12.500 -protocol_whitelist file -f srt -i file:/o/x.srt", "-map 1:a:0 -vn -sn -dn -af atempo=1.04,apad -t 101.000",
		// a subtitle output ends by itself (-readrate and a cut input would leave ffmpeg waiting)
		"-map 0:4 -t 100.000 -c:s webvtt -output_ts_offset 1000 -flush_packets 1 -f webvtt /o/s.vtt", "-map 2:0 -t 100.000 -c:s webvtt"} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in %s", want, a)
		}
	}
	in.Subs = nil
	in.Audio, in.SrcWidth = []media.LiveAudio{{Index: -1}}, 0
	a = strings.Join(media.LiveArgs("qsv", in), " ")
	if strings.Contains(a, "hwaccel") || !strings.Contains(a, "anullsrc") || !strings.Contains(a, "-map 1:a:0") || !strings.Contains(a, "format=nv12") {
		t.Errorf("no size, no audio: %s", a)
	}
}

// cycle is a Source playing the same programmes round and round.
type cycle struct {
	base  time.Time
	progs []Program
}

func (c cycle) Program(_ context.Context, ch int64, t time.Time) (Program, error) {
	var total time.Duration
	for _, p := range c.progs {
		total += p.End.Sub(p.Start)
	}
	n := t.Sub(c.base) / total
	start := c.base.Add(n * total)
	for i := 0; ; i++ {
		p := c.progs[i%len(c.progs)]
		d := p.End.Sub(p.Start)
		if !t.Before(start.Add(d)) {
			start = start.Add(d)
			continue
		}
		p.Start, p.End, p.Seq, p.Channel = start, start.Add(d), int64(i), ch
		return p, nil
	}
}

func ffprobeJSON(t *testing.T, args ...string) map[string]any {
	t.Helper()
	out, err := exec.Command("ffprobe", append([]string{"-v", "error", "-of", "json"}, args...)...).Output()
	if err != nil {
		t.Fatalf("ffprobe %v: %v", args, err)
	}
	var m map[string]any
	json.Unmarshal(out, &m)
	return m
}

// TestLiveCrossesProgrammeBoundaries runs real ffmpeg: programme A (a
// Matroska file with Russian, English and French audio and Russian
// subtitles) and B (video only, with a Russian voice-over and English
// subtitles beside it) play round. Every rendition lists the same
// segments with the same discontinuities; the video is 640×360 H.264
// alone, each audio segment AAC stereo; a slot B has no track for plays
// B's Russian voice; the subtitle segments carry the cues of their
// stretch, placed at their video segment's PTS; and ffmpeg plays every
// rendition of the master through both boundaries.
func TestLiveCrossesProgrammeBoundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("ffmpeg integration")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	mk := func(name string, args ...string) string {
		p := filepath.Join(dir, name)
		if out, err := exec.Command("ffmpeg", append(append([]string{"-v", "error", "-y"}, args...), p)...).CombinedOutput(); err != nil {
			t.Fatalf("make %s: %v %s", name, err, out)
		}
		return p
	}
	ruSrt := write("a.ru.srt", "1\n00:00:02,000 --> 00:00:04,000\nРаз\n\n2\n00:00:10,000 --> 00:00:16,000\nДва\n")
	a := mk("a.mkv", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=20",
		"-f", "lavfi", "-i", "sine=f=440:duration=20", "-f", "lavfi", "-i", "sine=f=880:duration=20", "-f", "lavfi", "-i", "sine=f=660:duration=20",
		"-i", ruSrt, "-map", "0", "-map", "1", "-map", "2", "-map", "3", "-map", "4",
		"-metadata:s:a:0", "language=rus", "-metadata:s:a:1", "language=eng", "-metadata:s:a:2", "language=fre", "-metadata:s:s:0", "language=rus",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-c:s", "srt")
	b := mk("b.mp4", "-f", "lavfi", "-i", "testsrc=size=640x480:rate=25:duration=8", "-c:v", "libx264", "-preset", "ultrafast")
	bru := mk("b.LostFilm.mka", "-f", "lavfi", "-i", "sine=f=330:duration=8", "-c:a", "aac")
	ben := write("b.en.srt", "1\n00:00:01,000 --> 00:00:02,500\nHello\n")
	probeA := media.ProbeResult{Video: &media.VideoStream{Codec: "h264", Width: 320, Height: 240},
		Audio: []media.AudioStream{{Index: 1, Codec: "aac", Lang: "rus", Default: true}, {Index: 2, Codec: "aac", Lang: "eng"}, {Index: 3, Codec: "aac", Lang: "fre"}},
		Subs:  []media.SubStream{{Index: 4, Codec: "subrip", Lang: "rus"}}}
	probeB := media.ProbeResult{Video: &media.VideoStream{Codec: "h264", Width: 640, Height: 480}}
	trA := Plan(probeA, nil, nil, PlanOptions{})
	trB := Plan(probeB, []media.ExternalAudio{{Path: bru, Studio: "LostFilm", Stream: media.AudioStream{Lang: "ru", Title: "LostFilm"}}},
		[]subtitles.External{{Path: ben, Lang: "en", Codec: "subrip"}}, PlanOptions{})
	base := time.Now().Add(-3 * time.Second)
	src := cycle{base: base, progs: []Program{
		{Entry: Entry{ItemID: 1, Start: base, End: base.Add(20 * time.Second)}, Path: a, Probe: probeA, Tracks: &trA},
		{Entry: Entry{ItemID: 2, Start: base, End: base.Add(8 * time.Second)}, Path: b, Probe: probeB, Tracks: &trB},
	}}
	live := &Live{FFmpeg: "ffmpeg", HW: "none", Dir: filepath.Join(dir, "live"), Source: src,
		ReadRate: -1, Width: 640, Height: 360, Bitrate: 800_000, Keep: 200, Window: 200, ReadySegments: 1}
	defer live.Close()
	ctx := context.Background()
	master, err := live.Playlist(ctx, 5, "index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(master), `URI="audio-orig.m3u8"`) || !strings.Contains(string(master), "\nvideo.m3u8\n") {
		t.Fatalf("master %s", master)
	}
	var pl string
	deadline := time.Now().Add(60 * time.Second)
	for {
		b, err := live.Playlist(ctx, 5, "video.m3u8")
		if err != nil {
			t.Fatal(err)
		}
		pl = string(b)
		if strings.Count(pl, "#EXT-X-DISCONTINUITY\n") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no boundary in time:\n%s", pl)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// every rendition: the same segments, the same discontinuities
	lists := map[string]string{"video.m3u8": pl}
	shape := func(s string) string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(l, "#EXT-X-MEDIA-SEQUENCE") || strings.HasPrefix(l, "#EXT-X-DISCONTINUITY") || strings.HasPrefix(l, "#EXT-X-TARGETDURATION") {
				out = append(out, l)
			} else if l != "" && !strings.HasPrefix(l, "#") {
				out = append(out, "seg")
			}
		}
		return strings.Join(out, ",")
	}
	for _, n := range []string{"audio-ru.m3u8", "audio-ru2.m3u8", "audio-en.m3u8", "audio-orig.m3u8", "subs-ru.m3u8", "subs-en.m3u8"} {
		b, err := live.Playlist(ctx, 5, n)
		if err != nil {
			t.Fatal(n, err)
		}
		lists[n] = string(b)
		if shape(string(b)) != shape(pl) {
			t.Fatalf("%s differs from the video:\n%s\nvideo:\n%s", n, b, pl)
		}
	}
	segs := runPlaylist([]byte(pl))
	var lastStart float64 = -1
	for _, s := range segs {
		p, _, err := live.Segment(5, "seg"+itoa(s.seq)+".ts")
		if err != nil {
			t.Fatalf("segment %d: %v", s.seq, err)
		}
		m := ffprobeJSON(t, "-show_streams", "-show_format", p)
		streams := m["streams"].([]any)
		v := streams[0].(map[string]any)
		if len(streams) != 1 || v["codec_name"] != "h264" || v["width"].(float64) != 640 || v["height"].(float64) != 360 {
			t.Fatalf("segment %d streams: %v", s.seq, streams)
		}
		start := atof(m["format"].(map[string]any)["start_time"].(string))
		if start <= lastStart {
			t.Fatalf("segment %d starts at %.3f, before the one before (%.3f)", s.seq, start, lastStart)
		}
		lastStart = start
	}
	// audio: each listed segment is AAC stereo 48 kHz and starts with its video segment
	c, _ := live.channel(5, false)
	c.mu.Lock()
	published := append([]liveSeg(nil), c.segs...)
	c.mu.Unlock()
	for _, slot := range AudioSlots {
		for i, n := range strings.Split(lists["audio-"+slot.ID+".m3u8"], "\n") {
			if n == "" || strings.HasPrefix(n, "#") {
				continue
			}
			p, _, err := live.Segment(5, n)
			if err != nil {
				t.Fatalf("%s line %d %s: %v", slot.ID, i, n, err)
			}
			m := ffprobeJSON(t, "-show_streams", "-show_format", p)
			au := m["streams"].([]any)[0].(map[string]any)
			if au["codec_name"] != "aac" || au["channels"].(float64) != 2 || au["sample_rate"] != "48000" {
				t.Fatalf("%s %s: %v", slot.ID, n, au)
			}
		}
	}
	// programme B: en and orig and ru2 play its Russian voice (the same files as ru), A has them all apart
	for _, s := range published {
		names := map[string]string{}
		for i, slot := range AudioSlots {
			names[slot.ID] = media.LiveAudioSegment(s.prog.slots[i], s.seq)
		}
		if s.prog.outs == 1 && (names["en"] != names["ru"] || names["orig"] != names["ru"]) {
			t.Fatalf("B's slots %v", names)
		}
		if s.prog.outs == 3 && (names["en"] == names["ru"] || names["orig"] == names["en"] || names["ru2"] != names["ru"]) {
			t.Fatalf("A's slots %v", names)
		}
		if s.prog.outs != 1 && s.prog.outs != 3 {
			t.Fatalf("outputs %d", s.prog.outs)
		}
	}
	// subtitles: «Два» (10–16 s of A, played from 3 s: 7–13 s) is in A's
	// second and third segment, at the right PTS; B's English cue in its first
	var sawRu, sawEn int
	for _, s := range published {
		ru, err := live.VTT(5, "s-ru-"+itoa(s.seq)+".vtt")
		if err != nil {
			t.Fatal(err)
		}
		en, _ := live.VTT(5, "s-en-"+itoa(s.seq)+".vtt")
		if !strings.HasPrefix(string(ru), "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:"+strconv.FormatInt(s.pts, 10)+",LOCAL:"+vttTime(s.at)+"\n") {
			t.Fatalf("map %q at %+v", ru, s)
		}
		if strings.Contains(string(ru), "Два") {
			sawRu++
			if s.prog.outs != 3 || (s.at < 5.9 || s.at > 12.1) || strings.Contains(string(ru), "Раз") {
				t.Fatalf("«Два» in the segment at %.3f of a programme with %d outputs", s.at, s.prog.outs)
			}
			// played from 3 s the first time round, from the start after;
			// ffmpeg 7.1 (Debian, the image's) starts the cue one AAC frame
			// (~23 ms) late, ffmpeg 9 on time: within 0.1 s either way
			if !cueNear(string(ru), 7, 13) && !cueNear(string(ru), 10, 16) {
				t.Fatalf("cue time %q", ru)
			}
		}
		if strings.Contains(string(en), "Hello") {
			sawEn++
			if s.prog.outs != 1 || s.at > 0.1 || !strings.Contains(string(en), "00:00:01.000 --> 00:00:02.500") {
				t.Fatalf("Hello at %.3f %q", s.at, en)
			}
		}
	}
	if sawRu < 2 || sawEn < 1 {
		t.Fatalf("cues seen ru %d en %d", sawRu, sawEn)
	}
	// ffmpeg as an HLS client plays every rendition through both boundaries
	runDirs, _ := filepath.Glob(filepath.Join(live.Dir, "5-*"))
	if len(runDirs) != 1 {
		t.Fatalf("run dirs %v", runDirs)
	}
	for n, body := range lists {
		if err := os.WriteFile(filepath.Join(runDirs[0], n), []byte(body+"#EXT-X-ENDLIST\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range published {
		for _, slot := range SubSlots {
			b, _ := live.VTT(5, "s-"+slot.ID+"-"+itoa(s.seq)+".vtt")
			os.WriteFile(filepath.Join(runDirs[0], "s-"+slot.ID+"-"+itoa(s.seq)+".vtt"), b, 0o644)
		}
	}
	os.WriteFile(filepath.Join(runDirs[0], "master.m3u8"), master, 0o644)
	for _, m := range []string{"0:v:0", "0:a:0", "0:a:1", "0:a:2", "0:a:3"} {
		if out, err := exec.Command("ffmpeg", "-v", "error", "-xerror", "-allowed_extensions", "ALL", "-i", filepath.Join(runDirs[0], "master.m3u8"),
			"-map", m, "-f", "null", "-").CombinedOutput(); err != nil {
			t.Fatalf("decode %s across the boundaries: %v %s", m, err, out)
		}
	}
	// nobody asks: the channel stops and its directory goes
	live.Stop(5)
	if left, _ := filepath.Glob(filepath.Join(live.Dir, "5-*")); len(left) != 0 {
		t.Fatalf("dir left: %v", left)
	}
}

func atof(s string) float64 {
	var v float64
	json.Unmarshal([]byte(s), &v)
	return v
}

func TestLiveLimitAndIdle(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil || testing.Short() {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=30", "-c:v", "libx264", "-preset", "ultrafast", p).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	base := time.Now()
	src := cycle{base: base, progs: []Program{{Entry: Entry{Start: base, End: base.Add(30 * time.Second)}, Path: p,
		Probe: media.ProbeResult{Video: &media.VideoStream{Codec: "h264", Width: 160, Height: 120}}}}}
	live := &Live{HW: "none", Dir: filepath.Join(dir, "live"), Source: src, Max: 1, IdleTimeout: 1500 * time.Millisecond,
		Width: 320, Height: 180, ReadySegments: 1}
	defer live.Close()
	if _, err := live.Playlist(context.Background(), 1, "index.m3u8"); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Playlist(context.Background(), 2, "index.m3u8"); err != ErrBusy {
		t.Fatalf("second channel: %v", err)
	}
	// the viewer switched: channel 1 is asked nothing for ZapIdle, 2 takes its place
	live.ZapIdle = 300 * time.Millisecond
	time.Sleep(500 * time.Millisecond)
	if _, err := live.Playlist(context.Background(), 2, "index.m3u8"); err != nil {
		t.Fatalf("zap: %v", err)
	}
	if r := live.Running(); len(r) != 1 || r[0] != 2 {
		t.Fatalf("running after zap: %v", r)
	}
	time.Sleep(4 * time.Second)
	if r := live.Running(); len(r) != 0 {
		t.Fatalf("still running: %v", r)
	}
}

// cueNear: vtt has a cue from about from to about to seconds (0.1 s).
func cueNear(vtt string, from, to float64) bool {
	for _, line := range strings.Split(vtt, "\n") {
		a, b, ok := strings.Cut(line, " --> ")
		if !ok {
			continue
		}
		x, ok1 := vttSeconds(strings.TrimSpace(a))
		y, ok2 := vttSeconds(strings.TrimSpace(b))
		if ok1 && ok2 && x-from < 0.1 && from-x < 0.1 && y-to < 0.1 && to-y < 0.1 {
			return true
		}
	}
	return false
}
