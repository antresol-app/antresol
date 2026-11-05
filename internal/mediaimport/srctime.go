package mediaimport

import (
	"strconv"
	"strings"
	"time"
)

// neverTime is the time of a source record without a usable timestamp: it
// is older than anything Vault stores, so it never overwrites Vault.
var neverTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// sourceTimeLayouts are the timestamp forms of the old services, tried in
// order; a layout without a zone is read as UTC.
var sourceTimeLayouts = []string{
	"2006-01-02 15:04:05.9999999",    // Jellyfin, Kavita (EF Core)
	"2006-01-02T15:04:05.9999999Z",   // ISO with Z
	"2006-01-02 15:04:05.999 -07:00", // Audiobookshelf (Sequelize)
	time.RFC3339Nano,
}

// parseSourceTime reads a timestamp of Jellyfin, Kavita or Audiobookshelf
// (or an integer: epoch milliseconds) as UTC. What it cannot read is
// 2000-01-01T00:00:00Z and false.
func parseSourceTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range sourceTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), true
	}
	return neverTime, false
}
