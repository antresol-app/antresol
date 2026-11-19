package wikidata

import (
	"context"
	"slices"
	"testing"
)

func TestWorkNames(t *testing.T) {
	c := fixtureClient(t)
	ctx := context.Background()
	for _, k := range []struct {
		title, author, wantRU, wantAuthor string
	}{
		{"Crime and Punishment", "Fyodor Dostoevsky", "Преступление и наказание", "Фёдор Михайлович Достоевский"},
		// the item is "The Heart of a Dog"
		{"Heart of a Dog", "Mikhail Bulgakov", "Собачье сердце", ""},
		// written by the brothers as one author (P50 is the pair)
		{"Roadside Picnic", "Arkady Strugatsky", "Пикник на обочине", ""},
		// the American title is an alias of the British one's item
		{"Harry Potter and the Sorcerer's Stone", "J.K. Rowling", "Гарри Поттер и философский камень", ""},
		// Russian to English
		{"Мастер и Маргарита", "Михаил Булгаков", "Мастер и Маргарита", "Mikhail Bulgakov"},
	} {
		ws, err := c.WorkNames(ctx, k.title, k.author)
		if err != nil {
			t.Fatal(err)
		}
		var titles, authors []string
		for _, w := range ws {
			titles = append(titles, w.Titles...)
			authors = append(authors, w.Authors...)
		}
		if !slices.Contains(titles, k.wantRU) || (k.wantAuthor != "" && !slices.Contains(authors, k.wantAuthor)) {
			t.Errorf("%s: %q %q", k.title, titles, authors)
		}
	}
	// another book of the author's is not this one; no author, no lookup
	for _, k := range [][2]string{{"The Master and Margarita: A Critical Study", "Mikhail Bulgakov"}, {"War and Peace", ""}} {
		if ws, err := c.WorkNames(ctx, k[0], k[1]); err != nil || len(ws) != 0 {
			t.Errorf("%s: %+v %v", k[0], ws, err)
		}
	}
}
