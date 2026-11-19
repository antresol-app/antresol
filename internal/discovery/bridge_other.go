//go:build !linux

package discovery

import "errors"

// netLinks: only Linux runs the server in a container here.
func netLinks() ([]link, error) {
	return nil, errors.New("discovery: link kinds are read on Linux only")
}
