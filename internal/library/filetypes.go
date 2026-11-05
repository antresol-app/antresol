package library

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/antresol-app/antresol/internal/comics"
)

// File types of the media scanners, for the code that lays files out for
// them (acquire's organize): it must agree with what they index exactly.

// IsMusicFile reports whether the music scanner indexes the file as a
// track (audio, but never an .m4b).
func IsMusicFile(name string) bool { return musicExt(name) }

// IsAudiobookFile reports whether the audiobook scanner indexes the file.
func IsAudiobookFile(name string) bool { return audioExt[strings.ToLower(filepath.Ext(name))] }

// IsBookFile reports whether the book scanner indexes the file (EPUB, FB2,
// FB2.ZIP, MOBI, AZW3, PDF; no DJVU).
func IsBookFile(name string) bool { return bookExts[bookExt(name)] }

// BookExt is the extension of a book file, ".fb2.zip" for a zipped FB2,
// lower-cased.
func BookExt(name string) string { return bookExt(name) }

// IsPicture reports whether the scanners read the file as artwork.
func IsPicture(name string) bool {
	return slices.Contains(imgExts, strings.ToLower(filepath.Ext(name)))
}

// IsMusicSkipDir reports whether the music scanner never looks for tracks
// in a folder of this name (Covers, Scans, Artwork).
func IsMusicSkipDir(name string) bool { return musicSkipDirs[strings.ToLower(name)] }

// ParseAlbumFolder reads artist, album and year from an album folder name
// the way the music scanner does: "1985 - Мания величия" → "", "Мания
// величия", 1985.
func ParseAlbumFolder(name string) (artist, album string, year int) { return parseAlbumFolder(name) }

// IsComicVolume reports whether the comics scanner indexes the file as a
// volume: a comic archive, but not the second or a later part of a
// multi-part RAR.
func IsComicVolume(name string) bool {
	base := filepath.Base(name)
	return comics.IsArchive(base) && !reRarPart.MatchString(base)
}

// AlbumTagInfo is the album a music file names.
type AlbumTagInfo struct {
	Title, Artist string // the album and its artist (album artist, CUE PERFORMER)
	Year          int
	Compilation   bool // tagged as a compilation (various artists)
}

// AlbumTag reads the album a music file names, the way the music scanner
// does: a CUE sheet's TITLE, PERFORMER and REM DATE, or an audio file's
// album, album artist and year tags. ok is false for a file it cannot read
// or one that names no album.
func AlbumTag(path string) (AlbumTagInfo, bool) {
	if strings.EqualFold(filepath.Ext(path), ".cue") {
		data, err := readSmall(path, 1<<20)
		if err != nil {
			return AlbumTagInfo{}, false
		}
		c, err := parseCue(data)
		if err != nil || strings.TrimSpace(c.Title) == "" {
			return AlbumTagInfo{}, false
		}
		return AlbumTagInfo{Title: strings.TrimSpace(c.Title), Artist: strings.TrimSpace(c.Performer), Year: cueYear(c.Date)}, true
	}
	t, err := readTags(path)
	if err != nil || t.album == "" {
		return AlbumTagInfo{}, false
	}
	artist := t.albumArtist
	if artist == "" {
		artist = t.artist
	}
	return AlbumTagInfo{Title: t.album, Artist: artist, Year: t.year, Compilation: t.compilation}, true
}
