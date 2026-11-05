package library

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/i18n"
)

// reGenericChapter is a chapter title that only numbers the chapter, the
// way encoders name M4B chapters: "Chapter 12", "Part 2 Chapter 1",
// "Track 05", "05". Anything with words of its own is a real title.
var reGenericChapter = regexp.MustCompile(`(?i)^(?:part\s*(\d+)\s*[-.,:]?\s*)?(?:(?:chapter|ch\.?|track)\s*)?(\d+)?$`)

// DisplayChapterTitle is the title the UI shows for chapter idx (0-based)
// in the interface language lang (i18n code): generic English numbering
// becomes «Часть 2 · Глава 1» ("Part 2 · Chapter 1"), an empty title
// «Глава N» ("Chapter N"); a title the scanner made in Russian («Глава 3»)
// is localized; real titles stay as they are.
func DisplayChapterTitle(lang, title string, idx int) string {
	t := strings.TrimSpace(title)
	m := reGenericChapter.FindStringSubmatch(t)
	if m == nil {
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
