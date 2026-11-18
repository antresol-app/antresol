package tmdb

import (
	"context"
	"strconv"
	"strings"
)

// DiscoverQuery narrows TMDB's /discover/movie: any of GenreIDs (none:
// every genre), released within YearFrom…YearTo (0: open), with at least
// MinVotes votes, by popularity.
type DiscoverQuery struct {
	GenreIDs         []int
	YearFrom, YearTo int
	MinVotes         int
	Page             int // from 1
}

// DiscoverResult is a title /discover lists.
type DiscoverResult struct {
	ID       int
	Title    string
	Year     int
	Overview string
	Poster   string // TMDB paths ("" = none)
	Backdrop string
	GenreIDs []int
}

// DiscoverMovies lists movies of q in the lookup language (no adult ones).
func (c *Client) DiscoverMovies(ctx context.Context, q DiscoverQuery) ([]DiscoverResult, error) {
	v := map[string][]string{"language": {c.language(ctx)}, "include_adult": {"false"}, "include_video": {"false"},
		"sort_by": {"popularity.desc"}, "page": {strconv.Itoa(max(q.Page, 1))}}
	if len(q.GenreIDs) > 0 {
		ids := make([]string, len(q.GenreIDs))
		for i, g := range q.GenreIDs {
			ids[i] = strconv.Itoa(g)
		}
		v["with_genres"] = []string{strings.Join(ids, "|")} // any of them
	}
	if q.YearFrom > 0 {
		v["primary_release_date.gte"] = []string{strconv.Itoa(q.YearFrom) + "-01-01"}
	}
	if q.YearTo > 0 {
		v["primary_release_date.lte"] = []string{strconv.Itoa(q.YearTo) + "-12-31"}
	}
	if q.MinVotes > 0 {
		v["vote_count.gte"] = []string{strconv.Itoa(q.MinVotes)}
	}
	var raw struct {
		Results []struct {
			ID           int    `json:"id"`
			Title        string `json:"title"`
			ReleaseDate  string `json:"release_date"`
			Overview     string `json:"overview"`
			PosterPath   string `json:"poster_path"`
			BackdropPath string `json:"backdrop_path"`
			GenreIDs     []int  `json:"genre_ids"`
			Adult        bool   `json:"adult"`
		} `json:"results"`
	}
	if err := c.get(ctx, "/discover/movie", v, &raw); err != nil {
		return nil, err
	}
	out := make([]DiscoverResult, 0, len(raw.Results))
	for _, r := range raw.Results {
		if r.Adult || r.ID <= 0 {
			continue
		}
		out = append(out, DiscoverResult{ID: r.ID, Title: r.Title, Year: yearOf(r.ReleaseDate), Overview: r.Overview,
			Poster: r.PosterPath, Backdrop: r.BackdropPath, GenreIDs: r.GenreIDs})
	}
	return out, nil
}
