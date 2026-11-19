package media

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
)

// The kinds of HWProblem: what an administrator does about a GPU that
// failed its test encode.
const (
	// ProblemNoDevice: the render node is not in the container (devices:)
	ProblemNoDevice = "noDevice"
	// ProblemPermission: the node is there but the server's user may not
	// open it: it is not in the group that owns the node (GID)
	ProblemPermission = "permission"
	// ProblemDriver: the node opens but no VA-API / Quick Sync driver
	// takes it (Driver: the one the vendor needs)
	ProblemDriver = "driver"
	// ProblemNVIDIA: ffmpeg finds no NVIDIA driver libraries (the NVIDIA
	// Container Toolkit, NVIDIA_DRIVER_CAPABILITIES with video)
	ProblemNVIDIA = "nvidia"
	// ProblemEncoder: the driver works but refuses the encoder (Encoder)
	ProblemEncoder = "encoder"
	// ProblemTimeout: the test encode did not finish
	ProblemTimeout = "timeout"
	ProblemUnknown = "unknown"
)

// HWProblem is why the GPU of Mode failed its test encode: Kind (one of
// the Problem* kinds) with what its advice needs, and ffmpeg's own words
// (Detail) for the settings page and the log.
type HWProblem struct {
	Kind    string `json:"kind"`
	Mode    string `json:"mode"`
	Device  string `json:"device,omitempty"`
	GID     int    `json:"gid,omitempty"`
	Driver  string `json:"driver,omitempty"`
	Encoder string `json:"encoder,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func (p *HWProblem) String() string {
	var hint string
	switch p.Kind {
	case ProblemNoDevice:
		hint = fmt.Sprintf("%s is not in the container: pass /dev/dri (devices:)", p.Device)
	case ProblemPermission:
		hint = fmt.Sprintf("no permission to open %s: add its group %d to group_add", p.Device, p.GID)
	case ProblemDriver:
		hint = "no VA-API driver takes the GPU"
		if p.Driver != "" {
			hint += " (it needs " + p.Driver + ")"
		}
	case ProblemNVIDIA:
		hint = "no NVIDIA driver in the container: the NVIDIA Container Toolkit with NVIDIA_DRIVER_CAPABILITIES=compute,video,utility"
	case ProblemEncoder:
		hint = "the driver refuses " + p.Encoder
	case ProblemTimeout:
		hint = "the test encode timed out"
	default:
		hint = "unknown error"
	}
	return fmt.Sprintf("%s: %s; ffmpeg: %s", p.Mode, hint, p.Detail)
}

// diagnose classifies the failed test encode of mode (ffmpeg's error
// detail). ffmpeg says the same «No VA display found» for a node it may
// not open and for one no driver takes, so the node is opened here first:
// that tells permission from driver.
func (o *HWOptions) diagnose(mode, detail, dev string) *HWProblem {
	p := &HWProblem{Kind: ProblemUnknown, Mode: mode, Encoder: h264Encoder[mode], Detail: detail}
	if mode == HWQSV || mode == HWVAAPI {
		p.Device = dev
		if !o.exist(dev) {
			p.Kind = ProblemNoDevice
			return p
		}
		if gid, err := o.access(dev); errors.Is(err, fs.ErrPermission) {
			p.Kind, p.GID = ProblemPermission, gid
			return p
		}
	}
	d := strings.ToLower(detail)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(d, strings.ToLower(w)) {
				return true
			}
		}
		return false
	}
	switch {
	case has("timed out"):
		p.Kind = ProblemTimeout
	case mode == HWNVENC && has("cannot load libcuda", "cannot load libnvidia-encode", "no nvenc capable devices",
		"cuda_error_no_device", "cuinit", "required nvenc api version"):
		p.Kind = ProblemNVIDIA
	case has("no va display", "vainitialize failed", "va_opendriver() returns -1", "failed to initialise vaapi",
		"error creating a mfx session", "failed to create a vaapi device", "unknown libva error", "libva error"):
		p.Kind = ProblemDriver
		p.Driver = vaDriverFor(o.read("/sys/class/drm/" + baseName(dev) + "/device/vendor"))
	case has("no usable encoding entrypoint", "error while opening encoder", "not supported", "unsupported",
		"openencodesessionex failed", "failed to create encode pipeline", "invalid argument"):
		p.Kind = ProblemEncoder
	}
	return p
}

// vaDriverFor is the VA-API driver a GPU of this PCI vendor needs (the
// image has all three on amd64; Intel's only there).
func vaDriverFor(vendor string) string {
	switch vendor {
	case "0x8086":
		return "iHD (intel-media-va-driver), i965 before Broadwell"
	case "0x1002":
		return "radeonsi (mesa-va-drivers)"
	}
	return ""
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// openDevice opens a render node read-write, as libva does, and says
// which group owns it (-1: unknown).
func openDevice(path string) (int, error) {
	gid := -1
	if fi, err := os.Stat(path); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			gid = int(st.Gid)
		}
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return gid, err
	}
	return gid, f.Close()
}
