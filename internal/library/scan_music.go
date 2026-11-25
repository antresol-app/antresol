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

	"github.com/dhowden/tag"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/natsort"
	"github.com/antresol-app/antresol/internal/release"
)

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

// walkMusic lists the track files and the .cue sheets (by folder) under
// root. Hidden entries, NAS/macOS junk and scan folders are skipped. An
// error means the walk is incomplete: the caller must not prune.
func walkMusic(ctx context.Context, root string) ([]walkedFile, map[string][]string, error) {
	var out []walkedFile
	cues := map[string][]string{}
	err := walkTree(ctx, root, func(low string) bool { return skipDirs[low] || musicSkipDirs[low] },
		func(p string, d fs.DirEntry) bool {
			if strings.EqualFold(filepath.Ext(d.Name()), ".cue") {
				cues[filepath.Dir(p)] = append(cues[filepath.Dir(p)], p)
				return false
			}
			return musicExt(d.Name())
		},
		func(p string, info fs.FileInfo) { out = append(out, walkedFile{p, info.Size(), info.ModTime().Unix()}) })
	return out, cues, err
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
	stem := func(p string) string {
		n := filepath.Base(p)
		return strings.ToLower(strings.TrimSuffix(n, filepath.Ext(n)))
	}
	first := func(images []string, ok func(stem string) bool) string {
		for _, p := range images {
			if ok(stem(p)) {
				return p
			}
		}
		return ""
	}
	images := listDirArt(dir).images
	for _, want := range coverStems {
		if p := first(images, func(s string) bool { return s == want }); p != "" {
			return p
		}
	}
	if p := first(images, func(s string) bool { return strings.HasPrefix(s, "albumart") }); p != "" {
		return p
	}
	es, _ := os.ReadDir(dir)
	for _, sub := range []string{"covers", "scans"} {
		for _, e := range es {
			if !e.IsDir() || strings.ToLower(e.Name()) != sub {
				continue
			}
			if p := first(listDirArt(filepath.Join(dir, e.Name())).images, func(s string) bool { return strings.HasPrefix(s, "front") }); p != "" {
				return p
			}
		}
	}
	return ""
}

// musicEntry is one track before grouping into albums.
type musicEntry struct {
	file          walkedFile
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
	cover         string // the name of the file's embedded picture, saved (readEntries); "" when it has none or the folder has a cover file
}

// albumFolder is a folder holding an album's audio (disc subfolders merged in).
type albumFolder struct {
	dir   string
	files []walkedFile
	disc  map[string]int // file path -> disc from its disc folder
}

// scanMusic indexes the music root: albums of tracks, CUE images, discs,
// compilations, covers and lyrics. A root that cannot be read completely
// is never pruned.
func (s *Scanner) scanMusic(ctx context.Context, st *ScanStats) error {
	return s.eachRoot(ctx, KindFolderMusic, st, s.scanMusicIn)
}

// scanMusicIn is scanMusic of one of the kind's folders.
func (s *Scanner) scanMusicIn(ctx context.Context, dir string, st *ScanStats) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	files, cues, walkErr := walkMusic(ctx, root)
	if walkErr != nil && files == nil {
		return walkErr
	}
	folders := map[string]*albumFolder{}
	var order []string
	for _, f := range files {
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
	return s.finish(ctx, root, seenPaths(files), st)
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
	ls := dirLister{}
	dirs := map[string]bool{af.dir: true}
	for _, f := range af.files {
		dirs[filepath.Dir(f.path)] = true
	}
	if es, err := ls.read(af.dir); err == nil {
		for _, e := range es {
			if l := strings.ToLower(e.Name()); e.IsDir() && (l == "covers" || l == "scans") {
				dirs[filepath.Join(af.dir, e.Name())] = true
			}
		}
	}
	list := make([]string, 0, len(dirs))
	for d := range dirs {
		list = append(list, d)
	}
	sort.Strings(list)
	h := sha1.New()
	fmt.Fprintf(h, "v%d\x00", folderSigVersion)
	if !hashDirs(h, list, ls) {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Scanner) scanAlbumFolder(ctx context.Context, root string, af *albumFolder, cues map[string][]string, st *ScanStats) error {
	natsortFiles(af.files)
	sig := folderSig(af)
	if unchanged, err := s.skipUnchangedFolder(ctx, af, sig, st); unchanged || err != nil {
		return err
	}
	st.Changed++
	names := albumFolderNames(root, af)
	sidecar := fileURL(sidecarCover(af.dir))
	entries := s.readEntries(af, findImages(af, cues), sidecar)
	groups, err := s.groupByEdition(ctx, af, names.album, entries)
	if err != nil {
		return err
	}
	var albumIDs []int64
	for _, g := range groups {
		id, err := s.upsertGroup(ctx, af, g, names, sidecar, st)
		if err != nil {
			return err
		}
		albumIDs = append(albumIDs, id)
	}
	if sig == "" {
		return nil
	}
	return s.Repo.setFolderSig(ctx, albumIDs, sig)
}

// skipUnchangedFolder reports whether a folder unchanged since its last scan
// keeps its rows: no tags, covers or sheets are read again.
func (s *Scanner) skipUnchangedFolder(ctx context.Context, af *albumFolder, sig string, st *ScanStats) (bool, error) {
	if sig == "" {
		return false, nil
	}
	paths := make([]string, len(af.files))
	for i, f := range af.files {
		paths[i] = f.path
	}
	albums, tracks, ok, err := s.Repo.unchangedFolder(ctx, af.dir, sig, paths)
	if err != nil || !ok {
		return false, err
	}
	st.Albums += albums
	st.Tracks += tracks
	st.Files += len(paths)
	return true, nil
}

// folderNames is what the names of an album folder say: its artist, album
// and year ("Artist - Album (Year)"), and the folder above it as the artist.
type folderNames struct {
	artist, album, parentArtist string
	year                        int
}

func albumFolderNames(root string, af *albumFolder) folderNames {
	var n folderNames
	if af.dir == root {
		return n
	}
	n.artist, n.album, n.year = parseAlbumFolder(filepath.Base(af.dir))
	if p := filepath.Dir(af.dir); p != root && strings.HasPrefix(p, root) {
		n.parentArtist = filepath.Base(p)
	}
	return n
}

// readEntries reads the tags of the folder's files into track entries (a CUE
// image gives one per track of its sheet). A folder with a cover file
// (sidecar) needs no embedded picture; else each file's picture is saved at
// once and the entry keeps its name: a box set of a hundred tracks does not
// hold a hundred pictures until the folder is done.
func (s *Scanner) readEntries(af *albumFolder, images map[string]imageSheet, sidecar string) []*musicEntry {
	var entries []*musicEntry
	di := dirIndex{}
	for _, f := range af.files {
		tg, err := readTags(f.path)
		tagged := err == nil
		if err != nil && !errors.Is(err, tag.ErrNoTagsFound) {
			slog.Warn("music: tags", "file", f.path, "err", err)
		}
		cover := ""
		if sidecar == "" && tg.picture != nil {
			var cerr error
			if cover, cerr = s.saveCover(tg.picture); cerr != nil {
				slog.Warn("music: cover", "file", f.path, "err", cerr)
			}
		}
		tg.picture = nil
		if sh, ok := images[f.path]; ok {
			entries = append(entries, imageEntries(f, tg, tagged, cover, sh, af.disc[f.path])...)
			continue
		}
		entries = append(entries, trackEntry(f, tg, tagged, cover, af.disc[f.path], di))
	}
	return entries
}

// imageEntries are the track entries of a CUE image file: one per track of
// its sheet, the sheet filling what the tags leave empty.
func imageEntries(f walkedFile, tg audioTags, tagged bool, cover string, sh imageSheet, folderDisc int) []*musicEntry {
	stem := strings.TrimSuffix(filepath.Base(f.path), filepath.Ext(f.path))
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
	var out []*musicEntry
	for i, ct := range sh.file.Tracks {
		e := &musicEntry{file: f, tags: tg, tagged: tagged, cover: cover, album: album, disc: disc, no: ct.Number,
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
		out = append(out, e)
	}
	return out
}

// trackEntry is the entry of a file that is one track: its tags, the file
// name where the tags are empty, the .lrc beside it where it has no lyrics.
func trackEntry(f walkedFile, tg audioTags, tagged bool, cover string, folderDisc int, di dirIndex) *musicEntry {
	stem := strings.TrimSuffix(filepath.Base(f.path), filepath.Ext(f.path))
	nameDisc, nameNo, nameTitle := parseTrackFileDisc(stem)
	e := &musicEntry{file: f, tags: tg, tagged: tagged, cover: cover, album: tg.album, artist: tg.artist, albumArtist: tg.albumArtist,
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
	return e
}

// albumGroup is the tracks of one album of a folder.
type albumGroup struct {
	title   string
	entries []*musicEntry
}

// groupByEdition groups the entries by normalised album title (disc marker
// stripped), editions folded: a track tagged «Группа крови (Remastered)»
// added to the folder of «Группа крови» joins that album (which keeps its
// title, so its item and match key stay). folderAlbum is the album named by
// the folder, for tracks with no album tag.
func (s *Scanner) groupByEdition(ctx context.Context, af *albumFolder, folderAlbum string, entries []*musicEntry) ([]*albumGroup, error) {
	var groups []*albumGroup
	byKey := map[string]*albumGroup{}
	had, err := s.Repo.albumTitlesIn(ctx, af.dir)
	if err != nil {
		return nil, err
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
			album = folderAlbum
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
			g = &albumGroup{title: title}
			byKey[k] = g
			groups = append(groups, g)
		}
		g.entries = append(g.entries, e)
	}
	return groups, nil
}

// upsertGroup writes one album of the folder with its tracks, in disc and
// track order, and returns the album's id.
func (s *Scanner) upsertGroup(ctx context.Context, af *albumFolder, g *albumGroup, names folderNames, sidecar string, st *ScanStats) (int64, error) {
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
		albumArtist = firstNonEmpty(names.artist, names.parentArtist, unknownArtist)
	}
	albumYear := albumYearOf(g.entries, names.year)
	in, files := trackInputs(af, g, albumArtist, compilation, albumYear, groupCover(sidecar, g.entries))
	id, err := s.Repo.UpsertAlbumTracks(ctx, albumArtist, in)
	if err != nil {
		return 0, err
	}
	st.Albums++
	st.Tracks += len(in)
	st.Files += files
	return id, nil
}

// albumYearOf is the year most of the tracks carry, else the folder's.
func albumYearOf(entries []*musicEntry, folderYear int) int {
	var years []string
	for _, e := range entries {
		if y := plausibleYear(e.year); y != 0 {
			years = append(years, strconv.Itoa(y))
		}
	}
	if y, _ := strconv.Atoi(mostCommon(years)); y != 0 {
		return y
	}
	return plausibleYear(folderYear)
}

// groupCover is the album's cover: the folder's cover file, else the
// embedded picture of the first track that has one.
func groupCover(sidecar string, entries []*musicEntry) string {
	if sidecar != "" {
		return sidecar
	}
	for _, e := range entries {
		if e.cover != "" {
			return e.cover
		}
	}
	return ""
}

// trackInputs are the tracks of a group for UpsertAlbumTracks, and the
// number of distinct files they come from.
func trackInputs(af *albumFolder, g *albumGroup, albumArtist string, compilation bool, albumYear int, cover string) ([]TrackInput, int) {
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
			MBReleaseGroupID: e.tags.mbReleaseGroup, MBTrackID: e.tags.mbTrack, Lyrics: e.lyrics, Cover: cover,
			ReplayGain: trackReplayGain(e),
		})
		filesSeen[e.file.path] = true
	}
	return in, len(filesSeen)
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

func natsortFiles(fs []walkedFile) {
	sort.SliceStable(fs, func(i, j int) bool { return natsort.Less(fs[i].path, fs[j].path) })
}

// editionKey is the key album titles are grouped by within a folder:
// normalized, edition suffixes («(Remastered 2012)», «[Deluxe Edition]»)
// dropped; a version («(Live)») stays apart.
func editionKey(title string) string { return release.Normalize(release.StripEditions(title)) }

// ReadReplayGain reads the ReplayGain tags of an audio file as the scanner
// does (image: a CUE image, whose tags are its disc's: the album gain only).
func ReadReplayGain(path string, image bool) (ReplayGain, error) {
	t, err := readTags(path)
	if err != nil {
		return ReplayGain{}, err
	}
	return trackReplayGain(&musicEntry{tags: t, image: image}), nil
}

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
