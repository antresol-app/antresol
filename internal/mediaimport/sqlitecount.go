package mediaimport

import (
	"github.com/antresol-app/antresol/internal/mediaimport/runkit"
)

// TableCount counts the rows of Table matching Where ("" = all).
type TableCount = runkit.TableCount

// CountIn sums the counts over a snapshot of the database at path (a
// missing table, file or an empty file counts 0); Empty: no file, or one
// with nothing but blanks.
var (
	CountIn = runkit.CountIn
	Empty   = runkit.Empty
)
