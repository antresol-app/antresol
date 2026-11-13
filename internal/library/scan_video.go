package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/antresol-app/antresol/internal/release"
)

// Scanner indexes the media roots into Repo: video (movies, series) here,
// music, books, audiobooks and comics in ScanAll's list (scan_all.go).
// Local artwork found next to media (poster.jpg, fanart.jpg, ...) is stored
// in Item.Poster / Item.Backdrop as "file:<absolute path>". Roots are
// absolute; an empty root is not scanned.
type Scanner struct {
	Repo *Repo
	// MediaRoot: missing roots under it are created empty (createMissingRoots);
	// "" creates none.
	MediaRoot                         string
	Movies, Series                    string
	Music, Ebooks, Audiobooks, Comics string
	ArtDir                            string // extracted artwork (embedded covers): DataDir/images
	ComicsCache                       string // unpacked comic pages: DataDir/comics-cache

	// Chapters reads the chapters of an audiobook file (nil: none).
	Chapters func(ctx context.Context, path string) ([]ChapterInput, error)

	// StorageOffline reports why the media disk is unavailable ("" while it
	// is online; nil: never offline). An unmounted disk leaves empty mount
	// points whose walks succeed, so while it is offline no root is pruned.
	StorageOffline func() string

	// extraOverride replaces the built-in non-video scanners (tests only).
	extraOverride []scanFunc

	// mu serializes scans (periodic job, organize after a download): an
	// older walk must not prune rows a newer scan has just added.
	mu sync.Mutex
}

var videoExt = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".webm": true, ".ts": true, ".m2ts": true}

var (
	reSample    = regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}])sample([^\p{L}\p{N}]|$)`)
	reTrailer   = regexp.MustCompile(`(?i)[-._ ](trailer|teaser)$`)
	reSeasonDir = regexp.MustCompile(`(?i)^(?:(?:season|сезон)[\s._-]*|s)(\d{1,2})(?:[^\p{L}\p{N}].*)?$`)
	reSpecials  = regexp.MustCompile(`(?i)^(?:specials?|спецвыпуски?|спешлы|extras)$`)
)

var skipDirs = map[string]bool{
	"sample": true, "samples": true, "extras": true, "featurettes": true, "trailers": true,
	"behind the scenes": true, "@eadir": true, "subs": true, "subtitles": true,
}

type videoFile struct {
	path  string
	size  int64
	mtime int64
}

// walkVideos lists video files under root, skipping samples, extras and hidden entries.
func walkVideos(ctx context.Context, root string) ([]videoFile, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	var out []videoFile
	var firstErr error
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || skipDirs[strings.ToLower(name)]) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || !videoExt[strings.ToLower(filepath.Ext(name))] {
			return nil
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if reSample.MatchString(base) || reTrailer.MatchString(base) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		out = append(out, videoFile{p, info.Size(), info.ModTime().Unix()})
		return nil
	})
	if err == nil {
		err = firstErr // incomplete walk: the caller must not prune
	}
	return out, err
}

// dirIndex caches case-insensitive directory listings.
type dirIndex map[string]map[string]string

func (di dirIndex) find(dir string, names ...string) string {
	m, ok := di[dir]
	if !ok {
		m = map[string]string{}
		if es, err := os.ReadDir(dir); err == nil {
			for _, e := range es {
				m[strings.ToLower(e.Name())] = e.Name()
			}
		}
		di[dir] = m
	}
	for _, n := range names {
		if real, ok := m[strings.ToLower(n)]; ok {
			return filepath.Join(dir, real)
		}
	}
	return ""
}

var imgExts = []string{".jpg", ".jpeg", ".png", ".webp"}

func withExts(stem string) []string {
	out := make([]string, len(imgExts))
	for i, e := range imgExts {
		out[i] = stem + e
	}
	return out
}

func fileURL(p string) string {
	if p == "" {
		return ""
	}
	return "file:" + p
}

func (di dirIndex) art(dir string, stems ...string) string {
	var names []string
	for _, s := range stems {
		names = append(names, withExts(s)...)
	}
	return fileURL(di.find(dir, names...))
}

// ScanVideo indexes both video roots. It is idempotent and prunes files
// that disappeared. A root that cannot be read completely is never pruned.
func (s *Scanner) ScanVideo(ctx context.Context) (ScanStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st ScanStats
	err := s.scanVideo(ctx, &st)
	return st, err
}

// scanVideo is ScanVideo without the lock (the caller holds s.mu).
func (s *Scanner) scanVideo(ctx context.Context, st *ScanStats) error {
	var errs []error
	if s.Movies != "" {
		if err := s.scanMovies(ctx, st); err != nil {
			errs = append(errs, fmt.Errorf("movies: %w", err))
		}
	}
	if s.Series != "" {
		if err := s.scanSeries(ctx, st); err != nil {
			errs = append(errs, fmt.Errorf("series: %w", err))
		}
	}
	return errors.Join(errs...)
}

// finish prunes what the walk of root did not see — unless the media disk
// is offline (an empty mount point would hide the whole library).
func (s *Scanner) finish(ctx context.Context, root string, seen map[string]bool, st *ScanStats) error {
	if s.StorageOffline != nil && s.StorageOffline() != "" {
		return nil
	}
	n, err := s.Repo.removeMissing(ctx, root, seen)
	st.Removed += n
	return err
}

var rePart = regexp.MustCompile(`(?i)[\s._-]*(?:cd|disc|disk|part|pt)[\s._-]?(\d{1,2})$`)

var reFolderYear = regexp.MustCompile(`\((?:19|20)\d\d\)`)

type movieGroup struct {
	dir   string
	base  string // file base without part suffix
	files []videoFile
}

func (s *Scanner) scanMovies(ctx context.Context, st *ScanStats) error {
	root, err := filepath.Abs(s.Movies)
	if err != nil {
		return err
	}
	files, walkErr := walkVideos(ctx, root)
	if walkErr != nil && files == nil {
		return walkErr
	}
	// Group multi-part files (… - cd1.mkv, … - cd2.mkv) into one movie.
	var groups []*movieGroup
	byKey := map[string]*movieGroup{}
	perDir := map[string]int{}
	for _, f := range files {
		dir := filepath.Dir(f.path)
		base := strings.TrimSuffix(filepath.Base(f.path), filepath.Ext(f.path))
		if m := rePart.FindStringIndex(base); m != nil && m[0] > 0 {
			base = base[:m[0]]
		}
		key := dir + "\x00" + strings.ToLower(base)
		g := byKey[key]
		if g == nil {
			g = &movieGroup{dir: dir, base: base}
			byKey[key] = g
			groups = append(groups, g)
			perDir[dir]++
		}
		g.files = append(g.files, f)
	}
	di := dirIndex{}
	seen := map[string]bool{}
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		sort.Slice(g.files, func(i, j int) bool { return g.files[i].path < g.files[j].path })
		dir, base := g.dir, g.base
		single := perDir[dir] == 1 && dir != root

		info := release.Parse(base)
		title, year := info.Title, info.Year
		var alts []string
		if info.AltTitle != "" {
			alts = append(alts, info.AltTitle)
		}
		if dir != root {
			dname := filepath.Base(dir)
			d := release.Parse(dname)
			switch {
			case single && d.Title != "" && (title == "" || reFolderYear.MatchString(dname)):
				// the "Title (Year)/…" layout: folder wins over the file name.
				if title != "" {
					alts = append(alts, title)
				}
				title = d.Title
				if d.Year != 0 {
					year = d.Year
				}
				if d.AltTitle != "" {
					alts = append(alts, d.AltTitle)
				}
			case title == "":
				title = d.Title
				if year == 0 {
					year = d.Year
				}
			case single && d.Title != "" && release.Normalize(d.Title) != release.Normalize(title):
				alts = append(alts, d.Title)
				if year == 0 {
					year = d.Year
				}
			}
		}
		in := MovieInput{Title: title, Year: year, AltTitles: alts}
		for _, f := range g.files {
			in.Files = append(in.Files, FileInput{Path: f.path, Size: f.size, Mtime: f.mtime})
		}
		nfoPath := di.find(dir, base+".nfo")
		if nfoPath == "" {
			nfoPath = di.find(dir, strings.TrimSuffix(filepath.Base(g.files[0].path), filepath.Ext(g.files[0].path))+".nfo")
		}
		if nfoPath == "" && single {
			nfoPath = di.find(dir, "movie.nfo")
		}
		if nfoPath != "" {
			if n, ok := readNFO(nfoPath); ok {
				if n.Title != "" {
					if in.Title != "" {
						in.AltTitles = append(in.AltTitles, in.Title)
					}
					in.Title = n.Title
				}
				if n.Year != 0 {
					in.Year = n.Year
				}
				in.OriginalTitle, in.Overview, in.Rating, in.TmdbID, in.ExtIDs = n.Original, n.Plot, n.Rating, n.TmdbID, n.ExtIDs
				in.Genres = n.Genres
			}
		}
		for _, f := range g.files {
			seen[f.path] = true
		}
		if in.Title == "" {
			st.Skipped += len(g.files)
			continue
		}
		in.Poster = di.art(dir, base+"-poster", base+".poster")
		in.Backdrop = di.art(dir, base+"-fanart", base+"-backdrop", base+"-background")
		if single {
			if in.Poster == "" {
				in.Poster = di.art(dir, "poster", "folder", "cover")
			}
			if in.Backdrop == "" {
				in.Backdrop = di.art(dir, "fanart", "backdrop", "background")
			}
		}
		if _, err := s.Repo.UpsertMovie(ctx, in); err != nil {
			return err
		}
		st.Movies++
		st.Files += len(g.files)
	}
	if walkErr != nil {
		return walkErr // incomplete walk: no pruning
	}
	return s.finish(ctx, root, seen, st)
}

type seriesMeta struct {
	title, original, rating string
	year                    int
	tmdb                    int64
	ext                     map[string]string
	overview                string
	genres                  []string
	alts                    []string
	poster, backdrop        string
}

func (s *Scanner) scanSeries(ctx context.Context, st *ScanStats) error {
	root, err := filepath.Abs(s.Series)
	if err != nil {
		return err
	}
	files, walkErr := walkVideos(ctx, root)
	if walkErr != nil && files == nil {
		return walkErr
	}
	di := dirIndex{}
	metas := map[string]*seriesMeta{} // series dir -> meta
	seen := map[string]bool{}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		seen[f.path] = true
		dir := filepath.Dir(f.path)
		base := strings.TrimSuffix(filepath.Base(f.path), filepath.Ext(f.path))
		finfo := release.Parse(base)

		// Series directory: the nearest ancestor with tvshow.nfo, else the first
		// directory below root, else none (loose file in the root).
		sdir := ""
		for d := dir; d != root && strings.HasPrefix(d, root); d = filepath.Dir(d) {
			if di.find(d, "tvshow.nfo") != "" {
				sdir = d
				break
			}
		}
		if sdir == "" && dir != root {
			rel, _ := filepath.Rel(root, f.path)
			sdir = filepath.Join(root, strings.Split(rel, string(filepath.Separator))[0])
		}

		var sm *seriesMeta
		if sdir != "" {
			sm = metas[sdir]
		}
		if sm == nil {
			sm = &seriesMeta{}
			if sdir != "" {
				d := release.Parse(filepath.Base(sdir))
				if !isSeasonName(d.Title) {
					sm.title, sm.year = d.Title, d.Year
					if d.AltTitle != "" {
						sm.alts = append(sm.alts, d.AltTitle)
					}
				}
				if p := di.find(sdir, "tvshow.nfo"); p != "" {
					if n, ok := readNFO(p); ok {
						if n.Title != "" {
							if sm.title != "" {
								sm.alts = append(sm.alts, sm.title)
							}
							sm.title = n.Title
						}
						if n.Year != 0 {
							sm.year = n.Year
						}
						sm.original, sm.rating, sm.tmdb, sm.ext = n.Original, n.Rating, n.TmdbID, n.ExtIDs
						sm.overview, sm.genres = n.Plot, n.Genres
					}
				}
				sm.poster = di.art(sdir, "poster", "folder", "cover")
				sm.backdrop = di.art(sdir, "fanart", "backdrop", "background")
				metas[sdir] = sm
			}
			if sm.title == "" {
				sm.title, sm.year = finfo.Title, finfo.Year // loose file or season-named dir
				if finfo.AltTitle != "" {
					sm.alts = append(sm.alts, finfo.AltTitle)
				}
			}
		}
		if sm.title == "" {
			st.Skipped++
			continue
		}

		var enfo NFO
		if p := di.find(dir, base+".nfo"); p != "" {
			enfo, _ = readNFO(p)
		}
		season, ep, etitle := resolveEpisode(base, sm.title, finfo, enfo, root, dir)
		if ep == 0 {
			st.Skipped++
			continue
		}

		_, err := s.Repo.UpsertEpisode(ctx, EpisodeInput{
			SeriesTitle: sm.title, SeriesOriginal: sm.original, SeriesYear: sm.year, SeriesTmdbID: sm.tmdb,
			SeriesRating: sm.rating, SeriesPoster: sm.poster, SeriesBackdrop: sm.backdrop, SeriesAlt: sm.alts,
			SeriesExtIDs: sm.ext, SeriesOverview: sm.overview, SeriesGenres: sm.genres,
			Season: season, Episode: ep, Title: etitle, Overview: enfo.Plot,
			File: FileInput{Path: f.path, Size: f.size, Mtime: f.mtime},
		})
		if err != nil {
			return err
		}
		st.Episodes++
		st.Files++
	}
	if walkErr != nil {
		return walkErr
	}
	return s.finish(ctx, root, seen, st)
}

var (
	reHasSE       = regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}])(s\d{1,2}[ ._]?e\d|\d{1,2}x\d{2})`)
	reEOnly       = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])e(\d{1,3})(?:[^\p{L}\p{N}]|$)`)
	reEpisodeWrd  = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:episode|ep|серия)[\s._-]*(\d{1,3})(?:[^\p{L}\p{N}]|$)`)
	reLeadNum     = regexp.MustCompile(`^\s*(\d{1,3})[\s._-]+(.+)$`)
	reSEWithName  = regexp.MustCompile(`(?i)s\d{1,2}[ ._]?e\d{1,3}(?:-?e\d{1,3})?\s+-\s+(.+)$`)
	reQualityTail = regexp.MustCompile(`(?i)\s+(?:web[- ]?dl|web[- ]?rip|webdl|blu-?ray|bdrip|bdremux|hdtv|dvdrip|dvd|remux|\d{3,4}p|hdr)\b.*$`)
)

// hasSE reports whether the name carries an explicit season marker (SxxEyy / NxNN).
func hasSE(name string) bool { return reHasSE.MatchString(name) }

func cleanTitle(s string) string {
	s = reQualityTail.ReplaceAllString(s, "")
	return strings.Trim(strings.TrimSpace(s), " -._")
}

// resolveEpisode determines season, episode and episode title. Priority:
// episode nfo, SxxEyy in the name, "E05", "Episode 3", "01. Title", and the
// number following the series title ("BBC.Planet.Earth.7.Great.Plains").
// Season falls back to a "Season N" directory, then 1.
func resolveEpisode(base, series string, finfo release.Info, enfo NFO, root, dir string) (season, ep int, title string) {
	seasonSet := false
	if enfo.Episode > 0 {
		ep, title = enfo.Episode, enfo.Title
		if enfo.Season > 0 {
			season, seasonSet = enfo.Season, true
		}
	}
	if title == "" {
		title = enfo.Title
	}
	if ep == 0 && finfo.Episode > 0 {
		ep = finfo.Episode
		if hasSE(base) || finfo.Season > 0 {
			season, seasonSet = finfo.Season, true
		}
	}
	if ep == 0 {
		if m := reEOnly.FindStringSubmatch(base); m != nil {
			ep, _ = strconv.Atoi(m[1])
		} else if m := reEpisodeWrd.FindStringSubmatch(base); m != nil {
			ep, _ = strconv.Atoi(m[1])
		} else if m := reLeadNum.FindStringSubmatch(base); m != nil {
			ep, _ = strconv.Atoi(m[1])
			if title == "" {
				title = cleanTitle(m[2])
			}
		} else if n := numberAfterTitle(base, series); n > 0 {
			ep = n
		}
	}
	if title == "" {
		if m := reSEWithName.FindStringSubmatch(base); m != nil {
			title = cleanTitle(m[1])
		}
	}
	if !seasonSet {
		if n, ok := seasonFromDirs(root, dir); ok {
			season = n
		} else if enfo.Season > 0 {
			season = enfo.Season
		} else {
			season = 1
		}
	}
	return season, ep, title
}

func tokens(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out = append(out, release.Normalize(f))
	}
	return out
}

// numberAfterTitle finds the series title inside the file name and returns the
// 1-3 digit number right after it, or 0.
func numberAfterTitle(base, series string) int {
	ft, st := tokens(base), tokens(series)
	if len(st) == 0 {
		return 0
	}
	for i := 0; i+len(st) < len(ft); i++ {
		match := true
		for j := range st {
			if ft[i+j] != st[j] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		next := ft[i+len(st)]
		if len(next) <= 3 {
			if n, err := strconv.Atoi(next); err == nil {
				return n
			}
		}
	}
	return 0
}

func isSeasonName(s string) bool {
	s = strings.TrimSpace(s)
	return reSeasonDir.MatchString(s) || reSpecials.MatchString(s)
}

// seasonFromDirs looks for "Season N" / "Сезон N" / "Specials" between root and dir, deepest first.
func seasonFromDirs(root, dir string) (int, bool) {
	for d := dir; d != root && strings.HasPrefix(d, root); d = filepath.Dir(d) {
		name := filepath.Base(d)
		if reSpecials.MatchString(name) {
			return 0, true
		}
		if m := reSeasonDir.FindStringSubmatch(name); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n, true
		}
		if n := release.Parse(name).Season; n > 0 {
			return n, true
		}
	}
	return 0, false
}
