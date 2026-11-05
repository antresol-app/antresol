package media

import (
	"os"
	"runtime"
)

const qsvDevice = "/dev/dri/renderD128"

// DetectHW resolves the configured hardware mode. Explicit values
// ("qsv", "videotoolbox", "none") are returned as is; "auto" (or empty)
// probes the machine; unknown values are treated as "auto".
func DetectHW(cfg string) string {
	switch cfg {
	case "qsv", "videotoolbox", "none":
		return cfg
	}
	if _, err := os.Stat(qsvDevice); err == nil {
		return "qsv"
	}
	if runtime.GOOS == "darwin" {
		return "videotoolbox"
	}
	return "none"
}
