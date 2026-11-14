package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSuggestWords(t *testing.T) {
	cases := []struct {
		text string
		want Rules
		name string
	}{
		{"создай канал который будет транслировать советские фильмы", Rules{Kinds: []string{"movie"}, Countries: []string{"SU"}}, "Советские фильмы"},
		{"комедии 90-х", Rules{Genres: []string{"комедия"}, YearFrom: 1990, YearTo: 1999}, "Комедии 90-х"},
		{"Французские мелодрамы 1960–1975", Rules{Countries: []string{"FR"}, Genres: []string{"мелодрама"}, YearFrom: 1960, YearTo: 1975}, "Французские мелодрамы 1960–1975"},
		{"сериалы про космос", Rules{Kinds: []string{"series"}, Keywords: []string{"space"}}, "Сериалы про космос"},
		{"канал с фантастикой после 2000", Rules{Genres: []string{"фантастика", "нф и фэнтези"}, YearFrom: 2000}, "Канал с фантастикой после 2000"},
		{"Japanese anime from the 1980s", Rules{Countries: []string{"JP"}, Genres: []string{"мультфильм"}, YearFrom: 1980, YearTo: 1989}, "Japanese anime from the 1980s"},
		{"создай канал с комедиями", Rules{Genres: []string{"комедия"}}, "Канал с комедиями"},
		{"чехословацкие сказки", Rules{Countries: []string{"XC"}}, "Чехословацкие сказки"},
	}
	for _, c := range cases {
		got := suggestWords(c.text)
		if !reflect.DeepEqual(got.Rules, c.want) {
			t.Errorf("%q: %+v, want %+v", c.text, got.Rules, c.want)
		}
		if n := nameOf(c.text); n != c.name {
			t.Errorf("%q: name %q, want %q", c.text, n, c.name)
		}
	}
}

func TestSuggestWithModel(t *testing.T) {
	// a small model that says Russia for «советские» and adds a theme
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/api/chat" || body["think"] != false {
			http.Error(w, "bad", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": `{"name":"Советская фантастика","kinds":["movie"],"genres":["фантастика","unknown"],"countries":["RU"],"yearFrom":0,"yearTo":0,"keywords":["space","nothing-here"]}`}})
	}))
	defer srv.Close()
	s := &Service{LLM: &LLM{URL: srv.URL, Model: "m"}}
	vocab := Vocabulary{Genres: map[string]bool{"фантастика": true, "комедия": true}, Countries: map[string]bool{"SU": true, "RU": true}, Keywords: map[string]bool{"space": true}}
	got := s.Suggest(context.Background(), "советские фильмы про космос", vocab)
	want := Suggestion{Name: "Советская фантастика", Via: "ai", Rules: Rules{Kinds: []string{"movie"}, Genres: []string{"фантастика"}, Countries: []string{"SU"}, Keywords: []string{"space"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	// the model down: the words alone
	srv.Close()
	got = s.Suggest(context.Background(), "советские фильмы", vocab)
	if got.Via != "words" || !reflect.DeepEqual(got.Rules.Countries, []string{"SU"}) || got.Name != "Советские фильмы" {
		t.Errorf("fallback %+v", got)
	}
}
