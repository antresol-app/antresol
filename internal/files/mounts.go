package files

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mount points decide where a path's trash is: a rename never crosses
// one (two bind mounts of the same disk are two mounts: EXDEV), so each
// mount gets its own .vault-trash (see Sandbox.TrashFor).

// MountResolver returns the mount point holding the (resolved) path; ok is
// false when it is not known (not Linux): the trash under MediaRoot is used.
type MountResolver func(path string) (mount string, ok bool)

// mountTable caches the system's mount points for a few seconds.
type mountTable struct {
	mu     sync.Mutex
	at     time.Time
	points []string
	err    error
}

var systemTable mountTable

const mountTTL = 10 * time.Second

// systemMountOf is the default MountResolver: the deepest mount point of
// /proc/self/mountinfo holding path.
func systemMountOf(path string) (string, bool) {
	t := &systemTable
	t.mu.Lock()
	if t.at.IsZero() || time.Since(t.at) > mountTTL {
		t.points, t.err = systemMounts()
		t.at = time.Now()
	}
	points, err := t.points, t.err
	t.mu.Unlock()
	if err != nil {
		return "", false
	}
	return deepestMount(points, path)
}

// deepestMount picks the longest of points holding path.
func deepestMount(points []string, path string) (string, bool) {
	path = filepath.Clean(path)
	best, found := "", false
	for _, p := range points {
		if (p == "/" || within(p, path)) && (!found || len(p) > len(best)) {
			best, found = p, true
		}
	}
	return best, found
}

// parseMountinfo reads the mount points (field 5) of /proc/self/mountinfo
// lines, undoing its octal escapes («\040» for a space).
func parseMountinfo(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		out = append(out, filepath.Clean(unescapeMount(f[4])))
	}
	return out
}

func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
