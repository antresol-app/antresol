//go:build unix && !linux

package discovery

import "syscall"

const soReusePort = syscall.SO_REUSEPORT
