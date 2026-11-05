package comics

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/htmlindex"
)

// ComicInfo is what the scanner and the reader use of a ComicInfo.xml
// (the Anansi schema Kavita and Komga read). Number is the issue/chapter
// number, Volume the volume number; 0 means not set.
type ComicInfo struct {
	Series, Title, Summary, Writer, LanguageISO, Manga, AgeRating string
	Number, Volume                                                float64
	Year, PageCount                                               int
}

// Direction is the reading direction the file asks for: "rtl" for
// Manga=YesAndRightToLeft, "ltr" for Manga=No, "" otherwise (Yes, Unknown,
// unset: the library default applies, right to left).
func (c ComicInfo) Direction() string {
	switch strings.ToLower(strings.TrimSpace(c.Manga)) {
	case "yesandrighttoleft":
		return "rtl"
	case "no":
		return "ltr"
	}
	return ""
}

// Rating is AgeRating on the RU scale (see ageRating).
func (c ComicInfo) Rating() string { return ageRating(c.AgeRating) }

// ageRating maps a ComicInfo AgeRating to the canonical RU rating
// (0+ 6+ 12+ 16+ 18+); unknown, pending or unset → "".
func ageRating(s string) string {
	switch strings.ToLower(strings.Join(strings.Fields(s), " ")) {
	case "adults only 18+", "x18+", "r18+":
		return "18+"
	case "mature 17+", "m":
		return "16+"
	case "teen", "t":
		return "12+"
	case "everyone 10+":
		return "6+"
	case "everyone", "g", "early childhood":
		return "0+"
	}
	return ""
}

const maxInfoBytes = 1 << 20

var errNotComicInfo = errors.New("comics: not a ComicInfo document")

// parseComicInfo reads a ComicInfo.xml leniently: unknown elements are
// ignored, a number that does not parse is left 0, a declared non-UTF-8
// charset (windows-1251 …) is decoded.
func parseComicInfo(b []byte) (ComicInfo, error) {
	var raw struct {
		XMLName                                                       xml.Name
		Series, Title, Summary, Writer, LanguageISO, Manga, AgeRating string
		Number, Volume, Year, PageCount                               string
	}
	d := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	d.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		enc, err := htmlindex.Get(label)
		if err != nil {
			return nil, fmt.Errorf("comics: charset %q: %w", label, err)
		}
		return enc.NewDecoder().Reader(in), nil
	}
	if err := d.Decode(&raw); err != nil {
		return ComicInfo{}, fmt.Errorf("comics: ComicInfo.xml: %w", err)
	}
	if !strings.EqualFold(raw.XMLName.Local, "ComicInfo") {
		return ComicInfo{}, errNotComicInfo
	}
	t := strings.TrimSpace
	return ComicInfo{
		Series: t(raw.Series), Title: t(raw.Title), Summary: t(raw.Summary), Writer: t(raw.Writer),
		LanguageISO: t(raw.LanguageISO), Manga: t(raw.Manga), AgeRating: t(raw.AgeRating),
		Number: leadingNumber(raw.Number), Volume: leadingNumber(raw.Volume),
		Year: int(leadingNumber(raw.Year)), PageCount: int(leadingNumber(raw.PageCount)),
	}, nil
}

// leadingNumber parses the number a value starts with ("12", "12.5",
// "12,5", "12a" → 12); anything else is 0.
func leadingNumber(s string) float64 {
	s = strings.TrimSpace(s)
	end, dot := 0, false
	for end < len(s) {
		c := s[end]
		if c >= '0' && c <= '9' {
			end++
			continue
		}
		if (c == '.' || c == ',') && !dot && end > 0 && end+1 < len(s) && s[end+1] >= '0' && s[end+1] <= '9' {
			dot = true
			end++
			continue
		}
		break
	}
	v, err := strconv.ParseFloat(strings.Replace(s[:end], ",", ".", 1), 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
