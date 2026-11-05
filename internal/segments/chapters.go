package segments

import (
	"strings"
	"unicode"

	"github.com/antresol-app/antresol/internal/media"
)

// ChaptersVersion is the version of the chapter reading; a file read by an
// older one is read again.
const ChaptersVersion = 1

// chapterKind names the segment a chapter title marks ("" none). Words are
// matched whole, case-insensitively, in Russian and English; the recap is
// checked first, then the intro («Opening Credits» is the intro), then the
// credits.
func chapterKind(title string) string {
	words := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	has := func(match func(w string) bool) bool {
		for _, w := range words {
			if match(w) {
				return true
			}
		}
		return false
	}
	switch {
	case has(func(w string) bool {
		return strings.HasPrefix(w, "recap") || w == "previously" || w == "ранее" || strings.HasPrefix(w, "предыдущ")
	}):
		return Recap
	case has(func(w string) bool {
		return strings.HasPrefix(w, "intro") || w == "opening" || w == "op" || w == "заставка" || strings.HasPrefix(w, "вступлени")
	}):
		return Intro
	case has(func(w string) bool {
		return w == "credits" || w == "credit" || w == "ending" || w == "ed" || w == "outro" || w == "титры"
	}):
		return Credits
	}
	return ""
}

// FromChapters turns a file's chapters into segments: chapters of one kind
// in a row join into one; the recap and the intro must start in the first
// half of the file, the credits in the second (durationMs 0: not checked);
// the first of each kind is kept. A chapter without an end runs to the next
// one, the last to the end of the file.
func FromChapters(chs []media.Chapter, durationMs int64) []Segment {
	type span struct {
		kind       string
		start, end int64
	}
	var spans []span
	for i, c := range chs {
		end := durationMs
		if c.EndMs != nil {
			end = *c.EndMs
		} else if i+1 < len(chs) {
			end = chs[i+1].StartMs
		}
		k := chapterKind(c.Title)
		if n := len(spans); n > 0 && k != "" && spans[n-1].kind == k && spans[n-1].end >= c.StartMs-1000 {
			spans[n-1].end = max(spans[n-1].end, end)
			continue
		}
		spans = append(spans, span{k, c.StartMs, end})
	}
	var out []Segment
	seen := map[string]bool{}
	for _, s := range spans {
		if s.kind == "" || seen[s.kind] || s.end <= s.start {
			continue
		}
		if durationMs > 0 {
			firstHalf := s.start < durationMs/2
			if (s.kind == Credits) == firstHalf {
				continue
			}
		}
		seen[s.kind] = true
		out = append(out, Segment{Kind: s.kind, Source: SourceChapters, StartMs: s.start, EndMs: s.end})
	}
	return out
}
