package tracking

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
)

// LibraryLookup is the part of *library.Repo the library scanner uses.
type LibraryLookup interface {
	ItemByTMDB(ctx context.Context, kind string, tmdbID int64) (library.Item, error)
	EpisodeFiles(ctx context.Context, tmdbID int64) (map[[2]int]library.EpisodeFile, error)
	FilesOf(ctx context.Context, itemID int64) ([]library.File, error)
}

type libraryScanner struct{ lib LibraryLookup }

// NewLibraryScanner is the Scanner of kind "library": the episodes (or the
// movie) with a file in the library. Ref is "item:<id>" of the episode (or
// movie) item, which Meta carries with the file to play; Quality is the
// file's resolution class.
func NewLibraryScanner(l LibraryLookup) Scanner { return libraryScanner{lib: l} }

func (libraryScanner) Kind() string { return "library" }

// libraryQuality is the label of a resolution class ("" unknown).
func libraryQuality(res int) string {
	switch res {
	case 2160, 1080, 720, 480:
		return strconv.Itoa(res) + "p"
	}
	return ""
}

func librarySource(season, episode int, c library.EpisodeFile) Source {
	return Source{Season: season, Episode: episode, Kind: "library", Ref: "item:" + strconv.FormatInt(c.ItemID, 10),
		Quality: libraryQuality(c.Resolution), Meta: SourceMeta{ItemID: c.ItemID, FileID: c.FileID}}
}

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
	if kind == library.KindMovie {
		files, err := s.lib.FilesOf(ctx, it.ID)
		if err != nil {
			return nil, err
		}
		best := library.EpisodeFile{ItemID: it.ID}
		for i, f := range files {
			if r := library.ResolutionOf(f); i == 0 || r > best.Resolution {
				best.FileID, best.Resolution = f.ID, r
			}
		}
		return []Source{librarySource(0, 0, best)}, nil
	}
	files, err := s.lib.EpisodeFiles(ctx, t.TMDBID)
	if err != nil {
		return nil, err
	}
	out := make([]Source, 0, len(files))
	for k, c := range files {
		out = append(out, librarySource(k[0], k[1], c))
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
		keys, err := libraryKeys(ctx, l, t.Kind, t.TMDBID)
		if err != nil {
			return nil, err
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

// libraryKeys maps the library episode items of a title (the movie itself:
// {0, 0}) to their {season, episode}; a title not in the library has none.
func libraryKeys(ctx context.Context, l WatchedLibrary, titleKind string, tmdbID int64) (map[int64][2]int, error) {
	kind := library.KindSeries
	if titleKind == "movie" {
		kind = library.KindMovie
	}
	it, err := l.ItemByTMDB(ctx, kind, tmdbID)
	if errors.Is(err, library.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	keys := map[int64][2]int{}
	if kind == library.KindMovie {
		keys[it.ID] = [2]int{0, 0}
		return keys, nil
	}
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
	return keys, nil
}

// WatchState is what one profile did with an episode (a movie: {0, 0}).
type WatchState struct {
	Watched     bool
	PositionSec int // where the unfinished viewing stopped, 0 when none
}

// WatchStateFunc reports a profile's WatchState per {season, episode} of a
// title. The episodes endpoint merges every function of
// Service.WatchStates: watched if any says so, the furthest position.
// (WatchedFunc, which the feed uses, says watched only.)
type WatchStateFunc func(ctx context.Context, userID int64, kind string, tmdbID int64) (map[[2]int]WatchState, error)

// StateProgress is the part of *progress.Store LibraryWatchState uses.
type StateProgress interface {
	Of(ctx context.Context, userIDs, itemIDs []int64) ([]progress.Entry, error)
}

// LibraryWatchState is the WatchStateFunc of the library's progress.
func LibraryWatchState(l WatchedLibrary, p StateProgress) WatchStateFunc {
	return func(ctx context.Context, userID int64, kind string, tmdbID int64) (map[[2]int]WatchState, error) {
		keys, err := libraryKeys(ctx, l, kind, tmdbID)
		if err != nil || len(keys) == 0 {
			return map[[2]int]WatchState{}, err
		}
		ids := make([]int64, 0, len(keys))
		for id := range keys {
			ids = append(ids, id)
		}
		entries, err := p.Of(ctx, []int64{userID}, ids)
		if err != nil {
			return nil, err
		}
		out := map[[2]int]WatchState{}
		for _, e := range entries {
			if e.UserID != userID {
				continue
			}
			st := WatchState{Watched: e.Finished}
			if !e.Finished {
				st.PositionSec = int(e.PositionMs / 1000)
			}
			out[keys[e.ItemID]] = st
		}
		return out, nil
	}
}

// MergeWatchStates runs the functions and merges per episode: watched if
// any says watched, position the largest.
func MergeWatchStates(ctx context.Context, fns []WatchStateFunc, userID int64, kind string, tmdbID int64) (map[[2]int]WatchState, error) {
	out := map[[2]int]WatchState{}
	for _, f := range fns {
		m, err := f(ctx, userID, kind, tmdbID)
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			cur := out[k]
			cur.Watched = cur.Watched || v.Watched
			cur.PositionSec = max(cur.PositionSec, v.PositionSec)
			out[k] = cur
		}
	}
	return out, nil
}
