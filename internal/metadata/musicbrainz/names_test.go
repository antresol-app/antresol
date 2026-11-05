package musicbrainz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata"
)

func TestSameName(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"Би-2", "Bi-2", true},
		{"Алиса", "Alisa", true},
		{"Игорь Тальков", "Igor Talkov", true},
		{"Вячеслав Быков", "Vyacheslav Bykov", true},
		{"Рок-острова", "Rok-Ostrova", true},
		{"Chernyi kofe", "Cherny Kofe", true}, // the alias, by consonants
		{"Чёрный кофе", "Cherny Kofe", true},
		{"JAY‐Z", "JAŸ-Z", true},
		{"Simon and Garfunkel", "Simon & Garfunkel", true},
		{"Алиса", "Alice", false},
		{"Луна", "Lana", false}, // two consonants: spelled alike only
		{"Kofe", "Cherny Kofe", false},
		{"Шура Би-2", "Bi-2", false},
		{"", "", false},
	} {
		if got := SameName(c.a, c.b); got != c.same {
			t.Errorf("SameName(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// The artist search answers Latin names with the Russian artist whose
// alias it is; same-named artists of a low score and the various-artists
// entry are left out.
func TestArtistsNamed(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("query")
		w.Write([]byte(`{"artists":[
			{"id":"bi2","name":"Би-2","sort-name":"Bi-2","score":100,"aliases":[{"name":"Bi2"}]},
			{"id":"shura","name":"Шура Би-2","sort-name":"Shura Bi-2","score":72},
			{"id":"other","name":"Bi-2","sort-name":"Bi-2","score":60},
			{"id":"` + variousArtists + `","name":"Bi-2","score":95}]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Limiter: &metadata.Limiter{}}
	as, err := c.ArtistsNamed(context.Background(), "Bi-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(as) != 1 || as[0] != (Artist{ID: "bi2", Name: "Би-2"}) || query != `Bi\-2` {
		t.Fatalf("artists %+v, query %q", as, query)
	}
}
