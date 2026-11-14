package media

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestResolveHWHEVCAndTonemap(t *testing.T) {
	defer setHWOpts("", nil)
	intel := []string{"/dev/dri/renderD128"}
	cases := []struct {
		name       string
		f          fakeHW
		mode       string
		hevc, tmG  bool
		tmC        bool
		lowPowered string
	}{
		{"qsv: hevc in low power, gpu tone mapping", fakeHW{goos: "linux", files: intel, vendor: "0x8086",
			works: []string{"h264_qsv", "hevc_qsv-lp", "tonemap-qsv", "tonemap-cpu"}}, HWQSV, true, true, true, "hevc_qsv"},
		{"qsv: hevc only in normal mode", fakeHW{goos: "linux", files: intel, vendor: "0x8086",
			works: []string{"h264_qsv", "hevc_qsv"}}, HWQSV, true, false, false, ""},
		{"vaapi without hevc (old Intel, AMD without the encoder)", fakeHW{goos: "linux", files: intel, vendor: "0x1002",
			works: []string{"h264_vaapi", "tonemap-cpu"}}, HWVAAPI, false, false, true, ""},
		{"vaapi: everything", fakeHW{goos: "linux", files: intel, vendor: "0x1002",
			works: []string{"h264_vaapi", "hevc_vaapi", "tonemap-vaapi", "tonemap-cpu"}}, HWVAAPI, true, true, true, ""},
		{"nvenc: never gpu tone mapping", fakeHW{goos: "linux", files: []string{"/dev/nvidiactl"},
			works: []string{"h264_nvenc", "hevc_nvenc", "tonemap-cpu"}}, HWNVENC, true, false, true, ""},
		{"software: no hevc", fakeHW{goos: "linux", works: []string{"tonemap-cpu"}}, HWNone, false, false, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := ResolveHW(context.Background(), c.f.opts("auto"))
			if st.Mode != c.mode || st.HEVC != c.hevc || st.TonemapGPU != c.tmG || st.TonemapCPU != c.tmC {
				t.Fatalf("%+v (ran %v)", st, c.f.all)
			}
			if c.lowPowered != "" && !encLowPower(c.lowPowered) {
				t.Fatalf("%s not low power: %v", c.lowPowered, c.f.all)
			}
		})
	}
}

// fakeAccel is an Accel whose ResolveHW answers st.
func fakeAccel(cfg AccelConfig, st HWStatus) *Accel {
	a := &Accel{Resolve: func(context.Context, HWOptions) HWStatus { return st }}
	a.Apply(context.Background(), cfg, false)
	return a
}

func TestAccelPlan(t *testing.T) {
	all := HWStatus{Mode: HWVAAPI, HEVC: true, TonemapGPU: true, TonemapCPU: true}
	on := AccelConfig{Mode: "auto", Tonemap: true, HEVC: true}
	hdr := Decision{Mode: "transcode", VideoCodec: "hevc", SourcePixFmt: "yuv420p10le", HDRTransfer: "smpte2084", HEVCClient: true}
	cases := []struct {
		name    string
		a       *Accel
		d       Decision
		tonemap string
		hevc    bool
	}{
		{"hdr on vaapi: gpu tone mapping, hevc", fakeAccel(on, all), hdr, "gpu", true},
		{"hlg: the cpu tone-maps", fakeAccel(on, all), func() Decision { d := hdr; d.HDRTransfer = "arib-std-b67"; return d }(), "cpu", true},
		{"av1 hdr: not on gpu frames", fakeAccel(on, all), func() Decision { d := hdr; d.VideoCodec = "av1"; return d }(), "cpu", true},
		{"burn: cpu, h264", fakeAccel(on, all), func() Decision { d := hdr; d.BurnSub = 3; return d }(), "cpu", false},
		{"client without hevc", fakeAccel(on, all), func() Decision { d := hdr; d.HEVCClient = false; return d }(), "gpu", false},
		{"sdr source", fakeAccel(on, all), func() Decision { d := hdr; d.HDRTransfer = ""; return d }(), "", true},
		{"settings off", fakeAccel(AccelConfig{Mode: "auto"}, all), hdr, "", false},
		{"hevc encoder failed its probe", fakeAccel(on, HWStatus{Mode: HWVAAPI, TonemapGPU: true}), hdr, "gpu", false},
		{"no tone mapping anywhere", fakeAccel(on, HWStatus{Mode: HWVAAPI, HEVC: true}), hdr, "", true},
		{"software: never hevc", fakeAccel(on, HWStatus{Mode: HWNone, HEVC: true, TonemapCPU: true}), hdr, "cpu", false},
		{"a copy is left alone", fakeAccel(on, all), func() Decision { d := hdr; d.VideoCopy = true; return d }(), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := c.a.plan(c.a.Mode(), c.d)
			if d.Tonemap != c.tonemap || d.OutHEVC != c.hevc || (c.hevc && !d.FMP4) {
				t.Fatalf("tonemap=%q hevc=%v fmp4=%v", d.Tonemap, d.OutHEVC, d.FMP4)
			}
		})
	}
	var nilAccel *Accel
	if d := nilAccel.plan(HWVAAPI, hdr); d.Tonemap != "" || d.OutHEVC {
		t.Fatalf("%+v", d)
	}
}

func TestEncodeArgsHEVCAndTonemap(t *testing.T) {
	setHWOpts("", map[string]bool{"hevc_qsv": true})
	defer setHWOpts("", nil)
	hdr := Decision{Mode: "transcode", VideoCodec: "hevc", SourcePixFmt: "yuv420p10le", SourceHeight: 2160, TargetHeight: 1080,
		AudioIndex: -1, HDRTransfer: "smpte2084"}
	with := func(tm string, hevc bool) Decision { d := hdr; d.Tonemap, d.OutHEVC, d.FMP4 = tm, hevc, hevc; return d }
	cases := []struct {
		name, hw string
		d        Decision
		want     []string
		not      []string
	}{
		{"vaapi gpu tone mapping, hevc", HWVAAPI, with("gpu", true), []string{
			"-hwaccel vaapi -hwaccel_device /dev/dri/renderD128 -hwaccel_output_format vaapi",
			"-vf scale_vaapi=w=-2:h=1080,tonemap_vaapi=format=nv12:t=bt709:m=bt709:p=bt709 -c:v hevc_vaapi -rc_mode VBR",
			"-tag:v hvc1", "-hls_segment_type fmp4"}, []string{"zscale"}},
		{"vaapi cpu tone mapping (hlg): system frames uploaded", HWVAAPI, func() Decision { d := with("cpu", false); d.HDRTransfer = "arib-std-b67"; return d }(), []string{
			"-init_hw_device vaapi=va:/dev/dri/renderD128 -filter_hw_device va",
			"-vf scale=-2:1080,zscale=tin=arib-std-b67:pin=bt2020:min=bt2020nc:t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p,format=nv12,hwupload -c:v h264_vaapi"},
			[]string{"-hwaccel ", "tonemap_vaapi"}},
		{"qsv gpu tone mapping (VA-API's, mapped), hevc in low power", HWQSV, with("gpu", true), []string{
			"-init_hw_device vaapi=va:/dev/dri/renderD128 -init_hw_device qsv=qs@va -filter_hw_device qs -hwaccel qsv -hwaccel_output_format qsv",
			"-vf hwmap=derive_device=vaapi,scale_vaapi=w=-2:h=1080,tonemap_vaapi=format=nv12:t=bt709:m=bt709:p=bt709,hwmap=derive_device=qsv,format=qsv -fps_mode passthrough -c:v hevc_qsv -low_power 1 -preset veryfast",
			"-forced_idr 1 -tag:v hvc1"}, []string{"vpp_qsv"}},
		{"qsv cpu tone mapping", HWQSV, with("cpu", false), []string{
			"zscale=tin=smpte2084", "format=yuv420p,format=nv12 -fps_mode passthrough -c:v h264_qsv -low_power 1"},
			[]string{"-hwaccel", "vpp_qsv"}},
		{"nvenc hevc, cpu tone mapping", HWNVENC, with("cpu", true), []string{
			"-hwaccel cuda", "zscale=tin=smpte2084", "format=yuv420p,format=yuv420p -c:v hevc_nvenc -preset p4", "-forced-idr 1 -tag:v hvc1"}, nil},
		{"videotoolbox hevc", HWVideoToolbox, with("", true), []string{"-c:v hevc_videotoolbox -b:v 6M -tag:v hvc1"}, []string{"zscale"}},
		{"software: h264 even when asked for hevc", HWNone, with("cpu", true), []string{"zscale=tin=smpte2084", "-c:v libx264"},
			[]string{"hevc", "hvc1"}},
		{"no tone mapping: as before", HWVAAPI, with("", false), []string{"-vf scale_vaapi=w=-2:h=1080:format=nv12 -c:v h264_vaapi"},
			[]string{"tonemap", "zscale", "hvc1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := strings.Join(BuildArgs(c.hw, "/m/in.mkv", c.d, 0, "/w"), " ")
			for _, w := range c.want {
				if !strings.Contains(a, w) {
					t.Fatalf("want %q in\n%s", w, a)
				}
			}
			for _, n := range c.not {
				if strings.Contains(a, n) {
					t.Fatalf("%q in\n%s", n, a)
				}
			}
		})
	}
	// a device copy tone-maps too (never HEVC)
	pl := DeviceCopyPlan{VideoCodec: "hevc", SourcePixFmt: "yuv420p10le", SourceHeight: 2160, TargetHeight: 1080, AudioIndex: -1,
		HDRTransfer: "smpte2084", Tonemap: "gpu"}
	if a := strings.Join(DeviceCopyArgs(HWVAAPI, "/m/a.mkv", pl, "/d/x.part.mp4"), " "); !strings.Contains(a, "tonemap_vaapi") || strings.Contains(a, "hevc_vaapi") {
		t.Fatal(a)
	}
	pl.Tonemap = "cpu"
	if a := strings.Join(DeviceCopyArgs(HWVAAPI, "/m/a.mkv", pl, "/d/x.part.mp4"), " "); !strings.Contains(a, "zscale") || strings.Contains(a, "-hwaccel ") {
		t.Fatal(a)
	}
	// a live programme: the CPU tone-maps, the GPU still encodes
	in := LiveInput{Input: "/m/a.mkv", OutDir: "/o", Playlist: "v.m3u8", VideoCodec: "hevc", PixFmt: "yuv420p10le", SrcWidth: 3840, SrcHeight: 2160,
		HDRTransfer: "smpte2084", Audio: []LiveAudio{{Index: LiveFirstAudio}}}
	if a := strings.Join(LiveArgs(HWVAAPI, in), " "); !strings.Contains(a, "-vf zscale=tin=smpte2084") || strings.Contains(a, "-hwaccel ") ||
		!strings.Contains(a, ",hwupload -c:v h264_vaapi") {
		t.Fatal(a)
	}
}

func TestProbeDecideHDRAndHEVCClient(t *testing.T) {
	p := ProbeResult{Container: "mkv", Video: &VideoStream{Codec: "hevc", Height: 2160, PixFmt: "yuv420p10le", HDR: true, ColorTransfer: "arib-std-b67"}}
	d := Decide(p, ClientCaps{VideoCodecs: []string{"h264", "hevc"}, MaxHeight: 1080}, -1)
	if d.Mode != "transcode" || d.HDRTransfer != "arib-std-b67" || !d.HEVCClient {
		t.Fatalf("%+v", d)
	}
	// a cache row from before color_transfer was kept: PQ
	p.Video.ColorTransfer = ""
	if d := Decide(p, ClientCaps{VideoCodecs: []string{"h264"}}, -1); d.HDRTransfer != "smpte2084" || d.HEVCClient {
		t.Fatalf("%+v", d)
	}
	p.Video.HDR = false
	if d := Decide(p, ClientCaps{VideoCodecs: []string{"h264"}}, -1); d.HDRTransfer != "" {
		t.Fatalf("%+v", d)
	}
}

// A session that asked for HEVC with GPU tone mapping restarts in software
// with H.264 and the CPU's tone mapping.
func TestHLSFallbackDropsHEVCAndGPUTonemap(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$*\" in *_vaapi*) echo gpu-boom >&2; exit 1;; esac\n" +
		"for a; do last=$a; done\nprintf '#EXTM3U\\n#EXTINF:6.0,\\nseg00000.m4s\\n' > \"$last\"\nexec sleep 1000\n"
	p := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	a := fakeAccel(AccelConfig{Mode: "vaapi", Tonemap: true, HEVC: true}, HWStatus{Mode: HWVAAPI, HEVC: true, TonemapGPU: true, TonemapCPU: true})
	m := &HLSManager{FFmpeg: p, WorkDir: t.TempDir(), HW: HWQSV, Accel: a}
	defer m.Close()
	d := Decision{Mode: "transcode", VideoCodec: "hevc", SourcePixFmt: "yuv420p10le", HDRTransfer: "smpte2084", HEVCClient: true, AudioIndex: -1}
	id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: "in", Decision: d, AudioIndex: -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Playlist(id); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	runs := strings.Split(strings.TrimSpace(string(b)), "\n")
	// GPU tone mapping, then the same GPU with the CPU's, then software
	if len(runs) != 3 || !strings.Contains(runs[0], "hevc_vaapi") || !strings.Contains(runs[0], "tonemap_vaapi") ||
		!strings.Contains(runs[1], "hevc_vaapi") || !strings.Contains(runs[1], "zscale") || strings.Contains(runs[1], "tonemap_vaapi") ||
		!strings.Contains(runs[2], "-c:v libx264") || !strings.Contains(runs[2], "zscale") || strings.Contains(runs[2], "hvc1") ||
		!strings.Contains(runs[2], "-hls_segment_type fmp4") {
		t.Fatalf("%q", runs)
	}
	if got := m.sessionHW(id); got != HWNone {
		t.Fatal(got)
	}
	// the settings changed meanwhile: a new session takes them
	a.Resolve = func(context.Context, HWOptions) HWStatus { return HWStatus{Mode: HWNone} }
	a.Apply(context.Background(), AccelConfig{Mode: HWNone}, false)
	os.Remove(log)
	id2, err := m.Start(context.Background(), StartOptions{FileID: 2, Input: "in", Decision: d, AudioIndex: -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Playlist(id2); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(log)
	if runs := strings.Split(strings.TrimSpace(string(b)), "\n"); len(runs) != 1 || slices.ContainsFunc(runs, func(r string) bool { return strings.Contains(r, "vaapi") }) {
		t.Fatalf("%q", runs)
	}
	if got := m.sessionHW(id); got != HWNone { // the first keeps running
		t.Fatal(got)
	}
}
