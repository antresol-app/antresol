package library

import (
	"strings"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/release"
)

// cyrLatin spells Russian letters in Latin, the way the common
// romanizations agree on; nameKey folds what they disagree on.
var cyrLatin = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh", 'з': "z",
	'и': "i", 'й': "i", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r",
	'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sh",
	'ъ': "", 'ы': "i", 'ь': "", 'э': "e", 'ю': "iu", 'я': "ia",
}

// latinFold: the spellings one Russian sound has in English, German or
// Polish romanizations, folded to one ("Tchekhov" = "Chekhov" = «Чехов»).
var latinFold = strings.NewReplacer("shch", "sh", "tch", "ch", "kh", "h", "ts", "c", "tz", "c", "cz", "ch", "sz", "sh",
	"ck", "k", "ph", "f", "x", "ks", "w", "v", "y", "i", "j", "i")

// nameKey is a name word in a script-blind spelling: Cyrillic is
// romanized, the romanizations' variants are folded, a vowel's i-glide is
// dropped (Dostoyevsky, Dostoevsky, «Достоевский» are "dostoevski") and
// doubled letters are single. Two spellings of one surname in Latin and in
// Cyrillic get the same key.
func nameKey(word string) string {
	var b strings.Builder
	for _, r := range word {
		if s, ok := cyrLatin[r]; ok {
			b.WriteString(s)
		} else {
			b.WriteRune(r)
		}
	}
	s := latinFold.Replace(b.String())
	vowel := func(c byte) bool { return strings.IndexByte("aeoui", c) >= 0 }
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 'i' && ((i > 0 && vowel(s[i-1])) || (i+1 < len(s) && vowel(s[i+1]) && s[i+1] != 'i')) {
			continue // the glide of «ой», «ье», "yo", "ie"
		}
		if len(out) > 0 && out[len(out)-1] == c {
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

// nameKeys are the keys of the name words of 3+ letters (initials do not
// tell people apart).
func nameKeys(names []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		for _, w := range strings.Fields(release.Normalize(n)) {
			if utf8.RuneCountInString(w) >= 3 {
				if k := nameKey(w); len(k) >= 3 {
					out[k] = true
				}
			}
		}
	}
	return out
}

// SameAuthor reports whether one of names (the ways another catalog writes
// a book's authors) shares a name word with one of authors, in any script:
// "Mikhail Bulgakov" is «Булгаков М. А.». No name on either side is no
// match (unlike sameAuthor: a caller that found a book by a translated
// title needs the author to agree).
func SameAuthor(names, authors []string) bool {
	want := nameKeys(names)
	if len(want) == 0 {
		return false
	}
	for k := range nameKeys(authors) {
		if want[k] {
			return true
		}
	}
	return false
}
