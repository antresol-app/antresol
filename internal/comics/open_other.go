//go:build !unix

package comics

import "os"

// openRegular opens a regular file for reading; anything else, a symlink
// included, is refused (checked before opening: no O_NOFOLLOW here).
func openRegular(p string) (*os.File, error) {
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errNotRegular
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	if st2, err := f.Stat(); err != nil || !os.SameFile(st, st2) {
		f.Close()
		return nil, errNotRegular
	}
	return f, nil
}

// openRegularIn is openRegular for a name inside root (os.Root: no
// component may leave it).
func openRegularIn(root *os.Root, name string) (*os.File, error) {
	st, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errNotRegular
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	if st2, err := f.Stat(); err != nil || !os.SameFile(st, st2) {
		f.Close()
		return nil, errNotRegular
	}
	return f, nil
}
