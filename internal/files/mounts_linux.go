//go:build linux

package files

import "os"

// systemMounts lists the mount points of this process's mount namespace.
func systemMounts() ([]string, error) {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return parseMountinfo(string(b)), nil
}
