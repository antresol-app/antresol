package media

import (
	"encoding/json"
	"strings"
	"testing"
)

var safariCaps = ClientCaps{
	VideoCodecs: []string{"h264", "hevc"},
	AudioCodecs: []string{"aac", "ac3", "mp3"},
	Containers:  []string{"mp4", "mov"},
	MaxHeight:   2160,
}

var chromeCaps = ClientCaps{
	VideoCodecs: []string{"h264", "vp9", "av1"},
	AudioCodecs: []string{"aac", "mp3", "opus", "flac"},
	Containers:  []string{"mp4", "webm"},
	MaxHeight:   2160,
}

func TestDecideDirectH264AACMP4(t *testing.T) {
	p := ProbeResult{
		Container: "mp4",
		Video:     &VideoStream{Codec: "h264", Width: 1920, Height: 1080},
		Audio:     []AudioStream{{Index: 1, Codec: "aac", Channels: 2, Default: true}},
	}
	d := Decide(p, safariCaps, 1)
	if d.Mode != "direct" || !d.VideoCopy || !d.AudioCopy {
		t.Fatalf("want direct, got %+v", d)
	}
}

func TestDecideHEVCMKVChrome(t *testing.T) {
	p := ProbeResult{
		Container: "mkv",
		Video:     &VideoStream{Codec: "hevc", Width: 1920, Height: 1080},
		Audio:     []AudioStream{{Index: 1, Codec: "aac", Channels: 2, Default: true}},
	}
	d := Decide(p, chromeCaps, 1)
	if d.Mode != "transcode" || d.VideoCopy {
		t.Fatalf("want transcode without video copy, got %+v", d)
	}
	if d.Reason == "" {
		t.Fatal("reason must be set")
	}
}

func TestDecideAC3AudioOnly(t *testing.T) {
	p := ProbeResult{
		Container: "mkv",
		Video:     &VideoStream{Codec: "h264", Width: 1920, Height: 1080},
		Audio:     []AudioStream{{Index: 1, Codec: "ac3", Channels: 6, Default: true}},
	}
	d := Decide(p, chromeCaps, 1)
	if !d.VideoCopy || d.AudioCopy || d.Mode == "direct" {
		t.Fatalf("want video copy + audio transcode, got %+v", d)
	}
}

func TestDecideMKVContainerRemux(t *testing.T) {
	p := ProbeResult{
		Container: "mkv",
		Video:     &VideoStream{Codec: "h264", Width: 1280, Height: 720},
		Audio:     []AudioStream{{Index: 1, Codec: "aac", Channels: 2, Default: true}},
	}
	d := Decide(p, chromeCaps, 1)
	if d.Mode != "remux" || !d.VideoCopy || !d.AudioCopy {
		t.Fatalf("want remux, got %+v", d)
	}
}

func TestDecideSecondAudioTrackNeedsHLS(t *testing.T) {
	p := ProbeResult{
		Container: "mp4",
		Video:     &VideoStream{Codec: "h264", Width: 1280, Height: 720},
		Audio: []AudioStream{
			{Index: 1, Codec: "aac", Default: true},
			{Index: 2, Codec: "aac"},
		},
	}
	if d := Decide(p, chromeCaps, 1); d.Mode != "direct" {
		t.Fatalf("default track: want direct, got %+v", d)
	}
	if d := Decide(p, chromeCaps, 2); d.Mode == "direct" || !d.AudioCopy {
		t.Fatalf("second track: want remux with audio copy, got %+v", d)
	}
}

func TestDecideMaxHeight(t *testing.T) {
	p := ProbeResult{
		Container: "mp4",
		Video:     &VideoStream{Codec: "h264", Width: 3840, Height: 2160},
		Audio:     []AudioStream{{Index: 1, Codec: "aac", Default: true}},
	}
	c := safariCaps
	c.MaxHeight = 1080
	d := Decide(p, c, 1)
	if d.Mode != "transcode" || d.TargetHeight != 1080 || d.VideoCopy {
		t.Fatalf("want transcode to 1080, got %+v", d)
	}
}

func TestDecideMaxBitrate(t *testing.T) {
	p := ProbeResult{
		Container: "mp4",
		Video:     &VideoStream{Codec: "h264", Width: 1920, Height: 1080, BitRate: 40_000_000},
		Audio:     []AudioStream{{Index: 1, Codec: "aac", Default: true}},
	}
	c := safariCaps
	c.MaxBitrate = 8_000_000
	if d := Decide(p, c, 1); d.Mode != "transcode" {
		t.Fatalf("want transcode, got %+v", d)
	}
}

func TestBuildArgsQSV(t *testing.T) {
	d := Decision{Mode: "transcode", AudioCopy: false, TargetHeight: 1080, VideoCodec: "hevc", AudioIndex: 1}
	args := strings.Join(BuildArgs("qsv", "/m/in.mkv", d, 0, "/w/s1"), " ")
	for _, want := range []string{"-hwaccel qsv", "h264_qsv", "-hls_time 6", "-hls_playlist_type event", "mpegts"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
}

func TestBuildArgsNone(t *testing.T) {
	d := Decision{Mode: "transcode", TargetHeight: 720, VideoCodec: "hevc", AudioIndex: 1}
	args := strings.Join(BuildArgs("none", "/m/in.mkv", d, 12.5, "/w/s1"), " ")
	for _, want := range []string{"libx264", "-preset veryfast", "-ss 12.5", "scale=-2:720", "-c:a aac"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	if strings.Contains(args, "-hwaccel") {
		t.Errorf("unexpected hwaccel: %s", args)
	}
}

func TestBuildArgsHEVCCopyFMP4(t *testing.T) {
	d := Decision{Mode: "remux", VideoCopy: true, AudioCopy: false, VideoCodec: "hevc", AudioIndex: 2, FMP4: true}
	args := strings.Join(BuildArgs("none", "http://127.0.0.1:1/stream", d, 0, "/w/s1"), " ")
	for _, want := range []string{"-c:v copy", "-hls_segment_type fmp4", "hvc1", "-i http://127.0.0.1:1/stream", "-map 0:2", "-reconnect 1", "-reconnect_max_retries -1"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
}

func TestBuildArgsH264CopyTS(t *testing.T) {
	d := Decision{Mode: "remux", VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioIndex: 1}
	args := strings.Join(BuildArgs("none", "/m/in.mkv", d, 0, "/w/s1"), " ")
	if !strings.Contains(args, "-hls_segment_type mpegts") || strings.Contains(args, "libx264") {
		t.Errorf("bad args: %s", args)
	}
}

func TestParseProbe(t *testing.T) {
	js := `{"format":{"format_name":"matroska,webm","duration":"123.456","bit_rate":"5000000"},
"streams":[
{"index":0,"codec_type":"video","codec_name":"hevc","profile":"Main 10","width":3840,"height":2160,"color_transfer":"smpte2084","bit_rate":"4500000"},
{"index":1,"codec_type":"audio","codec_name":"eac3","channels":6,"disposition":{"default":1},"tags":{"language":"eng","title":"Surround"}},
{"index":2,"codec_type":"subtitle","codec_name":"subrip","disposition":{"default":0,"forced":1},"tags":{"language":"rus"}}]}`
	r, err := parseProbe([]byte(js), "/x/a.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if r.Container != "mkv" || r.DurationMs != 123456 || r.Video == nil || !r.Video.HDR || r.Video.BitRate != 4500000 {
		t.Fatalf("%+v", r)
	}
	if len(r.Audio) != 1 || r.Audio[0].Lang != "eng" || !r.Audio[0].Default || r.Audio[0].Channels != 6 {
		t.Fatalf("%+v", r.Audio)
	}
	if len(r.Subs) != 1 || !r.Subs[0].Forced || r.Subs[0].Lang != "rus" {
		t.Fatalf("%+v", r.Subs)
	}
}

func TestDetectHWExplicit(t *testing.T) {
	for _, v := range []string{"qsv", "videotoolbox", "none"} {
		if got := DetectHW(v); got != v {
			t.Errorf("DetectHW(%q)=%q", v, got)
		}
	}
}

func mkv(v VideoStream, a AudioStream) ProbeResult {
	a.Index, a.Default = 1, true
	return ProbeResult{Container: "mkv", Video: &v, Audio: []AudioStream{a}}
}

func TestDecideSegmentContainerLimits(t *testing.T) {
	h264 := VideoStream{Codec: "h264", Width: 1920, Height: 1080, BitDepth: 8}
	cases := []struct {
		name               string
		p                  ProbeResult
		caps               ClientCaps
		videoCopy, audioCp bool
		fmp4               bool
	}{
		{"vp9", mkv(VideoStream{Codec: "vp9", Height: 1080, BitDepth: 8}, AudioStream{Codec: "opus"}), chromeCaps, true, true, true},
		{"av1", mkv(VideoStream{Codec: "av1", Height: 1080, BitDepth: 8}, AudioStream{Codec: "aac"}), chromeCaps, true, true, true},
		{"h264+flac", mkv(h264, AudioStream{Codec: "flac"}), chromeCaps, true, true, true},
		{"h264+dts", mkv(h264, AudioStream{Codec: "dts"}), chromeCaps, true, false, false},
		{"h264+truehd", mkv(h264, AudioStream{Codec: "truehd"}), chromeCaps, true, false, false},
		{"h264+aac", mkv(h264, AudioStream{Codec: "aac"}), chromeCaps, true, true, false},
	}
	for _, c := range cases {
		d := Decide(c.p, c.caps, 1)
		if d.Mode != "remux" || d.VideoCopy != c.videoCopy || d.AudioCopy != c.audioCp || d.FMP4 != c.fmp4 {
			t.Errorf("%s: %+v", c.name, d)
		}
	}
	// A client that claims DTS still must not get it copied into HLS.
	caps := chromeCaps
	caps.AudioCodecs = append([]string{"dts", "truehd"}, caps.AudioCodecs...)
	for _, a := range []string{"dts", "truehd"} {
		if d := Decide(mkv(h264, AudioStream{Codec: a}), caps, 1); d.AudioCopy {
			t.Errorf("%s copied into HLS: %+v", a, d)
		}
	}
	// flac with a re-encoded video: audio may only be copied via fMP4.
	hevc := VideoStream{Codec: "hevc", Height: 1080, BitDepth: 8}
	if d := Decide(mkv(hevc, AudioStream{Codec: "flac"}), chromeCaps, 1); d.Mode != "transcode" || !d.FMP4 || !d.AudioCopy {
		t.Errorf("hevc+flac: %+v", d)
	}
}

func TestDecideHi10P(t *testing.T) {
	p := ProbeResult{Container: "mp4", Audio: []AudioStream{{Index: 1, Codec: "aac", Default: true}},
		Video: &VideoStream{Codec: "h264", Profile: "High 10", Height: 1080, BitDepth: 10}}
	if d := Decide(p, chromeCaps, 1); d.Mode != "transcode" || d.VideoCopy {
		t.Fatalf("Hi10P: %+v", d)
	}
	p.Video = &VideoStream{Codec: "h264", Profile: "High", Height: 1080, BitDepth: 10}
	if d := Decide(p, chromeCaps, 1); d.VideoCopy {
		t.Fatalf("10-bit H.264: %+v", d)
	}
}

func TestDecideAudioIndexResolvedAndNoAudio(t *testing.T) {
	p := ProbeResult{Container: "mkv", Video: &VideoStream{Codec: "h264", Height: 720, BitDepth: 8},
		Audio: []AudioStream{{Index: 3, Codec: "aac"}, {Index: 4, Codec: "aac", Default: true}}}
	if d := Decide(p, chromeCaps, -1); d.AudioIndex != 4 {
		t.Fatalf("default: %+v", d)
	}
	if d := Decide(p, chromeCaps, 3); d.AudioIndex != 3 {
		t.Fatalf("explicit: %+v", d)
	}
	p.Audio = nil
	d := Decide(p, chromeCaps, -1)
	if d.AudioIndex != -1 {
		t.Fatalf("no audio: %+v", d)
	}
	args := strings.Join(BuildArgs("none", "in.mkv", d, 0, "/w"), " ")
	if !strings.Contains(args, "-an") || strings.Contains(args, "-c:a") || strings.Contains(args, "0:a:0") {
		t.Fatalf("no-audio args: %s", args)
	}
}

func TestBuildArgsMaxBitrateEvenHeight(t *testing.T) {
	d := Decision{TargetHeight: 721, MaxBitrate: 4_000_000, AudioIndex: -1, VideoCodec: "h264"}
	args := strings.Join(BuildArgs("none", "in", d, 0, "/w"), " ")
	if !strings.Contains(args, "-maxrate 4000000 -bufsize 8000000") || !strings.Contains(args, "scale=-2:720,") {
		t.Fatal(args)
	}
	q := strings.Join(BuildArgs("qsv", "in", d, 0, "/w"), " ")
	if !strings.Contains(q, "vpp_qsv=w=trunc(oh*a/2)*2:h=720") {
		t.Fatal(q)
	}
}

func TestDetectHWUnknownFallsBackToAuto(t *testing.T) {
	got := DetectHW("bogus")
	if got != DetectHW("auto") || (got != "qsv" && got != "videotoolbox" && got != "none") {
		t.Fatalf("got %q", got)
	}
}

func TestParseProbeBitDepth(t *testing.T) {
	js := `{"format":{"format_name":"mov,mp4,m4a"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","profile":"High 10","pix_fmt":"yuv420p10le","width":1920,"height":1080}]}`
	r, err := parseProbe([]byte(js), "a.mp4")
	if err != nil || r.Video.BitDepth != 10 || r.Video.PixFmt != "yuv420p10le" {
		t.Fatalf("%+v %v", r.Video, err)
	}
	for pf, want := range map[string]int{"yuv420p": 8, "nv12": 8, "yuvj420p": 8, "p010le": 10, "yuv444p12le": 12} {
		if got := bitDepth("", pf); got != want {
			t.Errorf("%s: %d", pf, got)
		}
	}
}

func TestBuildArgsQSVLowPowerBitrate(t *testing.T) {
	cases := []struct {
		d                  Decision
		br, maxrate, bufsz string
	}{
		{Decision{SourceHeight: 1080, AudioIndex: -1}, "8000000", "12000000", "24000000"},
		{Decision{SourceHeight: 2160, TargetHeight: 720, AudioIndex: -1}, "4000000", "6000000", "12000000"},
		{Decision{SourceHeight: 1080, TargetHeight: 480, AudioIndex: -1}, "2000000", "3000000", "6000000"},
		{Decision{AudioIndex: -1}, "8000000", "12000000", "24000000"},                                           // unknown height
		{Decision{SourceHeight: 1080, MaxBitrate: 5_000_000, AudioIndex: -1}, "5000000", "5000000", "10000000"}, // client cap
		{Decision{SourceHeight: 2160, AudioIndex: -1}, "20000000", "30000000", "60000000"},
	}
	for _, c := range cases {
		c.d.Mode, c.d.VideoCodec = "transcode", "hevc"
		args := strings.Join(BuildArgs("qsv", "in.mkv", c.d, 0, "/w"), " ")
		for _, want := range []string{
			"-c:v h264_qsv -low_power 1 -preset veryfast",
			"-b:v " + c.br + " -maxrate " + c.maxrate + " -bufsize " + c.bufsz,
			"-forced_idr 1 -force_key_frames expr:gte(t,n_forced*6)",
		} {
			if !strings.Contains(args, want) {
				t.Errorf("%+v: missing %q in %s", c.d, want, args)
			}
		}
		if strings.Contains(args, "global_quality") {
			t.Errorf("ICQ is unsupported on Alder Lake iHD: %s", args)
		}
	}
}

func TestBuildArgsQSVDecodePath(t *testing.T) {
	hw := []Decision{
		{VideoCodec: "hevc", SourcePixFmt: "yuv420p10le"},
		{VideoCodec: "h264", SourcePixFmt: "yuv420p"},
		{VideoCodec: "vp9"},
		{VideoCodec: "av1", SourcePixFmt: "yuv420p10le"},
	}
	for _, d := range hw {
		d.Mode, d.AudioIndex, d.TargetHeight = "transcode", -1, 720
		args := strings.Join(BuildArgs("qsv", "in.mkv", d, 0, "/w"), " ")
		if !strings.Contains(args, "-hwaccel qsv -hwaccel_output_format qsv") ||
			!strings.Contains(args, "-vf vpp_qsv=w=trunc(oh*a/2)*2:h=720:format=nv12") {
			t.Errorf("%s/%s: want hw decode: %s", d.VideoCodec, d.SourcePixFmt, args)
		}
	}
	sw := []Decision{
		{VideoCodec: "mpeg4"},
		{VideoCodec: "msmpeg4v3"},
		{VideoCodec: "h264", SourcePixFmt: "yuv420p10le"}, // Hi10P: no iGPU decode
		{VideoCodec: "hevc", SourcePixFmt: "yuv444p"},
	}
	for _, d := range sw {
		d.Mode, d.AudioIndex, d.TargetHeight = "transcode", -1, 720
		args := strings.Join(BuildArgs("qsv", "in.mkv", d, 0, "/w"), " ")
		if strings.Contains(args, "-hwaccel") || strings.Contains(args, "vpp_qsv") ||
			!strings.Contains(args, "-vf scale=-2:720,format=nv12 -fps_mode passthrough -c:v h264_qsv") {
			t.Errorf("%s/%s: want sw decode + qsv encode: %s", d.VideoCodec, d.SourcePixFmt, args)
		}
	}
}

func TestDecideSetsSourceInfo(t *testing.T) {
	p := ProbeResult{Container: "matroska,webm",
		Video: &VideoStream{Codec: "hevc", Height: 1038, PixFmt: "yuv420p10le", BitDepth: 10}}
	d := Decide(p, ClientCaps{VideoCodecs: []string{"h264"}}, -1)
	if d.SourceHeight != 1038 || d.SourcePixFmt != "yuv420p10le" {
		t.Fatalf("%+v", d)
	}
}

// appleCaps is what the native app reports: AVPlayer plays HEVC in MP4 only
// when the sample entry is hvc1.
var appleCaps = ClientCaps{
	VideoCodecs:   []string{"h264", "hevc"},
	AudioCodecs:   []string{"aac", "ac3", "eac3"},
	Containers:    []string{"mp4", "mov"},
	HevcNeedsHvc1: true,
}

func hevcMP4(tag string) ProbeResult {
	return ProbeResult{Container: "mp4", Video: &VideoStream{Codec: "hevc", CodecTag: tag, Height: 1080, BitDepth: 8},
		Audio: []AudioStream{{Index: 1, Codec: "aac", Default: true}}}
}

func TestDecideHev1NotDirectForApple(t *testing.T) {
	d := Decide(hevcMP4("hev1"), appleCaps, -1)
	if d.Mode != "remux" || !d.VideoCopy || !d.FMP4 || !strings.Contains(d.Reason, "hvc1") {
		t.Fatalf("%+v", d)
	}
}

func TestDecideEmptyTagNotDirectForApple(t *testing.T) {
	// an old probe-cache row has no tag: not known to be hvc1
	if d := Decide(hevcMP4(""), appleCaps, -1); d.Mode != "remux" || !d.VideoCopy {
		t.Fatalf("%+v", d)
	}
}

func TestDecideHvc1DirectForApple(t *testing.T) {
	if d := Decide(hevcMP4("hvc1"), appleCaps, -1); d.Mode != "direct" {
		t.Fatalf("%+v", d)
	}
}

func TestDecideHev1DirectForWeb(t *testing.T) {
	c := appleCaps
	c.HevcNeedsHvc1 = false
	if d := Decide(hevcMP4("hev1"), c, -1); d.Mode != "direct" {
		t.Fatalf("%+v", d)
	}
}

func TestParseProbeCodecTag(t *testing.T) {
	js := `{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"1"},
"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","codec_tag_string":"hev1","width":1920,"height":1080}]}`
	r, err := parseProbe([]byte(js), "/x/a.mp4")
	if err != nil || r.Video == nil || r.Video.CodecTag != "hev1" {
		t.Fatalf("%+v %v", r.Video, err)
	}
}

func TestClientCapsHevcFlagJSON(t *testing.T) {
	var c ClientCaps
	if err := json.Unmarshal([]byte(`{"videoCodecs":["hevc"],"hevcNeedsHvc1":true}`), &c); err != nil || !c.HevcNeedsHvc1 || len(c.VideoCodecs) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
}
