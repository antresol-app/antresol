package tmdb

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCountriesAndKeywords(t *testing.T) {
	var movie, show rawDetails
	if err := json.Unmarshal([]byte(`{"production_countries":[{"iso_3166_1":"SU"},{"iso_3166_1":"fr"},{"iso_3166_1":"SU"}],
		"keywords":{"keywords":[{"name":"Space"},{"name":"space"},{"name":"moon"}]}}`), &movie); err != nil {
		t.Fatal(err)
	}
	if got := countriesOf(movie); !reflect.DeepEqual(got, []string{"SU", "FR"}) {
		t.Errorf("movie countries %v", got)
	}
	if err := json.Unmarshal([]byte(`{"origin_country":["US"],"production_countries":[{"iso_3166_1":"GB"},{"iso_3166_1":"US"}],
		"keywords":{"results":[{"name":"heist"}]}}`), &show); err != nil {
		t.Fatal(err)
	}
	if got := countriesOf(show); !reflect.DeepEqual(got, []string{"US", "GB"}) {
		t.Errorf("show countries %v", got)
	}
	if len(movie.KeywordsRaw.Keywords) != 3 || show.KeywordsRaw.Results[0].Name != "heist" {
		t.Errorf("keywords %+v %+v", movie.KeywordsRaw, show.KeywordsRaw)
	}
	if got := countriesOf(rawDetails{}); got == nil || len(got) != 0 {
		t.Errorf("none: %v", got)
	}
}
