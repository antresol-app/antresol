package media

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPlanDeviceCopyRemuxMKV(t *testing.T) {
	p := ProbeResult{Container: "matroska,webm", DurationMs: 60_000,
		Video: &VideoStream{Codec: "h264", Height: 1080, BitDepth: 8, Profile: "High", PixFmt: "yuv420p"},
		Audio: []AudioStream{{Index: 1, Codec: "ac3", Default: true}},
		Subs:  []SubStream{{Index: 2, Codec: "subrip", Lang: "rus"}}}
	pl := PlanDeviceCopy(p, nil, 0, 0, -1)
	if !pl.VideoCopy || !pl.AudioCopy || pl.AudioIndex != 1 || !slices.Equal(pl.SubIndexes, []int{2}) ||
		!slices.Equal(pl.SubLangs, []string{"rus"}) || pl.DurationMs != 60_000 {
		t.Fatalf("%+v", pl)
	}
}

func TestPlanDeviceCopyMKVHevcDTS(t *testing.T) {
	p := ProbeResult{Container: "matroska,webm",
		Video: &VideoStream{Codec: "hevc", Height: 2160, BitDepth: 10, PixFmt: "yuv420p10le", Profile: "Main 10"},
		Audio: []AudioStream{{Index: 1, Codec: "dts", Default: true}},
		Subs:  []SubStream{{Index: 2, Codec: "hdmv_pgs_subtitle", Lang: "rus"}, {Index: 3, Codec: "subrip", Lang: "eng"}}}
	ext := []ExternalSub{{Path: "/m/a.ru.srt", Lang: "ru", Codec: "subrip"}}
	pl := PlanDeviceCopy(p, ext, 0, 0, -1)
	if !pl.VideoCopy || pl.AudioCopy || pl.AudioIndex != 1 || !slices.Equal(pl.SubIndexes, []int{3}) || len(pl.ExternalSubs) != 1 {
		t.Fatalf("%+v", pl)
	}
	args := DeviceCopyArgs("none", "/m/a.mkv", pl, "/d/x.part.mp4")
	j := strings.Join(args, " ")
	for _, want := range []string{"-c:v copy -tag:v hvc1", "-c:a aac -b:a 192k -ac 2", "-map 0:3", "-map 1:s:0"} {
		if !strings.Contains(j, want) {
			t.Errorf("missing %q in %s", want, j)
		}
	}
	if strings.Contains(j, "-map 0:2") {
		t.Errorf("PGS mapped: %s", j)
	}
}

func TestPlanDeviceCopyHi10P(t *testing.T) {
	p := ProbeResult{Video: &VideoStream{Codec: "h264", Height: 1080, BitDepth: 10, Profile: "High 10", PixFmt: "yuv420p10le"},
		Audio: []AudioStream{{Index: 1, Codec: "aac", Default: true}}}
	pl := PlanDeviceCopy(p, nil, 0, 0, -1)
	if pl.VideoCopy || !pl.AudioCopy || pl.TargetHeight != 1080 {
		t.Fatalf("%+v", pl)
	}
	for _, prof := range []string{"High 4:2:2", "High 4:4:4 Predictive"} {
		p.Video.BitDepth, p.Video.Profile = 8, prof
		if PlanDeviceCopy(p, nil, 0, 0, -1).VideoCopy {
			t.Fatalf("%s copied", prof)
		}
	}
	p.Video = &VideoStream{Codec: "mpeg4", Height: 480}
	if PlanDeviceCopy(p, nil, 0, 0, -1).VideoCopy {
		t.Fatal("mpeg4 copied")
	}
}

func TestPlanDeviceCopyQualityCap(t *testing.T) {
	p := ProbeResult{Video: &VideoStream{Codec: "hevc", Height: 2160, BitDepth: 10},
		Audio: []AudioStream{{Index: 1, Codec: "eac3", Default: true}, {Index: 2, Codec: "truehd"}}}
	pl := PlanDeviceCopy(p, nil, 1080, 8_000_000, 2)
	if pl.VideoCopy || pl.TargetHeight != 1080 || pl.MaxBitrate != 8_000_000 || pl.AudioIndex != 2 || pl.AudioCopy || pl.SourceHeight != 2160 {
		t.Fatalf("%+v", pl)
	}
	// within the cap: a copy
	p.Video.Height, p.Video.BitRate = 720, 3_000_000
	if pl := PlanDeviceCopy(p, nil, 1080, 8_000_000, -1); !pl.VideoCopy || !pl.AudioCopy || pl.AudioIndex != 1 {
		t.Fatalf("%+v", pl)
	}
	// over the bitrate cap
	p.Video.BitRate = 20_000_000
	if pl := PlanDeviceCopy(p, nil, 1080, 8_000_000, -1); pl.VideoCopy || pl.TargetHeight != 720 {
		t.Fatalf("%+v", pl)
	}
}

func TestDeviceCopyArgs(t *testing.T) {
	pl := DeviceCopyPlan{VideoCopy: true, VideoCodec: "hevc", AudioIndex: 1, AudioCopy: true,
		SubIndexes: []int{3, 4}, SubLangs: []string{"rus", ""}}
	args := DeviceCopyArgs("qsv", "/m/a.mkv", pl, "/d/abc.part.mp4")
	j := strings.Join(args, " ")
	for _, want := range []string{
		"-protocol_whitelist file -i file:/m/a.mkv", "-map 0:v:0 -map 0:1 -map 0:3 -map 0:4",
		"-c:v copy -tag:v hvc1", "-c:a copy", "-c:s mov_text",
		"-metadata:s:s:0 language=rus", "-metadata:s:s:1 language=und",
		"-movflags +faststart -progress pipe:1 -nostats -f mp4 /d/abc.part.mp4",
	} {
		if !strings.Contains(j, want) {
			t.Errorf("missing %q in %s", want, j)
		}
	}
	if strings.Contains(j, "-hwaccel") {
		t.Errorf("hwaccel for a copy: %s", j)
	}
	if !strings.HasSuffix(args[len(args)-1], ".part.mp4") {
		t.Fatal(args[len(args)-1])
	}
	// h264 copy: no hvc1 tag; transcode uses the HLS encoder settings
	pl.VideoCodec = "h264"
	if j := strings.Join(DeviceCopyArgs("none", "/m/a.mkv", pl, "/d/abc.part.mp4"), " "); strings.Contains(j, "hvc1") {
		t.Fatal(j)
	}
	tr := DeviceCopyPlan{VideoCodec: "h264", SourcePixFmt: "yuv420p", SourceHeight: 1080, TargetHeight: 720, MaxBitrate: 4_000_000, AudioIndex: 1}
	j = strings.Join(DeviceCopyArgs("qsv", "/m/a.mkv", tr, "/d/abc.part.mp4"), " ")
	enc := strings.Join(videoEncodeArgs("qsv", tr.VideoCodec, tr.SourcePixFmt, tr.TargetHeight, tr.SourceHeight, tr.MaxBitrate), " ")
	if !strings.Contains(j, enc) || !strings.Contains(j, "-hwaccel qsv -hwaccel_output_format qsv -protocol_whitelist file -i file:/m/a.mkv") ||
		strings.Contains(j, "-force_key_frames") {
		t.Fatalf("%s\nenc %s", j, enc)
	}
}

func TestDeviceCopyArgsExternalSrt(t *testing.T) {
	pl := DeviceCopyPlan{VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true, SubIndexes: []int{2}, SubLangs: []string{"eng"},
		ExternalSubs: []ExternalSub{{Path: "/m/a.ru.srt", Lang: "ru", Codec: "subrip"}}}
	args := DeviceCopyArgs("none", "/m/a.mkv", pl, "/d/x.part.mp4")
	j := strings.Join(args, " ")
	for _, want := range []string{
		"-i file:/m/a.mkv -protocol_whitelist file -f srt -i file:/m/a.ru.srt",
		"-map 0:2 -map 1:s:0", "-metadata:s:s:0 language=eng", "-metadata:s:s:1 language=rus",
	} {
		if !strings.Contains(j, want) {
			t.Errorf("missing %q in %s", want, j)
		}
	}
	if strings.Contains(j, "sub_charenc") {
		t.Error(j)
	}
	pl.ExternalSubs[0].CP1251 = true
	j = strings.Join(DeviceCopyArgs("none", "/m/a.mkv", pl, "/d/x.part.mp4"), " ")
	if !strings.Contains(j, "-i file:/m/a.mkv -sub_charenc CP1251 -protocol_whitelist file -f srt -i file:/m/a.ru.srt") {
		t.Error(j)
	}
	pl.ExternalSubs = []ExternalSub{{Path: "/m/a.de.srt", Lang: "de", Codec: "subrip"}, {Path: "/m/a.uk.ass", Lang: "uk", Codec: "ass"},
		{Path: "/m/a.en.vtt", Lang: "en", Codec: "webvtt"}}
	j = strings.Join(DeviceCopyArgs("none", "/m/a.mkv", pl, "/d/x.part.mp4"), " ")
	for _, want := range []string{"-metadata:s:s:1 language=und", "-metadata:s:s:2 language=ukr", "-metadata:s:s:3 language=eng",
		"-f ass -i file:/m/a.uk.ass", "-f webvtt -i file:/m/a.en.vtt", "-map 3:s:0"} {
		if !strings.Contains(j, want) {
			t.Errorf("missing %q in %s", want, j)
		}
	}
}

func TestDeviceCopyKeyIncludesExternalSubs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a.ru.srt")
	if err := os.WriteFile(sub, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k := DeviceCopyKey{FileID: 7, Size: 100, ModTime: mod, MaxHeight: 1080, MaxBitrate: 8_000_000, AudioIndex: -1,
		ExtSubsHash: ExternalSubsHash([]string{sub})}
	id1 := k.ID()
	if len(id1) != 16 {
		t.Fatal(id1)
	}
	if err := os.Chtimes(sub, mod, mod.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	k.ExtSubsHash = ExternalSubsHash([]string{sub})
	if k.ID() == id1 {
		t.Fatal("same id after the subtitle changed")
	}
	// order of the list does not matter; no subtitles differ from some
	other := filepath.Join(dir, "a.en.srt")
	os.WriteFile(other, []byte("1\n"), 0o644)
	if ExternalSubsHash([]string{sub, other}) != ExternalSubsHash([]string{other, sub}) || ExternalSubsHash(nil) == ExternalSubsHash([]string{sub}) {
		t.Fatal("hash order")
	}
	k2 := k
	k2.MaxHeight = 720
	if k2.ID() == k.ID() {
		t.Fatal("quality not in the key")
	}
}

func TestCopyStatusJSON(t *testing.T) {
	b, err := json.Marshal(CopyStatus{ID: "abc", State: "ready", Progress: 1, Size: 10, FileID: 3,
		Quality: QualityInfo{Selected: "auto", Applied: "1080", Remote: true}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"id", "state", "progress", "size", "fileId", "quality"} {
		if _, ok := m[k]; !ok {
			t.Errorf("no %s in %s", k, b)
		}
	}
	if _, ok := m["error"]; ok {
		t.Errorf("empty error present: %s", b)
	}
	q, _ := m["quality"].(map[string]any)
	if q["applied"] != "1080" || q["selected"] != "auto" || q["remote"] != true {
		t.Errorf("quality %s", b)
	}
}

// TestBuildArgsUnchanged pins the HLS command lines (written from
// BuildArgsThrottled before videoEncodeArgs was extracted from it).
func TestBuildArgsUnchanged(t *testing.T) {
	th := Throttle{Rate: 2, Burst: 30}
	cases := []struct {
		name  string
		hw    string
		d     Decision
		start float64
		in    string
		want  []string
	}{
		{"copy hevc fmp4", "qsv", Decision{VideoCopy: true, AudioCopy: true, VideoCodec: "hevc", AudioIndex: 1, FMP4: true}, 12.5, "/m/a.mkv",
			[]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-ss", "12.5", "-protocol_whitelist", "file", "-readrate", "2", "-readrate_initial_burst", "30", "-i", "file:/m/a.mkv", "-map", "0:v:0?", "-map", "0:1", "-sn", "-dn", "-c:v", "copy", "-tag:v", "hvc1", "-c:a", "copy", "-f", "hls", "-hls_time", "6", "-hls_playlist_type", "event", "-hls_list_size", "0", "-hls_flags", "independent_segments", "-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4", "-hls_segment_filename", "/out/seg%05d.m4s", "/out/index.m3u8"}},
		{"qsv decode 720", "qsv", Decision{VideoCodec: "h264", SourcePixFmt: "yuv420p", SourceHeight: 1080, TargetHeight: 720, MaxBitrate: 4_000_000, AudioIndex: 2}, 0, "/m/a.mkv",
			[]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-hwaccel", "qsv", "-hwaccel_output_format", "qsv", "-protocol_whitelist", "file", "-readrate", "2", "-readrate_initial_burst", "30", "-i", "file:/m/a.mkv", "-map", "0:v:0?", "-map", "0:2", "-sn", "-dn", "-vf", "vpp_qsv=w=trunc(oh*a/2)*2:h=720:format=nv12", "-fps_mode", "passthrough", "-c:v", "h264_qsv", "-low_power", "1", "-preset", "veryfast", "-b:v", "4000000", "-maxrate", "4000000", "-bufsize", "8000000", "-forced_idr", "1", "-force_key_frames", "expr:gte(t,n_forced*6)", "-c:a", "aac", "-b:a", "192k", "-ac", "2", "-f", "hls", "-hls_time", "6", "-hls_playlist_type", "event", "-hls_list_size", "0", "-hls_flags", "independent_segments", "-hls_segment_type", "mpegts", "-hls_segment_filename", "/out/seg%05d.ts", "/out/index.m3u8"}},
		{"qsv sw decode 10-bit", "qsv", Decision{VideoCodec: "h264", SourcePixFmt: "yuv420p10le", SourceHeight: 2160, TargetHeight: 1080, AudioIndex: 1}, 0, "/m/a.mkv",
			[]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-protocol_whitelist", "file", "-readrate", "2", "-readrate_initial_burst", "30", "-i", "file:/m/a.mkv", "-map", "0:v:0?", "-map", "0:1", "-sn", "-dn", "-vf", "scale=-2:1080,format=nv12", "-fps_mode", "passthrough", "-c:v", "h264_qsv", "-low_power", "1", "-preset", "veryfast", "-b:v", "8000000", "-maxrate", "12000000", "-bufsize", "24000000", "-forced_idr", "1", "-force_key_frames", "expr:gte(t,n_forced*6)", "-c:a", "aac", "-b:a", "192k", "-ac", "2", "-f", "hls", "-hls_time", "6", "-hls_playlist_type", "event", "-hls_list_size", "0", "-hls_flags", "independent_segments", "-hls_segment_type", "mpegts", "-hls_segment_filename", "/out/seg%05d.ts", "/out/index.m3u8"}},
		{"videotoolbox torrent", "videotoolbox", Decision{VideoCodec: "hevc", SourceHeight: 2160, TargetHeight: 1080, MaxBitrate: 4_000_000, AudioIndex: 1, AudioCopy: true}, 5, "http://127.0.0.1:8420/api/torrent-stream/x/0?k=s",
			[]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-hwaccel", "videotoolbox", "-ss", "5", "-protocol_whitelist", "http,tcp", "-format_whitelist", "matroska,mov,mpegts,avi,flv,asf,mpeg,ogg,rm,mpegvideo,h264,hevc,m4v,mp3,flac,wav,aac,ac3,eac3,dts,ape,wv,aiff", "-readrate", "2", "-readrate_initial_burst", "30", "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_on_network_error", "1", "-reconnect_delay_max", "30", "-reconnect_max_retries", "-1", "-reconnect_delay_total_max", "4294", "-rw_timeout", "600000000", "-i", "http://127.0.0.1:8420/api/torrent-stream/x/0?k=s", "-map", "0:v:0?", "-map", "0:1", "-sn", "-dn", "-vf", "scale=-2:1080,format=yuv420p", "-c:v", "h264_videotoolbox", "-b:v", "4000000", "-maxrate", "4000000", "-bufsize", "8000000", "-force_key_frames", "expr:gte(t,n_forced*6)", "-c:a", "copy", "-f", "hls", "-hls_time", "6", "-hls_playlist_type", "event", "-hls_list_size", "0", "-hls_flags", "independent_segments", "-hls_segment_type", "mpegts", "-hls_segment_filename", "/out/seg%05d.ts", "/out/index.m3u8"}},
		{"libx264 no audio", "none", Decision{VideoCodec: "mpeg4", SourceHeight: 480, AudioIndex: -1, MaxBitrate: 2_000_000}, 0, "/m/b.avi",
			[]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-protocol_whitelist", "file", "-readrate", "2", "-readrate_initial_burst", "30", "-i", "file:/m/b.avi", "-map", "0:v:0?", "-an", "-sn", "-dn", "-vf", "format=yuv420p", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-sc_threshold", "0", "-maxrate", "2000000", "-bufsize", "4000000", "-force_key_frames", "expr:gte(t,n_forced*6)", "-f", "hls", "-hls_time", "6", "-hls_playlist_type", "event", "-hls_list_size", "0", "-hls_flags", "independent_segments", "-hls_segment_type", "mpegts", "-hls_segment_filename", "/out/seg%05d.ts", "/out/index.m3u8"}},
	}
	for _, c := range cases {
		if got := BuildArgsThrottled(c.hw, c.in, c.d, c.start, "/out", th); !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
