//go:build !linux

package addons

import "syscall"

// sysProcAttr puts the addon in its own process group, stopped as a
// group by Stop and StopAll. No parent-death signal outside Linux.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
