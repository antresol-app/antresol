//go:build !sources

package config

// LibraryEdition is true in the core build (built without -tags sources): the
// server only serves its own library, and the code that brings titles from
// outside is not compiled in.
const LibraryEdition = true

// Acquisition is empty in the core build.
type Acquisition struct{}

func (c *Config) loadAcquisition() error {
	c.LibraryOnly = true
	return nil
}
