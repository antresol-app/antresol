package library

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// fixTagText repairs Cyrillic tag text written in Windows-1251 by Russian
// rippers: raw CP1251 bytes (ID3v1, invalid UTF-8) and CP1251 bytes read
// as ISO-8859-1 (ID3v2 encoding 0, "Âîéíà" for «Война»). Text that is
// still unreadable afterwards (replacement or control characters) is
// returned as "", so it never overrides a folder name. The music scanner
// can use it for its tags too.
func fixTagText(s string) string {
	s = strings.Trim(s, " \t\r\n\x00")
	if s == "" {
		return ""
	}
	if !utf8.ValidString(s) {
		s = decodeText([]byte(s))
	} else if b, ok := latin1Cyrillic(s); ok {
		out, _ := charmap.Windows1251.NewDecoder().Bytes(b)
		s = string(out)
	}
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return ""
		}
	}
	return strings.TrimSpace(s)
}

// commonRussian are the most frequent Russian letters.
const commonRussian = "оеаинтсрвлкОЕАИНТСРВЛК"

// latin1Cyrillic reports whether s looks like CP1251 text decoded as
// Latin-1 and returns the CP1251 bytes. It must be plausibly Russian:
// only runes ≤ U+00FF, none from the Latin-1 punctuation/sign range
// U+00A0–U+00BF except ¨/¸ (Ё/ё); at least 70% of the letters in
// U+00C0–U+00FF; the decoded text mostly (≥70%) Cyrillic letters with at
// least one (two from 6 letters on) common Russian letter. So «Été»,
// «Ölçü», «2×2», «Café», «Mötley Crüe» stay as they are.
func latin1Cyrillic(s string) ([]byte, bool) {
	high, letters := 0, 0
	b := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF || r >= 0xA0 && r <= 0xBF && r != 0xA8 && r != 0xB8 {
			return nil, false
		}
		switch {
		case r >= 0xC0 || r == 0xA8 || r == 0xB8:
			high++
			letters++
		case unicode.IsLetter(r):
			letters++
		}
		b = append(b, byte(r))
	}
	if high == 0 || high*10 < letters*7 {
		return nil, false
	}
	dec, err := charmap.Windows1251.NewDecoder().Bytes(b)
	if err != nil {
		return nil, false
	}
	cyr, all, common := 0, 0, 0
	for _, r := range string(dec) {
		if !unicode.IsLetter(r) {
			continue
		}
		all++
		if unicode.Is(unicode.Cyrillic, r) {
			cyr++
		}
		if strings.ContainsRune(commonRussian, r) {
			common++
		}
	}
	need := 1
	if all >= 6 {
		need = 2
	}
	return b, cyr*10 >= all*7 && common >= need
}
