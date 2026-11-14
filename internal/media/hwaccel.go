package media

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// The hardware modes of video transcodes (VAULT_HWACCEL; "auto" resolves
// to one of them).
const (
	HWNone         = "none"
	HWQSV          = "qsv"          // Intel Quick Sync (oneVPL/libmfx)
	HWVAAPI        = "vaapi"        // VA-API on a DRM render node: Intel or AMD
	HWNVENC        = "nvenc"        // NVIDIA: NVDEC decode, NVENC encode
	HWVideoToolbox = "videotoolbox" // macOS
)

// DefaultRenderNode is the DRM render node used when VAULT_HWACCEL_DEVICE
// is not set and none is found under /dev/dri.
const DefaultRenderNode = "/dev/dri/renderD128"

// ValidHW reports whether s is a VAULT_HWACCEL value.
func ValidHW(s string) bool {
	switch s {
	case "auto", HWNone, HWQSV, HWVAAPI, HWNVENC, HWVideoToolbox:
		return true
	}
	return false
}

// IsHW reports whether hw is a hardware mode (its transcodes retry in
// software when the hardware refuses).
func IsHW(hw string) bool {
	return hw != "" && hw != HWNone
}

// RenderNodes are the DRM render nodes of this machine, sorted.
func RenderNodes() []string {
	m, _ := filepath.Glob("/dev/dri/renderD*")
	sort.Strings(m)
	return m
}

// The resolved device options shared by every command line (ResolveHW sets
// them).
var hwOpts = struct {
	sync.RWMutex
	device   string          // the VA-API render node
	lowPower map[string]bool // encoders that open only in low-power (VDEnc) mode
}{device: DefaultRenderNode}

func vaapiDevice() string {
	hwOpts.RLock()
	defer hwOpts.RUnlock()
	return hwOpts.device
}

// encLowPower: the encoder (h264_vaapi, hevc_vaapi, hevc_qsv) needs -low_power 1.
func encLowPower(enc string) bool {
	hwOpts.RLock()
	defer hwOpts.RUnlock()
	return hwOpts.lowPower[enc]
}

func vaapiLowPower() bool { return encLowPower("h264_vaapi") }

func setHWOpts(device string, lowPower map[string]bool) {
	hwOpts.Lock()
	defer hwOpts.Unlock()
	if device == "" {
		device = DefaultRenderNode
	}
	hwOpts.device, hwOpts.lowPower = device, lowPower
}

// HWProbe is one test encode of ResolveHW.
type HWProbe struct {
	Mode  string `json:"mode"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// HWStatus is what ResolveHW found: Mode is what transcodes use; HEVC: its
// HEVC encoder works; TonemapGPU: its GPU tone-maps HDR (qsv, vaapi);
// TonemapCPU: ffmpeg tone-maps on the CPU (zscale).
type HWStatus struct {
	Mode       string    `json:"mode"`
	Requested  string    `json:"requested"`
	Device     string    `json:"device,omitempty"`
	HEVC       bool      `json:"hevc"`
	TonemapGPU bool      `json:"tonemapGpu"`
	TonemapCPU bool      `json:"tonemapCpu"`
	Probes     []HWProbe `json:"probes,omitempty"`
	ProbedAt   time.Time `json:"probedAt"`
	lowPower   map[string]bool
}

// HWOptions configure ResolveHW.
type HWOptions struct {
	FFmpeg  string // "": ffmpeg
	Mode    string // VAULT_HWACCEL ("" = auto)
	Device  string // VAULT_HWACCEL_DEVICE: a render node ("" = the first under /dev/dri)
	Timeout time.Duration
	// Fresh probes again instead of taking a cached result.
	Fresh bool

	// tests
	run   func(ctx context.Context, ffmpeg string, args []string) error
	goos  string
	exist func(path string) bool
	glob  func(pattern string) []string
	read  func(path string) string
}

var hwCache sync.Map // key → HWStatus

// ResolveHW turns VAULT_HWACCEL into the mode transcodes use. "auto" (or
// an unknown value) tries the machine's candidates with a tiny test encode
// each (NVIDIA when its device is there, QSV then VA-API for an Intel
// render node, VA-API for another, VideoToolbox on macOS) and takes the
// first that works, else software. An explicit mode is used as is; it is
// still test-encoded once, and a failure is logged (each transcode then
// falls back to software on its own). The chosen mode's HEVC encoder and
// HDR tone mapping are tried too. The result is cached per options.
func ResolveHW(ctx context.Context, o HWOptions) HWStatus {
	o.defaults()
	key := strings.Join([]string{o.FFmpeg, o.Mode, o.Device, o.goos}, "\x00")
	if o.run == nil { // tests bring their own runner and want no cache
		if v, ok := hwCache.Load(key); ok && !o.Fresh {
			st := v.(HWStatus)
			setHWOpts(st.Device, st.lowPower)
			return st
		}
	}
	o.withRunner()
	st := o.resolve(ctx)
	if o.run != nil && o.FFmpeg != "" {
		hwCache.Store(key, st)
	}
	return st
}

func (o *HWOptions) defaults() {
	if o.FFmpeg == "" {
		o.FFmpeg = "ffmpeg"
	}
	if o.Timeout <= 0 {
		o.Timeout = 20 * time.Second
	}
	if o.goos == "" {
		o.goos = runtime.GOOS
	}
	if o.exist == nil {
		o.exist = func(p string) bool { _, err := os.Stat(p); return err == nil }
	}
	if o.glob == nil {
		o.glob = func(p string) []string { m, _ := filepath.Glob(p); return m }
	}
	if o.read == nil {
		o.read = func(p string) string { b, _ := os.ReadFile(p); return strings.TrimSpace(string(b)) }
	}
	if !ValidHW(o.Mode) {
		if o.Mode != "" {
			log.Printf("media: VAULT_HWACCEL=%q is not a mode, using auto", o.Mode)
		}
		o.Mode = "auto"
	}
}

// withRunner: the real ffmpeg unless a test set one (then nothing is cached).
func (o *HWOptions) withRunner() {
	if o.run == nil {
		o.run = runFFmpegProbe
		return
	}
	o.FFmpeg = "" // a test's runner: no cache
}

func (o *HWOptions) resolve(ctx context.Context) HWStatus {
	dev := o.Device
	if dev == "" {
		dev = o.renderNode()
	}
	st := HWStatus{Mode: HWNone, Requested: o.Mode, ProbedAt: time.Now(), lowPower: map[string]bool{}}
	// the CPU's tone mapping serves every mode (and the software fallback)
	err := o.probe(ctx, "tonemap-cpu", tonemapProbeArgs("cpu", "", dev, false))
	st.Probes = append(st.Probes, probeResult("tonemap-cpu", err))
	st.TonemapCPU = err == nil
	if o.Mode == HWNone {
		setHWOpts(dev, st.lowPower)
		return st
	}
	cands := []string{o.Mode}
	if o.Mode == "auto" {
		cands = o.candidates(dev)
	}
	for _, c := range cands {
		if o.tryEncoder(ctx, &st, c, h264Encoder[c], dev) {
			st.Mode = c
			break
		}
	}
	if o.Mode != "auto" && st.Mode != o.Mode {
		// asked for explicitly: used anyway, sessions fall back one by one
		log.Printf("media: VAULT_HWACCEL=%s: the test encode failed (%s); using it anyway, transcodes fall back to software when it fails",
			o.Mode, lastError(st.Probes))
		st.Mode = o.Mode
	}
	if IsHW(st.Mode) {
		st.HEVC = o.tryEncoder(ctx, &st, st.Mode, hevcEncoder[st.Mode], dev)
		if st.Mode == HWQSV || st.Mode == HWVAAPI {
			err := o.probe(ctx, "tonemap-"+st.Mode, tonemapProbeArgs(st.Mode, h264Encoder[st.Mode], dev, st.lowPower[h264Encoder[st.Mode]]))
			if err != nil && strings.Contains(err.Error(), noMastering) {
				err = nil
			}
			st.Probes = append(st.Probes, probeResult("tonemap-"+st.Mode, err))
			st.TonemapGPU = err == nil
		}
	}
	if st.Mode == HWVAAPI || st.Mode == HWQSV {
		st.Device = dev
	}
	setHWOpts(dev, st.lowPower)
	return st
}

var h264Encoder = map[string]string{HWQSV: "h264_qsv", HWVAAPI: "h264_vaapi", HWNVENC: "h264_nvenc", HWVideoToolbox: "h264_videotoolbox"}
var hevcEncoder = map[string]string{HWQSV: "hevc_qsv", HWVAAPI: "hevc_vaapi", HWNVENC: "hevc_nvenc", HWVideoToolbox: "hevc_videotoolbox"}

// tryEncoder test-encodes with enc in mode; the VA-API and QSV encoders
// that need it are tried in low-power (VDEnc) mode too (some drivers open
// them only so, others never), and the mode that worked is remembered.
// h264_qsv always runs in low power (Alder Lake + iHD opens it only so).
func (o *HWOptions) tryEncoder(ctx context.Context, st *HWStatus, mode, enc, dev string) bool {
	variants := []bool{false}
	switch enc {
	case "h264_vaapi", "hevc_vaapi":
		variants = []bool{false, true}
	case "hevc_qsv":
		variants = []bool{true, false}
	}
	for _, lp := range variants {
		name := mode
		if enc != h264Encoder[mode] {
			name = enc
		}
		if lp && len(variants) > 1 {
			name += "-lp"
		}
		err := o.probe(ctx, name, probeArgs(mode, enc, dev, lp))
		st.Probes = append(st.Probes, probeResult(name, err))
		if err == nil {
			if lp && enc != "h264_qsv" {
				st.lowPower[enc] = true
			}
			return true
		}
	}
	return false
}

func lastError(p []HWProbe) string {
	for i := len(p) - 1; i >= 0; i-- {
		if !p[i].OK {
			return p[i].Error
		}
	}
	return ""
}

func probeResult(mode string, err error) HWProbe {
	if err != nil {
		return HWProbe{Mode: mode, Error: err.Error()}
	}
	return HWProbe{Mode: mode, OK: true}
}

// renderNode is the first DRM render node, else DefaultRenderNode.
func (o *HWOptions) renderNode() string {
	m := o.glob("/dev/dri/renderD*")
	sort.Strings(m)
	if len(m) > 0 {
		return m[0]
	}
	return DefaultRenderNode
}

// candidates are auto's modes in the order tried.
func (o *HWOptions) candidates(dev string) []string {
	if o.goos == "darwin" {
		return []string{HWVideoToolbox}
	}
	var c []string
	if o.exist("/dev/nvidiactl") || o.exist("/dev/nvidia0") {
		c = append(c, HWNVENC)
	}
	if o.exist(dev) {
		// QSV is Intel only; the PCI vendor says whose GPU the node is
		// (unknown: try it, the test encode decides)
		vendor := o.read("/sys/class/drm/" + filepath.Base(dev) + "/device/vendor")
		if vendor == "" || vendor == "0x8086" {
			c = append(c, HWQSV)
		}
		c = append(c, HWVAAPI)
	}
	return c
}

// probe runs one test encode with a timeout.
func (o *HWOptions) probe(ctx context.Context, mode string, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	err := o.run(ctx, o.FFmpeg, args)
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("%s: test encode timed out", mode)
	}
	return err
}

const probeSource = "testsrc2=size=640x360:rate=25:duration=0.5"

// probeArgs is a test encode of half a second of a generated picture with
// encoder enc of mode, through the filters of a transcode in that mode.
func probeArgs(mode, enc, dev string, lowPower bool) []string {
	a := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if mode == HWVAAPI {
		a = append(a, "-init_hw_device", "vaapi=va:"+dev, "-filter_hw_device", "va")
	}
	a = append(a, "-f", "lavfi", "-i", probeSource)
	switch mode {
	case HWQSV:
		a = append(a, "-vf", "format=nv12", "-c:v", enc)
		if enc == "h264_qsv" || lowPower {
			a = append(a, "-low_power", "1")
		}
	case HWVAAPI:
		a = append(a, "-vf", "format=nv12,hwupload,scale_vaapi=w=320:h=180:format=nv12", "-c:v", enc)
		if lowPower {
			a = append(a, "-low_power", "1")
		}
	default:
		a = append(a, "-vf", "format=yuv420p", "-c:v", enc)
	}
	return append(a, "-b:v", "1000000", "-frames:v", "10", "-f", "null", "-")
}

// tonemapProbeArgs tone-map a generated PQ picture the way a transcode
// does: on the GPU (qsv: vpp_qsv, vaapi: tonemap_vaapi) or the CPU (zscale).
func tonemapProbeArgs(mode, enc, dev string, lowPower bool) []string {
	a := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	src := "format=yuv420p10le,setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc"
	switch mode {
	case HWVAAPI:
		a = append(a, "-init_hw_device", "vaapi=va:"+dev, "-filter_hw_device", "va", "-f", "lavfi", "-i", probeSource,
			"-vf", strings.Replace(src, "yuv420p10le", "p010le", 1)+",hwupload,"+vaapiTonemap, "-c:v", enc)
		if lowPower {
			a = append(a, "-low_power", "1")
		}
	case HWQSV:
		a = append(a, "-init_hw_device", "vaapi=va:"+dev, "-init_hw_device", "qsv=qs@va", "-filter_hw_device", "qs",
			"-f", "lavfi", "-i", probeSource,
			"-vf", strings.Replace(src, "yuv420p10le", "p010le", 1)+",hwupload=extra_hw_frames=16,"+qsvTonemapChain(),
			"-c:v", enc, "-low_power", "1")
	default:
		a = append(a, "-f", "lavfi", "-i", probeSource, "-vf", src+","+cpuTonemap("smpte2084"), "-c:v", "rawvideo")
	}
	return append(a, "-frames:v", "10", "-f", "null", "-")
}

func qsvTonemapChain() string {
	return "hwmap=derive_device=vaapi," + vaapiTonemap + ",hwmap=derive_device=qsv,format=qsv"
}

// noMastering is tonemap_vaapi's refusal of a picture without HDR10
// mastering metadata: a generated test picture has none, so the probe
// reaching it means the driver tone-maps (its HDR capability is checked
// before the first frame).
const noMastering = "No mastering display data"

// vaapiTonemap is tonemap_vaapi to SDR BT.709 nv12.
const vaapiTonemap = "tonemap_vaapi=format=nv12:t=bt709:m=bt709:p=bt709"

// cpuTonemap is ffmpeg's tone mapping on the CPU, from the HDR transfer
// (PQ or HLG) of BT.2020 to SDR BT.709 (yuv420p).
func cpuTonemap(transfer string) string {
	if transfer == "" {
		transfer = "smpte2084"
	}
	return "zscale=tin=" + transfer + ":pin=bt2020:min=bt2020nc:t=linear:npl=100,format=gbrpf32le," +
		"zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"
}

func runFFmpegProbe(ctx context.Context, ffmpeg string, args []string) error {
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, noMastering) {
			return errors.New(noMastering)
		}
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		if msg == "" {
			return err
		}
		return errors.New(msg)
	}
	return nil
}

// HWName is a mode's name for people.
func HWName(mode string) string {
	switch mode {
	case HWQSV:
		return "Intel Quick Sync"
	case HWVAAPI:
		return "VA-API"
	case HWNVENC:
		return "NVIDIA NVENC"
	case HWVideoToolbox:
		return "VideoToolbox"
	}
	return "software"
}

// AccelConfig is how transcodes use the hardware: Mode and Device as
// VAULT_HWACCEL and VAULT_HWACCEL_DEVICE; Tonemap: HDR becomes SDR by tone
// mapping (else it plays pale); HEVC: a client that plays HEVC gets HEVC
// from a transcode.
type AccelConfig struct {
	Mode    string `json:"mode"`
	Device  string `json:"device"`
	Tonemap bool   `json:"tonemap"`
	HEVC    bool   `json:"hevc"`
}

// Accel holds the hardware settings in effect: Apply changes them at run
// time (new sessions take them; running ones keep theirs). The zero value
// is software without tone mapping; a nil *Accel too.
type Accel struct {
	FFmpeg string
	mu     sync.RWMutex
	cfg    AccelConfig
	st     HWStatus
	// Resolve overrides ResolveHW (tests)
	Resolve func(ctx context.Context, o HWOptions) HWStatus
}

// Apply puts cfg into effect (fresh: test-encode again even if cached).
func (a *Accel) Apply(ctx context.Context, cfg AccelConfig, fresh bool) HWStatus {
	res := a.Resolve
	if res == nil {
		res = ResolveHW
	}
	st := res(ctx, HWOptions{FFmpeg: a.FFmpeg, Mode: cfg.Mode, Device: cfg.Device, Fresh: fresh})
	a.mu.Lock()
	a.cfg, a.st = cfg, st
	a.mu.Unlock()
	return st
}

// State is the settings and what they resolved to.
func (a *Accel) State() (AccelConfig, HWStatus) {
	if a == nil {
		return AccelConfig{}, HWStatus{Mode: HWNone}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg, a.st
}

// Mode is the hardware mode of new transcodes.
func (a *Accel) Mode() string {
	_, st := a.State()
	if st.Mode == "" {
		return HWNone
	}
	return st.Mode
}

// Tonemap is how a transcode in hw tone-maps a source of HDR transfer
// transfer: "gpu", "cpu" or "" (an SDR source, tone mapping off or not
// available). gpu needs frames the GPU decodes (gpuFrames) and a PQ source
// (callers pass gpuFrames false for one without mastering-display
// metadata: tonemap_vaapi refuses it).
func (a *Accel) Tonemap(hw, transfer string, gpuFrames bool) string {
	cfg, st := a.State()
	if transfer == "" || !cfg.Tonemap {
		return ""
	}
	if (hw == HWQSV || hw == HWVAAPI) && hw == st.Mode && st.TonemapGPU && gpuFrames && transfer == "smpte2084" {
		return "gpu"
	}
	if st.TonemapCPU {
		return "cpu"
	}
	return ""
}

// HEVC: a transcode in hw encodes HEVC for a client that plays it.
func (a *Accel) HEVC(hw string) bool {
	cfg, st := a.State()
	return cfg.HEVC && st.HEVC && IsHW(hw) && hw == st.Mode
}

// plan sets d's Tonemap and OutHEVC for a transcode in hw (a copy: none).
func (a *Accel) plan(hw string, d Decision) Decision {
	d.Tonemap, d.OutHEVC = "", false
	if d.VideoCopy || a == nil {
		return d
	}
	gpuFrames := d.BurnSub == 0 && (hw == HWQSV && qsvDecodable(d.VideoCodec, d.SourcePixFmt) ||
		hw == HWVAAPI && vaapiDecodable(d.VideoCodec, d.SourcePixFmt))
	d.Tonemap = a.Tonemap(hw, d.HDRTransfer, gpuFrames && !d.HDRNoMastering)
	if d.HEVCClient && d.BurnSub == 0 && a.HEVC(hw) {
		d.OutHEVC, d.FMP4 = true, true // HEVC in HLS: fMP4 segments
	}
	return d
}
