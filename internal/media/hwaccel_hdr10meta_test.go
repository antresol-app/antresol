package media

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func ptrBool(b bool) *bool { return &b }

func TestParseHDRMeta(t *testing.T) {
	cases := []struct {
		name string
		js   string
		want HDRMeta
		err  bool
	}{
		{"in the first frame (SEI)", `{"streams":[{}],"frames":[{"side_data_list":[{"side_data_type":"H.26[45] User Data Unregistered SEI message"},
			{"side_data_type":"Mastering display metadata"},{"side_data_type":"Content light level metadata"}]}]}`, HDRMeta{true, true}, false},
		{"in the container", `{"streams":[{"side_data_list":[{"side_data_type":"Mastering display metadata"}]}],"frames":[{}]}`, HDRMeta{Mastering: true}, false},
		{"none", `{"streams":[{}],"frames":[{"side_data_list":[{"side_data_type":"H.26[45] User Data Unregistered SEI message"}]}]}`, HDRMeta{}, false},
		{"no frame decoded: unknown", `{"streams":[{}],"frames":[]}`, HDRMeta{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := parseHDRMeta([]byte(c.js))
			if (err != nil) != c.err || m != c.want {
				t.Fatalf("%+v %v", m, err)
			}
		})
	}
}

func TestParseProbeContainerMastering(t *testing.T) {
	js := `{"format":{"format_name":"matroska,webm"},"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","color_transfer":"smpte2084",
		"side_data_list":[{"side_data_type":"Mastering display metadata"},{"side_data_type":"Content light level metadata"}]}]}`
	r, err := parseProbe([]byte(js), "a.mkv")
	if err != nil || r.Video.HDRMastering == nil || !*r.Video.HDRMastering || !*r.Video.HDRLightLevel || r.Video.NeedsHDRMeta() || r.Video.NoHDRMastering() {
		t.Fatalf("%+v %v", r.Video, err)
	}
	// none in the container: unknown until the first frame is looked at (Probe)
	js = strings.Replace(js, `"side_data_list":[{"side_data_type":"Mastering display metadata"},{"side_data_type":"Content light level metadata"}]`, `"tags":{}`, 1)
	if r, _ = parseProbe([]byte(js), "a.mkv"); r.Video.HDRMastering != nil || !r.Video.NeedsHDRMeta() {
		t.Fatalf("%+v", r.Video)
	}
}

func TestDecideHDRNoMastering(t *testing.T) {
	v := &VideoStream{Codec: "hevc", Height: 2160, PixFmt: "yuv420p10le", HDR: true, ColorTransfer: "smpte2084"}
	p := ProbeResult{Container: "mkv", Video: v}
	caps := ClientCaps{VideoCodecs: []string{"h264"}}
	for _, c := range []struct {
		name      string
		mastering *bool
		transfer  string
		want      bool
	}{
		{"unknown (an old cache row): the gpu is tried", nil, "smpte2084", false},
		{"with mastering metadata", ptrBool(true), "smpte2084", false},
		{"without", ptrBool(false), "smpte2084", true},
		{"hlg never needs it", ptrBool(false), "arib-std-b67", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			v.HDRMastering, v.ColorTransfer = c.mastering, c.transfer
			if d := Decide(p, caps, -1); d.HDRNoMastering != c.want {
				t.Fatalf("%+v", d)
			}
			if pl := PlanDeviceCopy(p, nil, 0, 0, -1); pl.HDRNoMastering != c.want {
				t.Fatalf("%+v", pl)
			}
		})
	}
}

func TestAccelPlanHDRNoMastering(t *testing.T) {
	a := fakeAccel(AccelConfig{Mode: "auto", Tonemap: true, HEVC: true}, HWStatus{Mode: HWVAAPI, HEVC: true, TonemapGPU: true, TonemapCPU: true})
	d := Decision{Mode: "transcode", VideoCodec: "hevc", SourcePixFmt: "yuv420p10le", HDRTransfer: "smpte2084", HEVCClient: true, HDRNoMastering: true}
	if d := a.plan(HWVAAPI, d); d.Tonemap != "cpu" || !d.OutHEVC {
		t.Fatalf("%+v", d)
	}
}

// failingFFmpeg is a script ffmpeg that logs each command line and fails
// those containing fail; the others write a one-segment playlist (or the
// output file) and, for HLS, keep running.
func failingFFmpeg(t *testing.T, fail string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$*\" in *" + fail + "*) echo boom >&2; exit 1;; esac\n" +
		"for a; do last=$a; done\nprintf '#EXTM3U\\n#EXTINF:6.0,\\nseg00000.m4s\\n' > \"$last\"\n" +
		"case \"$last\" in *.m3u8) exec sleep 1000;; esac\n"
	bin = filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func runsOf(t *testing.T, log string) []string {
	t.Helper()
	b, _ := os.ReadFile(log)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// HDR10 without mastering metadata starts on the CPU's tone mapping: no
// failed first run on tonemap_vaapi (the GPU still encodes).
func TestHLSNoMasteringStartsOnCPUTonemap(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin, log := failingFFmpeg(t, "tonemap_vaapi")
	for _, hw := range []string{HWVAAPI, HWQSV} {
		t.Run(hw, func(t *testing.T) {
			os.Remove(log)
			a := fakeAccel(AccelConfig{Mode: hw, Tonemap: true, HEVC: true}, HWStatus{Mode: hw, HEVC: true, TonemapGPU: true, TonemapCPU: true})
			m := &HLSManager{FFmpeg: bin, WorkDir: t.TempDir(), HW: hw, Accel: a}
			defer m.Close()
			d := Decision{Mode: "transcode", VideoCodec: "hevc", SourcePixFmt: "yuv420p10le", HDRTransfer: "smpte2084",
				HEVCClient: true, HDRNoMastering: true, AudioIndex: -1}
			id, err := m.Start(context.Background(), StartOptions{FileID: 1, Input: "in", Decision: d, AudioIndex: -1})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Playlist(id); err != nil {
				t.Fatal(err)
			}
			runs := runsOf(t, log)
			if len(runs) != 1 || !strings.Contains(runs[0], "hevc_"+hw) || !strings.Contains(runs[0], "zscale") ||
				strings.Contains(runs[0], "-hwaccel ") || strings.Contains(runs[0], "tonemap_vaapi") {
				t.Fatalf("%q", runs)
			}
			// with the metadata (or not known): the gpu tone-maps
			os.Remove(log)
			d.HDRNoMastering = false
			id2, err := m.Start(context.Background(), StartOptions{FileID: 2, Input: "in", Decision: d, AudioIndex: -1})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Playlist(id2); err != nil {
				t.Fatal(err)
			}
			// its fake refusal falls back as before
			if runs := runsOf(t, log); len(runs) != 2 || !strings.Contains(runs[0], "tonemap_vaapi") || !strings.Contains(runs[1], "zscale") {
				t.Fatalf("%q", runs)
			}
		})
	}
}

func TestDeviceCopyKeyHEVC(t *testing.T) {
	k := testKey(5)
	h264 := k.ID()
	k.HEVC = true
	if k.ID() == h264 || !IsCopyID(k.ID()) {
		t.Fatal(k.ID(), h264)
	}
}

func TestDeviceCopyHEVCAndNoMastering(t *testing.T) {
	setHWOpts("", nil)
	defer setHWOpts("", nil)
	hdr := DeviceCopyPlan{VideoCodec: "hevc", SourcePixFmt: "yuv420p10le", SourceHeight: 2160, TargetHeight: 1080, AudioIndex: -1,
		DurationMs: 1000, HDRTransfer: "smpte2084"}
	on := AccelConfig{Mode: HWVAAPI, Tonemap: true, HEVC: true}
	all := HWStatus{Mode: HWVAAPI, HEVC: true, TonemapGPU: true, TonemapCPU: true}
	cases := []struct {
		name string
		cfg  AccelConfig
		st   HWStatus
		pl   func(DeviceCopyPlan) DeviceCopyPlan
		fail error // the first run's
		want []string
		not  []string
		runs int
	}{
		{"hevc asked for: hevc_vaapi, hvc1, faststart", on, all, func(p DeviceCopyPlan) DeviceCopyPlan { p.HEVC = true; return p }, nil,
			[]string{"-c:v hevc_vaapi", "-tag:v hvc1", "-movflags +faststart", "tonemap_vaapi"}, []string{"h264_vaapi"}, 1},
		{"not asked for: h264", on, all, func(p DeviceCopyPlan) DeviceCopyPlan { return p }, nil,
			[]string{"-c:v h264_vaapi"}, []string{"hevc_vaapi", "hvc1"}, 1},
		{"«HEVC на выходе» off: h264", AccelConfig{Mode: HWVAAPI, Tonemap: true}, all, func(p DeviceCopyPlan) DeviceCopyPlan { p.HEVC = true; return p }, nil,
			[]string{"-c:v h264_vaapi"}, []string{"hevc_vaapi"}, 1},
		{"hevc encoder failed its probe: h264", on, HWStatus{Mode: HWVAAPI, TonemapGPU: true, TonemapCPU: true}, func(p DeviceCopyPlan) DeviceCopyPlan { p.HEVC = true; return p }, nil,
			[]string{"-c:v h264_vaapi"}, []string{"hevc_vaapi"}, 1},
		{"a copied video stays as it is", on, all, func(p DeviceCopyPlan) DeviceCopyPlan { p.HEVC, p.VideoCopy = true, true; return p }, nil,
			[]string{"-c:v copy"}, []string{"hevc_vaapi", "-vf"}, 1},
		{"no mastering metadata: the cpu tone-maps, the gpu encodes", on, all, func(p DeviceCopyPlan) DeviceCopyPlan {
			p.HEVC, p.HDRNoMastering = true, true
			return p
		}, nil, []string{"-c:v hevc_vaapi", "zscale"}, []string{"tonemap_vaapi", "-hwaccel "}, 1},
		{"the gpu fails: software h264", on, all, func(p DeviceCopyPlan) DeviceCopyPlan { p.HEVC = true; return p }, errors.New("boom"),
			[]string{"-c:v libx264", "zscale"}, []string{"hvc1", "vaapi"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			var runs [][]string
			c2 := &DeviceCopies{Dir: t.TempDir(), MinFreeBytes: -1, Accel: fakeAccel(c.cfg, c.st),
				Run: func(ctx context.Context, args []string, progress func(int64)) error {
					mu.Lock()
					runs = append(runs, args)
					n := len(runs)
					mu.Unlock()
					if n == 1 && c.fail != nil {
						return c.fail
					}
					return os.WriteFile(args[len(args)-1], []byte("x"), 0o644)
				}}
			defer c2.Close()
			pl := c.pl(hdr)
			k := testKey(1)
			k.HEVC = pl.HEVC
			st, err := c2.Request(t.Context(), 1, k, "/m/a.mkv", pl)
			if err != nil {
				t.Fatal(err)
			}
			waitState(t, c2, st.ID, CopyReady)
			mu.Lock()
			defer mu.Unlock()
			if len(runs) != c.runs {
				t.Fatalf("%d runs", len(runs))
			}
			j := strings.Join(runs[len(runs)-1], " ")
			for _, w := range c.want {
				if !strings.Contains(j, w) {
					t.Errorf("no %q in %s", w, j)
				}
			}
			for _, w := range c.not {
				if strings.Contains(j, w) {
					t.Errorf("%q in %s", w, j)
				}
			}
		})
	}
}

func TestDeviceCopiesHEVCOut(t *testing.T) {
	var none *DeviceCopies = &DeviceCopies{}
	if none.HEVCOut() {
		t.Fatal("no accel")
	}
	on := &DeviceCopies{Accel: fakeAccel(AccelConfig{Mode: HWQSV, HEVC: true}, HWStatus{Mode: HWQSV, HEVC: true})}
	off := &DeviceCopies{Accel: fakeAccel(AccelConfig{Mode: HWQSV}, HWStatus{Mode: HWQSV, HEVC: true})}
	sw := &DeviceCopies{Accel: fakeAccel(AccelConfig{Mode: HWNone, HEVC: true}, HWStatus{Mode: HWNone, HEVC: true})}
	if !on.HEVCOut() || off.HEVCOut() || sw.HEVCOut() {
		t.Fatal(on.HEVCOut(), off.HEVCOut(), sw.HEVCOut())
	}
}

// A real ffprobe tells an HDR10 encode with mastering metadata (in its SEI)
// from one without.
func TestProbeHDRMetaReal(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ff, err1 := exec.LookPath("ffmpeg")
	fp, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	base := "log-level=error:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc"
	meta := base + ":hdr10=1:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400"
	for name, params := range map[string]string{"meta.mkv": meta, "nometa.mkv": base} {
		out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=0.4",
			"-vf", "format=yuv420p10le", "-c:v", "libx265", "-x265-params", params, filepath.Join(dir, name)).CombinedOutput()
		if err != nil {
			t.Skipf("no libx265: %v %s", err, out)
		}
	}
	for name, want := range map[string]bool{"meta.mkv": true, "nometa.mkv": false} {
		r, err := Probe(t.Context(), fp, filepath.Join(dir, name))
		if err != nil || r.Video == nil || r.Video.HDRMastering == nil || *r.Video.HDRMastering != want ||
			*r.Video.HDRLightLevel != want || r.Video.NoHDRMastering() == want {
			t.Fatalf("%s: %+v %v", name, r.Video, err)
		}
	}
}
