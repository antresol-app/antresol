//go:build !linux

package files

import "errors"

// systemMounts: no mountinfo outside Linux (macOS dev builds); every path
// then uses the trash under MediaRoot.
func systemMounts() ([]string, error) { return nil, errors.ErrUnsupported }
