package library

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	MediaRoot string
	// Movies…Comics are each kind's first folder, its default destination
	// (uploads, downloads); More are the kinds' further folders ("movies",
	// "series", "music", "ebooks", "audiobooks", "comics" → absolute
	// folders), scanned the same way. Roots lists them.
	Movies, Series                    string
	Music, Ebooks, Audiobooks, Comics string
	More                              map[string][]string
	// Flags are the folders marked «Только для взрослых» or «Скрыть из
	// общих подборок» (nil: none), read at each scan: what it indexes in
	// them is marked as it is stored, and every scan ends with the marks of
	// the whole library brought in line (Repo.SyncFolderFlags).
	Flags  func() FolderFlags
	ArtDir string // extracted artwork (embedded covers): DataDir/images

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
	// rescan is Rescan's request; full is the running scan's copy of it
	// (written and read under mu).
	rescan atomic.Bool
	full   bool
	// dirty: a scan since Jobs last looked wrote or removed something, or
	// read everything again (noteChanges). Whoever runs a scan sets it, an
	// addon's import included, so Jobs' own scan finding everything in place
	// does not hide it from the workers.
	dirty atomic.Bool
}

// Rescan makes the next scan read every movie and episode again, not only
// those whose files or sidecars changed (a scan after a start, a manual
// one: the way to pick up what a signature missed).
func (s *Scanner) Rescan() { s.rescan.Store(true) }

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

// walkVideos lists video files under root, skipping samples, extras and hidden entries.
func walkVideos(ctx context.Context, root string) ([]walkedFile, error) {
	return walkFiles(ctx, root, func(low string) bool { return skipDirs[low] }, func(name string) bool {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		return videoExt[strings.ToLower(ext)] && !reSample.MatchString(base) && !reTrailer.MatchString(base)
	})
}

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
	s.noteChanges(st, s.full)
	if ferr := s.syncFlags(ctx); ferr != nil {
		err = errors.Join(err, ferr)
	}
	return st, err
}

// scanVideo is ScanVideo without the lock (the caller holds s.mu).
func (s *Scanner) scanVideo(ctx context.Context, st *ScanStats) error {
	s.full = s.rescan.Swap(false)
	var errs []error
	if len(s.Roots(KindFolderMovies)) > 0 {
		if err := s.scanMovies(ctx, st); err != nil {
			errs = append(errs, fmt.Errorf("movies: %w", err))
		}
	}
	if len(s.Roots(KindFolderSeries)) > 0 {
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
	files []walkedFile
}

func (s *Scanner) scanMovies(ctx context.Context, st *ScanStats) error {
	return s.eachRoot(ctx, KindFolderMovies, st, s.scanMoviesIn)
}

// scanMoviesIn is scanMovies of one of the kind's folders.
func (s *Scanner) scanMoviesIn(ctx context.Context, dir string, st *ScanStats) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	files, walkErr := walkVideos(ctx, root)
	if walkErr != nil && files == nil {
		return walkErr
	}
	groups, perDir := groupMovies(files)
	stored, err := s.Repo.storedFilesUnder(ctx, root, videoKinds, fileSigSQL)
	if err != nil {
		return err
	}
	di := dirIndex{}
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		sort.Slice(g.files, func(i, j int) bool { return g.files[i].path < g.files[j].path })
		single := perDir[g.dir] == 1 && g.dir != root
		nfoPath := movieNFO(di, g, single)
		poster, backdrop := movieArt(di, g, single)
		sig := movieSig(g, single, nfoPath, poster, backdrop)
		if !s.full && unchangedFiles(g.files, sig, stored) { // read nothing, write nothing
			st.Movies++
			st.Files += len(g.files)
			continue
		}

		title, year, alts := movieTitles(g, root, single)
		in := MovieInput{Title: title, Year: year, AltTitles: alts, Poster: poster, Backdrop: backdrop}
		for _, f := range g.files {
			in.Files = append(in.Files, FileInput{Path: f.path, Size: f.size, Mtime: f.mtime, sig: sig})
		}
		if n, ok := readNFOIf(nfoPath); ok {
			applyMovieNFO(&in, n)
		}
		if in.Title == "" {
			st.Skipped += len(g.files)
			continue
		}
		if _, err := s.Repo.UpsertMovie(ctx, in); err != nil {
			return err
		}
		st.Movies++
		st.Changed++
		st.Files += len(g.files)
	}
	if walkErr != nil {
		return walkErr // incomplete walk: no pruning
	}
	return s.finish(ctx, root, seenPaths(files), st)
}

// groupMovies groups multi-part files (… - cd1.mkv, … - cd2.mkv) into one
// movie each, in the order the files come; perDir counts the movies of each
// folder.
func groupMovies(files []walkedFile) (groups []*movieGroup, perDir map[string]int) {
	byKey := map[string]*movieGroup{}
	perDir = map[string]int{}
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
	return groups, perDir
}

// movieTitles is the movie's title, year and other titles from its file
// name and its folder's.
func movieTitles(g *movieGroup, root string, single bool) (title string, year int, alts []string) {
	info := release.Parse(g.base)
	title, year = info.Title, info.Year
	if info.AltTitle != "" {
		alts = append(alts, info.AltTitle)
	}
	if g.dir == root {
		return title, year, alts
	}
	dname := filepath.Base(g.dir)
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
	return title, year, alts
}

// readNFOIf is readNFO of a path that may be "" (no .nfo).
func readNFOIf(path string) (NFO, bool) {
	if path == "" {
		return NFO{}, false
	}
	return readNFO(path)
}

// applyMovieNFO lets the movie's .nfo override what the names said.
func applyMovieNFO(in *MovieInput, n NFO) {
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

// videoKinds are the kinds whose files the video scan stores (an SQL list).
const videoKinds = "('movie','episode')"

// videoSigVersion is part of every movie and episode signature: bumping it
// reads every title once more, for a scan that now reads the same files
// differently.
const videoSigVersion = 1

// sigFile adds the sidecar at path (name, size, mtime) to h; "" is none.
func sigFile(h io.Writer, path string) {
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(h, "%s\x00?\x00", path)
		return
	}
	fmt.Fprintf(h, "%s\x00%d\x00%d\x00", path, info.Size(), info.ModTime().UnixNano())
}

// movieNFO is the .nfo describing a movie group: its own base name's, its
// first file's, or movie.nfo of a folder that holds only this movie.
func movieNFO(di dirIndex, g *movieGroup, single bool) string {
	p := di.find(g.dir, g.base+".nfo")
	if p == "" {
		p = di.find(g.dir, strings.TrimSuffix(filepath.Base(g.files[0].path), filepath.Ext(g.files[0].path))+".nfo")
	}
	if p == "" && single {
		p = di.find(g.dir, "movie.nfo")
	}
	return p
}

// movieArt is the movie's local poster and backdrop: named after it, or
// the folder's own when the folder holds only this movie.
func movieArt(di dirIndex, g *movieGroup, single bool) (poster, backdrop string) {
	poster = di.art(g.dir, g.base+"-poster", g.base+".poster")
	backdrop = di.art(g.dir, g.base+"-fanart", g.base+"-backdrop", g.base+"-background")
	if single {
		if poster == "" {
			poster = di.art(g.dir, "poster", "folder", "cover")
		}
		if backdrop == "" {
			backdrop = di.art(g.dir, "fanart", "backdrop", "background")
		}
	}
	return poster, backdrop
}

// movieSig is a signature of everything a movie's scan reads: its files
// (name, size, mtime), its .nfo, its pictures and whether its folder holds
// only this movie.
func movieSig(g *movieGroup, single bool, nfo, poster, backdrop string) string {
	h := sha1.New()
	fmt.Fprintf(h, "v%d\x00single=%v\x00%s\x00%s\x00", videoSigVersion, single, poster, backdrop)
	for _, f := range g.files {
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", f.path, f.size, f.mtime)
	}
	sigFile(h, nfo)
	return hex.EncodeToString(h.Sum(nil))
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
	return s.eachRoot(ctx, KindFolderSeries, st, s.scanSeriesIn)
}

// scanSeriesIn is scanSeries of one of the kind's folders.
func (s *Scanner) scanSeriesIn(ctx context.Context, dir string, st *ScanStats) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	files, walkErr := walkVideos(ctx, root)
	if walkErr != nil && files == nil {
		return walkErr
	}
	stored, err := s.Repo.storedFilesUnder(ctx, root, videoKinds, fileSigSQL)
	if err != nil {
		return err
	}
	di := dirIndex{}
	metas := map[string]*seriesMeta{} // series dir -> meta
	ssigs := map[string]string{}      // series dir -> signature of what its meta reads
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Dir(f.path)
		base := strings.TrimSuffix(filepath.Base(f.path), filepath.Ext(f.path))
		sdir := seriesDir(di, root, dir, f.path)
		ssig, ok := ssigs[sdir]
		if !ok {
			ssig = seriesSig(di, sdir)
			ssigs[sdir] = ssig
		}
		sig := episodeSig(ssig, f, di.find(dir, base+".nfo"))
		if !s.full && unchangedFiles([]walkedFile{f}, sig, stored) { // read nothing, write nothing
			st.Episodes++
			st.Files++
			continue
		}
		finfo := release.Parse(base)

		var sm *seriesMeta
		if sdir != "" {
			sm = metas[sdir]
		}
		if sm == nil {
			sm = loadSeriesMeta(di, sdir, finfo)
			if sdir != "" {
				metas[sdir] = sm
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
			File: FileInput{Path: f.path, Size: f.size, Mtime: f.mtime, sig: sig},
		})
		if err != nil {
			return err
		}
		st.Episodes++
		st.Changed++
		st.Files++
	}
	if walkErr != nil {
		return walkErr
	}
	return s.finish(ctx, root, seenPaths(files), st)
}

// loadSeriesMeta is what the series directory sdir says about its series:
// its name, tvshow.nfo and pictures; a loose file (sdir "") or a
// season-named directory has the title of the file's own name (finfo).
func loadSeriesMeta(di dirIndex, sdir string, finfo release.Info) *seriesMeta {
	sm := &seriesMeta{}
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
	}
	if sm.title == "" {
		sm.title, sm.year = finfo.Title, finfo.Year // loose file or season-named dir
		if finfo.AltTitle != "" {
			sm.alts = append(sm.alts, finfo.AltTitle)
		}
	}
	return sm
}

// seriesDir is the series directory of a file in dir: the nearest ancestor
// with tvshow.nfo, else the first directory below root, else "" (a loose
// file in the root).
func seriesDir(di dirIndex, root, dir, path string) string {
	for d := dir; d != root && strings.HasPrefix(d, root); d = filepath.Dir(d) {
		if di.find(d, "tvshow.nfo") != "" {
			return d
		}
	}
	if dir == root {
		return ""
	}
	rel, _ := filepath.Rel(root, path)
	return filepath.Join(root, strings.Split(rel, string(filepath.Separator))[0])
}

// seriesSig is a signature of what the series' meta reads beside the
// episode files: its directory, tvshow.nfo and pictures.
func seriesSig(di dirIndex, sdir string) string {
	if sdir == "" {
		return ""
	}
	h := sha1.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00", sdir, di.art(sdir, "poster", "folder", "cover"), di.art(sdir, "fanart", "backdrop", "background"))
	sigFile(h, di.find(sdir, "tvshow.nfo"))
	return hex.EncodeToString(h.Sum(nil))
}

// episodeSig is a signature of everything an episode's scan reads: its file
// (name, size, mtime), its .nfo and its series' (seriesSig).
func episodeSig(ssig string, f walkedFile, nfo string) string {
	h := sha1.New()
	fmt.Fprintf(h, "v%d\x00%s\x00%s\x00%d\x00%d\x00", videoSigVersion, ssig, f.path, f.size, f.mtime)
	sigFile(h, nfo)
	return hex.EncodeToString(h.Sum(nil))
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
