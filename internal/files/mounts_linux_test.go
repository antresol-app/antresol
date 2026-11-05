//go:build linux

package files

import "testing"

// The real mount table of this process: the root is in it and holds /proc.
func TestSystemMounts(t *testing.T) {
	points, err := systemMounts()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range points {
		found = found || p == "/"
	}
	if !found {
		t.Fatalf("no / in %q", points)
	}
	if m, ok := systemMountOf("/proc/self"); !ok || m == "" {
		t.Fatalf("/proc/self: %q %v", m, ok)
	}
}
