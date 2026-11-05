package metadata

import "testing"

func TestCatalogPoster(t *testing.T) {
	for p, want := range map[string]string{
		"https://image.tmdb.org/t/p/w342/a.jpg":       "https://image.tmdb.org/t/p/w342/a.jpg",
		"https://covers.openlibrary.org/b/id/1-M.jpg": "https://covers.openlibrary.org/b/id/1-M.jpg",
		"https://coverartarchive.org/release/x/f.jpg": "https://coverartarchive.org/release/x/f.jpg",
		"/api/images/0123abcd.jpg":                    "/api/images/0123abcd.jpg",
		"  /api/images/0123abcd.jpg ":                 "/api/images/0123abcd.jpg",
		"/api/images/../secret":                       "",
		"/api/images/a/b":                             "",
		"/api/images/":                                "",
		"http://image.tmdb.org/t/p/a.jpg":             "",
		"https://evil.example/x.jpg":                  "",
		"https://user@image.tmdb.org/a.jpg":           "",
		"https://image.tmdb.org:8443/a.jpg":           "",
		"":                                            "",
	} {
		if got := CatalogPoster(p); got != want {
			t.Errorf("%q: %q, want %q", p, got, want)
		}
	}
}
