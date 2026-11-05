package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// TMDBRequests: titles, ratings and covers from TMDB; an unknown id is
// errUnknownTitle, a failing TMDB any other error.
func TestTMDBRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/3/movie/603/release_dates"):
			w.Write([]byte(`{"results":[{"iso_3166_1":"RU","release_dates":[{"certification":"16"}]}]}`))
		case strings.HasPrefix(r.URL.Path, "/3/movie/603"):
			w.Write([]byte(`{"id":603,"title":"Матрица","release_date":"1999-03-30","poster_path":"/m.jpg","adult":false,
				"release_dates":{"results":[{"iso_3166_1":"RU","release_dates":[{"certification":"16"}]}]}}`))
		case strings.HasPrefix(r.URL.Path, "/3/movie/404"):
			http.NotFound(w, r)
		default:
			http.Error(w, "down", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	cat := TMDBRequests(&tmdb.Client{Key: "k", BaseURL: srv.URL + "/3"}, nil)
	ctx := context.Background()
	got, err := cat.RequestTitle(ctx, "movie", 603)
	if err != nil || got.Title != "Матрица" || got.Year != 1999 || got.Rating != "16+" || got.Adult || !strings.HasSuffix(got.Poster, "/w342/m.jpg") {
		t.Fatalf("%+v %v", got, err)
	}
	if rating, err := cat.TitleRating(ctx, "movie", 603); err != nil || rating != "16+" {
		t.Errorf("rating %q %v", rating, err)
	}
	if _, err := cat.RequestTitle(ctx, "movie", 404); !errors.Is(err, errUnknownTitle) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := cat.RequestTitle(ctx, "movie", 500); err == nil || errors.Is(err, errUnknownTitle) {
		t.Errorf("down: %v", err)
	}
	if _, err := cat.RequestTitle(ctx, "music", 1); !errors.Is(err, errUnknownTitle) {
		t.Errorf("music: %v", err)
	}
	if p := cat.RequestPoster("music", "x", "https://evil.example/a.jpg"); p != "" {
		t.Errorf("poster %q", p)
	}
	if p := cat.RequestPoster("book", "", "https://covers.openlibrary.org/b/id/1-M.jpg"); p == "" {
		t.Error("catalog cover dropped")
	}
}
