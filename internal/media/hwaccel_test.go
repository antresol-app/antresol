package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeHW is a machine for ResolveHW: the files that exist, the PCI vendor
// of the render node and the encoders whose test encode works.
type fakeHW struct {
	goos   string
	files  []string
	vendor string
	works  []string // probeName()s that work: h264_qsv, h264_vaapi, h264_vaapi-lp (low power only), hevc_qsv-lp, tonemap-vaapi…
	ran    []string // the H.264 test encodes, in order
	all    []string
	gid    int               // the render node's group
	denied bool              // the server's user may not open the render node
	errs   map[string]string // ffmpeg's error of a failing test encode
}

func (f *fakeHW) opts(mode string) HWOptions {
	return HWOptions{
		FFmpeg: "ffmpeg", Mode: mode, goos: f.goos,
		exist: func(p string) bool { return slices.Contains(f.files, p) },
		glob: func(string) []string {
			var m []string
			for _, p := range f.files {
				if strings.HasPrefix(p, "/dev/dri/renderD") {
					m = append(m, p)
				}
			}
			return m
		},
		read: func(p string) string {
			if strings.HasSuffix(p, "/device/vendor") {
				return f.vendor
			}
			return ""
		},
		access: func(string) (int, error) {
			if f.denied {
				return f.gid, os.ErrPermission
			}
			return f.gid, nil
		},
		run: func(_ context.Context, _ string, args []string) error {
			name := probeName(args)
			if strings.HasPrefix(name, "h264") {
				f.ran = append(f.ran, strings.TrimSuffix(name, "-lp"))
			}
			f.all = append(f.all, name)
			if slices.Contains(f.works, name) {
				return nil
			}
			if e, ok := f.errs[strings.TrimSuffix(name, "-lp")]; ok {
				return errors.New(e)
			}
			return errors.New(name + ": no device")
		},
	}
}

// probeName names a test encode: its encoder (with "-lp" in low-power
// mode for the encoders tried both ways), or tonemap-cpu / -vaapi / -qsv.
func probeName(args []string) string {
	j := strings.Join(args, " ")
	switch {
	case strings.Contains(j, "zscale"):
		return "tonemap-cpu"
	case strings.Contains(j, "qsv=qs@va"):
		return "tonemap-qsv"
	case strings.Contains(j, "tonemap_vaapi"):
		return "tonemap-vaapi"
	}
	enc := args[slices.Index(args, "-c:v")+1]
	if strings.Contains(j, "-low_power 1") && enc != "h264_qsv" {
		return enc + "-lp"
	}
	return enc
}

func TestResolveHWAuto(t *testing.T) {
	cases := []struct {
		name string
		f    fakeHW
		want string
		ran  []string
	}{
		{"intel qsv", fakeHW{goos: "linux", files: []string{"/dev/dri/renderD128"}, vendor: "0x8086", works: []string{"h264_qsv", "h264_vaapi"}},
			HWQSV, []string{"h264_qsv"}},
		{"intel without the qsv runtime", fakeHW{goos: "linux", files: []string{"/dev/dri/renderD128"}, vendor: "0x8086", works: []string{"h264_vaapi"}},
			HWVAAPI, []string{"h264_qsv", "h264_vaapi"}},
		{"amd: vaapi only", fakeHW{goos: "linux", files: []string{"/dev/dri/renderD128"}, vendor: "0x1002", works: []string{"h264_vaapi", "h264_qsv"}},
			HWVAAPI, []string{"h264_vaapi"}},
		{"nvidia first", fakeHW{goos: "linux", files: []string{"/dev/nvidiactl", "/dev/dri/renderD128"}, vendor: "0x10de", works: []string{"h264_nvenc"}},
			HWNVENC, []string{"h264_nvenc"}},
		{"nvidia device but no driver in the container", fakeHW{goos: "linux", files: []string{"/dev/nvidia0", "/dev/dri/renderD128"}, vendor: "0x8086", works: []string{"h264_qsv"}},
			HWQSV, []string{"h264_nvenc", "h264_qsv"}},
		{"render node without a driver", fakeHW{goos: "linux", files: []string{"/dev/dri/renderD128"}, vendor: "0x8086"},
			HWNone, []string{"h264_qsv", "h264_vaapi", "h264_vaapi"}},
		{"no gpu", fakeHW{goos: "linux"}, HWNone, nil},
		{"mac", fakeHW{goos: "darwin", works: []string{"h264_videotoolbox"}}, HWVideoToolbox, []string{"h264_videotoolbox"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := ResolveHW(context.Background(), c.f.opts("auto"))
			if st.Mode != c.want || !slices.Equal(c.f.ran, c.ran) {
				t.Fatalf("mode=%s ran=%v, want %s %v (%+v)", st.Mode, c.f.ran, c.want, c.ran, st)
			}
		})
	}
	defer setHWOpts("", nil)
	// an unknown value is auto
	f := fakeHW{goos: "linux", files: []string{"/dev/dri/renderD129"}, vendor: "0x1002", works: []string{"h264_vaapi"}}
	if st := ResolveHW(context.Background(), f.opts("bogus")); st.Mode != HWVAAPI || st.Requested != "auto" || st.Device != "/dev/dri/renderD129" {
		t.Fatalf("%+v", st)
	}
	if vaapiDevice() != "/dev/dri/renderD129" {
		t.Fatal(vaapiDevice())
	}
}

func TestResolveHWVAAPILowPower(t *testing.T) {
	defer setHWOpts("", nil)
	f := fakeHW{goos: "linux", files: []string{"/dev/dri/renderD128"}, vendor: "0x1002", works: []string{"h264_vaapi-lp"}}
	st := ResolveHW(context.Background(), f.opts("vaapi"))
	if st.Mode != HWVAAPI || len(st.Probes) < 3 || st.Probes[1].OK || !st.Probes[2].OK || !vaapiLowPower() {
		t.Fatalf("%+v lp=%v", st, vaapiLowPower())
	}
	a := strings.Join(BuildArgs(HWVAAPI, "/m/in.mkv", Decision{Mode: "transcode", VideoCodec: "h264", AudioIndex: -1}, 0, "/w"), " ")
	if !strings.Contains(a, "-c:v h264_vaapi -low_power 1 -rc_mode VBR") {
		t.Fatal(a)
	}
}

func TestResolveHWExplicit(t *testing.T) {
	defer setHWOpts("", nil)
	// an explicit mode is used even when its test encode fails
	f := fakeHW{goos: "linux"}
	for _, m := range []string{HWQSV, HWVAAPI, HWNVENC, HWVideoToolbox} {
		if st := ResolveHW(context.Background(), f.opts(m)); st.Mode != m || st.Requested != m || len(st.Probes) < 2 || st.Probes[1].OK {
			t.Fatalf("%s: %+v", m, st)
		}
	}
	// none tries only the CPU's tone mapping
	f.all = nil
	if st := ResolveHW(context.Background(), f.opts(HWNone)); st.Mode != HWNone || !slices.Equal(f.all, []string{"tonemap-cpu"}) {
		t.Fatalf("%+v %v", st, f.all)
	}
	// VAULT_HWACCEL_DEVICE
	o := f.opts(HWVAAPI)
	o.Device = "/dev/dri/renderD130"
	if st := ResolveHW(context.Background(), o); st.Device != "/dev/dri/renderD130" || vaapiDevice() != "/dev/dri/renderD130" {
		t.Fatalf("%+v", st)
	}
}

func TestResolveHWTimeout(t *testing.T) {
	o := HWOptions{Mode: HWNVENC, goos: "linux", Timeout: 50 * time.Millisecond,
		run: func(ctx context.Context, _ string, _ []string) error { <-ctx.Done(); return ctx.Err() }}
	st := ResolveHW(context.Background(), o)
	if len(st.Probes) < 2 || !strings.Contains(st.Probes[1].Error, "timed out") {
		t.Fatalf("%+v", st)
	}
}

func TestProbeArgs(t *testing.T) {
	for mode, want := range map[string]string{
		HWQSV:          "-f lavfi -i testsrc2=size=640x360:rate=25:duration=0.5 -vf format=nv12 -c:v h264_qsv -low_power 1",
		HWVAAPI:        "-init_hw_device vaapi=va:/dev/dri/renderD128 -filter_hw_device va -f lavfi -i testsrc2=size=640x360:rate=25:duration=0.5 -vf format=nv12,hwupload,scale_vaapi=w=320:h=180:format=nv12 -c:v h264_vaapi -b:v",
		HWNVENC:        "-vf format=yuv420p -c:v h264_nvenc",
		HWVideoToolbox: "-c:v h264_videotoolbox",
	} {
		a := strings.Join(probeArgs(mode, h264Encoder[mode], DefaultRenderNode, false), " ")
		if !strings.Contains(a, want) || !strings.HasSuffix(a, "-frames:v 10 -f null -") {
			t.Errorf("%s: %s", mode, a)
		}
	}
}

func TestBuildArgsVAAPI(t *testing.T) {
	setHWOpts("/dev/dri/renderD129", nil)
	defer setHWOpts("", nil)
	// decodable: decoded, scaled and encoded on the GPU
	d := Decision{Mode: "transcode", VideoCodec: "hevc", SourcePixFmt: "yuv420p10le", SourceHeight: 2160, TargetHeight: 1080, MaxBitrate: 6_000_000, AudioIndex: 1}
	a := strings.Join(BuildArgs(HWVAAPI, "/m/in.mkv", d, 0, "/w"), " ")
	for _, want := range []string{
		"-hwaccel vaapi -hwaccel_device /dev/dri/renderD129 -hwaccel_output_format vaapi",
		"-vf scale_vaapi=w=-2:h=1080:format=nv12 -c:v h264_vaapi -rc_mode VBR -b:v 6000000 -maxrate 6000000 -bufsize 12000000",
		"-force_key_frames expr:gte(t,n_forced*6)",
	} {
		if !strings.Contains(a, want) {
			t.Fatalf("want %q in %s", want, a)
		}
	}
	if strings.Contains(a, "-low_power") || strings.Contains(a, "-init_hw_device") {
		t.Fatal(a)
	}
	// AV1 and 10-bit H.264: the CPU decodes and scales, the GPU encodes
	for _, d := range []Decision{
		{Mode: "transcode", VideoCodec: "av1", SourcePixFmt: "yuv420p", TargetHeight: 720, AudioIndex: -1},
		{Mode: "transcode", VideoCodec: "h264", SourcePixFmt: "yuv420p10le", TargetHeight: 720, AudioIndex: -1},
	} {
		a := strings.Join(BuildArgs(HWVAAPI, "/m/in.mkv", d, 0, "/w"), " ")
		if !strings.Contains(a, "-init_hw_device vaapi=va:/dev/dri/renderD129 -filter_hw_device va") || strings.Contains(a, "-hwaccel ") ||
			!strings.Contains(a, "-vf scale=-2:720,format=nv12,hwupload -c:v h264_vaapi") {
			t.Fatalf("%s: %s", d.VideoCodec, a)
		}
	}
	// a burned-in subtitle: overlay on system frames, then uploaded
	d = Decision{Mode: "transcode", AudioIndex: 1, VideoCodec: "h264", SourcePixFmt: "yuv420p", BurnSub: 4, TargetHeight: 720}
	a = strings.Join(BuildArgs(HWVAAPI, "/m/x.mkv", d, 0, "/w"), " ")
	if !strings.Contains(a, "-init_hw_device vaapi=va:/dev/dri/renderD129 -filter_hw_device va") || strings.Contains(a, "-hwaccel ") ||
		!strings.Contains(a, "overlay=eof_action=pass:format=auto,scale=-2:720,format=nv12,hwupload[v]") || !strings.Contains(a, "-c:v h264_vaapi") {
		t.Fatal(a)
	}
	// a copy never touches the GPU
	a = strings.Join(BuildArgs(HWVAAPI, "/m/x.mkv", Decision{Mode: "remux", VideoCopy: true, VideoCodec: "h264", AudioIndex: 1}, 0, "/w"), " ")
	if strings.Contains(a, "vaapi") {
		t.Fatal(a)
	}
}

func TestBuildArgsNVENC(t *testing.T) {
	d := Decision{Mode: "transcode", VideoCodec: "hevc", SourcePixFmt: "yuv420p10le", SourceHeight: 2160, TargetHeight: 1080, AudioIndex: -1}
	a := strings.Join(BuildArgs(HWNVENC, "/m/in.mkv", d, 0, "/w"), " ")
	if !strings.Contains(a, "-hwaccel cuda -protocol_whitelist") || strings.Contains(a, "hwaccel_output_format") ||
		!strings.Contains(a, "-vf scale=-2:1080,format=yuv420p -c:v h264_nvenc -preset p4 -rc vbr -b:v 8000000 -maxrate 12000000 -bufsize 24000000 -forced-idr 1") {
		t.Fatal(a)
	}
	// burn: no NVDEC (overlay on system frames), NVENC still encodes
	d.BurnSub = 3
	a = strings.Join(BuildArgs(HWNVENC, "/m/in.mkv", d, 0, "/w"), " ")
	if strings.Contains(a, "-hwaccel") || !strings.Contains(a, "-c:v h264_nvenc") {
		t.Fatal(a)
	}
}

func TestDeviceCopyArgsGPUModes(t *testing.T) {
	pl := DeviceCopyPlan{VideoCodec: "h264", SourcePixFmt: "yuv420p", SourceHeight: 1080, TargetHeight: 720, AudioIndex: 1}
	a := strings.Join(DeviceCopyArgs(HWVAAPI, "/m/a.mkv", pl, "/d/x.part.mp4"), " ")
	if !strings.Contains(a, "-hwaccel vaapi -hwaccel_device /dev/dri/renderD128 -hwaccel_output_format vaapi") ||
		!strings.Contains(a, "-vf scale_vaapi=w=-2:h=720:format=nv12 -c:v h264_vaapi") {
		t.Fatal(a)
	}
	a = strings.Join(DeviceCopyArgs(HWNVENC, "/m/a.mkv", pl, "/d/x.part.mp4"), " ")
	if !strings.Contains(a, "-hwaccel cuda") || !strings.Contains(a, "-c:v h264_nvenc") {
		t.Fatal(a)
	}
}

func TestLiveArgsGPUModes(t *testing.T) {
	in := LiveInput{Input: "/m/a.mkv", OutDir: "/o", Playlist: "v.m3u8", VideoCodec: "h264", PixFmt: "yuv420p", SrcWidth: 1440, SrcHeight: 1080,
		Width: 1920, Height: 1080, Audio: []LiveAudio{{Index: LiveFirstAudio}}}
	a := strings.Join(LiveArgs(HWVAAPI, in), " ")
	if !strings.Contains(a, "-hwaccel vaapi -hwaccel_device /dev/dri/renderD128 -hwaccel_output_format vaapi") ||
		!strings.Contains(a, "-vf scale_vaapi=w=1440:h=1080:format=nv12,hwdownload,format=nv12,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black,setsar=1,hwupload") ||
		!strings.Contains(a, "-c:v h264_vaapi -rc_mode VBR -b:v 6000000") {
		t.Fatal(a)
	}
	// size unknown: the CPU fits, the GPU encodes
	in2 := in
	in2.SrcWidth, in2.SrcHeight = 0, 0
	a = strings.Join(LiveArgs(HWVAAPI, in2), " ")
	if !strings.Contains(a, "-init_hw_device vaapi=va:/dev/dri/renderD128 -filter_hw_device va") || !strings.Contains(a, ",hwupload -c:v h264_vaapi") ||
		strings.Contains(a, "-hwaccel ") {
		t.Fatal(a)
	}
	a = strings.Join(LiveArgs(HWNVENC, in), " ")
	if !strings.Contains(a, "-hwaccel cuda") || !strings.Contains(a, "-c:v h264_nvenc -preset p4 -rc vbr -b:v 6000000") ||
		strings.Contains(a, "hwupload") {
		t.Fatal(a)
	}
}

// Every GPU mode falls back to software when its ffmpeg exits before the
// first segment (an encoder that cannot open, a driver missing at run time).
func TestHLSSoftwareFallbackGPUModes(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	script := "#!/bin/sh\ncase \"$*\" in *_vaapi*|*_nvenc*|*_qsv*) echo gpu-boom >&2; exit 1;; esac\n" +
		"for a; do last=$a; done\nprintf '#EXTM3U\\n#EXTINF:6.0,\\nseg00000.ts\\n' > \"$last\"\nexec sleep 1000\n"
	p := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, hw := range []string{HWVAAPI, HWNVENC} {
		m := &HLSManager{FFmpeg: p, WorkDir: t.TempDir(), HW: hw}
		id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: "in", Decision: Decision{Mode: "transcode", VideoCodec: "hevc", AudioIndex: -1}, AudioIndex: -1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Playlist(id); err != nil {
			t.Fatalf("%s: %v", hw, err)
		}
		if got := m.sessionHW(id); got != HWNone {
			t.Fatalf("%s: hw=%s, want none after fallback", hw, got)
		}
		m.Close()
	}
}

// A GPU that fails its test encode says why: the kind decides the advice
// («О сервере», the settings page, the log).
func TestResolveHWProblem(t *testing.T) {
	defer setHWOpts("", nil)
	noDisplay := "[AVHWDeviceContext @ 0x1] No VA display found for device /dev/dri/renderD128.\nDevice creation failed: -22."
	node := []string{"/dev/dri/renderD128"}
	cases := []struct {
		name string
		f    fakeHW
		mode string
		want HWProblem
	}{
		// the public image without group_add: the node is there, vault may not open it
		{"permission", fakeHW{goos: "linux", files: node, vendor: "0x8086", denied: true, gid: 992,
			errs: map[string]string{"h264_vaapi": noDisplay}}, "auto",
			HWProblem{Kind: ProblemPermission, Mode: HWVAAPI, Device: node[0], GID: 992, Encoder: "h264_vaapi", Detail: noDisplay}},
		{"intel without a driver", fakeHW{goos: "linux", files: node, vendor: "0x8086",
			errs: map[string]string{"h264_vaapi": noDisplay}}, "auto",
			HWProblem{Kind: ProblemDriver, Mode: HWVAAPI, Device: node[0], Driver: vaDriverFor("0x8086"), Encoder: "h264_vaapi", Detail: noDisplay}},
		{"amd without a driver", fakeHW{goos: "linux", files: node, vendor: "0x1002",
			errs: map[string]string{"h264_vaapi": "libva error: vaGetDriverNameByIndex() failed with unknown libva error"}}, "auto",
			HWProblem{Kind: ProblemDriver, Mode: HWVAAPI, Device: node[0], Driver: "radeonsi (mesa-va-drivers)", Encoder: "h264_vaapi",
				Detail: "libva error: vaGetDriverNameByIndex() failed with unknown libva error"}},
		{"encoder refused", fakeHW{goos: "linux", files: node, vendor: "0x1002",
			errs: map[string]string{"h264_vaapi": "[h264_vaapi @ 0x1] No usable encoding entrypoint found for profile VAProfileH264High (7)."}}, "auto",
			HWProblem{Kind: ProblemEncoder, Mode: HWVAAPI, Device: node[0], Encoder: "h264_vaapi",
				Detail: "[h264_vaapi @ 0x1] No usable encoding entrypoint found for profile VAProfileH264High (7)."}},
		{"nvidia without the toolkit", fakeHW{goos: "linux", files: []string{"/dev/nvidia0"},
			errs: map[string]string{"h264_nvenc": "[h264_nvenc @ 0x1] Cannot load libcuda.so.1"}}, "auto",
			HWProblem{Kind: ProblemNVIDIA, Mode: HWNVENC, Encoder: "h264_nvenc", Detail: "[h264_nvenc @ 0x1] Cannot load libcuda.so.1"}},
		{"explicit vaapi, no node", fakeHW{goos: "linux"}, HWVAAPI,
			HWProblem{Kind: ProblemNoDevice, Mode: HWVAAPI, Device: DefaultRenderNode, Encoder: "h264_vaapi", Detail: "h264_vaapi-lp: no device"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := ResolveHW(context.Background(), c.f.opts(c.mode))
			if st.Problem == nil || *st.Problem != c.want {
				t.Fatalf("problem %+v, want %+v", st.Problem, c.want)
			}
		})
	}
	// a GPU that works, and no GPU at all, have no problem
	for _, f := range []fakeHW{{goos: "linux", files: node, vendor: "0x1002", works: []string{"h264_vaapi"}}, {goos: "linux"}} {
		if st := ResolveHW(context.Background(), f.opts("auto")); st.Problem != nil {
			t.Fatalf("%+v", st.Problem)
		}
	}
}

// The reason of a failed test encode is ffmpeg's first error lines, not
// libva's chatter or the consequences after them.
func TestProbeError(t *testing.T) {
	out := "libva info: VA-API version 1.22.0\nlibva info: Trying to open /usr/lib/x86_64-linux-gnu/dri/iHD_drv_video.so\n" +
		"[h264_qsv @ 0x1] Error creating a MFX session: -9.\n" +
		"[vost#0:0/h264_qsv @ 0x2] Error while opening encoder - maybe incorrect parameters such as bit_rate, rate, width or height.\n" +
		strings.Repeat("[vf#0:0 @ 0x3] Error sending frames to consumers: Unknown error occurred\n", 10)
	got := probeError(out)
	if !strings.HasPrefix(got, "[h264_qsv @ 0x1] Error creating a MFX session: -9.") || strings.Contains(got, "libva info") || len(got) > 400 {
		t.Fatalf("%q", got)
	}
}
