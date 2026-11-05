package api

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// RequestCatalog is where kids' requests get what they name: the TMDB title
// of a movie or series, covers, and the library's own copy of a title.
// TMDBRequests is the core one; the sources build's (AcquireRequests) also
// knows the catalogs of albums and books.
type RequestCatalog interface {
	// RequestTitle is a TMDB movie or series (errUnknownTitle when TMDB has
	// none).
	RequestTitle(ctx context.Context, kind string, tmdbID int) (RequestTitle, error)
	// TitleRating is the age rating of a TMDB movie or series ("" = none).
	TitleRating(ctx context.Context, kind string, tmdbID int) (string, error)
	// RequestPoster is the cover a request keeps for the admins' browsers:
	// clientPoster when it is a catalog's or the server's own, else "".
	RequestPoster(kind, extID, clientPoster string) string
	// LibraryItemOf is the library item of a requested title.
	LibraryItemOf(ctx context.Context, kind string, tmdbID int, title, subtitle string) (int64, bool)
}

// RequestTitle is a TMDB title a kid asks for.
type RequestTitle struct {
	Title, Poster, Rating string
	Year                  int
	Adult                 bool
}

// errUnknownTitle: TMDB has no such movie or series.
var errUnknownTitle = errors.New("api: unknown title")

// requestTitleErr answers a failed title lookup of a request: 404 when TMDB
// has no such title, else 503 (TMDB did not answer).
func requestTitleErr(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return // the client went away
	}
	if errors.Is(err, errUnknownTitle) {
		writeErr(w, r, http.StatusNotFound, "Такого фильма или сериала нет в TMDB")
		return
	}
	log.Printf("api: request title: %v", err)
	writeErr(w, r, http.StatusServiceUnavailable, "TMDB сейчас недоступен. Попробуйте позже")
}

// liveTMDB is the request's context with TMDB lookups in the viewer's
// language (search results and title pages); what goes into the library
// keeps the metadata language.
func liveTMDB(r *http.Request) context.Context {
	return tmdb.WithLanguage(r.Context(), i18n.TMDB(i18n.FromContext(r.Context())))
}

// tmdbRequests is the RequestCatalog of the editions without a catalog of
// albums and books: movies and series from TMDB, the library's own lookups.
type tmdbRequests struct {
	TMDB    *tmdb.Client
	Library *library.Repo
}

// TMDBRequests is the RequestCatalog over TMDB and the library.
func TMDBRequests(t *tmdb.Client, lib *library.Repo) RequestCatalog {
	return tmdbRequests{TMDB: t, Library: lib}
}

func (c tmdbRequests) RequestTitle(ctx context.Context, kind string, tmdbID int) (RequestTitle, error) {
	var d tmdb.Details
	var err error
	switch kind {
	case library.KindMovie:
		d, err = c.TMDB.Movie(ctx, tmdbID)
	case library.KindSeries:
		d, err = c.TMDB.TV(ctx, tmdbID)
	default:
		return RequestTitle{}, errUnknownTitle
	}
	if errors.Is(err, tmdb.ErrNotFound) {
		return RequestTitle{}, errUnknownTitle
	}
	if err != nil {
		return RequestTitle{}, err
	}
	if d.Title == "" {
		d.Title = d.OriginalTitle
	}
	return RequestTitle{Title: d.Title, Poster: c.TMDB.ImageURL("w342", d.Poster), Rating: d.Rating, Year: d.Year, Adult: d.Adult}, nil
}

func (c tmdbRequests) TitleRating(ctx context.Context, kind string, tmdbID int) (string, error) {
	if (kind != library.KindMovie && kind != library.KindSeries) || tmdbID <= 0 {
		return "", errUnknownTitle
	}
	tk := "movie"
	if kind == library.KindSeries {
		tk = "tv"
	}
	return c.TMDB.Certification(ctx, tk, tmdbID)
}

func (c tmdbRequests) RequestPoster(_, _, clientPoster string) string {
	return metadata.CatalogPoster(clientPoster)
}

func (c tmdbRequests) LibraryItemOf(ctx context.Context, kind string, tmdbID int, title, subtitle string) (int64, bool) {
	return c.Library.FindTitleID(ctx, kind, tmdbID, title, subtitle)
}
