package library

import "testing"

func TestRatingAllowedTable(t *testing.T) {
	cases := []struct {
		limit, kind, rating string
		want                bool
	}{
		{"", KindMovie, "", true},
		{"12+", KindMovie, "", false}, // video without a rating: hidden
		{"12+", KindMovie, "6+", true},
		{"12+", KindMovie, "16+", false},
		{"12+", KindArtist, "", true}, // music carries no rating: shown
		{"12+", KindArtist, "18+", false},
		{"12+", KindAlbum, "12+", true},
		{"12+", KindComic, "", true},
		{"12+", KindSeries, "weird", false},
		{"nonsense", KindArtist, "", true},
		{"nonsense", KindArtist, "6+", false}, // unknown limit: only unrated music
	}
	for _, c := range cases {
		if got := RatingAllowed(c.limit, Item{Kind: c.kind, Rating: c.rating}); got != c.want {
			t.Errorf("RatingAllowed(%q, %s %q) = %v", c.limit, c.kind, c.rating, got)
		}
	}
}
