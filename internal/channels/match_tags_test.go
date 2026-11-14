package channels

import "testing"

func TestMatchCountriesLanguagesKeywords(t *testing.T) {
	units := []Unit{
		{ID: 1, Kind: "movie", Year: 1975, Countries: []string{"SU"}, OrigLang: "ru", Keywords: []string{"space"}},
		{ID: 2, Kind: "movie", Year: 1999, Countries: []string{"FR", "IT"}, OrigLang: "fr"},
		{ID: 3, Kind: "series", Year: 2010, Countries: []string{"US"}, OrigLang: "en", Keywords: []string{"space", "robot"}},
		{ID: 4, Kind: "movie", Year: 1980}, // not enriched yet
	}
	ids := func(r Rules) (out []int64) {
		for _, u := range Match(units, r) {
			out = append(out, u.ID)
		}
		return out
	}
	eq := func(name string, got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: %v, want %v", name, got, want)
			}
		}
	}
	eq("soviet", ids(Rules{Countries: []string{"su"}}), 1)
	eq("france or us", ids(Rules{Countries: []string{"FR", "US"}}), 2, 3)
	eq("french original", ids(Rules{Languages: []string{"FR"}}), 2)
	eq("space", ids(Rules{Keywords: []string{"Space"}}), 1, 3)
	eq("space movies of the 70s", ids(Rules{Keywords: []string{"space"}, Kinds: []string{"movie"}, YearFrom: 1970, YearTo: 1979}), 1)
	eq("no filter", ids(Rules{}), 1, 2, 3, 4)
}
