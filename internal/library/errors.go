package library

import "errors"

// ErrNotFound is returned when an item or file does not exist.
var ErrNotFound = errors.New("library: not found")
