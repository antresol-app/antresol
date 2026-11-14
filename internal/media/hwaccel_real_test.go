package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealHW encodes on this machine's GPU: VAULT_TEST_HW lists the modes
// (e.g. "auto,qsv,vaapi"); each must pass its test encode, then clips are
// transcoded to 720p HLS, a device copy and a live programme through the
// production command lines: H.264 for a client without HEVC, HEVC for one
// with it (when the mode's HEVC encoder passed its probe), and an HDR10
// clip tone-mapped to SDR BT.709 on the GPU (when it can) and on the CPU.
// Skipped without VAULT_TEST_HW.
func TestRealHW(t *testing.T) {
	modes := os.Getenv("VAULT_TEST_HW")
	if modes == "" {
		t.Skip("VAULT_TEST_HW not set")
	}
	ff := needFFmpeg(t)
	fp, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	dir := t.TempDir()
	clip := func(name string, enc ...string) string {
		out := filepath.Join(dir, name)
		args := append([]string{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=25",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "8"}, enc...)
		if b, err := exec.Command(ff, append(append(args, "-c:a", "ac3"), out)...).CombinedOutput(); err != nil {
			t.Fatalf("clip %s: %v %s", name, err, b)
		}
		return out
	}
	h264 := clip("h264.mkv", "-c:v", "libx264", "-pix_fmt", "yuv420p")
	hevc10 := clip("hevc10.mkv", "-c:v", "libx265", "-pix_fmt", "yuv420p10le", "-x265-params", "log-level=error")
	hdr10 := clip("hdr10.mkv", "-c:v", "libx265", "-pix_fmt", "yuv420p10le",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc",
		"-x265-params", "log-level=error:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:hdr10=1:"+
			"master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400")
	probe := func(t *testing.T, file string) string {
		b, err := exec.Command(fp, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name,height,color_transfer",
			"-of", "csv=p=0", file).Output()
		if err != nil {
			t.Fatalf("ffprobe %s: %v", file, err)
		}
		line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n") // a TS lists it per program too
		return strings.TrimSpace(line)
	}
	// the mean luma of the middle frame (an untouched PQ picture reads dim
	// and grey as SDR; a tone-mapped one is brighter)
	luma := func(t *testing.T, file string) string {
		b, _ := exec.Command(ff, "-hide_banner", "-i", file, "-vf", `select=eq(n\,10),signalstats,metadata=print:key=lavfi.signalstats.YAVG`,
			"-frames:v", "1", "-f", "null", "-").CombinedOutput()
		s := string(b)
		if i := strings.Index(s, "YAVG="); i >= 0 {
			v, _, _ := strings.Cut(s[i+5:], "\n")
			return strings.TrimSpace(v)
		}
		return "?"
	}
	run := func(t *testing.T, args []string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		start := time.Now()
		if b, err := exec.CommandContext(ctx, ff, args...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %s: %v\n%s", strings.Join(args, " "), err, b)
		}
		t.Logf("%.1fs: ffmpeg %s", time.Since(start).Seconds(), strings.Join(args, " "))
	}
	hls := func(t *testing.T, hw string, in string, d Decision) string {
		out := t.TempDir()
		run(t, BuildArgs(hw, in, d, 0, out))
		ext := "ts"
		if d.FMP4 {
			ext = "m4s"
		}
		segs, _ := filepath.Glob(filepath.Join(out, "seg*."+ext))
		if len(segs) < 2 {
			t.Fatalf("%d segments", len(segs))
		}
		if d.FMP4 {
			// a fragment needs its init segment
			cat := filepath.Join(out, "cat.mp4")
			init, _ := os.ReadFile(filepath.Join(out, "init.mp4"))
			seg, _ := os.ReadFile(segs[1])
			os.WriteFile(cat, append(init, seg...), 0o644)
			return cat
		}
		return segs[1]
	}
	for _, mode := range strings.Split(modes, ",") {
		t.Run(mode, func(t *testing.T) {
			accel := &Accel{FFmpeg: ff}
			st := accel.Apply(context.Background(), AccelConfig{Mode: mode, Device: os.Getenv("VAULT_HWACCEL_DEVICE"), Tonemap: true, HEVC: true}, true)
			t.Logf("resolved: mode=%s hevc=%v tonemapGpu=%v tonemapCpu=%v device=%s", st.Mode, st.HEVC, st.TonemapGPU, st.TonemapCPU, st.Device)
			for _, p := range st.Probes {
				t.Logf("  probe %s ok=%v %s", p.Mode, p.OK, p.Error)
			}
			if mode != "auto" && mode != HWNone && (len(st.Probes) < 2 || !st.Probes[1].OK) {
				t.Fatalf("test encode failed: %+v", st)
			}
			hw := st.Mode
			base := func(codec, pix string) Decision {
				return Decision{Mode: "transcode", VideoCodec: codec, SourcePixFmt: pix, SourceHeight: 1080, TargetHeight: 720, MaxBitrate: 4_000_000, AudioIndex: 1}
			}
			// H.264 for a client without HEVC, from both clips
			for in, d := range map[string]Decision{h264: base("h264", "yuv420p"), hevc10: base("hevc", "yuv420p10le")} {
				d = accel.plan(hw, d)
				if got := probe(t, hls(t, hw, in, d)); !strings.HasPrefix(got, "h264,720") {
					t.Fatalf("%s hls: %q", filepath.Base(in), got)
				}
				pl := DeviceCopyPlan{VideoCodec: d.VideoCodec, SourcePixFmt: d.SourcePixFmt, SourceHeight: 1080, TargetHeight: 720, MaxBitrate: 4_000_000, AudioIndex: 1}
				cp := filepath.Join(t.TempDir(), "copy.mp4")
				run(t, DeviceCopyArgs(hw, in, pl, cp))
				if got := probe(t, cp); !strings.HasPrefix(got, "h264,720") {
					t.Fatalf("%s copy: %q", filepath.Base(in), got)
				}
				ld := t.TempDir()
				li := LiveInput{Input: in, OutDir: ld, Playlist: "v.m3u8", VideoCodec: d.VideoCodec, PixFmt: d.SourcePixFmt,
					SrcWidth: 1920, SrcHeight: 1080, Width: 1280, Height: 720, Bitrate: 3_000_000, DurationSec: 8,
					Audio: []LiveAudio{{Index: LiveFirstAudio}}}
				run(t, LiveArgs(hw, li))
				ls, _ := filepath.Glob(filepath.Join(ld, "seg*.ts"))
				if len(ls) < 1 {
					t.Fatalf("%s live: no segments", filepath.Base(in))
				}
				if got := probe(t, ls[0]); !strings.HasPrefix(got, "h264,720") {
					t.Fatalf("%s live: %q", filepath.Base(in), got)
				}
			}
			// HEVC for a client that plays it
			d := base("hevc", "yuv420p10le")
			d.HEVCClient = true
			d = accel.plan(hw, d)
			want := "h264,720"
			if d.OutHEVC {
				want = "hevc,720"
			}
			if got := probe(t, hls(t, hw, hevc10, d)); !strings.HasPrefix(got, want) {
				t.Fatalf("hevc client: %q, want %s (plan hevc=%v)", got, want, d.OutHEVC)
			}
			t.Logf("hevc client: %s", want)
			// HDR10 to SDR: the plan's way, then forced onto the CPU, then none
			for _, way := range []string{"plan", "cpu", "off"} {
				d := base("hevc", "yuv420p10le")
				d.HDRTransfer, d.HEVCClient = "smpte2084", true
				d = accel.plan(hw, d)
				switch way {
				case "cpu":
					if !st.TonemapCPU {
						continue
					}
					d.Tonemap = "cpu"
				case "off":
					d.Tonemap = ""
				}
				seg := hls(t, hw, hdr10, d)
				got := probe(t, seg)
				t.Logf("hdr10 %s (tonemap=%q hevc=%v): %s, mean luma %s", way, d.Tonemap, d.OutHEVC, got, luma(t, seg))
				if d.Tonemap != "" && !strings.Contains(got, ",bt709") {
					t.Fatalf("hdr10 %s: %q, want bt709", way, got)
				}
			}
		})
	}
}
