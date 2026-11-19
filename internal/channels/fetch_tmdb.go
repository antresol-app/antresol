package channels

import (
	"context"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// TMDBFetchCatalog names channels' titles' programmes from TMDB: a movie
// is itself, a series its episodes that aired (specials left out), season
// by season.
type TMDBFetchCatalog struct {
	C   *tmdb.Client
	Now func() time.Time // test clock (nil: time.Now)
}

// Runtimes assumed when TMDB has none.
const (
	defaultMovieMinutes   = 110
	defaultEpisodeMinutes = 45
	// fetchSearchLimit: the movies and the series of an editor's search, each.
	fetchSearchLimit = 12
)

func (c TMDBFetchCatalog) Programmes(ctx context.Context, t FetchTitle) ([]FetchProgramme, error) {
	if t.Kind == library.KindMovie {
		d, err := c.C.Movie(ctx, t.TMDBID)
		if err != nil {
			return nil, err
		}
		return []FetchProgramme{{Kind: library.KindMovie, Title: titleOr(d), Year: d.Year, Overview: d.Overview, Rating: d.Rating,
			Poster: d.Poster, RuntimeMs: minutes(d.Runtime, defaultMovieMinutes)}}, nil
	}
	d, err := c.C.TV(ctx, t.TMDBID)
	if err != nil {
		return nil, err
	}
	today := time.Now
	if c.Now != nil {
		today = c.Now
	}
	day := today().UTC().Format("2006-01-02")
	var out []FetchProgramme
	for _, sc := range d.Seasons {
		if sc.Number <= 0 || (t.Season > 0 && sc.Number != t.Season) {
			continue
		}
		se, err := c.C.Season(ctx, t.TMDBID, sc.Number)
		if err != nil {
			return nil, err
		}
		for _, e := range se.Episodes {
			if e.AirDate == "" || e.AirDate > day {
				continue // not out yet: nothing to fetch
			}
			out = append(out, FetchProgramme{Kind: library.KindEpisode, Season: sc.Number, Episode: e.Number, Title: titleOr(d),
				EpisodeTitle: e.Name, Year: d.Year, Overview: e.Overview, Rating: d.Rating, Poster: d.Poster,
				RuntimeMs: minutes(e.Runtime, minutesOr(d.Runtime, defaultEpisodeMinutes))})
		}
	}
	return out, nil
}

func titleOr(d tmdb.Details) string {
	if d.Title != "" {
		return d.Title
	}
	return d.OriginalTitle
}

func minutesOr(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func minutes(v, def int) int64 { return int64(minutesOr(v, def)) * 60000 }

// Search finds movies and series for the editor, interleaved, adult
// titles dropped; a series lists its seasons.
func (c TMDBFetchCatalog) Search(ctx context.Context, q string) ([]FetchFound, error) {
	var movies, shows []tmdb.Result
	var merr, terr error
	var wg sync.WaitGroup
	wg.Go(func() { movies, merr = c.C.SearchMovie(ctx, q, 0) })
	wg.Go(func() { shows, terr = c.C.SearchTV(ctx, q, 0) })
	wg.Wait()
	if merr != nil && terr != nil {
		return nil, merr
	}
	out := []FetchFound{}
	add := func(kind string, r tmdb.Result) {
		if r.Adult {
			return
		}
		f := FetchFound{Kind: kind, TMDBID: r.ID, Title: r.Title, Year: r.Year}
		if f.Title == "" {
			f.Title = r.OriginalTitle
		}
		if r.Poster != "" {
			f.Poster = c.C.ImageURL("w185", r.Poster)
		}
		out = append(out, f)
	}
	for i := 0; i < fetchSearchLimit && (i < len(movies) || i < len(shows)); i++ {
		if i < len(movies) {
			add(library.KindMovie, movies[i])
		}
		if i < len(shows) {
			add(library.KindSeries, shows[i])
		}
	}
	return out, nil
}

// Seasons are a series' seasons (specials left out), for the editor.
func (c TMDBFetchCatalog) Seasons(ctx context.Context, tmdbID int) ([]int, error) {
	d, err := c.C.TV(ctx, tmdbID)
	if err != nil {
		return nil, err
	}
	var out []int
	for _, s := range d.Seasons {
		if s.Number > 0 {
			out = append(out, s.Number)
		}
	}
	return out, nil
}
