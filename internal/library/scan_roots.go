package library

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// The kinds of library folders (Scanner.More, config.FolderKinds).
const (
	KindFolderMovies     = "movies"
	KindFolderSeries     = "series"
	KindFolderMusic      = "music"
	KindFolderEbooks     = "ebooks"
	KindFolderAudiobooks = "audiobooks"
	KindFolderComics     = "comics"
)

// Roots are a kind's folders: the first one (Movies…Comics), then More's,
// each once; none when the kind has no folder.
func (s *Scanner) Roots(kind string) []string {
	var first string
	switch kind {
	case KindFolderMovies:
		first = s.Movies
	case KindFolderSeries:
		first = s.Series
	case KindFolderMusic:
		first = s.Music
	case KindFolderEbooks:
		first = s.Ebooks
	case KindFolderAudiobooks:
		first = s.Audiobooks
	case KindFolderComics:
		first = s.Comics
	}
	var out []string
	seen := map[string]bool{}
	for _, r := range append([]string{first}, s.More[kind]...) {
		if r == "" || seen[filepath.Clean(r)] {
			continue
		}
		seen[filepath.Clean(r)] = true
		out = append(out, r)
	}
	return out
}

// AllRoots are every kind's folders (the default destinations first).
func (s *Scanner) AllRoots() []string {
	var out []string
	for _, k := range []string{KindFolderMovies, KindFolderSeries, KindFolderMusic, KindFolderEbooks, KindFolderAudiobooks, KindFolderComics} {
		out = append(out, s.Roots(k)...)
	}
	return out
}

// eachRoot runs one kind's scanner over each of its folders. A folder that
// fails does not stop the others: each walk prunes only under its own
// folder, and only when it read it completely. With several folders the
// errors name theirs.
func (s *Scanner) eachRoot(ctx context.Context, kind string, st *ScanStats, scan func(ctx context.Context, dir string, st *ScanStats) error) error {
	roots := s.Roots(kind)
	s.loadFlags()
	var errs []error
	for _, r := range roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := scan(ctx, r, st); err != nil {
			if len(roots) > 1 {
				err = fmt.Errorf("%s: %w", r, err)
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// loadFlags hands the folders' marks to the repository, which marks what
// the scan stores in them as it goes (a title from an adults-only folder is
// never shown to a kid between its upsert and the end of the scan).
func (s *Scanner) loadFlags() {
	if s.Repo == nil {
		return
	}
	var f FolderFlags
	if s.Flags != nil {
		f = s.Flags()
	}
	s.Repo.SetFolderFlags(f)
}

// syncFlags brings the marks of the whole library in line with the
// folders' (a folder that lost its mark, a file moved out of one).
func (s *Scanner) syncFlags(ctx context.Context) error {
	if s.Repo == nil {
		return nil
	}
	s.loadFlags()
	if err := s.Repo.SyncFolderFlags(ctx); err != nil {
		return fmt.Errorf("folder marks: %w", err)
	}
	return nil
}
