package library

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/antresol-app/antresol/internal/i18n"
)

// reGenericChapter is a chapter title that only numbers the chapter, the
// way encoders name M4B chapters: "Chapter 12", "Part 2 Chapter 1",
// "Track 05", "05". Anything with words of its own is a real title.
var reGenericChapter = regexp.MustCompile(`(?i)^(?:part\s*(\d+)\s*[-.,:]?\s*)?(?:(?:chapter|ch\.?|track)\s*)?(\d+)?$`)

var (
	// reAudioExt: a title that still carries its file's extension.
	reAudioExt = regexp.MustCompile(`(?i)\.(?:mp3|m4a|m4b|mp4|aac|ogg|oga|opus|flac|wav|wma|ape|wv)$`)
	// reSlug: a number and words joined by -, _ or . without a space
	// («01-starik-i-more»).
	reSlug = regexp.MustCompile(`^\d+(?:[-_.][\p{L}\d]+){2,}$`)
	// reRoman: a Roman numeral (lower case).
	reRoman = regexp.MustCompile(`^m{0,3}(?:cm|cd|d?c{0,3})(?:xc|xl|l?x{0,3})(?:ix|iv|v?i{0,3})$`)
)

// isFileName: the title is a file's name rather than words for people: it
// keeps the extension, joins its words with underscores, or is a numbered
// slug.
func isFileName(t string) bool {
	if reAudioExt.MatchString(t) {
		return true
	}
	if n := strings.Count(t, "_"); n >= 2 || n == 1 && !strings.ContainsFunc(t, unicode.IsSpace) {
		return true
	}
	return reSlug.MatchString(t) && strings.ContainsFunc(t, unicode.IsLetter)
}

// titleKey is a title's words alone: lower case, digits and punctuation
// gone, one space between them.
func titleKey(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) }), " ")
}

// RealChapterTitles reports whether a book's own chapter titles tell the
// listener anything: at least half of them are words of their own, not
// numbers, labels such as «Глава» or "Track", file names, or the book's
// name numbered. book is the book's title; stems are the names of its
// files without extensions (a book's only chapter named after its file says
// nothing either).
func RealChapterTitles(book string, titles, stems []string) bool {
	if len(titles) == 0 {
		return false
	}
	if len(titles) == 1 {
		only := titleKey(titles[0])
		for _, s := range stems {
			if _, name := parseTrackFile(s); only == titleKey(s) || only == titleKey(name) {
				return false
			}
		}
	}
	bookKey := titleKey(book)
	names := map[string]bool{}
	real := 0
	for _, t := range titles {
		if t = strings.TrimSpace(t); t == "" || isFileName(t) {
			continue
		}
		key := titleKey(t)
		if bookKey != "" {
			key = strings.TrimSpace(strings.ReplaceAll(" "+key+" ", " "+bookKey+" ", " "))
		}
		generic := true
		for _, w := range strings.Fields(key) {
			generic = generic && (genericWords[w] || reRoman.MatchString(w))
		}
		if !generic {
			real++
			names[key] = true
		}
	}
	if real > 1 && len(names) == 1 {
		return false // one name for every chapter, only numbered
	}
	return real*2 >= len(titles)
}

// DisplayChapterTitle is the title the UI shows for chapter idx (0-based)
// in the interface language lang (i18n code): generic English numbering
// becomes «Часть 2 · Глава 1» ("Part 2 · Chapter 1"), an empty title or a
// file's name «Глава N» ("Chapter N"); a title the scanner made in Russian
// («Глава 3») is localized; real titles stay as they are.
func DisplayChapterTitle(lang, title string, idx int) string {
	t := strings.TrimSpace(title)
	m := reGenericChapter.FindStringSubmatch(t)
	if m == nil {
		if isFileName(t) {
			return i18n.TL(lang, "Глава %s", strconv.Itoa(idx+1))
		}
		return i18n.LocalizeL(lang, title)
	}
	part, ch := m[1], m[2]
	switch {
	case part != "" && ch != "":
		return i18n.TL(lang, "Часть %s · Глава %s", trimZeros(part), trimZeros(ch))
	case part != "":
		return i18n.TL(lang, "Часть %s", trimZeros(part))
	case ch != "":
		return i18n.TL(lang, "Глава %s", trimZeros(ch))
	case t == "":
		return i18n.TL(lang, "Глава %s", strconv.Itoa(idx+1))
	}
	return title // "Chapter" with no number
}

func trimZeros(n string) string {
	if v, err := strconv.Atoi(n); err == nil {
		return strconv.Itoa(v)
	}
	return n
}
