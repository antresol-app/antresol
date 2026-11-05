package release

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// latinLetters spell the letters NFD does not decompose.
var latinLetters = strings.NewReplacer("æ", "ae", "Æ", "ae", "ø", "o", "Ø", "o", "œ", "oe", "Œ", "oe",
	"ß", "ss", "ð", "d", "Ð", "d", "þ", "th", "Þ", "th", "ł", "l", "Ł", "l", "đ", "d", "Đ", "d")

// cyrLatin transliterates Russian (and Ukrainian/Belarusian letters) the
// way album and artist names are usually spelled on Western releases.
var cyrLatin = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh", 'з': "z", 'и': "i",
	'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t",
	'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "",
	'э': "e", 'ю': "yu", 'я': "ya", 'і': "i", 'ї': "i", 'є': "e", 'ў': "u",
}

// FoldLatin folds a title for comparing names across spellings: lower
// case, no diacritics (Ágætis = agaetis), no apostrophes (Pepper's =
// Peppers), "&" and "and" dropped, Cyrillic transliterated (Группа крови =
// gruppa krovi), words separated by single spaces.
func FoldLatin(s string) string {
	s = latinLetters.Replace(s)
	s = norm.NFD.String(s)
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.Is(unicode.Mn, r), r == '\'', r == '’', r == '`', r == 'ʼ':
		case r == '&':
			b.WriteString(" and ")
		default:
			if t, ok := cyrLatin[r]; ok {
				b.WriteString(t)
			} else {
				b.WriteRune(r)
			}
		}
	}
	words := strings.Fields(Normalize(b.String()))
	out := words[:0]
	for _, w := range words {
		if w != "and" && w != "и" {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

// FoldMarks drops the diacritics of Latin letters (JAŸ-Z = JAY-Z,
// Måneskin = Maneskin, Rubáiyát = Rubaiyat; ø, æ, ß spelled out) and
// keeps every other script as is (й and ё stay Cyrillic letters).
func FoldMarks(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	s = norm.NFD.String(latinLetters.Replace(s))
	var b strings.Builder
	latin := false // the last letter is a Latin one: its marks go
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			if !latin {
				b.WriteRune(r)
			}
			continue
		}
		latin = unicode.Is(unicode.Latin, r)
		b.WriteRune(r)
	}
	return norm.NFC.String(b.String())
}

// HasCyrillic reports whether s has a Cyrillic letter.
func HasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// romanNumerals map to their arabic spelling ("Led Zeppelin II" = "2").
var romanNumerals = map[string]string{"i": "1", "ii": "2", "iii": "3", "iv": "4", "v": "5", "vi": "6",
	"vii": "7", "viii": "8", "ix": "9", "x": "10"}

// Numeral returns the arabic spelling of a numeral word ("ii" → "2", "4"
// → "4"; ok false for other words). Input is folded (lower case).
func Numeral(w string) (string, bool) {
	if n, ok := romanNumerals[w]; ok {
		return n, true
	}
	if w != "" && len(w) <= 2 && strings.Trim(w, "0123456789") == "" {
		return strings.TrimLeft(w, "0"), true
	}
	return "", false
}

// genericAliases are catalog notes, not names of an album (MusicBrainz
// says «live», «compilation» about many albums).
var genericAliases = map[string]bool{"live": true, "compilation": true, "studio album": true, "album": true, "debut": true,
	"debut album": true, "remaster": true, "remastered": true, "demo": true, "ep": true, "single": true}

// UsefulAliases drops empty, repeated and generic aliases.
func UsefulAliases(aliases []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range aliases {
		a = strings.TrimSpace(a)
		k := Normalize(a)
		if k == "" || genericAliases[k] || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, a)
	}
	return out
}
