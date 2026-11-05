package library

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/dhowden/tag"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/natsort"
	"github.com/antresol-app/antresol/internal/release"
)

var (
	// rePartDir is a part folder of one audiobook ("CD1", "Disc 2",
	// "Диск 3", "Часть 1", "Part 4 - Title"): it folds into its parent.
	rePartDir = regexp.MustCompile(`(?i)^(?:cd|disc|disk|диск|часть|part)\s*\d+(?:[^\p{L}\p{N}].*)?$`)
	// reIndexLeft is a series index left of « - »: "1", "2.5", "Книга 3",
	// "Vol. 2", "#3", "Том 2", "Часть 1".
	reIndexLeft = regexp.MustCompile(`(?i)^(?:#|№|книга|кн\.?|том|т\.|часть|ч\.|vol\.?|volume|book|bk\.?)?\s*(\d+(?:\.\d+)?)$`)
	// reIndexDot is "03. Title" / "3) Title".
	reIndexDot = regexp.MustCompile(`^(\d+(?:\.\d+)?)[.)]\s+(.+)$`)
	// reIndexWord is "Том 2. Title" / "Книга 3: Title" / "#3. Title".
	reIndexWord = regexp.MustCompile(`(?i)^(?:#|№|книга|кн\.?|том|vol\.?|volume|book)\s*(\d+(?:\.\d+)?)[.:]\s+(.+)$`)
	reBookYear  = regexp.MustCompile(`^(?:19|20)\d\d$`)
	// reReleaseInfo marks a bracket part that is not a narrator: bitrate,
	// format and the like.
	reReleaseInfo = regexp.MustCompile(`(?i)\d+\s*kbps|\bkbit|^(?:mp3|m4b|m4a|aac|flac|ogg|opus|vbr|cbr|lossless)$|\d+\s*(?:khz|hz|bit)\b`)
	// rePartSuffix is a part number at the end of a file stem ("Part1",
	// "Часть 2", "cd3", "_04"): files differing only by it are one book.
	rePartSuffix = regexp.MustCompile(`(?i)[\s._-]*(?:part|pt|часть|ч|cd|disc|том|глава|chapter|ch)?\.?\s*\d+$`)
	reURLOnly    = regexp.MustCompile(`(?i)^(?:https?://|www\.)\S*$`)
)

// parseAudiobookFolder reads an audiobook folder (or file stem) name:
// "Автор - Название [Чтец, Год, 128 kbps, MP3]", "Название", or a series
// index before the title ("1 - Название", "Книга 3 - Название", "Vol. 2 -
// Название", "#3 - Название", "03. Название", "Том 2. Название"), which is
// never an author.
func parseAudiobookFolder(name string) (author, title, narrator string, year int, seriesIndex float64) {
	s := strings.TrimSpace(name)
	for _, m := range reBrackets.FindAllString(s, -1) {
		for _, part := range strings.Split(strings.Trim(strings.TrimSpace(m), "[]"), ",") {
			part = strings.TrimSpace(part)
			switch {
			case part == "":
			case reBookYear.MatchString(part):
				if year == 0 {
					year, _ = strconv.Atoi(part)
				}
			case reReleaseInfo.MatchString(part):
			case narrator == "":
				narrator = part
			}
		}
	}
	s = strings.TrimSpace(reBrackets.ReplaceAllString(s, ""))
	if m := reParenYear.FindStringSubmatch(s); m != nil {
		if year == 0 {
			year, _ = strconv.Atoi(m[1])
		}
		s = strings.TrimSpace(reParenYear.ReplaceAllString(s, ""))
	}
	for _, re := range []*regexp.Regexp{reIndexWord, reIndexDot} {
		if m := re.FindStringSubmatch(s); m != nil {
			seriesIndex, _ = strconv.ParseFloat(m[1], 64)
			return "", strings.TrimSpace(m[2]), narrator, year, seriesIndex
		}
	}
	if loc := reDashSep.FindStringIndex(s); loc != nil {
		left, right := strings.TrimSpace(s[:loc[0]]), strings.TrimSpace(s[loc[1]:])
		if left != "" && right != "" {
			if m := reIndexLeft.FindStringSubmatch(left); m != nil {
				seriesIndex, _ = strconv.ParseFloat(m[1], 64)
				return "", right, narrator, year, seriesIndex
			}
			return left, right, narrator, year, 0
		}
	}
	return "", s, narrator, year, 0
}

type audiobookFile struct {
	path        string
	size, mtime int64
}

// walkAudiobooks lists the audio files under root. Hidden entries and
// NAS/macOS junk folders are skipped. An error means the walk is
// incomplete: the caller must not prune.
func walkAudiobooks(ctx context.Context, root string) ([]audiobookFile, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	var out []audiobookFile
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
			low := strings.ToLower(name)
			if p != root && (strings.HasPrefix(name, ".") || low == "__macosx" || low == "@eadir" || low == ".appledouble") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || !audioExt[strings.ToLower(filepath.Ext(name))] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		out = append(out, audiobookFile{p, info.Size(), info.ModTime().Unix()})
		return nil
	})
	if err == nil {
		err = firstErr
	}
	return out, err
}

// audiobookGroup is one audiobook before its metadata is read: a book
// folder (part folders folded in), or a single file (dir "").
type audiobookGroup struct {
	dir   string // book folder; "" for a single-file book
	depth int    // levels of the book folder (or the file's folder) below the root
	files []audiobookFile
}

// fileStem is a file's base name without its extension.
func fileStem(p string) string {
	b := filepath.Base(p)
	return strings.TrimSuffix(b, filepath.Ext(b))
}

// relDepth is the number of path elements of dir below root (0 = root).
func relDepth(root, dir string) int {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(rel, string(filepath.Separator)))
}

// oneBookFolder decides whether the audio files of a folder right under
// the root are one book or separate books in an author folder. Only a
// folder of nothing but M4B/M4A files can be an author folder
// ("Лем Станислав/Солярис.m4b", ".../Непобедимый.m4b"); it is still one
// book when its files are parts ("Part1.m4b", "Part2.m4b"), come from part
// subfolders, or the folder is named "Автор - Название [...]" or after its
// only file ("Название/Название.m4b"). Any other folder with audio
// ("Мастер и Маргарита/Пролог.mp3, Эпилог.mp3") is one book.
func oneBookFolder(dir string, files []audiobookFile) bool {
	for _, f := range files {
		if filepath.Dir(f.path) != dir {
			return true // parts came from CD/Часть subfolders
		}
		if e := strings.ToLower(filepath.Ext(f.path)); e != ".m4b" && e != ".m4a" {
			return true
		}
	}
	author, folderTitle, _, _, _ := parseAudiobookFolder(filepath.Base(dir))
	if author != "" {
		return true // "Автор - Название [...]"
	}
	base := ""
	for i, f := range files {
		s := release.Normalize(rePartSuffix.ReplaceAllString(fileStem(f.path), ""))
		if len(files) == 1 {
			return s == "" || s == release.Normalize(folderTitle) // "Название/Название.m4b", "Книга/01.m4b"
		}
		if i == 0 {
			base = s
		} else if s != base {
			return false
		}
	}
	return true // "Part1.m4b", "Part2.m4b"
}

// groupAudiobooks splits the files into books. A folder holding audio
// files is one book, its files (and CD/Disc/Часть/Part subfolders) the
// book's parts in natural order. A file right in the root, and the files
// of an author folder right under the root (see oneBookFolder), are books
// of their own.
func groupAudiobooks(root string, files []audiobookFile) []*audiobookGroup {
	bookDir := func(p string) string {
		d := filepath.Dir(p)
		for d != root && filepath.Dir(d) != root && rePartDir.MatchString(filepath.Base(d)) {
			d = filepath.Dir(d)
		}
		return d
	}
	byDir := map[string][]audiobookFile{}
	var dirs []string
	for _, f := range files {
		d := bookDir(f.path)
		if byDir[d] == nil {
			dirs = append(dirs, d)
		}
		byDir[d] = append(byDir[d], f)
	}
	var out []*audiobookGroup
	for _, d := range dirs {
		fs, depth := byDir[d], relDepth(root, d)
		if depth == 0 || depth == 1 && !oneBookFolder(d, fs) {
			for _, f := range fs {
				out = append(out, &audiobookGroup{depth: depth, files: []audiobookFile{f}})
			}
			continue
		}
		g := &audiobookGroup{dir: d, depth: depth, files: fs}
		sort.SliceStable(g.files, func(i, j int) bool {
			ri, _ := filepath.Rel(d, g.files[i].path)
			rj, _ := filepath.Rel(d, g.files[j].path)
			return natsort.Less(ri, rj)
		})
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].files[0].path < out[j].files[0].path })
	return out
}

// bookTags is what the audiobook scanner reads from a file's tags (text
// repaired by fixTagText; "" when unreadable).
type bookTags struct {
	title, album, artist, albumArtist, narrator, composer, comment string
	year                                                           int
	picture                                                        *tag.Picture
}

// narratorKeys are the raw tag keys of a narrator (MP4, Vorbis, ID3 TXXX).
var narratorKeys = map[string]bool{"©nrt": true, "\xa9nrt": true, "narrator": true, "NARRATOR": true, "TXXX:NARRATOR": true}

// rawText returns a raw tag value as text.
func rawText(v any) string {
	switch x := v.(type) {
	case string:
		return fixTagText(x)
	case *tag.Comm:
		if x != nil {
			return fixTagText(x.Text)
		}
	case []string:
		return fixTagText(strings.Join(x, ", "))
	}
	return ""
}

// bookTagReads counts the audiobook tag reads (tests: an unchanged book is
// not re-read).
var bookTagReads atomic.Int64

// readTagMeta reads the tags of an audio file; a panic of the tag reader
// is an error, not the end of the scan.
func readTagMeta(path string) (m tag.Metadata, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	defer func() {
		if r := recover(); r != nil {
			m, err = nil, fmt.Errorf("tag reader panic: %v", r)
		}
	}()
	return tag.ReadFrom(f)
}

// readBookTags reads an audiobook file's tags; a broken or untagged file
// is an error (the names are used then).
func readBookTags(path string) (t bookTags, err error) {
	bookTagReads.Add(1)
	m, err := readTagMeta(path)
	if err != nil {
		return t, err
	}
	t.title, t.album, t.artist = fixTagText(m.Title()), fixTagText(m.Album()), fixTagText(m.Artist())
	t.albumArtist, t.composer, t.comment = fixTagText(m.AlbumArtist()), fixTagText(m.Composer()), fixTagText(m.Comment())
	t.year, t.picture = m.Year(), m.Picture()
	raw := m.Raw()
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys) // TXXX before TXXX_0: the first frame wins
	for _, k := range keys {
		v := raw[k]
		key := k
		if c, ok := v.(*tag.Comm); ok && c != nil && (k == "TXXX" || strings.HasPrefix(k, "TXXX_") || k == "TXX" || strings.HasPrefix(k, "TXX_")) {
			key = "TXXX:" + strings.ToUpper(strings.TrimSpace(c.Description))
		}
		switch {
		case t.narrator == "" && narratorKeys[key]:
			t.narrator = rawText(v)
		case t.comment == "" && (strings.EqualFold(key, "description") || key == "TXXX:DESCRIPTION" || key == "TXXX:COMMENT"):
			t.comment = rawText(v)
		}
	}
	return t, nil
}

// readSidecar returns the trimmed text of an Audiobookshelf sidecar
// (desc.txt, reader.txt) in dir, "" if there is none.
func readSidecar(di dirIndex, dir, name string) string {
	p := di.find(dir, name)
	if p == "" {
		return ""
	}
	st, err := os.Stat(p)
	if err != nil || st.Size() > 1<<20 {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(decodeText(b), "\ufeff"))
}

// scanAudiobooks indexes the audiobooks root: a book per folder (part
// folders folded in) or per single file, metadata from the first file's
// tags, then the folder names and Audiobookshelf sidecars, chapters from
// the files (M4B/M4A chapter lists) or one per file. An unchanged book is
// not read again. A root that cannot be read completely is never pruned.
func (s *Scanner) scanAudiobooks(ctx context.Context, st *ScanStats) error {
	root, err := filepath.Abs(s.Audiobooks)
	if err != nil {
		return err
	}
	files, walkErr := walkAudiobooks(ctx, root)
	if walkErr != nil && files == nil {
		return walkErr
	}
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.path] = true
	}
	di := dirIndex{}
	for _, g := range groupAudiobooks(root, files) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.scanAudiobook(ctx, root, g, di, st); err != nil {
			return err
		}
	}
	if walkErr != nil {
		return walkErr // incomplete walk: no pruning
	}
	return s.finish(ctx, root, seen, st)
}

// audiobookSigVersion changes when the scanner reads books differently, so
// that stored books are read again.
const audiobookSigVersion = "ab2"

// audiobookSig is a signature of everything a book's scan reads: the
// entries (name, size, mtime) of the book folder and its part folders —
// audio, desc.txt, reader.txt, images — or, for a single-file book, the
// file and its same-stem images. "" when a folder cannot be listed.
func audiobookSig(g *audiobookGroup) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s\x00%d\x00", audiobookSigVersion, g.depth)
	entry := func(name string, info fs.FileInfo) {
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", name, info.Size(), info.ModTime().UnixNano())
	}
	if g.dir == "" {
		f := g.files[0]
		dir, st := filepath.Dir(f.path), strings.ToLower(fileStem(f.path))
		es, err := os.ReadDir(dir)
		if err != nil {
			return ""
		}
		fmt.Fprintf(h, "F%s\x00", f.path)
		for _, e := range es {
			if e.IsDir() || strings.ToLower(fileStem(e.Name())) != st {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return ""
			}
			entry(e.Name(), info)
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	dirs := map[string]bool{g.dir: true}
	for _, f := range g.files {
		dirs[filepath.Dir(f.path)] = true
	}
	list := make([]string, 0, len(dirs))
	for d := range dirs {
		list = append(list, d)
	}
	sort.Strings(list)
	for _, d := range list {
		es, err := os.ReadDir(d)
		if err != nil {
			return ""
		}
		fmt.Fprintf(h, "D%s\x00", d)
		for _, e := range es {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return ""
			}
			entry(e.Name(), info)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// chapterProbeFiles reports whether a book's chapters come from its files:
// a one-file book, or a book of M4B/M4A parts.
func chapterProbeFiles(files []FileInput) bool {
	if len(files) == 1 {
		return true
	}
	for _, f := range files {
		if e := strings.ToLower(filepath.Ext(f.Path)); e != ".m4b" && e != ".m4a" {
			return false
		}
	}
	return true
}

// probeKey is meta_json.chapters_probed: the files (path, size, mtime) the
// chapter probe read successfully.
func probeKey(files []FileInput) string {
	h := sha1.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", f.Path, f.Size, f.Mtime)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// quietTagErr reports a tag error not worth a log line: no tags at all, or
// a file too short to hold any.
func quietTagErr(err error, size int64) bool {
	return errors.Is(err, tag.ErrNoTagsFound) || size < 1024
}

func (s *Scanner) scanAudiobook(ctx context.Context, root string, g *audiobookGroup, di dirIndex, st *ScanStats) error {
	inputs := make([]FileInput, len(g.files))
	for i, f := range g.files {
		inputs[i] = FileInput{Path: f.path, Size: f.size, Mtime: f.mtime}
	}
	key := g.dir
	if key == "" {
		key = g.files[0].path
	}
	probing := s.Chapters != nil && chapterProbeFiles(inputs)
	pkey := probeKey(inputs)
	sig := audiobookSig(g)

	stored, have, err := s.Repo.storedAudiobook(ctx, key)
	if err != nil {
		return err
	}
	if have && sig != "" && stored.sig == sig && len(stored.files) == len(inputs) && (!probing || stored.probed == pkey) {
		same := true
		for _, f := range inputs {
			if _, ok := stored.files[f.Path]; !ok {
				same = false
				break
			}
		}
		if same { // unchanged: no tag reads, no rewrite
			st.Audiobooks++
			st.Files += len(inputs)
			return nil
		}
	}

	tags := make([]bookTags, len(g.files))
	tagged := make([]bool, len(g.files))
	for i, f := range g.files {
		t, err := readBookTags(f.path)
		if err != nil {
			if i == 0 && !quietTagErr(err, f.size) {
				log.Printf("library: audiobook tags %s: %v", f.path, err)
			}
			continue
		}
		tags[i], tagged[i] = t, true
	}
	first := tags[0]

	// names: the book folder (or the file's stem) and the folders above it
	dir, name := g.dir, g.dir
	if dir == "" {
		dir = filepath.Dir(g.files[0].path)
		name = strings.TrimSuffix(g.files[0].path, filepath.Ext(g.files[0].path))
	}
	nAuthor, nTitle, nNarrator, nYear, nIndex := parseAudiobookFolder(filepath.Base(name))
	var parts []string // the folders from the root down to the book folder
	if rel, err := filepath.Rel(root, dir); err == nil && rel != "." {
		parts = strings.Split(rel, string(filepath.Separator))
	}
	folderAuthor, series := "", ""
	switch {
	case g.dir == "": // a file in the root or in an author folder
		folderAuthor = nAuthor
		if folderAuthor == "" && len(parts) > 0 {
			folderAuthor = parts[len(parts)-1]
		}
	case len(parts) == 1: // "Автор - Название [Чтец]"
		folderAuthor = nAuthor
	default: // Автор/Название, Автор/Серия/N - Название: the author folder wins
		folderAuthor = parts[0]
		if len(parts) == 3 && nIndex > 0 {
			series = parts[1]
		}
	}
	if series == "" {
		nIndex = 0
	}

	title := first.album
	if title == "" && g.dir == "" {
		title = first.title
	}
	if title == "" {
		title = nTitle
	}
	author := first.albumArtist
	if author == "" {
		author = first.artist
	}
	if author == "" {
		author = folderAuthor
	}
	var authors []string
	if author != "" {
		authors = []string{author}
	}
	// sidecars belong to a book folder, not to an author folder
	narrator, desc := "", ""
	if g.dir != "" {
		narrator = readSidecar(di, dir, "reader.txt")
		desc = readSidecar(di, dir, "desc.txt")
	}
	composer := first.composer
	if strings.EqualFold(composer, author) {
		composer = "" // rippers often put the author there
	}
	for _, v := range []string{first.narrator, composer, nNarrator} {
		if narrator == "" {
			narrator = v
		}
	}
	year := first.year
	if year == 0 {
		year = nYear
	}
	if c := first.comment; desc == "" && utf8.RuneCountInString(c) >= 20 && !reURLOnly.MatchString(c) {
		desc = c // not "www.abook-club.ru" / "Rip by ..."
	}

	// cover: cover|folder.* or the only image of the book folder, else the
	// embedded picture
	cover := ""
	if g.dir != "" {
		cover = di.art(dir, "cover", "folder")
		if cover == "" {
			cover = onlyImage(dir)
		}
	} else {
		cover = di.art(dir, filepath.Base(name))
	}
	if cover == "" {
		for i := range tags {
			if tagged[i] && tags[i].picture != nil {
				n, err := s.saveCover(tags[i].picture)
				if err != nil {
					log.Printf("library: audiobook cover %s: %v", g.files[i].path, err)
				}
				if cover = n; cover != "" {
					break
				}
			}
		}
	}

	// chapters: from the files (stored ones while the files are unchanged),
	// else one per file; "probed" is set only after a probe without error
	perFile := fileChapters(g.files, tags)
	chapters, probed := perFile, ""
	if probing {
		var reuse []ChapterInput
		ok := false
		if have && stored.probed == pkey {
			if reuse, ok, err = s.Repo.storedChapters(ctx, stored.id, filePaths(inputs)); err != nil {
				return err
			}
		}
		if ok {
			chapters, probed = reuse, pkey
		} else if chs, err := s.embeddedChapters(ctx, inputs, perFile); err == nil {
			chapters, probed = chs, pkey
		}
	}

	if _, err := s.Repo.UpsertAudiobook(ctx, AudiobookInput{
		Dir: g.dir, Files: inputs, Title: title, Authors: authors, Narrator: narrator, Series: series, SeriesIndex: nIndex,
		Year: year, Description: desc, Cover: cover, Chapters: chapters, sig: sig, probed: probed,
	}); err != nil {
		return fmt.Errorf("audiobook %s: %w", g.files[0].path, err)
	}
	st.Audiobooks++
	st.Files += len(inputs)
	return nil
}

func filePaths(files []FileInput) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

// embeddedChapters reads the chapter list of each file; a file without one
// keeps its whole-file chapter from perFile. Any probe error is returned
// (the caller keeps the per-file chapters and probes again next scan).
func (s *Scanner) embeddedChapters(ctx context.Context, files []FileInput, perFile []ChapterInput) ([]ChapterInput, error) {
	var out []ChapterInput
	for i, f := range files {
		chs, err := s.Chapters(ctx, f.Path)
		if err != nil {
			log.Printf("library: audiobook chapters %s: %v", f.Path, err)
			return nil, err
		}
		if len(chs) == 0 {
			out = append(out, perFile[i])
			continue
		}
		for _, c := range chs {
			c.FileIndex = i
			if c.Title = strings.TrimSpace(c.Title); c.Title == "" {
				c.Title = i18n.TL(i18n.RU, "Глава %s", strconv.Itoa(len(out)+1))
			}
			out = append(out, c)
		}
	}
	return out, nil
}

// fileChapters makes one chapter per file: the title tag when the files'
// titles differ, else the name ("01 - Пролог" → «Пролог»); a bare number
// becomes «Глава N» (N from the name when the numbered files' numbers are
// unique in the book, else the position).
func fileChapters(files []audiobookFile, tags []bookTags) []ChapterInput {
	distinct := map[string]bool{}
	for _, t := range tags {
		if t.title != "" {
			distinct[t.title] = true
		}
	}
	useTags := len(distinct) > 1 || len(files) == 1 && len(distinct) == 1
	nos := make([]int, len(files))
	names := make([]string, len(files))
	numbered := true
	seenNo := map[int]bool{}
	for i, f := range files {
		st := fileStem(f.path)
		nos[i], names[i] = parseTrackFile(st)
		if nos[i] == 0 && !reNumOnly.MatchString(st) && !reTrackNo.MatchString(st) {
			continue // no number in the name ("Пролог")
		}
		if seenNo[nos[i]] {
			numbered = false
		}
		seenNo[nos[i]] = true
	}
	out := make([]ChapterInput, len(files))
	for i := range files {
		title := ""
		if useTags {
			title = tags[i].title
		}
		if title == "" {
			title = names[i]
		}
		if title == "" {
			n := i + 1
			if numbered {
				n = nos[i]
			}
			title = i18n.TL(i18n.RU, "Глава %s", strconv.Itoa(n))
		}
		out[i] = ChapterInput{Title: title, FileIndex: i}
	}
	return out
}
