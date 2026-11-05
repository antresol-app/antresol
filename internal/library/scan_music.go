package library

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/dhowden/tag"
	"github.com/dhowden/tag/mbz"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/natsort"
	"github.com/antresol-app/antresol/internal/release"
)

// musicExt reports whether p is a music track file: audioExt without .m4b
// (an .m4b is always an audiobook).
func musicExt(p string) bool {
	e := strings.ToLower(filepath.Ext(p))
	return e != ".m4b" && audioExt[e]
}

// musicSkipDirs are never a source of tracks (resource forks, NAS indexes,
// booklet scans).
var musicSkipDirs = map[string]bool{"__macosx": true, ".appledouble": true, "@eadir": true, "scans": true, "covers": true, "artwork": true}

var losslessCodecs = map[string]bool{"flac": true, "alac": true, "wav": true, "aiff": true, "ape": true, "wv": true}

var extCodec = map[string]string{
	".flac": "flac", ".mp3": "mp3", ".m4a": "aac", ".aac": "aac", ".ogg": "vorbis", ".oga": "vorbis", ".opus": "opus",
	".wav": "wav", ".aiff": "aiff", ".aif": "aiff", ".ape": "ape", ".wv": "wv",
}

// codecOf names the codec from the tag reader's file type, else the extension.
func codecOf(ft tag.FileType, path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ft {
	case tag.FLAC:
		return "flac"
	case tag.MP3:
		return "mp3"
	case tag.ALAC:
		return "alac"
	case tag.M4A:
		return "aac"
	case tag.OGG:
		if ext == ".opus" {
			return "opus"
		}
		return "vorbis"
	case tag.DSF:
		return "dsf"
	}
	if c, ok := extCodec[ext]; ok {
		return c
	}
	return strings.TrimPrefix(ext, ".")
}

type musicFile struct {
	path        string
	size, mtime int64
}

// walkMusic lists the track files and the .cue sheets (by folder) under
// root. Hidden entries, NAS/macOS junk and scan folders are skipped. An
// error means the walk is incomplete: the caller must not prune.
func walkMusic(ctx context.Context, root string) ([]musicFile, map[string][]string, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, nil, err
	}
	if !st.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a directory", root)
	}
	var out []musicFile
	cues := map[string][]string{}
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
			if p != root && (strings.HasPrefix(name, ".") || skipDirs[low] || musicSkipDirs[low]) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		if strings.EqualFold(filepath.Ext(name), ".cue") {
			cues[filepath.Dir(p)] = append(cues[filepath.Dir(p)], p)
			return nil
		}
		if !musicExt(name) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		out = append(out, musicFile{p, info.Size(), info.ModTime().Unix()})
		return nil
	})
	if err == nil {
		err = firstErr
	}
	return out, cues, err
}

// audioTags is what the scanner reads from a file's tags.
type audioTags struct {
	title, album, artist, albumArtist, genre, lyrics string
	year, track, disc                                int
	compilation                                      bool
	fileType                                         tag.FileType
	mbReleaseGroup, mbTrack                          string
	picture                                          *tag.Picture
	replayGain                                       ReplayGain
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int:
		return x != 0
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		return err == nil && n != 0 || strings.EqualFold(strings.TrimSpace(x), "true")
	case []byte:
		return len(x) > 0 && x[0] != 0 && x[0] != '0'
	case *tag.Comm:
		return x != nil && truthy(x.Text)
	}
	return false
}

// tagReads counts readTags calls (tests: an unchanged folder is not re-read).
var tagReads atomic.Int64

// readTags reads the tags of an audio file. A broken or untagged file
// returns an error; the scanner then uses the folder and file names.
func readTags(path string) (t audioTags, err error) {
	tagReads.Add(1)
	f, err := os.Open(path)
	if err != nil {
		return t, err
	}
	defer f.Close()
	defer func() {
		if r := recover(); r != nil { // the tag reader must not take the scan down
			err = fmt.Errorf("tag reader panic: %v", r)
		}
	}()
	m, err := tag.ReadFrom(f)
	if err != nil {
		return t, err
	}
	t.title, t.album, t.artist = strings.TrimSpace(m.Title()), strings.TrimSpace(m.Album()), strings.TrimSpace(m.Artist())
	t.albumArtist, t.genre = strings.TrimSpace(m.AlbumArtist()), strings.TrimSpace(m.Genre())
	t.year, t.lyrics, t.fileType, t.picture = plausibleYear(m.Year()), m.Lyrics(), m.FileType(), m.Picture()
	t.track, _ = m.Track()
	t.disc, _ = m.Disc()
	rawYear := 0
	for k, v := range m.Raw() {
		switch strings.ToLower(k) {
		case "tcmp", "tcp", "cpil", "compilation":
			t.compilation = t.compilation || truthy(v)
		case "date", "year", "tdrc", "tyer", "\u00a9day":
			if s, ok := v.(string); ok && rawYear == 0 {
				rawYear = parseYear(s)
			}
		}
	}
	if t.year == 0 { // the reader gave junk (e.g. 1 for an odd-length date): try the raw date
		t.year = rawYear
	}
	t.replayGain = replayGainOf(m.Raw())
	info := mbz.Extract(m)
	t.mbReleaseGroup = info.Get(mbz.ReleaseGroup)
	if t.mbTrack = info.Get(mbz.Recording); t.mbTrack == "" {
		t.mbTrack = info.Get(mbz.Track)
	}
	return t, nil
}

// coverName is the ArtDir name of an embedded cover: sha1 of its bytes.
func coverName(data []byte, ext string) string {
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:]) + "." + ext
}

// pictureExt returns "jpg" or "png" for an embedded picture, "" for other formats.
func pictureExt(p *tag.Picture) string {
	switch strings.ToLower(p.MIMEType) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/png":
		return "png"
	}
	switch http.DetectContentType(p.Data) {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	}
	return ""
}

// saveCover writes an embedded cover into ArtDir (once) and returns its
// name (see saveArt).
func (s *Scanner) saveCover(p *tag.Picture) (string, error) {
	if p == nil {
		return "", nil
	}
	ext := pictureExt(p)
	if ext == "" {
		return "", nil
	}
	return s.saveArt(p.Data, ext)
}

var coverStems = []string{"cover", "folder", "front"}

// sidecarCover finds the album cover file: cover|folder|front|albumart* in
// the album folder, then Covers/front*, Scans/front* (any case).
func sidecarCover(dir string) string {
	isImg := func(name string) bool {
		e := strings.ToLower(filepath.Ext(name))
		return e == ".jpg" || e == ".jpeg" || e == ".png" || e == ".webp"
	}
	list := func(d string) []string {
		es, err := os.ReadDir(d)
		if err != nil {
			return nil
		}
		var out []string
		for _, e := range es {
			if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") && isImg(e.Name()) {
				out = append(out, e.Name())
			}
		}
		natsort.Sort(out)
		return out
	}
	stem := func(n string) string { return strings.ToLower(strings.TrimSuffix(n, filepath.Ext(n))) }
	files := list(dir)
	for _, want := range coverStems {
		for _, n := range files {
			if stem(n) == want {
				return filepath.Join(dir, n)
			}
		}
	}
	for _, n := range files {
		if strings.HasPrefix(stem(n), "albumart") {
			return filepath.Join(dir, n)
		}
	}
	es, _ := os.ReadDir(dir)
	for _, sub := range []string{"covers", "scans"} {
		for _, e := range es {
			if !e.IsDir() || strings.ToLower(e.Name()) != sub {
				continue
			}
			for _, n := range list(filepath.Join(dir, e.Name())) {
				if strings.HasPrefix(stem(n), "front") {
					return filepath.Join(dir, e.Name(), n)
				}
			}
		}
	}
	return ""
}

// musicEntry is one track before grouping into albums.
type musicEntry struct {
	file          musicFile
	tags          audioTags
	tagged        bool
	album         string // album title, disc marker stripped
	disc, no      int
	title, artist string // artist: the track's own (tag or CUE), may be empty
	albumArtist   string // albumartist tag or the CUE's PERFORMER
	year          int
	genre         string
	startMs       int64
	endMs         *int64
	lyrics        string
	image         bool
}

// albumFolder is a folder holding an album's audio (disc subfolders merged in).
type albumFolder struct {
	dir   string
	files []musicFile
	disc  map[string]int // file path -> disc from its disc folder
}

// scanMusic indexes the music root: albums of tracks, CUE images, discs,
// compilations, covers and lyrics. A root that cannot be read completely
// is never pruned.
func (s *Scanner) scanMusic(ctx context.Context, st *ScanStats) error {
	root, err := filepath.Abs(s.Music)
	if err != nil {
		return err
	}
	files, cues, walkErr := walkMusic(ctx, root)
	if walkErr != nil && files == nil {
		return walkErr
	}
	folders := map[string]*albumFolder{}
	var order []string
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.path] = true
		dir, disc := filepath.Dir(f.path), 0
		if dir != root {
			if n := discFolder(filepath.Base(dir)); n > 0 && filepath.Dir(dir) != root {
				dir, disc = filepath.Dir(dir), n
			}
		}
		af := folders[dir]
		if af == nil {
			af = &albumFolder{dir: dir, disc: map[string]int{}}
			folders[dir] = af
			order = append(order, dir)
		}
		af.files = append(af.files, f)
		af.disc[f.path] = disc
	}
	sort.Strings(order)
	for _, dir := range order {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.scanAlbumFolder(ctx, root, folders[dir], cues, st); err != nil {
			return err
		}
	}
	if walkErr != nil {
		return walkErr // incomplete walk: no pruning
	}
	return s.finish(ctx, root, seen, st)
}

// imageSheet is a CUE FILE entry that splits one audio file into tracks.
type imageSheet struct {
	path string // the .cue file
	cue  Cue
	file CueFile
}

// findImages maps audio files of the folder to the CUE sheet entry that
// splits them. A sheet applies only to a file it gives several tracks
// (an image); a "tracks + .cue" rip keeps its own files.
func findImages(af *albumFolder, cues map[string][]string) map[string]imageSheet {
	have := map[string]bool{}
	dirs := map[string]bool{}
	inDir := map[string][]string{} // folder -> its audio files
	for _, f := range af.files {
		have[f.path] = true
		dirs[filepath.Dir(f.path)] = true
		inDir[filepath.Dir(f.path)] = append(inDir[filepath.Dir(f.path)], f.path)
	}
	var dirList []string
	for d := range dirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)
	out := map[string]imageSheet{}
	di := dirIndex{}
	for _, d := range dirList {
		paths := append([]string(nil), cues[d]...)
		natsort.Sort(paths)
		for _, cp := range paths {
			data, err := readSmall(cp, 1<<20)
			if err != nil {
				slog.Warn("music: cue", "file", cp, "err", err)
				continue
			}
			c, err := parseCue(data)
			if err != nil {
				slog.Warn("music: cue", "file", cp, "err", err)
				continue
			}
			for _, cf := range c.Files {
				if len(cf.Tracks) < 2 {
					continue
				}
				// by the FILE name; else the sheet's own stem (album.cue next
				// to album.flac); else the folder's only audio file for a
				// one-FILE sheet
				p := cueAudioFile(di, d, cf.Name)
				if p == "" || !have[p] {
					p = cueAudioFile(di, d, strings.TrimSuffix(filepath.Base(cp), filepath.Ext(cp))+".wav")
				}
				if (p == "" || !have[p]) && len(c.Files) == 1 && len(inDir[d]) == 1 {
					p = inDir[d][0]
				}
				if p == "" || !have[p] {
					continue
				}
				if _, dup := out[p]; !dup {
					out[p] = imageSheet{path: cp, cue: c, file: cf}
				}
			}
		}
	}
	return out
}

// readSmall reads a whole file of at most limit bytes.
func readSmall(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, errors.New("file too large")
	}
	return os.ReadFile(path)
}

func cueYear(date string) int { return parseYear(date) }

// Plausible album years; anything else from a tag is garbage (a year of 1 is
// a zero date) and means «unknown».
const (
	minYear = 1000
	maxYear = 2999
)

// plausibleYear returns y when it is a plausible year, else 0 (unknown).
func plausibleYear(y int) int {
	if y < minYear || y > maxYear {
		return 0
	}
	return y
}

// parseYear reads the year from a tag date: «1973», «1973-03-01»,
// «1973-03-01T00:00:00Z». 0 when there is no plausible one.
func parseYear(date string) int {
	date = strings.TrimSpace(date)
	if len(date) < 4 {
		return 0
	}
	for _, c := range date[:4] {
		if c < '0' || c > '9' {
			return 0
		}
	}
	if len(date) > 4 && date[4] >= '0' && date[4] <= '9' {
		return 0 // a longer number is not a date
	}
	y, _ := strconv.Atoi(date[:4])
	return plausibleYear(y)
}

// mostCommon returns the most frequent non-empty value (the first seen on a tie).
func mostCommon(vals []string) string {
	count := map[string]int{}
	best := ""
	for _, v := range vals {
		if v == "" {
			continue
		}
		count[v]++
		if count[v] > count[best] || best == "" {
			best = v
		}
	}
	return best
}

// folderSigVersion is part of every folder signature: bumping it rescans
// every album folder once, for a scan that now reads the same files
// differently. 2: CUE sheets with each pregap in the previous FILE.
// (ReplayGain tags of folders scanned before they were read are read by
// the loudness worker, gently, not by a version bump: rg_scanned.)
const folderSigVersion = 2

// folderSig is a signature of everything an album folder's scan reads:
// name, size and mtime of the files in the folder, its disc folders and its
// Covers/Scans folders (audio, .cue, .lrc, covers). "" when a folder cannot
// be listed (no skipping then).
func folderSig(af *albumFolder) string {
	dirs := map[string]bool{af.dir: true}
	for _, f := range af.files {
		dirs[filepath.Dir(f.path)] = true
	}
	if es, err := os.ReadDir(af.dir); err == nil {
		for _, e := range es {
			if l := strings.ToLower(e.Name()); e.IsDir() && (l == "covers" || l == "scans") {
				dirs[filepath.Join(af.dir, e.Name())] = true
			}
		}
	}
	var list []string
	for d := range dirs {
		list = append(list, d)
	}
	sort.Strings(list)
	h := sha1.New()
	fmt.Fprintf(h, "v%d\x00", folderSigVersion)
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
			fmt.Fprintf(h, "%s\x00%d\x00%d\x00", e.Name(), info.Size(), info.ModTime().UnixNano())
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Scanner) scanAlbumFolder(ctx context.Context, root string, af *albumFolder, cues map[string][]string, st *ScanStats) error {
	natsortFiles(af.files)
	// a folder unchanged since its last scan keeps its rows: no tags,
	// covers or sheets are read again
	sig := folderSig(af)
	if sig != "" {
		paths := make([]string, len(af.files))
		for i, f := range af.files {
			paths[i] = f.path
		}
		albums, tracks, ok, err := s.Repo.unchangedFolder(ctx, af.dir, sig, paths)
		if err != nil {
			return err
		}
		if ok {
			st.Albums += albums
			st.Tracks += tracks
			st.Files += len(paths)
			return nil
		}
	}
	images := findImages(af, cues)
	isRoot := af.dir == root
	var fArtist, fAlbum string
	var fYear int
	if !isRoot {
		fArtist, fAlbum, fYear = parseAlbumFolder(filepath.Base(af.dir))
	}
	parentArtist := ""
	if p := filepath.Dir(af.dir); !isRoot && p != root && strings.HasPrefix(p, root) {
		parentArtist = filepath.Base(p)
	}
	di := dirIndex{}

	var entries []*musicEntry
	for _, f := range af.files {
		tg, err := readTags(f.path)
		tagged := err == nil
		if err != nil && !errors.Is(err, tag.ErrNoTagsFound) {
			slog.Warn("music: tags", "file", f.path, "err", err)
		}
		stem := strings.TrimSuffix(filepath.Base(f.path), filepath.Ext(f.path))
		folderDisc := af.disc[f.path]
		if sh, ok := images[f.path]; ok {
			album := tg.album
			if album == "" {
				album = sh.cue.Title
			}
			year := tg.year
			if year == 0 {
				year = cueYear(sh.cue.Date)
			}
			genre := tg.genre
			if genre == "" {
				genre = sh.cue.Genre
			}
			_, imgDisc := stripDiscSuffix(stem)
			_, cueDisc := stripDiscSuffix(strings.TrimSuffix(filepath.Base(sh.path), filepath.Ext(sh.path)))
			disc := firstNonZero(tg.disc, sh.cue.Disc, folderDisc, imgDisc, cueDisc)
			aa := tg.albumArtist
			if aa == "" {
				aa = sh.cue.Performer
			}
			for i, ct := range sh.file.Tracks {
				e := &musicEntry{file: f, tags: tg, tagged: tagged, album: album, disc: disc, no: ct.Number,
					title: ct.Title, artist: ct.Performer, albumArtist: aa, year: year, genre: genre, startMs: ct.StartMs, image: true}
				if e.artist == "" {
					e.artist = tg.artist
				}
				if e.no == 0 {
					e.no = i + 1
				}
				if e.title == "" {
					e.title = i18n.TL(i18n.RU, "Трек %d", e.no)
				}
				if i+1 < len(sh.file.Tracks) {
					end := sh.file.Tracks[i+1].StartMs
					e.endMs = &end
				}
				entries = append(entries, e)
			}
			continue
		}
		nameDisc, nameNo, nameTitle := parseTrackFileDisc(stem)
		e := &musicEntry{file: f, tags: tg, tagged: tagged, album: tg.album, artist: tg.artist, albumArtist: tg.albumArtist,
			year: tg.year, genre: tg.genre, title: tg.title, no: tg.track}
		e.disc = firstNonZero(tg.disc, folderDisc)
		if e.no == 0 {
			e.no = nameNo
		}
		if e.disc == 0 && nameDisc > 0 && tg.track == 0 {
			e.disc = nameDisc
		}
		e.title = cleanTagTitle(e.title, e.no)
		if e.title == "" {
			e.title = nameTitle
		}
		if e.title == "" {
			if e.no > 0 {
				e.title = i18n.TL(i18n.RU, "Трек %d", e.no)
			} else {
				e.title = stem
			}
		}
		if p := di.find(filepath.Dir(f.path), stem+".lrc"); p != "" {
			if data, err := readSmall(p, 1<<20); err == nil {
				e.lyrics = decodeText(data)
			} else {
				slog.Warn("music: lyrics", "file", p, "err", err)
			}
		}
		if e.lyrics == "" {
			e.lyrics = tg.lyrics
		}
		entries = append(entries, e)
	}

	// album title (disc marker stripped) and groups by normalised title,
	// editions folded: a track tagged «Группа крови (Remastered)» added to
	// the folder of «Группа крови» joins that album (which keeps its title,
	// so its item and match key stay)
	type group struct {
		title   string
		entries []*musicEntry
	}
	var groups []*group
	byKey := map[string]*group{}
	had, err := s.Repo.albumTitlesIn(ctx, af.dir)
	if err != nil {
		return err
	}
	kept := map[string]string{} // folded key → title of an album item already in the folder
	for _, t := range had {
		if k := editionKey(t); kept[k] == "" {
			kept[k] = t
		}
	}
	for _, e := range entries {
		album := e.album
		if album == "" {
			album = fAlbum
		}
		if album == "" {
			album = "Без альбома"
		}
		name, sfxDisc := stripDiscSuffix(album)
		if name == "" {
			name = album
		}
		if e.disc == 0 {
			e.disc = sfxDisc
		}
		if e.disc == 0 {
			e.disc = 1
		}
		e.album = name
		k := editionKey(name)
		g := byKey[k]
		if g == nil {
			title := name
			if t := kept[k]; t != "" {
				title = t
			}
			g = &group{title: title}
			byKey[k] = g
			groups = append(groups, g)
		}
		g.entries = append(g.entries, e)
	}

	cover := fileURL(sidecarCover(af.dir))
	var albumIDs []int64
	for _, g := range groups {
		sort.SliceStable(g.entries, func(i, j int) bool {
			a, b := g.entries[i], g.entries[j]
			if a.disc != b.disc {
				return a.disc < b.disc
			}
			if a.no != b.no {
				return a.no < b.no
			}
			if a.file.path != b.file.path {
				return natsort.Less(a.file.path, b.file.path)
			}
			return a.startMs < b.startMs
		})
		albumArtist, compilation := chooseAlbumArtist(g.entries)
		if albumArtist == "" {
			albumArtist = firstNonEmpty(fArtist, parentArtist, unknownArtist)
		}
		var years []string
		for _, e := range g.entries {
			if y := plausibleYear(e.year); y != 0 {
				years = append(years, strconv.Itoa(y))
			}
		}
		albumYear, _ := strconv.Atoi(mostCommon(years))
		if albumYear == 0 {
			albumYear = plausibleYear(fYear)
		}
		gc := cover
		if gc == "" {
			for _, e := range g.entries {
				if e.tags.picture == nil {
					continue
				}
				name, err := s.saveCover(e.tags.picture)
				if err != nil {
					slog.Warn("music: cover", "file", e.file.path, "err", err)
				}
				if name != "" {
					gc = name
					break
				}
			}
		}
		var in []TrackInput
		filesSeen := map[string]bool{}
		for _, e := range g.entries {
			year := e.year
			if year == 0 {
				year = albumYear
			}
			codec := codecOf(e.tags.fileType, e.file.path)
			in = append(in, TrackInput{
				File:     FileInput{Path: e.file.path, Size: e.file.size, Mtime: e.file.mtime},
				AlbumDir: af.dir, Album: g.title, AlbumArtist: albumArtist, Artist: firstNonEmpty(e.artist, albumArtist),
				Title: e.title, Genre: e.genre, Year: year, Disc: e.disc, Track: e.no,
				StartMs: e.startMs, EndMs: e.endMs, Codec: codec, Lossless: losslessCodecs[codec], Compilation: compilation,
				MBReleaseGroupID: e.tags.mbReleaseGroup, MBTrackID: e.tags.mbTrack, Lyrics: e.lyrics, Cover: gc,
				ReplayGain: trackReplayGain(e),
			})
			filesSeen[e.file.path] = true
		}
		id, err := s.Repo.UpsertAlbumTracks(ctx, albumArtist, in)
		if err != nil {
			return err
		}
		albumIDs = append(albumIDs, id)
		st.Albums++
		st.Tracks += len(in)
		st.Files += len(filesSeen)
	}
	if sig == "" {
		return nil
	}
	return s.Repo.setFolderSig(ctx, albumIDs, sig)
}

// chooseAlbumArtist picks the album artist: the albumartist tag (or the
// image's PERFORMER), else the one artist all tracks share (the full string:
// "Simon & Garfunkel"), else — counting distinct primary artists, guests
// cut off ("Eminem feat. Dido" counts as Eminem) — the one primary artist,
// else "Разные исполнители" for a flagged compilation (TCMP/cpil/
// COMPILATION) or 3+ primary artists, else the most frequent primary. A
// primary artist is named by a full track artist string, never by the cut
// key; "" when nothing names an artist. compilation reports whether the
// album is a compilation.
func chooseAlbumArtist(entries []*musicEntry) (artist string, compilation bool) {
	var aas, fulls, keys []string
	fullDistinct := map[string]bool{}
	keyDistinct := map[string]bool{}
	for _, e := range entries {
		aas = append(aas, e.albumArtist)
		k := ""
		if e.artist != "" {
			fullDistinct[release.Normalize(e.artist)] = true
			k = release.Normalize(primaryArtist(e.artist))
			keyDistinct[k] = true
		}
		fulls = append(fulls, e.artist)
		keys = append(keys, k)
		compilation = compilation || e.tags.compilation
	}
	if aa := mostCommon(aas); aa != "" {
		if isVarious(aa) {
			return VariousArtists, true
		}
		return aa, compilation
	}
	switch {
	case len(fullDistinct) == 1:
		return mostCommon(fulls), compilation
	case len(keyDistinct) == 1:
		return nameForKey(fulls, keys, mostCommon(keys)), compilation
	case compilation || len(keyDistinct) >= 3:
		return VariousArtists, true
	}
	return nameForKey(fulls, keys, mostCommon(keys)), compilation
}

// nameForKey returns the full track artist string naming the primary artist
// key: one equal to the key ("Eminem"), else one without a guest marker
// ("Simon & Garfunkel" over "Simon & Garfunkel feat. X"), else any; the
// most frequent of the first non-empty tier.
func nameForKey(fulls, keys []string, key string) string {
	var exact, plain, other []string
	for i, f := range fulls {
		if f == "" || keys[i] != key {
			continue
		}
		switch {
		case release.Normalize(f) == key:
			exact = append(exact, f)
		case !reGuest.MatchString(f):
			plain = append(plain, f)
		default:
			other = append(other, f)
		}
	}
	return firstNonEmpty(mostCommon(exact), mostCommon(plain), mostCommon(other))
}

func firstNonZero(v ...int) int {
	for _, x := range v {
		if x != 0 {
			return x
		}
	}
	return 0
}

func firstNonEmpty(v ...string) string {
	for _, x := range v {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}

func natsortFiles(fs []musicFile) {
	sort.SliceStable(fs, func(i, j int) bool { return natsort.Less(fs[i].path, fs[j].path) })
}

// editionKey is the key album titles are grouped by within a folder:
// normalized, edition suffixes («(Remastered 2012)», «[Deluxe Edition]»)
// dropped; a version («(Live)») stays apart.
func editionKey(title string) string { return release.Normalize(release.StripEditions(title)) }

// trackReplayGain is the ReplayGain of an entry's track. A CUE image's
// tags describe the whole disc: its track gain is the disc's (the album
// gain when there is none), and each track's own loudness is measured.
func trackReplayGain(e *musicEntry) ReplayGain {
	rg := e.tags.replayGain
	if !e.image {
		return rg
	}
	out := ReplayGain{AlbumGain: rg.AlbumGain, AlbumPeak: rg.AlbumPeak}
	if out.AlbumGain == nil {
		out.AlbumGain, out.AlbumPeak = rg.TrackGain, rg.TrackPeak
	}
	return out
}
