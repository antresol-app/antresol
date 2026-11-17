package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/dhowden/tag"
	"github.com/dhowden/tag/mbz"
)

// The scanners' file rules and tag reading, apart from the scanners so
// that code laying files out for them (filetypes.go) can be built
// without the scanners themselves.

// musicExt reports whether p is a music track file: audioExt without .m4b
// (an .m4b is always an audiobook).
func musicExt(p string) bool {
	e := strings.ToLower(filepath.Ext(p))
	return e != ".m4b" && audioExt[e]
}

// musicSkipDirs are never a source of tracks (resource forks, NAS indexes,
// booklet scans).
var musicSkipDirs = map[string]bool{"__macosx": true, ".appledouble": true, "@eadir": true, "scans": true, "covers": true, "artwork": true}

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

// bookExts lists the e-book formats the book scanner indexes (DJVU,
// comics and everything else are left alone).
var bookExts = map[string]bool{".epub": true, ".fb2": true, ".fb2.zip": true, ".mobi": true, ".azw3": true, ".pdf": true}

// bookExt returns ".fb2.zip" for an FB2 archive, else the lower-cased
// extension.
func bookExt(name string) string {
	if reBookFB2Zip.MatchString(name) {
		return ".fb2.zip"
	}
	return strings.ToLower(filepath.Ext(name))
}

var reBookFB2Zip = regexp.MustCompile(`(?i)\.fb2\.zip$`)

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

var reRarPart = regexp.MustCompile(`(?i)\.part0*([2-9]|[1-9]\d+)\.rar$`)
