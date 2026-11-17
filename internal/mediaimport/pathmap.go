package mediaimport

import (
	"github.com/antresol-app/antresol/internal/mediaimport/runkit"
)

// PathRule rewrites the prefix From to To; PathMap is a set of them, the
// longest matching From wins (runkit: PathMap.Map).
type (
	PathRule = runkit.PathRule
	PathMap  = runkit.PathMap
)

// ParsePathMap parses "from=to,from=to" (VAULT_IMPORT_PATHS). "" is an
// empty map.
var ParsePathMap = runkit.ParsePathMap
