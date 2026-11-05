//go:build !unix

package media

import (
	"errors"
	"os"
)

var errUnsupported = errors.New("media: not supported on this platform")

func pauseProcess(*os.Process) error  { return errUnsupported }
func resumeProcess(*os.Process) error { return errUnsupported }
func freeBytes(string) (int64, error) { return 0, errUnsupported }
