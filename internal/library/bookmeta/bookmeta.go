// Package bookmeta reads the metadata of e-book files: EPUB (OPF), FB2 and
// FB2 inside a ZIP. MOBI, AZW3 and PDF give an empty Meta: the scanner
// then names them from the file and folder names.
package bookmeta

import (
	"fmt"
	"html"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Meta is what a book file says about itself. Empty fields are unknown.
type Meta struct {
	Title       string
	Authors     []string
	Series      string
	SeriesIndex float64
	Lang        string
	Description string // plain text, HTML removed
	ISBN        string
	Publisher   string
	Year        int
	Cover       []byte
	CoverType   string // extension without the dot: jpg|png|webp|gif
}

// Limits against oversized or hostile files (variables for tests).
var (
	maxXML   int64 = 16 << 20  // container.xml, OPF; the part of an FB2 up to the end of <description>
	maxCover int64 = 20 << 20  // a decoded cover
	maxFB2   int64 = 256 << 20 // an FB2 file (plain or unzipped)
)

var reFB2Zip = regexp.MustCompile(`(?i)\.fb2\.zip$`)

// Read returns the metadata of the book at path, chosen by its extension:
// .epub, .fb2, .fb2.zip (the first .fb2 in the archive); .mobi, .azw3 and
// .pdf give an empty Meta and no error. A broken file returns an error.
func Read(path string) (m Meta, err error) {
	defer func() {
		if r := recover(); r != nil { // a reader bug must not take the scan down
			m, err = Meta{}, fmt.Errorf("bookmeta: panic: %v", r)
		}
	}()
	if reFB2Zip.MatchString(path) {
		return readFB2Zip(path)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub":
		return readEPUB(path)
	case ".fb2":
		return readFB2File(path)
	case ".mobi", ".azw3", ".pdf":
		return Meta{}, nil
	}
	return Meta{}, fmt.Errorf("bookmeta: unsupported format %s", filepath.Ext(path))
}

// imageType maps a MIME type, then the content, then a file name to
// jpg|png|webp|gif; "" if it is none of them.
func imageType(mime string, data []byte, name string) string {
	if t := mimeExt(mime); t != "" {
		return t
	}
	if t := mimeExt(http.DetectContentType(data)); t != "" {
		return t
	}
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "jpg", "jpeg":
		return "jpg"
	case "png", "webp", "gif":
		return strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	}
	return ""
}

func mimeExt(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	switch mime {
	case "image/jpeg", "image/jpg", "image/pjpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return ""
}

var (
	reBlockTag = regexp.MustCompile(`(?i)<\s*(?:br|/p|/div|/h[1-6]|/li|empty-line)\b[^>]*>`)
	reTag      = regexp.MustCompile(`<[^>]*>`)
)

// plainText turns an HTML or FB2 fragment into plain text: tags removed,
// entities decoded, a line per paragraph, spaces collapsed.
func plainText(s string) string {
	s = reBlockTag.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

var reYear = regexp.MustCompile(`\b(1[0-9]{3}|20[0-9]{2})\b`)

// yearOf finds a year in a date such as "1967-01-01", "1869" or "May 1999".
func yearOf(s string) int {
	if m := reYear.FindString(s); m != "" {
		y, _ := strconv.Atoi(m)
		return y
	}
	return 0
}

func parseIndex(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(s, ",", ".")), 64)
	if err != nil || f < 0 {
		return 0
	}
	return f
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }
