//go:build !sources

package files

import (
	"context"

	"github.com/antresol-app/antresol/internal/config"
)

// acquisitionRoots: the core build has only the library's roots.
func acquisitionRoots(config.Config) []Root { return nil }

// seedWarningText: nothing in the core build holds files open for
// others (Seeds stays nil), so file operations never warn.
func seedWarningText(context.Context, string) string { return "" }

// keptWarningText: likewise, nothing keeps running after a delete.
func keptWarningText(context.Context, string) string { return "" }
