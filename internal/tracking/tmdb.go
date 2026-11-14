package tracking

import (
	"context"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// TMDB is the part of *tmdb.Client the skeleton uses.
type TMDB interface {
	TV(ctx context.Context, id int) (tmdb.Details, error)
	Season(ctx context.Context, tvID, n int) (tmdb.SeasonDetails, error)
	Movie(ctx context.Context, id int) (tmdb.Details, error)
}

// airingGap: a series whose last aired episode is this recent counts as
// airing even when TMDB lists no later one yet.
const airingGap = 14 * 24 * time.Hour

type tmdbSkeleton struct {
	c   TMDB
	now func() time.Time
}

// NewTMDBSkeleton is the Skeleton of TMDB: a series' episodes of every
// season but the specials, a movie as one episode (0, 0) on its release
// date; the status follows the air dates.
func NewTMDBSkeleton(c TMDB) Skeleton { return &tmdbSkeleton{c: c, now: time.Now} }

func (s *tmdbSkeleton) Episodes(ctx context.Context, t Title) ([]Episode, string, error) {
	eps, status, _, err := s.EpisodesInfo(ctx, t)
	return eps, status, err
}

// EpisodesInfo is Episodes with the title's name, poster and certification
// from the same details read.
func (s *tmdbSkeleton) EpisodesInfo(ctx context.Context, t Title) ([]Episode, string, TitleInfo, error) {
	now := s.now()
	if t.Kind == "movie" {
		d, err := s.c.Movie(ctx, int(t.TMDBID))
		if err != nil {
			return nil, "", TitleInfo{}, err
		}
		info := infoOf(d)
		eps := []Episode{{TitleID: t.ID, AirDate: d.ReleaseDate}}
		status := "unknown"
		if d.ReleaseDate != "" {
			status = "ended"
			if d.ReleaseDate > now.Format(time.DateOnly) {
				status = "upcoming"
			}
		}
		return eps, status, info, nil
	}
	d, err := s.c.TV(ctx, int(t.TMDBID))
	if err != nil {
		return nil, "", TitleInfo{}, err
	}
	var eps []Episode
	for _, sc := range d.Seasons {
		if sc.Number <= 0 {
			continue
		}
		sd, err := s.c.Season(ctx, int(t.TMDBID), sc.Number)
		if err != nil {
			return nil, "", TitleInfo{}, err
		}
		for _, e := range sd.Episodes {
			eps = append(eps, Episode{TitleID: t.ID, Season: sc.Number, Number: e.Number, Name: e.Name, AirDate: e.AirDate})
		}
	}
	return eps, seriesStatus(eps, now), infoOf(d), nil
}

func infoOf(d tmdb.Details) TitleInfo {
	return TitleInfo{Name: d.Title, Poster: d.Poster, Cert: library.NormalizeRating(d.Rating)}
}

// seriesStatus: upcoming when nothing dated has aired yet, airing when an
// episode has aired and another is dated later (or the last one aired
// within airingGap), ended when everything dated aired long ago, unknown
// with no dates at all.
func seriesStatus(eps []Episode, now time.Time) string {
	today := now.Format(time.DateOnly)
	var last string
	future := false
	for _, e := range eps {
		switch {
		case e.AirDate == "":
		case e.AirDate > today:
			future = true
		case e.AirDate > last:
			last = e.AirDate
		}
	}
	switch {
	case last == "" && future:
		return "upcoming"
	case last == "":
		return "unknown"
	case future:
		return "airing"
	}
	if d, err := time.Parse(time.DateOnly, last); err == nil && now.Sub(d) <= airingGap {
		return "airing"
	}
	return "ended"
}
