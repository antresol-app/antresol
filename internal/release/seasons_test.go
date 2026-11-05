package release

import "testing"

// Golden values: recorded from the indexer's implementation before it
// moved here, not re-derived.
func TestSeasonRange(t *testing.T) {
	for _, c := range []struct {
		title  string
		lo, hi int
	}{
		{"Сёгун S01-S03 1080p", 1, 3},
		{"Тьма. Сезоны: 1-3", 1, 3},
		{"Lost S1-6E1-121", 1, 6},
		{"Suits S01E01-S09E10", 1, 9},
		{"Suits.S03.1080p", 3, 3},
		{"Фильм 2020", 0, 0},
	} {
		if lo, hi := SeasonRange(c.title); lo != c.lo || hi != c.hi {
			t.Errorf("SeasonRange(%q) = %d, %d; want %d, %d", c.title, lo, hi, c.lo, c.hi)
		}
	}
}
