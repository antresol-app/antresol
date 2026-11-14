package tracking

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/antresol-app/antresol/internal/library"
)

// LibraryLookup is the part of *library.Repo the library scanner uses.
type LibraryLookup interface {
	ItemByTMDB(ctx context.Context, kind string, tmdbID int64) (library.Item, error)
	HaveEpisodes(ctx context.Context, tmdbID int64) (map[[2]int]bool, error)
}

type libraryScanner struct{ lib LibraryLookup }

// NewLibraryScanner is the Scanner of kind "library": the episodes (or the
// movie) with a file in the library; ref is the movie's or series' item id.
func NewLibraryScanner(l LibraryLookup) Scanner { return libraryScanner{lib: l} }

func (libraryScanner) Kind() string { return "library" }

func (s libraryScanner) Scan(ctx context.Context, t Title, _ []Episode) ([]Source, error) {
	kind := library.KindSeries
	if t.Kind == "movie" {
		kind = library.KindMovie
	}
	it, err := s.lib.ItemByTMDB(ctx, kind, t.TMDBID)
	if errors.Is(err, library.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ref := strconv.FormatInt(it.ID, 10)
	if kind == library.KindMovie {
		return []Source{{Kind: "library", Ref: ref}}, nil
	}
	have, err := s.lib.HaveEpisodes(ctx, t.TMDBID)
	if err != nil {
		return nil, err
	}
	out := make([]Source, 0, len(have))
	for k := range have {
		out = append(out, Source{Season: k[0], Episode: k[1], Kind: "library", Ref: ref})
	}
	slices.SortFunc(out, func(a, b Source) int {
		if a.Season != b.Season {
			return a.Season - b.Season
		}
		return a.Episode - b.Episode
	})
	return out, nil
}

// WatchedLibrary is the part of *library.Repo LibraryWatched uses.
type WatchedLibrary interface {
	ItemByTMDB(ctx context.Context, kind string, tmdbID int64) (library.Item, error)
	Children(ctx context.Context, id int64) ([]library.Item, error)
}

// WatchedProgress is the part of *progress.Store LibraryWatched uses.
type WatchedProgress interface {
	WatchedOf(ctx context.Context, userID int64, items, series []int64) (map[int64]bool, error)
}

// LibraryWatched is the WatchedFunc of the library's progress: the
// title's library episodes (or movie) the profile finished. A title not in
// the library has none.
func LibraryWatched(l WatchedLibrary, p WatchedProgress) WatchedFunc {
	return func(ctx context.Context, userID int64, t Title) (map[[2]int]bool, error) {
		kind := library.KindSeries
		if t.Kind == "movie" {
			kind = library.KindMovie
		}
		it, err := l.ItemByTMDB(ctx, kind, t.TMDBID)
		if errors.Is(err, library.ErrNotFound) {
			return map[[2]int]bool{}, nil
		}
		if err != nil {
			return nil, err
		}
		keys := map[int64][2]int{}
		if kind == library.KindMovie {
			keys[it.ID] = [2]int{0, 0}
		} else {
			seasons, err := l.Children(ctx, it.ID)
			if err != nil {
				return nil, err
			}
			for _, se := range seasons {
				if se.Kind != library.KindSeason {
					continue
				}
				eps, err := l.Children(ctx, se.ID)
				if err != nil {
					return nil, err
				}
				for _, e := range eps {
					if e.Kind == library.KindEpisode {
						keys[e.ID] = [2]int{se.SeasonNo, e.EpisodeNo}
					}
				}
			}
		}
		out := map[[2]int]bool{}
		if len(keys) == 0 {
			return out, nil
		}
		ids := make([]int64, 0, len(keys))
		for id := range keys {
			ids = append(ids, id)
		}
		done, err := p.WatchedOf(ctx, userID, ids, nil)
		if err != nil {
			return nil, err
		}
		for id, k := range keys {
			if done[id] {
				out[k] = true
			}
		}
		return out, nil
	}
}
