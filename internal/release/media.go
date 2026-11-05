package release

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// AudioInfo is what a music or audiobook release name says about its files.
type AudioInfo struct {
	Format      string // FLAC | ALAC | APE | WV | MP3 | AAC | M4B ("" = not named)
	Bitrate     int    // kbps, 0 = not named
	VBR         string // a LAME preset: V0 | V1 | V2 … ("" = not named)
	Lossless    bool
	Image       bool // one file per disc split by a CUE sheet ("image+.cue")
	Discography bool // a discography or a collection of albums
	Year        int  // the (first) release year; 0 for a year range
	// Reissue: the name marks a later edition (Remaster, Reissue, Deluxe,
	// Anniversary, … Edition, Box Set, a label: Переиздание, Ремастер,
	// Moroz Records), so its year may be the edition's.
	Reissue bool
}

// BookInfo is what an e-book, comic or manga release name says.
type BookInfo struct {
	Formats    []string // in the order named: EPUB FB2 PDF DJVU MOBI AZW3 CBZ CBR CB7 DOC DOCX RTF TXT
	Lang       string   // a lang catalog code (ru, en, ja, uk, …) | raw (untranslated scans) | "" = not named
	Collection bool     // collected works, a series pack
}

// tokens splits a name into upper-cased words of letters and digits.
func tokens(name string) []string {
	return strings.FieldsFunc(strings.ToUpper(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// audioFormats in the order of preference when a name lists several.
var audioFormats = []struct{ token, format string }{
	{"FLAC", "FLAC"}, {"ALAC", "ALAC"}, {"APE", "APE"}, {"WV", "WV"}, {"WAVPACK", "WV"},
	{"M4B", "M4B"}, {"AAC", "AAC"}, {"M4A", "AAC"}, {"MP3", "MP3"},
}

var (
	reBitrate   = regexp.MustCompile(`(?i)(\d{2,4})\s*(?:kbps|kbit|kb/s|кбит|кб/с)`)
	reYearRange = regexp.MustCompile(`(?:^|\D)(?:19|20)\d\d\s*[-–—]\s*(?:19|20)\d\d(?:\D|$)`)
	reAudioYear = regexp.MustCompile(`(?:^|\D)((?:19|20)\d\d)(?:\D|$)`)
	// a discography or a set of albums; «Коллекционное издание» and
	// «Collector's Edition» are one album
	reDiscography = regexp.MustCompile(`(?i)дискограф|discograph|антолог|anthology|\bbox\s*set\b|коллекция\s+альбомов|collection\s+of\s+albums|\bcollection\s*[\(\[]?\s*(?:19|20)\d\d\s*[-–]`)
	// a later edition of an album, or a label's release year after the
	// title («(Moroz Records) 1996»)
	reReissue    = regexp.MustCompile(`(?i)remaster|re-?issue|re-?release|deluxe|anniversary|edition|expanded|box\s*set|immersion|\brecords\b|moroz|cd-maximum|mfsl|sacd|shm-?cd|japan|vinyl|\blp\b|24\s*-?bit|24[-/]96|24[-/]192|мелоди|переизда|ремастер|издание|юбилейн|рекордс|винил`)
	reYear4      = regexp.MustCompile(`(?:19|20)\d\d`)
	reCollection = regexp.MustCompile(`(?i)собрани\p{L}*\s+сочинени|полное\s+собрание|сборник|omnibus|антолог|\sв\s+\d+\s+(?:том|книг)|\d+\s+(?:томах|книгах)`)
)

// ParseAudio reads format, bitrate, year and the image/discography marks
// from a music or audiobook release name: "Кино - Группа крови - 1988,
// FLAC (tracks+.cue), lossless", "Ария - Дискография (1985-2014) [MP3,
// 320 kbps]".
func ParseAudio(name string) AudioInfo {
	var a AudioInfo
	seen := map[string]bool{}
	for _, t := range tokens(name) {
		seen[t] = true
	}
	for _, f := range audioFormats {
		if seen[f.token] {
			a.Format = f.format
			break
		}
	}
	switch a.Format {
	case "FLAC", "ALAC", "APE", "WV":
		a.Lossless = true
	}
	if seen["LOSSLESS"] {
		a.Lossless = true
	}
	a.Image = seen["IMAGE"] || seen["ОБРАЗ"]
	if m := reBitrate.FindStringSubmatch(name); m != nil {
		a.Bitrate, _ = strconv.Atoi(m[1])
	} else if a.Format == "MP3" {
		// «[MP3, 320]»: a bare common bitrate next to the format
		for _, b := range []string{"320", "256", "224", "192", "160", "128"} {
			if seen[b] {
				a.Bitrate, _ = strconv.Atoi(b)
				break
			}
		}
	}
	for _, v := range []string{"V0", "V1", "V2", "V3"} {
		if seen[v] {
			a.VBR = v
			break
		}
	}
	a.Discography = reDiscography.MatchString(name)
	a.Reissue = reReissue.MatchString(name)
	if !reYearRange.MatchString(name) {
		if m := reAudioYear.FindStringSubmatch(name); m != nil {
			a.Year, _ = strconv.Atoi(m[1])
		}
	}
	return a
}

// Label is the format badge of a music release: «FLAC», «MP3 320», «MP3
// V0», «MP3», «Lossless» ("" when the name tells nothing).
func (a AudioInfo) Label() string {
	f := a.Format
	if f == "" && a.Lossless {
		return "Lossless"
	}
	if f == "" && (a.Bitrate > 0 || a.VBR != "") {
		f = "MP3" // «320Kbps» alone: an MP3 release
	}
	switch {
	case f == "":
		return ""
	case a.Lossless:
		return f
	case a.VBR != "":
		return f + " " + a.VBR
	case a.Bitrate > 0:
		return f + " " + strconv.Itoa(a.Bitrate)
	}
	return f
}

// IsReissue reports whether a name marks a later edition (see
// AudioInfo.Reissue).
func IsReissue(name string) bool { return reReissue.MatchString(name) }

// AudioYears returns the years a release name gives: single years and
// year ranges ("1983-2016", as [from, to]). A year is 19xx or 20xx that is
// not part of a longer number ("24/96" and "320" are not years).
func AudioYears(name string) (years []int, ranges [][2]int) {
	type hit struct{ y, start, end int }
	var hits []hit
	for _, m := range reYear4.FindAllStringIndex(name, -1) {
		if m[0] > 0 && isDigit(name[m[0]-1]) || m[1] < len(name) && isDigit(name[m[1]]) {
			continue
		}
		y, _ := strconv.Atoi(name[m[0]:m[1]])
		hits = append(hits, hit{y, m[0], m[1]})
	}
	for i := 0; i < len(hits); i++ {
		if i+1 < len(hits) && strings.Trim(name[hits[i].end:hits[i+1].start], " -–—") == "" &&
			strings.ContainsAny(name[hits[i].end:hits[i+1].start], "-–—") && hits[i+1].y >= hits[i].y {
			ranges = append(ranges, [2]int{hits[i].y, hits[i+1].y})
			i++
			continue
		}
		years = append(years, hits[i].y)
	}
	return years, ranges
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

var bookFormats = map[string]string{
	"EPUB": "EPUB", "FB2": "FB2", "PDF": "PDF", "DJVU": "DJVU", "DJV": "DJVU",
	"MOBI": "MOBI", "AZW3": "AZW3", "CBZ": "CBZ", "CBR": "CBR", "CB7": "CB7",
	"DOC": "DOC", "DOCX": "DOCX", "RTF": "RTF", "TXT": "TXT",
}

var bookLangs = map[string]string{
	"RUS": "ru", "RU": "ru", "РУС": "ru", "РУССКИЙ": "ru", "RUSSIAN": "ru",
	"РУССКОМ": "ru", // «на русском»
	"ENG":     "en", "EN": "en", "ENGLISH": "en", "АНГЛИЙСКОМ": "en", "JAP": "ja", "JPN": "ja", "JP": "ja",
	"UKR": "uk", "УКР": "uk", "УКРАИНСКИЙ": "uk", "GER": "de", "DEU": "de", "FRA": "fr", "FRE": "fr",
	"SPA": "es", "ESP": "es", "ITA": "it", "POL": "pl", "POR": "pt", "CHI": "zh", "CHN": "zh", "KOR": "ko",
}

// ParseBook reads the formats, the language and the collection mark from a
// book, comic or manga release name: "Булгаков М. - Мастер и Маргарита
// [1967, FB2, EPUB, RUS]", "Собрание сочинений в 10 томах".
func ParseBook(name string) BookInfo {
	var b BookInfo
	raw := false
	seen := map[string]bool{}
	for _, t := range tokens(name) {
		if f, ok := bookFormats[t]; ok && !seen[f] {
			seen[f] = true
			b.Formats = append(b.Formats, f)
		}
		if l, ok := bookLangs[t]; ok && b.Lang == "" {
			b.Lang = l
		}
		raw = raw || t == "RAW"
	}
	if raw && b.Lang != "ru" && b.Lang != "en" {
		b.Lang = "raw"
	}
	b.Collection = reCollection.MatchString(name)
	return b
}

// BookLang is the language of a book or audiobook release: the language
// its name states («[RUS]», «[ENG]», «на русском», «Russian»), else
// Cyrillic letters in the title mean Russian, else "" (unknown: a Latin
// name may be an English book or a transliterated Russian one). Never
// "raw" (that is a manga mark).
func BookLang(name string) string {
	if l := ParseBook(name).Lang; l != "" && l != "raw" {
		return l
	}
	for _, r := range name {
		if unicode.Is(unicode.Cyrillic, r) {
			return "ru"
		}
	}
	return ""
}

// langTags are the name tags ParseBook reads back for each code (a source
// that states the language elsewhere puts its tag on the title).
var langTags = map[string]string{
	"ru": "RUS", "en": "ENG", "ja": "JPN", "uk": "UKR", "de": "GER", "fr": "FRA",
	"es": "SPA", "it": "ITA", "pl": "POL", "pt": "POR", "zh": "CHI", "ko": "KOR",
}

// LangTag is the release-name tag of a language code ("ru" → "RUS"), "" if none.
func LangTag(code string) string { return langTags[code] }

// LangCode folds a language code of source metadata (OPDS dc:language
// «ru», «rus», «ru-RU»; «en», «eng», «en-US») to a code of the language
// catalog (internal/lang); "" when it is another or none.
func LangCode(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if i := strings.IndexAny(c, "-_"); i > 0 {
		c = c[:i]
	}
	switch c {
	case "ru", "rus":
		return "ru"
	case "en", "eng":
		return "en"
	case "ja", "jpn":
		return "ja"
	case "uk", "ukr":
		return "uk"
	case "de", "deu", "ger":
		return "de"
	case "fr", "fra", "fre":
		return "fr"
	case "es", "spa":
		return "es"
	case "it", "ita":
		return "it"
	case "pt", "por":
		return "pt"
	case "pl", "pol":
		return "pl"
	case "zh", "zho", "chi":
		return "zh"
	case "ko", "kor":
		return "ko"
	}
	return ""
}
