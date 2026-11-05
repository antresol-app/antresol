// Package natsort orders names the way people read them: digit runs compare
// as numbers ("2.mp3" < "10.mp3", "v2" < "v10"), letters ignore case and ё
// equals е.
package natsort

import (
	"sort"
	"strings"
	"unicode/utf8"
)

func fold(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "ё", "е")
}

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

// Less reports whether a sorts before b. Names equal under the rules above
// fall back to a plain byte comparison, so the order is total.
func Less(a, b string) bool {
	if c := compare(fold(a), fold(b)); c != 0 {
		return c < 0
	}
	return a < b
}

func compare(a, b string) int {
	i, j, tie := 0, 0, 0
	for i < len(a) && j < len(b) {
		if isDigit(a[i]) && isDigit(b[j]) {
			si, sj := i, j
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			na := strings.TrimLeft(a[si:i], "0")
			nb := strings.TrimLeft(b[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) - len(nb)
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			// equal numbers: fewer leading zeros first ("1" < "01"),
			// unless the rest of the names decides
			if d := (i - si) - (j - sj); d != 0 && tie == 0 {
				tie = d
			}
			continue
		}
		ra, wa := utf8.DecodeRuneInString(a[i:])
		rb, wb := utf8.DecodeRuneInString(b[j:])
		if ra != rb {
			if ra < rb {
				return -1
			}
			return 1
		}
		i += wa
		j += wb
	}
	if d := (len(a) - i) - (len(b) - j); d != 0 {
		return d
	}
	return tie
}

// Sort sorts s in natural order.
func Sort(s []string) {
	sort.SliceStable(s, func(i, j int) bool { return Less(s[i], s[j]) })
}
