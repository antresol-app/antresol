//go:build unix

package comics

import (
	"os"
	"syscall"
)

// openRegular opens a regular file for reading without following a
// symlink (O_NOFOLLOW) and without blocking on a FIFO (O_NONBLOCK, no
// effect on regular files); anything but a regular file is refused.
func openRegular(p string) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, errNotRegular
	}
	return f, nil
}

// openRegularIn is openRegular for a name inside root (os.Root: no
// component may leave it, the last one is not followed).
func openRegularIn(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, errNotRegular
	}
	return f, nil
}
