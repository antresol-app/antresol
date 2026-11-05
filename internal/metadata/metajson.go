package metadata

import (
	"encoding/json"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/antresol-app/antresol/internal/release"
)

// Helpers for enricher state in items.meta_json: stamps are RFC3339 UTC
// strings ("mb_at", "ol_miss", ...).

// ParseMeta decodes meta_json; a corrupt value is an error (never
// overwritten), an empty one is an empty map.
func ParseMeta(s string) (map[string]any, error) {
	m := map[string]any{}
	if strings.TrimSpace(s) != "" {
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, err
		}
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// MetaStr is a trimmed string value ("" if absent or not a string).
func MetaStr(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

// MetaTime parses an RFC3339 stamp (zero if absent or invalid).
func MetaTime(m map[string]any, k string) time.Time {
	t, _ := time.Parse(time.RFC3339, MetaStr(m, k))
	return t
}

// MetaInt is a JSON number value as int (0 if absent).
func MetaInt(m map[string]any, k string) int {
	f, _ := m[k].(float64)
	return int(f)
}

// MetaStrings is a string list value (a lone string counts as one).
func MetaStrings(m map[string]any, k string) []string {
	var out []string
	switch v := m[k].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case string:
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Stamp formats t as a meta_json stamp.
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Fresh reports whether stamp k was set less than retry (plus the item's
// jitter) before now.
func Fresh(m map[string]any, k string, now time.Time, retry time.Duration) bool {
	t := MetaTime(m, k)
	return !t.IsZero() && now.Sub(t) < retry
}

// Jitter spreads an item's weekly retries over two days, so the misses of
// one big first run do not all come due on the same day.
func Jitter(id int64) time.Duration {
	if id < 0 {
		id = -id
	}
	return time.Duration(id%48) * time.Hour
}

// NormTitle is release.Normalize of the NFC form: a name from a macOS
// (NFD) folder spells "й" as "и" + U+0306, which Normalize would turn
// into "и".
func NormTitle(s string) string { return release.Normalize(norm.NFC.String(s)) }
