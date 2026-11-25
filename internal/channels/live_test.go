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
	o := PlanOptions{Studios: []string{"Кубик в Кубе"}}
	set := BuildSet(setProgs(o), o)
	m := string(renderMaster(set, 8_192_000, 6_192_000, 1920, 1080))
	want := "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-INDEPENDENT-SEGMENTS\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Кубик в Кубе",LANGUAGE="ru",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2",URI="audio-0.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="LostFilm",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=NO,CHANNELS="2",URI="audio-1.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Дубляж",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=NO,CHANNELS="2",URI="audio-2.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Русский",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=NO,CHANNELS="2",URI="audio-3.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Русский 2",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=NO,CHANNELS="2",URI="audio-4.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Оригинал (English)",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,CHANNELS="2",URI="audio-5.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Оригинал (Français)",LANGUAGE="fr",DEFAULT=NO,AUTOSELECT=YES,CHANNELS="2",URI="audio-6.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=NO,CHANNELS="2",URI="audio-7.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="sub",NAME="Русские",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="subs-0.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="sub",NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="subs-1.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="sub",NAME="English 2",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=NO,FORCED=NO,URI="subs-2.m3u8"` + "\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=8192000,AVERAGE-BANDWIDTH=6192000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="aud",SUBTITLES="sub",CLOSED-CAPTIONS=NONE` + "\nvideo.m3u8\n"
	if m != want {
		t.Fatalf("got\n%s\nwant\n%s", m, want)
	}
	// no subtitles anywhere: no subtitle group
	m = string(renderMaster(BuildSet(nil, PlanOptions{}), 1, 1, 640, 360))
	if strings.Contains(m, "SUBTITLES") || !strings.Contains(m, `NAME="Звук",LANGUAGE="und",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2",URI="audio-0.m3u8"`) {
		t.Fatalf("empty set %s", m)
	}
	// programme A has two audio outputs (rendition 0, 1 → 0; 2 → 1), B one
	pa := &liveProg{outs: 2, audio: []int{0, 0, 1}, subs: make([]*liveSubs, 2)}
	pb := &liveProg{outs: 1, audio: []int{0, 0, 0}}
	segs := []liveSeg{{seq: 10, dur: 6, prog: pa, adur: []float64{6.016, 6.01}}, {seq: 11, dur: 4, prog: pa, adur: []float64{4.1, 4}},
		{seq: 12, dur: 6, disc: true, prog: pb, adur: []float64{5.99}}}
	got := string(renderMedia(segs, 1, 10, mediaKind{audio: 3}))
	want = "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:7\n#EXT-X-MEDIA-SEQUENCE:10\n#EXT-X-DISCONTINUITY-SEQUENCE:1\n#EXT-X-INDEPENDENT-SEGMENTS\n" +
		"#EXTINF:6.010,\na1-10.ts\n#EXTINF:4.000,\na1-11.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:5.990,\na0-12.ts\n"
	if got != want {
		t.Fatalf("audio 2\n%s", got)
	}
	got = string(renderMedia(segs, 1, 2, mediaKind{sub: 2}))
	want = "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:11\n#EXT-X-DISCONTINUITY-SEQUENCE:1\n#EXT-X-INDEPENDENT-SEGMENTS\n" +
		"#EXTINF:4.000,\ns-1-11.vtt\n#EXT-X-DISCONTINUITY\n#EXTINF:6.000,\ns-1-12.vtt\n"
	if got != want {
		t.Fatalf("subs 1\n%s", got)
	}
}

// An AAC-LC stereo 48 kHz source goes out as it is: no encode, no apad;
// the others are encoded as before.
func TestLiveArgsCopiesAAC(t *testing.T) {
	in := media.LiveInput{Input: "/m/a.mkv", Audio: []media.LiveAudio{{Index: 1, Copy: true}, {Index: 2}, {Input: "/m/b.mka", Copy: true}, {Index: -1, Copy: true}},
		DurationSec: 100, OutDir: "/o", Playlist: "run.m3u8"}
	a := strings.Join(media.LiveArgs("none", in), " ")
	for _, want := range []string{
		"-map 0:1 -vn -sn -dn -t 100.000 -c:a copy -f hls", "/o/a0-%d.ts /o/run-a0.m3u8",
		"-map 0:2 -vn -sn -dn -af apad -t 101.000 -c:a aac -b:a 192k -ac 2 -ar 48000", "/o/a1-%d.ts",
		"-map 1:a:0 -vn -sn -dn -t 100.000 -c:a copy -f hls", "/o/a2-%d.ts",
		// silence is never "copied"
		"-map 2:a:0 -vn -sn -dn -af apad -t 101.000 -c:a aac", "/o/a3-%d.ts",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in %s", want, a)
		}
	}
	if strings.Count(a, "-c:a copy") != 2 || strings.Count(a, "apad") != 2 {
		t.Errorf("copies %s", a)
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
		"-map 1:a:0 -vn -sn -dn -af atempo=1.04,apad -t 101.000"} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in %s", want, a)
		}
	}
	// the subtitles are an ffmpeg of their own: a subtitle stream waits for
	// a cue past the cut, which would keep the programme's ffmpeg reading
	if strings.Contains(a, "webvtt") || strings.Contains(a, "x.srt") {
		t.Errorf("subtitles in the programme's ffmpeg: %s", a)
	}
	in.Throttle = media.Throttle{Rate: 1, Burst: 20}
	s := strings.Join(media.LiveSubArgs(in), " ")
	for _, want := range []string{"-ss 12.500 -readrate 1.000 -readrate_initial_burst 20.000 -t 100.000 -i file:/m/a.mkv",
		"-ss 12.500 -protocol_whitelist file -f srt -i file:/o/x.srt",
		"-map 0:4 -t 100.000 -c:s webvtt -output_ts_offset 1000 -flush_packets 1 -f webvtt /o/s.vtt", "-map 1:0 -t 100.000 -c:s webvtt"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	if strings.Contains(s, "-map 0:v") || strings.Contains(s, "hwaccel") {
		t.Errorf("the subtitles' ffmpeg reads more than subtitles: %s", s)
	}
	// a file of subtitles alone: no input for the programme's file
	in.Subs = []media.LiveSub{{Input: "/o/x.srt", Codec: "subrip", Out: "t.vtt"}}
	if s := strings.Join(media.LiveSubArgs(in), " "); strings.Contains(s, "a.mkv") || !strings.Contains(s, "-map 0:0 -t 100.000") {
		t.Errorf("subtitle file only: %s", s)
	}
	in.Throttle = media.Throttle{}
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

// programSet is the track set of what a test Source schedules from then
// on (the job of Service.TrackSet in production).
func programSet(ctx context.Context, src Source, id int64, from time.Time) TrackSet {
	var progs []Tracks
	t := from
	for range 200 {
		p, err := src.Program(ctx, id, t)
		if err != nil || !p.End.After(t) {
			break
		}
		if !p.Skip {
			tr := Plan(p.Probe, nil, nil, PlanOptions{})
			if p.Tracks != nil {
				tr = *p.Tracks
			}
			progs = append(progs, tr)
		}
		t = p.End
		if t.Sub(from) >= SetHorizon {
			break
		}
	}
	return BuildSet(progs, PlanOptions{})
}

func (c cycle) TrackSet(ctx context.Context, ch int64, from time.Time) TrackSet {
	return programSet(ctx, c, ch, from)
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
// Matroska file with Russian AAC-LC 48 kHz audio — copied —, English
// 44.1 kHz — encoded — and French, and Russian subtitles) and B (video
// only, with a LostFilm voice-over and English subtitles beside it) play
// round. The run's set has both programmes' keys (Русский, LostFilm,
// Оригинал (Français), English; Русские, English); every rendition
// lists the same segments with the same discontinuities; the video is
// 640×360 H.264 alone, each audio segment AAC stereo 48 kHz; a key a
// programme lacks plays its Russian voice; subtitles it lacks are empty
// WebVTT; and ffmpeg plays every rendition of the master through both
// boundaries.
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
		"-f", "lavfi", "-i", "sine=f=440:duration=20:sample_rate=48000", "-f", "lavfi", "-i", "sine=f=880:duration=20:sample_rate=44100",
		"-f", "lavfi", "-i", "sine=f=660:duration=20:sample_rate=48000",
		"-i", ruSrt, "-map", "0", "-map", "1", "-map", "2", "-map", "3", "-map", "4",
		"-metadata:s:a:0", "language=rus", "-metadata:s:a:1", "language=eng", "-metadata:s:a:2", "language=fre", "-metadata:s:s:0", "language=rus",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-ac", "2", "-b:a:0", "48k", "-c:s", "srt")
	b := mk("b.mp4", "-f", "lavfi", "-i", "testsrc=size=640x480:rate=25:duration=8", "-c:v", "libx264", "-preset", "ultrafast")
	bru := mk("b.LostFilm.mka", "-f", "lavfi", "-i", "sine=f=330:duration=8:sample_rate=48000", "-c:a", "aac", "-ac", "2")
	ben := write("b.en.srt", "1\n00:00:01,000 --> 00:00:02,500\nHello\n")
	probeA, err := media.Probe(context.Background(), "ffprobe", a)
	if err != nil {
		t.Fatal(err)
	}
	if probeA.Audio[0].Profile != "LC" || probeA.Audio[0].SampleRate != 48000 || probeA.Audio[1].SampleRate != 44100 {
		t.Fatalf("probe %+v", probeA.Audio)
	}
	probeB := media.ProbeResult{Video: &media.VideoStream{Codec: "h264", Width: 640, Height: 480}}
	trA := Plan(probeA, nil, nil, PlanOptions{})
	trB := Plan(probeB, []media.ExternalAudio{{Path: bru, Studio: "LostFilm", Stream: media.AudioStream{Lang: "ru", Title: "LostFilm"}}},
		[]subtitles.External{{Path: ben, Lang: "en", Codec: "subrip"}}, PlanOptions{})
	if !trA.Audio[0].Source.Copy || trA.Audio[2].Source.Copy || !trA.Audio[1].Source.Copy {
		t.Fatalf("copy: %+v", trA.Audio)
	}
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
	for _, want := range []string{`NAME="Русский",LANGUAGE="ru",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2",URI="audio-0.m3u8"`,
		`NAME="LostFilm",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=NO,CHANNELS="2",URI="audio-1.m3u8"`,
		`NAME="Оригинал (Français)",LANGUAGE="fr",DEFAULT=NO,AUTOSELECT=YES,CHANNELS="2",URI="audio-2.m3u8"`,
		`NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,CHANNELS="2",URI="audio-3.m3u8"`,
		`NAME="Русские",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="subs-0.m3u8"`,
		`NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="subs-1.m3u8"`, "\nvideo.m3u8\n"} {
		if !strings.Contains(string(master), want) {
			t.Fatalf("master lacks %s:\n%s", want, master)
		}
	}
	if strings.Count(string(master), "TYPE=AUDIO") != 4 || strings.Count(string(master), "TYPE=SUBTITLES") != 2 {
		t.Fatalf("master %s", master)
	}
	// a profile in English gets the same renditions named in English
	en, err := live.Playlist(WithNameLanguage(ctx, "en"), 5, "index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`NAME="Russian",LANGUAGE="ru",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2",URI="audio-0.m3u8"`,
		`NAME="LostFilm",LANGUAGE="ru",DEFAULT=NO`, `NAME="Original (Français)",LANGUAGE="fr"`, `NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,CHANNELS="2",URI="audio-3.m3u8"`,
		`NAME="Russian",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="subs-0.m3u8"`} {
		if !strings.Contains(string(en), want) {
			t.Fatalf("en master lacks %s:\n%s", want, en)
		}
	}
	// and its DEFAULTs where its «Мои языки» pick: English voice, English subtitles
	mine, err := live.Playlist(WithTrackPrefs(WithNameLanguage(ctx, "en"), TrackPrefs{AudioLangs: []string{"en"}, SubMode: "always"}), 5, "index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`NAME="Russian",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=YES,CHANNELS="2",URI="audio-0.m3u8"`,
		`NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2",URI="audio-3.m3u8"`,
		`NAME="Russian",LANGUAGE="ru",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="subs-0.m3u8"`,
		`NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES,FORCED=NO,URI="subs-1.m3u8"`} {
		if !strings.Contains(string(mine), want) {
			t.Fatalf("profile master lacks %s:\n%s", want, mine)
		}
	}
	if _, err := live.Playlist(ctx, 5, "audio-4.m3u8"); err != ErrNoSegment {
		t.Fatalf("a rendition past the set: %v", err)
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
	audioLists := []string{"audio-0.m3u8", "audio-1.m3u8", "audio-2.m3u8", "audio-3.m3u8"}
	for _, n := range append(append([]string(nil), audioLists...), "subs-0.m3u8", "subs-1.m3u8") {
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
		p, _, err := live.Segment(context.Background(), 5, "seg"+itoa(s.seq)+".ts")
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
	// audio: each listed segment is AAC stereo 48 kHz
	c, _ := live.channel(runKey{id: 5}, false)
	c.mu.Lock()
	published := append([]liveSeg(nil), c.segs...)
	c.mu.Unlock()
	for _, n := range audioLists {
		for i, line := range strings.Split(lists[n], "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			p, _, err := live.Segment(context.Background(), 5, line)
			if err != nil {
				t.Fatalf("%s line %d %s: %v", n, i, line, err)
			}
			m := ffprobeJSON(t, "-show_streams", "-show_format", p)
			au := m["streams"].([]any)[0].(map[string]any)
			if au["codec_name"] != "aac" || au["channels"].(float64) != 2 || au["sample_rate"] != "48000" {
				t.Fatalf("%s %s: %v", n, line, au)
			}
		}
	}
	// A: Русский (copied, 48 kbit/s) and LostFilm (its fallback) are one
	// output, French (copied) and English (encoded) their own; B: one
	// output (LostFilm) for all four
	for _, s := range published {
		out := s.prog.audio
		switch s.prog.outs {
		case 3:
			if out[0] != out[1] || out[2] == out[0] || out[3] == out[0] || out[3] == out[2] {
				t.Fatalf("A's renditions %v", out)
			}
			if s.dur < 5 {
				continue
			}
			ru, _ := os.Stat(filepath.Join(c.dir, media.LiveAudioSegment(out[0], s.seq)))
			en, _ := os.Stat(filepath.Join(c.dir, media.LiveAudioSegment(out[3], s.seq)))
			if ru == nil || en == nil || ru.Size()*2 > en.Size() {
				t.Fatalf("the copied 48 kbit/s track is not smaller than the 192 kbit/s encode: %v %v", ru, en)
			}
		case 1:
			if out[0] != 0 || out[1] != 0 || out[2] != 0 || out[3] != 0 {
				t.Fatalf("B's renditions %v", out)
			}
		default:
			t.Fatalf("outputs %d", s.prog.outs)
		}
	}
	// subtitles: «Два» (10–16 s of A, played from 3 s: 7–13 s) is in A's
	// second and third segment, at the right PTS; B's English cue in its
	// first; B has no Russian ones and A no English ones: empty WebVTT
	var sawRu, sawEn int
	for _, s := range published {
		ru, err := live.VTT(context.Background(), 5, "s-0-"+itoa(s.seq)+".vtt")
		if err != nil {
			t.Fatal(err)
		}
		en, _ := live.VTT(context.Background(), 5, "s-1-"+itoa(s.seq)+".vtt")
		head := "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:" + strconv.FormatInt(s.pts, 10) + ",LOCAL:" + vttTime(s.at) + "\n"
		if !strings.HasPrefix(string(ru), head) || !strings.HasPrefix(string(en), head) {
			t.Fatalf("map %q %q at %+v", ru, en, s)
		}
		if s.prog.outs == 1 && string(ru) != head || s.prog.outs == 3 && string(en) != head {
			t.Fatalf("not empty where the programme has none: %q %q", ru, en)
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
	if _, err := live.VTT(context.Background(), 5, "s-2-"+itoa(published[0].seq)+".vtt"); err != ErrNoSegment {
		t.Fatalf("a subtitle rendition past the set: %v", err)
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
		for j := range 2 {
			n := "s-" + strconv.Itoa(j) + "-" + itoa(s.seq) + ".vtt"
			b, _ := live.VTT(context.Background(), 5, n)
			os.WriteFile(filepath.Join(runDirs[0], n), b, 0o644)
		}
	}
	os.WriteFile(filepath.Join(runDirs[0], "master.m3u8"), master, 0o644)
	// the variant names the subtitle group (what a player follows to the
	// renditions)
	if !strings.Contains(string(master), `AUDIO="aud",SUBTITLES="sub",`) {
		t.Fatalf("the variant does not name the subtitle group:\n%s", master)
	}
	// each subtitle rendition, opened as a media playlist: every WebVTT
	// segment through both boundaries parses, and each cue the segments
	// carry comes out as a packet (none lost at a discontinuity)
	for j := range 2 {
		n := "subs-" + strconv.Itoa(j) + ".m3u8"
		var cues int
		for _, line := range strings.Split(lists[n], "\n") {
			if line != "" && !strings.HasPrefix(line, "#") {
				b, _ := os.ReadFile(filepath.Join(runDirs[0], line))
				cues += strings.Count(string(b), " --> ")
			}
		}
		if out, err := exec.Command("ffmpeg", "-v", "error", "-xerror", "-allowed_extensions", "ALL", "-i", filepath.Join(runDirs[0], n),
			"-map", "0:s:0", "-c:s", "copy", "-f", "null", "-").CombinedOutput(); err != nil {
			t.Fatalf("decode %s across the boundaries: %v %s", n, err, out)
		}
		pk := ffprobeJSON(t, "-allowed_extensions", "ALL", "-show_packets", filepath.Join(runDirs[0], n))
		if got, _ := pk["packets"].([]any); cues == 0 || len(got) != cues {
			t.Fatalf("%s: %d cue packets, the segments carry %d", n, len(got), cues)
		}
	}
	// through the master too, where the client's HLS demuxer reads
	// subtitle renditions: ffmpeg before 8 refuses every one ("Can't
	// support the subtitle"), a limit of that client, not of the playlist
	maps := []string{"0:v:0", "0:a:0", "0:a:1", "0:a:2", "0:a:3"}
	probe, _ := exec.Command("ffprobe", "-v", "warning", "-allowed_extensions", "ALL", "-show_entries", "stream=codec_type", "-of", "csv",
		filepath.Join(runDirs[0], "master.m3u8")).CombinedOutput()
	if strings.Contains(string(probe), "Can't support the subtitle") {
		t.Log("this ffmpeg's HLS demuxer reads no subtitle renditions; they were checked as media playlists")
	} else {
		maps = append(maps, "0:s:0", "0:s:1")
	}
	for _, m := range maps {
		outArgs := []string{"-f", "null", "-"}
		if strings.HasPrefix(m, "0:s:") {
			outArgs = []string{"-c:s", "copy", "-f", "null", "-"} // every WebVTT segment parses (a webvtt muxer refuses the timeline restarting at a discontinuity)
		}
		if out, err := exec.Command("ffmpeg", append([]string{"-v", "error", "-xerror", "-allowed_extensions", "ALL", "-i", filepath.Join(runDirs[0], "master.m3u8"),
			"-map", m}, outArgs...)...).CombinedOutput(); err != nil {
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

// «Качество» of a channel: a lower variant is a run of its own (its own
// picture in its master and its segments), the channel's own stream goes on
// beside it, and a variant at or above the channel's own is that stream.
func TestLiveVariantIsARunOfItsOwn(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil || testing.Short() {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=30", "-c:v", "libx264", "-preset", "ultrafast", p).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	base := time.Now()
	src := cycle{base: base, progs: []Program{{Entry: Entry{Start: base, End: base.Add(30 * time.Second)}, Path: p,
		Probe: media.ProbeResult{Video: &media.VideoStream{Codec: "h264", Width: 320, Height: 240}}}}}
	live := &Live{HW: "none", Dir: filepath.Join(dir, "live"), Source: src, ReadRate: -1, Width: 640, Height: 360, Bitrate: 1_000_000, ReadySegments: 1}
	defer live.Close()
	own := context.Background()
	low := WithVariant(own, Variant{Height: 180, Bitrate: 400_000})
	same := WithVariant(own, Variant{Height: 1080, Bitrate: 8_000_000})
	for ctx, want := range map[context.Context]string{own: "RESOLUTION=640x360", low: "RESOLUTION=320x180", same: "RESOLUTION=640x360"} {
		m, err := live.Playlist(ctx, 1, "index.m3u8")
		if err != nil || !strings.Contains(string(m), want) {
			t.Fatalf("%s: %v\n%s", want, err, m)
		}
	}
	if r := live.Running(); len(r) != 1 || r[0] != 1 {
		t.Fatalf("running %v", r)
	}
	live.mu.Lock()
	runs := len(live.chans)
	live.mu.Unlock()
	if runs != 2 {
		t.Fatalf("%d runs", runs)
	}
	height := func(ctx context.Context) float64 {
		pl, err := live.Playlist(ctx, 1, "video.m3u8")
		if err != nil {
			t.Fatal(err)
		}
		segs := runPlaylist(pl)
		path, _, err := live.Segment(ctx, 1, "seg"+itoa(segs[0].seq)+".ts")
		if err != nil {
			t.Fatal(err)
		}
		st := ffprobeJSON(t, "-show_streams", path)["streams"].([]any)[0].(map[string]any)
		return st["height"].(float64)
	}
	if h := height(low); h != 180 {
		t.Fatalf("variant's segment %v lines", h)
	}
	if h := height(own); h != 360 {
		t.Fatalf("own segment %v lines", h)
	}
	// an edited channel stops every run of it
	live.Stop(1)
	if r := live.Running(); len(r) != 0 {
		t.Fatalf("after Stop %v", r)
	}
}

// A voice-over file beside the video that is copied (AAC-LC stereo 48 kHz,
// unshifted) ends with the programme: before, nothing cut it (the video's
// -t is its own input's), so at real-time reading the programme's ffmpeg
// went on for the rest of the voice-over and the channel stood still on the
// next programme — a whole episode after an intro cut (owner, 2026-11-20:
// «зависает примерно через 5 минут»).
func TestLiveCopiedVoiceOverEndsWithTheProgramme(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil || testing.Short() {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	mk := func(name string, args ...string) string {
		p := filepath.Join(dir, name)
		if out, err := exec.Command("ffmpeg", append(append([]string{"-v", "error", "-y"}, args...), p)...).CombinedOutput(); err != nil {
			t.Fatalf("make %s: %v %s", name, err, out)
		}
		return p
	}
	v := mk("a.mkv", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=60", "-c:v", "libx264", "-preset", "ultrafast", "-g", "10")
	vo := mk("a.LostFilm.mka", "-f", "lavfi", "-i", "sine=f=330:duration=60:sample_rate=48000", "-c:a", "aac", "-ac", "2")
	probe := media.ProbeResult{Video: &media.VideoStream{Codec: "h264", Width: 160, Height: 120}}
	tr := Plan(probe, []media.ExternalAudio{{Path: vo, Studio: "LostFilm", Stream: media.AudioStream{Codec: "aac", Profile: "LC", Channels: 2, SampleRate: 48000, Lang: "ru"}}}, nil, PlanOptions{})
	if !tr.Audio[0].Source.Copy || tr.Audio[0].Source.External == "" {
		t.Fatalf("not a copied sidecar: %+v", tr.Audio)
	}
	// the schedule's intro cut: 14 s of the episode, then the rest of it
	base := time.Now()
	src := cycle{base: base, progs: []Program{
		{Entry: Entry{ItemID: 1, Start: base, End: base.Add(14 * time.Second)}, Path: v, Probe: probe, Tracks: &tr},
		{Entry: Entry{ItemID: 1, Start: base, End: base.Add(40 * time.Second), OffsetMs: 20_000}, Path: v, Probe: probe, Tracks: &tr},
	}}
	// read in real time, as on the server (the lead covers the first piece)
	live := &Live{FFmpeg: "ffmpeg", HW: "none", Dir: filepath.Join(dir, "live"), Source: src, ReadRate: 1, Lead: 20 * time.Second,
		Width: 320, Height: 180, ReadySegments: 1}
	defer live.Close()
	ctx := context.Background()
	deadline := time.Now().Add(15 * time.Second)
	for {
		pl, err := live.Playlist(ctx, 1, "video.m3u8")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(pl), "#EXT-X-DISCONTINUITY\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the programme after the cut never started:\n%s", pl)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestLiveSubtitlesDoNotHoldTheProgrammeOpen: a programme's subtitle
// stream with a cue just after the cut and the next one far later kept the
// programme's ffmpeg reading (in real time) up to that later cue — ~30 s
// past a cut piece of a few seconds — so the next programme started that
// late. A new channel starts on its first episode's pieces between the
// cut recap and intro: its first playlist waited out ReadyWait and the
// iPhone gave up («Не удаётся показать канал»; owner, 2026-11-20, channel
// «Billions»). The piece's end now starts the next programme at once, and
// the next programme still has its subtitles.
func TestLiveSubtitlesDoNotHoldTheProgrammeOpen(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil || testing.Short() {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	srt := filepath.Join(dir, "a.ru.srt")
	// a cue at 10 s, then nothing until 60 s (Billions S03E01's Russian track
	// after its recap: 126.7 s, then 176.8 s)
	if err := os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:02,000\nРаз\n\n2\n00:00:10,000 --> 00:00:13,000\nДва\n\n3\n00:01:00,000 --> 00:01:01,000\nТри\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := filepath.Join(dir, "a.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25:duration=80",
		"-f", "lavfi", "-i", "sine=f=440:duration=80:sample_rate=48000", "-i", srt, "-map", "0", "-map", "1", "-map", "2",
		"-metadata:s:s:0", "language=rus", "-c:v", "libx264", "-preset", "ultrafast", "-g", "50", "-c:a", "aac", "-c:s", "srt", v).CombinedOutput(); err != nil {
		t.Fatalf("make: %v %s", err, out)
	}
	probe, err := media.Probe(context.Background(), "ffprobe", v)
	if err != nil {
		t.Fatal(err)
	}
	tr := Plan(probe, nil, nil, PlanOptions{})
	if len(tr.Subs) != 1 {
		t.Fatalf("subs %+v", tr.Subs)
	}
	// the run starts a second before a cut piece ends (5–7 s of the file),
	// then another programme plays the file from its start
	now := time.Now()
	src := fixedSchedule{
		{Entry: Entry{ItemID: 1, Start: now.Add(-time.Second), End: now.Add(time.Second), OffsetMs: 5_000}, Path: v, Probe: probe, Tracks: &tr},
		{Entry: Entry{ItemID: 1, Start: now.Add(time.Second), End: now.Add(60 * time.Second)}, Path: v, Probe: probe, Tracks: &tr},
	}
	// read in real time, as on the server; the first playlist waits for two segments
	live := &Live{FFmpeg: "ffmpeg", HW: "none", Dir: filepath.Join(dir, "live"), Source: src, ReadRate: 1, Lead: 20 * time.Second,
		Width: 320, Height: 180, ReadySegments: 2, ReadyWait: 20 * time.Second}
	defer live.Close()
	ctx := context.Background()
	asked := time.Now()
	if _, err := live.Playlist(ctx, 1, "index.m3u8"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(asked); took > 8*time.Second {
		t.Fatalf("the first playlist took %s: the cut piece's ffmpeg held the next programme back", took.Round(time.Second))
	}
	pl, err := live.Playlist(ctx, 1, "video.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(pl), "#EXT-X-DISCONTINUITY\n")
	if !ok {
		t.Fatalf("no second programme yet:\n%s", pl)
	}
	_, seg, _ := strings.Cut(after, "\nseg")
	seq, _, _ := strings.Cut(seg, ".ts")
	// the next programme's subtitles: its cue at 1–2 s (a cue shows once the
	// one after it is written)
	deadline := time.Now().Add(10 * time.Second)
	for {
		vtt, err := live.VTT(ctx, 1, "s-0-"+seq+".vtt")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(vtt), "Раз") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("segment %s lacks its cue:\n%s", seq, vtt)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// fixedSchedule is a Source of programmes played once, in order.
type fixedSchedule []Program

func (f fixedSchedule) TrackSet(ctx context.Context, ch int64, from time.Time) TrackSet {
	return programSet(ctx, f, ch, from)
}

func (f fixedSchedule) Program(_ context.Context, ch int64, t time.Time) (Program, error) {
	for _, p := range f {
		if t.Before(p.End) {
			p.Channel = ch
			return p, nil
		}
	}
	return Program{}, ErrNothing
}
