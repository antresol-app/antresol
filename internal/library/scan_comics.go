package library

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/comics"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/natsort"
)

// peekComic reads a volume for the scanner (a variable for the tests).
var peekComic = comics.Peek

var (
	reComicBrackets = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)|\{[^}]*\}`)
	reComicVolume   = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:volume|vol|v|tome|том|т)\.?[\s_]*(\d+(?:[.,]\d+)?)`)
	reComicChapter  = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:chapter|chap|ch|c|глава|гл)\.?[\s_]*(\d+(?:[.,]\d+)?)`)
	reComicHash     = regexp.MustCompile(`#\s*(\d+(?:[.,]\d+)?)`)
	reComicBare     = regexp.MustCompile(`(?:^|[\s_.-])(\d+(?:[.,]\d+)?)\s*$`)
	reComicYear     = regexp.MustCompile(`^(?:19|20)\d\d$`)
	reRarPart       = regexp.MustCompile(`(?i)\.part0*([2-9]|[1-9]\d+)\.rar$`)
)

func comicNum(s string) float64 {
	v, _ := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
	return v
}

// parseComicFile splits a volume file or folder name (without extension)
// into the series name and the numbers: «Берсерк v01» → «Берсерк», 1, 0.
// The series is what precedes the first number; groups in brackets
// ([Viz], (2019)) are ignored. See parseComicNumber.
func parseComicFile(stem string) (series string, volume, chapter float64) {
	s := reComicBrackets.ReplaceAllString(strings.ReplaceAll(stem, "_", " "), " ")
	cut := len(s)
	at := func(m []int) {
		if m != nil && m[0] < cut {
			cut = m[0]
		}
	}
	if m := reComicVolume.FindStringSubmatchIndex(s); m != nil {
		volume = comicNum(s[m[2]:m[3]])
		at(m)
	}
	if m := reComicChapter.FindStringSubmatchIndex(s); m != nil {
		chapter = comicNum(s[m[2]:m[3]])
		at(m)
	} else if m := reComicHash.FindStringSubmatchIndex(s); m != nil {
		chapter = comicNum(s[m[2]:m[3]])
		at(m)
	}
	if volume == 0 && chapter == 0 {
		// a bare trailing number is the volume, unless it is a year («Akira 1988»)
		if m := reComicBare.FindStringSubmatchIndex(s); m != nil && !reComicYear.MatchString(s[m[2]:m[3]]) {
			volume = comicNum(s[m[2]:m[3]])
			at(m)
		}
	}
	series = strings.Join(strings.Fields(strings.Trim(strings.TrimSpace(s[:cut]), " -–—_.,#:")), " ")
	return series, volume, chapter
}

// parseComicNumber returns the volume and chapter numbers in a name: «v01»,
// «Vol.01 Ch.001», «Том 3», «Глава 12», «ch.12.5», «#12», a bare trailing
// number («Берсерк 05» = volume 5). 0 = no number.
func parseComicNumber(name string) (volume, chapter float64) {
	_, volume, chapter = parseComicFile(name)
	return volume, chapter
}

func fmtComicNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// comicVolumeTitle is a volume's display name: «Том 1», «Том 1 · Глава 3»,
// «Глава 12», else the file name.
func comicVolumeTitle(volume, chapter float64, stem string) string {
	switch {
	case volume > 0 && chapter > 0:
		return i18n.TL(i18n.RU, "Том %s · Глава %s", fmtComicNum(volume), fmtComicNum(chapter))
	case volume > 0:
		return i18n.TL(i18n.RU, "Том %s", fmtComicNum(volume))
	case chapter > 0:
		return i18n.TL(i18n.RU, "Глава %s", fmtComicNum(chapter))
	}
	return stem
}

// comicStem is the name of a volume without its extension (a folder: its name).
func comicStem(p string) string {
	base := filepath.Base(p)
	if comics.IsArchive(base) {
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return base
}

// comicFile is a volume on disk: an archive, or a folder of images (size =
// sum of its pages, mtime = the newest page).
type comicFile struct {
	path         string
	size, mtime  int64
	folderVolume bool
}

func skipComicDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.EqualFold(name, "__MACOSX") || strings.EqualFold(name, "@eaDir")
}

// walkComics lists the volumes under root: comic archives (not the second
// and later parts of a multi-part RAR) and folders of images. A folder is
// a volume when it holds images, no archive, and no subfolder with images
// (so a series folder with a cover.jpg is not one). Images right in the
// root are not a volume.
func walkComics(ctx context.Context, root string) ([]comicFile, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	type dirPages struct {
		images      int
		size, mtime int64
		archives    bool
	}
	dirs := map[string]*dirPages{}
	at := func(dir string) *dirPages {
		d := dirs[dir]
		if d == nil {
			d = &dirPages{}
			dirs[dir] = d
		}
		return d
	}
	var out []comicFile
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
			if p != root && skipComicDir(name) {
				return fs.SkipDir
			}
			return nil
		}
		archive := comics.IsArchive(name) && !reRarPart.MatchString(name)
		if strings.HasPrefix(name, ".") || !d.Type().IsRegular() || (!archive && !comics.IsPage(name)) {
			return nil // symlinks, FIFOs and devices are never pages or volumes
		}
		info, err := d.Info()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		dp := at(filepath.Dir(p))
		if archive {
			dp.archives = true
			out = append(out, comicFile{path: p, size: info.Size(), mtime: info.ModTime().Unix()})
			return nil
		}
		dp.images++
		dp.size += info.Size()
		dp.mtime = max(dp.mtime, info.ModTime().Unix())
		return nil
	})
	if err == nil {
		err = firstErr // incomplete walk: the caller must not prune
	}
	imagesBelow := map[string]bool{}
	for dir, d := range dirs {
		if d.images == 0 {
			continue
		}
		for a := filepath.Dir(dir); len(a) >= len(root); a = filepath.Dir(a) {
			if imagesBelow[a] {
				break
			}
			imagesBelow[a] = true
			if a == root {
				break
			}
		}
	}
	for dir, d := range dirs {
		if dir != root && d.images > 0 && !d.archives && !imagesBelow[dir] {
			out = append(out, comicFile{path: dir, size: d.size, mtime: d.mtime, folderVolume: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return natsort.Less(out[i].path, out[j].path) })
	return out, err
}

// comicGroup is a series as found on disk: a folder right under the root
// (Kavita: everything below it belongs to it), or, for volumes right in
// the root, the series name parsed from the file name.
type comicGroup struct {
	key, dir, name string
	vols           []comicFile
}

func groupComics(root string, files []comicFile) []*comicGroup {
	groups := map[string]*comicGroup{}
	var order []string
	for _, f := range files {
		rel, err := filepath.Rel(root, f.path)
		if err != nil {
			continue
		}
		var g comicGroup
		if top, _, nested := strings.Cut(rel, string(filepath.Separator)); nested {
			g.dir = filepath.Join(root, top)
			g.name = top
		} else {
			stem := comicStem(f.path)
			if g.name, _, _ = parseComicFile(stem); g.name == "" {
				g.name = stem
			}
		}
		g.key = comicMatchKey(g.dir, f.path, g.name)
		if groups[g.key] == nil {
			groups[g.key] = &g
			order = append(order, g.key)
		}
		groups[g.key].vols = append(groups[g.key].vols, f)
	}
	sort.Strings(order)
	out := make([]*comicGroup, len(order))
	for i, k := range order {
		out[i] = groups[k]
	}
	return out
}

// sig is a signature of the series' volumes on disk (paths, sizes, mtimes).
func (g *comicGroup) sig() string {
	h := sha1.New()
	fmt.Fprintf(h, "%s\x00", g.key)
	for _, v := range g.vols {
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00%v\x00", v.path, v.size, v.mtime, v.folderVolume)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// unchangedComic reports whether a volume is stored, visible, with the
// same size and mtime, in the given series.
func unchangedComic(v comicFile, stored map[string]storedComicFile, seriesID int64) bool {
	sf, ok := stored[v.path]
	return ok && seriesID != 0 && sf.seriesID == seriesID && sf.size == v.size && sf.mtime == v.mtime
}

// scanComics indexes the comics root (Kavita layout): a series per folder
// under the root, a volume per archive or folder of images; files right
// in the root form series by their parsed name. Metadata comes from the
// names and ComicInfo.xml (direction right to left unless it says
// otherwise), the cover from the first page. Unchanged series and volumes
// are not read again. A root that cannot be read completely is never
// pruned.
func (s *Scanner) scanComics(ctx context.Context, st *ScanStats) error {
	root, err := filepath.Abs(s.Comics)
	if err != nil {
		return err
	}
	files, walkErr := walkComics(ctx, root)
	if walkErr != nil && files == nil {
		return walkErr
	}
	storedFiles, err := s.Repo.comicFilesUnder(ctx, root)
	if err != nil {
		return err
	}
	storedSeries, err := s.Repo.comicSeriesUnder(ctx, root)
	if err != nil {
		return err
	}
	type pending struct {
		id        int64
		name, sig string
	}
	var todo []pending
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.path] = true
	}
	for _, g := range groupComics(root, files) {
		if err := ctx.Err(); err != nil {
			return err
		}
		sig := g.sig()
		ss := storedSeries[g.key]
		same := ss.id != 0 && ss.sig == sig
		for _, v := range g.vols {
			same = same && unchangedComic(v, storedFiles, ss.id)
		}
		if same { // nothing in the series changed: not read, not written
			st.Comics += len(g.vols)
			st.Files += len(g.vols)
			continue
		}
		seriesID := ss.id
		for _, v := range g.vols {
			if !unchangedComic(v, storedFiles, ss.id) {
				_, id, err := s.Repo.upsertComicVolume(ctx, s.comicInput(g, v))
				if err != nil {
					return fmt.Errorf("comic %s: %w", v.path, err)
				}
				seriesID = id
			}
			st.Comics++
			st.Files++
		}
		todo = append(todo, pending{seriesID, g.name, sig})
	}
	if walkErr == nil {
		if err := s.finish(ctx, root, seen, st); err != nil {
			return err
		}
	}
	// after pruning: a removed first volume hands the cover to the next
	for _, p := range todo {
		if err := s.Repo.finishComicSeries(ctx, p.id, p.name, p.sig); err != nil {
			return fmt.Errorf("comic series %q: %w", p.name, err)
		}
	}
	return walkErr // incomplete walk: no pruning
}

// comicInput reads a volume (names, ComicInfo.xml, first page).
func (s *Scanner) comicInput(g *comicGroup, v comicFile) ComicInput {
	stem := comicStem(v.path)
	pages, info, hasInfo, first, err := peekComic(v.path)
	if err != nil {
		log.Printf("library: comic %s: %v", v.path, err)
	}
	_, vol, ch := parseComicFile(stem)
	if vol == 0 && ch == 0 && hasInfo {
		vol, ch = info.Volume, info.Number
	}
	title := comicVolumeTitle(vol, ch, stem)
	if vol == 0 && ch == 0 && info.Title != "" {
		title = info.Title
	}
	dir := info.Direction()
	if dir == "" {
		dir = "rtl" // the comics root is manga/: right to left unless ComicInfo says otherwise
	}
	return ComicInput{
		File:      FileInput{Path: v.path, Size: v.size, Mtime: v.mtime},
		SeriesDir: g.dir, Series: g.name, Volume: vol, Chapter: ch, Title: title,
		Writer: info.Writer, Summary: info.Summary, Lang: info.LanguageISO, Direction: dir,
		Rating: NormalizeRating(info.Rating()), Year: info.Year, Pages: len(pages),
		Cover:      s.comicCover(v, pages, first),
		infoSeries: info.Series,
	}
}

// comicCover is the volume cover from its first page: the page file itself
// for a folder volume when it can be shown as is, else a copy (or a 600 px
// JPEG) in ArtDir; "" for AVIF or an unreadable page.
func (s *Scanner) comicCover(v comicFile, pages []string, page []byte) string {
	out, ext := comics.Cover(page)
	if out == nil {
		return ""
	}
	if v.folderVolume && len(pages) > 0 && bytes.Equal(out, page) {
		return fileURL(filepath.Join(v.path, filepath.FromSlash(pages[0])))
	}
	name, err := s.saveArt(out, ext)
	if err != nil {
		log.Printf("library: comic cover %s: %v", v.path, err)
	}
	return name
}
