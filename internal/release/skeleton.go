package release

import "strings"

// skeletonDigraphs spell the sounds both alphabets write differently
// alike; upper-case letters are the folded sounds (folded words are lower
// case, so they never clash).
var skeletonDigraphs = strings.NewReplacer(
	"shch", "S", "tch", "C", "ch", "C", "sh", "S", "zh", "Z", "kh", "h", "ph", "f", "ck", "k",
	"c", "k", "q", "k", "x", "ks", "j", "dZ",
)

// Skeleton is the consonant skeleton of a folded (lower-case) name word,
// the same for its Latin and Cyrillic spellings: «roberts» and «робертс»
// are «rbrts», «tolkien» and «толкин» «tlkn», «chernyi» and «чёрный»
// «Crn». Vowels, «h», «w» and «y» are dropped (Rowling = Роулинг) and
// doubled consonants are one; keepW spells «w» as «v» instead (Weir =
// Вейер).
func Skeleton(word string, keepW bool) string {
	var lat strings.Builder
	for _, r := range word {
		if l, ok := cyrLatin[r]; ok {
			lat.WriteString(l)
		} else {
			lat.WriteRune(r)
		}
	}
	var b strings.Builder
	var last rune
	for _, r := range skeletonDigraphs.Replace(lat.String()) {
		if keepW && r == 'w' {
			r = 'v'
		}
		switch r {
		case 'a', 'e', 'i', 'o', 'u', 'y', 'h', 'w':
			continue
		}
		if r != last {
			b.WriteRune(r)
		}
		last = r
	}
	return b.String()
}
