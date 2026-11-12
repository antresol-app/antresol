package i18n

import (
	"context"
	"strings"
)

// tmdbGenres pairs TMDB's movie and TV genre names in Russian (as TMDB's
// ru-RU answers spell them) and English (en-US). The library stores genres
// in its metadata language; Genre shows them in the viewer's.
var tmdbGenres = [][2]string{
	{"боевик", "Action"},
	{"приключения", "Adventure"},
	{"мультфильм", "Animation"},
	{"комедия", "Comedy"},
	{"криминал", "Crime"},
	{"документальный", "Documentary"},
	{"драма", "Drama"},
	{"семейный", "Family"},
	{"фэнтези", "Fantasy"},
	{"история", "History"},
	{"ужасы", "Horror"},
	{"музыка", "Music"},
	{"детектив", "Mystery"},
	{"мелодрама", "Romance"},
	{"фантастика", "Science Fiction"},
	{"телевизионный фильм", "TV Movie"},
	{"триллер", "Thriller"},
	{"военный", "War"},
	{"вестерн", "Western"},
	// TV only
	{"боевик и приключения", "Action & Adventure"},
	{"детский", "Kids"},
	{"новости", "News"},
	{"реалити-шоу", "Reality"},
	{"НФ и Фэнтези", "Sci-Fi & Fantasy"},
	{"мыльная опера", "Soap"},
	{"ток-шоу", "Talk"},
	{"война и политика", "War & Politics"},
}

var genreIndex = func() map[string][2]string {
	m := make(map[string][2]string, 2*len(tmdbGenres))
	for _, g := range tmdbGenres {
		m[strings.ToLower(g[0])] = g
		m[strings.ToLower(g[1])] = g
	}
	return m
}()

// Genre is a TMDB genre name in the request's language, whichever language
// it was stored in; any other name (music tags, a folder's word) as is.
func Genre(ctx context.Context, name string) string {
	g, ok := genreIndex[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return name
	}
	if FromContext(ctx) == EN {
		return g[1]
	}
	return g[0]
}

// Genres maps Genre over a list (nil stays nil).
func Genres(ctx context.Context, names []string) []string {
	if names == nil {
		return nil
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = Genre(ctx, n)
	}
	return out
}

// GenreKey is the one spelling of a genre whichever language it is stored
// in (TMDB's Russian name, lower case): «Comedy», «комедия» and «Комедия»
// are one key. Any other name is its lower case.
func GenreKey(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if g, ok := genreIndex[n]; ok {
		return strings.ToLower(g[0])
	}
	return n
}
