package bookmeta

import "testing"

func TestISBN13(t *testing.T) {
	for in, want := range map[string]string{
		"9780441172719":     "9780441172719",
		"978-0-441-17271-9": "9780441172719",
		"0441172717":        "9780441172719", // the ISBN-10 of the same book
		"0-441-17271-7":     "9780441172719",
		"080442957X":        "9780804429573", // an X check digit
		"080442957x":        "9780804429573",
		"":                  "",
		"9780441172710":     "", // a wrong check digit
		"0441172718":        "",
		"not an isbn":       "",
	} {
		if got := ISBN13(in); got != want {
			t.Errorf("ISBN13(%q) = %q, want %q", in, got, want)
		}
	}
}
