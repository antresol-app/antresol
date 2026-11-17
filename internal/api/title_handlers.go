package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/go-chi/chi/v5"
)

// TitleSource is the part of *tmdb.Client the title page uses.
type TitleSource interface {
	Movie(ctx context.Context, id int) (tmdb.Details, error)
	TV(ctx context.Context, id int) (tmdb.Details, error)
	ImageURL(size, p string) string
}

// TitleInfo is the page of a TMDB movie or series that is not in the
// library yet (any signed-in profile; kids as kidMaySee lets them). Its keys
// are a search result's (tmdbId … libraryItemId) and the title's own.
type TitleInfo struct {
	TMDBID   int    `json:"tmdbId"`
	ExtID    string `json:"extId"` // always "": a TMDB title
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"` // always ""
	Year     int    `json:"year"`
	Poster   string `json:"poster"`
	Overview string `json:"overview"`
	// InLibrary and LibraryItemID: the library's copy of the title (null:
	// none).
	InLibrary     bool   `json:"inLibrary"`
	LibraryItemID *int64 `json:"libraryItemId"`
	// OriginalTitle: when it differs from Title.
	OriginalTitle string   `json:"originalTitle,omitempty"`
	Backdrop      string   `json:"backdrop"`
	Genres        []string `json:"genres"`
	Runtime       int      `json:"runtime,omitempty"` // minutes
	Rating        string   `json:"rating,omitempty"`
	// IMDbID ("tt0816692"): its ratings come from /api/ratings/{imdbId}.
	IMDbID string `json:"imdbId,omitempty"`
	// Cast: the leading actors.
	Cast []CastMember `json:"cast"`
	// Seasons: a series' regular seasons (no specials, none empty).
	Seasons []SeasonCount `json:"seasons"`
	// Adult: TMDB marks it adult content (never shown to kids).
	Adult bool `json:"-"`
	// KidRequest: «Попросить» (kid profiles only): whether the title may be
	// asked for, and the kid's request of it.
	KidRequest *requestState `json:"kidRequest,omitempty"`
}

// SeasonCount is a season of a series on its title page.
type SeasonCount struct {
	Number   int `json:"number"`
	Episodes int `json:"episodes"`
}

func mountTitle(r chi.Router, h *lib) {
	r.Get("/api/title/{kind}/{tmdbId}", h.titlePage)
}

// GET /api/title/{kind}/{tmdbId} (kind movie | series) → TitleInfo. A kid
// gets it only while requests are open. Any rating-limited profile (a kid,
// or an adult with a limit) gets only a title it may see (its rating within
// the profile's limit, not adult); what it is denied carries nothing of
// the title.
func (h *lib) titlePage(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	id, err := strconv.Atoi(chi.URLParam(r, "tmdbId"))
	if (kind != library.KindMovie && kind != library.KindSeries) || err != nil || id <= 0 {
		writeErr(w, r, http.StatusNotFound, "Неизвестный фильм или сериал")
		return
	}
	if h.Titles == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "TMDB сейчас недоступен. Попробуйте позже")
		return
	}
	ctx := r.Context()
	u := userOf(r)
	if u.IsKid && (h.requests == nil || h.requests.open == nil || !h.requests.open(ctx)) {
		writeErr(w, r, http.StatusForbidden, "Недоступно в детском профиле")
		return
	}
	t, err := h.title(liveTMDB(r), kind, id)
	if errors.Is(err, tmdb.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Неизвестный фильм или сериал")
		return
	}
	if err != nil {
		if ctx.Err() != nil {
			return // the client went away
		}
		log.Printf("api: title %s %d: %v", kind, id, err)
		writeErr(w, r, http.StatusServiceUnavailable, "TMDB сейчас недоступен. Попробуйте позже")
		return
	}
	if u.IsKid {
		h.requests.ratings.put(kind, id, t.Rating, t.Adult)
	}
	if ratingLimit(u) != "" && !kidMaySee(ctx, u, t.Rating, t.Adult) {
		// over the limit, unrated or adult: nothing of it (title, poster) goes out
		writeErr(w, r, http.StatusForbidden, "Недоступно для этого профиля")
		return
	}
	if u.IsKid {
		t.KidRequest = h.requests.titleState(ctx, u, h.requests.mineByKey(ctx, u.ID), kind, id, "", t.Title, "", "")
	}
	writeJSON(w, http.StatusOK, t)
}

// title is TMDB's details of a movie or series with the library's copy.
func (h *lib) title(ctx context.Context, kind string, id int) (TitleInfo, error) {
	var d tmdb.Details
	var err error
	if kind == library.KindMovie {
		d, err = h.Titles.Movie(ctx, id)
	} else {
		d, err = h.Titles.TV(ctx, id)
	}
	if err != nil {
		return TitleInfo{}, err
	}
	if d.Title == "" {
		d.Title = d.OriginalTitle
	}
	t := TitleInfo{TMDBID: id, Kind: kind, Title: d.Title, Year: d.Year, Overview: d.Overview,
		Genres: nonNil(d.Genres), Runtime: d.Runtime, Rating: d.Rating, IMDbID: d.IMDbID, Adult: d.Adult,
		Cast: []CastMember{}, Seasons: []SeasonCount{}}
	if d.OriginalTitle != d.Title {
		t.OriginalTitle = d.OriginalTitle
	}
	for _, p := range d.Cast {
		c := CastMember{Name: p.Name, Character: p.Character}
		if p.Profile != "" {
			c.Photo = h.Titles.ImageURL("w185", p.Profile)
		}
		t.Cast = append(t.Cast, c)
	}
	for _, se := range d.Seasons {
		if se.Number > 0 && se.Episodes > 0 {
			t.Seasons = append(t.Seasons, SeasonCount{Number: se.Number, Episodes: se.Episodes})
		}
	}
	if d.Poster != "" {
		t.Poster = h.Titles.ImageURL("w342", d.Poster)
	}
	if d.Backdrop != "" {
		t.Backdrop = h.Titles.ImageURL("w1280", d.Backdrop)
	}
	if it, err := h.Library.ItemByTMDB(ctx, kind, int64(id)); err == nil {
		iid := it.ID
		t.InLibrary, t.LibraryItemID = true, &iid
	} else if !errors.Is(err, library.ErrNotFound) {
		log.Printf("api: title %s %d: library: %v", kind, id, err)
	}
	return t, nil
}
