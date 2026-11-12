package channels

import "testing"

func TestDisplayNamesInViewerLanguage(t *testing.T) {
	for _, c := range []struct {
		ch         Channel
		lang, want string
	}{
		{Channel{Name: "Все сериалы", AutoKey: "series"}, "en", "All Series"},
		{Channel{Name: "Все фильмы", AutoKey: "movies"}, "en", "All Movies"},
		{Channel{Name: "Форс-мажоры", AutoKey: "series:12"}, "en", "Форс-мажоры"},
		{Channel{Name: "Кино 90-х", AutoKey: "decade:1990"}, "en", "90s Movies"},
		{Channel{Name: "Мой канал", AutoKey: "movies", Edited: true}, "en", "Мой канал"},
		{Channel{Name: "Все сериалы", AutoKey: "series"}, "ru", "Все сериалы"},
	} {
		if got, _ := Display(c.lang, c.ch); got != c.want {
			t.Errorf("%s in %s: %s, want %s", c.ch.AutoKey, c.lang, got, c.want)
		}
	}
}
