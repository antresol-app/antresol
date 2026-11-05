package release

import (
	"slices"
	"strings"
)

// EditionWords mark a title suffix that names an edition, not a version:
// «(Remastered 2011)», « - 2011 Remaster», «(Deluxe Edition)», «[Explicit]».
var EditionWords = []string{"remaster", "remastered", "deluxe", "explicit", "clean", "edition", "anniversary", "expanded",
	"bonus", "mono", "stereo", "ремастер", "ремастеринг"}

// VersionWords mark a recording (or album) that is not the studio one:
// live, karaoke, remix, «концерт»….
var VersionWords = []string{"live", "karaoke", "instrumental", "acoustic", "cover", "remix", "demo", "unplugged", "session",
	"rehearsal", "концерт", "вживую", "живое", "караоке", "минус", "акустика", "акустическая", "ремикс", "демо"}

// StripEditions removes edition suffixes from a song or album title,
// last first. A suffix that names a version stays whole, edition words
// or not («(Live - 2021 Remaster)», « - Acoustic / Remastered»).
func StripEditions(s string) string {
	s = strings.TrimSpace(s)
	for {
		head, tail, ok := LastSuffix(s)
		if !ok || !HasWord(tail, EditionWords) || HasWord(tail, VersionWords) {
			return s
		}
		s = head
	}
}

// LastSuffix splits off the title's last «(…)», «[…]» or « - …».
func LastSuffix(s string) (head, tail string, ok bool) {
	if n := len(s); n > 0 && (s[n-1] == ')' || s[n-1] == ']') {
		open := byte('(')
		if s[n-1] == ']' {
			open = '['
		}
		if i := strings.LastIndexByte(s, open); i > 0 {
			return strings.TrimSpace(s[:i]), s[i+1 : n-1], true
		}
	}
	for _, sep := range []string{" - ", " – ", " — "} {
		if i := strings.LastIndex(s, sep); i > 0 {
			return strings.TrimSpace(s[:i]), s[i+len(sep):], true
		}
	}
	return s, "", false
}

// HasWord: one of the words of s (Normalize) is in list.
func HasWord(s string, list []string) bool {
	for _, w := range strings.Fields(Normalize(s)) {
		if slices.Contains(list, w) {
			return true
		}
	}
	return false
}
