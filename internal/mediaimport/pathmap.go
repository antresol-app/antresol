package mediaimport

import (
	"fmt"
	"path"
	"strings"
)

// PathRule rewrites the prefix From (a path of another service) to To (the
// same folder as Vault sees it).
type PathRule struct{ From, To string }

// PathMap is a set of prefix rewrites; the longest matching From wins.
type PathMap []PathRule

// ParsePathMap parses "from=to,from=to" (VAULT_IMPORT_PATHS). "" is an
// empty map.
func ParsePathMap(s string) (PathMap, error) {
	var m PathMap
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		from, to, ok := strings.Cut(part, "=")
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if !ok || from == "" || to == "" {
			return nil, fmt.Errorf("сопоставление путей %q: нужен вид «откуда=куда»", part)
		}
		m = append(m, PathRule{From: path.Clean(from), To: path.Clean(to)})
	}
	return m, nil
}

// Map rewrites p by the rule with the longest From that is p itself or a
// parent folder of it (whole path segments only); p is returned as is when
// no rule matches.
func (m PathMap) Map(p string) string {
	best := -1
	var rest string
	for i, r := range m {
		var tail string
		switch {
		case p == r.From:
		case r.From == "/" && strings.HasPrefix(p, "/"):
			tail = p[1:]
		case strings.HasPrefix(p, r.From+"/"):
			tail = p[len(r.From)+1:]
		default:
			continue
		}
		if best < 0 || len(r.From) > len(m[best].From) {
			best, rest = i, tail
		}
	}
	if best < 0 {
		return p
	}
	return path.Join(m[best].To, rest)
}
