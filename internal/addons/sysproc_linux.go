package addons

import "syscall"

// sysProcAttr puts the addon in its own process group (stopped as a
// group) and has the kernel send SIGTERM if the server dies. Pdeathsig
// follows the OS thread that forked; Go keeps such threads alive unless a
// goroutine exits while locked to one, which the server does not do.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
