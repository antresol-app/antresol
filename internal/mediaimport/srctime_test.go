package mediaimport

import (
	"testing"
	"time"
)

func TestSourceTimeLayouts(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"2026-10-01 19:30:00.1234567", time.Date(2026, 10, 1, 19, 30, 0, 123456700, time.UTC)},    // Jellyfin, Kavita (EF Core)
		{"2026-10-01 19:30:00", time.Date(2026, 10, 1, 19, 30, 0, 0, time.UTC)},                    // no fraction
		{"2026-10-01T19:30:00.1234567Z", time.Date(2026, 10, 1, 19, 30, 0, 123456700, time.UTC)},   // ISO with Z
		{"2026-10-01 21:30:00.123 +02:00", time.Date(2026, 10, 1, 19, 30, 0, 123000000, time.UTC)}, // ABS / Sequelize
		{"2026-10-01T21:30:00.5+02:00", time.Date(2026, 10, 1, 19, 30, 0, 500000000, time.UTC)},    // RFC 3339
		{"1790000000000", time.UnixMilli(1790000000000).UTC()},                                     // epoch ms
	}
	for _, c := range cases {
		got, ok := parseSourceTime(c.in)
		if !ok || !got.Equal(c.want) || got.Location() != time.UTC {
			t.Errorf("parseSourceTime(%q) = %v, %v; want %v (UTC)", c.in, got, ok, c.want)
		}
	}
	never := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, s := range []string{"", "вчера", "2026-13-45", "12abc"} {
		got, ok := parseSourceTime(s)
		if ok || !got.Equal(never) {
			t.Errorf("parseSourceTime(%q) = %v, %v; want 2000-01-01, false", s, got, ok)
		}
	}
}
