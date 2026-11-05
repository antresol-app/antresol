package i18n

import (
	"context"
	"testing"
)

func TestGenreFollowsTheViewer(t *testing.T) {
	en, ru := WithLanguage(context.Background(), EN), WithLanguage(context.Background(), RU)
	for _, c := range []struct {
		ctx      context.Context
		in, want string
	}{
		{en, "драма", "Drama"},
		{en, "Мультфильм", "Animation"},
		{en, "фантастика", "Science Fiction"},
		{en, "НФ и Фэнтези", "Sci-Fi & Fantasy"},
		{en, "Drama", "Drama"},
		{ru, "Drama", "драма"},
		{ru, "Sci-Fi & Fantasy", "НФ и Фэнтези"},
		{ru, "мелодрама", "мелодрама"},
		{en, "дарк-эмбиент", "дарк-эмбиент"}, // not a TMDB genre: as stored
	} {
		if got := Genre(c.ctx, c.in); got != c.want {
			t.Errorf("Genre(%s, %q) = %q, want %q", FromContext(c.ctx), c.in, got, c.want)
		}
	}
	if got := Genres(en, []string{"драма", "комедия"}); len(got) != 2 || got[0] != "Drama" || got[1] != "Comedy" {
		t.Fatal(got)
	}
	if Genres(en, nil) != nil {
		t.Fatal("nil stays nil")
	}
}
